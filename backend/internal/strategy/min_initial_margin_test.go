package strategy

import (
	"testing"

	"quanty_trade/internal/exchange"
)

// stubAvail 替换可用余额接缝，返回确定的可用余额。
func stubAvail(t *testing.T, v float64) {
	t.Helper()
	old := usdmAvailableUSDT
	usdmAvailableUSDT = func(*exchange.BinanceExchange, uint) (float64, error) { return v, nil }
	t.Cleanup(func() { usdmAvailableUSDT = old })
}

// pctInst 造一个走 percent_balance 的实例：初始保证金 = 可用余额 × pct。
func pctInst(extra map[string]interface{}) *StrategyInstance {
	cfg := map[string]interface{}{
		"order_amount_mode": "percent_balance",
		"order_amount_pct":  0.375,
		"leverage":          10,
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return confInst(cfg)
}

// 初始保证金低于下限 → 拒单（不下单、不占槽位）。
// 2026-09-21 用户直令：min_initial_margin_usdt 默认 20，且是【保证金】口径。
func TestMinInitialMarginRejectsBelowFloor(t *testing.T) {
	stubAvail(t, 50) // 50 × 0.375 = 18.75 < 20
	inst := pctInst(nil)
	amt, err := resolveUSDMOrderAmount(inst, &exchange.BinanceExchange{}, "TESTUSDT", 0, 1.0, 0)
	if err != nil {
		t.Fatalf("不应返回 error, got %v", err)
	}
	if amt != 0 {
		t.Fatalf("初始保证金 18.75 低于下限 20，应拒单(0), got amt=%v", amt)
	}
}

// 下限可配：抬到 30，则 26.25 也应被拒。
func TestMinInitialMarginConfigurableFloor(t *testing.T) {
	stubAvail(t, 70) // 70 × 0.375 = 26.25
	inst := pctInst(map[string]interface{}{"min_initial_margin_usdt": 30})
	amt, err := resolveUSDMOrderAmount(inst, &exchange.BinanceExchange{}, "TESTUSDT", 0, 1.0, 0)
	if err != nil {
		t.Fatalf("不应返回 error, got %v", err)
	}
	if amt != 0 {
		t.Fatalf("下限=30 时初始保证金 26.25 应被拒, got amt=%v", amt)
	}
}

// 下界可配：压到 1，则 3.75 不该被这道闸拦（仍需下游放行）。
func TestMinInitialMarginLowFloorPasses(t *testing.T) {
	stubAvail(t, 10) // 10 × 0.375 = 3.75
	inst := pctInst(map[string]interface{}{"min_initial_margin_usdt": 1})
	amt, err := resolveUSDMOrderAmount(inst, &exchange.BinanceExchange{}, "TESTUSDT", 0, 1.0, 0)
	if err != nil {
		t.Fatalf("不应返回 error, got %v", err)
	}
	if amt <= 0 {
		t.Fatalf("下限=1 时不该被保证金闸拦下, got amt=%v", amt)
	}
}

// 控制组：同一套配置，只把可用余额抬到跨过下限，就不该被这道闸拦下。
// 没有这一组，上面「返回 0」的断言无法排除是别的原因造成的。
func TestMinInitialMarginPassesAboveFloor(t *testing.T) {
	stubAvail(t, 200) // 200 × 0.375 = 75 ≥ 20
	inst := pctInst(nil)
	amt, err := resolveUSDMOrderAmount(inst, &exchange.BinanceExchange{}, "TESTUSDT", 0, 1.0, 0)
	if err != nil {
		t.Fatalf("不应返回 error, got %v", err)
	}
	if amt <= 0 {
		t.Fatalf("初始保证金 75 高于下限 20，不该被这道闸拒, got amt=%v", amt)
	}
}
