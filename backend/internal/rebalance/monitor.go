package rebalance

import (
	"strings"
	"sync"
	"time"
)

// Monitor polls both exchanges' spot balances on an interval and runs the planner
// to produce READ-ONLY rebalance recommendations. It never executes a transfer —
// execution is a separate, confirmation-gated path. The whitelist is fetched fresh
// each cycle (whitelistFn) so UI edits take effect without a restart.
type Monitor struct {
	cfg         *Config
	whitelistFn func() *Whitelist
	interval    time.Duration

	mu         sync.RWMutex
	balances   []Balance
	plans      []Plan
	lastUpdate time.Time
	lastErr    string
	running    bool
}

func NewMonitor(cfg *Config, whitelistFn func() *Whitelist, interval time.Duration) *Monitor {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &Monitor{cfg: cfg, whitelistFn: whitelistFn, interval: interval}
}

// Start launches the poll loop until stop is closed. Safe to call once.
func (m *Monitor) Start(stop <-chan struct{}) {
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()
	go func() {
		t := time.NewTicker(m.interval)
		defer t.Stop()
		m.refresh()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				m.refresh()
			}
		}
	}()
}

func (m *Monitor) refresh() {
	var all []Balance
	var errs []string
	if g, err := FetchGateSpotBalances(); err != nil {
		errs = append(errs, "gate: "+err.Error())
	} else {
		all = append(all, g...)
	}
	if b, err := FetchBinanceSpotBalances(); err != nil {
		errs = append(errs, "binance: "+err.Error())
	} else {
		all = append(all, b...)
	}

	var plans []Plan
	if m.cfg != nil && m.cfg.Enable && m.whitelistFn != nil {
		plans = m.cfg.BuildPlanner(m.whitelistFn()).Plan(all)
	}

	m.mu.Lock()
	m.balances = all
	m.plans = plans
	m.lastUpdate = time.Now()
	m.lastErr = strings.Join(errs, " | ")
	m.mu.Unlock()
}

// Snapshot is the read-only view for the API/UI.
type Snapshot struct {
	Balances   []Balance `json:"balances"`
	Plans      []Plan    `json:"plans"`
	LastUpdate time.Time `json:"last_update"`
	Error      string    `json:"error"`
	Running    bool      `json:"running"`
}

func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Snapshot{
		Balances:   m.balances,
		Plans:      m.plans,
		LastUpdate: m.lastUpdate,
		Error:      m.lastErr,
		Running:    m.running,
	}
}
