package strategy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"quanty_trade/internal/bus"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"quanty_trade/internal/ws"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// resolveStrategyPath converts a strategy Path stored in DB into an executable
// absolute file path.
//
// Rules:
// - If Path is absolute, use it as-is.
// - If STRATEGIES_DIR is set, treat Path as relative to that directory.
// - Reject paths that escape STRATEGIES_DIR (basic path traversal guard).
func resolveStrategyPath(p string) (string, error) {
	if filepath.IsAbs(p) {
		fi, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		if fi.IsDir() {
			return "", fmt.Errorf("strategy path is a directory, need .py file: %s", p)
		}
		return p, nil
	}
	base := conf.C().Paths.StrategiesDir
	if base == "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return "", err
		}
		if fi.IsDir() {
			return "", fmt.Errorf("strategy path is a directory, need .py file: %s", abs)
		}
		return abs, nil
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	joined := filepath.Clean(filepath.Join(absBase, p))
	if fi, err := os.Stat(joined); err == nil && fi.IsDir() {
		return "", fmt.Errorf("strategy path is a directory, need .py file: %s", joined)
	}
	rel, err := filepath.Rel(absBase, joined)
	if err != nil {
		return "", err
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
		return joined, nil
	}
	return "", fmt.Errorf("invalid strategy path: %s", p)
}

func parseSymbolsValue(v interface{}) []string {
	out := make([]string, 0)
	switch t := v.(type) {
	case []string:
		for _, s := range t {
			if x := strings.TrimSpace(s); x != "" {
				out = append(out, x)
			}
		}
	case []interface{}:
		for _, it := range t {
			if s, ok := it.(string); ok {
				if x := strings.TrimSpace(s); x != "" {
					out = append(out, x)
				}
			}
		}
	case string:
		for _, p := range strings.Split(t, ",") {
			if x := strings.TrimSpace(p); x != "" {
				out = append(out, x)
			}
		}
	}
	seen := map[string]struct{}{}
	dedup := make([]string, 0, len(out))
	for _, s := range out {
		k := strings.ToUpper(strings.TrimSpace(s))
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		dedup = append(dedup, s)
	}
	if len(dedup) > 20 {
		dedup = dedup[:20]
	}
	return dedup
}

func isAllowedSymbol(inst *StrategyInstance, symbol string) bool {
	if inst == nil {
		return false
	}
	sym := exchange.NormalizeSymbol(symbol)
	if sym == "" {
		return false
	}
	if xs := parseSymbolsValue(inst.Config["symbols"]); len(xs) > 0 {
		for _, s := range xs {
			if exchange.NormalizeSymbol(s) == sym {
				return true
			}
		}
		return false
	}
	if raw, ok := inst.Config["symbol"].(string); ok && strings.TrimSpace(raw) != "" {
		return exchange.NormalizeSymbol(raw) == sym
	}
	return true
}

func getNumber(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f
		}
		return 0
	default:
		return 0
	}
}

func getString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func getBool(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "true" || s == "1" || s == "yes" || s == "y" || s == "on"
	default:
		return false
	}
}

func clampOrderAmount(inst *StrategyInstance, requested float64) float64 {
	if inst == nil {
		return 0
	}
	amt := requested
	if amt <= 0 {
		amt = getNumber(inst.Config["trade_amount"])
	}
	if amt <= 0 {
		return 0
	}
	maxAmt := getNumber(inst.Config["max_order_amount"])
	if maxAmt <= 0 {
		maxAmt = getNumber(inst.Config["max_trade_amount"])
	}
	if maxAmt > 0 && amt > maxAmt {
		amt = maxAmt
	}
	minAmt := getNumber(inst.Config["min_order_amount"])
	if minAmt > 0 && amt < minAmt {
		return 0
	}
	return amt
}

// StrategyStatus is the runtime state of a strategy process managed by Manager.
type StrategyStatus string

const (
	StatusRunning StrategyStatus = "running"
	StatusStopped StrategyStatus = "stopped"
	StatusError   StrategyStatus = "error"
)

type StrategyInstance struct {
	// ID is the StrategyInstance primary key (UUID string).
	ID string `json:"id"`
	// Name is the user-visible strategy name.
	Name string `json:"name"`
	// Path points to the python file of this strategy.
	Path             string `json:"path"`
	RuntimePath      string `json:"runtime_path"`
	RuntimeGenerated bool   `json:"runtime_generated"`
	RuntimeKeep      bool   `json:"runtime_keep"`
	// Config is the in-memory decoded config JSON.
	Config map[string]interface{} `json:"config"`
	// Status is the process state.
	Status StrategyStatus `json:"status"`
	// OwnerID is the user who owns this running instance.
	OwnerID uint `json:"owner_id"`
	// CreatedAt is when this in-memory instance was created.
	CreatedAt time.Time `json:"created_at"`

	// cmd/stdout are the managed python process and pipes.
	cmd    *exec.Cmd
	stdout io.ReadCloser

	// mu guards process state and pipes.
	mu sync.Mutex
	// orderMu guards inflight open order counters and concurrency checks.
	orderMu       sync.Mutex
	inflightOpen  int
	lastSkipLogAt map[string]time.Time
	invalidSymbol map[string]time.Time
	// hub is the websocket broadcaster for UI updates.
	hub *ws.Hub
	// exchange is the exchange implementation (mock/binance/etc.).
	exchange exchange.Exchange

	mgr *Manager

	redisCancel     context.CancelFunc
	bootID          string
	startedAt       time.Time
	lastHB          time.Time
	stopping        bool
	restarting      bool
	resync          bool
	resyncLogBootID string
	resyncNextAt    time.Time
	resyncBackoff   time.Duration
	stateReadySeen  bool
	heartbeatSeen   bool
	feedSymbols     []string
	candleStops     map[string]func()
	candlePubCount  map[string]int
	candleRxCount   map[string]int
	lastCandleClose map[string]float64
	lastCandleAt    map[string]time.Time
}

// Manager manages lifecycle of all strategy instances and coordinates exchange access.
type Manager struct {
	// instances keeps in-memory runtime state keyed by strategy instance id.
	instances map[string]*StrategyInstance
	mu        sync.RWMutex
	// hub broadcasts runtime events (logs/orders/candles/backtest updates) to frontend.
	hub *ws.Hub
	// exchange is the global exchange connector used by all strategies.
	exchange exchange.Exchange

	redisBus *bus.RedisBus
}

func (m *Manager) ReleaseOpenSlot(strategyID string) {
	if m == nil || strings.TrimSpace(strategyID) == "" {
		return
	}
	m.mu.RLock()
	rb := m.redisBus
	m.mu.RUnlock()
	if rb == nil {
		return
	}
	_, _ = rb.ReleaseOpenSlot(context.Background(), strategyID)
}

func (m *Manager) SyncRedisOpenCountsFromExchange(ctx context.Context) {
	if m == nil {
		return
	}

	m.mu.RLock()
	rb := m.redisBus
	ex := m.exchange
	snap := make([]*StrategyInstance, 0, len(m.instances))
	for _, inst := range m.instances {
		snap = append(snap, inst)
	}
	m.mu.RUnlock()
	if rb == nil || ex == nil {
		return
	}

	bx, ok := ex.(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return
	}

	byOwner := map[uint][]*StrategyInstance{}
	for _, inst := range snap {
		if inst == nil || inst.OwnerID == 0 || strings.TrimSpace(inst.ID) == "" {
			continue
		}
		byOwner[inst.OwnerID] = append(byOwner[inst.OwnerID], inst)
	}

	for ownerID, insts := range byOwner {
		ps, err := ex.FetchPositions(ownerID, "active")
		if err != nil {
			logger.Errorf("[REDIS OPEN COUNT] sync failed owner=%d err=%v", ownerID, err)
			continue
		}
		openBySymbol := map[string]struct{}{}
		for _, p := range ps {
			if p.Amount <= 0 {
				continue
			}
			openBySymbol[exchange.NormalizeSymbol(p.Symbol)] = struct{}{}
		}

		for _, inst := range insts {
			allowed := parseSymbolsValue(inst.Config["symbols"])
			if len(allowed) == 0 {
				if sym, ok := inst.Config["symbol"].(string); ok && strings.TrimSpace(sym) != "" {
					allowed = []string{strings.TrimSpace(sym)}
				}
			}
			n := int64(0)
			if len(allowed) > 0 {
				seen := map[string]struct{}{}
				for _, s := range allowed {
					k := exchange.NormalizeSymbol(s)
					if k == "" {
						continue
					}
					if _, ok := seen[k]; ok {
						continue
					}
					seen[k] = struct{}{}
					if _, ok := openBySymbol[k]; ok {
						n++
					}
				}
			}
			_ = rb.SetOpenCount(ctx, inst.ID, n, 6*time.Hour)
		}
	}
}

func emitStrategyLog(inst *StrategyInstance, level string, msg string) {
	if inst == nil || strings.TrimSpace(msg) == "" {
		return
	}
	if database.DB != nil {
		_ = database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      level,
			Message:    msg,
			CreatedAt:  time.Now(),
		}).Error
	}
	if inst.hub != nil {
		inst.hub.BroadcastJSON(map[string]interface{}{
			"type": "log",
			"data": msg,
			"id":   inst.ID,
		})
	}
}

// BacktestResult is returned by synchronous backtests and stored in Backtest.Result.
type BacktestResult struct {
	TotalTrades    int           `json:"total_trades"`
	TotalProfit    float64       `json:"total_profit"`
	ReturnRate     float64       `json:"return_rate"`
	InitialBalance float64       `json:"initial_balance"`
	FinalBalance   float64       `json:"final_balance"`
	EquityCurve    []EquityPoint `json:"equity_curve"`
}

type EquityPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Equity    float64   `json:"equity"`
}

// NewManager constructs a Manager.
//
// Typical usage (see cmd/main.go):
// - Create Hub and run it in a goroutine
// - Create Exchange implementation (Mock/Binance)
// - NewManager(hub, exchange)
func NewManager(hub *ws.Hub, ex exchange.Exchange) *Manager {
	return &Manager{
		instances: make(map[string]*StrategyInstance),
		hub:       hub,
		exchange:  ex,
	}
}

func (m *Manager) SetRedisBus(b *bus.RedisBus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redisBus = b
}

// AddStrategy registers an in-memory strategy instance. It does not start the process.
//
// Typical usage:
// - When creating a StrategyInstance in DB (API CreateStrategy)
// - When syncing from DB on startup (SyncFromDB)
func (m *Manager) AddStrategy(id, name, path string, ownerID uint, config map[string]interface{}) *StrategyInstance {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := &StrategyInstance{
		ID:        id,
		Name:      name,
		Path:      path,
		Config:    config,
		Status:    StatusStopped,
		OwnerID:   ownerID,
		CreatedAt: time.Now(),
		hub:       m.hub,
		exchange:  m.exchange,
		mgr:       m,
	}
	m.instances[id] = inst
	return inst
}

