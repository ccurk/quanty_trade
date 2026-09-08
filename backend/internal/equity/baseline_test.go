package equity

import (
	"testing"
	"time"
)

// TestBaselineIsInternallyConsistent 把口径的几个数钉在一起。
//
// 为什么值得一条测试:这些常量存在的唯一理由,就是防止同一个问题被不同的人
// 用不同的数回答(#18/#22)。如果它们【彼此】就对不上,那这个文件比没有更糟 ——
// 引用它的人会以为自己引的是一份自洽的口径。
func TestBaselineIsInternallyConsistent(t *testing.T) {
	// 硬地板必须真的在区间下界之下,且点估计落在区间内。
	if !(BaselineHardFloorUSD < BaselineTotalLowUSD) {
		t.Errorf("硬地板 %.2f 应当低于区间下界 %.2f", BaselineHardFloorUSD, BaselineTotalLowUSD)
	}
	if !(BaselineTotalLowUSD <= BaselineTotalUSD && BaselineTotalUSD <= BaselineTotalHighUSD) {
		t.Errorf("点估计 %.2f 不在区间 [%.2f, %.2f] 内",
			BaselineTotalUSD, BaselineTotalLowUSD, BaselineTotalHighUSD)
	}

	// 硬地板 = Polymarket + Gate 下界,两项都有可复核的观测。
	// 允许 0.5 的余量:Gate 那档报告里写的是 $17.4,常量取整到 17。
	if floor := BaselinePolymarketUSD + BaselineGateSpotLowUSD; floor-BaselineHardFloorUSD > 0.5 ||
		BaselineHardFloorUSD-floor > 0.5 {
		t.Errorf("硬地板 %.2f 与 Polymarket+Gate下界 %.2f 对不上", BaselineHardFloorUSD, floor)
	}

	// 分档加总必须落进总区间 —— 这是"两个场子该【相加】不该三角定位"这条
	// 结论在代码里的形式(equity-blindspot-2026-09-09.md §4)。
	lo := BaselinePolymarketUSD + BaselineGateSpotLowUSD + BaselineBinanceUSDMLowUSD
	hi := BaselinePolymarketUSD + BaselineGateSpotHighUSD + BaselineBinanceUSDMHighUSD
	if lo < BaselineTotalLowUSD-1 || lo > BaselineTotalLowUSD+10 {
		t.Errorf("分档下界之和 %.2f 与总区间下界 %.2f 不相符", lo, BaselineTotalLowUSD)
	}
	if hi > BaselineTotalHighUSD+1 {
		t.Errorf("分档上界之和 %.2f 超出总区间上界 %.2f", hi, BaselineTotalHighUSD)
	}

	// 误差不对称:点估计不该正好落在区间中点。真值更可能在上方
	// (Binance 那档是下界,且 Binance 现货完全没测)。
	if mid := (BaselineTotalLowUSD + BaselineTotalHighUSD) / 2; BaselineTotalUSD == mid {
		t.Errorf("点估计正好是区间中点 %.2f —— 误差应当是不对称的", mid)
	}

	if len(BaselineInvalidatedBy) == 0 {
		t.Error("口径必须写明什么时候失效,否则下次又会有人当成长期事实引用")
	}
}

// TestBaselineStale:这套数有保鲜期。Binance 那一档是从 daily_pn_ls 反解的,
// 而那个窗口每天滚动,所以证据本身在被持续替换。
func TestBaselineStale(t *testing.T) {
	if BaselineStale(BaselineAsOf.Add(BaselineShelfLife - time.Hour)) {
		t.Error("保鲜期内不应判为过期")
	}
	if !BaselineStale(BaselineAsOf.Add(BaselineShelfLife + time.Hour)) {
		t.Error("超过保鲜期必须判为过期")
	}
}
