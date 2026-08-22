package marketmaker

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// KuCoin spot REST (NOT Binance-compatible): KC-API-* headers, KC-API-SIGN =
// base64(HMAC-SHA256(secret, timestamp+method+endpoint+body)); KC-API-PASSPHRASE
// (key version 2) is itself base64(HMAC-SHA256(secret, passphrase)); symbols use a
// dash (BTC-USDT); post-only via the postOnly flag. Every response is wrapped in
// {code:"200000", data:...}. Verified against KuCoin REST docs (2026-08).
const kucoinDefaultBaseURL = "https://api.kucoin.com"

func init() {
	RegisterExec("kucoin", func(cfg ExecConfig) (ExecExchange, error) {
		base := strings.TrimRight(cfg.BaseURL, "/")
		if base == "" {
			base = kucoinDefaultBaseURL
		}
		return &KucoinExchange{
			baseURL:    base,
			apiKey:     cfg.APIKey,
			secret:     cfg.APISecret,
			passphrase: cfg.Passphrase,
			http:       &http.Client{Timeout: 10 * time.Second},
			filters:    map[string]SymbolFilter{},
		}, nil
	})
}

type KucoinExchange struct {
	baseURL    string
	apiKey     string
	secret     string
	passphrase string
	http       *http.Client

	filterMu     sync.Mutex
	filters      map[string]SymbolFilter
	filtersReady bool
}

func (e *KucoinExchange) Name() string { return "kucoin" }

func kucoinSym(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

func kucoinClientOID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "mm" + hex.EncodeToString(b)
}

func (e *KucoinExchange) b64hmac(msg string) string {
	mac := hmac.New(sha256.New, []byte(e.secret))
	mac.Write([]byte(msg))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// do issues a request. When signed, endpoint MUST already include any query string
// (it is part of the signature).
func (e *KucoinExchange) do(method, endpoint string, body []byte, signed bool) ([]byte, error) {
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, e.baseURL+endpoint, rdr)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if signed {
		if e.apiKey == "" || e.secret == "" || e.passphrase == "" {
			return nil, fmt.Errorf("kucoin: missing api credentials (need api_key/api_secret/passphrase)")
		}
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		req.Header.Set("KC-API-KEY", e.apiKey)
		req.Header.Set("KC-API-SIGN", e.b64hmac(ts+method+endpoint+string(body)))
		req.Header.Set("KC-API-TIMESTAMP", ts)
		req.Header.Set("KC-API-PASSPHRASE", e.b64hmac(e.passphrase))
		req.Header.Set("KC-API-KEY-VERSION", "2")
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("kucoin %s %s -> %d: %s", method, endpoint, resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	// unwrap {code,data}
	var env struct {
		Code string          `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rb, &env); err != nil {
		return nil, err
	}
	if env.Code != "200000" {
		return nil, fmt.Errorf("kucoin %s %s code=%s msg=%s", method, endpoint, env.Code, env.Msg)
	}
	return env.Data, nil
}

func (e *KucoinExchange) FetchBookTicker(symbol string) (BookTicker, error) {
	data, err := e.do(http.MethodGet, "/api/v1/market/orderbook/level1?symbol="+kucoinSym(symbol), nil, false)
	if err != nil {
		return BookTicker{}, err
	}
	var r struct {
		BestBid     string `json:"bestBid"`
		BestBidSize string `json:"bestBidSize"`
		BestAsk     string `json:"bestAsk"`
		BestAskSize string `json:"bestAskSize"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return BookTicker{}, err
	}
	return BookTicker{Symbol: kucoinSym(symbol), BidPx: atof(r.BestBid), BidQty: atof(r.BestBidSize), AskPx: atof(r.BestAsk), AskQty: atof(r.BestAskSize), Ts: time.Now()}, nil
}

func (e *KucoinExchange) SymbolFilter(symbol string) (SymbolFilter, error) {
	sym := kucoinSym(symbol)
	e.filterMu.Lock()
	if e.filtersReady {
		f, ok := e.filters[sym]
		e.filterMu.Unlock()
		if ok {
			return f, nil
		}
		return SymbolFilter{}, fmt.Errorf("kucoin: symbol %s not found", sym)
	}
	e.filterMu.Unlock()

	data, err := e.do(http.MethodGet, "/api/v1/symbols", nil, false)
	if err != nil {
		return SymbolFilter{}, err
	}
	var raw []struct {
		Symbol         string `json:"symbol"`
		BaseCurrency   string `json:"baseCurrency"`
		QuoteCurrency  string `json:"quoteCurrency"`
		BaseIncrement  string `json:"baseIncrement"`
		PriceIncrement string `json:"priceIncrement"`
		QuoteMinSize   string `json:"quoteMinSize"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return SymbolFilter{}, err
	}
	e.filterMu.Lock()
	for _, s := range raw {
		e.filters[strings.ToUpper(s.Symbol)] = SymbolFilter{
			BaseAsset:   strings.ToUpper(s.BaseCurrency),
			QuoteAsset:  strings.ToUpper(s.QuoteCurrency),
			TickSize:    atof(s.PriceIncrement),
			StepSize:    atof(s.BaseIncrement),
			MinNotional: atof(s.QuoteMinSize),
		}
	}
	e.filtersReady = true
	f, ok := e.filters[sym]
	e.filterMu.Unlock()
	if !ok {
		return SymbolFilter{}, fmt.Errorf("kucoin: symbol %s not found", sym)
	}
	return f, nil
}

func (e *KucoinExchange) PlaceLimit(symbol, side string, price, qty float64, tif string, postOnly bool) (string, error) {
	if tif == "" {
		tif = "GTC"
	}
	payload := map[string]interface{}{
		"clientOid":   kucoinClientOID(),
		"symbol":      kucoinSym(symbol),
		"side":        strings.ToLower(side),
		"type":        "limit",
		"price":       strconv.FormatFloat(price, 'f', -1, 64),
		"size":        strconv.FormatFloat(qty, 'f', -1, 64),
		"timeInForce": strings.ToUpper(tif),
		"postOnly":    postOnly,
	}
	body, _ := json.Marshal(payload)
	data, err := e.do(http.MethodPost, "/api/v1/orders", body, true)
	if err != nil {
		return "", err
	}
	var r struct {
		OrderID string `json:"orderId"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	return r.OrderID, nil
}

func (e *KucoinExchange) CancelOrder(symbol, orderID string) error {
	_, err := e.do(http.MethodDelete, "/api/v1/orders/"+orderID, nil, true)
	return err
}

func (e *KucoinExchange) OpenOrders(symbol string) ([]OpenOrder, error) {
	data, err := e.do(http.MethodGet, "/api/v1/orders?status=active&symbol="+kucoinSym(symbol), nil, true)
	if err != nil {
		return nil, err
	}
	var r struct {
		Items []struct {
			ID    string `json:"id"`
			Side  string `json:"side"`
			Price string `json:"price"`
			Size  string `json:"size"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	out := make([]OpenOrder, 0, len(r.Items))
	for _, o := range r.Items {
		out = append(out, OpenOrder{ID: o.ID, Side: strings.ToUpper(o.Side), Price: atof(o.Price), Qty: atof(o.Size)})
	}
	return out, nil
}

func (e *KucoinExchange) Balances() (map[string]float64, error) {
	data, err := e.do(http.MethodGet, "/api/v1/accounts?type=trade", nil, true)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Currency  string `json:"currency"`
		Available string `json:"available"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, b := range raw {
		if v := atof(b.Available); v > 0 {
			out[strings.ToUpper(b.Currency)] += v
		}
	}
	return out, nil
}
