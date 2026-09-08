package equity

import "time"

// 公司资金口径 v1 —— THE single place this estimate is written down.
//
// It exists because the same question got answered with different numbers by
// different people, and the disagreement was never visible: 台账 #18 and #22
// collided because $57.60 (Polymarket) and $236 (Binance perp) were treated as two
// competing estimates of ONE quantity and triangulated into a "consensus", when
// they are balances at TWO DIFFERENT VENUES and belong ADDED
// (state/strategy/equity-blindspot-2026-09-09.md §4). Hence the hard rule below,
// and hence Snapshot.Venue being mandatory.
//
//	RULE: never write "we have $X". Every funding claim carries a venue label.
//
// This whole block is a STOPGAP. It is an estimate assembled from indirect
// evidence, and it exists only until equity_snapshots has real rows in it — at
// which point the answer is a SELECT and this file should be deleted, not updated.
// Nothing in the trading path reads these numbers; they are here so that a human
// or a report can cite one constant instead of re-deriving a different number.
const (
	// BaselinePolymarketUSD: owner's UI reading, state/owner_actions.json #1.
	// Evidence: medium — not independently checkable on-chain.
	BaselinePolymarketUSD = 57.60

	// Gate spot: config × measured resting order prices, a LOWER bound.
	// Corroborated only negatively: 432,451 log lines contain zero balance errors.
	BaselineGateSpotLowUSD  = 17.00
	BaselineGateSpotHighUSD = 45.00

	// Binance USD-M: the WEAKEST leg. Back-solved from daily_pn_ls fill notionals
	// ÷ (order_amount_pct × leverage). It is a lower bound on AVAILABLE balance,
	// not a measurement of equity, and it rests on three unverified assumptions
	// (equity-blindspot-2026-09-09.md §3). 老徐's "$236, medium confidence" label
	// is what this replaces.
	BaselineBinanceUSDMLowUSD  = 230.00
	BaselineBinanceUSDMHighUSD = 300.00

	// BaselineHardFloorUSD is the only number here with a directly observed basis
	// under every component: Polymarket $57.60 + Gate $17.40. Everything above it
	// is inference. When a decision cannot tolerate being wrong, use THIS.
	BaselineHardFloorUSD = 75.00

	// Point estimate and interval across all venues.
	// The error is ASYMMETRIC — the true value is more likely ABOVE the point
	// estimate than below, because the Binance leg is a lower bound and Binance
	// spot was never measured at all (assumed 0, no evidence either way).
	BaselineTotalUSD     = 355.00
	BaselineTotalLowUSD  = 300.00
	BaselineTotalHighUSD = 420.00
)

// BaselineAsOf is when this was assembled.
var BaselineAsOf = time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)

// BaselineShelfLife is how long the Binance leg stays meaningful even if nothing
// else happens: it is back-solved from the daily_pn_ls window, which rolls
// forward every day, so the evidence underneath it is continuously replaced.
const BaselineShelfLife = 7 * 24 * time.Hour

// BaselineInvalidatedBy lists the events that void this estimate ON THE DAY THEY
// HAPPEN, regardless of shelf life. All three are already queued, so this is not a
// hypothetical list — expect it to fire.
//
// There is no way for this process to observe any of them, which is precisely why
// the fix is equity_snapshots rather than a better estimate.
var BaselineInvalidatedBy = []string{
	"owner_actions #1: 把 $57.60 从永续划到 Predictions → Polymarket 档失效",
	"owner_actions #2: 换 marketmaker.json.pending → Gate 档失效",
	"meme 引擎 order_amount_pct 0.08→0.01 落地 → Binance 反解常数改变,整档失效",
}

// BaselineStale reports whether the estimate is past its shelf life at now.
// It cannot detect the BaselineInvalidatedBy events — those need a human, or the
// table this package writes.
func BaselineStale(now time.Time) bool {
	return now.Sub(BaselineAsOf) > BaselineShelfLife
}
