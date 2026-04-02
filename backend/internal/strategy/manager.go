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

func loadLatestDiskStrategySource(inst *StrategyInstance, row *models.StrategyInstance) (string, string, bool) {
	candidates := make([]string, 0, 2)
	names := make([]string, 0, 3)
	if row != nil {
		if p := strings.TrimSpace(row.Template.Path); p != "" && !strings.HasPrefix(strings.ToLower(p), "db://") {
			candidates = append(candidates, p)
		}
		if n := strings.TrimSpace(row.Template.Name); n != "" {
			names = append(names, n, strings.ReplaceAll(n, " ", "_")+".py")
		}
	}
	if inst != nil {
		if p := strings.TrimSpace(inst.Path); p != "" && !strings.HasPrefix(strings.ToLower(p), "db://") {
			candidates = append(candidates, p)
			names = append(names, filepath.Base(p))
		}
	}

	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	if absDir, err := filepath.Abs(strategiesDir); err == nil {
		entries, _ := os.ReadDir(absDir)
		for _, want := range names {
			want = strings.ToLower(strings.TrimSpace(filepath.Base(want)))
			if want == "" || want == "." {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := strings.ToLower(e.Name())
				if name == want || strings.HasPrefix(name, strings.TrimSuffix(want, ".py")) {
					candidates = append(candidates, filepath.Join(absDir, e.Name()))
				}
			}
		}
	}

	seen := map[string]struct{}{}
	bestPath := ""
	bestCode := ""
	bestAt := time.Time{}
	for _, p := range candidates {
		abs, err := resolveStrategyPath(p)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		if strings.Contains(filepath.ToSlash(abs), "/_runtime/") {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		code := strings.TrimSpace(string(b))
		if code == "" {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if bestCode == "" || info.ModTime().After(bestAt) {
			bestPath = abs
			bestCode = code
			bestAt = info.ModTime()
		}
	}
	if bestCode != "" {
		return bestPath, bestCode, true
	}
	return "", "", false
}

func sanitizeStrategyRuntimeCode(code string) string {
	if strings.TrimSpace(code) == "" {
		return code
	}
	oldDX := "dx = np.where((plus_di + minus_di) == 0, 0, 100 * np.abs(plus_di - minus_di) / (plus_di + minus_di))"
	newDX := "di_sum = plus_di + minus_di\n    with np.errstate(divide=\"ignore\", invalid=\"ignore\"):\n        dx = np.divide(\n            100 * np.abs(plus_di - minus_di),\n            di_sum,\n            out=np.zeros_like(di_sum),\n            where=di_sum != 0,\n        )\n    dx = np.nan_to_num(dx, nan=0.0, posinf=0.0, neginf=0.0)"
	code = strings.ReplaceAll(code, oldDX, newDX)
	return code
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
	StatusStarting StrategyStatus = "starting"
	StatusRunning  StrategyStatus = "running"
	StatusStopped  StrategyStatus = "stopped"
	StatusError    StrategyStatus = "error"
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
	// signal batching for per-kline selection
	sigMu           sync.Mutex
	pendingSignals  map[string][]bus.SignalMessage
	signalBatchWait time.Duration

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
	tpslCancel      map[string]context.CancelFunc
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

	sigMu           sync.Mutex
	pendingSignals  map[string][]bus.SignalMessage
	signalBatchWait time.Duration

	quickCloseMu sync.Mutex
	quickCloseAt map[string]time.Time

	notifier RuntimeNotifier

	orderCh chan orderReq
	startCh chan string
	stopCh  chan stopReq
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

func (m *Manager) StopPositionTPStopMonitor(strategyID string, symbol string) {
	if m == nil || strings.TrimSpace(strategyID) == "" || strings.TrimSpace(symbol) == "" {
		return
	}
	m.mu.RLock()
	inst := m.instances[strategyID]
	m.mu.RUnlock()
	if inst == nil {
		return
	}
	m.stopPositionTPStopMonitor(inst, symbol)
}

func (m *Manager) SyncRedisOpenCountsFromExchange(ctx context.Context) {
	if m == nil {
		return
	}
	syncOnce := func() {
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

		byOwner := map[uint][]*StrategyInstance{}
		for _, inst := range snap {
			if inst == nil || inst.OwnerID == 0 || strings.TrimSpace(inst.ID) == "" {
				continue
			}
			byOwner[inst.OwnerID] = append(byOwner[inst.OwnerID], inst)
		}

		for ownerID, insts := range byOwner {
			activePositions := map[string]exchange.Position{}
			if ps, err := ex.FetchPositions(ownerID, "active"); err == nil {
				for _, p := range ps {
					if math.Abs(p.Amount) <= 0 {
						continue
					}
					key := exchange.NormalizeSymbol(p.Symbol)
					if key == "" {
						continue
					}
					activePositions[key] = p
				}
			} else {
				logger.Errorf("[REDIS OPEN COUNT] fetch positions failed owner=%d err=%v", ownerID, err)
				continue
			}

			var openRows []models.StrategyPosition
			if err := database.DB.Where("owner_id = ? AND status = ?", ownerID, "open").Find(&openRows).Error; err != nil {
				logger.Errorf("[REDIS OPEN COUNT] load open rows failed owner=%d err=%v", ownerID, err)
				continue
			}

			var recentOrders []models.StrategyOrder
			_ = database.DB.Where("owner_id = ?", ownerID).Order("requested_at desc").Limit(500).Find(&recentOrders).Error
			latestOrderBySymbol := map[string]models.StrategyOrder{}
			for _, ord := range recentOrders {
				symKey := exchange.NormalizeSymbol(ord.Symbol)
				if symKey == "" {
					continue
				}
				if _, ok := latestOrderBySymbol[symKey]; ok {
					continue
				}
				if strings.TrimSpace(ord.StrategyID) == "" {
					continue
				}
				latestOrderBySymbol[symKey] = ord
			}

			countByStrategy := map[string]int64{}
			countedSymbols := map[string]struct{}{}
			now := time.Now()
			for _, row := range openRows {
				symKey := exchange.NormalizeSymbol(row.Symbol)
				if symKey == "" {
					continue
				}
				if _, ok := activePositions[symKey]; !ok {
					updates := map[string]interface{}{
						"amount":     0,
						"status":     "closed",
						"updated_at": now,
					}
					if row.CloseTime.IsZero() {
						updates["close_time"] = now
					}
					_ = database.DB.Model(&models.StrategyPosition{}).Where("id = ?", row.ID).Updates(updates).Error
					if strings.TrimSpace(row.StrategyID) != "" {
						_, _ = rb.ReleaseOpenSlot(ctx, row.StrategyID)
					}
					logger.Infof("[REDIS OPEN COUNT] auto close stale open position owner=%d strategy=%s symbol=%s", ownerID, row.StrategyID, row.Symbol)
					continue
				}
				if strings.TrimSpace(row.StrategyID) != "" {
					countByStrategy[row.StrategyID]++
					countedSymbols[symKey] = struct{}{}
				}
			}

			for symKey, pos := range activePositions {
				if _, ok := countedSymbols[symKey]; ok {
					continue
				}
				ord, ok := latestOrderBySymbol[symKey]
				if !ok || strings.TrimSpace(ord.StrategyID) == "" {
					continue
				}
				countByStrategy[ord.StrategyID]++
				countedSymbols[symKey] = struct{}{}
				_ = database.DB.Create(&models.StrategyPosition{
					StrategyID:   ord.StrategyID,
					StrategyName: ord.StrategyName,
					OwnerID:      ownerID,
					Exchange:     pos.ExchangeName,
					Symbol:       pos.Symbol,
					Amount:       pos.Amount,
					AvgPrice:     pos.Price,
					Status:       "open",
					OpenTime:     pos.OpenTime,
					UpdatedAt:    now,
				}).Error
			}

			pendingCutoff := now.Add(-2 * time.Minute)
			for symKey, ord := range latestOrderBySymbol {
				if _, ok := countedSymbols[symKey]; ok {
					continue
				}
				if strings.TrimSpace(ord.StrategyID) == "" {
					continue
				}
				st := strings.ToLower(strings.TrimSpace(ord.Status))
				if st != "requested" && st != "new" && st != "partially_filled" {
					continue
				}
				if ord.RequestedAt.Before(pendingCutoff) {
					continue
				}
				countByStrategy[ord.StrategyID]++
				countedSymbols[symKey] = struct{}{}
			}
			for _, inst := range insts {
				_ = rb.SetOpenCount(ctx, inst.ID, countByStrategy[inst.ID], 6*time.Hour)
			}
		}
	}

	syncOnce()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncOnce()
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
		instances:       make(map[string]*StrategyInstance),
		hub:             hub,
		exchange:        ex,
		pendingSignals:  make(map[string][]bus.SignalMessage),
		signalBatchWait: 500 * time.Millisecond,
		quickCloseAt:    make(map[string]time.Time),
		orderCh:         make(chan orderReq, 256),
		startCh:         make(chan string, 128),
		stopCh:          make(chan stopReq, 128),
	}
}

type orderReq struct {
	inst       *StrategyInstance
	symbol     string
	side       string
	amount     float64
	price      float64
	takeProfit float64
	stopLoss   float64
	signalID   string
}

type stopReq struct {
	id    string
	force bool
}

func (m *Manager) StartWorkers() {
	go m.runOrderWorker()
	go m.runStartWorker()
	go m.runStopWorker()
}

func (m *Manager) enqueueOrderForInstance(inst *StrategyInstance, symbol string, side string, amount float64, price float64, takeProfit float64, stopLoss float64, signalID string) {
	if inst == nil || symbol == "" || side == "" {
		return
	}
	select {
	case m.orderCh <- orderReq{inst: inst, symbol: symbol, side: side, amount: amount, price: price, takeProfit: takeProfit, stopLoss: stopLoss, signalID: signalID}:
	default:
		emitStrategyLog(inst, "error", "Order queue is full, dropping order request")
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
	sourcePath := strings.TrimSpace(row.Template.Path)
	if diskPath, diskCode, ok := loadLatestDiskStrategySource(inst, &row); ok {
		sourcePath = diskPath
		if diskCode != code && row.Template.ID > 0 {
			_ = database.DB.Model(&models.StrategyTemplate{}).Where("id = ?", row.Template.ID).
				Updates(map[string]interface{}{"code": diskCode, "path": diskPath, "updated_at": time.Now()}).Error
		}
		code = diskCode
	}
	if code == "" {
		return resolveStrategyPath(inst.Path)
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

	if prev := strings.TrimSpace(inst.RuntimePath); prev != "" && prev != inst.Path && strings.Contains(filepath.ToSlash(prev), "/_runtime/") {
		_ = os.Remove(prev)
	}
	absPath := filepath.Join(runtimeDir, fmt.Sprintf("%s_%d.py", inst.ID, time.Now().UnixMilli()))
	code = sanitizeStrategyRuntimeCode(code)
	runtimeCode := "import os\nimport sys\nsys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), \"..\")))\n\n" + miniRedisRuntimeShim() + "\n" + code + "\n"
	if err := os.WriteFile(absPath, []byte(runtimeCode), 0o644); err != nil {
		return "", err
	}

	keep := getBool(inst.Config["keep_runtime_file"]) || getBool(inst.Config["debug"]) || getBool(inst.Config["log_trace"])
	inst.RuntimePath = absPath
	inst.RuntimeGenerated = true
	inst.RuntimeKeep = keep
	if sourcePath == "" {
		sourcePath = "db_template_code"
	}
	emitStrategyLog(inst, "info", fmt.Sprintf("已生成运行脚本 source=%s runtime=%s", sourcePath, absPath))
	return absPath, nil
}

func miniRedisRuntimeShim() string {
	return "import socket\nimport types\n\nclass MiniRedis:\n    def __init__(self, host=\"127.0.0.1\", port=6379, password=\"\", db=0, timeout=30):\n        self.host = host\n        self.port = int(port)\n        self.password = password or \"\"\n        self.db = int(db or 0)\n        self.timeout = timeout\n        self.sock = None\n        self.buf = b\"\"\n\n    def connect(self):\n        self.sock = socket.create_connection((self.host, self.port), timeout=self.timeout if self.timeout else None)\n        if self.timeout:\n            self.sock.settimeout(self.timeout)\n        if self.password:\n            try:\n                self.execute(\"AUTH\", self.password)\n            except RuntimeError as e:\n                msg = str(e)\n                if \"called without any password configured\" not in msg:\n                    raise\n        if self.db:\n            self.execute(\"SELECT\", str(self.db))\n        return self\n\n    def close(self):\n        try:\n            if self.sock:\n                self.sock.close()\n        finally:\n            self.sock = None\n            self.buf = b\"\"\n\n    def _encode(self, *parts):\n        out = [f\"*{len(parts)}\\r\\n\".encode(\"utf-8\")]\n        for p in parts:\n            if p is None:\n                p = \"\"\n            if not isinstance(p, (bytes, bytearray)):\n                p = str(p).encode(\"utf-8\")\n            out.append(f\"${len(p)}\\r\\n\".encode(\"utf-8\"))\n            out.append(p)\n            out.append(b\"\\r\\n\")\n        return b\"\".join(out)\n\n    def _read_exact(self, n):\n        while len(self.buf) < n:\n            chunk = self.sock.recv(4096)\n            if not chunk:\n                raise ConnectionError(\"redis connection closed\")\n            self.buf += chunk\n        out, self.buf = self.buf[:n], self.buf[n:]\n        return out\n\n    def _read_line(self):\n        while b\"\\r\\n\" not in self.buf:\n            chunk = self.sock.recv(4096)\n            if not chunk:\n                raise ConnectionError(\"redis connection closed\")\n            self.buf += chunk\n        i = self.buf.index(b\"\\r\\n\")\n        line, self.buf = self.buf[:i], self.buf[i + 2 :]\n        return line\n\n    def _read_resp(self):\n        prefix = self._read_exact(1)\n        if prefix == b\"+\":\n            return self._read_line().decode(\"utf-8\", errors=\"replace\")\n        if prefix == b\"-\":\n            raise RuntimeError(self._read_line().decode(\"utf-8\", errors=\"replace\"))\n        if prefix == b\":\":\n            return int(self._read_line())\n        if prefix == b\"$\":\n            n = int(self._read_line())\n            if n == -1:\n                return None\n            data = self._read_exact(n)\n            _ = self._read_exact(2)\n            return data.decode(\"utf-8\", errors=\"replace\")\n        if prefix == b\"*\":\n            n = int(self._read_line())\n            if n == -1:\n                return None\n            return [self._read_resp() for _ in range(n)]\n        raise RuntimeError(f\"unknown RESP prefix: {prefix!r}\")\n\n    def execute(self, *args):\n        if not self.sock:\n            self.connect()\n        self.sock.sendall(self._encode(*args))\n        return self._read_resp()\n\n    def publish(self, channel, payload):\n        return self.execute(\"PUBLISH\", channel, payload)\n\n    def subscribe(self, channel):\n        return self.execute(\"SUBSCRIBE\", channel)\n\n    def psubscribe(self, pattern):\n        return self.execute(\"PSUBSCRIBE\", pattern)\n\n    def pubsub(self, *args, **kwargs):\n        return self\n\n    def _set_timeout(self, timeout):\n        if self.sock:\n            self.sock.settimeout(timeout if timeout else self.timeout)\n\n    def get_message(self, timeout=1.0):\n        old_timeout = self.timeout\n        try:\n            self._set_timeout(timeout)\n            return self.read_pubsub_message()\n        finally:\n            self._set_timeout(old_timeout)\n\n    def read_pubsub_message(self):\n        try:\n            msg = self._read_resp()\n        except (TimeoutError, socket.timeout):\n            return None\n        if not isinstance(msg, list) or len(msg) < 3:\n            return None\n        kind = msg[0]\n        if kind == \"message\":\n            return {\"type\": \"message\", \"channel\": msg[1], \"data\": msg[2]}\n        if kind == \"pmessage\" and len(msg) >= 4:\n            return {\"type\": \"pmessage\", \"pattern\": msg[1], \"channel\": msg[2], \"data\": msg[3]}\n        return None\n\n_mod = types.ModuleType(\"mini_redis\")\n_mod.MiniRedis = MiniRedis\nsys.modules.setdefault(\"mini_redis\", _mod)\n"
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
	if diskPath, diskCode, ok := loadLatestDiskStrategySource(inst, &row); ok {
		if diskCode != code && row.Template.ID > 0 {
			_ = database.DB.Model(&models.StrategyTemplate{}).Where("id = ?", row.Template.ID).
				Updates(map[string]interface{}{"code": diskCode, "path": diskPath, "updated_at": time.Now()}).Error
		}
		code = diskCode
	}
	if code == "" {
		absPath, err := resolveStrategyPath(inst.Path)
		return absPath, func() {}, err
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
	code = sanitizeStrategyRuntimeCode(code)
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

	if inst.mgr != nil {
		inst.mgr.setStrategyStatus(inst, StatusStopped)
	} else {
		inst.mu.Lock()
		inst.Status = StatusStopped
		inst.mu.Unlock()
	}
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
