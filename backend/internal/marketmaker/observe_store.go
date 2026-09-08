package marketmaker

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/logger"
)

// ObserveRow is the latest measured edge for one exec-exchange pair vs the feed,
// with the maker fee folded in so NET edge (what you could actually keep) is front
// and center — the gross edge alone is misleading.
type ObserveRow struct {
	Exchange string `json:"exchange"` // 执行所(成交发生地),如 gate
	Feed     string `json:"feed"`     // 参考所(行情来源),如 binance
	Symbol   string `json:"symbol"`
	// 两所盘口快照 —— 原始报价,不是派生量。下面每一个 bps 都能只用这四个数
	// 重算一遍;算不出同一个结果,就是推导错了而不是数据错了。
	// 台账 #7 之所以卡住,正是因为过去只有派生量、没有这四个原始数。
	RefBidPx  float64 `json:"ref_bid_px"`
	RefAskPx  float64 `json:"ref_ask_px"`
	ExecBidPx float64 `json:"exec_bid_px"`
	ExecAskPx float64 `json:"exec_ask_px"`

	RefMid        float64 `json:"ref_mid"`
	ExecMid       float64 `json:"exec_mid"`
	ExecSpreadBps float64 `json:"exec_spread_bps"` // s:执行所自身价差
	// MidDiffBps 是基差 b =(execMid−refMid)/refMid×10000,台账 #7 的核心量。
	// |b| > s/2 时,报价中心锚在参考所会把两条腿一起推到执行所盘口的同一侧。
	MidDiffBps float64 `json:"mid_diff_bps"`
	// 报价中心锚:锚在哪个所、当时值多少。切锚是策略变更,其效果必须能被离线分辨,
	// 所以逐条记下来 —— 否则 A/B 之后没人说得清对比的是哪两组样本。
	QuoteAnchor    string  `json:"quote_anchor"` // "ref"=参考所(默认) | "exec"=执行所
	AnchorMid      float64 `json:"anchor_mid"`
	BuyEdgeBps     float64 `json:"buy_edge_bps"`
	SellEdgeBps    float64 `json:"sell_edge_bps"`
	FeeBps         float64 `json:"fee_bps"`           // one-leg maker fee (bps)
	FeeLive        bool    `json:"fee_live"`          // true=交易所实时费率, false=默认假设
	NetBestEdgeBps float64 `json:"net_best_edge_bps"` // BestEdge - 2*fee(买卖两腿)
	// RoundTripNetBps 是【真正能靠做市赚到】的净边:两条腿都成交才叫一个来回,
	// 收入 = buyEdge + sellEdge ≡ 执行所自身价差(与参考所选谁无关),成本 = 两腿 maker 费。
	//
	// 为什么不能看 NetBestEdgeBps:它拿【单腿】收入减【双腿】成本,量纲就不齐。
	// 有基差 b 时 buyEdge ≈ s/2 - b、sellEdge ≈ s/2 + b,取 max 得 s/2 + |b| ——
	// 基差被整段当成了"边"。|b| > s/2(半价差)时它就比真实来回还高,虚高 ≈ |b| - s/2。
	// 这正是台账 #7"16/20 候选 |基差| > 半价差"与"面板正、capture 负"之间的桥。
	//
	// 用"≈"是因为 b 以参考所中价为基、s 以执行所中价为基,展开后还有一个交叉项:
	// 精确式 虚高 = |b| − s/2 + b·s/20000(推导与断言见 TestObserveRecordIsOfflineVerifiable)。
	// #7 工作点上交叉项 ≈0.05bps,不改变任何结论,但别把一阶式当恒等式用。
	RoundTripNetBps float64   `json:"round_trip_net_bps"`
	Ts              time.Time `json:"ts"`
}

// BestEdgeBps is the larger of the two capturable edges (gross, before fees).
func (r ObserveRow) BestEdgeBps() float64 {
	if r.BuyEdgeBps > r.SellEdgeBps {
		return r.BuyEdgeBps
	}
	return r.SellEdgeBps
}

// observeRow 把一次"两所盘口 + 费率"的快照折成一条观测记录。
//
// 抽成函数(而不是留在引擎循环里内联)只为一件事:让【落盘的口径】和【单测的口径】
// 是同一份代码。台账 #7 的教训就是结论算在一个地方、复核在另一个地方,对不上时
// 没人分得清是数据错还是推导错。
//
// 前置条件:ref/exec 两个盘口都必须有效(Mid()>0)。无效时返回零值行,由
// logObserve/recordObserve 丢弃 —— 不能让 Inf/NaN 流进 JSON(json.Marshal 会
// 直接报错,那条记录就无声消失了,这比少一条记录更糟)。
func (p PairConfig) observeRow(exchange, feed string, ref, exec BookTicker, feeBps float64, feeLive bool, ts time.Time) ObserveRow {
	refMid, execMid := ref.Mid(), exec.Mid()
	if refMid <= 0 || execMid <= 0 {
		return ObserveRow{}
	}
	buyEdge := (refMid - exec.BidPx) / refMid * 10000
	sellEdge := (exec.AskPx - refMid) / refMid * 10000
	best := buyEdge
	if sellEdge > best {
		best = sellEdge
	}
	anchor := p.QuoteAnchor
	if anchor == "" {
		anchor = "ref" // 空=历史默认。落盘写死成 "ref",离线按锚分组时不用再猜空串是什么
	}
	return ObserveRow{
		Exchange: exchange, Feed: feed, Symbol: p.ExecSymbol,
		RefBidPx: ref.BidPx, RefAskPx: ref.AskPx, ExecBidPx: exec.BidPx, ExecAskPx: exec.AskPx,
		RefMid: refMid, ExecMid: execMid,
		ExecSpreadBps: exec.SpreadBps(),
		MidDiffBps:    (execMid - refMid) / refMid * 10000,
		QuoteAnchor:   anchor, AnchorMid: p.anchorMid(refMid, execMid),
		BuyEdgeBps: buyEdge, SellEdgeBps: sellEdge,
		FeeBps: feeBps, FeeLive: feeLive,
		NetBestEdgeBps:  best - 2*feeBps,
		RoundTripNetBps: exec.SpreadBps() - 2*feeBps,
		Ts:              ts,
	}
}

