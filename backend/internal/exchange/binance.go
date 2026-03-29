package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
	"quanty_trade/internal/secure"

	"github.com/gorilla/websocket"
)

type BinanceExchange struct {
	name         string
	httpClient   *http.Client
	baseURL      string
	wsBaseURL    string
	wsAPIBaseURL string
	market       string
	baseURLSet   bool
	wsBaseURLSet bool

	mu        sync.RWMutex
	credsByID map[uint]binanceCred

	infoOnce sync.Once
	info     binanceExchangeInfoCache

	streamMu    sync.Mutex
	streamsByID map[uint]*binanceUserStream

	leverageMu    sync.Mutex
	leverageByKey map[string]int

	symbolSelectMu       sync.Mutex
	symbolSelectCacheKey string
	symbolSelectCache    SymbolSelectResult
	symbolSelectCacheExp time.Time
	symbolSelectBanUntil time.Time

	positionsCacheMu  sync.Mutex
	positionsCacheExp map[uint]time.Time
	positionsCache    map[uint][]Position

	usdmAvailMu    sync.Mutex
	usdmAvailExp   map[uint]time.Time
	usdmAvailCache map[uint]float64

	// rate limit tracking
	rateLimitWeight1m int
	lastRateLimitLoad time.Time
	usedWeight1m      int
	requestBanUntil   time.Time
}

type SymbolSelectCriteria struct {
	MinPrice      float64
	MaxPrice      float64
	MinPrecision  int
	MinVolatility float64
	Quote         string
	Limit         int
	OnlySymbols   []string
}

type SymbolSelectResult struct {
	Selected []string
	Rejected map[string]string
}

type binanceCred struct {
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
	Testnet   bool   `json:"testnet"`
}

func NewBinanceExchange() *BinanceExchange {
	ex := &BinanceExchange{
		name:       "Binance",
		httpClient: &http.Client{Timeout: 15 * time.Second},
		credsByID:  make(map[uint]binanceCred),
	}
	c := conf.C().Exchange.Binance
	ex.market = strings.ToLower(strings.TrimSpace(c.Market))
	if ex.market == "" {
		ex.market = "spot"
	}

	if ex.market == "usdm" {
		ex.baseURL = "https://fapi.binance.com"
		ex.wsBaseURL = "wss://fstream.binance.com"
	} else {
		ex.baseURL = "https://api.binance.com"
		ex.wsBaseURL = "wss://stream.binance.com:9443"
	}
	ex.wsAPIBaseURL = "wss://ws-api.binance.com:443/ws-api/v3"

	if c.BaseURL != "" {
		ex.baseURL = c.BaseURL
		ex.baseURLSet = true
	}
	if c.WsBaseURL != "" {
		ex.wsBaseURL = c.WsBaseURL
		ex.wsBaseURLSet = true
	}
	ex.streamsByID = make(map[uint]*binanceUserStream)
	ex.leverageByKey = make(map[string]int)
	ex.positionsCacheExp = make(map[uint]time.Time)
	ex.positionsCache = make(map[uint][]Position)
	ex.usdmAvailExp = make(map[uint]time.Time)
	ex.usdmAvailCache = make(map[uint]float64)
	ex.rateLimitWeight1m = 1200 // sensible default; will be overridden by exchangeInfo
	return ex
}

func (b *BinanceExchange) GetName() string { return b.name }

func (b *BinanceExchange) Market() string { return b.market }

func (b *BinanceExchange) LastPrice(symbol string) (float64, error) {
	sym := binanceSymbol(symbol)
	params := url.Values{}
	params.Set("symbol", sym)
	path := "/api/v3/ticker/price"
	if b.market == "usdm" {
		path = "/fapi/v1/ticker/price"
	}
	body, _, err := b.publicRequest(context.Background(), path, params)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Price string `json:"price"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}
	px, _ := strconv.ParseFloat(strings.TrimSpace(resp.Price), 64)
	return px, nil
}

func (b *BinanceExchange) USDMAvailableUSDT(ownerID uint) (float64, error) {
	if b.market != "usdm" {
		return 0, fmt.Errorf("not usdm")
	}

	now := time.Now()
	b.usdmAvailMu.Lock()
	if exp, ok := b.usdmAvailExp[ownerID]; ok && now.Before(exp) {
		v := b.usdmAvailCache[ownerID]
		b.usdmAvailMu.Unlock()
		return v, nil
	}
	b.usdmAvailMu.Unlock()

	cred, err := b.getCred(ownerID)
	if err != nil {
		return 0, err
	}
	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v2/balance", nil)
	if err != nil {
		return 0, err
	}
	var raw []struct {
		Asset            string `json:"asset"`
		AvailableBalance string `json:"availableBalance"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, err
	}
	for _, r := range raw {
		if strings.EqualFold(r.Asset, "USDT") {
			v, _ := strconv.ParseFloat(strings.TrimSpace(r.AvailableBalance), 64)
			b.usdmAvailMu.Lock()
			b.usdmAvailCache[ownerID] = v
			b.usdmAvailExp[ownerID] = time.Now().Add(5 * time.Second)
			b.usdmAvailMu.Unlock()
			return v, nil
		}
	}
	b.usdmAvailMu.Lock()
	b.usdmAvailCache[ownerID] = 0
	b.usdmAvailExp[ownerID] = time.Now().Add(5 * time.Second)
	b.usdmAvailMu.Unlock()
	return 0, nil
}

func (b *BinanceExchange) SetUserCredentials(ownerID uint, apiKey, apiSecret string, testnet bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cred := binanceCred{APIKey: apiKey, APISecret: apiSecret, Testnet: testnet}
	b.credsByID[ownerID] = cred
}

func (b *BinanceExchange) getCred(ownerID uint) (binanceCred, error) {
	b.mu.RLock()
	cred, ok := b.credsByID[ownerID]
	b.mu.RUnlock()
	if ok && cred.APIKey != "" && cred.APISecret != "" {
		return cred, nil
	}

	if ownerID != 0 && database.DB != nil {
		var user models.User
		if err := database.DB.Where("id = ?", ownerID).First(&user).Error; err == nil && user.Configs != "" {
			plain, err := secure.DecryptString(user.Configs)
			if err != nil {
				return binanceCred{}, err
			}
			parsed, err := parseBinanceCredFromUserConfigs(plain)
			if err == nil && parsed.APIKey != "" && parsed.APISecret != "" {
				b.mu.Lock()
				b.credsByID[ownerID] = parsed
				b.mu.Unlock()
				return parsed, nil
			}
		}
	}

	c := conf.C().Exchange.Binance
	if c.APIKey != "" && c.APISecret != "" {
		return binanceCred{
			APIKey:    c.APIKey,
			APISecret: c.APISecret,
			Testnet:   c.Testnet,
		}, nil
	}

	envKey := os.Getenv("BINANCE_API_KEY")
	envSecret := os.Getenv("BINANCE_API_SECRET")
	if envKey != "" && envSecret != "" {
		return binanceCred{
			APIKey:    envKey,
			APISecret: envSecret,
			Testnet:   os.Getenv("BINANCE_TESTNET") == "true",
		}, nil
	}

	return binanceCred{}, fmt.Errorf("missing binance credentials")
}

func parseBinanceCredFromUserConfigs(configs string) (binanceCred, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(configs), &raw); err != nil {
		return binanceCred{}, err
	}
	v, ok := raw["binance"]
	if !ok {
		return binanceCred{}, fmt.Errorf("missing binance config")
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return binanceCred{}, fmt.Errorf("invalid binance config")
	}

	getStr := func(keys ...string) string {
		for _, k := range keys {
			if vv, ok := m[k]; ok {
				if s, ok := vv.(string); ok {
					return s
				}
			}
		}
		return ""
	}
	getBool := func(keys ...string) bool {
		for _, k := range keys {
			if vv, ok := m[k]; ok {
				if b, ok := vv.(bool); ok {
					return b
				}
				if s, ok := vv.(string); ok {
					return s == "true" || s == "1"
				}
			}
		}
		return false
	}

	return binanceCred{
		APIKey:    getStr("api_key", "apiKey", "key"),
		APISecret: getStr("api_secret", "apiSecret", "secret"),
		Testnet:   getBool("testnet", "useTestnet"),
	}, nil
}

func (b *BinanceExchange) apiBaseURL(cred binanceCred) string {
	if b.baseURLSet {
		return b.baseURL
	}
	if cred.Testnet {
		if b.market == "usdm" {
			return "https://testnet.binancefuture.com"
		}
		return "https://testnet.binance.vision"
	}
	return b.baseURL
}

func (b *BinanceExchange) wsAPIURL(cred binanceCred) string {
	if v := conf.C().Exchange.Binance.WsAPIURL; v != "" {
		return v
	}
	if cred.Testnet {
		return "wss://testnet.binance.vision/ws-api/v3"
	}
	return b.wsAPIBaseURL
}

func binanceSymbol(symbol string) string {
	return NormalizeSymbol(symbol)
}

func decimalPrecisionFromPriceStr(s string) int {
	x := strings.TrimSpace(s)
	if x == "" {
		return 0
	}
	if strings.Contains(x, "e") || strings.Contains(x, "E") {
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			y := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.10f", f), "0"), ".")
			if i := strings.IndexByte(y, '.'); i >= 0 {
				return len(y) - i - 1
			}
			return 0
		}
		return 0
	}
	x = strings.TrimRight(x, "0")
	x = strings.TrimRight(x, ".")
	if i := strings.IndexByte(x, '.'); i >= 0 {
		return len(x) - i - 1
	}
	return 0
}

