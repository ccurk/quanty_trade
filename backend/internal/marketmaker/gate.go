package marketmaker

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/equity"
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
			// UID 级限流器 + 429 熔断(台账 #109)。零配置 = 台账 #84 实测的
			// 惩罚档 10 请求/10 秒;调档时改 exec[].rate_limit,不必改代码。
			limiter: newGateLimiter(cfg.RateLimit),
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

	// limiter 是 UID 级限流器 + 429 熔断,见 gate_ratelimit.go。
	// 它按【逻辑请求】计数,分三个池记账(下单 / 撤单 / 查询,台账 #197);
	// 每个池里 REST 与 WS 两条出口共用同一份名额 —— 交易所按 UID 记账,
	// 不因走哪条连接而变多。(WS 是否真的计入我没有核实过;按"计入"处理是
	// fail-closed 的那一侧,若日后证实 WS 免限,调大对应池的配置即可,不必改代码。)
	limiter *gateLimiter

	filterMu sync.Mutex
	filters  map[string]SymbolFilter
}

func (e *GateExchange) Name() string { return "gate" }

// SupportsShort: 现货,只能卖出已持有的量。
func (e *GateExchange) SupportsShort() bool { return false }

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

// signed 是签名请求的对外入口:限流 → 发送 → 429 识别 → 退避重试 → 熔断反馈。
// 两个正交的参数,语义见 gate_ratelimit.go 顶部:
//
//	budget —— 这一发花【谁的】名额(下单 10/10s / 撤单 / 查询,三本账互不相干)
//	class  —— 这一发享受什么【待遇】:
//	          gateClassCritical(撤单路径):可等名额、可绕过熔断、可退避重试(幂等)
//	          gateClassQuote   (报价路径):不等、不重试、熔断打开时本地直接拒
//
// 【为什么下单不重试】不是安全性问题(429 是明确拒绝,订单没进去,重发不会双挂),
// 是经济性问题:下单池的预算就是 1 请求/秒,一发重试花掉的是下一次报价的名额,
// 而那时价格已经陈了。引擎下一轮会用新价重新报,那才是正确的"重试"。
// 这与 gate_ws.go PlaceLimit 里"帧已出站就不 REST 重发"是同一条思路的延续。
func (e *GateExchange) signed(budget gateBudget, class gateReqClass, method, path string, q url.Values, body []byte) ([]byte, error) {
	// 熔断打开时报价类本地直接拒,一发都不出去(对照 binance.go:826 的 RateLimited() 快拒)。
	// 撤单类【故意】不受熔断阻挡:熔断的目的是别把配额浪费在注定被拒的报价上,
	// 而不是把已经挂在盘口的单锁死在那里。
	if class != gateClassCritical && e.limiter.RateLimited() {
		return nil, fmt.Errorf("%w: 熔断打开(连续 429),报价类暂停出网", ErrGateRateLimited)
	}

	attempts := 1
	if class == gateClassCritical {
		attempts += e.limiter.cfg.MaxRetries
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := e.limiter.acquire(budget, class); err != nil {
			return nil, err
		}
		rb, status, retryAfter, hint, err := e.signedOnce(method, path, q, body)
		// 档位以交易所口径为准 —— 成功失败都采纳,429 那一发带的信息最值钱。
		if hint.OK {
			e.limiter.observeRemote(budget, hint.Limit, hint.Remain, hint.ResetAt)
		}
		if err == nil {
			e.limiter.onSuccess()
			return rb, nil
		}
		lastErr = err
		if !gateIsRateLimited(status, rb) {
			// 非限流错误(4xx 业务错、网络错):不重试。网络错时请求可能已经落地,
			// 重发下单会双挂;撤单虽幂等,但也没有证据表明重发会更好。
			return nil, err
		}
		e.limiter.on429(retryAfter)
		if attempt == attempts-1 {
			break
		}
		e.limiter.sleep(e.limiter.backoffFor(attempt, retryAfter))
	}
	return nil, lastErr
}

// gateHTTPError 让 HTTP 状态码脱离错误字符串,成为调用方能判别的【数据】。
//
// 改之前这里只 fmt.Errorf 一个串,调用方除了匹配字符串没有任何办法知道
// "这一次是 404 订单已不存在"还是"真的撤不动"。撤单恰恰需要这个区分:
// 404 ORDER_NOT_FOUND 意味着这张单已经不在盘口上了 —— 那正是撤单要的结果,
// 不是失败。cancelAll 把两者一视同仁地计成 failed,于是"已经撤掉"被读成
// "没撤掉",记成撤单欠账,pair 就再也不恢复报价。
//
// Error() 的格式与原先的 fmt.Errorf 逐字节一致 —— 日志输出不变。
type gateHTTPError struct {
	Status int
	Method string
	Path   string
	Body   string
}