// StartStrategy starts the python process for a strategy instance.
//
// Process protocol:
// - Backend sends JSON lines to stdin: {"type":"candle","data":{...}}
// - Strategy sends JSON lines to stdout:
//   - {"type":"log","data":"..."}
//   - {"type":"order","data":{"symbol":"BTC/USDT","side":"buy","amount":0.01,"price":0}}
//
// Side effects:
// - Starts market data subscription if config contains "symbol"
// - Starts User Data Stream for exchanges that support it (Binance)
func (m *Manager) StartStrategy(id string) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	redisBus := m.redisBus
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}

	inst.mu.Lock()
	if inst.Status == StatusRunning {
		inst.mu.Unlock()
		return nil
	}

	logger.Infof("[STRATEGY START] id=%s owner=%d name=%s path=%s", inst.ID, inst.OwnerID, inst.Name, inst.Path)

	if redisBus == nil {
		inst.mu.Unlock()
		rb, err := bus.NewRedisBusFromConfig()
		if err != nil {
			return fmt.Errorf("redis bus not available: %v (请确保已启动 Redis，并配置 REDIS_ENABLED=true / REDIS_ADDR)", err)
		}
		m.SetRedisBus(rb)
		m.mu.RLock()
		redisBus = m.redisBus
		m.mu.RUnlock()
		inst.mu.Lock()
		if inst.Status == StatusRunning {
			inst.mu.Unlock()
			return nil
		}
	}

	runCfg := make(map[string]interface{}, len(inst.Config)+4)
	for k, v := range inst.Config {
		runCfg[k] = v
	}
	debugOn := getBool(inst.Config["debug"])
	logTrace := getBool(inst.Config["log_trace"]) || debugOn
	if logTrace {
		runCfg["log_trace"] = true
		if _, ok := runCfg["log_every"]; !ok {
			runCfg["log_every"] = 1
		}
		if _, ok := runCfg["log_idle_sec"]; !ok {
			runCfg["log_idle_sec"] = 5
		}
		if _, ok := runCfg["log_rx"]; !ok {
			runCfg["log_rx"] = true
		}
		if _, ok := runCfg["log_decisions"]; !ok {
			runCfg["log_decisions"] = true
		}
	}
	runCfg["strategy_id"] = inst.ID
	runCfg["owner_id"] = inst.OwnerID
	rc := conf.C().Redis
	runCfg["redis_addr"] = rc.Addr
	runCfg["redis_password"] = rc.Password
	runCfg["redis_db"] = rc.DB
	runCfg["redis_prefix"] = rc.Prefix
	runCfg["use_redis"] = true
	runCfg["healthcheck"] = map[string]interface{}{
		"enabled":         true,
		"interval_sec":    5,
		"timeout_sec":     20,
		"ready_grace_sec": 30,
	}

	fixedSymbol := ""
	if raw, ok := inst.Config["symbol"].(string); ok {
		fixedSymbol = strings.TrimSpace(raw)
	}
	activeSymbol := fixedSymbol

	feedSymbols := parseSymbolsValue(inst.Config["symbols"])
	if len(feedSymbols) == 0 && strings.TrimSpace(activeSymbol) != "" {
		feedSymbols = []string{strings.TrimSpace(activeSymbol)}
	}

	selectMode := strings.ToLower(strings.TrimSpace(getString(inst.Config["symbol_select_mode"])))
	autoSymbols := getBool(inst.Config["auto_symbols"])
	minPrice := getNumber(inst.Config["min_price"])
	maxPrice := getNumber(inst.Config["max_price"])
	minPrecision := int(getNumber(inst.Config["min_precision"]))
	minVolatility := getNumber(inst.Config["min_volatility"])
	limit := int(getNumber(inst.Config["select_limit"]))
	if limit <= 0 {
		limit = 20
	}
	useFilter := strings.TrimSpace(activeSymbol) == "" && (selectMode == "filter" || autoSymbols || minPrice > 0 || maxPrice > 0 || minPrecision > 0 || minVolatility > 0)
	if useFilter {
		emitStrategyLog(inst, "info", fmt.Sprintf("Symbol select start mode=%s min_price=%v max_price=%v min_precision=%d min_volatility=%v limit=%d", selectMode, minPrice, maxPrice, minPrecision, minVolatility, limit))
		criteria := exchange.SymbolSelectCriteria{
			MinPrice:      minPrice,
			MaxPrice:      maxPrice,
			MinPrecision:  minPrecision,
			MinVolatility: minVolatility,
			Quote:         "USDT",
			Limit:         limit,
			OnlySymbols:   feedSymbols,
		}
		if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok {
			res, err := bx.SelectSymbolsDetailed(criteria)
			if err != nil {
				emitStrategyLog(inst, "error", fmt.Sprintf("Symbol select failed err=%v", err))
				if strings.TrimSpace(activeSymbol) == "" && len(feedSymbols) == 0 {
					inst.mu.Unlock()
					return fmt.Errorf("symbol select failed and no symbols configured")
				}
			} else if len(res.Selected) == 0 {
				emitStrategyLog(inst, "error", "Symbol select returned empty set")
				if strings.TrimSpace(activeSymbol) == "" && len(feedSymbols) == 0 {
					inst.mu.Unlock()
					return fmt.Errorf("symbol select returned empty set and no symbols configured")
				}
			} else {
				before := append([]string(nil), feedSymbols...)
				feedSymbols = res.Selected
				preview := strings.Join(feedSymbols, ",")
				if len(feedSymbols) > 10 {
					preview = strings.Join(feedSymbols[:10], ",") + ",..."
				}
				emitStrategyLog(inst, "info", fmt.Sprintf("Symbol select ok count=%d mode=%s symbols=%s", len(feedSymbols), selectMode, preview))
				if logTrace && len(before) > 0 && len(res.Rejected) > 0 {
					n := 0
					for _, s := range before {
						if reason, ok := res.Rejected[s]; ok {
							emitStrategyLog(inst, "info", fmt.Sprintf("Symbol filtered out symbol=%s reason=%s", s, reason))
							n++
							if n >= 20 {
								break
							}
						}
					}
				}
			}
		} else {
			emitStrategyLog(inst, "error", "Symbol select requires Binance exchange")
			if strings.TrimSpace(activeSymbol) == "" && len(feedSymbols) == 0 {
				inst.mu.Unlock()
				return fmt.Errorf("symbol select requires binance and no symbols configured")
			}
		}
	}
	if len(feedSymbols) > 0 {
		runCfg["symbols"] = feedSymbols
		if _, ok := runCfg["symbol"].(string); !ok || strings.TrimSpace(fmt.Sprintf("%v", runCfg["symbol"])) == "" {
			runCfg["symbol"] = feedSymbols[0]
		}
	}

	configJSON, _ := json.Marshal(runCfg)

	absPath, err := m.prepareRuntimeStrategyFile(inst)
	if err != nil {
		inst.mu.Unlock()
		return err
	}
	cmd := exec.Command("python3", absPath, string(configJSON))
	runDir := filepath.Dir(absPath)
	if inst.RuntimeGenerated {
		runDir = filepath.Dir(runDir)
	}
	cmd.Dir = runDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		inst.mu.Unlock()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		inst.mu.Unlock()
		return err
	}

	if err := cmd.Start(); err != nil {
		inst.mu.Unlock()
		return err
	}

	inst.cmd = cmd
	inst.stdout = stdout
	inst.Status = StatusRunning
	inst.feedSymbols = feedSymbols
	inst.resync = true
	inst.bootID = ""
	inst.startedAt = time.Now()
	inst.lastHB = time.Time{}
	inst.stopping = false
	inst.restarting = false
	inst.mu.Unlock()

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	candleCh := ""
	signalCh := ""
	stateCh := ""
	if redisBus != nil {
		candleCh = redisBus.CandleChannel(inst.ID)
		signalCh = redisBus.SignalChannel(inst.ID)
		stateCh = redisBus.StateChannel(inst.ID)
	}
	logger.Infof("[STRATEGY PROCESS] id=%s owner=%d pid=%d symbols=%d candle_ch=%s signal_ch=%s state_ch=%s", inst.ID, inst.OwnerID, pid, len(feedSymbols), candleCh, signalCh, stateCh)
	emitStrategyLog(inst, "info", fmt.Sprintf("Process started pid=%d symbols=%d candle_ch=%s signal_ch=%s state_ch=%s", pid, len(feedSymbols), candleCh, signalCh, stateCh))

	ctx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	inst.redisCancel = cancel
	inst.mu.Unlock()
	_ = redisBus.SubscribeSignals(ctx, inst.ID, func(s bus.SignalMessage) {
		if strings.TrimSpace(s.StrategyID) == "" {
			s.StrategyID = inst.ID
		}
		if s.StrategyID != inst.ID {
			return
		}
		m.handleRedisSignal(inst, s)
	})
	_ = redisBus.SubscribeState(ctx, inst.ID, func(st bus.StateMessage) {
		if st.StrategyID != inst.ID {
			return
		}
		if strings.TrimSpace(st.BootID) == "" {
			return
		}
		typ := strings.ToLower(strings.TrimSpace(st.Type))
		if logTrace {
			emitStrategyLog(inst, "info", fmt.Sprintf("State recv type=%s boot_id=%s", typ, st.BootID))
		}
		now := time.Now()
		inst.mu.Lock()
		changed := inst.bootID != st.BootID
		if changed || typ == "ready" {
			inst.bootID = st.BootID
			inst.resync = true
		}
		if typ == "ready" {
			if !inst.stateReadySeen {
				inst.stateReadySeen = true
				inst.mu.Unlock()
				emitStrategyLog(inst, "info", fmt.Sprintf("Strategy ready boot_id=%s", st.BootID))
				inst.mu.Lock()
			}
		}
		if typ == "heartbeat" {
			if !inst.heartbeatSeen {
				inst.heartbeatSeen = true
				inst.mu.Unlock()
				emitStrategyLog(inst, "info", fmt.Sprintf("Strategy heartbeat boot_id=%s", st.BootID))
				inst.mu.Lock()
			}
		}
		if typ == "ready" || typ == "heartbeat" {
			inst.lastHB = now
		}
		inst.mu.Unlock()
	})
	go func() {
		time.Sleep(10 * time.Second)
		inst.mu.Lock()
		seen := inst.stateReadySeen
		inst.mu.Unlock()
		if !seen {
			emitStrategyLog(inst, "info", "Waiting strategy ready (python state channel not received yet)")
		}
	}()
	go m.historySyncLoop(ctx, inst, redisBus)
	go m.healthMonitorLoop(ctx, inst)
	go m.waitProcessLoop(inst)

	if database.DB != nil {
		debugOn := false
		if raw, ok := inst.Config["debug"]; ok {
			if v, ok := raw.(bool); ok {
				debugOn = v
			} else if v, ok := raw.(float64); ok {
				debugOn = v != 0
			}
		}
		if debugOn {
			cfg := make(map[string]interface{}, len(inst.Config))
			for k, v := range inst.Config {
				cfg[k] = v
			}
			cfg["debug"] = false
			if b, err := json.Marshal(cfg); err == nil {
				_ = database.DB.Model(&models.StrategyInstance{}).Where("id = ?", inst.ID).
					Updates(map[string]interface{}{"config": string(b), "updated_at": time.Now()}).Error
				inst.Config["debug"] = false
			}
		}
	}

	if ex, ok := inst.exchange.(interface {
		EnsureUserDataStream(ownerID uint, hub *ws.Hub) error
	}); ok {
		_ = ex.EnsureUserDataStream(inst.OwnerID, inst.hub)
	}

	if len(feedSymbols) == 0 {
		logger.Warnf("[STRATEGY START WARN] id=%s owner=%d reason=no symbol in config", inst.ID, inst.OwnerID)
		database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      "error",
			Message:    "No symbol in config; strategy will not receive market data",
			CreatedAt:  time.Now(),
		})
		inst.hub.BroadcastJSON(map[string]interface{}{
			"type":        "error",
			"strategy_id": inst.ID,
			"owner_id":    inst.OwnerID,
			"error":       "No symbol in config; strategy will not receive market data",
		})
	} else {
		runCfg["symbols"] = feedSymbols
		if _, ok := runCfg["symbol"].(string); !ok || strings.TrimSpace(fmt.Sprintf("%v", runCfg["symbol"])) == "" {
			runCfg["symbol"] = feedSymbols[0]
		}

		go func(syms []string) {
			time.Sleep(20 * time.Second)
			inst.mu.Lock()
			rx := inst.candleRxCount
			inst.mu.Unlock()
			for _, s := range syms {
				n := 0
				if rx != nil {
					n = rx[s]
				}
				if n == 0 {
					emitStrategyLog(inst, "info", fmt.Sprintf("Waiting first closed kline symbol=%s (Binance kline only triggers on close)", s))
				}
			}
		}(append([]string(nil), feedSymbols...))

		for _, sym := range feedSymbols {
			sym := sym
			go func() {
				emitStrategyLog(inst, "info", fmt.Sprintf("SubscribeCandles start symbol=%s", sym))
				var (
					stop func()
					err  error
				)
				if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok {
					stop, err = bx.SubscribeCandlesWithEvents(sym, func(candle exchange.Candle) {
						inst.mu.Lock()
						if inst.candleRxCount == nil {
							inst.candleRxCount = map[string]int{}
						}
						inst.candleRxCount[sym]++
						if inst.lastCandleClose == nil {
							inst.lastCandleClose = map[string]float64{}
						}
						if inst.lastCandleAt == nil {
							inst.lastCandleAt = map[string]time.Time{}
						}
						inst.lastCandleClose[sym] = candle.Close
						inst.lastCandleAt[sym] = candle.Timestamp
						rxN := inst.candleRxCount[sym]
						inst.mu.Unlock()
						if rxN == 1 {
							emitStrategyLog(inst, "info", fmt.Sprintf("Exchange candle first symbol=%s ts=%s close=%v", sym, candle.Timestamp.Format(time.RFC3339), candle.Close))
						}

						payload := map[string]interface{}{
							"symbol":    sym,
							"timestamp": candle.Timestamp,
							"open":      candle.Open,
							"high":      candle.High,
							"low":       candle.Low,
							"close":     candle.Close,
							"volume":    candle.Volume,
						}
						pubErr := redisBus.PublishCandle(context.Background(), bus.CandleMessage{
							StrategyID: inst.ID,
							Symbol:     sym,
							Timestamp:  candle.Timestamp,
							Open:       candle.Open,
							High:       candle.High,
							Low:        candle.Low,
							Close:      candle.Close,
							Volume:     candle.Volume,
						})
						if pubErr != nil {
							logger.Errorf("[REDIS PUBLISH ERROR] id=%s owner=%d symbol=%s err=%v", inst.ID, inst.OwnerID, sym, pubErr)
							emitStrategyLog(inst, "error", fmt.Sprintf("Redis publish candle failed symbol=%s err=%v", sym, pubErr))
						} else {
							inst.mu.Lock()
							if inst.candlePubCount == nil {
								inst.candlePubCount = map[string]int{}
							}
							inst.candlePubCount[sym]++
							pubN := inst.candlePubCount[sym]
							inst.mu.Unlock()
							if pubN == 1 {
								emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish candle first ch=%s symbol=%s ts=%s close=%v", redisBus.CandleChannel(inst.ID), sym, candle.Timestamp.Format(time.RFC3339), candle.Close))
							}
							logRedis := getBool(inst.Config["log_redis"])
							logEvery := int(getNumber(inst.Config["log_candle_every"]))
							if logEvery <= 0 {
								logEvery = 60
							}
							if logRedis {
								if pubN%logEvery == 0 {
									emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish candle ok ch=%s symbol=%s ts=%s close=%v", redisBus.CandleChannel(inst.ID), sym, candle.Timestamp.Format(time.RFC3339), candle.Close))
								}
							}
						}
						inst.hub.BroadcastJSON(map[string]interface{}{
							"type":        "candle",
							"strategy_id": inst.ID,
							"owner_id":    inst.OwnerID,
							"data":        payload,
						})
					}, func(event string, detail string, err error) {
						switch event {
						case "dialing":
							emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS dialing symbol=%s url=%s", sym, detail))
						case "connected":
							emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS connected symbol=%s url=%s", sym, detail))
						case "connect_failed":
							emitStrategyLog(inst, "error", fmt.Sprintf("Binance WS connect failed symbol=%s url=%s err=%v", sym, detail, err))
						case "disconnected":
							emitStrategyLog(inst, "error", fmt.Sprintf("Binance WS disconnected symbol=%s url=%s err=%v", sym, detail, err))
						case "rx_raw_first":
							emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS recv first raw symbol=%s %s", sym, detail))
						case "rx_first":
							emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS recv first kline symbol=%s %s", sym, detail))
						case "rx_first_closed":
							emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS recv first closed kline symbol=%s %s", sym, detail))
						case "unmarshal_failed":
							emitStrategyLog(inst, "error", fmt.Sprintf("Binance WS unmarshal failed symbol=%s err=%s", sym, detail))
						default:
							emitStrategyLog(inst, "error", fmt.Sprintf("Binance WS unknown event symbol=%s event=%s detail=%s err=%v", sym, event, detail, err))
						}
					})
				} else {
					stop, err = inst.exchange.SubscribeCandles(sym, func(candle exchange.Candle) {
						inst.mu.Lock()
						if inst.candleRxCount == nil {
							inst.candleRxCount = map[string]int{}
						}
						inst.candleRxCount[sym]++
						if inst.lastCandleClose == nil {
							inst.lastCandleClose = map[string]float64{}
						}
						if inst.lastCandleAt == nil {
							inst.lastCandleAt = map[string]time.Time{}
						}
						inst.lastCandleClose[sym] = candle.Close
						inst.lastCandleAt[sym] = candle.Timestamp
						rxN := inst.candleRxCount[sym]
						inst.mu.Unlock()
						if rxN == 1 {
							emitStrategyLog(inst, "info", fmt.Sprintf("Exchange candle first symbol=%s ts=%s close=%v", sym, candle.Timestamp.Format(time.RFC3339), candle.Close))
						}

						payload := map[string]interface{}{
							"symbol":    sym,
							"timestamp": candle.Timestamp,
							"open":      candle.Open,
							"high":      candle.High,
							"low":       candle.Low,
							"close":     candle.Close,
							"volume":    candle.Volume,
						}
						pubErr := redisBus.PublishCandle(context.Background(), bus.CandleMessage{
							StrategyID: inst.ID,
							Symbol:     sym,
							Timestamp:  candle.Timestamp,
							Open:       candle.Open,
							High:       candle.High,
							Low:        candle.Low,
							Close:      candle.Close,
							Volume:     candle.Volume,
						})
						if pubErr != nil {
							logger.Errorf("[REDIS PUBLISH ERROR] id=%s owner=%d symbol=%s err=%v", inst.ID, inst.OwnerID, sym, pubErr)
							emitStrategyLog(inst, "error", fmt.Sprintf("Redis publish candle failed symbol=%s err=%v", sym, pubErr))
						} else {
							inst.mu.Lock()
							if inst.candlePubCount == nil {
								inst.candlePubCount = map[string]int{}
							}
							inst.candlePubCount[sym]++
							pubN := inst.candlePubCount[sym]
							inst.mu.Unlock()
							if pubN == 1 {
								emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish candle first ch=%s symbol=%s ts=%s close=%v", redisBus.CandleChannel(inst.ID), sym, candle.Timestamp.Format(time.RFC3339), candle.Close))
							}
							logRedis := getBool(inst.Config["log_redis"])
							logEvery := int(getNumber(inst.Config["log_candle_every"]))
							if logEvery <= 0 {
								logEvery = 60
							}
							if logRedis {
								if pubN%logEvery == 0 {
									emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish candle ok ch=%s symbol=%s ts=%s close=%v", redisBus.CandleChannel(inst.ID), sym, candle.Timestamp.Format(time.RFC3339), candle.Close))
								}
							}
						}
						inst.hub.BroadcastJSON(map[string]interface{}{
							"type":        "candle",
							"strategy_id": inst.ID,
							"owner_id":    inst.OwnerID,
							"data":        payload,
						})
					})
				}
				if err != nil {
					logger.Errorf("[STRATEGY SUBSCRIBE ERROR] id=%s owner=%d symbol=%s err=%v", inst.ID, inst.OwnerID, sym, err)
					database.DB.Create(&models.StrategyLog{
						StrategyID: inst.ID,
						Level:      "error",
						Message:    fmt.Sprintf("SubscribeCandles error: %v", err),
						CreatedAt:  time.Now(),
					})
					inst.hub.BroadcastJSON(map[string]interface{}{
						"type":        "error",
						"strategy_id": inst.ID,
						"owner_id":    inst.OwnerID,
						"error":       fmt.Sprintf("SubscribeCandles error: %v", err),
					})
					return
				}
				emitStrategyLog(inst, "info", fmt.Sprintf("SubscribeCandles ok symbol=%s", sym))
				if stop != nil {
					inst.mu.Lock()
					if inst.candleStops == nil {
						inst.candleStops = map[string]func(){}
					}
					if prev, ok := inst.candleStops[sym]; ok && prev != nil {
						prev()
					}
					inst.candleStops[sym] = stop
					inst.mu.Unlock()
				}
			}()
		}
	}

	go inst.readStdout()
	go inst.readStderr(stderr)
	return nil
}