func displayFromBinanceSymbol(raw string, quote string) string {
	q := strings.ToUpper(strings.TrimSpace(quote))
	s := strings.ToUpper(strings.TrimSpace(raw))
	if q == "" {
		q = "USDT"
	}
	if !strings.HasSuffix(s, q) {
		return s
	}
	base := strings.TrimSuffix(s, q)
	if base == "" {
		return s
	}
	return base + "/" + q
}

func parseBinanceNum(raw json.RawMessage) (float64, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, fmt.Errorf("empty number")
	}
	if strings.HasPrefix(s, "\"") {
		u, err := strconv.Unquote(s)
		if err != nil {
			return 0, err
		}
		s = strings.TrimSpace(u)
	}
	return strconv.ParseFloat(s, 64)
}

func (b *BinanceExchange) SelectSymbols(criteria SymbolSelectCriteria) ([]string, error) {
	res, err := b.SelectSymbolsDetailed(criteria)
	if err != nil {
		return nil, err
	}
	return res.Selected, nil
}

func (b *BinanceExchange) SelectSymbolsDetailed(criteria SymbolSelectCriteria) (SymbolSelectResult, error) {
	b.symbolSelectMu.Lock()
	now := time.Now()
	if now.Before(b.symbolSelectBanUntil) {
		until := b.symbolSelectBanUntil
		b.symbolSelectMu.Unlock()
		return SymbolSelectResult{}, fmt.Errorf("binance api error: {\"code\":-1003,\"msg\":%q}", fmt.Sprintf("Way too many requests; IP banned until %d", until.UnixMilli()))
	}
	key := symbolSelectCacheKey(criteria)
	if key == b.symbolSelectCacheKey && now.Before(b.symbolSelectCacheExp) {
		cached := b.symbolSelectCache
		b.symbolSelectMu.Unlock()
		return cached, nil
	}
	b.symbolSelectMu.Unlock()

	quote := strings.ToUpper(strings.TrimSpace(criteria.Quote))
	if quote == "" {
		quote = "USDT"
	}
	limit := criteria.Limit
	if limit <= 0 {
		limit = 20
	}
	only := map[string]struct{}{}
	trackReject := false
	rejected := map[string]string{}
	if len(criteria.OnlySymbols) > 0 {
		trackReject = true
		for _, s := range criteria.OnlySymbols {
			n := strings.ToUpper(binanceSymbol(s))
			if n != "" {
				only[n] = struct{}{}
			}
		}
	}

	path := "/api/v3/ticker/24hr"
	if b.market == "usdm" {
		path = "/fapi/v1/ticker/24hr"
	}
	body, _, err := b.publicRequest(context.Background(), path, nil)
	if err != nil {
		if banUntil, ok := parseBinanceIPBanUntil(err.Error()); ok {
			b.symbolSelectMu.Lock()
			if banUntil.After(b.symbolSelectBanUntil) {
				b.symbolSelectBanUntil = banUntil
			}
			b.symbolSelectMu.Unlock()
		}
		return SymbolSelectResult{}, err
	}

	var rows []struct {
		Symbol             string `json:"symbol"`
		LastPrice          string `json:"lastPrice"`
		HighPrice          string `json:"highPrice"`
		LowPrice           string `json:"lowPrice"`
		PriceChangePercent string `json:"priceChangePercent"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return SymbolSelectResult{}, err
	}

	type cand struct {
		Display string
		Score   float64
	}
	out := make([]cand, 0, limit)
	seen := map[string]struct{}{}
	for _, r := range rows {
		sym := strings.ToUpper(strings.TrimSpace(r.Symbol))
		if sym == "" {
			continue
		}
		if !strings.HasSuffix(sym, quote) {
			continue
		}
		seen[sym] = struct{}{}
		if len(only) > 0 {
			if _, ok := only[sym]; !ok {
				continue
			}
		}

		price, _ := strconv.ParseFloat(strings.TrimSpace(r.LastPrice), 64)
		if price <= 0 {
			if trackReject {
				rejected[displayFromBinanceSymbol(sym, quote)] = "invalid last price"
			}
			continue
		}
		if criteria.MinPrice > 0 && price < criteria.MinPrice {
			if trackReject {
				rejected[displayFromBinanceSymbol(sym, quote)] = fmt.Sprintf("price %v < min_price %v", price, criteria.MinPrice)
			}
			continue
		}
		if criteria.MaxPrice > 0 && price > criteria.MaxPrice {
			if trackReject {
				rejected[displayFromBinanceSymbol(sym, quote)] = fmt.Sprintf("price %v > max_price %v", price, criteria.MaxPrice)
			}
			continue
		}

		prec := decimalPrecisionFromPriceStr(r.LastPrice)
		if criteria.MinPrecision > 0 && prec < criteria.MinPrecision {
			if trackReject {
				rejected[displayFromBinanceSymbol(sym, quote)] = fmt.Sprintf("precision %d < min_precision %d", prec, criteria.MinPrecision)
			}
			continue
		}

		changePct, _ := strconv.ParseFloat(strings.TrimSpace(r.PriceChangePercent), 64)
		high, _ := strconv.ParseFloat(strings.TrimSpace(r.HighPrice), 64)
		low, _ := strconv.ParseFloat(strings.TrimSpace(r.LowPrice), 64)
		volPct := 0.0
		if low > 0 && high > 0 && high >= low {
			volPct = (high - low) / low * 100.0
		}
		score := math.Abs(changePct)
		if volPct > score {
			score = volPct
		}
		if criteria.MinVolatility > 0 && score < criteria.MinVolatility {
			if trackReject {
				rejected[displayFromBinanceSymbol(sym, quote)] = fmt.Sprintf("volatility %0.4f < min_volatility %0.4f", score, criteria.MinVolatility)
			}
			continue
		}

		out = append(out, cand{Display: displayFromBinanceSymbol(sym, quote), Score: score})
	}

	if trackReject {
		for s := range only {
			if _, ok := seen[s]; !ok {
				rejected[displayFromBinanceSymbol(s, quote)] = "symbol not found in ticker"
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	symbols := make([]string, 0, len(out))
	for _, c := range out {
		symbols = append(symbols, c.Display)
	}
	res := SymbolSelectResult{Selected: symbols, Rejected: rejected}
	b.symbolSelectMu.Lock()
	b.symbolSelectCacheKey = key
	b.symbolSelectCache = res
	b.symbolSelectCacheExp = time.Now().Add(30 * time.Second)
	b.symbolSelectMu.Unlock()
	return res, nil
}

func symbolSelectCacheKey(c SymbolSelectCriteria) string {
	parts := make([]string, 0, len(c.OnlySymbols))
	for _, s := range c.OnlySymbols {
		parts = append(parts, strings.ToUpper(binanceSymbol(s)))
	}
	sort.Strings(parts)
	return fmt.Sprintf("q=%s|minp=%0.8f|maxp=%0.8f|minprec=%d|minvol=%0.8f|limit=%d|only=%s",
		strings.ToUpper(strings.TrimSpace(c.Quote)),
		c.MinPrice, c.MaxPrice, c.MinPrecision, c.MinVolatility, c.Limit,
		strings.Join(parts, ","),
	)
}

var binanceIPBanUntilRe = regexp.MustCompile(`banned until\s+(\d+)`)

func parseBinanceIPBanUntil(errMsg string) (time.Time, bool) {
	if !strings.Contains(errMsg, "\"code\":-1003") && !strings.Contains(errMsg, "Way too many requests") {
		return time.Time{}, false
	}
	m := binanceIPBanUntilRe.FindStringSubmatch(errMsg)
	if len(m) != 2 {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

func (b *BinanceExchange) displaySymbol(symbol string) string {
	s := strings.ToUpper(symbol)
	if strings.Contains(s, "/") {
		return s
	}
	f, err := b.getFilters(s)
	if err == nil && f.BaseAsset != "" && f.QuoteAsset != "" {
		return strings.ToUpper(f.BaseAsset) + "/" + strings.ToUpper(f.QuoteAsset)
	}
	return s
}

func binanceInterval(timeframe string) (string, error) {
	switch timeframe {
	case "1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w", "1M":
		return timeframe, nil
	default:
		return "", fmt.Errorf("unsupported timeframe: %s", timeframe)
	}
}

func binanceAPIError(body []byte) (int, string, bool) {
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, "", false
	}
	if parsed.Code == 0 && strings.TrimSpace(parsed.Msg) == "" {
		return 0, "", false
	}
	return parsed.Code, strings.TrimSpace(parsed.Msg), true
}

func formatBinanceAPIError(code int, msg string) error {
	lowerMsg := strings.ToLower(strings.TrimSpace(msg))
	if strings.Contains(lowerMsg, "restricted location") || strings.Contains(lowerMsg, "service unavailable from a restricted location") {
		return fmt.Errorf("binance api error: {\"code\":%d,\"msg\":%q} (当前服务器或出口 IP 位于 Binance 限制地区，接口请求会被直接拒绝；请切换到 Binance 允许地区的网络/代理，或改用其他交易所)", code, msg)
	}
	if code == -4411 {
		return fmt.Errorf("binance api error: {\"code\":%d,\"msg\":%q} (需要在币安签署 TradFi-Perps 合约后才能使用 USDM fapi 下单)", code, msg)
	}
	if code == -4120 {
		return fmt.Errorf("binance api error: {\"code\":%d,\"msg\":%q} (币安已将 USDM 条件单迁移到 Algo Order 接口，需要使用 /fapi/v1/algoOrder)", code, msg)
	}
	return fmt.Errorf("binance api error: {\"code\":%d,\"msg\":%q}", code, msg)
}

func (b *BinanceExchange) signedRequest(ctx context.Context, cred binanceCred, method, path string, params url.Values) ([]byte, int, error) {
	if params == nil {
		params = url.Values{}
	}

	// Honor ban window if set
	if !b.requestBanUntil.IsZero() && time.Now().Before(b.requestBanUntil) {
		return nil, 0, fmt.Errorf("binance api error: {\"code\":429,\"msg\":\"Rate limited\"}")
	}
	_ = b.loadRateLimitIfNeeded()

	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	if params.Get("recvWindow") == "" {
		params.Set("recvWindow", "5000")
	}

	query := params.Encode()
	mac := hmac.New(sha256.New, []byte(cred.APISecret))
	mac.Write([]byte(query))
	signature := hex.EncodeToString(mac.Sum(nil))
	query = query + "&signature=" + signature

	u := b.apiBaseURL(cred) + path + "?" + query
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-MBX-APIKEY", cred.APIKey)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	// Track used weight
	if w := strings.TrimSpace(resp.Header.Get("X-MBX-USED-WEIGHT-1m")); w != "" {
		if v, err := strconv.Atoi(w); err == nil {
			b.usedWeight1m = v
		}
	}
	// If approaching limit, small cooperative delay
	if b.rateLimitWeight1m > 0 && b.usedWeight1m*100/b.rateLimitWeight1m >= 80 {
		time.Sleep(200 * time.Millisecond)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		if code, msg, ok := binanceAPIError(body); ok {
			// 429 handling: set ban window using Retry-After or body.retryAfter
			if resp.StatusCode == 429 || code == 429 || code == -1003 {
				retryAfter := int64(0)
				if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
					if v, err := strconv.ParseInt(ra, 10, 64); err == nil {
						// Retry-After may be seconds
						retryAfter = time.Now().Add(time.Duration(v) * time.Second).UnixMilli()
					}
				}
				if retryAfter == 0 {
					var obj map[string]interface{}
					if err := json.Unmarshal(body, &obj); err == nil {
						switch t := obj["retryAfter"].(type) {
						case float64:
							retryAfter = int64(t)
						case int64:
							retryAfter = t
						}
					}
				}
				if retryAfter > 0 {
					b.requestBanUntil = time.UnixMilli(retryAfter)
				} else {
					b.requestBanUntil = time.Now().Add(2 * time.Minute)
				}
			}
			return body, resp.StatusCode, formatBinanceAPIError(code, msg)
		}
		return body, resp.StatusCode, fmt.Errorf("binance api error: %s", string(body))
	}
	return body, resp.StatusCode, nil
}

func (b *BinanceExchange) publicRequest(ctx context.Context, path string, params url.Values) ([]byte, int, error) {
	if !b.requestBanUntil.IsZero() && time.Now().Before(b.requestBanUntil) {
		return nil, 0, fmt.Errorf("binance api error: {\"code\":429,\"msg\":\"Rate limited\"}")
	}
	_ = b.loadRateLimitIfNeeded()
	u := b.baseURL + path
	if params != nil && len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if w := strings.TrimSpace(resp.Header.Get("X-MBX-USED-WEIGHT-1m")); w != "" {
		if v, err := strconv.Atoi(w); err == nil {
			b.usedWeight1m = v
		}
	}
	if b.rateLimitWeight1m > 0 && b.usedWeight1m*100/b.rateLimitWeight1m >= 80 {
		time.Sleep(200 * time.Millisecond)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		if code, msg, ok := binanceAPIError(body); ok {
			if resp.StatusCode == 429 || code == 429 || code == -1003 {
				retryAfter := int64(0)
				if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
					if v, err := strconv.ParseInt(ra, 10, 64); err == nil {
						retryAfter = time.Now().Add(time.Duration(v) * time.Second).UnixMilli()
					}
				}
				if retryAfter == 0 {
					var obj map[string]interface{}
					if err := json.Unmarshal(body, &obj); err == nil {
						switch t := obj["retryAfter"].(type) {
						case float64:
							retryAfter = int64(t)
						case int64:
							retryAfter = t
						}
					}
				}
				if retryAfter > 0 {
					b.requestBanUntil = time.UnixMilli(retryAfter)
				} else {
					b.requestBanUntil = time.Now().Add(2 * time.Minute)
				}
			}
			return body, resp.StatusCode, formatBinanceAPIError(code, msg)
		}
		return body, resp.StatusCode, fmt.Errorf("binance api error: %s", string(body))
	}
	return body, resp.StatusCode, nil
}

func (b *BinanceExchange) loadRateLimitIfNeeded() error {
	// Load once per hour
	if !b.lastRateLimitLoad.IsZero() && time.Since(b.lastRateLimitLoad) < time.Hour && b.rateLimitWeight1m > 0 {
		return nil
	}
	endpoint := "/api/v3/exchangeInfo"
	if b.market == "usdm" {
		endpoint = "/fapi/v1/exchangeInfo"
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, b.baseURL+endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("binance api error: %s", string(body))
	}
	var parsed struct {
		RateLimits []struct {
			RateLimitType string `json:"rateLimitType"`
			Interval      string `json:"interval"`
			IntervalNum   int    `json:"intervalNum"`
			Limit         int    `json:"limit"`
		} `json:"rateLimits"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		for _, rl := range parsed.RateLimits {
			if strings.EqualFold(rl.RateLimitType, "REQUEST_WEIGHT") && strings.EqualFold(rl.Interval, "MINUTE") {
				b.rateLimitWeight1m = rl.Limit
				break
			}
		}
	}
	b.lastRateLimitLoad = time.Now()
	return nil
}
func (b *BinanceExchange) FetchCandles(symbol string, timeframe string, limit int) ([]Candle, error) {
	interval, err := binanceInterval(timeframe)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	sym := binanceSymbol(symbol)
	params := url.Values{}
	params.Set("symbol", sym)
	params.Set("interval", interval)
	params.Set("limit", strconv.Itoa(limit))

	path := "/api/v3/klines"
	if b.market == "usdm" {
		path = "/fapi/v1/klines"
	}

	body, _, err := b.publicRequest(context.Background(), path, params)
	if err != nil {
		return nil, err
	}

	var raw [][]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]Candle, 0, len(raw))
	for _, k := range raw {
		if len(k) < 6 {
			continue
		}
		openTimeMs, _ := toInt64(k[0])
		open, _ := toFloat64(k[1])
		high, _ := toFloat64(k[2])
		low, _ := toFloat64(k[3])
		closeP, _ := toFloat64(k[4])
		vol, _ := toFloat64(k[5])
		out = append(out, Candle{
			Timestamp: time.UnixMilli(openTimeMs),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closeP,
			Volume:    vol,
		})
	}
	return out, nil
}

