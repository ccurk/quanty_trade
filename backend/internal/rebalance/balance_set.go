package rebalance

import "strings"

// balance_set.go exists for exactly one reason: "we could not read this exchange's
// balances" and "this exchange holds nothing" used to reach the planner as the same
// thing — an absent row. That is how a failed API call turns into a real withdrawal:
// an unread gate looks like an empty gate, an empty gate looks like it needs a
// refill, and a refill is a live transfer of real money.
//
// A BalanceSet records, per exchange, whether that exchange's balances were actually
// READ. Unknown is not zero, and only one of the two is allowed to produce a plan.

// BalanceSet is a balance snapshot plus per-exchange read status. The zero value is
// usable and means "nothing read, nothing known" — which is the safe default.
type BalanceSet struct {
	balances []Balance
	known    map[string]bool // exchange (lower-cased) -> its balances were really read
	errs     []string
}

// Add records one exchange's fetch outcome. A non-nil err leaves that exchange
// UNKNOWN and discards any rows handed in alongside it: a partial read is not a
// snapshot, and half a snapshot is precisely what produces a wrong-but-plausible
// transfer. Callers must call Add for every exchange they tried, errors included —
// an exchange nobody reports on simply stays unknown.
func (s *BalanceSet) Add(exchange string, rows []Balance, err error) {
	ex := normExchange(exchange)
	if err != nil {
		s.errs = append(s.errs, ex+": "+err.Error())
		return // known[ex] stays false — fail closed
	}
	if s.known == nil {
		s.known = map[string]bool{}
	}
	s.known[ex] = true
	s.balances = append(s.balances, rows...)
}

// Known reports whether this exchange's balances were actually read. False means
// UNKNOWN — never read it as "zero".
func (s *BalanceSet) Known(exchange string) bool {
	if s == nil {
		return false
	}
	return s.known[normExchange(exchange)]
}

// Balances returns the rows from the exchanges that were read successfully.
func (s *BalanceSet) Balances() []Balance {
	if s == nil {
		return nil
	}
	return s.balances
}

// Err joins every failed exchange's error, or "" when every read succeeded.
func (s *BalanceSet) Err() string {
	if s == nil {
		return ""
	}
	return strings.Join(s.errs, " | ")
}

// FetchAllSpotBalances reads both exchanges and keeps each one's read status. It is
// the single entry point for "give me current balances" so the three callers (the
// monitor, the manual execute endpoint, the auto loop) cannot each re-invent the
// error handling — they used to, and all three dropped the error the same way.
func FetchAllSpotBalances() *BalanceSet {
	var bs BalanceSet
	g, gErr := FetchGateSpotBalances()
	bs.Add("gate", g, gErr)
	b, bErr := FetchBinanceSpotBalances()
	bs.Add("binance", b, bErr)
	return &bs
}

func normExchange(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
