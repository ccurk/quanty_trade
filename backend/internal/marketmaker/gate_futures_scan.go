package marketmaker

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"

	"quanty_trade/internal/logger"
)

// gate_futures_scan.go —— gate USDT 永续盘口的【只读】观测链路,逐行 JSONL 落盘。
//
// 为什么另起一条链路,而不是给 universe.go 的 switch 加一个 case
// (那是 gate-futures-mm-assessment-2026-09-08.md §3.3 原本写的第一步):
// universe 的参考价来自 fetchBinanceRefMids(),打的是 binance【现货】,而且只留中价。
// 2026-09-09 实测(gate 988 个 USDT 永续):
//
//   - 能匹配到 binance【永续】的 670 个;能匹配到 binance【现货】的只有 360 个
//     —— 走现货参考,直接有 310 个合约看不见。
//   - 两边都能匹配的 339 个里,「对永续测的基差」与「对现货测的基差」中位数差
//     8.31bps、p90 19.68bps,249/339 差 >5bps(最大 110.7bps)。
//
// 候选的净空间本身就在 1~10bps 量级,8bps 的参考误差比信号还大。拿现货当永续的参考,
// 量出来的是「永续-现货基差」,不是我们要的「同一品种在两个所的价差」。所以参考换成
// binance 永续,并且把两边的买一卖一【和各自的量】原样留下。
//
// 台账 #14(跨所价差套利)卡的不是结论,是「只有 9/08 一次快照、没有持续数据」。
// 这里每 futScanIntervalSec 秒写一批行,每行自带两个所的 8 个原始数,任何派生量都能
// 离线重算一遍 —— 这正是 observe_store.go 从台账 #7 学到的那条:只落派生量,复核时
// 分不清是数据错了还是推导错了。
//
// 严格只读:两个公开 batch 端点,不带凭据、不下单、不碰任何下单路径。
const (
	gateFuturesTickersURL = "https://api.gateio.ws/api/v4/futures/usdt/tickers"
	binanceFuturesBookURL = "https://fapi.binance.com/fapi/v1/ticker/bookTicker"

	// 只落「真能下手」的合约。实测(2026-09-09 一轮真实扫描):988 个 gate 永续 →
	// 有 binance 永续参考且过 $5M 成交额门槛的 74 个,单行实测 720 字节,
	// 60 秒一轮 = 10.7 万行/天 ≈ 76.7 MB/天。门槛降到 $1M 是 151 个(约 157 MB/天),
	// 而那一档的价差本来就打不满,不值这份磁盘。
	futScanMinQuoteVolUSD = 5_000_000
	futScanIntervalSec    = 60

	// 磁盘上限自带,不寄望外部清理 —— 台账 #37:server.log 曾无轮转涨到 195MiB、
	// 日增 66MiB,2026-08-27 那次 DB 挂死的根因就是 40G 盘被日志写满。
	// 50MB 当前 + 4 份备份 = 250MB 原始量,按上面实测的 76.7MB/天 ≈ 保留 3.3 天。
	// 备份 gzip(单行 JSON 压缩比很高),所以实际占盘远小于 250MB。
	// 要留更久就调大 futScanMaxBackups —— 每 +1 约多 1.3 天、多约 6MB 盘。
	futScanMaxSizeMB  = 50
	futScanMaxBackups = 4

	futScanFileName = "gate-futures-scan.jsonl"
)