func (b *BinanceExchange) FetchHistoricalCandles(symbol string, timeframe string, startTime, endTime time.Time) ([]Candle, error) {
	interval, err := binanceInterval(timeframe)
	if err != nil {
		return nil, err
	}

	sym := binanceSymbol(symbol)
	var all []Candle
	startMs := startTime.UnixMilli()
	endMs := endTime.UnixMilli()

	for {
		params := url.Values{}
		params.Set("symbol", sym)
		params.Set("interval", interval)
		params.Set("startTime", strconv.FormatInt(startMs, 10))
		params.Set("endTime", strconv.FormatInt(endMs, 10))
		params.Set("limit", "1000")

		path := "/api/v3/klines"
		if b.market == "usdm" {
			path = "/fapi/v1/klines"
		}
		body, _, err := b.publicRequest(context.Background(), path, params)
		if err != nil {
			return nil, err
		}

		var raw [][]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			break
		}

		for _, k := range raw {
			if len(k) < 6 {
				continue
			}
			openTimeMs, _ := toInt64(k[0])
			open, _ := toFloat64(k[1])
			high, _ := toFloat64(k[2])
			low, _ := toFloat64(k[3])
			closeP, _ := toFloat64(k[4])
			vol, _ := toFloat64(k[5])
			ts := time.UnixMilli(openTimeMs)
			if ts.Before(startTime) || ts.After(endTime) {
				continue
			}
			all = append(all, Candle{
				Timestamp: ts,
				Open:      open,
				High:      high,
				Low:       low,
				Close:     closeP,
				Volume:    vol,
			})
		}

		lastOpenTimeMs, _ := toInt64(raw[len(raw)-1][0])
		next := lastOpenTimeMs + 1
		if next <= startMs {
			break
		}
		if next >= endMs {
			break
		}
		startMs = next
		if len(raw) < 1000 {
			break
		}
	}

	return all, nil
}

func toFloat64(v interface{}) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case string:
		return strconv.ParseFloat(t, 64)
	case json.Number:
		return t.Float64()
	default:
		return 0, fmt.Errorf("unsupported number type")
	}
}

