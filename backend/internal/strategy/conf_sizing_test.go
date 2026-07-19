package strategy

import (
	"math"
	"testing"
)

func confInst(cfg map[string]interface{}) *StrategyInstance {
	return &StrategyInstance{Config: cfg}
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
