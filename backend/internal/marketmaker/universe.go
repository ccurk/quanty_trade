package marketmaker

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/logger"
)

// universe.go scans the WHOLE market instead of a hardcoded pair list. Each cycle it
// pulls one BATCH ticker call per exchange (binance all-bookTicker as reference +
// each exec's all-tickers), computes net edge for every exec pair that also lists on
// binance, ranks the lot by net edge, and keeps the top N. That single-batch design
// is why it scales to hundreds of pairs without hitting REST rate limits — and why
// it's inherently "dynamic": every cycle re-ranks the entire market, so whatever has
// edge floats to the top on its own. This feeds the dashboard 做市 card; the config
// pairs in engine.go are only for LIVE quoting.

var universeHTTP = &http.Client{Timeout: 15 * time.Second}

const universeTopN = 100

// UniverseRow is one exec pair measured against its binance reference.
type UniverseRow struct {
	Exchange       string  `json:"exchange"`
	Symbol         string  `json:"symbol"`     // exec-native symbol, e.g. BTC_USDT
	RefSymbol      string  `json:"ref_symbol"` // binance symbol, e.g. BTCUSDT
	RefMid         float64 `json:"ref_mid"`
	ExecMid        float64 `json:"exec_mid"`
	BuyEdgeBps     float64 `json:"buy_edge_bps"`
	SellEdgeBps    float64 `json:"sell_edge_bps"`
	FeeBps         float64 `json:"fee_bps"`
	FeeLive        bool    `json:"fee_live"`
	NetBestEdgeBps float64 `json:"net_best_edge_bps"`
}

func (r UniverseRow) BestEdgeBps() float64 {
	if r.BuyEdgeBps > r.SellEdgeBps {
		return r.BuyEdgeBps
	}
	return r.SellEdgeBps
}

var (
	universeMu      sync.RWMutex
	universeRows    []UniverseRow // top N by net edge
	universeMatched int           // total pairs matched (whole market)
	universeAt      time.Time
	universeErr     string
	universeRunning bool
)

// execTicker is a normalized exec quote keyed to its binance reference symbol.
type execTicker struct {
	refSymbol string
	native    string
	bid, ask  float64
}

// StartUniverseScanner runs the full-market scan every refreshSec (public data only).
func StartUniverseScanner(exchanges []string, refreshSec int) {
	if len(exchanges) == 0 {
		exchanges = []string{"gate", "kucoin", "coinsph"}
	}
	if refreshSec <= 0 {
		refreshSec = 10
	}
	universeMu.Lock()
	universeRunning = true
	universeMu.Unlock()
	go func() {
		t := time.NewTicker(time.Duration(refreshSec) * time.Second)
		defer t.Stop()
		scanUniverseOnce(exchanges)
		for range t.C {
			scanUniverseOnce(exchanges)
		}
	}()
}

