package rebalance

import "testing"

func newTestPlanner(wl *Whitelist) *Planner {
	return &Planner{
		Exec:           "gate",
		Reservoir:      "binance",
		DefaultNetwork: "TRC20",
		Networks:       map[string]string{"USDT": "TRC20"},
		Bands: []AssetBand{
			{Asset: "USDT", Target: 1000, Min: 500, Max: 1500},
		},
		Whitelist:   wl,
		MinTransfer: map[string]float64{"USDT": 50},
	}
}

func TestPlanRefillWhenBelowMin(t *testing.T) {
	wl := NewWhitelist([]AllowedAddress{{Exchange: "gate", Asset: "USDT", Network: "TRC20", Address: "TGateDeposit"}})
	plans := newTestPlanner(wl).Plan([]Balance{{Exchange: "gate", Asset: "USDT", Free: 200}})
	if len(plans) != 1 {
		t.Fatalf("want 1 plan, got %d", len(plans))
	}
	p := plans[0]
	if p.FromExchange != "binance" || p.ToExchange != "gate" {
		t.Errorf("want binance->gate, got %s->%s", p.FromExchange, p.ToExchange)
	}
	if p.Amount != 800 { // target 1000 - current 200
		t.Errorf("want amount 800, got %v", p.Amount)
	}
	if !p.Executable() || p.ToAddress != "TGateDeposit" {
		t.Errorf("want executable with whitelisted addr, got %+v", p)
	}
}

func TestPlanDrainWhenAboveMax(t *testing.T) {
	wl := NewWhitelist([]AllowedAddress{{Exchange: "binance", Asset: "USDT", Network: "TRC20", Address: "TBinanceDeposit"}})
	plans := newTestPlanner(wl).Plan([]Balance{{Exchange: "gate", Asset: "USDT", Free: 1800, Locked: 100}})
	if len(plans) != 1 {
		t.Fatalf("want 1 plan, got %d", len(plans))
	}
	p := plans[0]
	if p.FromExchange != "gate" || p.ToExchange != "binance" {
		t.Errorf("want gate->binance, got %s->%s", p.FromExchange, p.ToExchange)
	}
	if p.Amount != 900 { // current 1900 (free+locked) - target 1000
		t.Errorf("want amount 900, got %v", p.Amount)
	}
	if p.ToAddress != "TBinanceDeposit" {
		t.Errorf("want binance whitelisted addr, got %q", p.ToAddress)
	}
}

func TestPlanNoopInsideBand(t *testing.T) {
	wl := NewWhitelist(nil)
	plans := newTestPlanner(wl).Plan([]Balance{{Exchange: "gate", Asset: "USDT", Free: 1000}})
	if len(plans) != 0 {
		t.Fatalf("in-band should produce no plan, got %d", len(plans))
	}
}

func TestPlanBlockedWhenNotWhitelisted(t *testing.T) {
	wl := NewWhitelist(nil) // nothing whitelisted
	plans := newTestPlanner(wl).Plan([]Balance{{Exchange: "gate", Asset: "USDT", Free: 100}})
	if len(plans) != 1 {
		t.Fatalf("want 1 (blocked) plan, got %d", len(plans))
	}
	if plans[0].Executable() {
		t.Errorf("plan must be non-executable without a whitelisted dest: %+v", plans[0])
	}
}

func TestPlanSkipsDust(t *testing.T) {
	wl := NewWhitelist([]AllowedAddress{{Exchange: "gate", Asset: "USDT", Network: "TRC20", Address: "TGateDeposit"}})
	// current 470 → gap to target is 530? No: Min=500, current 470 < Min, gap = 1000-470 = 530 (not dust).
	// Use a case just below Min with a tiny gap by raising current to 480 and Min 500 → gap 520 still big.
	// Dust is exercised via a band whose target-min gap is tiny:
	p := newTestPlanner(wl)
	p.Bands = []AssetBand{{Asset: "USDT", Target: 500, Min: 490, Max: 1500}}
	plans := p.Plan([]Balance{{Exchange: "gate", Asset: "USDT", Free: 480}}) // gap = 20 < MinTransfer 50
	if len(plans) != 0 {
		t.Fatalf("sub-dust gap should be skipped, got %d plans", len(plans))
	}
}
