package strategy

import "math"

// roundMoney8 rounds a money quantity to 8 decimal places. Used at DB write
// boundaries so accumulated PnL / notional / avg-close values don't carry
// IEEE-754 drift across many fills. 8 dp matches USDT-margined precision.
func roundMoney8(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	// `+ 0` normalizes IEEE-754 negative zero to +0; it is not a no-op the
	// compiler may drop, because (-0)+(+0) = +0 while (-0)*1 = -0.
	//
	// Why it matters: a tiny negative residue rounds to -0 here (math.Round keeps
	// the sign), and -0 lands in the DB as a double whose text rendering is "-0".
	// Measured on prod 2026-09-09: exactly one row, strategy_positions id=1721
	// (Meme / TST/USDT / short), reproduced from its own stored entry/close
	// prices: 3990 * (0.015569999999999999 - 0.01557) = -6.92e-15 → -0.
	// Arithmetic is unaffected (-0 == 0 everywhere), but "-0" does not survive a
	// mysqldump round-trip: the dump writes the literal -0 and MySQL's parser
	// folds it back to +0, so any byte- or text-level restore check reports one
	// permanent phantom mismatch. A verification everyone learns to ignore is
	// worse than one that misses something (台账 #155 / #31).
	return math.Round(v*1e8)/1e8 + 0
}
