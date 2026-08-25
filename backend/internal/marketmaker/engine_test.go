package marketmaker

import (
	"math"
	"testing"
)

func TestRoundToTick(t *testing.T) {
	// bid floors to tick, ask ceils to tick — never crossing the target inward.
	if got := roundToTick(100.037, 0.01, false); math.Abs(got-100.03) > 1e-9 {
		t.Fatalf("bid floor: got %v want 100.03", got)
	}
	if got := roundToTick(100.031, 0.01, true); math.Abs(got-100.04) > 1e-9 {
		t.Fatalf("ask ceil: got %v want 100.04", got)
	}
	if got := roundToTick(50, 0, false); got != 50 { // zero tick = passthrough
		t.Fatalf("zero tick passthrough: got %v", got)
	}
}

func TestRoundToStep(t *testing.T) {
	if got := roundToStep(1.2345, 0.001); math.Abs(got-1.234) > 1e-9 {
		t.Fatalf("step floor: got %v want 1.234", got)
	}
	if got := roundToStep(5, 0); got != 5 {
		t.Fatalf("zero step passthrough: got %v", got)
	}
}

func TestBookTickerMath(t *testing.T) {
	b := BookTicker{BidPx: 100, AskPx: 102}
	if b.Mid() != 101 {
		t.Fatalf("mid: got %v want 101", b.Mid())
	}
	if math.Abs(b.SpreadBps()-198.0198) > 0.01 {
		t.Fatalf("spread bps: got %v want ~198.02", b.SpreadBps())
	}
	if (BookTicker{BidPx: 0, AskPx: 102}).Mid() != 0 {
		t.Fatalf("missing side must give mid 0")
	}
}

func TestSkewedQuote(t *testing.T) {
	const refMid, half = 100.0, 0.001 // 10bps half-spread; tick=0 for exact math

	// invRatio=0 → symmetric around refMid (99.9 / 100.1).
	bid0, ask0 := skewedQuote(refMid, half, 0, 1.0, 0)
	if math.Abs((bid0+ask0)/2-refMid) > 1e-9 {
		t.Fatalf("inv=0 must be symmetric: bid=%v ask=%v", bid0, ask0)
	}
	if math.Abs(bid0-99.9) > 1e-9 || math.Abs(ask0-100.1) > 1e-9 {
		t.Fatalf("inv=0 quotes: got bid=%v ask=%v want 99.9/100.1", bid0, ask0)
	}

	// invRatio=1, frac=1 → center=refMid*(1-half): ask pulled in to ~mid (sell hard),
	// and BOTH quotes must sit below the inv=0 quotes (the whole book leans to sell).
	bid1, ask1 := skewedQuote(refMid, half, 1, 1.0, 0)
	if math.Abs(ask1-refMid) > 0.001 {
		t.Fatalf("inv=1 ask should sit ~mid (%.1f): got %v", refMid, ask1)
	}
	if ask1 >= ask0 || bid1 >= bid0 {
		t.Fatalf("full inventory must lower BOTH quotes: ask %v->%v bid %v->%v", ask0, ask1, bid0, bid1)
	}

	// Monotone: more inventory ⇒ lower ask (sell sooner) and lower bid (buy less).
	prevAsk, prevBid := math.Inf(1), math.Inf(1)
	for _, r := range []float64{0, 0.25, 0.5, 0.75, 1} {
		b, a := skewedQuote(refMid, half, r, 1.0, 0)
		if a > prevAsk || b > prevBid {
			t.Fatalf("skew not monotone at invRatio=%v: ask=%v(prev %v) bid=%v(prev %v)", r, a, prevAsk, b, prevBid)
		}
		prevAsk, prevBid = a, b
	}

	// invRatio>1 clamps to 1 (never over-skews past full).
	bHi, aHi := skewedQuote(refMid, half, 5, 1.0, 0)
	if math.Abs(aHi-ask1) > 1e-9 || math.Abs(bHi-bid1) > 1e-9 {
		t.Fatalf("invRatio>1 must clamp to 1: got bid=%v ask=%v", bHi, aHi)
	}
}

func TestRideToBook(t *testing.T) {
	// PORTAL 真实场景:gate 比 binance 贵。我们按 spread 算出的卖价 100.15 远在 gate 卖一
	// (100.33)之下 → 过去就是这样贱卖。ride 后卖价应顶到 gate 卖一−tick=100.32,吃满溢价;
	// 买价 99.85 已在 gate 买一(100.11)之下 → 不动。
	bid, ask := rideToBook(99.85, 100.15, 100.11, 100.33, 0.01)
	if math.Abs(ask-100.32) > 1e-9 {
		t.Fatalf("ask should ride up to venue ask-tick 100.32, got %v", ask)
	}
	if bid != 99.85 {
		t.Fatalf("bid should stay at fair floor 99.85, got %v", bid)
	}

	// 紧盘口(SOL 类):gate 卖一 100.02 比我们的 100.10 更低 → 不下调(不砍自己的价);
	// gate 买一 99.98 比我们的 99.90 更高 → 不上调(不追高)。两边原样 = 守住 fair 价差。
	bid, ask = rideToBook(99.90, 100.10, 99.98, 100.02, 0.01)
	if bid != 99.90 || ask != 100.10 {
		t.Fatalf("tight book must not tighten our quotes, got %v/%v", bid, ask)
	}

	// 折价所:gate 买一 100.30 高于我们的买价上限 → 不追(买价不得越过 fair 上限)。
	bid, ask = rideToBook(99.90, 100.50, 100.30, 100.40, 0.01)
	if bid != 99.90 {
		t.Fatalf("bid must not chase a premium venue bid, got %v", bid)
	}

	// post-only 安全:算出的卖价会穿过 gate 买一 → 抬到买一+tick,不成 taker。
	bid, ask = rideToBook(99.0, 99.5, 100.0, 100.2, 0.01)
	if ask <= 100.0 {
		t.Fatalf("ask must not cross into venue bid, got %v", ask)
	}

	// 盘口缺边(0)跳过 ride 与安全钳制。
	bid, ask = rideToBook(99.0, 101.0, 0, 0, 0.01)
	if bid != 99.0 || ask != 101.0 {
		t.Fatalf("zero book sides must pass through, got %v/%v", bid, ask)
	}
}

func TestExecRegistry(t *testing.T) {
	for _, name := range []string{"coinsph", "mexc", "gate", "kucoin"} {
		ex, err := NewExec(ExecConfig{Name: name})
		if err != nil || ex == nil || ex.Name() != name {
			t.Fatalf("exec %q not registered correctly: ex=%v err=%v", name, ex, err)
		}
	}
	if _, err := NewExec(ExecConfig{Name: "nope"}); err == nil {
		t.Fatalf("unknown exec must error")
	}
}
