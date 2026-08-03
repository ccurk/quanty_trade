package strategy

import (
	"testing"
	"time"
)

// TestMergeBacktestConfig: overrides apply to experiment knobs but can never
// touch identity/transport keys (the clone-on-live-bus incident vector), and
// the merged config drives the exit-layer resolution.
func TestMergeBacktestConfig(t *testing.T) {
	base := map[string]interface{}{
		"hunger_take_profit_pct": 0.06,
		"max_hold_minutes":       float64(60),
		"redis_addr":             "prod:6379",
	}
	merged := mergeBacktestConfig(base, map[string]interface{}{
		"hunger_take_profit_pct": 0.10,
		"max_hold_minutes":       float64(90),
		"redis_addr":             "evil:6379",
		"strategy_id":            "spoofed",
		"backtest":               false,
	})
	if merged["hunger_take_profit_pct"] != 0.10 {
		t.Fatalf("hunger_tp override lost: %v", merged["hunger_take_profit_pct"])
	}
	if merged["redis_addr"] != "prod:6379" {
		t.Fatalf("redis_addr must be un-overridable, got %v", merged["redis_addr"])
	}
	if _, ok := merged["strategy_id"]; ok {
		t.Fatal("strategy_id must not be injectable via overrides")
	}

	simInst := &StrategyInstance{Config: merged}
	_, _, tp, _ := resolveHungerMode(simInst)
	if tp != 0.10 {
		t.Fatalf("exit sim must read merged config, hunger_tp=%v", tp)
	}
	if mh := resolveMaxHoldTimeout(simInst); mh != 90*time.Minute {
		t.Fatalf("max_hold=%v want 90m", mh)
	}
}
