package marketmaker

import (
	"math"
	"testing"
	"time"
)

func approx(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %.4f want %.4f", what, got, want)
	}
}

func TestMarkoutBpsSign(t *testing.T) {
	// 买入后价格涨 = 赚 = 正
	approx(t, markoutBps("buy", 100, 100.1), 10, 0.01, "buy 涨")
	// 买入后价格跌 = 被逆向选择 = 负
	approx(t, markoutBps("buy", 100, 99.9), -10, 0.01, "buy 跌")
	// 卖出后价格跌 = 赚 = 正(符号必须反过来,这是最容易写错的一处)
	approx(t, markoutBps("sell", 100, 99.9), 10, 0.01, "sell 跌")
	// 卖出后价格涨 = 亏 = 负
	approx(t, markoutBps("sell", 100, 100.1), -10, 0.01, "sell 涨")
	// 非法输入不炸,返回 0
	approx(t, markoutBps("buy", 0, 100), 0, 0, "零价")
}

func TestTrackerResolvesAllHorizons(t *testing.T) {
	tr := NewMarkoutTracker()
	t0 := time.Now()

	tr.RecordFill("gate", "DOT_USDT", "f1", "buy", 100, 10, 2, t0)
	// 三个 horizon 各喂一个样本:1s 涨 10bps,5s 涨 20bps,30s 跌回 -5bps
	tr.Observe("DOT_USDT", 100.10, t0.Add(1*time.Second))
	tr.Observe("DOT_USDT", 100.20, t0.Add(5*time.Second))
	tr.Observe("DOT_USDT", 99.95, t0.Add(30*time.Second))

	if n := tr.PendingCount(); n != 0 {
		t.Fatalf("三个 horizon 都该结算完,还剩 %d 笔", n)
	}
	st := tr.Stats()
	if len(st) != 1 || st[0].Fills != 1 {
		t.Fatalf("汇总应为 1 个品种 1 笔成交,得到 %+v", st)
	}
	approx(t, st[0].AvgByHz["1s"], 10, 0.01, "1s 毛")
	approx(t, st[0].AvgByHz["5s"], 20, 0.01, "5s 毛")
	approx(t, st[0].AvgByHz["30s"], -5, 0.01, "30s 毛")
	// 扣单腿手续费 2bps
	approx(t, st[0].AvgNetByHz["1s"], 8, 0.01, "1s 净")
	approx(t, st[0].AvgNetByHz["30s"], -7, 0.01, "30s 净")
}

func TestTrackerUsesFirstSampleAtOrAfterHorizon(t *testing.T) {
	// 必须取 target 时刻【之后】的第一个样本。用更早的样本 = 拿过期价充数,
	// 会系统性低估逆向选择 —— 那正是本模块要抓的东西。
	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "X_USDT", "f1", "buy", 100, 1, 0, t0)

	tr.Observe("X_USDT", 999, t0.Add(500*time.Millisecond)) // 早于 1s,不该被用
	tr.Observe("X_USDT", 100.05, t0.Add(1200*time.Millisecond))
	tr.Observe("X_USDT", 100.05, t0.Add(5*time.Second))
	tr.Observe("X_USDT", 100.05, t0.Add(30*time.Second))

	st := tr.Stats()
	approx(t, st[0].AvgByHz["1s"], 5, 0.01, "1s 应取 1.2s 的样本而非 0.5s 的")
}

func TestAdverseSelectionShowsAsNegative(t *testing.T) {
	// 复刻"扣费前 capture 就为负"的形态:每笔买入后价格都往下走。
	// 这正是 2026-09-08 小唐怀疑的形态 —— 报价中心挂错边,买单整天被砸中。
	tr := NewMarkoutTracker()
	base := time.Now()
	for i := 0; i < 5; i++ {
		t0 := base.Add(time.Duration(i) * time.Minute)
		tr.RecordFill("gate", "DOT_USDT", "f", "buy", 100, 1, 2, t0)
		tr.Observe("DOT_USDT", 99.97, t0.Add(1*time.Second))
		tr.Observe("DOT_USDT", 99.95, t0.Add(5*time.Second))
		tr.Observe("DOT_USDT", 99.90, t0.Add(30*time.Second))
	}
	st := tr.Stats()
	if st[0].AvgByHz["30s"] >= 0 {
		t.Fatalf("持续被逆向选择时 30s markout 必须为负,得到 %.2f", st[0].AvgByHz["30s"])
	}
	// 关键判据:毛 markout 已经为负 → 问题不在手续费,加品种只会亏得更快
	if st[0].AvgByHz["1s"] >= 0 {
		t.Fatalf("1s 毛 markout 应为负,得到 %.2f", st[0].AvgByHz["1s"])
	}
}

