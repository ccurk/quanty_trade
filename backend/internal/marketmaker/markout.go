package marketmaker

import (
	"sort"
	"sync"
	"time"
)

// Markout 度量 —— 回答"这笔成交之后价格往哪走了"。
//
// 为什么必须有它:引擎原有的 buyEdge/sellEdge(engine.go:139)测的是【报价时刻】
// 对【同时刻】参考中价的边。这个数可以永远为正,而实际 PnL 永远为负 ——
// 因为它从不看成交【之后】价格往哪走。
//
// 结果是引擎在结构上无法区分亏损来自三者中的哪一个:
//   手续费(费率问题,换 venue 可解)
//   价差不够(选品问题,换品种可解)
//   逆向选择(被知情单打中,换品种无解,只会亏得更快)
//
// markout 把三者分开:
//   markout 为正、扣费后为负 → 问题在费率 → 转合约(gate 永续 maker 2bps vs 现货 20bps)
//   markout 为负            → 问题在逆向选择 → 加任何品种都是放大亏损,先修报价中心
//
// 2026-09-08 实测背景:gate 相对 binance 有系统性基差,20 个合约候选里 16 个、
// 22 个现货候选里 12 个 |基差| > 半价差。引擎却把报价中心挂在 binance 中价上,
// 于是两条腿同时挂在 gate 盘口的错误一侧 —— 这是 markout 为负的头号嫌疑。
// 本文件只负责【量出来】,不负责修;修之前先要有能证实/证伪的数。

// MarkoutHorizons 是采样时点。取 1s/5s/30s 是因为:
//   1s  —— 抓瞬时逆向选择(被快单打中)
//   5s  —— 抓短线趋势跟随
//   30s —— 抓真实持仓成本;超过这个尺度就更像方向性风险而非做市质量
var MarkoutHorizons = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}

// markoutBps 计算单笔成交在某个时点的 markout(bps)。
//
// 正 = 成交后价格朝对我们有利的方向走(买完涨了 / 卖完跌了)= 这笔成交是好的。
// 负 = 被逆向选择,对手方比我们知道得多。
//
// 纯函数,不碰时间和 IO —— 阈值调整与回归全部可单测。
func markoutBps(side string, fillPx, refMidAfter float64) float64 {
	if fillPx <= 0 || refMidAfter <= 0 {
		return 0
	}
	move := (refMidAfter - fillPx) / fillPx * 10000
	if side == "sell" {
		return -move // 卖出后价格下跌才是赚,所以要反号
	}
	return move
}

// MarkoutRow 是一笔成交的完整 markout 画像,按 horizon 展开。
type MarkoutRow struct {
	Exchange string             `json:"exchange"`
	Symbol   string             `json:"symbol"`
	FillID   string             `json:"fill_id"`
	Side     string             `json:"side"`
	FillPx   float64            `json:"fill_px"`
	Amount   float64            `json:"amount"`
	FeeBps   float64            `json:"fee_bps"`
	ByHorizon map[string]float64 `json:"by_horizon"` // "1s" -> markout bps
	FillTs   time.Time          `json:"fill_ts"`
}

// NetAtBps 是扣掉单腿手续费后的 markout —— 这才是这笔成交真实赚没赚。
func (r MarkoutRow) NetAtBps(h string) float64 { return r.ByHorizon[h] - r.FeeBps }

type midSample struct {
	ts  time.Time
	mid float64
}

type pendingFill struct {
	row  MarkoutRow
	left map[time.Duration]bool // 还没采到的 horizon
}

// MarkoutTracker 记录成交,并在参考中价样本到点时把 markout 补齐。
//
// 设计取舍:不主动去拉行情,而是【搭车】引擎本来就有的观测循环 ——
// 每轮观测都会算 refMid,顺手喂给 Observe() 即可。这样不增加任何请求,
// 也不会因为限流把主流程拖慢。
type MarkoutTracker struct {
	mu      sync.Mutex
	mids    map[string][]midSample // symbol -> 最近的参考中价样本
	pending map[string][]*pendingFill
	done    []MarkoutRow
	maxDone int
	// 成交去重:引擎每 10s 拉最近 100 笔,同一笔会被反复拿到。
	// 不去重的话一笔成交会被重复计入,把统计彻底带偏。
	seen    map[string]bool
	seenMax int
}

func NewMarkoutTracker() *MarkoutTracker {
	return &MarkoutTracker{
		mids:    map[string][]midSample{},
		pending: map[string][]*pendingFill{},
		maxDone: 5000,
		seen:    map[string]bool{},
		seenMax: 20000,
	}
}

// Observe 喂一个参考中价样本,并结算所有到点的 pending 成交。
// 引擎的观测循环每轮调一次即可。
func (t *MarkoutTracker) Observe(symbol string, refMid float64, now time.Time) {
	if refMid <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	h := append(t.mids[symbol], midSample{ts: now, mid: refMid})
	// 只留最长 horizon 再多一点的窗口,防内存无限涨
	cutoff := now.Add(-(MarkoutHorizons[len(MarkoutHorizons)-1] + 30*time.Second))
	i := sort.Search(len(h), func(k int) bool { return h[k].ts.After(cutoff) })
	t.mids[symbol] = h[i:]

	t.resolveLocked(symbol, now)
}

