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

func TestStaleFillsAreDropped(t *testing.T) {
	// 行情断流时 pending 不能无限堆积
	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "Y_USDT", "f1", "buy", 100, 1, 0, t0)
	tr.Observe("Y_USDT", 100, t0.Add(5*time.Minute)) // 远超最长 horizon + 2min
	if n := tr.PendingCount(); n != 0 {
		t.Fatalf("过期未采齐的成交应被丢弃,还剩 %d 笔", n)
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