// logObserve 把一条观测记录以【单行 JSON】写进 server.log(main.go 已把 log 落盘)。
//
// 为什么换掉原来的 key=value 文本:台账 #7 的结论("ONG_USDT 中位基差 45.2bps ≫
// 中位半价差 11.5bps")此前只活在内存的 observeStore 和代码注释里,重启即失,
// 第二个人无法复核 —— 这才是 #7 迟迟不能关的真正原因,不是结论本身有疑问。
// 逐条 JSON 落盘之后,复核只要一条管道(不需要跑这个程序、不需要交易所凭据):
//
//	grep -o '\[mm-observe\] {.*}' logs/server.log | sed 's/^\[mm-observe\] //' |
//	  jq -s 'map(select(.symbol=="ONG_USDT")) |
//	         (map(.mid_diff_bps)|sort) as $b | (map(.exec_spread_bps)|sort) as $s |
//	         {n: length,
//	          basis_bps:      $b[($b|length/2|floor)],
//	          spread_bps:     $s[($s|length/2|floor)],
//	          inflate_bps:    (($b[($b|length/2|floor)]|fabs) - $s[($s|length/2|floor)]/2)}'
//
// inflate_bps 是 NetBestEdgeBps 相对 RoundTripNetBps 的虚高,这里取一阶式 |b| − s/2。
// 精确式还有一个交叉项(+b·s/20000,量级 ~0.05bps),推导见 RoundTripNetBps 的注释与
// TestObserveRecordIsOfflineVerifiable;要严格对账就直接用每条记录里的
// net_best_edge_bps − round_trip_net_bps,那是原样落盘的,不受近似影响。
//
// 为什么沿用 logger 而不是新开一个 .jsonl / 建表:仓库里只有 logger 这一套落盘设施
// (main.go:73 把 log 接到 logs/server.log),再造一套要带上文件句柄、轮转、配置路径;
// 建表则要在 1 秒 1 条的观测热路径上挂一个新的失败点。两者都比这条重。
// 代价说清楚:这条线比原来长约一倍,server.log 体积会涨,且仓库目前没有日志轮转。
func logObserve(r ObserveRow) {
	if r.RefMid <= 0 || r.ExecMid <= 0 {
		return
	}
	b, err := json.Marshal(r)
	if err != nil { // 只可能是 Inf/NaN,即上游盘口已经脏了 —— 要吵出来,不能静默
		logger.Warnf("[mm-observe] marshal %s@%s failed: %v", r.Symbol, r.Exchange, err)
		return
	}
	logger.Infof("[mm-observe] %s", b)
}

// PairStat is the accumulated net-edge statistics for one pair over the run —
// this is what turns "a live number" into "data to decide on".
type PairStat struct {
	Exchange  string    `json:"exchange"`
	Symbol    string    `json:"symbol"`
	Samples   int64     `json:"samples"`
	MaxNetBps float64   `json:"max_net_bps"`
	AvgNetBps float64   `json:"avg_net_bps"`
	PosRate   float64   `json:"pos_rate"` // net>0 的采样占比
	Since     time.Time `json:"since"`
}

type statAcc struct {
	samples, pos   int64
	maxNet, sumNet float64
	since          time.Time
}

var (
	observeMu    sync.RWMutex
	observeStore = map[string]ObserveRow{}
	statStore    = map[string]*statAcc{}
	mmRunning    bool
)

func recordObserve(r ObserveRow) {
	k := r.Exchange + "|" + r.Symbol
	observeMu.Lock()
	observeStore[k] = r
	s := statStore[k]
	if s == nil {
		s = &statAcc{since: time.Now()}
		statStore[k] = s
	}
	s.samples++
	s.sumNet += r.NetBestEdgeBps
	if s.samples == 1 || r.NetBestEdgeBps > s.maxNet {
		s.maxNet = r.NetBestEdgeBps
	}
	if r.NetBestEdgeBps > 0 {
		s.pos++
	}
	observeMu.Unlock()
}

func setRunning(v bool) {
	observeMu.Lock()
	mmRunning = v
	observeMu.Unlock()
}

// ObserveSnapshot returns the latest observed row per pair (widest NET edge first)
// plus whether the engine is running — for the dashboard's 做市 card.
func ObserveSnapshot() ([]ObserveRow, bool) {
	observeMu.RLock()
	defer observeMu.RUnlock()
	out := make([]ObserveRow, 0, len(observeStore))
	for _, r := range observeStore {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NetBestEdgeBps > out[j].NetBestEdgeBps })
	return out, mmRunning
}

// StatsSnapshot returns per-pair net-edge stats (highest max-net first).
func StatsSnapshot() []PairStat {
	observeMu.RLock()
	defer observeMu.RUnlock()
	out := make([]PairStat, 0, len(statStore))
	for k, s := range statStore {
		ex, sym, _ := strings.Cut(k, "|")
		ps := PairStat{Exchange: ex, Symbol: sym, Samples: s.samples, MaxNetBps: s.maxNet, Since: s.since}
		if s.samples > 0 {
			ps.AvgNetBps = s.sumNet / float64(s.samples)
			ps.PosRate = float64(s.pos) / float64(s.samples)
		}
		out = append(out, ps)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MaxNetBps > out[j].MaxNetBps })
	return out
}
