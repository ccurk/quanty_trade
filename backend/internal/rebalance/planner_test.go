package rebalance

import (
	"errors"
	"testing"
)

// readOK builds a BalanceSet in which every named exchange was read SUCCESSFULLY.
// An exchange may legitimately contribute zero rows — that is "it really holds
// nothing", which must stay distinguishable from "we could not read it".
func readOK(rows []Balance, exchanges ...string) *BalanceSet {
	var bs BalanceSet
	for _, ex := range exchanges {
		var own []Balance
		for _, b := range rows {
			if eqFold(b.Exchange, ex) {
				own = append(own, b)
			}
		}
		bs.Add(ex, own, nil)
	}
	return &bs
}

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
	plans, blocked := newTestPlanner(wl).Plan(readOK([]Balance{{Exchange: "gate", Asset: "USDT", Free: 200}}, "gate", "binance"))
	if blocked != "" {
		t.Fatalf("balances were read fine, must not block: %s", blocked)
	}
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
	plans, _ := newTestPlanner(wl).Plan(readOK([]Balance{{Exchange: "gate", Asset: "USDT", Free: 1800, Locked: 100}}, "gate", "binance"))
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
	plans, _ := newTestPlanner(wl).Plan(readOK([]Balance{{Exchange: "gate", Asset: "USDT", Free: 1000}}, "gate", "binance"))
	if len(plans) != 0 {
		t.Fatalf("in-band should produce no plan, got %d", len(plans))
	}
}

func TestPlanBlockedWhenNotWhitelisted(t *testing.T) {
	wl := NewWhitelist(nil) // nothing whitelisted
	plans, _ := newTestPlanner(wl).Plan(readOK([]Balance{{Exchange: "gate", Asset: "USDT", Free: 100}}, "gate", "binance"))
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
	plans, _ := p.Plan(readOK([]Balance{{Exchange: "gate", Asset: "USDT", Free: 480}}, "gate", "binance")) // gap = 20 < MinTransfer 50
	if len(plans) != 0 {
		t.Fatalf("sub-dust gap should be skipped, got %d plans", len(plans))
	}
}

// ---- "读不到" vs "真的是 0" ------------------------------------------------
// 这两件事以前在 planner 面前长得一模一样(都是没有那条 Balance),那正是
// "gate 读余额失败 → 当成 0 → 从 binance 补 → 真提现" 的病根。下面两个用例
// 输入的余额行完全相同(都没有 gate 的 USDT),只有"知不知道"不同,结论必须相反。

func TestPlanRefusesWhenExecBalanceUnknown(t *testing.T) {
	wl := NewWhitelist([]AllowedAddress{{Exchange: "gate", Asset: "USDT", Network: "TRC20", Address: "TGateDeposit"}})
	var bs BalanceSet
	bs.Add("gate", nil, errors.New("gate accounts HTTP 500: boom")) // 读失败
	bs.Add("binance", []Balance{{Exchange: "binance", Asset: "USDT", Free: 50000}}, nil)

	plans, blocked := newTestPlanner(wl).Plan(&bs)
	if blocked == "" {
		t.Fatalf("exec 所余额未知时必须 blocked,却放行了")
	}
	if len(plans) != 0 {
		t.Fatalf("exec 所余额未知时不许产出任何计划,却产出了 %d 个: %+v", len(plans), plans)
	}
}

func TestPlanZeroBalanceIsNotUnknown(t *testing.T) {
	// 同样没有 gate 的 USDT 行,但这次是"读成功了,账户里就是没有"。
	// 这是正常的补仓场景,必须照常产出可执行计划 —— 修复不能把功能一起挡死。
	wl := NewWhitelist([]AllowedAddress{{Exchange: "gate", Asset: "USDT", Network: "TRC20", Address: "TGateDeposit"}})
	plans, blocked := newTestPlanner(wl).Plan(readOK(nil, "gate", "binance"))
	if blocked != "" {
		t.Fatalf("读成功但余额为 0 是正常场景,不该 blocked: %s", blocked)
	}
	if len(plans) != 1 || plans[0].Amount != 1000 || !plans[0].Executable() {
		t.Fatalf("真为 0 时应产出补至 1000 的可执行计划,得到 %+v", plans)
	}
}

func TestPlanIgnoresReservoirReadFailure(t *testing.T) {
	// 水库(binance)读不到不影响任何带内比较 —— 带比的是 exec 所的库存。
	// 所以这里不该 blocked,否则就是为了安全把功能挡死。
	wl := NewWhitelist([]AllowedAddress{{Exchange: "gate", Asset: "USDT", Network: "TRC20", Address: "TGateDeposit"}})
	var bs BalanceSet
	bs.Add("gate", []Balance{{Exchange: "gate", Asset: "USDT", Free: 200}}, nil)
	bs.Add("binance", nil, errors.New("binance account HTTP 418"))

	plans, blocked := newTestPlanner(wl).Plan(&bs)
	if blocked != "" || len(plans) != 1 || plans[0].Amount != 800 {
		t.Fatalf("水库读失败不该拦住 exec 侧的计划,得到 blocked=%q plans=%+v", blocked, plans)
	}
	if bs.Err() == "" {
		t.Errorf("水库的错误必须留在 BalanceSet 里,不能被吞掉")
	}
}
