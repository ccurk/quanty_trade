package strategy

import (
	"errors"
	"testing"

	"quanty_trade/internal/exchange"
)

// stubBal 同时钉住两个接缝，因为它们现在是两个不同的量：
//   avail  → 只用于可行性上限（avail×lev×0.95，能不能下得出去）
//   equity → 乘基（Wallet + Unrealized，下多大）
//
// 2026-09-21 用户直令：乘基由 avail 换成 equity。
func stubBal(t *testing.T, avail, wallet, unreal float64) {
	t.Helper()
	oldA, oldE := usdmAvailableUSDT, usdmWalletEquity
	usdmAvailableUSDT = func(*exchange.BinanceExchange, uint) (float64, error) { return avail, nil }
	usdmWalletEquity = func(*exchange.BinanceExchange, uint) (exchange.USDMBalance, error) {
		return exchange.USDMBalance{Available: avail, Wallet: wallet, Unrealized: unreal}, nil
	}
	t.Cleanup(func() { usdmAvailableUSDT, usdmWalletEquity = oldA, oldE })
}

// pctInst 造一个走 percent_balance 的实例：初始保证金 = 权益 × pct。
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
	stubBal(t, 50, 50, 0) // 权益 50 × 0.375 = 18.75 < 20
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
	stubBal(t, 70, 70, 0) // 70 × 0.375 = 26.25
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
	stubBal(t, 10, 10, 0) // 10 × 0.375 = 3.75
	inst := pctInst(map[string]interface{}{"min_initial_margin_usdt": 1})
	amt, err := resolveUSDMOrderAmount(inst, &exchange.BinanceExchange{}, "TESTUSDT", 0, 1.0, 0)
	if err != nil {
		t.Fatalf("不应返回 error, got %v", err)
	}
	if amt <= 0 {
		t.Fatalf("下限=1 时不该被保证金闸拦下, got amt=%v", amt)
	}
}

// 控制组：同一套配置，只把权益抬到跨过下限，就不该被这道闸拦下。
// 没有这一组，上面「返回 0」的断言无法排除是别的原因造成的。
func TestMinInitialMarginPassesAboveFloor(t *testing.T) {
	stubBal(t, 200, 200, 0) // 200 × 0.375 = 75 ≥ 20
	inst := pctInst(nil)
	amt, err := resolveUSDMOrderAmount(inst, &exchange.BinanceExchange{}, "TESTUSDT", 0, 1.0, 0)
	if err != nil {
		t.Fatalf("不应返回 error, got %v", err)
	}
	if amt <= 0 {
		t.Fatalf("初始保证金 75 高于下限 20，不该被这道闸拒, got amt=%v", amt)
	}
}

// ★ 判别测试：乘基是【权益】，不是【可用余额】。
// 故意让 avail(200) > equity(100)：若有人把 base 改回 avail，本测试必红。
// 顺带钉住 equity = Wallet + Unrealized（这里 90 + 10）。
func TestSizingBaseIsEquityNotAvail(t *testing.T) {
	stubBal(t, 200, 90, 10) // avail=200，equity=90+10=100
	inst := pctInst(map[string]interface{}{
		"order_amount_pct":        0.25,
		"min_initial_margin_usdt": 1,
	})
	amt, err := resolveUSDMOrderAmount(inst, &exchange.BinanceExchange{}, "TESTUSDT", 0, 1.0, 0)
	if err != nil {
		t.Fatalf("不应返回 error, got %v", err)
	}
	// 权益 100 × 0.25 = 25 保证金 × lev 10 = 250 名义；px=1.0 ⇒ amt=250。
	// 若按 avail(200) 算会是 500。
	if amt != 250 {
		t.Fatalf("乘基应为权益(100) ⇒ amt=250；得到 %v 说明乘基不是权益（500=用了 avail）", amt)
	}
}

// 判别测试的反向控制组：equity(200) > avail(100)，尺寸应变大到 500。
// 可行性上限 avail×lev×0.95 = 950 不拦。
func TestSizingBaseIsEquityNotAvailControl(t *testing.T) {
	stubBal(t, 100, 210, -10) // avail=100，equity=210-10=200
	inst := pctInst(map[string]interface{}{
		"order_amount_pct":        0.25,
		"min_initial_margin_usdt": 1,
	})
	amt, err := resolveUSDMOrderAmount(inst, &exchange.BinanceExchange{}, "TESTUSDT", 0, 1.0, 0)
	if err != nil {
		t.Fatalf("不应返回 error, got %v", err)
	}
	if amt != 500 {
		t.Fatalf("乘基应为权益(200) ⇒ amt=500；得到 %v 说明乘基不是权益", amt)
	}
}

// 权益取数为 0 有两种原因：接口失败，或权益真的是 0。两者都必须拒单，
// 且不能 panic —— 日志侧由代码区分（见 resolveUSDMOrderAmount 里的 equityErr 分支）。
func TestSizingEquityFetchErrorSkips(t *testing.T) {
	stubBal(t, 200, 200, 0)
	usdmWalletEquity = func(*exchange.BinanceExchange, uint) (exchange.USDMBalance, error) {
		return exchange.USDMBalance{}, errors.New("boom")
	}
	inst := pctInst(map[string]interface{}{"min_initial_margin_usdt": 1})
	amt, err := resolveUSDMOrderAmount(inst, &exchange.BinanceExchange{}, "TESTUSDT", 0, 1.0, 0)
	if err != nil {
		t.Fatalf("不应返回 error, got %v", err)
	}
	if amt != 0 {
		t.Fatalf("权益取数失败应跳过开仓(0), got amt=%v", amt)
	}
}