func toInt64(v interface{}) (int64, error) {
	switch t := v.(type) {
	case float64:
		return int64(t), nil
	case string:
		return strconv.ParseInt(t, 10, 64)
	case json.Number:
		return t.Int64()
	default:
		return 0, fmt.Errorf("unsupported int type")
	}
}

func normalizeNewClientOrderID(v string) string {
	if strings.TrimSpace(v) == "" {
		return models.GenerateUUID()
	}
	out := make([]rune, 0, len(v))
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		} else {
			out = append(out, '-')
		}
		if len(out) >= 36 {
			break
		}
	}
	s := strings.Trim(string(out), "-_")
	if s == "" {
		return models.GenerateUUID()
	}
	if len(s) > 36 {
		return s[:36]
	}
	return s
}

func (b *BinanceExchange) SetLeverage(ownerID uint, symbol string, leverage int) error {
	if b.market != "usdm" {
		return nil
	}
	if leverage < 1 {
		return nil
	}
	if leverage > 125 {
		leverage = 125
	}

	cred, err := b.getCred(ownerID)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%d:%s", ownerID, binanceSymbol(symbol))
	b.leverageMu.Lock()
	if v, ok := b.leverageByKey[key]; ok && v == leverage {
		b.leverageMu.Unlock()
		return nil
	}
	b.leverageMu.Unlock()

	params := url.Values{}
	params.Set("symbol", binanceSymbol(symbol))
	params.Set("leverage", strconv.Itoa(leverage))
	_, _, err = b.signedRequest(context.Background(), cred, http.MethodPost, "/fapi/v1/leverage", params)
	if err != nil {
		return err
	}

	b.leverageMu.Lock()
	b.leverageByKey[key] = leverage
	b.leverageMu.Unlock()
	return nil
}

