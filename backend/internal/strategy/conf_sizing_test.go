package strategy

import (
	"math"
	"testing"
)

func confInst(cfg map[string]interface{}) *StrategyInstance {
	inst := &StrategyInstance{}
	inst.setConfig(cfg)
	return inst
}

func TestConfSizingDisabledByDefault(t *testing.T) {
	mult, ok := confSizingMultiplier(confInst(map[string]interface{}{}), 0.55)
	if ok || mult != 1 {
		t.Fatalf("默认应关闭且乘数=1, got mult=%v ok=%v", mult, ok)
	}
}

func TestConfSizingZeroConfidencePassthrough(t *testing.T) {
	cfg := map[string]interface{}{"conf_sizing_enabled": true}
	mult, ok := confSizingMultiplier(confInst(cfg), 0)
	if ok || mult != 1 {
		t.Fatalf("信号未带置信度时应透传, got mult=%v ok=%v", mult, ok)
	}
}

func TestConfSizingLinearInterpolation(t *testing.T) {
	cfg := map[string]interface{}{
		"conf_sizing_enabled":  true,
		"conf_sizing_conf_lo":  0.40,
		"conf_sizing_conf_hi":  0.60,
		"conf_sizing_min_mult": 0.60,
		"conf_sizing_max_mult": 1.40,
	}
	cases := []struct {
		conf float64
		want float64
	}{
		{0.30, 0.60}, // 低于 lo → 底
		{0.40, 0.60},
		{0.50, 1.00}, // 中点
		{0.60, 1.40},
		{0.90, 1.40}, // 高于 hi → 顶
	}
	for _, c := range cases {
		mult, ok := confSizingMultiplier(confInst(cfg), c.conf)
		if !ok {
			t.Fatalf("conf=%v 应启用", c.conf)
		}
		if math.Abs(mult-c.want) > 1e-9 {
			t.Fatalf("conf=%v want mult=%v got %v", c.conf, c.want, mult)
		}
	}
}

func TestConfSizingDefaultsFromMinConfidence(t *testing.T) {
	// 未配 conf_lo 时用 min_confidence 兜底；hi 默认 lo+0.15。
	cfg := map[string]interface{}{
		"conf_sizing_enabled": true,
		"min_confidence":      0.40,
	}
	mult, ok := confSizingMultiplier(confInst(cfg), 0.55)
	if !ok || math.Abs(mult-1.40) > 1e-9 {
		t.Fatalf("conf=0.55(=lo+0.15) 应到顶 1.40, got %v ok=%v", mult, ok)
	}
	mult, _ = confSizingMultiplier(confInst(cfg), 0.40)
	if math.Abs(mult-0.60) > 1e-9 {
		t.Fatalf("conf=0.40(=lo) 应在底 0.60, got %v", mult)
	}
}

func TestConfSizingMaxMultBelowOneHonored(t *testing.T) {
	// max_mult 落在 [0.2,1) 是合法配置。曾经的 `if maxM < 1 { maxM = 1 }`
	// 把这段整段销毁（配置的 0.8 被静默抬回 1.0），这里锁死修好后的行为。
	cfg := map[string]interface{}{
		"conf_sizing_enabled":  true,
		"conf_sizing_conf_lo":  0.40,
		"conf_sizing_conf_hi":  0.60,
		"conf_sizing_min_mult": 0.60,
		"conf_sizing_max_mult": 0.80,
	}
	hiM, _ := confSizingMultiplier(confInst(cfg), 0.90)
	if math.Abs(hiM-0.80) > 1e-9 {
		t.Fatalf("max_mult=0.8 应生效(0.8), got %v", hiM)
	}
	loM, _ := confSizingMultiplier(confInst(cfg), 0.10)
	if math.Abs(loM-0.60) > 1e-9 {
		t.Fatalf("min_mult=0.6 应生效(0.6), got %v", loM)
	}
	if hiM < loM {
		t.Fatalf("乘数不应随置信度上升而下降: low=%v hi=%v", loM, hiM)
	}
}

