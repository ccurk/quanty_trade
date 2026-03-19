package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
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

	mu        sync.RWMutex
	credsByID map[uint]binanceCred

	infoOnce sync.Once
	info     binanceExchangeInfoCache

	streamMu    sync.Mutex
	streamsByID map[uint]*binanceUserStream
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
	ex.baseURL = "https://api.binance.com"
	ex.wsBaseURL = "wss://stream.binance.com:9443"
	ex.wsAPIBaseURL = "wss://ws-api.binance.com:443/ws-api/v3"
	ex.streamsByID = make(map[uint]*binanceUserStream)
	return ex
}

func (b *BinanceExchange) GetName() string { return b.name }

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
	if cred.Testnet {
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

func (b *BinanceExchange) signedRequest(ctx context.Context, cred binanceCred, method, path string, params url.Values) ([]byte, int, error) {
	if params == nil {
		params = url.Values{}
	}

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
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("binance api error: %s", string(body))
	}
	return body, resp.StatusCode, nil
}

func (b *BinanceExchange) publicRequest(ctx context.Context, path string, params url.Values) ([]byte, int, error) {
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
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("binance api error: %s", string(body))
	}
	return body, resp.StatusCode, nil
}

func (b *BinanceExchange) FetchCandles(symbol string, timeframe string, limit int) ([]Candle, error) {
	end := time.Now()
	start := end.Add(-time.Duration(limit) * time.Minute)
	return b.FetchHistoricalCandles(symbol, timeframe, start, end)
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

		body, _, err := b.publicRequest(context.Background(), "/api/v3/klines", params)
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
	params.Set("newOrderRespType", "RESULT")
	if clientOrderID != "" {
		params.Set("newClientOrderId", clientOrderID)
	} else {
		params.Set("newClientOrderId", fmt.Sprintf("qt_%d_%d", ownerID, time.Now().UnixNano()))
	}
	if price > 0 {
		params.Set("type", "LIMIT")
		params.Set("timeInForce", "GTC")
		adjPrice := roundDownPrice(price, filters.TickSize)
		adjQty := roundDownToStep(qty, filters.StepSize)
		if filters.MinQty > 0 && adjQty < filters.MinQty {
			return nil, fmt.Errorf("quantity too small")
		}
		if filters.MinNotional > 0 && adjQty*adjPrice < filters.MinNotional {
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
		params.Set("quantity", formatByStep(adjQty, filters.StepSize))
	}

	body, _, err := b.signedRequest(context.Background(), cred, http.MethodPost, "/api/v3/order", params)
	if err != nil {
		return nil, err
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

	cred, err := b.getCred(ownerID)
	if err != nil {
		return nil, err
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
	now := time.Now()
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
			Amount:       amt,
			Price:        0,
			StrategyName: "",
			ExchangeName: b.name,
			Status:       "active",
			OwnerID:      ownerID,
			OpenTime:     now,
		})
	}
	return out, nil
}

func (b *BinanceExchange) ClosePosition(symbol string, ownerID uint) error {
	cred, err := b.getCred(ownerID)
	if err != nil {
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

func (b *BinanceExchange) SubscribeCandles(symbol string, callback func(Candle)) error {
	sym := strings.ToLower(binanceSymbol(symbol))
	stream := sym + "@kline_1m"
	wsURL := b.wsBaseURL + "/ws/" + stream

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return err
	}

	go func() {
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var payload struct {
				K struct {
					T int64  `json:"t"`
					O string `json:"o"`
					H string `json:"h"`
					L string `json:"l"`
					C string `json:"c"`
					V string `json:"v"`
					X bool   `json:"x"`
				} `json:"k"`
			}
			if err := json.Unmarshal(msg, &payload); err != nil {
				continue
			}
			if !payload.K.X {
				continue
			}
			open, _ := strconv.ParseFloat(payload.K.O, 64)
			high, _ := strconv.ParseFloat(payload.K.H, 64)
			low, _ := strconv.ParseFloat(payload.K.L, 64)
			closeP, _ := strconv.ParseFloat(payload.K.C, 64)
			vol, _ := strconv.ParseFloat(payload.K.V, 64)
			callback(Candle{
				Timestamp: time.UnixMilli(payload.K.T),
				Open:      open,
				High:      high,
				Low:       low,
				Close:     closeP,
				Volume:    vol,
			})
		}
	}()

	return nil
}
