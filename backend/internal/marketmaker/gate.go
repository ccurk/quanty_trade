package marketmaker

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/logger"
)

// Gate.io spot API v4 (NOT Binance-compatible): signed with HMAC-SHA512 over
// "METHOD\n/api/v4<path>\n<query>\nHEX(SHA512(body))\n<unix-seconds>" carried in
// KEY / SIGN / Timestamp headers; symbols use an underscore (BTC_USDT); post-only
// is time_in_force="poc". Verified against gateio/gateapi-python model docs (2026-08).
const gateDefaultBaseURL = "https://api.gateio.ws"

func init() {
	RegisterExec("gate", func(cfg ExecConfig) (ExecExchange, error) {
		base := strings.TrimRight(cfg.BaseURL, "/")
		if base == "" {
			base = gateDefaultBaseURL
		}
		e := &GateExchange{
			baseURL: base,
			apiKey:  cfg.APIKey,
			secret:  cfg.APISecret,
			http:    &http.Client{Timeout: 10 * time.Second},
			filters: map[string]SymbolFilter{},
		}
		if cfg.WSTrade {
			// ws_trade=true: 下单/撤单优先走 WS 长连接(建连+登录惰性发生在首单),
			// REST 保留为回退路径; 详细回退语义见 gate_ws.go 头注。
			e.ws = newGateWSTrader("", cfg.APIKey, cfg.APISecret)
			logger.Infof("[mm-gatews] ws_trade enabled for gate exec")
		}
		return e, nil
	})
}

type GateExchange struct {
	baseURL string
	apiKey  string
	secret  string
	http    *http.Client
	ws      *gateWSTrader // nil = REST only (ws_trade=false, 默认)

	filterMu sync.Mutex
	filters  map[string]SymbolFilter
}

func (e *GateExchange) Name() string { return "gate" }

// gateSym converts any of BTC/USDT, BTC-USDT, BTCUSDT(only if it has a sep) to Gate's BTC_USDT.
func gateSym(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return s
}

