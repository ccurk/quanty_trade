package rebalance

import (
	"fmt"
	"sort"
	"strings"
)

// Planner turns a balance snapshot into concrete transfer proposals, bounded by
// the inventory bands and the withdrawal whitelist. It NEVER executes — it only
// proposes; execution is a separate, confirmation-gated step.
//
// Exec is the exchange whose inventory we manage (e.g. "gate"); Reservoir is the
// funding hub surplus flows to and shortfall is pulled from (e.g. "binance").
// Network is the chain used to move each asset (e.g. "TRC20" for USDT) — one per
// asset via Networks, falling back to DefaultNetwork.
type Planner struct {
	Exec           string
	Reservoir      string
	DefaultNetwork string
	Networks       map[string]string // asset -> chain override, e.g. {"USDT":"TRC20"}
	Bands          []AssetBand
	Whitelist      *Whitelist
	// MinTransfer skips dust: an out-of-band gap smaller than this (in asset units)
	// produces no plan — moving tiny amounts just burns withdrawal fees.
	MinTransfer map[string]float64
}

func (p *Planner) network(asset string) string {
	if n, ok := p.Networks[strings.ToUpper(asset)]; ok && n != "" {
		return n
	}
	return p.DefaultNetwork
}

// Plan compares current Exec-exchange balances against the bands and returns the
// transfers needed to bring each out-of-band asset back to Target. Balances for
// other exchanges are ignored. Each plan's destination is resolved from the
// whitelist; if none is whitelisted the plan is still returned but marked
// non-executable (empty ToAddress + a Reason) — surfaced, never silently dropped.
func (p *Planner) Plan(balances []Balance) []Plan {
	onExec := map[string]float64{}
	for _, b := range balances {
		if eqFold(b.Exchange, p.Exec) {
			onExec[strings.ToUpper(b.Asset)] += b.Total()
		}
	}

	var out []Plan
	for _, band := range p.Bands {
		asset := strings.ToUpper(band.Asset)
		cur := onExec[asset]
		var amt float64
		var from, to, reason string
		switch {
		case cur < band.Min:
			amt = band.Target - cur // pull IN: reservoir -> exec
			from, to = p.Reservoir, p.Exec
			reason = fmt.Sprintf("%s 库存 %.6f < 下限 %.6f,补至目标 %.6f", asset, cur, band.Min, band.Target)
		case cur > band.Max:
			amt = cur - band.Target // push OUT: exec -> reservoir
			from, to = p.Exec, p.Reservoir
			reason = fmt.Sprintf("%s 库存 %.6f > 上限 %.6f,回落至目标 %.6f", asset, cur, band.Max, band.Target)
		default:
			continue // inside the dead-band: do nothing
		}
		if min := p.MinTransfer[asset]; amt < min {
			continue // below dust threshold: not worth a withdrawal fee
		}
		out = append(out, p.build(asset, from, to, amt, reason))
	}
	// Largest imbalance first — most urgent to act on.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Amount > out[j].Amount })
	return out
}

func (p *Planner) build(asset, from, to string, amount float64, reason string) Plan {
	net := p.network(asset)
	pl := Plan{
		Asset:        asset,
		FromExchange: from,
		ToExchange:   to,
		Amount:       amount,
		Network:      net,
		Reason:       reason,
	}
	if addr, memo, ok := p.Whitelist.Resolve(to, asset, net); ok {
		pl.ToAddress = addr
		pl.Memo = memo
	} else {
		pl.Reason += fmt.Sprintf(" | ⚠ 目标 %s/%s/%s 不在白名单,无法执行", to, asset, net)
	}
	return pl
}
