package exchange

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

type MarketSymbol struct {
	Symbol      string  `json:"symbol"`
	BaseAsset   string  `json:"base_asset"`
	QuoteAsset  string  `json:"quote_asset"`
	LastPrice   float64 `json:"last_price"`
	QuoteVolume float64 `json:"quote_volume_24h"`
}

func (b *BinanceExchange) FetchMarketSymbols(quoteAsset string, minPrice float64, maxPrice float64, minQuoteVolume float64, limit int, excludeStable bool, baseAssets []string) ([]MarketSymbol, error) {
	q := strings.ToUpper(strings.TrimSpace(quoteAsset))
	if q == "" {
		q = "USDT"
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	baseAllow := map[string]struct{}{}
	for _, a := range baseAssets {
		a = strings.ToUpper(strings.TrimSpace(a))
		if a != "" {
			baseAllow[a] = struct{}{}
		}
	}

	stables := map[string]struct{}{}
	if excludeStable {
		for _, a := range []string{"USDT", "USDC", "BUSD", "TUSD", "FDUSD", "DAI", "USDP"} {
			stables[a] = struct{}{}
		}
	}

	path := "/api/v3/ticker/24hr"
	if b.market == "usdm" {
		path = "/fapi/v1/ticker/24hr"
	}
	body, _, err := b.publicRequest(context.Background(), path, nil)
	if err != nil {
		return nil, err
	}

	var tickers []struct {
		Symbol      string `json:"symbol"`
		LastPrice   string `json:"lastPrice"`
		QuoteVolume string `json:"quoteVolume"`
	}
	if err := json.Unmarshal(body, &tickers); err != nil {
		return nil, err
	}

	tickerBySymbol := make(map[string]struct {
		LastPrice   float64
		QuoteVolume float64
	}, len(tickers))
	for _, t := range tickers {
		lp, _ := strconv.ParseFloat(t.LastPrice, 64)
		qv, _ := strconv.ParseFloat(t.QuoteVolume, 64)
		if lp <= 0 && qv <= 0 {
			continue
		}
		tickerBySymbol[strings.ToUpper(strings.TrimSpace(t.Symbol))] = struct {
			LastPrice   float64
			QuoteVolume float64
		}{LastPrice: lp, QuoteVolume: qv}
	}

	b.ensureInfoCache()
	_ = b.refreshExchangeInfo()

	b.info.mu.RLock()
	defer b.info.mu.RUnlock()

	out := make([]MarketSymbol, 0, limit)
	for _, f := range b.info.bySymbol {
		if strings.ToUpper(f.QuoteAsset) != q {
			continue
		}
		if excludeStable {
			if _, ok := stables[strings.ToUpper(f.BaseAsset)]; ok {
				continue
			}
		}
		if len(baseAllow) > 0 {
			if _, ok := baseAllow[strings.ToUpper(f.BaseAsset)]; !ok {
				continue
			}
		}
		tk, ok := tickerBySymbol[strings.ToUpper(f.Symbol)]
		if !ok {
			continue
		}
		if minPrice > 0 && tk.LastPrice < minPrice {
			continue
		}
		if maxPrice > 0 && tk.LastPrice > maxPrice {
			continue
		}
		if minQuoteVolume > 0 && tk.QuoteVolume < minQuoteVolume {
			continue
		}
		out = append(out, MarketSymbol{
			Symbol:      strings.ToUpper(f.BaseAsset) + "/" + strings.ToUpper(f.QuoteAsset),
			BaseAsset:   strings.ToUpper(f.BaseAsset),
			QuoteAsset:  strings.ToUpper(f.QuoteAsset),
			LastPrice:   tk.LastPrice,
			QuoteVolume: tk.QuoteVolume,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].QuoteVolume > out[j].QuoteVolume })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