// TestStaleSampleProducesNoMarkout —— 这条测试原名 TestStaleFillsAreDropped,
// 名字是错的:那笔成交从来没有被丢弃,而是被【用 5 分钟之后的价】结算了三次,
// PendingCount 归零是因为它"完成"了。名字说丢了,行为是照算 —— 于是这个坑
// 被一条绿色的测试盖了起来。
//
// 现在行为对上了名字:超过 maxSampleLag 的样本不产出 markout。
// 这笔成交仍然被记下来(见 markout_sink_test.go 的
// TestStaleSampleKeepsEvidenceWithoutMarkout:证据全在,只是没有数),
// 但它不会给任何一个 horizon 贡献均值。
func TestStaleSampleProducesNoMarkout(t *testing.T) {
	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "Y_USDT", "f1", "buy", 100, 1, 0, t0)
	tr.Observe("Y_USDT", 100, t0.Add(5*time.Minute)) // 断流恢复后的第一个样本

	// pending 仍然要清空:三个 horizon 都已了结(结论是"这个样本不能用"),
	// 再等只会来更晚的样本。
	if n := tr.PendingCount(); n != 0 {
		t.Fatalf("三个 horizon 都该了结,pending 还剩 %d 笔", n)
	}
	st := tr.Stats()
	if len(st) != 1 || st[0].Fills != 1 {
		t.Fatalf("这笔成交必须仍被记下(缺口本身是数据),得到 %+v", st)
	}
	// 关键:一个可用样本都没有 —— 这才是"陈旧"该有的后果。
	if len(st[0].FillsByHz) != 0 {
		t.Fatalf("陈旧样本不得产出任何 horizon 的 markout,得到 %+v", st[0].FillsByHz)
	}
	if len(st[0].AvgByHz) != 0 {
		t.Fatalf("没有可用样本就不该有均值(否则 0 会被当成一个观测),得到 %+v", st[0].AvgByHz)
	}
}

// TestPendingIsDroppedWhenSamplesNeverArrive 是真正的"丢弃"路径。
//
// 与上一条的区别正是落库要分开的两种缺口:上面是【有样本但太晚】,
// 这里是【从头到尾一个样本都没有】。后者只能靠超时兜底清出 pending,
// 否则行情永久断流会让 pending 无限堆积。
func TestPendingIsDroppedWhenSamplesNeverArrive(t *testing.T) {
	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "Z_USDT", "f1", "buy", 100, 1, 0, t0)

	// 只有【成交之前】的样本;成交之后行情再没回来,任何 target 都取不到样本。
	tr.mu.Lock()
	tr.mids["Z_USDT"] = []midSample{{ts: t0.Add(-time.Second), mid: 100}}
	tr.resolveLocked("Z_USDT", t0.Add(3*time.Minute)) // 超过 30s+2min
	tr.mu.Unlock()

	if n := tr.PendingCount(); n != 0 {
		t.Fatalf("永远采不到样本的成交必须被超时清出,还剩 %d 笔", n)
	}
	if st := tr.Stats(); len(st) != 0 {
		t.Fatalf("超时丢弃的成交不进 done/Stats(它没有任何测量结果),得到 %+v", st)
	}
}

