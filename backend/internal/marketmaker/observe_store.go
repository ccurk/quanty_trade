package marketmaker

import (
	"sort"
	"sync"
	"time"
)

// ObserveRow is the latest measured edge for one exec-exchange pair vs the feed.
type ObserveRow struct {
	Exchange      string    `json:"exchange"`
	Symbol        string    `json:"symbol"`
	RefMid        float64   `json:"ref_mid"`
	ExecMid       float64   `json:"exec_mid"`
	ExecSpreadBps float64   `json:"exec_spread_bps"`
	BuyEdgeBps    float64   `json:"buy_edge_bps"`
	SellEdgeBps   float64   `json:"sell_edge_bps"`
	Ts            time.Time `json:"ts"`
}

// BestEdgeBps is the larger of the two capturable edges (gross, before fees).
func (r ObserveRow) BestEdgeBps() float64 {
	if r.BuyEdgeBps > r.SellEdgeBps {
		return r.BuyEdgeBps
	}
	return r.SellEdgeBps
}

var (
	observeMu    sync.RWMutex
	observeStore = map[string]ObserveRow{}
	mmRunning    bool
)

func recordObserve(r ObserveRow) {
	observeMu.Lock()
	observeStore[r.Exchange+"|"+r.Symbol] = r
	observeMu.Unlock()
}

func setRunning(v bool) {
	observeMu.Lock()
	mmRunning = v
	observeMu.Unlock()
}

// ObserveSnapshot returns the latest observed edge per pair (widest gross edge
// first) plus whether the engine is running — for the dashboard's 做市 card.
func ObserveSnapshot() ([]ObserveRow, bool) {
	observeMu.RLock()
	defer observeMu.RUnlock()
	out := make([]ObserveRow, 0, len(observeStore))
	for _, r := range observeStore {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BestEdgeBps() > out[j].BestEdgeBps() })
	return out, mmRunning
}
