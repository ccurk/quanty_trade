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

func TestClampToBook(t *testing.T) {
	// 今晚 ONG 的真实场景:gate 比 binance 贵,skew 后的卖价(0.07377)低于 gate 买一
	// (假设 0.07380)→ post-only 必被拒。钳制后卖价应抬到 gate 买一 + tick,买价不动。
	bid, ask := clampToBook(0.07330, 0.07377, 0.07380, 0.07385, 0.00001)
	if math.Abs(ask-0.07381) > 1e-12 {
		t.Fatalf("ask should clamp to venue bid+tick 0.07381, got %v", ask)
	}
	if bid != 0.07330 {
		t.Fatalf("bid must be untouched, got %v", bid)
	}

	// 反向:执行所比参考便宜,买价(100.2)高于其卖一(100.1)→ 压到 100.1−tick。
	bid, ask = clampToBook(100.2, 100.5, 100.0, 100.1, 0.01)
	if math.Abs(bid-100.09) > 1e-9 {
		t.Fatalf("bid should clamp to venue ask-tick 100.09, got %v", bid)
	}
	if ask != 100.5 {
		t.Fatalf("ask must be untouched, got %v", ask)
	}

	// 不穿越时:两边都原样。
	bid, ask = clampToBook(99.0, 101.0, 99.5, 100.5, 0.01)
	if bid != 99.0 || ask != 101.0 {
		t.Fatalf("non-crossing quotes must pass through, got %v/%v", bid, ask)
	}

	// 盘口缺边(0)不钳制。
	bid, ask = clampToBook(99.0, 101.0, 0, 0, 0.01)
	if bid != 99.0 || ask != 101.0 {
		t.Fatalf("zero book sides must not clamp, got %v/%v", bid, ask)
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