func (m *Manager) prepareRuntimeStrategyFile(inst *StrategyInstance) (string, error) {
	if inst == nil {
		return "", fmt.Errorf("missing instance")
	}
	if database.DB == nil {
		return resolveStrategyPath(inst.Path)
	}

	var row models.StrategyInstance
	if err := database.DB.Preload("Template").Where("id = ?", inst.ID).First(&row).Error; err != nil {
		return resolveStrategyPath(inst.Path)
	}

	code := strings.TrimSpace(row.Template.Code)
	if code == "" {
		return resolveStrategyPath(inst.Path)
	}
	if p := strings.TrimSpace(row.Template.Path); p != "" && !strings.HasPrefix(strings.ToLower(p), "db://") {
		if abs, err := resolveStrategyPath(p); err == nil {
			if b, err := os.ReadFile(abs); err == nil {
				disk := strings.TrimSpace(string(b))
				if disk != "" && disk != code {
					code = disk
					_ = database.DB.Model(&models.StrategyTemplate{}).Where("id = ?", row.Template.ID).
						Updates(map[string]interface{}{"code": code, "updated_at": time.Now()}).Error
				}
			}
		}
	}

	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	absDir, err := filepath.Abs(strategiesDir)
	if err != nil {
		return "", err
	}
	runtimeDir := filepath.Join(absDir, "_runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return "", err
	}

	absPath := filepath.Join(runtimeDir, inst.ID+".py")
	runtimeCode := "import os\nimport sys\nsys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), \"..\")))\n\n" + miniRedisRuntimeShim() + "\n" + code + "\n"
	if err := os.WriteFile(absPath, []byte(runtimeCode), 0o644); err != nil {
		return "", err
	}

	keep := getBool(inst.Config["keep_runtime_file"]) || getBool(inst.Config["debug"]) || getBool(inst.Config["log_trace"])
	inst.RuntimePath = absPath
	inst.RuntimeGenerated = true
	inst.RuntimeKeep = keep
	inst.Path = absPath
	return absPath, nil
}

