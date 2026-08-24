package marketmaker

import (
	"encoding/json"
	"io"
	"math"
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

const (
	universeTopN = 100
	// 硬删:偏离过大几乎必是"同名不同币 / 完全陈价",毫无做市价值,直接丢弃。
	hardDropDivergenceBps = 300
	// 疑点阈值(不删,只打标"suspect"):机会不漏、陷阱看得见,由人工识别。
	suspectDivergenceBps = 50    // 与币安中价偏离 > 此 = 可疑(可能异币/陈价/套利)
	suspectSpreadBps     = 30    // 执行所自身价差 > 此 = 可疑(偏薄/宽价差幻觉)
	suspectMinQuoteVol   = 50000 // 24h 成交额 < $50k = 可疑(薄),仅在接口给出成交额时判定
)

// UniverseRow is one exec pair measured against its binance reference.
type UniverseRow struct {
	Exchange       string  `json:"exchange"`
	Symbol         string  `json:"symbol"`     // exec-native symbol, e.g. BTC_USDT
	RefSymbol      string  `json:"ref_symbol"` // binance symbol, e.g. BTCUSDT
	RefMid         float64 `json:"ref_mid"`
	ExecMid        float64 `json:"exec_mid"`
	ExecSpreadBps  float64 `json:"exec_spread_bps"` // 执行所自身买卖价差
	MidDiffBps     float64 `json:"mid_diff_bps"`    // 与币安中价的偏离
	BuyEdgeBps     float64 `json:"buy_edge_bps"`
	SellEdgeBps    float64 `json:"sell_edge_bps"`
	FeeBps         float64 `json:"fee_bps"`
	FeeLive        bool    `json:"fee_live"`
	NetBestEdgeBps float64 `json:"net_best_edge_bps"`
	QuoteVol       float64 `json:"quote_vol"` // 执行所 24h 成交额(USDT,若接口提供)
	Suspect        string  `json:"suspect"`   // 空=干净候选;否则为疑点(薄/偏离/宽价差),不删只标
	// 持续性统计(跨多轮扫描累计,用于把"某一秒的快照"升级成"稳定信号"):
	Samples   int64   `json:"samples"`     // 累计采样数
	PosRate   float64 `json:"pos_rate"`    // 净边>0 的占比
	AvgNet    float64 `json:"avg_net_bps"` // 平均净边
	Signal    bool    `json:"signal"`      // 稳定信号:干净 + 采样够 + 大比例为正 + 平均为正
	Tradeable bool    `json:"tradeable"`   // 能下单:干净 + 扣真实 per-pair 费后净边 > 0
}

const (
	signalMinSamples = 30  // 至少 30 次采样(~5 分钟 @10s)才有统计意义
	signalMinPosRate = 0.8 // 净边>0 占比 ≥ 80%
)

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

// pairStat accumulates a pair's net-edge history across scans, so a persistent
// positive net edge (a real signal) is told apart from a one-scan flicker (noise).
type pairStat struct {
	samples, posCount int64
	sumNet, maxNet    float64
	since             time.Time
}

var (
	statMu    sync.RWMutex
	pairStats = map[string]*pairStat{}
)

// execTicker is a normalized exec quote keyed to its binance reference symbol.
type execTicker struct {
	refSymbol string
	native    string
	bid, ask  float64
	quoteVol  float64 // 24h 成交额(USDT),接口不提供则 0
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
		if strings.EqualFold(ex, "gate") {
			gsyms := make([]string, 0, len(tickers))
			for _, gt := range tickers {
				gsyms = append(gsyms, gt.native)
			}
			PrefetchGateMakerFees(gsyms) // 批量拉真实 per-pair maker 费(零费对=0),填缓存
		}
		for _, tk := range tickers {
			refMid, ok := ref[tk.refSymbol]
			if !ok || refMid <= 0 || tk.bid <= 0 || tk.ask <= 0 {
				continue
			}
			execMid := (tk.bid + tk.ask) / 2
			execSpreadBps := (tk.ask - tk.bid) / execMid * 10000
			midDiffBps := math.Abs(execMid-refMid) / refMid * 10000
			if midDiffBps > hardDropDivergenceBps {
				continue // 硬删:偏离过大,同名异币/完全陈价,无做市价值
			}
			matched++
			// 疑点判定:不删,只打标 —— 机会不漏、陷阱看得见,人工识别。
			suspect := ""
			if execSpreadBps > suspectSpreadBps {
				suspect = "宽价差"
			} else if midDiffBps > suspectDivergenceBps {
				suspect = "偏离大"
			} else if tk.quoteVol > 0 && tk.quoteVol < suspectMinQuoteVol {
				suspect = "量薄"
			}
			feeBps, feeLive := CachedMakerFeeBps(ex, tk.native) // 真实 per-pair maker 费(零费=0)
			buyEdge := (refMid - tk.bid) / refMid * 10000
			sellEdge := (tk.ask - refMid) / refMid * 10000
			best := buyEdge
			if sellEdge > best {
				best = sellEdge
			}
			rows = append(rows, UniverseRow{
				Exchange: ex, Symbol: tk.native, RefSymbol: tk.refSymbol,
				RefMid: refMid, ExecMid: execMid, ExecSpreadBps: execSpreadBps, MidDiffBps: midDiffBps,
				BuyEdgeBps: buyEdge, SellEdgeBps: sellEdge,
				FeeBps: feeBps, FeeLive: feeLive, NetBestEdgeBps: best - 2*feeBps,
				QuoteVol: tk.quoteVol, Suspect: suspect,
				Tradeable: suspect == "" && best-2*feeBps > 0,
			})
		}
	}
	// 干净候选(无疑点)排前,再按净边降序 —— 卡片"最优机会"永远是最好的真候选,
	// 可疑的仍保留在后面(机会不漏),但不会被当成头号机会。
	// 累计持续性统计(只统计干净候选,含未进 top-100 的),用于信号判定。
	now := time.Now()
	statMu.Lock()
	for i := range rows {
		if rows[i].Suspect != "" {
			continue
		}
		k := rows[i].Exchange + "|" + rows[i].Symbol
		s := pairStats[k]
		if s == nil {
			s = &pairStat{since: now}
			pairStats[k] = s
		}
		s.samples++
		s.sumNet += rows[i].NetBestEdgeBps
		if s.samples == 1 || rows[i].NetBestEdgeBps > s.maxNet {
			s.maxNet = rows[i].NetBestEdgeBps
		}
		if rows[i].NetBestEdgeBps > 0 {
			s.posCount++
		}
	}
	statMu.Unlock()

	sort.Slice(rows, func(i, j int) bool {
		ci, cj := rows[i].Suspect == "", rows[j].Suspect == ""
		if ci != cj {
			return ci
		}
		return rows[i].NetBestEdgeBps > rows[j].NetBestEdgeBps
	})
	if len(rows) > universeTopN {
		rows = rows[:universeTopN]
	}
	// 把统计 + 信号标记附到展示行上。信号 = 干净 + 采样够 + 大比例为正 + 平均为正。
	statMu.RLock()
	for i := range rows {
		if s := pairStats[rows[i].Exchange+"|"+rows[i].Symbol]; s != nil && s.samples > 0 {
			rows[i].Samples = s.samples
			rows[i].PosRate = float64(s.posCount) / float64(s.samples)
			rows[i].AvgNet = s.sumNet / float64(s.samples)
			rows[i].Signal = rows[i].Suspect == "" && s.samples >= signalMinSamples && rows[i].PosRate >= signalMinPosRate && rows[i].AvgNet > 0
		}
	}
	statMu.RUnlock()
	universeMu.Lock()
	universeRows = rows
	universeMatched = matched
	universeAt = time.Now()
	universeErr = ""
	universeMu.Unlock()
	topNet := 0.0
	topDesc := "-"
	if len(rows) > 0 {
		topNet = rows[0].NetBestEdgeBps
		topDesc = rows[0].Symbol + "@" + rows[0].Exchange
	}
	logger.Infof("[mm-universe] scanned matched=%d topN=%d top=%s net=%.1fbps", matched, len(rows), topDesc, topNet)
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
		QuoteVolume  string `json:"quote_volume"`
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
			quoteVol: atofU(r.QuoteVolume),
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
				Symbol   string `json:"symbol"`
				Buy      string `json:"buy"`
				Sell     string `json:"sell"`
				VolValue string `json:"volValue"`
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
			quoteVol: atofU(r.VolValue),
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
