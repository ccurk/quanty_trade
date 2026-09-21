package strategy

import "testing"

// 「仓位损失」判据本身：pnl <= 阈值即失败，端点含在内。
func TestIsPositionFailureBoundaryIsInclusive(t *testing.T) {
	// 恰好等于阈值算失败 —— 端点方向不能漂。若哪天有人写成 `<`，
	// 一笔恰好 +0.50U 的单会从「失败」翻成「赢」，本测试必红。
	for _, tc := range []struct {
		pnl  float64
		want bool
	}{
		{2.5, false},
		{0.5001, false},
		{0.5, true}, // 端点
		{0.25, true},
		{0.0, true},
		{-1.25, true},
	} {
		if got := IsPositionFailure(tc.pnl, 0.5); got != tc.want {
			t.Errorf("IsPositionFailure(%v, 0.5) = %v, want %v", tc.pnl, got, tc.want)
		}
	}
}

// 阈值解析：「缺键」与「显式填 0」必须区分开。
// 缺键 → 包默认；填 0 是合法配置（失败 = 不赚钱的），不能被当成「没配」而回落成 0.5。
func TestFailureThresholdUSDTDistinguishesAbsentFromZero(t *testing.T) {
	if got := FailureThresholdUSDT(nil); got != FailurePnLThresholdUSDT {
		t.Errorf("nil cfg = %v, want the package default %v", got, FailurePnLThresholdUSDT)
	}
	if got := FailureThresholdUSDT(map[string]interface{}{}); got != FailurePnLThresholdUSDT {
		t.Errorf("empty cfg = %v, want the package default %v", got, FailurePnLThresholdUSDT)
	}
	if got := FailureThresholdUSDT(map[string]interface{}{"failure_pnl_threshold_usdt": 0}); got != 0 {
		t.Errorf("explicit 0 = %v, want 0 — 显式配置不能被当成缺键", got)
	}
	if got := FailureThresholdUSDT(map[string]interface{}{"failure_pnl_threshold_usdt": 1.2}); got != 1.2 {
		t.Errorf("explicit 1.2 = %v, want 1.2", got)
	}
	// 显式 null 等同缺键（config JSON 里键存在但值为 null 是常见形态）
	if got := FailureThresholdUSDT(map[string]interface{}{"failure_pnl_threshold_usdt": nil}); got != FailurePnLThresholdUSDT {
		t.Errorf("explicit null = %v, want the package default %v", got, FailurePnLThresholdUSDT)
	}
}