// FuturesScanRow 是一个 gate 永续合约在某一瞬间、对其 binance 永续参考量出来的一条观测。
//
// 字段顺序即阅读顺序:先八个【原始报价】,再深度,最后才是派生的 bps。
// 落盘的规矩是「原始数必须够重算出所有派生量」—— 否则离线复核时算不出同一个结果,
// 没人分得清是数据脏还是公式错(台账 #7 就是栽在这)。
type FuturesScanRow struct {
	Ts        time.Time `json:"ts"`
	Exchange  string    `json:"exchange"`   // 恒为 gate-futures
	Feed      string    `json:"feed"`       // 恒为 binance-futures
	Contract  string    `json:"contract"`   // gate 原生合约名,如 BTC_USDT
	RefSymbol string    `json:"ref_symbol"` // binance 永续名,如 BTCUSDT

	// --- 八个原始数:两个所各自的买一卖一 ---
	ExecBidPx float64 `json:"exec_bid_px"`
	ExecAskPx float64 `json:"exec_ask_px"`
	RefBidPx  float64 `json:"ref_bid_px"`
	RefAskPx  float64 `json:"ref_ask_px"`
	// gate 的量以【张】计,binance 以【基础币】计 —— 两边单位不同,所以张数原样留下,
	// 换算因子(quanto_multiplier)也原样留下,离线可自证:base = 张 × multiplier。
	ExecBidSzCont float64 `json:"exec_bid_sz_cont"`
	ExecAskSzCont float64 `json:"exec_ask_sz_cont"`
	RefBidQty     float64 `json:"ref_bid_qty"`
	RefAskQty     float64 `json:"ref_ask_qty"`

	QuantoMultiplier float64 `json:"quanto_multiplier"` // 张 → 基础币

	// --- L1 深度(美元名义)。跨所套利真正要问的是「这一档能打多少钱」,
	// 而不是「多少个币」;两边单位不同,折成美元才可比。可离线核对:
	// exec_bid_usd == exec_bid_sz_cont × quanto_multiplier × exec_bid_px。 ---
	ExecBidUSD float64 `json:"exec_bid_usd"`
	ExecAskUSD float64 `json:"exec_ask_usd"`
	RefBidUSD  float64 `json:"ref_bid_usd"`
	RefAskUSD  float64 `json:"ref_ask_usd"`

	ExecMid       float64 `json:"exec_mid"`
	RefMid        float64 `json:"ref_mid"`
	ExecSpreadBps float64 `json:"exec_spread_bps"` // 各自以自己的中价为基
	RefSpreadBps  float64 `json:"ref_spread_bps"`
	// BasisBps 是有符号基差 (execMid−refMid)/refMid×1e4。不取绝对值:#14 要的就是方向
	// —— 谁贵谁便宜决定往哪个方向套。
	BasisBps float64 `json:"basis_bps"`

	// --- 台账 #14 的两个可执行方向,毛值、未扣任何手续费 ---
	// 账号级合约费率至今未核实(待办 #6),现在扣任何一个数都是把假设焊进数据里。
	// 所以只落毛值:离线拿到真费率后 net = 毛 − (吃单腿 + 吃单腿),一行 jq 就能补。
	//
	// XBuyExecBps  = 在 gate 买(付 exec ask)、在 binance 卖(打 ref bid)
	// XSellExecBps = 在 binance 买(付 ref ask)、在 gate 卖(打 exec bid)
	// 两者恒满足 XBuy + XSell = −(两个所各自的绝对价差之和)/refMid×1e4,
	// 即两个方向不可能同时为正 —— 这条恒等式是这条记录的自检位(见单测)。
	XBuyExecBps  float64 `json:"x_buy_exec_bps"`
	XSellExecBps float64 `json:"x_sell_exec_bps"`

	IndexPx float64 `json:"index_px"`
	// FundingRateBps 是 gate 侧当期资金费率(bps / 每个结算周期)。跨所对冲仓过结算点
	// 时这一项直接进损益。binance 侧资金费本轮【没有采】,所以净 carry 还算不出来。
	FundingRateBps float64 `json:"funding_rate_bps"`
	QuoteVol24h    float64 `json:"quote_vol_24h"`

	// Suspect 沿用 universe.go 的做法:只打标不删行 —— 机会不漏、陷阱看得见。
	Suspect string `json:"suspect,omitempty"`
}

// gateFuturesTicker 是 /futures/usdt/tickers 里我们用到的字段。
// 2026-09-09 实测:988 个合约里这几个字段 988/988 全部非空。
type gateFuturesTicker struct {
	Contract         string `json:"contract"`
	HighestBid       string `json:"highest_bid"`
	HighestSize      string `json:"highest_size"` // 买一档张数
	LowestAsk        string `json:"lowest_ask"`
	LowestSize       string `json:"lowest_size"` // 卖一档张数
	QuantoMultiplier string `json:"quanto_multiplier"`
	IndexPrice       string `json:"index_price"`
	FundingRate      string `json:"funding_rate"`
	Volume24hQuote   string `json:"volume_24h_quote"`
}

var (
	futScanMu      sync.Mutex
	futScanWriter  io.Writer
	futScanRunning bool
)