func (e *gateHTTPError) Error() string {
	return fmt.Sprintf("gate %s %s -> %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// cancelIsAlreadyGone 判定一次撤单失败是否等价于「这张单已经不在盘口上了」。
//
// 是的话,撤单的目标状态已经达成 —— 调用方必须按成功处理,否则"已经撤掉"会被
// 读成"没撤掉",进而记成撤单欠账、把 pair 永久锁在停报价状态(2026-09-16 现场)。
func cancelIsAlreadyGone(err error) bool {
	var he *gateHTTPError
	return errors.As(err, &he) && he.Status == http.StatusNotFound
}

// signedOnce 发一发签名请求,把 HTTP 状态码、Retry-After 和限流档位提示一并交回上层。
// 原来的实现把状态码折进 error 字符串里,导致调用方无法区分"被限流"和"参数错" ——
// 这正是 gate 这条腿此前没有任何 429 处理的直接原因。
func (e *GateExchange) signedOnce(method, path string, q url.Values, body []byte) ([]byte, int, time.Duration, gateRateLimitHint, error) {
	var noHint gateRateLimitHint
	if e.apiKey == "" || e.secret == "" {
		return nil, 0, 0, noHint, fmt.Errorf("gate: missing api credentials")
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
		return nil, 0, 0, noHint, err
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
		return nil, 0, 0, noHint, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	retryAfter := gateRetryAfter(resp.Header.Get("Retry-After"))
	// 限流档位提示。⚠️ 我【没有核实】gate 一定会在 REST 响应头上回这三个头
	// (官方只在 WS 应答信封里明确列了它们,见 gate_ws.go)。回了就用、
	// 没回就退回本地配置常量 —— 读不到是零成本的,读到是白捡的。
	hint := parseGateRateLimitHint(
		resp.Header.Get("X-Gate-RateLimit-Limit"),
		resp.Header.Get("X-Gate-RateLimit-Requests-Remain"),
		resp.Header.Get("X-Gate-RateLimit-Reset-Timestamp"),
	)
	if resp.StatusCode >= 300 {
		return rb, resp.StatusCode, retryAfter, hint,
			&gateHTTPError{Status: resp.StatusCode, Method: method, Path: path, Body: strings.TrimSpace(string(rb))}
	}
	return rb, resp.StatusCode, retryAfter, hint, nil
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
		// WS 与 REST 花的是同一个下单池(交易所按 UID 记账,不因走哪条连接而变多)。
		if err := e.limiter.acquire(gateBudgetOrder, gateClassQuote); err != nil {
			return "", err
		}
		id, hint, sentOut, err := e.ws.PlaceLimit(payload)
		// WS 应答信封里的档位/剩余是【官方明确列了的】那一份,优先级最高。
		if hint.OK {
			e.limiter.observeRemote(gateBudgetOrder, hint.Limit, hint.Remain, hint.ResetAt)
		}
		if err == nil {
			e.limiter.onSuccess()
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
	resp, err := e.signed(gateBudgetOrder, gateClassQuote, http.MethodPost, "/spot/orders", nil, body)
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

// CancelOrder 花【撤单池】的名额、走 gateClassCritical 的待遇。
//
// 撤单不进公告 40657 那 10/10s 的下单档(它只点名 POST 与 PATCH),官方基线是
// 5000r/s —— 所以撤单本来就不该和下单抢名额,这是台账 #197 修掉的那半条。
// 待遇仍是 critical:撤不掉的单 = 挂在盘口的裸露敞口,可以等、可以退避重试、
// 不被熔断挡住。
func (e *GateExchange) CancelOrder(symbol, orderID string) error {
	if e.ws != nil {
		// WS 与 REST 花的是同一个撤单池(按 UID 记账,不因走哪条连接而变多)。
		if err := e.limiter.acquire(gateBudgetCancel, gateClassCritical); err != nil {
			logger.Warnf("[mm-gatews] cancel %s: 限流器未放行 WS,转 REST: %v", orderID, err)
		} else if hint, err := e.ws.CancelOrder(orderID, gateSym(symbol)); err == nil {
			if hint.OK {
				e.limiter.observeRemote(gateBudgetCancel, hint.Limit, hint.Remain, hint.ResetAt)
			}
			e.limiter.onSuccess()
			return nil
		} else {
			// 撤单幂等: WS 任一阶段失败都可安全 REST 兜底(重复撤单最多报"不存在")。
			logger.Warnf("[mm-gatews] cancel %s: WS failed, REST fallback: %v", orderID, err)
		}
	}
	_, err := e.signed(gateBudgetCancel, gateClassCritical, http.MethodDelete, "/spot/orders/"+orderID, url.Values{"currency_pair": {gateSym(symbol)}}, nil)
	if err != nil {
		// 404 ORDER_NOT_FOUND = 这张单已经不在盘口上了,撤单的目标状态已达成。
		// 撤单是幂等的,"不存在"不是失败(本函数上方那句注释早就写了这半条,
		// 只是错误一路裸传到了 cancelAll,被计成 failed → 撤单欠账 → 永久停报价)。
		//
		// 安全性:cancelAll 只撤它【刚刚】用同一个 symbol 从挂单档读到的 ID,
		// 所以这里的 404 不可能是"symbol 传错导致撤错了别处",只可能是这张单
		// 在"读挂单"与"撤单"之间成交或被撤了 —— 两种情况盘口都是干净的。
		if cancelIsAlreadyGone(err) {
			logger.Infof("[mm-gatews] cancel %s: 404 订单已不存在,视为撤单成功", orderID)
			return nil
		}
	}
	return err
}

// RateLimitStatus 把限流器状态交给引擎(types.go RateLimitReporter)。
// 引擎据此决定熔断持续打开时是否撤单站下 —— 那是唯一一个"引擎必须知道
// 限流器内部状态"的决策:它问的是"我还能不能移动报价",而不是"这一发能不能发"。
func (e *GateExchange) RateLimitStatus() RateLimitStatus {
	return RateLimitStatus{
		BreakerOpenSince: e.limiter.breakerOpenSince(),
		WindowMs:         e.limiter.cfg.WindowMs,
	}
}

// OpenOrdersForCancel 是 cancelAll 专用的读挂单口(types.go CancelPathReader)。
// 与 CancelOrder 同走 gateClassCritical:可等名额、绕过熔断,并且在【查询池】里
// 看得见包括预留在内的全部名额 —— 报价周期的读把查询池打光时,cancelAll 的这一步
// 仍然拿得到名额。普通 OpenOrders 走 gateClassQuote,熔断打开时本地直接被拒。
func (e *GateExchange) OpenOrdersForCancel(symbol string) ([]OpenOrder, error) {
	return e.openOrders(gateClassCritical, symbol)
}

func (e *GateExchange) OpenOrders(symbol string) ([]OpenOrder, error) {
	return e.openOrders(gateClassQuote, symbol)
}

// openOrders 花【查询池】的名额:GET /spot/orders 是私有查询端点,不进
// 公告 40657 那 10/10s 的下单档。改之前它和下单共用一个池,是 #197 的另一半。
func (e *GateExchange) openOrders(class gateReqClass, symbol string) ([]OpenOrder, error) {
	resp, err := e.signed(gateBudgetQuery, class, http.MethodGet, "/spot/orders", url.Values{"currency_pair": {gateSym(symbol)}, "status": {"open"}}, nil)
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

// Balances returns spendable spot balances, keyed by upper-cased currency.
//
// The returned map is unchanged (available only) because that is what the quoting
// loop's inventory maths needs. What IS new is that the same response's "locked"
// field — resting-order locks, always present, see
// internal/rebalance/balance_source.go which decodes it off this very endpoint —
// is no longer discarded: it is recorded alongside available as an equity
// snapshot. This call site is the ~4x/second read from 台账 #42 that had never
// once been written down.
func (e *GateExchange) Balances() (map[string]float64, error) {
	// 查询池:GET /spot/accounts 是私有查询端点,和下单的 10/10s 不是一本账。
	resp, err := e.signed(gateBudgetQuery, gateClassQuote, http.MethodGet, "/spot/accounts", nil, nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Currency  string `json:"currency"`
		Available string `json:"available"`
		Locked    string `json:"locked"`
	}
	if err := json.Unmarshal(resp, &raw); err != nil {
		return nil, err
	}
	now := time.Now()
	out := map[string]float64{}
	for _, b := range raw {
		avail, locked := atof(b.Available), atof(b.Locked)
		if avail > 0 {
			out[strings.ToUpper(b.Currency)] = avail
		}
		// Record every asset that holds anything, including one whose balance is
		// entirely locked in resting orders — that row is exactly the case the
		// available-only view cannot see. Non-blocking and downsampled inside
		// equity.Emit, so this stays free on the quoting path.
		if avail > 0 || locked > 0 {
			equity.Emit(equity.Snapshot{
				Venue: equity.VenueGateSpot, Asset: strings.ToUpper(b.Currency), TakenAt: now,
				// Spot has no mark-to-market, so Unrealized stays 0; the venue
				// column is what marks that as "not applicable", not "unread".
				Free: avail, Total: avail + locked,
				Source: "GET /spot/accounts",
			})
		}
	}
	return out, nil
}
