package rebalance

import (
	"sync"
	"time"

	"quanty_trade/internal/logger"
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
	blocked    string // non-empty ⇒ inventory unknown this cycle, no plans were produced
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
	bs := FetchAllSpotBalances()

	var plans []Plan
	var blocked string
	if m.cfg != nil && m.cfg.Enable && m.whitelistFn != nil {
		plans, blocked = m.cfg.BuildPlanner(m.whitelistFn()).Plan(bs)
	}

	m.mu.Lock()
	prevErr := m.lastErr
	m.balances = bs.Balances()
	m.plans = plans
	m.blocked = blocked
	m.lastUpdate = time.Now()
	m.lastErr = bs.Err()
	m.mu.Unlock()

	// 让外面看得见:余额读不到时必须有日志,而不是只留一个没人看的字符串字段。
	// 只在状态翻转时打,因为 refresh 每 15s 一轮,每轮都打会把日志淹掉、反而没人看。
	if bs.Err() != prevErr {
		switch {
		case bs.Err() == "":
			logger.Infof("[rebalance] 余额读取已恢复")
		case blocked != "":
			logger.Errorf("[rebalance] %s", blocked) // 已含具体错误,不再重复打一遍
		default:
			logger.Errorf("[rebalance] 余额读取失败: %s", bs.Err())
		}
	}
}

// Snapshot is the read-only view for the API/UI. Blocked is what keeps the UI
// honest: with it empty, "0 plans" means "everything is inside the band"; with it
// set, it means "we don't know what's there" — two very different things that used
// to render as the same reassuring line.
type Snapshot struct {
	Balances   []Balance `json:"balances"`
	Plans      []Plan    `json:"plans"`
	Blocked    string    `json:"blocked"`
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
		Blocked:    m.blocked,
		LastUpdate: m.lastUpdate,
		Error:      m.lastErr,
		Running:    m.running,
	}
}