func miniRedisRuntimeShim() string {
	return "import socket\nimport types\n\nclass MiniRedis:\n    def __init__(self, host=\"127.0.0.1\", port=6379, password=\"\", db=0, timeout=30):\n        self.host = host\n        self.port = int(port)\n        self.password = password or \"\"\n        self.db = int(db or 0)\n        self.timeout = timeout\n        self.sock = None\n        self.buf = b\"\"\n\n    def connect(self):\n        self.sock = socket.create_connection((self.host, self.port), timeout=self.timeout if self.timeout else None)\n        if self.timeout:\n            self.sock.settimeout(self.timeout)\n        if self.password:\n            try:\n                self.execute(\"AUTH\", self.password)\n            except RuntimeError as e:\n                msg = str(e)\n                if \"called without any password configured\" not in msg:\n                    raise\n        if self.db:\n            self.execute(\"SELECT\", str(self.db))\n        return self\n\n    def close(self):\n        try:\n            if self.sock:\n                self.sock.close()\n        finally:\n            self.sock = None\n            self.buf = b\"\"\n\n    def _encode(self, *parts):\n        out = [f\"*{len(parts)}\\r\\n\".encode(\"utf-8\")]\n        for p in parts:\n            if p is None:\n                p = \"\"\n            if not isinstance(p, (bytes, bytearray)):\n                p = str(p).encode(\"utf-8\")\n            out.append(f\"${len(p)}\\r\\n\".encode(\"utf-8\"))\n            out.append(p)\n            out.append(b\"\\r\\n\")\n        return b\"\".join(out)\n\n    def _read_exact(self, n):\n        while len(self.buf) < n:\n            chunk = self.sock.recv(4096)\n            if not chunk:\n                raise ConnectionError(\"redis connection closed\")\n            self.buf += chunk\n        out, self.buf = self.buf[:n], self.buf[n:]\n        return out\n\n    def _read_line(self):\n        while b\"\\r\\n\" not in self.buf:\n            chunk = self.sock.recv(4096)\n            if not chunk:\n                raise ConnectionError(\"redis connection closed\")\n            self.buf += chunk\n        i = self.buf.index(b\"\\r\\n\")\n        line, self.buf = self.buf[:i], self.buf[i + 2 :]\n        return line\n\n    def _read_resp(self):\n        prefix = self._read_exact(1)\n        if prefix == b\"+\":\n            return self._read_line().decode(\"utf-8\", errors=\"replace\")\n        if prefix == b\"-\":\n            raise RuntimeError(self._read_line().decode(\"utf-8\", errors=\"replace\"))\n        if prefix == b\":\":\n            return int(self._read_line())\n        if prefix == b\"$\":\n            n = int(self._read_line())\n            if n == -1:\n                return None\n            data = self._read_exact(n)\n            _ = self._read_exact(2)\n            return data.decode(\"utf-8\", errors=\"replace\")\n        if prefix == b\"*\":\n            n = int(self._read_line())\n            if n == -1:\n                return None\n            return [self._read_resp() for _ in range(n)]\n        raise RuntimeError(f\"unknown RESP prefix: {prefix!r}\")\n\n    def execute(self, *args):\n        if not self.sock:\n            self.connect()\n        self.sock.sendall(self._encode(*args))\n        return self._read_resp()\n\n    def publish(self, channel, payload):\n        return self.execute(\"PUBLISH\", channel, payload)\n\n    def subscribe(self, channel):\n        return self.execute(\"SUBSCRIBE\", channel)\n\n    def read_pubsub_message(self):\n        try:\n            msg = self._read_resp()\n        except (TimeoutError, socket.timeout):\n            return None\n        if not isinstance(msg, list) or len(msg) < 3:\n            return None\n        if msg[0] != \"message\":\n            return None\n        return {\"channel\": msg[1], \"data\": msg[2]}\n\n_mod = types.ModuleType(\"mini_redis\")\n_mod.MiniRedis = MiniRedis\nsys.modules.setdefault(\"mini_redis\", _mod)\n"
}

func (m *Manager) prepareBacktestStrategyFile(inst *StrategyInstance, backtestID uint) (string, func(), error) {
	if inst == nil {
		return "", func() {}, fmt.Errorf("missing instance")
	}
	if database.DB == nil {
		absPath, err := resolveStrategyPath(inst.Path)
		return absPath, func() {}, err
	}

	var row models.StrategyInstance
	if err := database.DB.Preload("Template").Where("id = ?", inst.ID).First(&row).Error; err != nil {
		absPath, err2 := resolveStrategyPath(inst.Path)
		return absPath, func() {}, err2
	}

	code := strings.TrimSpace(row.Template.Code)
	if code == "" {
		absPath, err := resolveStrategyPath(inst.Path)
		return absPath, func() {}, err
	}
	if p := strings.TrimSpace(row.Template.Path); p != "" && !strings.HasPrefix(strings.ToLower(p), "db://") {
		if abs, err := resolveStrategyPath(p); err == nil {
			if b, err := os.ReadFile(abs); err == nil {
				disk := strings.TrimSpace(string(b))
				if disk != "" && disk != code {
					code = disk
					_ = database.DB.Model(&models.StrategyTemplate{}).Where("id = ?", row.Template.ID).
						Updates(map[string]interface{}{"code": code, "updated_at": time.Now()}).Error
				}
			}
		}
	}

	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	absDir, err := filepath.Abs(strategiesDir)
	if err != nil {
		return "", func() {}, err
	}
	runtimeDir := filepath.Join(absDir, "_runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return "", func() {}, err
	}

	tmp := filepath.Join(runtimeDir, fmt.Sprintf("backtest_%d_%s.py", backtestID, inst.ID))
	runtimeCode := "import os\nimport sys\nsys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), \"..\")))\n\n" + miniRedisRuntimeShim() + "\n" + code + "\n"
	if err := os.WriteFile(tmp, []byte(runtimeCode), 0o644); err != nil {
		return "", func() {}, err
	}
	return tmp, func() { _ = os.Remove(tmp) }, nil
}

func applyOrderFillToPosition(hub *ws.Hub, ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, executedQty float64, avgPrice float64, takeProfit float64, stopLoss float64, eventTime time.Time) {
	if database.DB == nil || executedQty <= 0 || strategyID == "" {
		return
	}

	now := time.Now()
	var pos models.StrategyPosition
	err := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", ownerID, strategyID, symbol, "open").First(&pos).Error
	if err != nil {
		if side != "buy" {
			return
		}
		pos = models.StrategyPosition{
			StrategyID:       strategyID,
			StrategyName:     strategyName,
			OwnerID:          ownerID,
			Exchange:         exchangeName,
			Symbol:           symbol,
			Amount:           executedQty,
			AvgPrice:         avgPrice,
			TakeProfit:       takeProfit,
			StopLoss:         stopLoss,
			ClosedQty:        0,
			AvgClosePrice:    0,
			RealizedPnL:      0,
			RealizedNotional: 0,
			Status:           "open",
			OpenTime:         eventTime,
			UpdatedAt:        now,
		}
		database.DB.Create(&pos)
		if hub != nil {
			hub.BroadcastJSON(map[string]interface{}{"type": "position", "data": pos})
		}
		return
	}

	if side == "buy" {
		newAmt := pos.Amount + executedQty
		newAvg := pos.AvgPrice
		if newAmt > 0 {
			newAvg = ((pos.AvgPrice * pos.Amount) + (avgPrice * executedQty)) / newAmt
		}
		upd := map[string]interface{}{"amount": newAmt, "avg_price": newAvg, "updated_at": now}
		if takeProfit > 0 {
			upd["take_profit"] = takeProfit
		}
		if stopLoss > 0 {
			upd["stop_loss"] = stopLoss
		}
		database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).Updates(upd)
		if hub != nil {
			pos.Amount = newAmt
			pos.AvgPrice = newAvg
			if takeProfit > 0 {
				pos.TakeProfit = takeProfit
			}
			if stopLoss > 0 {
				pos.StopLoss = stopLoss
			}
			hub.BroadcastJSON(map[string]interface{}{"type": "position", "data": pos})
		}
		return
	}

	if side == "sell" {
		newAmt := pos.Amount - executedQty
		realized := executedQty * (avgPrice - pos.AvgPrice)
		newRealizedPnL := pos.RealizedPnL + realized
		newRealizedNotional := pos.RealizedNotional + (executedQty * pos.AvgPrice)
		newClosedQty := pos.ClosedQty + executedQty
		newAvgClose := pos.AvgClosePrice
		if newClosedQty > 0 {
			newAvgClose = ((pos.AvgClosePrice * pos.ClosedQty) + (avgPrice * executedQty)) / newClosedQty
		}
		if newAmt <= 0 {
			database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).
				Updates(map[string]interface{}{
					"amount":            0,
					"closed_qty":        newClosedQty,
					"avg_close_price":   newAvgClose,
					"realized_pn_l":     newRealizedPnL,
					"realized_notional": newRealizedNotional,
					"status":            "closed",
					"close_time":        eventTime,
					"updated_at":        now,
				})
			if hub != nil {
				pos.Amount = 0
				pos.ClosedQty = newClosedQty
				pos.AvgClosePrice = newAvgClose
				pos.RealizedPnL = newRealizedPnL
				pos.RealizedNotional = newRealizedNotional
				pos.Status = "closed"
				pos.CloseTime = eventTime
				hub.BroadcastJSON(map[string]interface{}{"type": "position", "data": pos})
			}
			return
		}
		database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).
			Updates(map[string]interface{}{"amount": newAmt, "closed_qty": newClosedQty, "avg_close_price": newAvgClose, "realized_pn_l": newRealizedPnL, "realized_notional": newRealizedNotional, "updated_at": now})
		if hub != nil {
			pos.Amount = newAmt
			pos.ClosedQty = newClosedQty
			pos.AvgClosePrice = newAvgClose
			pos.RealizedPnL = newRealizedPnL
			pos.RealizedNotional = newRealizedNotional
			hub.BroadcastJSON(map[string]interface{}{"type": "position", "data": pos})
		}
	}
}

func (inst *StrategyInstance) readStderr(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	traceLines := make([]string, 0, 64)
	inTrace := false
	flush := func() {
		if len(traceLines) == 0 {
			inTrace = false
			return
		}
		msg := strings.Join(traceLines, "\n")
		traceLines = traceLines[:0]
		inTrace = false
		logger.Errorf("[%s ERROR] %s", inst.Name, msg)
		_ = database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      "error",
			Message:    msg,
			CreatedAt:  time.Now(),
		}).Error
		inst.hub.BroadcastJSON(map[string]interface{}{
			"type":        "error",
			"strategy_id": inst.ID,
			"owner_id":    inst.OwnerID,
			"error":       msg,
		})
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Traceback (most recent call last):") {
			flush()
			inTrace = true
			traceLines = append(traceLines, line)
			continue
		}
		if inTrace {
			traceLines = append(traceLines, line)
			if strings.HasPrefix(line, "During handling of the above exception") {
				continue
			}
			if strings.HasPrefix(line, "The above exception") {
				continue
			}
			if strings.HasPrefix(line, "  File ") || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") || strings.TrimSpace(line) == "" {
				continue
			}
			if strings.Contains(line, ":") {
				flush()
			}
			continue
		}
		traceLines = append(traceLines, line)
		flush()
	}
	flush()
}