func (b *BinanceExchange) PlaceOrder(ownerID uint, clientOrderID string, symbol string, side string, amount float64, price float64) (*Order, error) {
	cred, err := b.getCred(ownerID)
	if err != nil {
		return nil, err
	}

	filters, err := b.getFilters(symbol)
	if err != nil {
		return nil, err
	}

	sym := binanceSymbol(symbol)
	s := strings.ToUpper(side)
	if s == "BUY" || s == "SELL" {
	} else if side == "buy" {
		s = "BUY"
	} else if side == "sell" {
		s = "SELL"
	} else {
		return nil, fmt.Errorf("invalid side: %s", side)
	}

	qty := math.Abs(amount)
	if qty == 0 {
		return nil, fmt.Errorf("amount must be > 0")
	}

	params := url.Values{}
	params.Set("symbol", sym)
	params.Set("side", s)
	if b.market != "usdm" {
		params.Set("newOrderRespType", "RESULT")
	}
	params.Set("newClientOrderId", normalizeNewClientOrderID(clientOrderID))
	if price > 0 {
		params.Set("type", "LIMIT")
		params.Set("timeInForce", "GTC")
		adjPrice := roundDownPrice(price, filters.TickSize)
		adjQty := roundDownToStep(qty, filters.StepSize)
		if filters.MinQty > 0 && adjQty < filters.MinQty {
			return nil, fmt.Errorf("quantity too small")
		}
		minNotional := filters.MinNotional
		if b.market == "usdm" && minNotional < 5 {
			minNotional = 5
		}
		if minNotional > 0 && adjQty*adjPrice < minNotional {
			return nil, fmt.Errorf("notional too small")
		}
		params.Set("quantity", formatByStep(adjQty, filters.StepSize))
		params.Set("price", formatByStep(adjPrice, filters.TickSize))
	} else {
		params.Set("type", "MARKET")
		adjQty := roundDownToStep(qty, filters.StepSize)
		if filters.MinQty > 0 && adjQty < filters.MinQty {
			return nil, fmt.Errorf("quantity too small")
		}
		if b.market == "usdm" || filters.MinNotional > 0 {
			px, err := b.LastPrice(symbol)
			if err == nil && px > 0 {
				minNotional := filters.MinNotional
				if b.market == "usdm" && minNotional < 5 {
					minNotional = 5
				}
				if adjQty*px < minNotional {
					needQty := roundUpToStep(minNotional/px, filters.StepSize)
					if filters.MinQty > 0 && needQty < filters.MinQty {
						needQty = filters.MinQty
					}
					adjQty = needQty
				}
			}
		}
		params.Set("quantity", formatByStep(adjQty, filters.StepSize))
	}

	orderPath := "/api/v3/order"
	if b.market == "usdm" {
		orderPath = "/fapi/v1/order"
	}
	body, _, err := b.signedRequest(context.Background(), cred, http.MethodPost, orderPath, params)
	if err != nil {
		return nil, err
	}

	if b.market == "usdm" {
		var resp struct {
			OrderID       int64  `json:"orderId"`
			ClientOrderID string `json:"clientOrderId"`
			Symbol        string `json:"symbol"`
			Side          string `json:"side"`
			Price         string `json:"price"`
			OrigQty       string `json:"origQty"`
			ExecutedQty   string `json:"executedQty"`
			AvgPrice      string `json:"avgPrice"`
			Status        string `json:"status"`
			UpdateTime    int64  `json:"updateTime"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}

		orderID := strconv.FormatInt(resp.OrderID, 10)
		clientID := resp.ClientOrderID
		status := strings.ToLower(resp.Status)

		px, _ := strconv.ParseFloat(resp.Price, 64)
		avgPx, _ := strconv.ParseFloat(resp.AvgPrice, 64)
		origQty, _ := strconv.ParseFloat(resp.OrigQty, 64)
		executedQty, _ := strconv.ParseFloat(resp.ExecutedQty, 64)

		if status != "filled" || avgPx == 0 || executedQty == 0 {
			if refreshed, err := b.waitUSDMOrderFinal(cred, sym, clientID, orderID); err == nil && refreshed != nil {
				status = strings.ToLower(refreshed.Status)
				if v, err := strconv.ParseFloat(refreshed.AvgPrice, 64); err == nil && v > 0 {
					avgPx = v
				}
				if v, err := strconv.ParseFloat(refreshed.ExecutedQty, 64); err == nil && v > 0 {
					executedQty = v
				}
				if v, err := strconv.ParseFloat(refreshed.OrigQty, 64); err == nil && v > 0 {
					origQty = v
				}
				if refreshed.UpdateTime > 0 {
					resp.UpdateTime = refreshed.UpdateTime
				}
			}
		}

		aq := origQty
		if executedQty > 0 {
			aq = executedQty
		}
		if avgPx > 0 {
			px = avgPx
		}
		if strings.ToLower(resp.Status) == "partially_filled" && executedQty > 0 {
			status = "filled"
		}
		ts := time.Now()
		if resp.UpdateTime > 0 {
			ts = time.UnixMilli(resp.UpdateTime)
		}

		return &Order{
			ID:            orderID,
			ClientOrderID: clientID,
			Symbol:        symbol,
			Side:          strings.ToLower(resp.Side),
			Amount:        aq,
			Price:         px,
			Status:        status,
			Timestamp:     ts,
		}, nil
	}

	var resp struct {
		OrderID             int64  `json:"orderId"`
		ClientOrderID       string `json:"clientOrderId"`
		Symbol              string `json:"symbol"`
		Side                string `json:"side"`
		Price               string `json:"price"`
		OrigQty             string `json:"origQty"`
		ExecutedQty         string `json:"executedQty"`
		CummulativeQuoteQty string `json:"cummulativeQuoteQty"`
		Status              string `json:"status"`
		TransactTime        int64  `json:"transactTime"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	px, _ := strconv.ParseFloat(resp.Price, 64)
	origQty, _ := strconv.ParseFloat(resp.OrigQty, 64)
	executedQty, _ := strconv.ParseFloat(resp.ExecutedQty, 64)
	quoteQty, _ := strconv.ParseFloat(resp.CummulativeQuoteQty, 64)

	aq := origQty
	if executedQty > 0 {
		aq = executedQty
	}
	if executedQty > 0 && quoteQty > 0 {
		px = quoteQty / executedQty
	}
	ts := time.Now()
	if resp.TransactTime > 0 {
		ts = time.UnixMilli(resp.TransactTime)
	}

	return &Order{
		ID:            strconv.FormatInt(resp.OrderID, 10),
		ClientOrderID: resp.ClientOrderID,
		Symbol:        symbol,
		Side:          strings.ToLower(resp.Side),
		Amount:        aq,
		Price:         px,
		Status:        strings.ToLower(resp.Status),
		Timestamp:     ts,
	}, nil
}

type usdmOrderFinal struct {
	Status      string `json:"status"`
	OrigQty     string `json:"origQty"`
	ExecutedQty string `json:"executedQty"`
	AvgPrice    string `json:"avgPrice"`
	UpdateTime  int64  `json:"updateTime"`
}

func (b *BinanceExchange) waitUSDMOrderFinal(cred binanceCred, symbol string, clientOrderID string, orderID string) (*usdmOrderFinal, error) {
	if b.market != "usdm" {
		return nil, nil
	}
	if symbol == "" {
		return nil, nil
	}

	var last usdmOrderFinal
	for i := 0; i < 30; i++ {
		params := url.Values{}
		params.Set("symbol", symbol)
		if strings.TrimSpace(clientOrderID) != "" {
			params.Set("origClientOrderId", clientOrderID)
		} else if strings.TrimSpace(orderID) != "" {
			params.Set("orderId", orderID)
		}

		body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v1/order", params)
		if err != nil {
			return nil, err
		}

		var parsed usdmOrderFinal
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, err
		}
		last = parsed

		st := strings.ToLower(parsed.Status)
		if st == "filled" || st == "canceled" || st == "rejected" || st == "expired" || st == "partially_filled" {
			return &parsed, nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return &last, nil
}

func (b *BinanceExchange) FetchOrders(ownerID uint, symbol string) ([]Order, error) {
	cred, err := b.getCred(ownerID)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("symbol", binanceSymbol(symbol))
	params.Set("limit", "100")

	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/api/v3/allOrders", params)
	if err != nil {
		return nil, err
	}

	var raw []struct {
		OrderID int64  `json:"orderId"`
		Symbol  string `json:"symbol"`
		Side    string `json:"side"`
		Price   string `json:"price"`
		OrigQty string `json:"origQty"`
		Status  string `json:"status"`
		Time    int64  `json:"time"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	out := make([]Order, 0, len(raw))
	for _, r := range raw {
		px, _ := strconv.ParseFloat(r.Price, 64)
		aq, _ := strconv.ParseFloat(r.OrigQty, 64)
		out = append(out, Order{
			ID:        strconv.FormatInt(r.OrderID, 10),
			Symbol:    symbol,
			Side:      strings.ToLower(r.Side),
			Amount:    aq,
			Price:     px,
			Status:    strings.ToLower(r.Status),
			Timestamp: time.UnixMilli(r.Time),
		})
	}
	return out, nil
}

func (b *BinanceExchange) FetchPositions(ownerID uint, status string) ([]Position, error) {
	if status != "active" {
		return []Position{}, nil
	}

	now := time.Now()
	b.positionsCacheMu.Lock()
	if exp, ok := b.positionsCacheExp[ownerID]; ok && now.Before(exp) {
		cached := b.positionsCache[ownerID]
		out := append([]Position(nil), cached...)
		b.positionsCacheMu.Unlock()
		return out, nil
	}
	b.positionsCacheMu.Unlock()

	cred, err := b.getCred(ownerID)
	if err != nil {
		return nil, err
	}

	if b.market == "usdm" {
		body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v2/positionRisk", nil)
		if err != nil {
			return nil, err
		}

		var raw []struct {
			Symbol           string `json:"symbol"`
			PositionAmt      string `json:"positionAmt"`
			EntryPrice       string `json:"entryPrice"`
			MarkPrice        string `json:"markPrice"`
			Leverage         string `json:"leverage"`
			UnrealizedProfit string `json:"unRealizedProfit"`
			UpdateTime       int64  `json:"updateTime"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}

		out := make([]Position, 0)
		for _, p := range raw {
			amt, _ := strconv.ParseFloat(p.PositionAmt, 64)
			if amt == 0 {
				continue
			}
			dir := "long"
			if amt < 0 {
				dir = "short"
			}
			entry, _ := strconv.ParseFloat(p.EntryPrice, 64)
			mark, _ := strconv.ParseFloat(p.MarkPrice, 64)
			unpnl, _ := strconv.ParseFloat(p.UnrealizedProfit, 64)
			lev, _ := strconv.ParseFloat(p.Leverage, 64)
			entryValue := math.Abs(amt) * entry
			ret := 0.0
			if entryValue > 0 {
				// ROI = unrealizedPnL / initialMargin, initialMargin ~= notional/leverage
				if lev > 0 {
					ret = (unpnl / (entryValue / lev)) * 100
				} else {
					ret = (unpnl / entryValue) * 100
				}
			}
			ts := time.Now()
			if p.UpdateTime > 0 {
				ts = time.UnixMilli(p.UpdateTime)
			}
			out = append(out, Position{
				Symbol:        b.displaySymbol(p.Symbol),
				Direction:     dir,
				Amount:        math.Abs(amt),
				Price:         entry,
				CurrentPrice:  mark,
				UnrealizedPnL: unpnl,
				ReturnRate:    ret,
				StrategyName:  "",
				ExchangeName:  b.name,
				Status:        "active",
				OwnerID:       ownerID,
				OpenTime:      ts,
			})
		}

		b.positionsCacheMu.Lock()
		b.positionsCache[ownerID] = append([]Position(nil), out...)
		b.positionsCacheExp[ownerID] = time.Now().Add(5 * time.Second)
		b.positionsCacheMu.Unlock()
		return out, nil
	}

	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/api/v3/account", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	out := make([]Position, 0)
	now2 := time.Now()
	for _, bal := range resp.Balances {
		free, _ := strconv.ParseFloat(bal.Free, 64)
		locked, _ := strconv.ParseFloat(bal.Locked, 64)
		amt := free + locked
		if amt <= 0 {
			continue
		}
		if strings.EqualFold(bal.Asset, "USDT") {
			continue
		}
		symbol := strings.ToUpper(bal.Asset) + "/USDT"
		if _, err := b.getFilters(symbol); err != nil {
			continue
		}
		out = append(out, Position{
			Symbol:       symbol,
			Direction:    "long",
			Amount:       amt,
			Price:        0,
			StrategyName: "",
			ExchangeName: b.name,
			Status:       "active",
			OwnerID:      ownerID,
			OpenTime:     now2,
		})
	}
	b.positionsCacheMu.Lock()
	b.positionsCache[ownerID] = append([]Position(nil), out...)
	b.positionsCacheExp[ownerID] = time.Now().Add(5 * time.Second)
	b.positionsCacheMu.Unlock()
	return out, nil
}

func (b *BinanceExchange) USDMPositionInfo(ownerID uint, symbol string) (float64, float64, float64, float64, error) {
	cred, err := b.getCred(ownerID)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v2/positionRisk", nil)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	var raw []struct {
		Symbol      string `json:"symbol"`
		PositionAmt string `json:"positionAmt"`
		EntryPrice  string `json:"entryPrice"`
		MarkPrice   string `json:"markPrice"`
		Leverage    string `json:"leverage"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, 0, 0, 0, err
	}
	target := binanceSymbol(symbol)
	var bestAmt, bestEntry, bestMark, bestLev float64
	for _, p := range raw {
		if strings.EqualFold(p.Symbol, target) {
			amt, _ := strconv.ParseFloat(p.PositionAmt, 64)
			entry, _ := strconv.ParseFloat(p.EntryPrice, 64)
			mark, _ := strconv.ParseFloat(p.MarkPrice, 64)
			lev, _ := strconv.ParseFloat(p.Leverage, 64)
			if amt != 0 {
				return amt, entry, mark, lev, nil
			}
			bestAmt = amt
			bestEntry = entry
			bestMark = mark
			bestLev = lev
		}
	}
	return bestAmt, bestEntry, bestMark, bestLev, nil
}

func (b *BinanceExchange) usdmPositionSideMode(cred binanceCred) (bool, error) {
	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v1/positionSide/dual", nil)
	if err != nil {
		return false, err
	}
	var resp struct {
		DualSidePosition bool `json:"dualSidePosition"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, err
	}
	return resp.DualSidePosition, nil
}

func (b *BinanceExchange) ClosePosition(symbol string, ownerID uint) error {
	cred, err := b.getCred(ownerID)
	if err != nil {
		return err
	}

	if b.market == "usdm" {
		_, _, _, err := b.ClosePositionOrder(symbol, ownerID)
		return err
	}

	filters, err := b.getFilters(symbol)
	if err != nil {
		return err
	}

	sym := strings.ToUpper(symbol)
	parts := strings.Split(sym, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid symbol: %s", symbol)
	}
	baseAsset := parts[0]

	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/api/v3/account", nil)
	if err != nil {
		return err
	}

	var resp struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return err
	}

	var freeAmt float64
	for _, bal := range resp.Balances {
		if strings.EqualFold(bal.Asset, baseAsset) {
			freeAmt, _ = strconv.ParseFloat(bal.Free, 64)
			break
		}
	}
	if freeAmt <= 0 {
		return nil
	}

	adjQty := roundDownToStep(freeAmt, filters.StepSize)
	if filters.MinQty > 0 && adjQty < filters.MinQty {
		return nil
	}

	params := url.Values{}
	params.Set("symbol", binanceSymbol(symbol))
	params.Set("side", "SELL")
	params.Set("type", "MARKET")
	params.Set("quantity", formatByStep(adjQty, filters.StepSize))
	_, _, err = b.signedRequest(context.Background(), cred, http.MethodPost, "/api/v3/order", params)
	return err
}

