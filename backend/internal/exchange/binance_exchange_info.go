package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type binanceSymbolFilters struct {
	Symbol      string
	BaseAsset   string
	QuoteAsset  string
	TickSize    float64
	StepSize    float64
	MinQty      float64
	MinNotional float64
}

type binanceExchangeInfoCache struct {
	mu       sync.RWMutex
	bySymbol map[string]binanceSymbolFilters
	expires  time.Time
}

func (b *BinanceExchange) getFilters(symbol string) (binanceSymbolFilters, error) {
	sym := binanceSymbol(symbol)
	b.ensureInfoCache()

	b.info.mu.RLock()
	if time.Now().Before(b.info.expires) {
		if f, ok := b.info.bySymbol[sym]; ok {
			b.info.mu.RUnlock()
			return f, nil
		}
	}
	b.info.mu.RUnlock()

	if err := b.refreshExchangeInfo(); err != nil {
		return binanceSymbolFilters{}, err
	}

	b.info.mu.RLock()
	defer b.info.mu.RUnlock()
	f, ok := b.info.bySymbol[sym]
	if !ok {
		return binanceSymbolFilters{}, fmt.Errorf("symbol not found: %s", sym)
	}
	return f, nil
}

func (b *BinanceExchange) ensureInfoCache() {
	b.infoOnce.Do(func() {
		b.info = binanceExchangeInfoCache{
			bySymbol: make(map[string]binanceSymbolFilters),
			expires:  time.Time{},
		}
	})
}

func (b *BinanceExchange) refreshExchangeInfo() error {
	b.ensureInfoCache()

	params := url.Values{}
	path := "/api/v3/exchangeInfo"
	if b.market == "usdm" {
		path = "/fapi/v1/exchangeInfo"
	}
	u := b.baseURL + path
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var parsed struct {
		Symbols []struct {
			Symbol     string `json:"symbol"`
			Status     string `json:"status"`
			BaseAsset  string `json:"baseAsset"`
			QuoteAsset string `json:"quoteAsset"`
			Filters    []struct {
				FilterType  string `json:"filterType"`
				TickSize    string `json:"tickSize"`
				StepSize    string `json:"stepSize"`
				MinQty      string `json:"minQty"`
				MinNotional string `json:"minNotional"`
			} `json:"filters"`
		} `json:"symbols"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return err
	}

	next := make(map[string]binanceSymbolFilters, len(parsed.Symbols))
	for _, s := range parsed.Symbols {
		if s.Status != "TRADING" {
			continue
		}
		f := binanceSymbolFilters{
			Symbol:     s.Symbol,
			BaseAsset:  s.BaseAsset,
			QuoteAsset: s.QuoteAsset,
		}
		for _, flt := range s.Filters {
			switch flt.FilterType {
			case "PRICE_FILTER":
				f.TickSize, _ = strconv.ParseFloat(flt.TickSize, 64)
			case "LOT_SIZE", "MARKET_LOT_SIZE":
				f.StepSize, _ = strconv.ParseFloat(flt.StepSize, 64)
				f.MinQty, _ = strconv.ParseFloat(flt.MinQty, 64)
			case "MIN_NOTIONAL":
				f.MinNotional, _ = strconv.ParseFloat(flt.MinNotional, 64)
			}
		}
		next[s.Symbol] = f
	}

	b.info.mu.Lock()
	b.info.bySymbol = next
	b.info.expires = time.Now().Add(10 * time.Minute)
	b.info.mu.Unlock()
	return nil
}

func roundDownToStep(v float64, step float64) float64 {
	if step <= 0 {
		return v
	}
	return math.Floor(v/step) * step
}

func roundUpToStep(v float64, step float64) float64 {
	if step <= 0 {
		return v
	}
	return math.Ceil(v/step) * step
}

func roundDownPrice(v float64, tick float64) float64 {
	if tick <= 0 {
		return v
	}
	return math.Floor(v/tick) * tick
}

func trimZeros(s string) string {
	if strings.IndexByte(s, '.') < 0 {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

func formatByStep(v float64, step float64) string {
	if step <= 0 {
		return trimZeros(strconv.FormatFloat(v, 'f', -1, 64))
	}
	decimals := int(math.Round(-math.Log10(step)))
	if decimals < 0 {
		decimals = 0
	}
	return trimZeros(strconv.FormatFloat(v, 'f', decimals, 64))
}
