package marketmaker

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// ObserveRow is the latest measured edge for one exec-exchange pair vs the feed,
// with the maker fee folded in so NET edge (what you could actually keep) is front
// and center — the gross edge alone is misleading.
type ObserveRow struct {
	Exchange       string    `json:"exchange"`
	Symbol         string    `json:"symbol"`
	RefMid         float64   `json:"ref_mid"`
	ExecMid        float64   `json:"exec_mid"`
	ExecSpreadBps  float64   `json:"exec_spread_bps"`
	BuyEdgeBps     float64   `json:"buy_edge_bps"`
	SellEdgeBps    float64   `json:"sell_edge_bps"`
	FeeBps         float64   `json:"fee_bps"`  // one-leg maker fee (bps)
	FeeLive        bool      `json:"fee_live"` // true=交易所实时费率, false=默认假设
	NetBestEdgeBps float64   `json:"net_best_edge_bps"` // BestEdge - 2*fee(买卖两腿)
	Ts             time.Time `json:"ts"`
}

// BestEdgeBps is the larger of the two capturable edges (gross, before fees).
func (r ObserveRow) BestEdgeBps() float64 {
	if r.BuyEdgeBps > r.SellEdgeBps {
		return r.BuyEdgeBps
	}
	return r.SellEdgeBps
}

// PairStat is the accumulated net-edge statistics for one pair over the run —
// this is what turns "a live number" into "data to decide on".
type PairStat struct {
	Exchange  string    `json:"exchange"`
	Symbol    string    `json:"symbol"`
	Samples   int64     `json:"samples"`
	MaxNetBps float64   `json:"max_net_bps"`
	AvgNetBps float64   `json:"avg_net_bps"`
	PosRate   float64   `json:"pos_rate"` // net>0 的采样占比
	Since     time.Time `json:"since"`
}

type statAcc struct {
	samples, pos   int64
	maxNet, sumNet float64
	since          time.Time
}

var (
	observeMu    sync.RWMutex
	observeStore = map[string]ObserveRow{}
	statStore    = map[string]*statAcc{}
	mmRunning    bool
)

func recordObserve(r ObserveRow) {
	k := r.Exchange + "|" + r.Symbol
	observeMu.Lock()
	observeStore[k] = r
	s := statStore[k]
	if s == nil {
		s = &statAcc{since: time.Now()}
		statStore[k] = s
	}
	s.samples++
	s.sumNet += r.NetBestEdgeBps
	if s.samples == 1 || r.NetBestEdgeBps > s.maxNet {
		s.maxNet = r.NetBestEdgeBps
	}
	if r.NetBestEdgeBps > 0 {
		s.pos++
	}
	observeMu.Unlock()
}

func setRunning(v bool) {
	observeMu.Lock()
	mmRunning = v
	observeMu.Unlock()
}

// ObserveSnapshot returns the latest observed row per pair (widest NET edge first)
// plus whether the engine is running — for the dashboard's 做市 card.
func ObserveSnapshot() ([]ObserveRow, bool) {
	observeMu.RLock()
	defer observeMu.RUnlock()
	out := make([]ObserveRow, 0, len(observeStore))
	for _, r := range observeStore {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NetBestEdgeBps > out[j].NetBestEdgeBps })
	return out, mmRunning
}

// StatsSnapshot returns per-pair net-edge stats (highest max-net first).
func StatsSnapshot() []PairStat {
	observeMu.RLock()
	defer observeMu.RUnlock()
	out := make([]PairStat, 0, len(statStore))
	for k, s := range statStore {
		ex, sym, _ := strings.Cut(k, "|")
		ps := PairStat{Exchange: ex, Symbol: sym, Samples: s.samples, MaxNetBps: s.maxNet, Since: s.since}
		if s.samples > 0 {
			ps.AvgNetBps = s.sumNet / float64(s.samples)
			ps.PosRate = float64(s.pos) / float64(s.samples)
		}
		out = append(out, ps)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MaxNetBps > out[j].MaxNetBps })
	return out
}