func (e *GateExchange) publicGET(path string, q url.Values) ([]byte, error) {
	u := e.baseURL + "/api/v4" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	resp, err := e.http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gate %s -> %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (e *GateExchange) signed(method, path string, q url.Values, body []byte) ([]byte, error) {
	if e.apiKey == "" || e.secret == "" {
		return nil, fmt.Errorf("gate: missing api credentials")
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	bodyHash := sha512.Sum512(body) // SHA512("") is a valid constant for empty bodies
	query := ""
	if q != nil {
		query = q.Encode()
	}
	sigStr := method + "\n" + "/api/v4" + path + "\n" + query + "\n" + hex.EncodeToString(bodyHash[:]) + "\n" + ts
	mac := hmac.New(sha512.New, []byte(e.secret))
	mac.Write([]byte(sigStr))
	sign := hex.EncodeToString(mac.Sum(nil))

	u := e.baseURL + "/api/v4" + path
	if query != "" {
		u += "?" + query
	}
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("KEY", e.apiKey)
	req.Header.Set("SIGN", sign)
	req.Header.Set("Timestamp", ts)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gate %s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return rb, nil
}

func (e *GateExchange) FetchBookTicker(symbol string) (BookTicker, error) {
	body, err := e.publicGET("/spot/tickers", url.Values{"currency_pair": {gateSym(symbol)}})
	if err != nil {
		return BookTicker{}, err
	}
	var r []struct {
		CurrencyPair string `json:"currency_pair"`
		HighestBid   string `json:"highest_bid"`
		LowestAsk    string `json:"lowest_ask"`
	}
	if err := json.Unmarshal(body, &r); err != nil || len(r) == 0 {
		return BookTicker{}, fmt.Errorf("gate ticker empty/parse: %v", err)
	}
	return BookTicker{Symbol: r[0].CurrencyPair, BidPx: atof(r[0].HighestBid), AskPx: atof(r[0].LowestAsk), Ts: time.Now()}, nil
}

func (e *GateExchange) SymbolFilter(symbol string) (SymbolFilter, error) {
	sym := gateSym(symbol)
	e.filterMu.Lock()
	if f, ok := e.filters[sym]; ok {
		e.filterMu.Unlock()
		return f, nil
	}
	e.filterMu.Unlock()

	body, err := e.publicGET("/spot/currency_pairs/"+sym, nil)
	if err != nil {
		return SymbolFilter{}, err
	}
	var r struct {
		Base            string `json:"base"`
		Quote           string `json:"quote"`
		AmountPrecision int    `json:"amount_precision"`
		Precision       int    `json:"precision"`
		MinBaseAmount   string `json:"min_base_amount"`
		MinQuoteAmount  string `json:"min_quote_amount"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return SymbolFilter{}, err
	}
	f := SymbolFilter{
		BaseAsset:   strings.ToUpper(r.Base),
		QuoteAsset:  strings.ToUpper(r.Quote),
		TickSize:    math.Pow(10, -float64(r.Precision)),
		StepSize:    math.Pow(10, -float64(r.AmountPrecision)),
		MinNotional: atof(r.MinQuoteAmount),
	}
	e.filterMu.Lock()
	e.filters[sym] = f
	e.filterMu.Unlock()
	return f, nil
}

func (e *GateExchange) PlaceLimit(symbol, side string, price, qty float64, tif string, postOnly bool) (string, error) {
	t := "gtc"
	if postOnly {
		t = "poc" // pending-or-cancelled = maker-only
	} else if tif != "" {
		t = strings.ToLower(tif)
	}
	payload := map[string]string{
		"currency_pair": gateSym(symbol),
		"type":          "limit",
		"account":       "spot",
		"side":          strings.ToLower(side),
		"amount":        strconv.FormatFloat(qty, 'f', -1, 64),
		"price":         strconv.FormatFloat(price, 'f', -1, 64),
		"time_in_force": t,
	}
	if e.ws != nil {
		id, sentOut, err := e.ws.PlaceLimit(payload)
		if err == nil {
			return id, nil
		}
		if sentOut {
			// 帧已出站: 订单可能已被交易所接受, REST 重发=双挂单风险, 直接报错;
			// engine 下个 refresh 周期经 OpenOrders 对账自愈。
			return "", err
		}
		logger.Warnf("[mm-gatews] place %s: WS unavailable pre-send, REST fallback: %v", payload["currency_pair"], err)
	}
	body, _ := json.Marshal(payload)
	resp, err := e.signed(http.MethodPost, "/spot/orders", nil, body)
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return "", err
	}
	return r.ID, nil
}

func (e *GateExchange) CancelOrder(symbol, orderID string) error {
	if e.ws != nil {
		if err := e.ws.CancelOrder(orderID, gateSym(symbol)); err == nil {
			return nil
		} else {
			// 撤单幂等: WS 任一阶段失败都可安全 REST 兜底(重复撤单最多报"不存在")。
			logger.Warnf("[mm-gatews] cancel %s: WS failed, REST fallback: %v", orderID, err)
		}
	}
	_, err := e.signed(http.MethodDelete, "/spot/orders/"+orderID, url.Values{"currency_pair": {gateSym(symbol)}}, nil)
	return err
}

func (e *GateExchange) OpenOrders(symbol string) ([]OpenOrder, error) {
	resp, err := e.signed(http.MethodGet, "/spot/orders", url.Values{"currency_pair": {gateSym(symbol)}, "status": {"open"}}, nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID     string `json:"id"`
		Side   string `json:"side"`
		Price  string `json:"price"`
		Amount string `json:"amount"`
	}
	if err := json.Unmarshal(resp, &raw); err != nil {
		return nil, err
	}
	out := make([]OpenOrder, 0, len(raw))
	for _, o := range raw {
		out = append(out, OpenOrder{ID: o.ID, Side: strings.ToUpper(o.Side), Price: atof(o.Price), Qty: atof(o.Amount)})
	}
	return out, nil
}

func (e *GateExchange) Balances() (map[string]float64, error) {
	resp, err := e.signed(http.MethodGet, "/spot/accounts", nil, nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Currency  string `json:"currency"`
		Available string `json:"available"`
	}
	if err := json.Unmarshal(resp, &raw); err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, b := range raw {
		if v := atof(b.Available); v > 0 {
			out[strings.ToUpper(b.Currency)] = v
		}
	}
	return out, nil
}
