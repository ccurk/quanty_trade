package marketmaker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Coins.ph Pro runs on Binance Cloud, so its REST API mirrors Binance spot: same
// HMAC-SHA256(query) signing and the same filter/order shapes, differing only in
// base URL, the /openapi path prefix, and the X-COINS-APIKEY header. Verified
// against https://docs.coins.ph/rest-api/ (2026-08).
const coinsDefaultBaseURL = "https://api.pro.coins.ph"

func init() { RegisterExec("coinsph", newCoins) }

func newCoins(cfg ExecConfig) (ExecExchange, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = coinsDefaultBaseURL
	}
	return &CoinsExchange{
		baseURL: base,
		apiKey:  cfg.APIKey,
		secret:  cfg.APISecret,
		http:    &http.Client{Timeout: 10 * time.Second},
		filters: map[string]SymbolFilter{},
	}, nil
}

type CoinsExchange struct {
	baseURL string
	apiKey  string
	secret  string
	http    *http.Client

	filterMu sync.Mutex
	filters  map[string]SymbolFilter
}

func (c *CoinsExchange) Name() string { return "coinsph" }

func normSym(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "/", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

// ---- HTTP helpers ----

func (c *CoinsExchange) publicGET(path string, params url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	resp, err := c.http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("coinsph %s -> %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *CoinsExchange) signedRequest(method, path string, params url.Values) ([]byte, error) {
	if c.apiKey == "" || c.secret == "" {
		return nil, fmt.Errorf("coinsph: missing api credentials (fill exec.api_key/api_secret in config)")
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	if params.Get("recvWindow") == "" {
		params.Set("recvWindow", "5000")
	}
	query := params.Encode()
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(query))
	sig := hex.EncodeToString(mac.Sum(nil))
	u := c.baseURL + path + "?" + query + "&signature=" + sig

	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-COINS-APIKEY", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("coinsph %s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// ---- ExecExchange ----

func (c *CoinsExchange) FetchBookTicker(symbol string) (BookTicker, error) {
	q := url.Values{}
	q.Set("symbol", normSym(symbol))
	body, err := c.publicGET("/openapi/quote/v1/ticker/bookTicker", q)
	if err != nil {
		return BookTicker{}, err
	}
	var r struct {
		Symbol   string `json:"symbol"`
		BidPrice string `json:"bidPrice"`
		BidQty   string `json:"bidQty"`
		AskPrice string `json:"askPrice"`
		AskQty   string `json:"askQty"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return BookTicker{}, err
	}
	return BookTicker{
		Symbol: r.Symbol,
		BidPx:  atof(r.BidPrice),
		BidQty: atof(r.BidQty),
		AskPx:  atof(r.AskPrice),
		AskQty: atof(r.AskQty),
		Ts:     time.Now(),
	}, nil
}

func (c *CoinsExchange) SymbolFilter(symbol string) (SymbolFilter, error) {
	sym := normSym(symbol)
	c.filterMu.Lock()
	if f, ok := c.filters[sym]; ok {
		c.filterMu.Unlock()
		return f, nil
	}
	c.filterMu.Unlock()

	body, err := c.publicGET("/openapi/v1/exchangeInfo", url.Values{"symbol": {sym}})
	if err != nil {
		return SymbolFilter{}, err
	}
	var r struct {
		Symbols []struct {
			Symbol  string `json:"symbol"`
			Filters []struct {
				FilterType  string `json:"filterType"`
				TickSize    string `json:"tickSize"`
				StepSize    string `json:"stepSize"`
				MinNotional string `json:"minNotional"`
				Notional    string `json:"notional"`
			} `json:"filters"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return SymbolFilter{}, err
	}
	var f SymbolFilter
	for _, s := range r.Symbols {
		if normSym(s.Symbol) != sym {
			continue
		}
		for _, fl := range s.Filters {
			switch fl.FilterType {
			case "PRICE_FILTER":
				f.TickSize = atof(fl.TickSize)
			case "LOT_SIZE":
				f.StepSize = atof(fl.StepSize)
			case "MIN_NOTIONAL", "NOTIONAL":
				if v := atof(fl.MinNotional); v > 0 {
					f.MinNotional = v
				} else if v := atof(fl.Notional); v > 0 {
					f.MinNotional = v
				}
			}
		}
	}
	c.filterMu.Lock()
	c.filters[sym] = f
	c.filterMu.Unlock()
	return f, nil
}

func (c *CoinsExchange) PlaceLimit(symbol, side string, price, qty float64, tif string, postOnly bool) (string, error) {
	q := url.Values{}
	q.Set("symbol", normSym(symbol))
	q.Set("side", strings.ToUpper(side))
	q.Set("quantity", strconv.FormatFloat(qty, 'f', -1, 64))
	q.Set("price", strconv.FormatFloat(price, 'f', -1, 64))
	if postOnly {
		q.Set("type", "LIMIT_MAKER") // maker-only (Binance-spot semantics); no timeInForce
	} else {
		q.Set("type", "LIMIT")
		if tif == "" {
			tif = "GTC"
		}
		q.Set("timeInForce", strings.ToUpper(tif))
	}
	body, err := c.signedRequest(http.MethodPost, "/openapi/v1/order", q)
	if err != nil {
		return "", err
	}
	var r struct {
		OrderID json.Number `json:"orderId"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	return r.OrderID.String(), nil
}

func (c *CoinsExchange) CancelOrder(symbol, orderID string) error {
	q := url.Values{}
	q.Set("symbol", normSym(symbol))
	q.Set("orderId", orderID)
	_, err := c.signedRequest(http.MethodDelete, "/openapi/v1/order", q)
	return err
}

func (c *CoinsExchange) OpenOrders(symbol string) ([]OpenOrder, error) {
	q := url.Values{}
	if symbol != "" {
		q.Set("symbol", normSym(symbol))
	}
	body, err := c.signedRequest(http.MethodGet, "/openapi/v1/openOrders", q)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		OrderID json.Number `json:"orderId"`
		Side    string      `json:"side"`
		Price   string      `json:"price"`
		OrigQty string      `json:"origQty"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]OpenOrder, 0, len(raw))
	for _, o := range raw {
		out = append(out, OpenOrder{ID: o.OrderID.String(), Side: strings.ToUpper(o.Side), Price: atof(o.Price), Qty: atof(o.OrigQty)})
	}
	return out, nil
}

func (c *CoinsExchange) Balances() (map[string]float64, error) {
	body, err := c.signedRequest(http.MethodGet, "/openapi/v1/account", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Balances []struct {
			Asset string `json:"asset"`
			Free  string `json:"free"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, b := range r.Balances {
		if v := atof(b.Free); v > 0 {
			out[strings.ToUpper(b.Asset)] = v
		}
	}
	return out, nil
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}