// RecordFill 登记一笔成交,等待后续 horizon 到点。
// 同一个 fillID 重复登记会被忽略 —— 调用方可以放心每轮把最近成交全喂进来。
func (t *MarkoutTracker) RecordFill(exchange, symbol, fillID, side string, px, amount, feeBps float64, ts time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := symbol + "|" + fillID
	if t.seen[key] {
		return
	}
	if len(t.seen) >= t.seenMax {
		t.seen = map[string]bool{} // 简单清空:重放最多多算一轮,比无限涨内存好
	}
	t.seen[key] = true
	left := map[time.Duration]bool{}
	for _, h := range MarkoutHorizons {
		left[h] = true
	}
	t.pending[symbol] = append(t.pending[symbol], &pendingFill{
		row: MarkoutRow{
			Exchange: exchange, Symbol: symbol, FillID: fillID, Side: side,
			FillPx: px, Amount: amount, FeeBps: feeBps,
			ByHorizon: map[string]float64{}, FillTs: ts,
		},
		left: left,
	})
}

// resolveLocked 把到点的 horizon 补上。调用方须持锁。
func (t *MarkoutTracker) resolveLocked(symbol string, now time.Time) {
	samples := t.mids[symbol]
	if len(samples) == 0 {
		return
	}
	keep := t.pending[symbol][:0]
	for _, pf := range t.pending[symbol] {
		for _, h := range MarkoutHorizons {
			if !pf.left[h] {
				continue
			}
			target := pf.row.FillTs.Add(h)
			if now.Before(target) {
				continue
			}
			// 取 target 时刻之后的第一个样本 —— 宁可稍晚,不用更早的(那是偷看未来的反面:用过期价)
			idx := sort.Search(len(samples), func(k int) bool { return !samples[k].ts.Before(target) })
			if idx >= len(samples) {
				continue // 还没有覆盖到该时点的样本,下轮再说
			}
			pf.row.ByHorizon[horizonKey(h)] = markoutBps(pf.row.Side, pf.row.FillPx, samples[idx].mid)
			pf.left[h] = false
		}
		if len(pf.row.ByHorizon) == len(MarkoutHorizons) {
			t.done = append(t.done, pf.row)
			if len(t.done) > t.maxDone {
				t.done = t.done[len(t.done)-t.maxDone:]
			}
			continue // 已完成,不再 keep
		}
		// 超过最长 horizon 还没采齐(行情断流),丢弃避免堆积
		if now.Sub(pf.row.FillTs) > MarkoutHorizons[len(MarkoutHorizons)-1]+2*time.Minute {
			continue
		}
		keep = append(keep, pf)
	}
	t.pending[symbol] = keep
}

func horizonKey(h time.Duration) string {
	switch h {
	case time.Second:
		return "1s"
	case 5 * time.Second:
		return "5s"
	case 30 * time.Second:
		return "30s"
	}
	return h.String()
}

// MarkoutStat 是一个品种的汇总 —— 判断"要不要继续做这个品种"就看这张表。
type MarkoutStat struct {
	Exchange   string             `json:"exchange"`
	Symbol     string             `json:"symbol"`
	Fills      int                `json:"fills"`
	AvgByHz    map[string]float64 `json:"avg_by_horizon"`     // 毛 markout
	AvgNetByHz map[string]float64 `json:"avg_net_by_horizon"` // 扣单腿手续费
}

// Stats 汇总已完成的 markout。这是给老徐/小唐做决策的入口。
func (t *MarkoutTracker) Stats() []MarkoutStat {
	t.mu.Lock()
	defer t.mu.Unlock()

	type acc struct {
		n      int
		sum    map[string]float64
		sumNet map[string]float64
		ex     string
	}
	byKey := map[string]*acc{}
	for _, r := range t.done {
		a := byKey[r.Symbol]
		if a == nil {
			a = &acc{sum: map[string]float64{}, sumNet: map[string]float64{}, ex: r.Exchange}
			byKey[r.Symbol] = a
		}
		a.n++
		for k, v := range r.ByHorizon {
			a.sum[k] += v
			a.sumNet[k] += v - r.FeeBps
		}
	}

	out := make([]MarkoutStat, 0, len(byKey))
	for sym, a := range byKey {
		st := MarkoutStat{Exchange: a.ex, Symbol: sym, Fills: a.n,
			AvgByHz: map[string]float64{}, AvgNetByHz: map[string]float64{}}
		for k, v := range a.sum {
			st.AvgByHz[k] = v / float64(a.n)
		}
		for k, v := range a.sumNet {
			st.AvgNetByHz[k] = v / float64(a.n)
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

// PendingCount 用于自检:长期不为 0 说明行情样本没喂进来。
func (t *MarkoutTracker) PendingCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, v := range t.pending {
		n += len(v)
	}
	return n
}

// markoutTracker 是引擎全局的 markout 记录器。做成包级单例,是为了让 HTTP 层
// 能直接读到统计(和 observe_store / pnl 的既有做法保持一致)。
var markoutTracker = NewMarkoutTracker()

// MarkoutStats 暴露给 API 层。
func MarkoutStats() []MarkoutStat { return markoutTracker.Stats() }
