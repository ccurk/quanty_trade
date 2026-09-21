package strategy

import (
	"math"
	"testing"

	"quanty_trade/internal/exchange"
)

// 2026-09-21：杠杆按【止损宽度】封顶，让止损夹永不咬合。
//
// 病（活体实测，n=4015 条 long 信号）：ATR 止损距离中位 1.954%、p90 3.662%；而
// lev=20（conf=1.0，占信号 31%）时止损夹 = 0.3/20 = 1.500% ⇒ 75.8% 的入场止损被
// strategy_position.go:508 收紧到 1.500%，止损落进币自己的噪声带里（实测 76% 先破刀），
// 而止盈没跟着动，R:R 仍是 1.250。
//
// 修法：不动止损，改降杠杆使 0.3/lev ≥ ATR 止损距离。单笔止损金额 =
// 名义×止损宽 = (权益×pct×lev)×止损宽，cap 生效时逐笔恒等于 权益×pct×0.3。
func TestLeverageCappedByStopWidth(t *testing.T) {
	const px = 100.0
	const equity = 200.0
	const pct = 0.375
	initial := equity * pct // 75

	newInst := func() *StrategyInstance {
		return pctInst(map[string]interface{}{
			"conf_leverage_enabled": true,
			"conf_leverage_min":     3,
			"conf_leverage_max":     20,
			"conf_leverage_conf_lo": 0.70,
			"conf_leverage_conf_hi": 0.98,
		})
	}

	// 宽止损 3%：conf=1.0 → confLeverage=20，但 0.3/0.03 = 10 ⇒ 应封顶到 10
	t.Run("宽止损降杠杆", func(t *testing.T) {
		stubBal(t, equity, equity, 0)
		sl := px * (1 - 0.03)
		amt, err := resolveUSDMOrderAmount(newInst(), &exchange.BinanceExchange{}, "TESTUSDT", 0, px, sl, 1.0)
		if err != nil || amt <= 0 {
			t.Fatalf("应能下单, amt=%v err=%v", amt, err)
		}
		notional := amt * px
		if math.Abs(notional-initial*10) > 1e-6 {
			t.Fatalf("杠杆应被封顶到 10（名义 = %.1f×10 = %.1f），实际名义 = %.4f",
				initial, initial*10, notional)
		}
		// 不变量：单笔止损金额 = 保证金的 30%
		if risk := notional * 0.03; math.Abs(risk-initial*0.3) > 1e-6 {
			t.Fatalf("单笔止损金额应为保证金的 30%%（= %.4f），实际 = %.4f", initial*0.3, risk)
		}
		// 不变量：止损宽 ≤ 夹（0.3/levChosen）⇒ strategy_position.go:508 夹不咬合
		if clamp := 0.3 / 10.0; 0.03 > clamp+1e-12 {
			t.Fatalf("止损宽 %.4f 仍宽于夹 %.4f，夹会咬合", 0.03, clamp)
		}
	})

	// 窄止损 1%：0.3/0.01 = 30 > 20 ⇒ 不封顶，杠杆仍按置信度走
	t.Run("窄止损不干预", func(t *testing.T) {
		stubBal(t, equity, equity, 0)
		sl := px * (1 - 0.01)
		amt, err := resolveUSDMOrderAmount(newInst(), &exchange.BinanceExchange{}, "TESTUSDT", 0, px, sl, 1.0)
		if err != nil || amt <= 0 {
			t.Fatalf("应能下单, amt=%v err=%v", amt, err)
		}
		notional := amt * px
		if math.Abs(notional-initial*20) > 1e-6 {
			t.Fatalf("窄止损不应被干预（名义应为 %.1f），实际 = %.4f", initial*20, notional)
		}
	})

	// 无止损（stopLoss=0）：跳过封顶逻辑，行为与改动前完全一致
	t.Run("无止损回退旧行为", func(t *testing.T) {
		stubBal(t, equity, equity, 0)
		amt, err := resolveUSDMOrderAmount(newInst(), &exchange.BinanceExchange{}, "TESTUSDT", 0, px, 0, 1.0)
		if err != nil || amt <= 0 {
			t.Fatalf("应能下单, amt=%v err=%v", amt, err)
		}
		if notional := amt * px; math.Abs(notional-initial*20) > 1e-6 {
			t.Fatalf("无止损时不应封顶（名义应为 %.1f），实际 = %.4f", initial*20, notional)
		}
	})

	// 尊重 owner「杠杆 3~20x」下限：极宽止损（20%）时 cap=1 但被抬到 3
	t.Run("极宽止损受 conf_leverage_min 托底", func(t *testing.T) {
		stubBal(t, equity, equity, 0)
		sl := px * (1 - 0.20)
		amt, err := resolveUSDMOrderAmount(newInst(), &exchange.BinanceExchange{}, "TESTUSDT", 0, px, sl, 1.0)
		if err != nil || amt <= 0 {
			t.Fatalf("应能下单, amt=%v err=%v", amt, err)
		}
		if notional := amt * px; math.Abs(notional-initial*3) > 1e-6 {
			t.Fatalf("应被 conf_leverage_min=3 托底（名义应为 %.1f），实际 = %.4f", initial*3, notional)
		}
	})
}