// StartGateFuturesScanner 每 refreshSec 秒扫一轮 gate 永续全市场并落盘。
// logDir 由调用方给(app 层持有 conf,本包不依赖 conf);为空则不落盘只记日志。
func StartGateFuturesScanner(logDir string, refreshSec int) {
	if refreshSec <= 0 {
		refreshSec = futScanIntervalSec
	}
	futScanMu.Lock()
	if futScanRunning {
		futScanMu.Unlock()
		return
	}
	futScanRunning = true
	if logDir != "" {
		futScanWriter = &lumberjack.Logger{
			Filename:   filepath.Join(logDir, futScanFileName),
			MaxSize:    futScanMaxSizeMB,
			MaxBackups: futScanMaxBackups,
			Compress:   true,
		}
	}
	futScanMu.Unlock()

	go func() {
		t := time.NewTicker(time.Duration(refreshSec) * time.Second)
		defer t.Stop()
		scanGateFuturesOnce()
		for range t.C {
			scanGateFuturesOnce()
		}
	}()
}

// scanGateFuturesOnce 拉一轮两个所的盘口,合成观测行并落盘。返回落盘行数。
func scanGateFuturesOnce() int {
	ref, err := fetchBinanceFuturesBooks()
	if err != nil || len(ref) == 0 {
		logger.Warnf("[mm-futscan] binance 永续参考拉取失败: %v", err)
		return 0
	}
	gate, err := fetchGateFuturesTickers()
	if err != nil || len(gate) == 0 {
		logger.Warnf("[mm-futscan] gate 永续行情拉取失败: %v", err)
		return 0
	}
	rows := buildFuturesScanRows(gate, ref, time.Now().UTC())

	futScanMu.Lock()
	w := futScanWriter
	futScanMu.Unlock()
	written := 0
	if w != nil {
		var werr error
		written, werr = writeFuturesScanRows(w, rows)
		if werr != nil {
			logger.Warnf("[mm-futscan] 落盘失败(已写 %d 行): %v", written, werr)
		}
	}
	logger.Infof("[mm-futscan] gate 永续 %d 个 · 有 binance 永续参考并过量门槛 %d 个 · 落盘 %d 行%s",
		len(gate), len(rows), written, futScanTopDesc(rows))
	return written
}