func TestConfSizingInsaneConfigClamped(t *testing.T) {
	// 配置写反/越界时收敛到 [0.2,1]×[1,2] 带宽内。
	cfg := map[string]interface{}{
		"conf_sizing_enabled":  true,
		"conf_sizing_min_mult": 0.01,
		"conf_sizing_max_mult": 9.9,
	}
	lowM, _ := confSizingMultiplier(confInst(cfg), 0.01)
	hiM, _ := confSizingMultiplier(confInst(cfg), 0.99)
	if lowM < 0.20 || hiM > 2.0 {
		t.Fatalf("乘数越界: low=%v hi=%v", lowM, hiM)
	}
	// max 配得比 min 还小 → 两端都收敛到合法带宽且不反向。
	cfg2 := map[string]interface{}{
		"conf_sizing_enabled":  true,
		"conf_sizing_min_mult": 0.9,
		"conf_sizing_max_mult": 0.3,
	}
	l2, _ := confSizingMultiplier(confInst(cfg2), 0.01)
	h2, _ := confSizingMultiplier(confInst(cfg2), 0.99)
	if h2 < l2 {
		t.Fatalf("乘数不应随置信度上升而下降: low=%v hi=%v", l2, h2)
	}
}

func TestConfLeverageDisabledByDefault(t *testing.T) {
	cfg := map[string]interface{}{"leverage": 10}
	if got := confLeverage(confInst(cfg), 0.99); got != 10 {
		t.Fatalf("默认应关闭、返回 config 杠杆 10, got %v", got)
	}
}

func TestConfLeverageZeroConfidencePassthrough(t *testing.T) {
	cfg := map[string]interface{}{"leverage": 10, "conf_leverage_enabled": true}
	if got := confLeverage(confInst(cfg), 0); got != 10 {
		t.Fatalf("信号未带置信度时应透传 config 杠杆 10, got %v", got)
	}
}

func TestConfLeverageLinearInterpolation(t *testing.T) {
	cfg := map[string]interface{}{
		"leverage":              10,
		"conf_leverage_enabled": true,
		"conf_leverage_conf_lo": 0.70,
		"conf_leverage_conf_hi": 0.98,
		"conf_leverage_min":     3,
		"conf_leverage_max":     20,
	}
	cases := []struct {
		conf float64
		want int
	}{
		{0.50, 3},  // 低于 lo → 底
		{0.70, 3},  // 恰在 lo → 底
		{0.84, 12}, // 中点 t=0.5 → 3+round(8.5)=12
		{0.98, 20}, // 恰在 hi → 顶
		{1.00, 20}, // 高于 hi → 顶
	}
	for _, c := range cases {
		if got := confLeverage(confInst(cfg), c.conf); got != c.want {
			t.Fatalf("conf=%v want lev=%v got %v", c.conf, c.want, got)
		}
	}
}

func TestConfLeverageFallsBackToSizingBand(t *testing.T) {
	// 未配 conf_leverage_conf_lo/hi 时，回落到 conf_sizing 的同一带宽。
	cfg := map[string]interface{}{
		"leverage":              10,
		"conf_leverage_enabled": true,
		"conf_sizing_conf_lo":   0.40,
		"conf_sizing_conf_hi":   0.60,
		"conf_leverage_min":     3,
		"conf_leverage_max":     20,
	}
	if got := confLeverage(confInst(cfg), 0.40); got != 3 {
		t.Fatalf("conf=0.40(=lo) 应在底 3, got %v", got)
	}
	if got := confLeverage(confInst(cfg), 0.60); got != 20 {
		t.Fatalf("conf=0.60(=hi) 应在顶 20, got %v", got)
	}
}

func TestConfLeverageInsaneConfigClamped(t *testing.T) {
	// max 配得比 min 小 → 两端收敛到合法带宽且不反向；上限夹到交易所界 125。
	cfg := map[string]interface{}{
		"leverage":              10,
		"conf_leverage_enabled": true,
		"conf_leverage_min":     30,
		"conf_leverage_max":     5,
	}
	loL := confLeverage(confInst(cfg), 0.01)
	hiL := confLeverage(confInst(cfg), 0.99)
	if hiL < loL {
		t.Fatalf("杠杆不应随置信度上升而下降: low=%v hi=%v", loL, hiL)
	}
	cfg2 := map[string]interface{}{
		"leverage":              10,
		"conf_leverage_enabled": true,
		"conf_leverage_min":     1,
		"conf_leverage_max":     9999,
	}
	if got := confLeverage(confInst(cfg2), 1.0); got != 125 {
		t.Fatalf("上限应夹到 125, got %v", got)
	}
}
