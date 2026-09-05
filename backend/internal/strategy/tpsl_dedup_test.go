package strategy

import (
	"testing"

	"quanty_trade/internal/models"
)

// 2026-09-05 我踏马来了/USDT 实况:交易所一个仓位,库里三行(多 owner 共用同一账户的
// 重复记账),只有 id=2752 带 TP/SL。监控逐行处理时,另外两行推不出 TP/SL,每 15s 报一条
// error,六小时刷了 432 条。去重必须挑出"带 TP/SL 的那行",否则修了个寂寞。
func TestBetterTPSLRowPrefersConfigured(t *testing.T) {
	configured := models.StrategyPosition{ID: 2752, TakeProfit: 0.011658, StopLoss: 0.015082}
	blank1 := models.StrategyPosition{ID: 2753}
	blank2 := models.StrategyPosition{ID: 2755} // id 更大,但没配置

	// 带配置的必须胜出,与 id 大小无关(2755 > 2752 也不能盖过它)。
	for _, blank := range []models.StrategyPosition{blank1, blank2} {
		if got := betterTPSLRow(configured, blank); got.ID != 2752 {
			t.Errorf("已配置的行必须胜出,得到 id=%d", got.ID)
		}
		if got := betterTPSLRow(blank, configured); got.ID != 2752 {
			t.Errorf("顺序不该影响结果,得到 id=%d", got.ID)
		}
	}

	// 都没配置时取较新的一条(信息最接近当前状态)。
	if got := betterTPSLRow(blank1, blank2); got.ID != 2755 {
		t.Errorf("同为空配置时应取较新行,得到 id=%d", got.ID)
	}

	// 只配了一条腿不算"已配置" —— 半套保护仍会走补挂路径。
	half := models.StrategyPosition{ID: 3000, StopLoss: 0.015}
	if got := betterTPSLRow(configured, half); got.ID != 2752 {
		t.Errorf("只有 SL 的行不该盖过 TP/SL 齐全的行,得到 id=%d", got.ID)
	}
}

// 限流必须真的限住:同一 (owner, symbol) 第二次调用要被挡下,否则 15s 巡检照样刷爆。
func TestShouldLogTPSLGapRateLimits(t *testing.T) {
	m := &Manager{}
	const owner = uint(2)
	const sym = "我踏马来了/USDT"

	if !m.shouldLogTPSLGap(owner, sym) {
		t.Fatal("首次应放行")
	}
	if m.shouldLogTPSLGap(owner, sym) {
		t.Fatal("10 分钟内的重复告警必须被限流")
	}
	// 不同 symbol 互不影响,避免一个仓位把别的仓位的告警吞掉。
	if !m.shouldLogTPSLGap(owner, "OTHER/USDT") {
		t.Fatal("不同 symbol 应各自计时")
	}
}
