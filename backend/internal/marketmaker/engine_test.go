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

func TestExecRegistry(t *testing.T) {
	for _, name := range []string{"coinsph", "mexc"} {
		ex, err := NewExec(ExecConfig{Name: name})
		if err != nil || ex == nil || ex.Name() != name {
			t.Fatalf("exec %q not registered correctly: ex=%v err=%v", name, ex, err)
		}
	}
	if _, err := NewExec(ExecConfig{Name: "nope"}); err == nil {
		t.Fatalf("unknown exec must error")
	}
}
