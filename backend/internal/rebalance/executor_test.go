package rebalance

import (
	"testing"
	"time"
)

func semiCfg() *Config {
	return &Config{
		Enable: true, ExecExchange: "gate", ReservoirExchange: "binance",
		DryRun: false, Mode: ModeSemi, CooldownMinutes: 30,
		MaxPerTransferUSD: 2000, MaxPerDayUSD: 5000,
		Assets: []AssetConfig{{Asset: "USDT", Network: "TRC20", Opt: 1000, Min: 500, Max: 1500, MinTransfer: 50}},
	}
}

func execPlan(amount float64) Plan {
	return Plan{Asset: "USDT", FromExchange: "binance", ToExchange: "gate", Amount: amount, Network: "TRC20", ToAddress: "Tgate"}
}

var priceUSDT = func(a string) float64 {
	if a == "USDT" {
		return 1
	}
	return 0
}

// spyWithdraw records whether/what the withdrawal fn was called with.
func spyWithdraw() (WithdrawFn, *int, *float64) {
	calls := 0
	var amt float64
	fn := func(from, asset, net, addr, memo string, a float64) (string, error) {
		calls++
		amt = a
		return "tx-123", nil
	}
	return fn, &calls, &amt
}

func TestExecuteLiveSubmits(t *testing.T) {
	w, calls, amt := spyWithdraw()
	r := NewExecutor(semiCfg(), w, priceUSDT).Execute(execPlan(800), 0, time.Time{}, time.Now())
	if !r.Executed || r.TxID != "tx-123" || *calls != 1 || *amt != 800 {
		t.Fatalf("expected live submit of 800, got %+v (calls=%d amt=%v)", r, *calls, *amt)
	}
}

func TestExecuteDryRunDoesNotCall(t *testing.T) {
	c := semiCfg()
	c.DryRun = true
	w, calls, _ := spyWithdraw()
	r := NewExecutor(c, w, priceUSDT).Execute(execPlan(800), 0, time.Time{}, time.Now())
	if !r.DryRun || r.Executed || *calls != 0 {
		t.Fatalf("dry-run must not call withdraw, got %+v (calls=%d)", r, *calls)
	}
}

func TestExecuteRecommendNeverExecutes(t *testing.T) {
	c := semiCfg()
	c.Mode = ModeRecommend
	w, calls, _ := spyWithdraw()
	r := NewExecutor(c, w, priceUSDT).Execute(execPlan(800), 0, time.Time{}, time.Now())
	if r.Executed || *calls != 0 || r.Skipped == "" {
		t.Fatalf("recommend must skip, got %+v", r)
	}
}

func TestExecuteBlockedWhenNotWhitelisted(t *testing.T) {
	w, calls, _ := spyWithdraw()
	p := execPlan(800)
	p.ToAddress = "" // not whitelisted
	r := NewExecutor(semiCfg(), w, priceUSDT).Execute(p, 0, time.Time{}, time.Now())
	if r.Executed || *calls != 0 || r.Skipped == "" {
		t.Fatalf("non-whitelisted must be blocked, got %+v", r)
	}
}

func TestExecuteCooldownBlocks(t *testing.T) {
	w, calls, _ := spyWithdraw()
	now := time.Now()
	r := NewExecutor(semiCfg(), w, priceUSDT).Execute(execPlan(800), 0, now.Add(-10*time.Minute), now)
	if r.Executed || *calls != 0 || r.Skipped == "" {
		t.Fatalf("within cooldown must skip, got %+v", r)
	}
}

func TestExecuteUnknownPriceRefused(t *testing.T) {
	w, calls, _ := spyWithdraw()
	p := execPlan(800)
	p.Asset = "BTC" // priceUSDT returns 0
	r := NewExecutor(semiCfg(), w, priceUSDT).Execute(p, 0, time.Time{}, time.Now())
	if r.Executed || *calls != 0 || r.Skipped == "" {
		t.Fatalf("unknown price must refuse, got %+v", r)
	}
}

func TestExecuteCapsSingleTransfer(t *testing.T) {
	w, _, amt := spyWithdraw()
	r := NewExecutor(semiCfg(), w, priceUSDT).Execute(execPlan(3000), 0, time.Time{}, time.Now()) // >2000 cap
	if !r.Executed || *amt != 2000 || r.Amount != 2000 {
		t.Fatalf("expected cap to 2000, got amt=%v result=%+v", *amt, r)
	}
}

func TestExecuteCapsToDailyRemaining(t *testing.T) {
	w, _, amt := spyWithdraw()
	// spent 4900 today, daily cap 5000 → only 100 left
	r := NewExecutor(semiCfg(), w, priceUSDT).Execute(execPlan(800), 4900, time.Time{}, time.Now())
	if !r.Executed || *amt != 100 {
		t.Fatalf("expected cap to daily remaining 100, got amt=%v result=%+v", *amt, r)
	}
}

func TestExecuteDailyExhausted(t *testing.T) {
	w, calls, _ := spyWithdraw()
	r := NewExecutor(semiCfg(), w, priceUSDT).Execute(execPlan(800), 5000, time.Time{}, time.Now())
	if r.Executed || *calls != 0 || r.Skipped == "" {
		t.Fatalf("daily exhausted must skip, got %+v", r)
	}
}

func TestExecuteDustAfterCapSkips(t *testing.T) {
	w, calls, _ := spyWithdraw()
	// daily remaining 30 (< minTransfer 50) → capped to 30 then skipped as dust
	r := NewExecutor(semiCfg(), w, priceUSDT).Execute(execPlan(800), 4970, time.Time{}, time.Now())
	if r.Executed || *calls != 0 || r.Skipped == "" {
		t.Fatalf("sub-dust after cap must skip, got %+v", r)
	}
}