// StopStrategy stops a running python strategy process.
func (m *Manager) StopStrategy(id string, force bool) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}

	if !force {
		var openCount int64
		database.DB.Model(&models.StrategyPosition{}).
			Where("owner_id = ? AND strategy_id = ? AND status = ?", inst.OwnerID, inst.ID, "open").
			Count(&openCount)
		if openCount > 0 {
			return fmt.Errorf("strategy has open positions; close positions before stopping")
		}
		if bx, ok := m.exchange.(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
			syms := parseSymbolsValue(inst.Config["symbols"])
			if len(syms) == 0 {
				if sym, ok := inst.Config["symbol"].(string); ok && strings.TrimSpace(sym) != "" {
					syms = []string{strings.TrimSpace(sym)}
				}
			}
			if len(syms) > 0 {
				exPos, err := bx.FetchPositions(inst.OwnerID, "active")
				if err == nil {
					want := map[string]struct{}{}
					for _, s := range syms {
						want[exchange.NormalizeSymbol(s)] = struct{}{}
					}
					for _, p := range exPos {
						if _, ok := want[exchange.NormalizeSymbol(p.Symbol)]; ok && p.Amount > 0 {
							return fmt.Errorf("strategy has open positions on exchange; close positions before stopping")
						}
					}
				}
			}
		}
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.Status != StatusRunning {
		return nil
	}
	inst.stopping = true

	if inst.redisCancel != nil {
		inst.redisCancel()
		inst.redisCancel = nil
	}
	if len(inst.candleStops) > 0 {
		for _, stop := range inst.candleStops {
			if stop != nil {
				stop()
			}
		}
		inst.candleStops = nil
	}

	if err := inst.cmd.Process.Kill(); err != nil {
		return err
	}

	inst.Status = StatusStopped
	return nil
}

func (m *Manager) historySyncLoop(ctx context.Context, inst *StrategyInstance, redisBus *bus.RedisBus) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			inst.mu.Lock()
			need := inst.resync
			bootID := inst.bootID
			symbols := append([]string(nil), inst.feedSymbols...)
			nextAt := inst.resyncNextAt
			inst.mu.Unlock()

			if !need || strings.TrimSpace(bootID) == "" || len(symbols) == 0 {
				continue
			}
			if !nextAt.IsZero() && time.Now().Before(nextAt) {
				continue
			}

			if getBool(inst.Config["log_redis"]) {
				inst.mu.Lock()
				if inst.resyncLogBootID != bootID {
					inst.resyncLogBootID = bootID
					inst.mu.Unlock()
					emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish history start ch=%s boot_id=%s symbols=%d", redisBus.CandleChannel(inst.ID), bootID, len(symbols)))
				} else {
					inst.mu.Unlock()
				}
			}

			ok := true
			historyBars := 200
			for _, sym := range symbols {
				candles, err := inst.exchange.FetchCandles(sym, "1m", historyBars)
				if err != nil || len(candles) == 0 {
					ok = false
					emitStrategyLog(inst, "error", fmt.Sprintf("FetchCandles failed for history symbol=%s err=%v", sym, err))
					continue
				}
				out := make([]bus.CandleMessage, 0, len(candles))
				for _, c := range candles {
					out = append(out, bus.CandleMessage{
						Type:       "candle",
						StrategyID: inst.ID,
						Symbol:     sym,
						Timestamp:  c.Timestamp,
						Open:       c.Open,
						High:       c.High,
						Low:        c.Low,
						Close:      c.Close,
						Volume:     c.Volume,
					})
				}
				if err := redisBus.PublishHistory(context.Background(), inst.ID, sym, out); err != nil {
					ok = false
					logger.Errorf("[REDIS PUBLISH ERROR] id=%s owner=%d symbol=%s type=history err=%v", inst.ID, inst.OwnerID, sym, err)
					emitStrategyLog(inst, "error", fmt.Sprintf("Redis publish history failed symbol=%s err=%v", sym, err))
				} else if getBool(inst.Config["log_redis"]) {
					emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish history ok symbol=%s bars=%d", sym, len(out)))
				}
			}
			if ok {
				inst.mu.Lock()
				inst.resync = false
				inst.resyncLogBootID = ""
				inst.resyncNextAt = time.Time{}
				inst.resyncBackoff = 0
				inst.mu.Unlock()
				if getBool(inst.Config["log_redis"]) {
					emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish history done boot_id=%s", bootID))
				}
			} else {
				inst.mu.Lock()
				b := inst.resyncBackoff
				if b <= 0 {
					b = 2 * time.Second
				} else {
					b = b * 2
				}
				if b > 60*time.Second {
					b = 60 * time.Second
				}
				inst.resyncBackoff = b
				inst.resyncNextAt = time.Now().Add(b)
				inst.mu.Unlock()
			}
		}
	}
}

func (m *Manager) healthMonitorLoop(ctx context.Context, inst *StrategyInstance) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			inst.mu.Lock()
			if inst.Status != StatusRunning || inst.stopping {
				inst.mu.Unlock()
				return
			}
			startedAt := inst.startedAt
			bootID := inst.bootID
			lastHB := inst.lastHB
			restarting := inst.restarting
			inst.mu.Unlock()

			if restarting {
				continue
			}

			now := time.Now()
			readyGrace := 30 * time.Second
			hbTimeout := 20 * time.Second

			if strings.TrimSpace(bootID) == "" {
				if !startedAt.IsZero() && now.Sub(startedAt) > readyGrace {
					m.requestRestart(inst, "no_ready")
				}
				continue
			}
			if lastHB.IsZero() {
				continue
			}
			if now.Sub(lastHB) > hbTimeout {
				m.requestRestart(inst, "heartbeat_timeout")
			}
		}
	}
}

func (m *Manager) waitProcessLoop(inst *StrategyInstance) {
	inst.mu.Lock()
	cmd := inst.cmd
	inst.mu.Unlock()
	if cmd == nil {
		return
	}
	err := cmd.Wait()

	inst.mu.Lock()
	runtimePath := inst.RuntimePath
	runtimeGenerated := inst.RuntimeGenerated
	runtimeKeep := inst.RuntimeKeep
	inst.RuntimePath = ""
	inst.RuntimeGenerated = false
	inst.RuntimeKeep = false
	inst.mu.Unlock()
	if err != nil && runtimeGenerated && strings.TrimSpace(runtimePath) != "" {
		runtimeKeep = true
		emitStrategyLog(inst, "error", fmt.Sprintf("Strategy crashed; runtime script kept path=%s", runtimePath))
	}
	if runtimeGenerated && !runtimeKeep && strings.TrimSpace(runtimePath) != "" {
		_ = os.Remove(runtimePath)
	}

	inst.mu.Lock()
	stopping := inst.stopping
	running := inst.Status == StatusRunning
	if running {
		inst.Status = StatusError
	}
	inst.mu.Unlock()

	if stopping {
		return
	}
	if err != nil {
		logger.Errorf("[STRATEGY EXIT] id=%s owner=%d err=%v", inst.ID, inst.OwnerID, err)
	} else {
		logger.Errorf("[STRATEGY EXIT] id=%s owner=%d", inst.ID, inst.OwnerID)
	}
	_ = database.DB.Create(&models.StrategyLog{
		StrategyID: inst.ID,
		Level:      "error",
		Message:    fmt.Sprintf("Strategy process exited: %v", err),
		CreatedAt:  time.Now(),
	}).Error
	inst.hub.BroadcastJSON(map[string]interface{}{
		"type":        "error",
		"strategy_id": inst.ID,
		"owner_id":    inst.OwnerID,
		"error":       "Strategy process exited",
	})
	m.requestRestart(inst, "process_exited")
}

func (m *Manager) requestRestart(inst *StrategyInstance, reason string) {
	if inst == nil {
		return
	}
	inst.mu.Lock()
	if inst.restarting || inst.stopping || inst.Status != StatusRunning {
		inst.mu.Unlock()
		return
	}
	inst.restarting = true
	id := inst.ID
	ownerID := inst.OwnerID
	inst.mu.Unlock()

	logger.Errorf("[STRATEGY HEALTH] id=%s owner=%d action=restart reason=%s", id, ownerID, reason)
	_ = database.DB.Create(&models.StrategyLog{
		StrategyID: id,
		Level:      "error",
		Message:    fmt.Sprintf("Healthcheck restart: %s", reason),
		CreatedAt:  time.Now(),
	}).Error
	m.hub.BroadcastJSON(map[string]interface{}{
		"type":        "error",
		"strategy_id": id,
		"owner_id":    ownerID,
		"error":       fmt.Sprintf("Healthcheck restart: %s", reason),
	})

	go func() {
		_ = m.StopStrategy(id, true)
		time.Sleep(2 * time.Second)
		if err := m.StartStrategy(id); err != nil {
			logger.Errorf("[STRATEGY HEALTH] id=%s owner=%d action=restart_failed reason=%s err=%v", id, ownerID, reason, err)
			inst.mu.Lock()
			inst.restarting = false
			inst.mu.Unlock()
		}
	}()
}

func (m *Manager) handleRedisSignal(inst *StrategyInstance, s bus.SignalMessage) {
	if inst == nil {
		return
	}
	logSignal := getBool(inst.Config["log_signal"]) || getBool(inst.Config["log_redis"]) || getBool(inst.Config["debug"])
	symbol := strings.TrimSpace(s.Symbol)
	if symbol == "" {
		if logSignal {
			emitStrategyLog(inst, "info", "Skip signal: empty symbol")
		}
		return
	}
	if !isAllowedSymbol(inst, symbol) {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: symbol not allowed symbol=%s", symbol))
		}
		return
	}
	action := strings.ToLower(strings.TrimSpace(s.Action))
	if action == "" {
		action = "open"
	}
	if action != "open" {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: unsupported action=%s", action))
		}
		return
	}
	side := strings.ToLower(strings.TrimSpace(s.Side))
	if side == "long" {
		side = "buy"
	} else if side == "short" {
		side = "sell"
	} else if side == "auto" || side == "both" {
		side = ""
	}
	if side == "" {
		side = strings.ToLower(strings.TrimSpace(getString(inst.Config["default_open_side"])))
		if side == "" {
			side = "buy"
		}
		if side == "long" {
			side = "buy"
		} else if side == "short" {
			side = "sell"
		}
	}
	if side != "buy" && side != "sell" {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: invalid side=%s", side))
		}
		return
	}
	amount := clampOrderAmount(inst, s.Amount)
	if amount <= 0 {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: amount invalid raw=%v", s.Amount))
		}
		return
	}
	if !(s.TakeProfit > 0 && s.StopLoss > 0) {
		emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: 缺少止盈止损，拒绝开仓 symbol=%s side=%s amount=%v tp=%v sl=%v", symbol, side, amount, s.TakeProfit, s.StopLoss))
		return
	}
	if logSignal {
		emitStrategyLog(inst, "info", fmt.Sprintf("Recv signal: symbol=%s side=%s amount=%v tp=%v sl=%v signal_id=%s", symbol, side, amount, s.TakeProfit, s.StopLoss, strings.TrimSpace(s.SignalID)))
	}
	m.placeOrderForInstance(inst, symbol, side, amount, 0, s.TakeProfit, s.StopLoss, strings.TrimSpace(s.SignalID))
}

