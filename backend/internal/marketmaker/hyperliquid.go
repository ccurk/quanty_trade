package marketmaker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hyperliquid perpetuals adapter. Chosen over spot venues for two structural
// reasons measured on gate: (1) base maker fee is 1.5bps vs gate's 10bps — a
// 17bps saving per round trip, with NO market-maker application needed; (2) it is
// a perp, so we can quote both sides symmetrically instead of only being able to
// sell inventory we already hold (on gate that forced one-sided quoting and a
// steady inventory build whenever the venue traded at a premium).
//
// Auth is an Ethereum key signing EIP-712 payloads (see hyperliquid_sign.go),
// not an HMAC API key. The key is read from env (never from the repo/config file).
const (
	hlMainnetAPI = "https://api.hyperliquid.xyz"
	hlTestnetAPI = "https://api.hyperliquid-testnet.xyz"
	// hlMinNotional: exchange rejects orders under $10 ("Order must have minimum
	// value of $10"). Verified against the exchange endpoint's documented error.
	hlMinNotional = 10.0
	// hlPerpMaxDecimals: price may carry at most (MAX_DECIMALS - szDecimals)
	// decimals, MAX_DECIMALS = 6 for perps.
	hlPerpMaxDecimals = 6
)

func init() {
	RegisterExec("hyperliquid", func(cfg ExecConfig) (ExecExchange, error) {
		base := strings.TrimRight(cfg.BaseURL, "/")
		if base == "" {
			base = hlMainnetAPI
		}
		// 私钥与地址只从环境变量取,绝不落配置文件(配置文件会进镜像/仓库)。
		priv := strings.TrimSpace(os.Getenv("MM_HL_PRIVATE_KEY"))
		if priv == "" {
			priv = strings.TrimSpace(cfg.APISecret)
		}
		addr := strings.TrimSpace(os.Getenv("MM_HL_ACCOUNT_ADDRESS"))
		if addr == "" {
			addr = strings.TrimSpace(cfg.APIKey)
		}
		return &HyperliquidExchange{
			baseURL: base,
			privKey: priv,
			account: addr,
			mainnet: !strings.Contains(base, "testnet"),
			http:    &http.Client{Timeout: 10 * time.Second},
			meta:    map[string]hlAssetMeta{},
		}, nil
	})
}

type hlAssetMeta struct {
	Index      int
	SzDecimals int
}

type HyperliquidExchange struct {
	baseURL string
	privKey string
	account string
	mainnet bool
	http    *http.Client

	metaMu   sync.RWMutex
	meta     map[string]hlAssetMeta
	metaAt   time.Time
	metaOnce sync.Mutex
}

func (e *HyperliquidExchange) Name() string { return "hyperliquid" }

// SupportsShort: perps — we can open a short without holding the asset.
func (e *HyperliquidExchange) SupportsShort() bool { return true }

// hlCoin maps the engine's symbol conventions (SOL_USDT / SOLUSDT / SOL-USDT /
// SOL) to Hyperliquid's bare coin name ("SOL"). HL perps settle in USDC.
func hlCoin(symbol string) string {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "-", "_")
	if i := strings.Index(s, "_"); i > 0 {
		return s[:i]
	}
	for _, suf := range []string{"USDT", "USDC", "USD"} {
		if len(s) > len(suf) && strings.HasSuffix(s, suf) {
			return strings.TrimSuffix(s, suf)
		}
	}
	return s
}

