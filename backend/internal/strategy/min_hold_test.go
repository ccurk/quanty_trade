package strategy

import "testing"

// 最短持仓保护绝不能拦住风控出场 —— 拦住止损 = 在亏损扩大时把风控关掉。
// 出场 reason 取自代码中实际出现的全部取值(grep 'reason = "' 得到)。
func TestIsRiskExit(t *testing.T) {
	mustExempt := []string{
		"sl", "tp", "roi_sl", "roi_tp",
		"guard_sl", "guard_tp", "guard_roi_sl", "guard_roi_tp",
		"hunger_sl", "hunger_tp", "minute_roi_sl_scan",
		"", // 来源不明 → 保守放行
		"LIQUIDATION", "risk_control",
	}
	for _, r := range mustExempt {
		if !isRiskExit(r) {
			t.Errorf("reason=%q 必须豁免最短持仓(风控出场被拦=风控失效)", r)
		}
	}

	// 只有主动/信号类平仓才受最短持仓约束。
	mustHold := []string{"rotation", "signal_reverse", "manual_close"}
	for _, r := range mustHold {
		if isRiskExit(r) {
			t.Errorf("reason=%q 不该被当成风控出场,否则最短持仓保护形同虚设", r)
		}
	}
}

// 配置缺省必须等于"不启用",保证向后兼容(不给老策略凭空加行为)。
func TestMinHoldDurationDefaults(t *testing.T) {
	if got := minHoldDuration(nil); got != 0 {
		t.Fatalf("nil 实例应返回 0,得到 %v", got)
	}
	inst := &StrategyInstance{Config: map[string]interface{}{}}
	if got := minHoldDuration(inst); got != 0 {
		t.Fatalf("未配置应返回 0(不启用),得到 %v", got)
	}
	inst.Config["min_hold_seconds"] = 0
	if got := minHoldDuration(inst); got != 0 {
		t.Fatalf("配置 0 应表示不启用,得到 %v", got)
	}
	inst.Config["min_hold_seconds"] = 900
	if got := minHoldDuration(inst); got.Seconds() != 900 {
		t.Fatalf("配置 900 应为 900s,得到 %v", got)
	}
}