func (m *Manager) placeOrderForInstance(inst *StrategyInstance, symbol string, side string, amount float64, price float64, takeProfit float64, stopLoss float64, signalID string) {
	if inst == nil {
		return
	}
	// 强制使用市价单
	price = 0
	if !isAllowedSymbol(inst, symbol) {
		inst.orderMu.Lock()
		if inst.lastSkipLogAt == nil {
			inst.lastSkipLogAt = map[string]time.Time{}
		}
		k := "not_allowed_symbol:" + exchange.NormalizeSymbol(symbol)
		if t, ok := inst.lastSkipLogAt[k]; ok && time.Since(t) < 60*time.Second {
			inst.orderMu.Unlock()
			return
		}
		inst.lastSkipLogAt[k] = time.Now()
		inst.orderMu.Unlock()
		emitStrategyLog(inst, "info", fmt.Sprintf("Skip order: symbol not allowed symbol=%s", symbol))
		return
	}
	normalizedSide := strings.ToLower(strings.TrimSpace(side))
	if normalizedSide == "long" {
		normalizedSide = "buy"
	} else if normalizedSide == "short" {
		normalizedSide = "sell"
	}
	if normalizedSide != "buy" && normalizedSide != "sell" {
		return
	}
	amount = clampOrderAmount(inst, amount)
	if amount <= 0 {
		return
	}

	if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		lev := int(getNumber(inst.Config["leverage"]))
		if lev <= 0 {
			lev = 1
		}
		_ = bx.SetLeverage(inst.OwnerID, symbol, lev)

		mode := strings.ToLower(strings.TrimSpace(getString(inst.Config["order_amount_mode"])))
		if mode == "" {
			mode = "notional"
		}
		minNotional := getNumber(inst.Config["min_order_notional"])
		if minNotional <= 0 {
			minNotional = 5
		}

		getPx := func() (float64, error) {
			if price > 0 {
				return price, nil
			}
			inst.mu.Lock()
			px := 0.0
			if inst.lastCandleClose != nil {
				px = inst.lastCandleClose[symbol]
			}
			inst.mu.Unlock()
			if px > 0 {
				return px, nil
			}
			return bx.LastPrice(symbol)
		}

		avail := 0.0
		if v, err := bx.USDMAvailableUSDT(inst.OwnerID); err == nil && v > 0 {
			avail = v
		}

		maxNotional := 0.0
		if avail > 0 {
			maxNotional = avail * float64(lev) * 0.95
		}
		// Apply symbol-specific leverage bracket cap
		if capN, err := bx.USDMMaxNotionalForLeverage(inst.OwnerID, symbol, lev); err == nil && capN > 0 {
			// Subtract current position notional to get remaining capacity under bracket
			if posAmt, _, markPx, e2 := bx.USDMPositionAmt(inst.OwnerID, symbol); e2 == nil && markPx > 0 && posAmt != 0 {
				curN := math.Abs(posAmt) * markPx
				rem := capN - curN
				if rem < 0 {
					rem = 0
				}
				capN = rem
			}
			if maxNotional == 0 || capN < maxNotional {
				maxNotional = capN
			}
		}

		if mode == "notional" {
			px, err := getPx()
			if err != nil || px <= 0 {
				emitStrategyLog(inst, "error", fmt.Sprintf("Skip order: 获取价格失败 symbol=%s err=%v", symbol, err))
				return
			}
			notional := amount
			if notional < minNotional {
				notional = minNotional
			}
			if maxNotional > 0 {
				if maxNotional < minNotional {
					emitStrategyLog(inst, "info", fmt.Sprintf("Skip order: 保证金不足(最低下单额) symbol=%s avail=%0.4f lev=%d min_notional=%0.2f", symbol, avail, lev, minNotional))
					return
				}
				if notional > maxNotional {
					notional = maxNotional
				}
			}
			amount = notional / px
		} else {
			px, err := getPx()
			if err != nil || px <= 0 {
				emitStrategyLog(inst, "error", fmt.Sprintf("Skip order: 获取价格失败 symbol=%s err=%v", symbol, err))
				return
			}
			notional := amount * px
			if notional < minNotional {
				amount = minNotional / px
				notional = minNotional
			}
			if maxNotional > 0 {
				if maxNotional < minNotional {
					emitStrategyLog(inst, "info", fmt.Sprintf("Skip order: 保证金不足(最低下单额) symbol=%s avail=%0.4f lev=%d min_notional=%0.2f", symbol, avail, lev, minNotional))
					return
				}
				if notional > maxNotional {
					amount = maxNotional / px
				}
			}
		}
		amount = clampOrderAmount(inst, amount)
		if amount <= 0 {
			return
		}
		if px, err := getPx(); err == nil && px > 0 {
			if amount*px < minNotional {
				emitStrategyLog(inst, "info", fmt.Sprintf("Skip order: notional too small symbol=%s notional=%0.4f min_notional=%0.2f", symbol, amount*px, minNotional))
				return
			}
		}
	}

	maxPos := 1
	if v, ok := inst.Config["max_concurrent_positions"].(float64); ok && int(v) > 0 {
		maxPos = int(v)
	}

	var rb *bus.RedisBus
	if inst.mgr != nil {
		inst.mgr.mu.RLock()
		rb = inst.mgr.redisBus
		inst.mgr.mu.RUnlock()
	}

	exOpenCount := int64(0)
	exSymbolOpen := false
	exSymbolKey := exchange.NormalizeSymbol(symbol)

	inst.orderMu.Lock()
	if inst.invalidSymbol != nil {
		if until, ok := inst.invalidSymbol[exSymbolKey]; ok && time.Now().Before(until) {
			if inst.lastSkipLogAt == nil {
				inst.lastSkipLogAt = map[string]time.Time{}
			}
			k := "invalid_symbol:" + exSymbolKey
			if t, ok := inst.lastSkipLogAt[k]; !ok || time.Since(t) >= 60*time.Second {
				inst.lastSkipLogAt[k] = time.Now()
				inst.orderMu.Unlock()
				emitStrategyLog(inst, "error", fmt.Sprintf("Skip order: symbol not supported in current market symbol=%s", symbol))
				return
			}
			inst.orderMu.Unlock()
			return
		}
	}
	inst.orderMu.Unlock()

	if ps, err := inst.exchange.FetchPositions(inst.OwnerID, "active"); err == nil {
		for _, p := range ps {
			if math.Abs(p.Amount) <= 0 {
				continue
			}
			if exchange.NormalizeSymbol(p.Symbol) == exSymbolKey {
				exSymbolOpen = true
			}
			if !isAllowedSymbol(inst, p.Symbol) {
				continue
			}
			exOpenCount++
		}
	}
	if exSymbolOpen {
		inst.orderMu.Lock()
		if inst.lastSkipLogAt == nil {
			inst.lastSkipLogAt = map[string]time.Time{}
		}
		k := "has_pos:" + exSymbolKey
		if t, ok := inst.lastSkipLogAt[k]; ok && time.Since(t) < 10*time.Second {
			inst.orderMu.Unlock()
			return
		}
		inst.lastSkipLogAt[k] = time.Now()
		inst.orderMu.Unlock()
		emitStrategyLog(inst, "info", fmt.Sprintf("Skip order: symbol already has position symbol=%s", symbol))
		return
	}

	acquiredSlot := false
	if maxPos > 0 && rb != nil {
		_ = rb.SetOpenCount(context.Background(), inst.ID, exOpenCount, 6*time.Hour)
		ok, _, err := rb.AcquireOpenSlot(context.Background(), inst.ID, maxPos, 6*time.Hour)
		if err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("Skip order: redis acquire failed err=%v", err))
			return
		}
		if !ok {
			inst.orderMu.Lock()
			if inst.lastSkipLogAt == nil {
				inst.lastSkipLogAt = map[string]time.Time{}
			}
			k := "max_pos"
			if t, ok := inst.lastSkipLogAt[k]; ok && time.Since(t) < 10*time.Second {
				inst.orderMu.Unlock()
				return
			}
			inst.lastSkipLogAt[k] = time.Now()
			inst.orderMu.Unlock()
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip order: max_concurrent_positions reached strategy=%s symbol=%s open=%d max=%d", inst.ID, symbol, exOpenCount, maxPos))
			return
		}
		acquiredSlot = true
	}

	clientOrderID := models.GenerateUUID()
	database.DB.Create(&models.StrategyOrder{
		StrategyID:   inst.ID,
		StrategyName: inst.Name,
		OwnerID:      inst.OwnerID,
		Exchange:     inst.exchange.GetName(),
		Symbol:       symbol,
		Side:         normalizedSide,
		OrderType: func() string {
			if price > 0 {
				return "limit"
			}
			return "market"
		}(),
		ClientOrderID: clientOrderID,
		Status:        "requested",
		RequestedQty:  amount,
		Price:         price,
		RequestedAt:   time.Now(),
		UpdatedAt:     time.Now(),
	})

	order, err := inst.exchange.PlaceOrder(inst.OwnerID, clientOrderID, symbol, normalizedSide, amount, price)
	if err != nil {
		if acquiredSlot && rb != nil {
			_, _ = rb.ReleaseOpenSlot(context.Background(), inst.ID)
		}

		errMsg := fmt.Sprintf("Failed to place order: %v", err)
		if strings.Contains(errMsg, "\"code\":-2019") {
			errMsg = fmt.Sprintf("Failed to place order: 保证金不足 (%v)", err)
		} else if strings.Contains(errMsg, "\"code\":-4164") {
			errMsg = fmt.Sprintf("Failed to place order: notional 小于最小下单额 (%v)", err)
		} else if strings.Contains(errMsg, "\"code\":-2027") {
			lev := int(getNumber(inst.Config["leverage"]))
			if lev <= 0 {
				lev = 1
			}
			errMsg = fmt.Sprintf("Failed to place order: 当前杠杆下持仓上限超出，请降低杠杆或下单金额(数量) lev=%d (%v)", lev, err)
		} else if strings.Contains(errMsg, "\"code\":-1003") {
			errMsg = fmt.Sprintf("Failed to place order: IP 限流/封禁 (%v)", err)
		} else if strings.Contains(errMsg, "symbol not found:") {
			inst.orderMu.Lock()
			if inst.invalidSymbol == nil {
				inst.invalidSymbol = map[string]time.Time{}
			}
			inst.invalidSymbol[exSymbolKey] = time.Now().Add(10 * time.Minute)
			if inst.lastSkipLogAt == nil {
				inst.lastSkipLogAt = map[string]time.Time{}
			}
			k := "symbol_not_found_err:" + exSymbolKey
			if t, ok := inst.lastSkipLogAt[k]; ok && time.Since(t) < 10*time.Second {
				inst.orderMu.Unlock()
				database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
					Updates(map[string]interface{}{"status": "failed", "updated_at": time.Now()})
				return
			}
			inst.lastSkipLogAt[k] = time.Now()
			inst.orderMu.Unlock()
			errMsg = fmt.Sprintf("Failed to place order: symbol not supported in current market, will skip for 10m (%v)", err)
		}

		database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
			Updates(map[string]interface{}{"status": "failed", "updated_at": time.Now()})
		database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      "error",
			Message:    errMsg,
			CreatedAt:  time.Now(),
		})
		inst.hub.BroadcastJSON(map[string]interface{}{
			"type":        "error",
			"strategy_id": inst.ID,
			"owner_id":    inst.OwnerID,
			"error":       errMsg,
		})
		return
	}

	database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
		Updates(map[string]interface{}{
			"exchange_order_id": order.ID,
			"status":            order.Status,
			"executed_qty":      order.Amount,
			"avg_price":         order.Price,
			"updated_at":        time.Now(),
		})
	inst.hub.BroadcastJSON(map[string]interface{}{"type": "order", "data": order})

	if strings.ToLower(order.Status) == "filled" {
		applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), symbol, normalizedSide, order.Amount, order.Price, takeProfit, stopLoss, order.Timestamp)
	}

	if takeProfit > 0 || stopLoss > 0 {
		go m.tryPlaceExchangeTPStop(inst, symbol, takeProfit, stopLoss, clientOrderID, signalID)
	}
}