// futScanTopDesc 给日志摘一句「当下最大的一个跨所毛价差」,方便肉眼盯。
func futScanTopDesc(rows []FuturesScanRow) string {
	best := ""
	bestBps := 0.0
	for _, r := range rows {
		v := r.XBuyExecBps
		dir := "买gate卖bin"
		if r.XSellExecBps > v {
			v, dir = r.XSellExecBps, "买bin卖gate"
		}
		if best == "" || v > bestBps {
			best, bestBps = r.Contract+" "+dir, v
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" · 最优毛价差 %s %.1fbps", best, bestBps)
}

// buildFuturesScanRows 是纯函数:给定两个所的行情快照,产出观测行。
// 不碰网络、不碰时间、不碰磁盘 —— 所有口径都能在单测里逐个数字对。
func buildFuturesScanRows(gate []gateFuturesTicker, ref map[string]BookTicker, ts time.Time) []FuturesScanRow {
	out := make([]FuturesScanRow, 0, len(gate))
	for _, g := range gate {
		if !strings.HasSuffix(g.Contract, "_USDT") {
			continue
		}
		quoteVol := atofU(g.Volume24hQuote)
		if quoteVol < futScanMinQuoteVolUSD {
			continue // 打不满的合约不占磁盘,理由见 futScanMinQuoteVolUSD
		}
		refSym := strings.ReplaceAll(g.Contract, "_", "")
		rb, ok := ref[refSym]
		if !ok {
			continue
		}
		exec := BookTicker{
			Symbol: g.Contract,
			BidPx:  atofU(g.HighestBid), AskPx: atofU(g.LowestAsk),
		}
		mult := atofU(g.QuantoMultiplier)
		execMid, refMid := exec.Mid(), rb.Mid()
		if execMid <= 0 || refMid <= 0 || mult <= 0 {
			continue // 单边盘/脏数据:留下来只会让派生量变成 Inf/NaN
		}
		bidCont, askCont := atofU(g.HighestSize), atofU(g.LowestSize)
		// gate 的 size 字段是【张】,乘 quanto_multiplier 才是基础币;binance 本就是基础币。
		exec.BidQty, exec.AskQty = bidCont*mult, askCont*mult

		basisBps := (execMid - refMid) / refMid * 10000
		suspect := ""
		switch {
		case exec.SpreadBps() > suspectSpreadBps:
			suspect = "宽价差"
		case basisBps > suspectDivergenceBps || basisBps < -suspectDivergenceBps:
			suspect = "偏离大"
		}

		out = append(out, FuturesScanRow{
			Ts: ts, Exchange: "gate-futures", Feed: "binance-futures",
			Contract: g.Contract, RefSymbol: refSym,
			ExecBidPx: exec.BidPx, ExecAskPx: exec.AskPx,
			RefBidPx: rb.BidPx, RefAskPx: rb.AskPx,
			ExecBidSzCont: bidCont, ExecAskSzCont: askCont,
			RefBidQty: rb.BidQty, RefAskQty: rb.AskQty,
			QuantoMultiplier: mult,
			ExecBidUSD:       exec.BidQty * exec.BidPx,
			ExecAskUSD:       exec.AskQty * exec.AskPx,
			RefBidUSD:        rb.BidQty * rb.BidPx,
			RefAskUSD:        rb.AskQty * rb.AskPx,
			ExecMid:          execMid, RefMid: refMid,
			ExecSpreadBps: exec.SpreadBps(), RefSpreadBps: rb.SpreadBps(),
			BasisBps: basisBps,
			// 两个方向都以 refMid 为基,与 universe.go / observe_store.go 的 buyEdge/sellEdge
			// 同一口径 —— 换基会让这份数据没法和已有观测放在一起比。
			XBuyExecBps:  (rb.BidPx - exec.AskPx) / refMid * 10000,
			XSellExecBps: (exec.BidPx - rb.AskPx) / refMid * 10000,
			IndexPx:      atofU(g.IndexPrice),
			// gate 的 funding_rate 是比例(如 0.000025),×1e4 得 bps。
			FundingRateBps: atofU(g.FundingRate) * 10000,
			QuoteVol24h:    quoteVol,
			Suspect:        suspect,
		})
	}
	return out
}

// writeFuturesScanRows 逐行写单行 JSON。返回成功写入的行数。
// 单行一条、不带任何前缀 —— 这样 `jq -s` / pandas.read_json(lines=True) 能直接吃,
// 不需要先 grep 剥前缀(observe 那条线就得先剥 "[mm-observe] ")。
func writeFuturesScanRows(w io.Writer, rows []FuturesScanRow) (int, error) {
	n := 0
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			// 只可能是 Inf/NaN,即上游盘口已经脏了 —— 要吵出来,不能静默吞掉
			return n, fmt.Errorf("marshal %s: %w", r.Contract, err)
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func fetchGateFuturesTickers() ([]gateFuturesTicker, error) {
	body, err := getJSON(gateFuturesTickersURL)
	if err != nil {
		return nil, err
	}
	var raw []gateFuturesTicker
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// fetchBinanceFuturesBooks 拉 binance USDT 永续全市场买一卖一(含各自挂单量)。
// 和 fetchBinanceRefMids() 的区别就在这个「含量、且不折成中价」—— 跨所套利问的是
// 「能不能吃到、能吃多少」,只有中价答不了。
func fetchBinanceFuturesBooks() (map[string]BookTicker, error) {
	body, err := getJSON(binanceFuturesBookURL)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Symbol   string `json:"symbol"`
		BidPrice string `json:"bidPrice"`
		BidQty   string `json:"bidQty"`
		AskPrice string `json:"askPrice"`
		AskQty   string `json:"askQty"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]BookTicker, len(raw))
	for _, r := range raw {
		if !strings.HasSuffix(r.Symbol, "USDT") {
			continue
		}
		bt := BookTicker{
			Symbol: r.Symbol,
			BidPx:  atofU(r.BidPrice), BidQty: atofU(r.BidQty),
			AskPx: atofU(r.AskPrice), AskQty: atofU(r.AskQty),
		}
		if bt.Mid() <= 0 {
			continue
		}
		out[r.Symbol] = bt
	}
	return out, nil
}