// post sends a JSON body to an info/exchange endpoint.
func (e *HyperliquidExchange) post(path string, payload interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, e.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("hyperliquid %s -> %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// loadMeta caches the asset universe. The ARRAY INDEX is the asset id used in
// order/cancel actions — it is positional, so the whole universe must be fetched.
func (e *HyperliquidExchange) loadMeta() error {
	e.metaMu.RLock()
	fresh := len(e.meta) > 0 && time.Since(e.metaAt) < time.Hour
	e.metaMu.RUnlock()
	if fresh {
		return nil
	}
	e.metaOnce.Lock()
	defer e.metaOnce.Unlock()
	e.metaMu.RLock()
	fresh = len(e.meta) > 0 && time.Since(e.metaAt) < time.Hour
	e.metaMu.RUnlock()
	if fresh {
		return nil
	}

	raw, err := e.post("/info", map[string]string{"type": "meta"})
	if err != nil {
		return err
	}
	var m struct {
		Universe []struct {
			Name       string `json:"name"`
			SzDecimals int    `json:"szDecimals"`
			IsDelisted bool   `json:"isDelisted"`
		} `json:"universe"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	next := make(map[string]hlAssetMeta, len(m.Universe))
	for i, a := range m.Universe {
		if a.IsDelisted {
			continue // 已下架的币仍占索引位,但不可交易
		}
		next[strings.ToUpper(a.Name)] = hlAssetMeta{Index: i, SzDecimals: a.SzDecimals}
	}
	if len(next) == 0 {
		return fmt.Errorf("hyperliquid: meta 为空")
	}
	e.metaMu.Lock()
	e.meta, e.metaAt = next, time.Now()
	e.metaMu.Unlock()
	return nil
}

func (e *HyperliquidExchange) assetMeta(symbol string) (hlAssetMeta, error) {
	if err := e.loadMeta(); err != nil {
		return hlAssetMeta{}, err
	}
	coin := hlCoin(symbol)
	e.metaMu.RLock()
	a, ok := e.meta[coin]
	e.metaMu.RUnlock()
	if !ok {
		return hlAssetMeta{}, fmt.Errorf("hyperliquid: 未知交易对 %s (coin=%s)", symbol, coin)
	}
	return a, nil
}

func (e *HyperliquidExchange) FetchBookTicker(symbol string) (BookTicker, error) {
	raw, err := e.post("/info", map[string]string{"type": "l2Book", "coin": hlCoin(symbol)})
	if err != nil {
		return BookTicker{}, err
	}
	var b struct {
		Coin   string `json:"coin"`
		Levels [][]struct {
			Px string `json:"px"`
			Sz string `json:"sz"`
		} `json:"levels"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return BookTicker{}, err
	}
	// levels[0] = bids (descending), levels[1] = asks (ascending).
	if len(b.Levels) < 2 || len(b.Levels[0]) == 0 || len(b.Levels[1]) == 0 {
		return BookTicker{}, fmt.Errorf("hyperliquid: %s 盘口为空", symbol)
	}
	return BookTicker{
		Symbol: b.Coin,
		BidPx:  atofP(b.Levels[0][0].Px), BidQty: atofP(b.Levels[0][0].Sz),
		AskPx: atofP(b.Levels[1][0].Px), AskQty: atofP(b.Levels[1][0].Sz),
		Ts: time.Now(),
	}, nil
}

// SymbolFilter derives rounding rules from szDecimals. Note HL also caps prices at
// 5 significant figures, which is price-dependent and therefore cannot be expressed
// as a fixed tick; PlaceLimit applies that rule (hlNormalizePrice) as the final step.
func (e *HyperliquidExchange) SymbolFilter(symbol string) (SymbolFilter, error) {
	a, err := e.assetMeta(symbol)
	if err != nil {
		return SymbolFilter{}, err
	}
	priceDecimals := hlPerpMaxDecimals - a.SzDecimals
	if priceDecimals < 0 {
		priceDecimals = 0
	}
	return SymbolFilter{
		BaseAsset:   hlCoin(symbol),
		QuoteAsset:  "USDC",
		TickSize:    math.Pow(10, -float64(priceDecimals)),
		StepSize:    math.Pow(10, -float64(a.SzDecimals)),
		MinNotional: hlMinNotional,
	}, nil
}

// exchangeAction signs and posts an L1 action, returning the raw response.
func (e *HyperliquidExchange) exchangeAction(action mpValue, nonce uint64) ([]byte, error) {
	if strings.TrimSpace(e.privKey) == "" {
		return nil, fmt.Errorf("hyperliquid: 未配置私钥(MM_HL_PRIVATE_KEY)")
	}
	sig, err := hlSignAction(e.privKey, action, nonce, "", e.mainnet)
	if err != nil {
		return nil, err
	}
	// action 必须与签名时的字段顺序一致 —— 用同一棵 mpMap 转 JSON 保证不会漂移。
	return e.post("/exchange", map[string]interface{}{
		"action":       mpToAny(action),
		"nonce":        nonce,
		"signature":    sig,
		"vaultAddress": nil,
	})
}

// mpToAny converts the ordered msgpack tree into a plain value for JSON encoding.
// JSON key order does not affect the signature (that was computed over msgpack),
// but reusing one tree removes any chance the signed and sent payloads diverge.
func mpToAny(v mpValue) interface{} {
	switch t := v.(type) {
	case *mpMap:
		m := make(map[string]interface{}, len(t.keys))
		for i, k := range t.keys {
			m[k] = mpToAny(t.vals[i])
		}
		return m
	case mpArr:
		out := make([]interface{}, 0, len(t))
		for _, e := range t {
			out = append(out, mpToAny(e))
		}
		return out
	case mpStr:
		return string(t)
	case mpInt:
		return int64(t)
	case mpBool:
		return bool(t)
	}
	return nil
}

// hlOrderStatus parses the exchange reply. NOTE: the envelope reports
// status:"ok" even when the order was rejected — the real outcome is inside
// response.data.statuses[0]. Treating the envelope as success silently loses every
// rejection (the same class of bug as gate's two-frame ack).
func hlOrderStatus(raw []byte) (oid string, err error) {
	var r struct {
		Status   string `json:"status"`
		Response struct {
			Type string `json:"type"`
			Data struct {
				Statuses []struct {
					Resting *struct {
						OID int64 `json:"oid"`
					} `json:"resting"`
					Filled *struct {
						OID     int64  `json:"oid"`
						TotalSz string `json:"totalSz"`
						AvgPx   string `json:"avgPx"`
					} `json:"filled"`
					Error string `json:"error"`
				} `json:"statuses"`
			} `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("hyperliquid: 响应解析失败: %v (%s)", err, truncate(string(raw), 200))
	}
	if !strings.EqualFold(r.Status, "ok") {
		return "", fmt.Errorf("hyperliquid: %s", truncate(string(raw), 200))
	}
	if len(r.Response.Data.Statuses) == 0 {
		return "", fmt.Errorf("hyperliquid: 响应无 statuses (%s)", truncate(string(raw), 200))
	}
	st := r.Response.Data.Statuses[0]
	switch {
	case strings.TrimSpace(st.Error) != "":
		return "", fmt.Errorf("hyperliquid: %s", st.Error)
	case st.Resting != nil:
		return strconv.FormatInt(st.Resting.OID, 10), nil
	case st.Filled != nil:
		return strconv.FormatInt(st.Filled.OID, 10), nil
	}
	return "", fmt.Errorf("hyperliquid: 未知下单结果 (%s)", truncate(string(raw), 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (e *HyperliquidExchange) PlaceLimit(symbol, side string, price, qty float64, tif string, postOnly bool) (string, error) {
	a, err := e.assetMeta(symbol)
	if err != nil {
		return "", err
	}
	t := "Gtc"
	if postOnly {
		t = "Alo" // add-liquidity-only = post only(会穿价时直接撤销,不会成 taker)
	} else if strings.EqualFold(tif, "IOC") {
		t = "Ioc"
	}
	px := hlNormalizePrice(price, a.SzDecimals, true)
	sz, _ := strconv.ParseFloat(strconv.FormatFloat(qty, 'f', a.SzDecimals, 64), 64)
	if px <= 0 || sz <= 0 {
		return "", fmt.Errorf("hyperliquid: 价格/数量取整后为 0 (px=%v sz=%v)", price, qty)
	}
	// 字段顺序必须与 SDK 的 order wire 一致(签名对 msgpack 字节敏感)。
	wire := newMap().
		setInt("a", int64(a.Index)).
		setBool("b", strings.EqualFold(side, "BUY")).
		setStr("p", hlFloatToWire(px)).
		setStr("s", hlFloatToWire(sz)).
		setBool("r", false).
		set("t", newMap().set("limit", newMap().setStr("tif", t)))
	action := newMap().
		setStr("type", "order").
		set("orders", mpArr{wire}).
		setStr("grouping", "na")

	raw, err := e.exchangeAction(action, uint64(time.Now().UnixMilli()))
	if err != nil {
		return "", err
	}
	return hlOrderStatus(raw)
}

func (e *HyperliquidExchange) CancelOrder(symbol, orderID string) error {
	a, err := e.assetMeta(symbol)
	if err != nil {
		return err
	}
	oid, err := strconv.ParseInt(strings.TrimSpace(orderID), 10, 64)
	if err != nil {
		return fmt.Errorf("hyperliquid: 非法订单号 %q", orderID)
	}
	action := newMap().
		setStr("type", "cancel").
		set("cancels", mpArr{newMap().setInt("a", int64(a.Index)).setInt("o", oid)})

	raw, err := e.exchangeAction(action, uint64(time.Now().UnixMilli()))
	if err != nil {
		return err
	}
	// 撤单响应同为 statuses[]:成功是字符串 "success",失败是 {error:...}。
	var r struct {
		Status   string `json:"status"`
		Response struct {
			Data struct {
				Statuses []json.RawMessage `json:"statuses"`
			} `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("hyperliquid: 撤单响应解析失败: %v", err)
	}
	if !strings.EqualFold(r.Status, "ok") {
		return fmt.Errorf("hyperliquid: 撤单失败 %s", truncate(string(raw), 200))
	}
	for _, st := range r.Response.Data.Statuses {
		var s string
		if json.Unmarshal(st, &s) == nil && strings.EqualFold(s, "success") {
			return nil
		}
		var errObj struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(st, &errObj) == nil && errObj.Error != "" {
			// 订单已成交/已撤 = 目的已达成,按幂等成功处理。
			if strings.Contains(strings.ToLower(errObj.Error), "never placed") ||
				strings.Contains(strings.ToLower(errObj.Error), "already canceled") ||
				strings.Contains(strings.ToLower(errObj.Error), "filled") {
				return nil
			}
			return fmt.Errorf("hyperliquid: 撤单失败 %s", errObj.Error)
		}
	}
	return nil
}

func (e *HyperliquidExchange) OpenOrders(symbol string) ([]OpenOrder, error) {
	if strings.TrimSpace(e.account) == "" {
		return nil, fmt.Errorf("hyperliquid: 未配置账户地址(MM_HL_ACCOUNT_ADDRESS)")
	}
	raw, err := e.post("/info", map[string]string{"type": "openOrders", "user": e.account})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Coin    string `json:"coin"`
		LimitPx string `json:"limitPx"`
		OID     int64  `json:"oid"`
		Side    string `json:"side"` // "A"=ask(卖) "B"=bid(买)
		Sz      string `json:"sz"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	coin := hlCoin(symbol)
	out := make([]OpenOrder, 0, len(rows))
	for _, r := range rows {
		if !strings.EqualFold(r.Coin, coin) {
			continue // openOrders 是全账户的,必须按 coin 过滤
		}
		side := "SELL"
		if strings.EqualFold(r.Side, "B") {
			side = "BUY"
		}
		out = append(out, OpenOrder{
			ID: strconv.FormatInt(r.OID, 10), Side: side,
			Price: atofP(r.LimitPx), Qty: atofP(r.Sz),
		})
	}
	return out, nil
}

// Balances returns the SIGNED position per coin (negative = short) plus USDC
// withdrawable margin. The engine treats this as inventory; on a perp it may go
// negative, which is exactly what lets it quote both sides symmetrically.
func (e *HyperliquidExchange) Balances() (map[string]float64, error) {
	if strings.TrimSpace(e.account) == "" {
		return nil, fmt.Errorf("hyperliquid: 未配置账户地址(MM_HL_ACCOUNT_ADDRESS)")
	}
	raw, err := e.post("/info", map[string]string{"type": "clearinghouseState", "user": e.account})
	if err != nil {
		return nil, err
	}
	var s struct {
		Withdrawable   string `json:"withdrawable"`
		AssetPositions []struct {
			Position struct {
				Coin string `json:"coin"`
				Szi  string `json:"szi"` // 有符号持仓,负=空
			} `json:"position"`
		} `json:"assetPositions"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	out := map[string]float64{"USDC": atofP(s.Withdrawable)}
	for _, p := range s.AssetPositions {
		out[strings.ToUpper(p.Position.Coin)] = atofP(p.Position.Szi)
	}
	return out, nil
}