func (m *Manager) tryPlaceExchangeTPStop(inst *StrategyInstance, symbol string, takeProfit float64, stopLoss float64, baseClientOrderID string, signalID string) {
	if inst == nil {
		return
	}
	useExchange := true
	if v, ok := inst.Config["use_exchange_tpsl"]; ok {
		useExchange = getBool(v)
	}
	if useExchange {
		if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
			var lastErr error
			for i := 0; i < 30; i++ {
				amt, entryPx, _, err := bx.USDMPositionAmt(inst.OwnerID, symbol)
				if err == nil && amt != 0 {
					if entryPx > 0 && stopLoss > 0 {
						if amt > 0 {
							// long: SL in [entry*0.7, entry]
							minSL := entryPx * 0.7
							maxSL := entryPx
							if stopLoss < minSL {
								stopLoss = minSL
							} else if stopLoss > maxSL {
								stopLoss = maxSL
							}
						} else {
							// short: SL in [entry, entry*1.3]
							minSL := entryPx
							maxSL := entryPx * 1.3
							if stopLoss < minSL {
								stopLoss = minSL
							} else if stopLoss > maxSL {
								stopLoss = maxSL
							}
						}
					}
					lastErr = bx.PlaceUSDMTPStopOrders(inst.OwnerID, baseClientOrderID, symbol, takeProfit, stopLoss)
					if lastErr == nil {
						emitStrategyLog(inst, "info", fmt.Sprintf("已设置止盈止损 symbol=%s tp=%v sl=%v", symbol, takeProfit, stopLoss))
						return
					}
					break
				}
				lastErr = err
				time.Sleep(500 * time.Millisecond)
			}
			level := "error"
			if lastErr != nil {
				msg := lastErr.Error()
				if strings.Contains(msg, "\"code\":-2021") || strings.Contains(msg, "immediately trigger") {
					level = "info"
				}
			}
			emitStrategyLog(inst, level, fmt.Sprintf("设置止盈止损失败，回退为本地监控 symbol=%s tp=%v sl=%v err=%v", symbol, takeProfit, stopLoss, lastErr))
			m.monitorPositionTPStop(inst, symbol, takeProfit, stopLoss, signalID)
			return
		}
	}
	m.monitorPositionTPStop(inst, symbol, takeProfit, stopLoss, signalID)
}

func (m *Manager) monitorPositionTPStop(inst *StrategyInstance, symbol string, takeProfit float64, stopLoss float64, signalID string) {
	if inst == nil {
		return
	}
	sym := strings.TrimSpace(symbol)
	if sym == "" {
		return
	}
	isShort := false
	if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		if amt, entryPx, _, err := bx.USDMPositionAmt(inst.OwnerID, sym); err == nil {
			if amt < 0 {
				isShort = true
			}
			if entryPx > 0 && stopLoss > 0 {
				if !isShort {
					minSL := entryPx * 0.7
					maxSL := entryPx
					if stopLoss < minSL {
						stopLoss = minSL
					} else if stopLoss > maxSL {
						stopLoss = maxSL
					}
				} else {
					minSL := entryPx
					maxSL := entryPx * 1.3
					if stopLoss < minSL {
						stopLoss = minSL
					} else if stopLoss > maxSL {
						stopLoss = maxSL
					}
				}
			}
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		inst.mu.Lock()
		px := 0.0
		if inst.lastCandleClose != nil {
			px = inst.lastCandleClose[sym]
		}
		inst.mu.Unlock()
		if px <= 0 {
			continue
		}
		hitTP := false
		hitSL := false
		if !isShort {
			hitTP = takeProfit > 0 && px >= takeProfit
			hitSL = stopLoss > 0 && px <= stopLoss
		} else {
			hitTP = takeProfit > 0 && px <= takeProfit
			hitSL = stopLoss > 0 && px >= stopLoss
		}
		if !hitTP && !hitSL {
			continue
		}
		reason := "tp"
		if hitSL {
			reason = "sl"
		}
		_ = m.closePositionForInstance(inst, sym, reason, signalID)
		return
	}
}

func (m *Manager) closePositionForInstance(inst *StrategyInstance, symbol string, reason string, signalID string) error {
	if inst == nil {
		return fmt.Errorf("nil instance")
	}
	sym := strings.TrimSpace(symbol)
	if sym == "" {
		return fmt.Errorf("empty symbol")
	}

	if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		order, _, _, err := bx.ClosePositionOrder(sym, inst.OwnerID)
		if err != nil {
			return err
		}
		if order == nil {
			return nil
		}
		database.DB.Create(&models.StrategyOrder{
			StrategyID:      inst.ID,
			StrategyName:    inst.Name,
			OwnerID:         inst.OwnerID,
			Exchange:        bx.GetName(),
			Symbol:          sym,
			Side:            strings.ToLower(order.Side),
			OrderType:       "market",
			ClientOrderID:   order.ClientOrderID,
			ExchangeOrderID: order.ID,
			Status:          strings.ToLower(order.Status),
			RequestedQty:    order.Amount,
			Price:           0,
			ExecutedQty:     order.Amount,
			AvgPrice:        order.Price,
			RequestedAt:     time.Now(),
			UpdatedAt:       time.Now(),
		})
		inst.hub.BroadcastJSON(map[string]interface{}{"type": "order", "data": order})
		if strings.ToLower(order.Status) == "filled" {
			applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), sym, strings.ToLower(order.Side), order.Amount, order.Price, 0, 0, order.Timestamp)
		}
		return nil
	}

	var pos models.StrategyPosition
	if err := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", inst.OwnerID, inst.ID, sym, "open").
		Order("open_time desc").
		First(&pos).Error; err != nil {
		return nil
	}
	if pos.Amount <= 0 {
		return nil
	}
	clientOrderID := models.GenerateUUID()
	database.DB.Create(&models.StrategyOrder{
		StrategyID:    inst.ID,
		StrategyName:  inst.Name,
		OwnerID:       inst.OwnerID,
		Exchange:      inst.exchange.GetName(),
		Symbol:        sym,
		Side:          "sell",
		OrderType:     "market",
		ClientOrderID: clientOrderID,
		Status:        "requested",
		RequestedQty:  pos.Amount,
		Price:         0,
		RequestedAt:   time.Now(),
		UpdatedAt:     time.Now(),
	})
	order, err := inst.exchange.PlaceOrder(inst.OwnerID, clientOrderID, sym, "sell", pos.Amount, 0)
	if err != nil {
		database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
			Updates(map[string]interface{}{"status": "failed", "updated_at": time.Now()})
		return err
	}
	database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
		Updates(map[string]interface{}{
			"exchange_order_id": order.ID,
			"status":            order.Status,
			"executed_qty":      order.Amount,
			"avg_price":         order.Price,
			"updated_at":        time.Now(),
		})
	inst.hub.BroadcastJSON(map[string]interface{}{"type": "order", "data": order})
	if strings.ToLower(order.Status) == "filled" {
		applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), sym, "sell", order.Amount, order.Price, 0, 0, order.Timestamp)
	}
	_ = reason
	_ = signalID
	return nil
}

func (m *Manager) RemoveStrategy(id string) error {
	// Remove from in-memory registry first, then kill process if needed.
	m.mu.Lock()
	inst, ok := m.instances[id]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.instances, id)
	m.mu.Unlock()

	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.Status == StatusRunning {
		inst.cmd.Process.Kill()
	}
	return nil
}

// UpdateStrategyConfig updates a strategy's config in memory.
// Caller is responsible for persisting to DB (API handler does this).
// Config cannot be changed while the strategy is running to avoid race conditions.
func (m *Manager) UpdateStrategyConfig(id string, config map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}

	if inst.Status == StatusRunning {
		return fmt.Errorf("cannot update config while strategy is running")
	}

	inst.Config = config
	return nil
}

func (inst *StrategyInstance) readStdout() {
	scanner := bufio.NewScanner(inst.stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			txt := strings.TrimSpace(string(line))
			if txt == "" {
				continue
			}
			database.DB.Create(&models.StrategyLog{
				StrategyID: inst.ID,
				Level:      "info",
				Message:    txt,
				CreatedAt:  time.Now(),
			})
			inst.hub.BroadcastJSON(map[string]interface{}{"type": "log", "data": txt, "id": inst.ID})
			continue
		}
		if t, _ := msg["type"].(string); t != "log" {
			continue
		}
		logMsg, _ := msg["data"].(string)
		if strings.TrimSpace(logMsg) == "" {
			continue
		}
		database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      "info",
			Message:    logMsg,
			CreatedAt:  time.Now(),
		})
		inst.hub.BroadcastJSON(map[string]interface{}{"type": "log", "data": logMsg, "id": inst.ID})
	}

	inst.mu.Lock()
	inst.Status = StatusStopped
	inst.mu.Unlock()
}

func (m *Manager) SyncFromDB(db *gorm.DB) error {
	// SyncFromDB loads all strategy instances from DB into memory so they can be
	// started/stopped without recreating them.
	//
	// This is called once on backend startup.
	var instances []models.StrategyInstance
	if err := db.Preload("Template").Find(&instances).Error; err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, inst := range instances {
		if _, ok := m.instances[inst.ID]; !ok {
			var config map[string]interface{}
			json.Unmarshal([]byte(inst.Config), &config)

			path := strings.TrimSpace(inst.Template.Path)
			needsWrite := path == ""
			if !needsWrite {
				if fi, err := os.Stat(path); err != nil || (err == nil && fi.IsDir()) {
					needsWrite = true
				}
			}
			if needsWrite {
				filename := fmt.Sprintf("%s_%d.py", inst.Template.Name, inst.Template.AuthorID)
				filename = strings.ReplaceAll(filename, " ", "_")
				filename = filepath.Base(filename)
				strategiesDir := conf.C().Paths.StrategiesDir
				if strategiesDir == "" {
					strategiesDir = conf.Path("strategies")
				}
				absDir, err := filepath.Abs(strategiesDir)
				if err == nil {
					_ = os.MkdirAll(absDir, 0o755)
					candidate := ""
					if strings.TrimSpace(inst.Template.Code) != "" {
						absPath := filepath.Join(absDir, filename)
						if err := os.WriteFile(absPath, []byte(inst.Template.Code), 0o644); err == nil {
							candidate = absPath
						}
					} else {
						// Try match existing file by name prefix
						entries, _ := os.ReadDir(absDir)
						prefix := strings.ToLower(strings.ReplaceAll(inst.Template.Name, " ", "_")) + "_"
						for _, e := range entries {
							if e.IsDir() {
								continue
							}
							n := strings.ToLower(e.Name())
							if strings.HasSuffix(n, ".py") && strings.HasPrefix(n, prefix) {
								candidate = filepath.Join(absDir, e.Name())
								break
							}
						}
					}
					if candidate != "" {
						path = candidate
						_ = database.DB.Model(&models.StrategyTemplate{}).Where("id = ?", inst.Template.ID).
							Updates(map[string]interface{}{"path": candidate, "updated_at": time.Now()}).Error
						logger.Infof("[SYNC PATH FIX] template_id=%d path=%s", inst.Template.ID, candidate)
					}
				}
			}
			if path == "" {
				path = inst.Template.Path
			}

			m.instances[inst.ID] = &StrategyInstance{
				ID:        inst.ID,
				Name:      inst.Name,
				Path:      path,
				Config:    config,
				Status:    StatusStopped,
				OwnerID:   inst.OwnerID,
				CreatedAt: inst.CreatedAt,
				hub:       m.hub,
				exchange:  m.exchange,
				mgr:       m,
			}

		}
	}
	return nil
}

// ListStrategies returns all strategy instances visible to a user.
// Admins can see all instances; non-admins can only see their own.
func (m *Manager) ListStrategies(ownerID uint, isAdmin bool) []*StrategyInstance {

	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*StrategyInstance, 0)
	for _, inst := range m.instances {
		if isAdmin || inst.OwnerID == ownerID {
			list = append(list, inst)
		}
	}

	// Sort by CreatedAt Desc
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})

	return list
}

// GetExchange exposes the exchange connector for API handlers.
func (m *Manager) GetExchange() exchange.Exchange {
	return m.exchange
}

// Clear stops all running strategies and clears the in-memory registry.
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.instances {
		if inst.Status == StatusRunning {
			inst.cmd.Process.Kill()
		}
	}
	m.instances = make(map[string]*StrategyInstance)
}

// StartBacktest starts an asynchronous backtest and returns the Backtest row id.
//
// The backtest lifecycle is broadcast to websocket clients:
// - type=backtest_status with status=running/failed/completed
// - type=backtest_progress with periodic equity updates
func (m *Manager) StartBacktest(id string, startTime, endTime time.Time, initialBalance float64, userID uint) (uint, error) {
	// 1. Check if instance exists
	m.mu.RLock()
	_, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("strategy %s not found", id)
	}

	// 2. Create a record in database first
	bt := &models.Backtest{
		StrategyID:     id,
		StartTime:      startTime,
		EndTime:        endTime,
		InitialBalance: initialBalance,
		Status:         "pending",
		UserID:         userID,
		CreatedAt:      time.Now(),
	}
	database.DB.Create(bt)

	// 3. Run in background
	go func() {
		bt.Status = "running"
		database.DB.Save(bt)

		m.hub.BroadcastJSON(map[string]interface{}{
			"type":        "backtest_status",
			"backtest_id": bt.ID,
			"strategy_id": id,
			"user_id":     userID,
			"status":      "running",
		})

		result, err := m.runBacktestSimulation(id, startTime, endTime, initialBalance, userID, bt.ID)
		if err != nil {
			bt.Status = "failed"
			database.DB.Save(bt)
			m.hub.BroadcastJSON(map[string]interface{}{
				"type":        "backtest_status",
				"backtest_id": bt.ID,
				"strategy_id": id,
				"user_id":     userID,
				"status":      "failed",
				"error":       err.Error(),
			})
			return
		}

		resJSON, _ := json.Marshal(result)
		bt.Status = "completed"
		bt.FinalBalance = result.FinalBalance
		bt.TotalTrades = result.TotalTrades
		bt.TotalProfit = result.TotalProfit
		bt.ReturnRate = result.ReturnRate
		bt.Result = string(resJSON)
		database.DB.Save(bt)

		m.hub.BroadcastJSON(map[string]interface{}{
			"type":        "backtest_status",
			"backtest_id": bt.ID,
			"strategy_id": id,
			"user_id":     userID,
			"status":      "completed",
			"result":      result,
		})
	}()

	return bt.ID, nil
}

// Backtest runs a synchronous backtest and returns the result immediately.
// It still writes a Backtest row to DB for history tracking.
func (m *Manager) Backtest(id string, startTime, endTime time.Time, initialBalance float64, userID uint) (*BacktestResult, error) {
	// 1. Check if instance exists
	m.mu.RLock()
	_, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("strategy %s not found", id)
	}

	// 2. Create a record in database first
	bt := &models.Backtest{
		StrategyID:     id,
		StartTime:      startTime,
		EndTime:        endTime,
		InitialBalance: initialBalance,
		Status:         "running",
		UserID:         userID,
		CreatedAt:      time.Now(),
	}
	database.DB.Create(bt)

	m.hub.BroadcastJSON(map[string]interface{}{
		"type":        "backtest_status",
		"backtest_id": bt.ID,
		"strategy_id": id,
		"user_id":     userID,
		"status":      "running",
	})

	result, err := m.runBacktestSimulation(id, startTime, endTime, initialBalance, userID, bt.ID)
	if err != nil {
		bt.Status = "failed"
		database.DB.Save(bt)
		m.hub.BroadcastJSON(map[string]interface{}{
			"type":        "backtest_status",
			"backtest_id": bt.ID,
			"strategy_id": id,
			"user_id":     userID,
			"status":      "failed",
			"error":       err.Error(),
		})
		return nil, err
	}

	// 3. Update database record with results
	resJSON, _ := json.Marshal(result)
	bt.Status = "completed"
	bt.FinalBalance = result.FinalBalance
	bt.TotalTrades = result.TotalTrades
	bt.TotalProfit = result.TotalProfit
	bt.ReturnRate = result.ReturnRate
	bt.Result = string(resJSON)
	database.DB.Save(bt)

	m.hub.BroadcastJSON(map[string]interface{}{
		"type":        "backtest_status",
		"backtest_id": bt.ID,
		"strategy_id": id,
		"user_id":     userID,
		"status":      "completed",
		"result":      result,
	})

	return result, nil
}

func (m *Manager) runBacktestSimulation(id string, startTime, endTime time.Time, initialBalance float64, userID uint, backtestID uint) (*BacktestResult, error) {
	// runBacktestSimulation starts a separate python process (same strategy file)
	// and feeds it historical candles via stdin.
	//
	// Note: current simulation uses a simplified execution model:
	// - When strategy outputs an order, the fill price is candle.Close
	// - Position/accounting is simplified for MVP and should be enhanced for production
	//   (fees, slippage, partial fills, leverage, etc.).
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("strategy %s not found", id)
	}

	symbol, _ := inst.Config["symbol"].(string)
	if symbol == "" {
		return nil, fmt.Errorf("strategy config must have a symbol")
	}

	// 1. Fetch historical data
	candles, err := m.exchange.FetchHistoricalCandles(symbol, "1h", startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %v", err)
	}

	if len(candles) == 0 {
		return nil, fmt.Errorf("no historical data found for the given time range")
	}

	// 2. Setup Backtest Environment
	configJSON, _ := json.Marshal(inst.Config)
	absPath, cleanup, err := m.prepareBacktestStrategyFile(inst, backtestID)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd := exec.Command("python3", absPath, string(configJSON))
	cmd.Dir = filepath.Dir(absPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer cmd.Process.Kill()

	// 3. Simulation Variables
	balance := initialBalance
	positionAmount := 0.0
	positionMargin := 0.0
	entryPrice := 0.0
	totalTrades := 0
	totalProfit := 0.0
	equityCurve := make([]EquityPoint, 0)

	// Channel to receive orders from strategy
	orderChan := make(chan map[string]interface{}, 10)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var msg map[string]interface{}
			if err := json.Unmarshal(scanner.Bytes(), &msg); err == nil {
				if msg["type"] == "order" {
					orderChan <- msg["data"].(map[string]interface{})
				}
			}
		}
	}()

	// 4. Run Simulation
	lastProgressEmit := time.Now()
	for _, candle := range candles {
		// Send candle to strategy
		candleMsg := map[string]interface{}{
			"type": "candle",
			"data": candle,
		}
		json.NewEncoder(stdin).Encode(candleMsg)

		// Brief pause to allow strategy to process and potentially send an order
		// In a real backtester, we would wait for a "done" signal, but here we'll use a small timeout
		time.Sleep(10 * time.Millisecond)

		// Check for orders
		select {
		case orderReq := <-orderChan:
			side, _ := orderReq["side"].(string)
			amount, _ := orderReq["amount"].(float64)
			price := candle.Close // Simplified: execute at current candle close
			lev := 1
			if raw, ok := inst.Config["leverage"]; ok {
				if v, ok := raw.(float64); ok && int(v) > 0 {
					lev = int(v)
				}
			}
			if lev <= 0 {
				lev = 1
			}
			simOrderID := models.GenerateUUID()

			if side == "buy" {
				requiredMargin := (amount * price) / float64(lev)
				if balance >= requiredMargin {
					balance -= requiredMargin
					newAmt := positionAmount + amount
					if newAmt > 0 {
						entryPrice = ((entryPrice * positionAmount) + (price * amount)) / newAmt
					} else {
						entryPrice = price
					}
					positionAmount = newAmt
					positionMargin += requiredMargin
					totalTrades++
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{
						"type": "order",
						"data": map[string]interface{}{
							"id":              simOrderID,
							"client_order_id": simOrderID,
							"symbol":          symbol,
							"side":            "buy",
							"amount":          amount,
							"price":           price,
							"status":          "filled",
							"timestamp":       candle.Timestamp,
						},
					})
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{
						"type": "position",
						"data": map[string]interface{}{
							"symbol":    symbol,
							"qty":       positionAmount,
							"avg_price": entryPrice,
							"status":    "open",
						},
					})
				} else {
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{
						"type": "order",
						"data": map[string]interface{}{
							"id":              simOrderID,
							"client_order_id": simOrderID,
							"symbol":          symbol,
							"side":            "buy",
							"amount":          amount,
							"price":           price,
							"status":          "rejected",
							"timestamp":       candle.Timestamp,
						},
					})
				}
			} else if side == "sell" {
				if positionAmount >= amount {
					released := 0.0
					if positionAmount > 0 && positionMargin > 0 {
						released = positionMargin * (amount / positionAmount)
					}
					pnl := amount * (price - entryPrice)
					balance += released + pnl
					positionAmount -= amount
					positionMargin -= released
					if positionAmount <= 0 {
						positionAmount = 0
						positionMargin = 0
						entryPrice = 0
					}
					totalTrades++
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{
						"type": "order",
						"data": map[string]interface{}{
							"id":              simOrderID,
							"client_order_id": simOrderID,
							"symbol":          symbol,
							"side":            "sell",
							"amount":          amount,
							"price":           price,
							"status":          "filled",
							"timestamp":       candle.Timestamp,
						},
					})
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{
						"type": "position",
						"data": map[string]interface{}{
							"symbol":    symbol,
							"qty":       positionAmount,
							"avg_price": entryPrice,
							"status": func() string {
								if positionAmount > 0 {
									return "open"
								}
								return "closed"
							}(),
						},
					})
				} else {
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{
						"type": "order",
						"data": map[string]interface{}{
							"id":              simOrderID,
							"client_order_id": simOrderID,
							"symbol":          symbol,
							"side":            "sell",
							"amount":          amount,
							"price":           price,
							"status":          "rejected",
							"timestamp":       candle.Timestamp,
						},
					})
				}
			}
		default:
			// No order this time
		}

		// Calculate current equity
		currentEquity := balance
		if positionAmount > 0 {
			currentEquity = balance + positionMargin + (positionAmount * (candle.Close - entryPrice))
		}
		equityCurve = append(equityCurve, EquityPoint{
			Timestamp: candle.Timestamp,
			Equity:    currentEquity,
		})

		if time.Since(lastProgressEmit) >= 500*time.Millisecond {
			lastProgressEmit = time.Now()
			m.hub.BroadcastJSON(map[string]interface{}{
				"type":        "backtest_progress",
				"backtest_id": backtestID,
				"strategy_id": id,
				"user_id":     userID,
				"timestamp":   candle.Timestamp,
				"equity":      currentEquity,
				"balance":     balance,
				"position":    positionAmount,
				"trades":      totalTrades,
			})
		}
	}

	finalBalance := balance + (positionAmount * candles[len(candles)-1].Close)
	totalProfit = finalBalance - initialBalance
	returnRate := (totalProfit / initialBalance) * 100

	return &BacktestResult{
		TotalTrades:    totalTrades,
		TotalProfit:    totalProfit,
		ReturnRate:     returnRate,
		InitialBalance: initialBalance,
		FinalBalance:   finalBalance,
		EquityCurve:    equityCurve,
	}, nil
}