// TestSampleLagCapIsPerHorizon 钉死阈值本身:max(2s, horizon/4)。
//
// 为什么不是一个固定值:样本节奏由 refresh_ms(默认 1000)决定,与 horizon 无关,
// 所以短 horizon 的下限只能由采样栅格定(2s = 容忍一次漏采);
// 而长 horizon 按比例放宽才不会把"只被拉长几个百分点"的可用样本误杀。
// 一个 5s 才回来的样本:1s/5s 两档作废,30s 那档(上限 7.5s)照常可用。
func TestSampleLagCapIsPerHorizon(t *testing.T) {
	if got := maxSampleLag(time.Second); got != 2*time.Second {
		t.Fatalf("1s 档上限应为下限 2s,得到 %v", got)
	}
	if got := maxSampleLag(5 * time.Second); got != 2*time.Second {
		t.Fatalf("5s 档上限应为下限 2s,得到 %v", got)
	}
	if got := maxSampleLag(30 * time.Second); got != 7500*time.Millisecond {
		t.Fatalf("30s 档上限应为 h/4=7.5s,得到 %v", got)
	}

	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "W_USDT", "f1", "buy", 100, 1, 0, t0)
	// 每档都在成交后 35s 才来一个样本:
	//   1s  档 lag = 34s > 2s    → 作废
	//   5s  档 lag = 30s > 2s    → 作废
	//   30s 档 lag =  5s ≤ 7.5s  → 可用
	tr.Observe("W_USDT", 100.10, t0.Add(35*time.Second))

	st := tr.Stats()
	if len(st) != 1 {
		t.Fatalf("应有 1 个品种,得到 %+v", st)
	}
	if _, ok := st[0].AvgByHz["1s"]; ok {
		t.Error("1s 档滞后 34s,远超 2s 上限,不该产出 markout")
	}
	if _, ok := st[0].AvgByHz["5s"]; ok {
		t.Error("5s 档滞后 30s,远超 2s 上限,不该产出 markout")
	}
	if st[0].FillsByHz["30s"] != 1 {
		t.Fatalf("30s 档滞后 5s 在 7.5s 上限内,应照常可用,得到 %+v", st[0].FillsByHz)
	}
	approx(t, st[0].AvgByHz["30s"], 10, 0.01, "30s markout")
}

// TestLagCapBoundaryIsInclusive:恰好等于上限的样本【可用】,多 1ms 就不可用。
// 边界写反(用 >= 判作废)会在健康行情下白扔掉一整个 tick 的样本。
func TestLagCapBoundaryIsInclusive(t *testing.T) {
	cap1s := maxSampleLag(time.Second)
	t0 := time.Now()

	// 只有三个 horizon 都了结这笔才会进 done/Stats,所以后两档也要喂准时的样本。
	run := func(lag time.Duration) MarkoutStat {
		tr := NewMarkoutTracker()
		tr.RecordFill("gate", "B_USDT", "f1", "buy", 100, 1, 0, t0)
		tr.Observe("B_USDT", 100.10, t0.Add(time.Second+lag)) // 1s 档,滞后 = lag
		tr.Observe("B_USDT", 100.10, t0.Add(5*time.Second))   // 5s 档,准时
		tr.Observe("B_USDT", 100.10, t0.Add(30*time.Second))  // 30s 档,准时
		st := tr.Stats()
		if len(st) != 1 {
			t.Fatalf("lag=%v:三个 horizon 都该了结,Stats 得到 %+v", lag, st)
		}
		return st[0]
	}

	if n := run(cap1s).FillsByHz["1s"]; n != 1 {
		t.Fatalf("lag 恰好等于上限的样本必须可用(否则健康行情下会白扔一整个 tick),得到 %d 个", n)
	}
	if n := run(cap1s + time.Millisecond).FillsByHz["1s"]; n != 0 {
		t.Fatalf("超上限 1ms 的样本就不该产出 markout,得到 %d 个", n)
	}
}

// TestMarkoutBasisContamination 锁死"基准必须与成交同所"这条(台账 #7)。
// 场景:gate 比 binance 系统性贵 45.2bps(2026-09-08 ONG_USDT 实测形态),
// 价格【完全不动】—— 真实 markout 应为 0。
func TestMarkoutBasisContamination(t *testing.T) {
	const refMid = 100.0
	const basisBps = 45.2
	execMid := refMid * (1 + basisBps/10000) // gate 中价
	fillPx := execMid                        // 在 gate 按其中价成交

	// 错误做法:拿参考所(binance)中价当 midAfter —— 价格没动,却测出 ∓基差。
	if got := markoutBps("buy", fillPx, refMid); math.Abs(got+basisBps) > 0.3 {
		t.Fatalf("跨所基准下买单 markout 应≈-%.1fbps(纯基差),得到 %.2f", basisBps, got)
	}
	if got := markoutBps("sell", fillPx, refMid); math.Abs(got-basisBps) > 0.3 {
		t.Fatalf("跨所基准下卖单 markout 应≈+%.1fbps(纯基差),得到 %.2f", basisBps, got)
	}
	// 正确做法:同所基准,价格没动就该是 0,基差整段抵消。
	approx(t, markoutBps("buy", fillPx, execMid), 0, 1e-9, "同所基准 buy")
	approx(t, markoutBps("sell", fillPx, execMid), 0, 1e-9, "同所基准 sell")
}
