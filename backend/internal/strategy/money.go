package strategy

import "math"

// roundMoney8 rounds a money quantity to 8 decimal places. Used at DB write
// boundaries so accumulated PnL / notional / avg-close values don't carry
// IEEE-754 drift across many fills. 8 dp matches USDT-margined precision.
func roundMoney8(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	return math.Round(v*1e8) / 1e8
}
