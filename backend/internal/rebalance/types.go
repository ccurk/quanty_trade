// Package rebalance keeps the execution exchange's spot inventory inside a target
// band by proposing cross-exchange transfers (e.g. binance<->gate). It is split so
// the DANGEROUS part (moving real funds) is isolated behind two independent guards:
//
//  1. the exchange's own withdrawal address whitelist (the hard guarantee), and
//  2. this app's Whitelist allow-list (a second check before we ever submit).
//
// The Planner in this package ONLY proposes transfers — it never executes. Nothing
// here holds credentials or calls a withdrawal API; execution lives elsewhere and
// is gated on confirmation.
package rebalance

import "strings"

// Balance is a spot balance snapshot for one asset on one exchange.
type Balance struct {
	Exchange string  `json:"exchange"`
	Asset    string  `json:"asset"`
	Free     float64 `json:"free"`
	Locked   float64 `json:"locked"`
}

// Total is free + locked (locked = resting in open orders).
func (b Balance) Total() float64 { return b.Free + b.Locked }

// AllowedAddress is one whitelisted withdrawal destination. A transfer may ONLY
// target an address listed here — and that same address must also be whitelisted
// on the destination exchange itself. Asset AND Network must match the deposit
// address exactly: sending USDT-TRC20 to a USDT-ERC20 address loses the funds.
type AllowedAddress struct {
	Exchange string // destination exchange we withdraw TO, e.g. "gate"
	Asset    string // e.g. "USDT"
	Network  string // chain, e.g. "TRC20" — must match the address's chain exactly
	Address  string
	Memo     string // tag/memo for chains that require it; empty otherwise
	Label    string // human note, e.g. "gate USDT 充值地址"
}

// AssetBand is the inventory target for one asset on the execution exchange. Below
// Min → pull funds IN from the reservoir; above Max → push funds OUT to it. The
// Min<current<Max dead-band is what stops the system from thrashing on every tick.
type AssetBand struct {
	Asset  string
	Target float64 // amount to return to when out of band
	Min    float64 // below this → refill
	Max    float64 // above this → drain
}

// Plan is one proposed transfer. It is a PROPOSAL, not an action. ToAddress is
// resolved from the whitelist; an empty ToAddress means no whitelisted destination
// was found and the transfer CANNOT be executed — the Reason says why.
type Plan struct {
	Asset        string  `json:"asset"`
	FromExchange string  `json:"from_exchange"`
	ToExchange   string  `json:"to_exchange"`
	Amount       float64 `json:"amount"`
	Network      string  `json:"network"`
	ToAddress    string  `json:"to_address"` // "" ⇒ blocked (no whitelisted destination)
	Memo         string  `json:"memo"`
	Reason       string  `json:"reason"`
}

// Executable is true only when a whitelisted destination address was resolved.
func (p Plan) Executable() bool { return strings.TrimSpace(p.ToAddress) != "" }

func eqFold(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }
