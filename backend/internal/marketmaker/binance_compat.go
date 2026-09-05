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

// Many second-tier exchanges run Binance Cloud or clone Binance's spot API, so
// they share one shape: HMAC-SHA256(query) signing, an api-key header, and the
// same order/filter/balance JSON — differing only in base URL, endpoint paths,
// and the header name. bnCompatExchange implements ExecExchange once for all of
// them; each concrete exchange is a thin registration below.
//
// Verified against docs.coins.ph/rest-api (Coins.ph) and mexcdevelop.github.io
// (MEXC spot v3), 2026-08.

type bnCompatPaths struct {
	bookTicker   string
	exchangeInfo string
	order        string // POST place / DELETE cancel
	openOrders   string
	account      string
}

func init() {
	RegisterExec("coinsph", func(cfg ExecConfig) (ExecExchange, error) {
		return newBnCompat("coinsph", cfg, "https://api.pro.coins.ph", "X-COINS-APIKEY", bnCompatPaths{
			bookTicker:   "/openapi/quote/v1/ticker/bookTicker",
			exchangeInfo: "/openapi/v1/exchangeInfo",
			order:        "/openapi/v1/order",
			openOrders:   "/openapi/v1/openOrders",
			account:      "/openapi/v1/account",
		}), nil
	})
	RegisterExec("mexc", func(cfg ExecConfig) (ExecExchange, error) {
		return newBnCompat("mexc", cfg, "https://api.mexc.com", "X-MEXC-APIKEY", bnCompatPaths{
			bookTicker:   "/api/v3/ticker/bookTicker",
			exchangeInfo: "/api/v3/exchangeInfo",
			order:        "/api/v3/order",
			openOrders:   "/api/v3/openOrders",
			account:      "/api/v3/account",
		}), nil
	})
}

func newBnCompat(name string, cfg ExecConfig, defBase, keyHeader string, paths bnCompatPaths) ExecExchange {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = defBase
	}
	return &bnCompatExchange{
		name:      name,
		baseURL:   base,
		keyHeader: keyHeader,
		apiKey:    cfg.APIKey,
		secret:    cfg.APISecret,
		paths:     paths,
		http:      &http.Client{Timeout: 10 * time.Second},
		filters:   map[string]SymbolFilter{},
	}
}

type bnCompatExchange struct {
	name      string
	baseURL   string
	keyHeader string
	apiKey    string
	secret    string
	paths     bnCompatPaths
	http      *http.Client

	filterMu sync.Mutex
	filters  map[string]SymbolFilter
}

func (e *bnCompatExchange) Name() string { return e.name }

// SupportsShort: 这些都是现货所(coinsph/mexc 等),只能卖出已持有的量。
func (e *bnCompatExchange) SupportsShort() bool { return false }

func normSym(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "/", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func (e *bnCompatExchange) publicGET(path string, params url.Values) ([]byte, error) {
	u := e.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	resp, err := e.http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s -> %d: %s", e.name, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (e *bnCompatExchange) signedRequest(method, path string, params url.Values) ([]byte, error) {
	if e.apiKey == "" || e.secret == "" {
		return nil, fmt.Errorf("%s: missing api credentials (fill exec.api_key/api_secret)", e.name)
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	if params.Get("recvWindow") == "" {
		params.Set("recvWindow", "5000")
	}
	query := params.Encode()
	mac := hmac.New(sha256.New, []byte(e.secret))
	mac.Write([]byte(query))
	sig := hex.EncodeToString(mac.Sum(nil))
	u := e.baseURL + path + "?" + query + "&signature=" + sig

	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(e.keyHeader, e.apiKey)
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s %s -> %d: %s", e.name, method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (e *bnCompatExchange) FetchBookTicker(symbol string) (BookTicker, error) {
	body, err := e.publicGET(e.paths.bookTicker, url.Values{"symbol": {normSym(symbol)}})
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
	return BookTicker{Symbol: r.Symbol, BidPx: atof(r.BidPrice), BidQty: atof(r.BidQty), AskPx: atof(r.AskPrice), AskQty: atof(r.AskQty), Ts: time.Now()}, nil
}

func (e *bnCompatExchange) SymbolFilter(symbol string) (SymbolFilter, error) {
	sym := normSym(symbol)
	e.filterMu.Lock()
	if f, ok := e.filters[sym]; ok {
		e.filterMu.Unlock()
		return f, nil
	}
	e.filterMu.Unlock()

	body, err := e.publicGET(e.paths.exchangeInfo, url.Values{"symbol": {sym}})
	if err != nil {
		return SymbolFilter{}, err
	}
	var r struct {
		Symbols []struct {
			Symbol     string `json:"symbol"`
			BaseAsset  string `json:"baseAsset"`
			QuoteAsset string `json:"quoteAsset"`
			Filters    []struct {
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
		f.BaseAsset = strings.ToUpper(s.BaseAsset)
		f.QuoteAsset = strings.ToUpper(s.QuoteAsset)
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
	e.filterMu.Lock()
	e.filters[sym] = f
	e.filterMu.Unlock()
	return f, nil
}

func (e *bnCompatExchange) PlaceLimit(symbol, side string, price, qty float64, tif string, postOnly bool) (string, error) {
	q := url.Values{}
	q.Set("symbol", normSym(symbol))
	q.Set("side", strings.ToUpper(side))
	q.Set("quantity", strconv.FormatFloat(qty, 'f', -1, 64))
	q.Set("price", strconv.FormatFloat(price, 'f', -1, 64))
	if postOnly {
		q.Set("type", "LIMIT_MAKER") // maker-only; no timeInForce (Binance-spot semantics)
	} else {
		q.Set("type", "LIMIT")
		if tif == "" {
			tif = "GTC"
		}
		q.Set("timeInForce", strings.ToUpper(tif))
	}
	body, err := e.signedRequest(http.MethodPost, e.paths.order, q)
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

func (e *bnCompatExchange) CancelOrder(symbol, orderID string) error {
	q := url.Values{}
	q.Set("symbol", normSym(symbol))
	q.Set("orderId", orderID)
	_, err := e.signedRequest(http.MethodDelete, e.paths.order, q)
	return err
}

func (e *bnCompatExchange) OpenOrders(symbol string) ([]OpenOrder, error) {
	q := url.Values{}
	if symbol != "" {
		q.Set("symbol", normSym(symbol))
	}
	body, err := e.signedRequest(http.MethodGet, e.paths.openOrders, q)
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

func (e *bnCompatExchange) Balances() (map[string]float64, error) {
	body, err := e.signedRequest(http.MethodGet, e.paths.account, nil)
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
