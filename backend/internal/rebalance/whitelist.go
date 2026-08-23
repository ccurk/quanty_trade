package rebalance

// Whitelist is the app-side allow-list of withdrawal destinations — the SECOND of
// the two guards (the exchange's own address whitelist is the first, hard one).
// A transfer destination must resolve here or the planner refuses to move funds.
type Whitelist struct {
	addrs []AllowedAddress
}

func NewWhitelist(addrs []AllowedAddress) *Whitelist { return &Whitelist{addrs: addrs} }

// Resolve returns the whitelisted deposit address (and memo) for (exchange, asset,
// network), or ok=false when nothing matches. Matching is case-insensitive on all
// three keys so config casing ("gate"/"Gate", "trc20"/"TRC20") never silently misses.
func (w *Whitelist) Resolve(exchange, asset, network string) (addr, memo string, ok bool) {
	if w == nil {
		return "", "", false
	}
	for _, a := range w.addrs {
		if eqFold(a.Exchange, exchange) && eqFold(a.Asset, asset) && eqFold(a.Network, network) {
			return a.Address, a.Memo, true
		}
	}
	return "", "", false
}

// Has reports whether a destination is whitelisted, without returning the address.
func (w *Whitelist) Has(exchange, asset, network string) bool {
	_, _, ok := w.Resolve(exchange, asset, network)
	return ok
}