func scanUniverseOnce(exchanges []string) {
	ref, err := fetchBinanceRefMids()
	if err != nil || len(ref) == 0 {
		universeMu.Lock()
		universeErr = "binance ref failed: " + errStr(err)
		universeMu.Unlock()
		return
	}
	var rows []UniverseRow
	matched := 0
	for _, ex := range exchanges {
		var tickers []execTicker
		var e error
		switch strings.ToLower(ex) {
		case "gate":
			tickers, e = fetchGateTickers()
		case "kucoin":
			tickers, e = fetchKucoinTickers()
		case "coinsph":
			tickers, e = fetchCoinsphTickers()
		default:
			continue
		}
		if e != nil {
			continue
		}
		feeBps, feeLive := execAccountFeeBps(ex)
		for _, tk := range tickers {
			refMid, ok := ref[tk.refSymbol]
			if !ok || refMid <= 0 || tk.bid <= 0 || tk.ask <= 0 {
				continue
			}
			matched++
			buyEdge := (refMid - tk.bid) / refMid * 10000
			sellEdge := (tk.ask - refMid) / refMid * 10000
			best := buyEdge
			if sellEdge > best {
				best = sellEdge
			}
			rows = append(rows, UniverseRow{
				Exchange: ex, Symbol: tk.native, RefSymbol: tk.refSymbol,
				RefMid: refMid, ExecMid: (tk.bid + tk.ask) / 2,
				BuyEdgeBps: buyEdge, SellEdgeBps: sellEdge,
				FeeBps: feeBps, FeeLive: feeLive, NetBestEdgeBps: best - 2*feeBps,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].NetBestEdgeBps > rows[j].NetBestEdgeBps })
	if len(rows) > universeTopN {
		rows = rows[:universeTopN]
	}
	universeMu.Lock()
	universeRows = rows
	universeMatched = matched
	universeAt = time.Now()
	universeErr = ""
	universeMu.Unlock()
	logger.Infof("[mm-universe] scanned matched=%d topN=%d", matched, len(rows))
}

// UniverseSnapshot returns the top-N rows (net-edge desc), total matched, and running.
func UniverseSnapshot() (rows []UniverseRow, matched int, running bool) {
	universeMu.RLock()
	defer universeMu.RUnlock()
	out := make([]UniverseRow, len(universeRows))
	copy(out, universeRows)
	return out, universeMatched, universeRunning
}

// execAccountFeeBps returns one maker fee per exchange (account-level). gate is
// live-fetched (cached); others use the labeled default. Per-pair promo fees for the
// top candidates can be layered later.
func execAccountFeeBps(ex string) (float64, bool) {
	if strings.EqualFold(ex, "gate") {
		return MakerFeeBps("gate", "BTC_USDT") // 代表对拉账户级 maker 费(gate 账户费率通常全对一致)
	}
	return defaultFeeBps(ex), false
}

func fetchBinanceRefMids() (map[string]float64, error) {
	body, err := getJSON("https://api.binance.com/api/v3/ticker/bookTicker")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Symbol   string `json:"symbol"`
		BidPrice string `json:"bidPrice"`
		AskPrice string `json:"askPrice"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(raw))
	for _, r := range raw {
		if !strings.HasSuffix(r.Symbol, "USDT") {
			continue
		}
		bid, ask := atofU(r.BidPrice), atofU(r.AskPrice)
		if bid > 0 && ask > 0 {
			out[r.Symbol] = (bid + ask) / 2
		}
	}
	return out, nil
}

func fetchGateTickers() ([]execTicker, error) {
	body, err := getJSON("https://api.gateio.ws/api/v4/spot/tickers")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		CurrencyPair string `json:"currency_pair"`
		HighestBid   string `json:"highest_bid"`
		LowestAsk    string `json:"lowest_ask"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]execTicker, 0, len(raw))
	for _, r := range raw {
		if !strings.HasSuffix(r.CurrencyPair, "_USDT") {
			continue
		}
		out = append(out, execTicker{
			refSymbol: strings.ReplaceAll(r.CurrencyPair, "_", ""),
			native:    r.CurrencyPair,
			bid:       atofU(r.HighestBid), ask: atofU(r.LowestAsk),
		})
	}
	return out, nil
}

func fetchKucoinTickers() ([]execTicker, error) {
	body, err := getJSON("https://api.kucoin.com/api/v1/market/allTickers")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Data struct {
			Ticker []struct {
				Symbol string `json:"symbol"`
				Buy    string `json:"buy"`
				Sell   string `json:"sell"`
			} `json:"ticker"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]execTicker, 0, len(raw.Data.Ticker))
	for _, r := range raw.Data.Ticker {
		if !strings.HasSuffix(r.Symbol, "-USDT") {
			continue
		}
		out = append(out, execTicker{
			refSymbol: strings.ReplaceAll(r.Symbol, "-", ""),
			native:    r.Symbol,
			bid:       atofU(r.Buy), ask: atofU(r.Sell),
		})
	}
	return out, nil
}

func fetchCoinsphTickers() ([]execTicker, error) {
	body, err := getJSON("https://api.pro.coins.ph/openapi/quote/v1/ticker/bookTicker")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Symbol   string `json:"symbol"`
		BidPrice string `json:"bidPrice"`
		AskPrice string `json:"askPrice"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]execTicker, 0, len(raw))
	for _, r := range raw {
		if !strings.HasSuffix(r.Symbol, "USDT") {
			continue
		}
		out = append(out, execTicker{
			refSymbol: r.Symbol, native: r.Symbol,
			bid: atofU(r.BidPrice), ask: atofU(r.AskPrice),
		})
	}
	return out, nil
}

func getJSON(url string) ([]byte, error) {
	resp, err := universeHTTP.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func atofU(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