func (b *BinanceExchange) ClosePositionOrder(symbol string, ownerID uint) (*Order, float64, float64, error) {
	if b.market != "usdm" {
		return nil, 0, 0, fmt.Errorf("ClosePositionOrder only supported for usdm market")
	}

	cred, err := b.getCred(ownerID)
	if err != nil {
		return nil, 0, 0, err
	}

	filters, err := b.getFilters(symbol)
	if err != nil {
		return nil, 0, 0, err
	}

	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v2/positionRisk", nil)
	if err != nil {
		return nil, 0, 0, err
	}

	var raw []struct {
		Symbol      string `json:"symbol"`
		PositionAmt string `json:"positionAmt"`
		EntryPrice  string `json:"entryPrice"`
		UpdateTime  int64  `json:"updateTime"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, 0, err
	}

	target := binanceSymbol(symbol)
	var positionAmt float64
	var entryPrice float64
	var updateTime int64
	for _, p := range raw {
		if strings.EqualFold(p.Symbol, target) {
			amt, _ := strconv.ParseFloat(p.PositionAmt, 64)
			if amt != 0 {
				positionAmt = amt
				entryPrice, _ = strconv.ParseFloat(p.EntryPrice, 64)
				updateTime = p.UpdateTime
				break
			}
			// Keep 0 as fallback if no active position is found
			positionAmt = amt
			entryPrice, _ = strconv.ParseFloat(p.EntryPrice, 64)
			updateTime = p.UpdateTime
		}
	}
	if positionAmt == 0 {
		return nil, 0, 0, nil
	}

	side := "SELL"
	qty := math.Abs(positionAmt)
	if positionAmt < 0 {
		side = "BUY"
	}
	dualSideMode, err := b.usdmPositionSideMode(cred)
	if err != nil {
		return nil, entryPrice, 0, err
	}
	positionSide := "BOTH"
	if dualSideMode {
		if positionAmt < 0 {
			positionSide = "SHORT"
		} else {
			positionSide = "LONG"
		}
	}

	adjQty := roundDownToStep(qty, filters.StepSize)
	if filters.MinQty > 0 && adjQty < filters.MinQty {
		return nil, entryPrice, 0, nil
	}

	clientOrderID := normalizeNewClientOrderID(models.GenerateUUID())
	params := url.Values{}
	params.Set("symbol", target)
	params.Set("side", side)
	params.Set("type", "MARKET")
	params.Set("positionSide", positionSide)
	if !dualSideMode {
		params.Set("reduceOnly", "true")
	}
	params.Set("newClientOrderId", clientOrderID)
	params.Set("quantity", formatByStep(adjQty, filters.StepSize))

	orderBody, _, err := b.signedRequest(context.Background(), cred, http.MethodPost, "/fapi/v1/order", params)
	if err != nil {
		return nil, entryPrice, 0, err
	}

	var resp struct {
		OrderID       int64  `json:"orderId"`
		ClientOrderID string `json:"clientOrderId"`
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		OrigQty       string `json:"origQty"`
		ExecutedQty   string `json:"executedQty"`
		AvgPrice      string `json:"avgPrice"`
		Status        string `json:"status"`
		UpdateTime    int64  `json:"updateTime"`
	}
	if err := json.Unmarshal(orderBody, &resp); err != nil {
		return nil, entryPrice, 0, err
	}

	orderID := strconv.FormatInt(resp.OrderID, 10)
	final := &usdmOrderFinal{
		Status:      resp.Status,
		OrigQty:     resp.OrigQty,
		ExecutedQty: resp.ExecutedQty,
		AvgPrice:    resp.AvgPrice,
		UpdateTime:  resp.UpdateTime,
	}
	if strings.ToLower(final.Status) != "filled" || strings.TrimSpace(final.AvgPrice) == "" || strings.TrimSpace(final.ExecutedQty) == "" {
		if refreshed, err := b.waitUSDMOrderFinal(cred, target, resp.ClientOrderID, orderID); err == nil && refreshed != nil {
			final = refreshed
		}
	}

	executedQty, _ := strconv.ParseFloat(final.ExecutedQty, 64)
	avgPx, _ := strconv.ParseFloat(final.AvgPrice, 64)
	ts := time.Now()
	if final.UpdateTime > 0 {
		ts = time.UnixMilli(final.UpdateTime)
	} else if resp.UpdateTime > 0 {
		ts = time.UnixMilli(resp.UpdateTime)
	} else if updateTime > 0 {
		ts = time.UnixMilli(updateTime)
	}

	return &Order{
		ID:            orderID,
		ClientOrderID: resp.ClientOrderID,
		Symbol:        symbol,
		Side:          strings.ToLower(resp.Side),
		Amount:        executedQty,
		Price:         avgPx,
		Status:        strings.ToLower(final.Status),
		Timestamp:     ts,
	}, entryPrice, positionAmt, nil
}

// CancelPrePositionOpenOrders cancels open orders that could open a new position (non-reduceOnly).
// It is triggered after a cancellation event to ensure no stale entry orders remain for the symbol.
func (b *BinanceExchange) CancelPrePositionOpenOrders(ownerID uint, symbol string) error {
	cred, err := b.getCred(ownerID)
	if err != nil {
		return err
	}
	if b.market == "usdm" {
		params := url.Values{}
		params.Set("symbol", binanceSymbol(symbol))
		body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v1/openOrders", params)
		if err != nil {
			return err
		}
		var orders []struct {
			OrderID     int64  `json:"orderId"`
			ClientOrder string `json:"clientOrderId"`
			ReduceOnly  bool   `json:"reduceOnly"`
			Side        string `json:"side"`
			Type        string `json:"type"`
			Status      string `json:"status"`
			OrigQty     string `json:"origQty"`
			Price       string `json:"price"`
		}
		if err := json.Unmarshal(body, &orders); err != nil {
			return err
		}
		for _, o := range orders {
			if o.ReduceOnly {
				continue
			}
			// cancel this open entry order
			q := url.Values{}
			q.Set("symbol", binanceSymbol(symbol))
			q.Set("orderId", strconv.FormatInt(o.OrderID, 10))
			_, _, _ = b.signedRequest(context.Background(), cred, http.MethodDelete, "/fapi/v1/order", q)
		}
		// If no position, also cancel remaining conditional (algo) orders for this symbol
		if amt, _, _, err := b.USDMPositionAmt(ownerID, symbol); err == nil && amt == 0 {
			_ = b.CancelUSDMAlgoOpenOrders(ownerID, symbol)
		}
		return nil
	}
	// spot: cancel all open orders for this symbol
	params := url.Values{}
	params.Set("symbol", binanceSymbol(symbol))
	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/api/v3/openOrders", params)
	if err != nil {
		return err
	}
	var orders []struct {
		OrderID int64  `json:"orderId"`
		Status  string `json:"status"`
		Side    string `json:"side"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(body, &orders); err != nil {
		return err
	}
	for _, o := range orders {
		q := url.Values{}
		q.Set("symbol", binanceSymbol(symbol))
		q.Set("orderId", strconv.FormatInt(o.OrderID, 10))
		_, _, _ = b.signedRequest(context.Background(), cred, http.MethodDelete, "/api/v3/order", q)
	}
	return nil
}

type USDMCancelSummary struct {
	NormalFound    int
	NormalCanceled int
	AlgoFound      int
	AlgoCanceled   int
}

func (b *BinanceExchange) CancelUSDMAllSymbolOrders(ownerID uint, symbol string) error {
	_, err := b.CancelUSDMAllSymbolOrdersDetailed(ownerID, symbol)
	return err
}

func (b *BinanceExchange) CancelUSDMAllSymbolOrdersDetailed(ownerID uint, symbol string) (USDMCancelSummary, error) {
	summary := USDMCancelSummary{}
	if b.market != "usdm" {
		return summary, nil
	}
	cred, err := b.getCred(ownerID)
	if err != nil {
		return summary, err
	}
	params := url.Values{}
	params.Set("symbol", binanceSymbol(symbol))
	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v1/openOrders", params)
	if err != nil {
		return summary, err
	}
	var orders []struct {
		OrderID int64 `json:"orderId"`
	}
	if err := json.Unmarshal(body, &orders); err != nil {
		return summary, err
	}
	summary.NormalFound = len(orders)
	var firstErr error
	for _, o := range orders {
		if o.OrderID <= 0 {
			continue
		}
		q := url.Values{}
		q.Set("symbol", binanceSymbol(symbol))
		q.Set("orderId", strconv.FormatInt(o.OrderID, 10))
		_, _, err := b.signedRequest(context.Background(), cred, http.MethodDelete, "/fapi/v1/order", q)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		summary.NormalCanceled++
	}
	algoOrders, algoErr := b.ListUSDMAlgoOpenOrders(ownerID, symbol)
	if algoErr != nil {
		if firstErr == nil {
			firstErr = algoErr
		}
		return summary, firstErr
	}
	summary.AlgoFound = len(algoOrders)
	for _, o := range algoOrders {
		q := url.Values{}
		q.Set("symbol", binanceSymbol(symbol))
		if o.AlgoID > 0 {
			q.Set("algoId", strconv.FormatInt(o.AlgoID, 10))
		} else if strings.TrimSpace(o.ClientAlgoID) != "" {
			q.Set("clientAlgoId", o.ClientAlgoID)
		} else {
			continue
		}
		_, _, err := b.signedRequest(context.Background(), cred, http.MethodDelete, "/fapi/v1/algoOrder", q)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		summary.AlgoCanceled++
	}
	return summary, firstErr
}

func (b *BinanceExchange) ListUSDMAlgoOpenOrders(ownerID uint, symbol string) ([]USDMAlgoOrder, error) {
	if b.market != "usdm" {
		return nil, nil
	}
	cred, err := b.getCred(ownerID)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("symbol", binanceSymbol(symbol))
	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v1/algoOpenOrders", params)
	if err != nil {
		return nil, err
	}
	var orders []struct {
		AlgoID       int64  `json:"algoId"`
		ClientAlgoID string `json:"clientAlgoId"`
		Symbol       string `json:"symbol"`
		Side         string `json:"side"`
		Type         string `json:"orderType"`
		TriggerPrice string `json:"triggerPrice"`
		Price        string `json:"price"`
	}
	if err := json.Unmarshal(body, &orders); err != nil {
		return nil, err
	}
	out := make([]USDMAlgoOrder, 0, len(orders))
	for _, o := range orders {
		out = append(out, USDMAlgoOrder{
			AlgoID:         o.AlgoID,
			ClientAlgoID:   o.ClientAlgoID,
			Symbol:         o.Symbol,
			Side:           o.Side,
			Type:           o.Type,
			TriggerPrice:   o.TriggerPrice,
			ExecutionPrice: o.Price,
		})
	}
	return out, nil
}

func isUSDMTPSLType(v string) bool {
	t := strings.ToUpper(strings.TrimSpace(v))
	return strings.Contains(t, "TAKE_PROFIT") || strings.Contains(t, "STOP")
}

func (b *BinanceExchange) ListUSDMTPSLOpenOrders(ownerID uint, symbol string) ([]USDMAlgoOrder, error) {
	if b.market != "usdm" {
		return nil, nil
	}
	cred, err := b.getCred(ownerID)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("symbol", binanceSymbol(symbol))
	created := make([]USDMAlgoOrder, 0, 4)

	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v1/openOrders", params)
	if err == nil {
		var orders []struct {
			OrderID       int64  `json:"orderId"`
			ClientOrderID string `json:"clientOrderId"`
			Symbol        string `json:"symbol"`
			Side          string `json:"side"`
			Type          string `json:"type"`
			OrigType      string `json:"origType"`
			StopPrice     string `json:"stopPrice"`
			Price         string `json:"price"`
		}
		if err := json.Unmarshal(body, &orders); err == nil {
			for _, o := range orders {
				typ := o.OrigType
				if strings.TrimSpace(typ) == "" {
					typ = o.Type
				}
				if !isUSDMTPSLType(typ) {
					continue
				}
				created = append(created, USDMAlgoOrder{
					AlgoID:         o.OrderID,
					ClientAlgoID:   o.ClientOrderID,
					Symbol:         o.Symbol,
					Side:           o.Side,
					Type:           typ,
					TriggerPrice:   o.StopPrice,
					ExecutionPrice: o.Price,
				})
			}
		}
	}

	algoOrders, algoErr := b.ListUSDMAlgoOpenOrders(ownerID, symbol)
	if algoErr != nil && err != nil {
		return nil, algoErr
	}
	created = append(created, algoOrders...)
	return created, nil
}

func (b *BinanceExchange) CancelUSDMAlgoOpenOrders(ownerID uint, symbol string) error {
	if b.market != "usdm" {
		return nil
	}
	cred, err := b.getCred(ownerID)
	if err != nil {
		return err
	}
	orders, err := b.ListUSDMAlgoOpenOrders(ownerID, symbol)
	if err != nil {
		return err
	}
	var firstErr error
	for _, o := range orders {
		q := url.Values{}
		q.Set("symbol", binanceSymbol(symbol))
		if o.AlgoID > 0 {
			q.Set("algoId", strconv.FormatInt(o.AlgoID, 10))
		} else if strings.TrimSpace(o.ClientAlgoID) != "" {
			q.Set("clientAlgoId", o.ClientAlgoID)
		} else {
			continue
		}
		_, _, err := b.signedRequest(context.Background(), cred, http.MethodDelete, "/fapi/v1/algoOrder", q)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// USDMMaxNotionalForLeverage returns the symbol-specific maximum notional allowed at the given leverage bracket.
// If the exchange does not return brackets or leverage is out of range, it returns 0.
func (b *BinanceExchange) USDMMaxNotionalForLeverage(ownerID uint, symbol string, leverage int) (float64, error) {
	if b.market != "usdm" {
		return 0, fmt.Errorf("not usdm")
	}
	cred, err := b.getCred(ownerID)
	if err != nil {
		return 0, err
	}
	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v1/leverageBracket", nil)
	if err != nil {
		return 0, err
	}
	var resp []struct {
		Symbol   string `json:"symbol"`
		Brackets []struct {
			InitialLeverage int     `json:"initialLeverage"`
			NotionalCap     float64 `json:"notionalCap"`
			NotionalFloor   float64 `json:"notionalFloor"`
		} `json:"brackets"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}
	target := strings.ToUpper(binanceSymbol(symbol))
	maxCap := 0.0
	for _, e := range resp {
		if strings.ToUpper(e.Symbol) != target {
			continue
		}
		for _, br := range e.Brackets {
			if br.InitialLeverage == leverage {
				return br.NotionalCap, nil
			}
			if br.InitialLeverage >= leverage {
				if maxCap == 0 || br.NotionalCap < maxCap {
					maxCap = br.NotionalCap
				}
			}
		}
		break
	}
	return maxCap, nil
}

func (b *BinanceExchange) USDMPositionAmt(ownerID uint, symbol string) (float64, float64, float64, error) {
	// Try cached positions first to avoid high-frequency polling
	now := time.Now()
	b.positionsCacheMu.Lock()
	if exp, ok := b.positionsCacheExp[ownerID]; ok && now.Before(exp) {
		cached := b.positionsCache[ownerID]
		target := strings.ToUpper(NormalizeSymbol(symbol))
		for _, p := range cached {
			if strings.ToUpper(NormalizeSymbol(p.Symbol)) == target {
				amt := p.Amount
				if strings.EqualFold(p.Direction, "short") {
					amt = -amt
				}
				entry := p.Price
				mark := p.CurrentPrice
				b.positionsCacheMu.Unlock()
				return amt, entry, mark, nil
			}
		}
	}
	b.positionsCacheMu.Unlock()
	cred, err := b.getCred(ownerID)
	if err != nil {
		return 0, 0, 0, err
	}
	body, _, err := b.signedRequest(context.Background(), cred, http.MethodGet, "/fapi/v2/positionRisk", nil)
	if err != nil {
		return 0, 0, 0, err
	}
	var raw []struct {
		Symbol      string `json:"symbol"`
		PositionAmt string `json:"positionAmt"`
		EntryPrice  string `json:"entryPrice"`
		MarkPrice   string `json:"markPrice"`
		UpdateTime  int64  `json:"updateTime"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, 0, 0, err
	}

	target := binanceSymbol(symbol)
	var positionAmt float64
	var entryPrice float64
	var markPrice float64
	for _, p := range raw {
		if strings.EqualFold(p.Symbol, target) {
			amt, _ := strconv.ParseFloat(p.PositionAmt, 64)
			if amt != 0 {
				positionAmt = amt
				entryPrice, _ = strconv.ParseFloat(p.EntryPrice, 64)
				markPrice, _ = strconv.ParseFloat(p.MarkPrice, 64)
				break
			}
			positionAmt = amt
			entryPrice, _ = strconv.ParseFloat(p.EntryPrice, 64)
			markPrice, _ = strconv.ParseFloat(p.MarkPrice, 64)
		}
	}
	return positionAmt, entryPrice, markPrice, nil
}

type USDMAlgoOrder struct {
	Kind           string
	AlgoID         int64
	ClientAlgoID   string
	Symbol         string
	Side           string
	Type           string
	TriggerPrice   string
	ExecutionPrice string
}

func (b *BinanceExchange) PlaceUSDMTPStopOrders(ownerID uint, baseClientOrderID string, symbol string, takeProfit float64, stopLoss float64) ([]USDMAlgoOrder, error) {
	if b.market != "usdm" {
		return nil, fmt.Errorf("tp/sl supported only for usdm")
	}
	cred, err := b.getCred(ownerID)
	if err != nil {
		return nil, err
	}

	filters, err := b.getFilters(symbol)
	if err != nil {
		return nil, err
	}

	positionAmt, entryPrice, markPrice, err := b.USDMPositionAmt(ownerID, symbol)
	if err != nil {
		return nil, err
	}
	if positionAmt == 0 {
		return nil, fmt.Errorf("no open position for symbol")
	}
	dualSideMode, err := b.usdmPositionSideMode(cred)
	if err != nil {
		return nil, err
	}

	sym := binanceSymbol(symbol)
	closeSide := "SELL"
	if positionAmt < 0 {
		closeSide = "BUY"
	}
	positionSide := "BOTH"
	if dualSideMode {
		if positionAmt < 0 {
			positionSide = "SHORT"
		} else {
			positionSide = "LONG"
		}
	}
	closeQty := roundDownToStep(math.Abs(positionAmt), filters.StepSize)
	if filters.MinQty > 0 && closeQty < filters.MinQty {
		closeQty = filters.MinQty
	}

	var firstErr error
	created := make([]USDMAlgoOrder, 0, 2)
	place := func(kind string, orderType string, stopPrice float64) {
		if stopPrice <= 0 {
			return
		}
		params := url.Values{}
		params.Set("algoType", "CONDITIONAL")
		params.Set("symbol", sym)
		params.Set("side", closeSide)
		params.Set("positionSide", positionSide)
		params.Set("timeInForce", "GTC")
		params.Set("workingType", "MARK_PRICE")
		params.Set("priceProtect", "TRUE")
		if !dualSideMode {
			params.Set("reduceOnly", "true")
		}
		params.Set("type", orderType)
		params.Set("orderType", orderType)
		clientID := normalizeNewClientOrderID(kind + "_" + baseClientOrderID)
		params.Set("clientAlgoId", clientID)
		params.Set("quantity", formatByStep(closeQty, filters.StepSize))
		adj := roundDownPrice(stopPrice, filters.TickSize)
		execPrice := adj
		if orderType == "STOP" {
			if closeSide == "BUY" {
				execPrice = roundDownPrice(stopPrice*(1+0.003), filters.TickSize)
			} else {
				execPrice = roundDownPrice(stopPrice*(1-0.003), filters.TickSize)
			}
		} else if orderType == "TAKE_PROFIT" {
			if closeSide == "BUY" {
				execPrice = roundDownPrice(stopPrice*(1+0.001), filters.TickSize)
			} else {
				execPrice = roundDownPrice(stopPrice*(1-0.001), filters.TickSize)
			}
		}
		if execPrice <= 0 {
			execPrice = adj
		}
		orderParams := url.Values{}
		orderParams.Set("symbol", sym)
		orderParams.Set("side", closeSide)
		orderParams.Set("positionSide", positionSide)
		orderParams.Set("type", func() string {
			if orderType == "TAKE_PROFIT" {
				return "TAKE_PROFIT_MARKET"
			}
			return "STOP_MARKET"
		}())
		orderParams.Set("stopPrice", formatByStep(adj, filters.TickSize))
		orderParams.Set("closePosition", "true")
		orderParams.Set("workingType", "MARK_PRICE")
		orderParams.Set("priceProtect", "TRUE")
		orderParams.Set("newClientOrderId", clientID)
		body, _, e := b.signedRequest(context.Background(), cred, http.MethodPost, "/fapi/v1/order", orderParams)
		useAlgoFallback := false
		if e != nil {
			msg := e.Error()
			if strings.Contains(msg, "\"code\":-4120") || strings.Contains(msg, "Algo Order API") || strings.Contains(msg, "algoOrder") {
				useAlgoFallback = true
			} else if firstErr == nil {
				firstErr = e
				return
			} else {
				return
			}
		}
		if useAlgoFallback {
			params.Set("triggerPrice", formatByStep(adj, filters.TickSize))
			params.Set("price", formatByStep(execPrice, filters.TickSize))
			body, _, e = b.signedRequest(context.Background(), cred, http.MethodPost, "/fapi/v1/algoOrder", params)
			if e != nil {
				if firstErr == nil {
					firstErr = e
				}
				return
			}
			var resp struct {
				AlgoID       int64  `json:"algoId"`
				ClientAlgoID string `json:"clientAlgoId"`
				Symbol       string `json:"symbol"`
				Side         string `json:"side"`
				Type         string `json:"orderType"`
				TriggerPrice string `json:"triggerPrice"`
				Price        string `json:"price"`
			}
			if err := json.Unmarshal(body, &resp); err == nil {
				created = append(created, USDMAlgoOrder{
					Kind:           kind,
					AlgoID:         resp.AlgoID,
					ClientAlgoID:   resp.ClientAlgoID,
					Symbol:         resp.Symbol,
					Side:           resp.Side,
					Type:           resp.Type,
					TriggerPrice:   resp.TriggerPrice,
					ExecutionPrice: resp.Price,
				})
			}
			return
		}
		var resp struct {
			OrderID       int64  `json:"orderId"`
			ClientOrderID string `json:"clientOrderId"`
			Symbol        string `json:"symbol"`
			Side          string `json:"side"`
			Type          string `json:"type"`
			OrigType      string `json:"origType"`
			StopPrice     string `json:"stopPrice"`
			Price         string `json:"price"`
		}
		if err := json.Unmarshal(body, &resp); err == nil {
			created = append(created, USDMAlgoOrder{
				Kind:         kind,
				AlgoID:       resp.OrderID,
				ClientAlgoID: resp.ClientOrderID,
				Symbol:       resp.Symbol,
				Side:         resp.Side,
				Type: func() string {
					if strings.TrimSpace(resp.OrigType) != "" {
						return resp.OrigType
					}
					return resp.Type
				}(),
				TriggerPrice:   resp.StopPrice,
				ExecutionPrice: resp.Price,
			})
		}
	}

	ref := markPrice
	if ref <= 0 {
		ref = entryPrice
	}
	if ref > 0 && filters.TickSize > 0 {
		if takeProfit > 0 {
			adj := roundDownPrice(takeProfit, filters.TickSize)
			if closeSide == "SELL" && adj <= ref {
				return nil, fmt.Errorf("tp would immediately trigger")
			}
			if closeSide == "BUY" && adj >= ref {
				return nil, fmt.Errorf("tp would immediately trigger")
			}
		}
		if stopLoss > 0 {
			adj := roundDownPrice(stopLoss, filters.TickSize)
			if closeSide == "SELL" && adj >= ref {
				return nil, fmt.Errorf("sl would immediately trigger")
			}
			if closeSide == "BUY" && adj <= ref {
				return nil, fmt.Errorf("sl would immediately trigger")
			}
		}
	}
	if takeProfit > 0 {
		place("tp", "TAKE_PROFIT", takeProfit)
	}
	if stopLoss > 0 {
		place("sl", "STOP", stopLoss)
	}
	return created, firstErr
}

func (b *BinanceExchange) SubscribeCandles(symbol string, callback func(Candle)) (func(), error) {
	return b.SubscribeCandlesWithEvents(symbol, callback, nil)
}

func (b *BinanceExchange) SubscribeCandlesWithEvents(symbol string, callback func(Candle), onStatus func(event string, detail string, err error)) (func(), error) {
	sym := strings.ToLower(binanceSymbol(symbol))
	stream := sym + "@kline_1m"
	wsURL := b.wsBaseURL + "/ws/" + stream

	stop := make(chan struct{})
	go func() {
		backoff := 1 * time.Second
		maxBackoff := 30 * time.Second
		dialer := websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: 10 * time.Second,
			NetDialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		}
		for {
			select {
			case <-stop:
				return
			default:
			}
			if onStatus != nil {
				onStatus("dialing", wsURL, nil)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			conn, _, err := dialer.DialContext(ctx, wsURL, nil)
			cancel()
			if err != nil {
				log.Printf("[BINANCE WS] kline connect failed symbol=%s url=%s err=%v", symbol, wsURL, err)
				if onStatus != nil {
					onStatus("connect_failed", wsURL, err)
				}
				time.Sleep(backoff)
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}
			log.Printf("[BINANCE WS] kline connected symbol=%s stream=%s", symbol, stream)
			if onStatus != nil {
				onStatus("connected", wsURL, nil)
			}
			backoff = 1 * time.Second
			go func(c *websocket.Conn) {
				<-stop
				_ = c.Close()
			}(conn)

			gotFirst := false
			gotFirstClosed := false
			gotRawFirst := false
			loggedUnmarshalErr := false
			for {
				select {
				case <-stop:
					_ = conn.Close()
					return
				default:
				}
				_, msg, err := conn.ReadMessage()
				if err != nil {
					_ = conn.Close()
					log.Printf("[BINANCE WS] kline disconnected symbol=%s err=%v (reconnect in %s)", symbol, err, backoff)
					if onStatus != nil {
						onStatus("disconnected", wsURL, err)
					}
					time.Sleep(backoff)
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
					break
				}
				if onStatus != nil && !gotRawFirst {
					gotRawFirst = true
					head := msg
					if len(head) > 160 {
						head = head[:160]
					}
					s := strings.ReplaceAll(string(head), "\n", " ")
					s = strings.ReplaceAll(s, "\r", " ")
					onStatus("rx_raw_first", fmt.Sprintf("len=%d head=%s", len(msg), s), nil)
				}
				var payload struct {
					K struct {
						T int64           `json:"t"`
						O json.RawMessage `json:"o"`
						H json.RawMessage `json:"h"`
						L json.RawMessage `json:"l"`
						C json.RawMessage `json:"c"`
						V json.RawMessage `json:"v"`
						X bool            `json:"x"`
					} `json:"k"`
				}
				if err := json.Unmarshal(msg, &payload); err != nil {
					if onStatus != nil && !loggedUnmarshalErr {
						loggedUnmarshalErr = true
						onStatus("unmarshal_failed", err.Error(), err)
					}
					continue
				}
				if onStatus != nil && !gotFirst {
					gotFirst = true
					onStatus("rx_first", fmt.Sprintf("x=%v t=%d c=%s", payload.K.X, payload.K.T, strings.TrimSpace(string(payload.K.C))), nil)
				}
				if onStatus != nil && payload.K.X && !gotFirstClosed {
					gotFirstClosed = true
					onStatus("rx_first_closed", fmt.Sprintf("t=%d c=%s", payload.K.T, strings.TrimSpace(string(payload.K.C))), nil)
				}
				if !payload.K.X {
					continue
				}
				open, _ := parseBinanceNum(payload.K.O)
				high, _ := parseBinanceNum(payload.K.H)
				low, _ := parseBinanceNum(payload.K.L)
				closeP, _ := parseBinanceNum(payload.K.C)
				vol, _ := parseBinanceNum(payload.K.V)
				callback(Candle{
					Timestamp: time.UnixMilli(payload.K.T),
					Open:      open,
					High:      high,
					Low:       low,
					Close:     closeP,
					Volume:    vol,
				})
			}
		}
	}()

	return func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
	}, nil
}
