package strategy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	Path string `json:"path"`
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
	// hub is the websocket broadcaster for UI updates.
	hub *ws.Hub
	// exchange is the exchange implementation (mock/binance/etc.).
	exchange exchange.Exchange

	mgr *Manager

	redisCancel context.CancelFunc
	bootID      string
	resync      bool
	feedSymbols []string
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
		return fmt.Errorf("redis bus not available")
	}

	runCfg := make(map[string]interface{}, len(inst.Config)+4)
	for k, v := range inst.Config {
		runCfg[k] = v
	}
	runCfg["strategy_id"] = inst.ID
	runCfg["owner_id"] = inst.OwnerID
	rc := conf.C().Redis
	runCfg["redis_addr"] = rc.Addr
	runCfg["redis_password"] = rc.Password
	runCfg["redis_db"] = rc.DB
	runCfg["redis_prefix"] = rc.Prefix
	runCfg["use_redis"] = true

	fixedSymbol := ""
	if raw, ok := inst.Config["symbol"].(string); ok {
		fixedSymbol = strings.TrimSpace(raw)
	}
	activeSymbol := fixedSymbol

	feedSymbols := parseSymbolsValue(inst.Config["symbols"])
	if len(feedSymbols) == 0 && strings.TrimSpace(activeSymbol) != "" {
		feedSymbols = []string{strings.TrimSpace(activeSymbol)}
	}
	if len(feedSymbols) > 0 {
		runCfg["symbols"] = feedSymbols
		if _, ok := runCfg["symbol"].(string); !ok || strings.TrimSpace(fmt.Sprintf("%v", runCfg["symbol"])) == "" {
			runCfg["symbol"] = feedSymbols[0]
		}
	}

	configJSON, _ := json.Marshal(runCfg)

	absPath, err := resolveStrategyPath(inst.Path)
	if err != nil {
		inst.mu.Unlock()
		return err
	}
	cmd := exec.Command("python3", absPath, string(configJSON))
	cmd.Dir = filepath.Dir(absPath)
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
	inst.mu.Unlock()

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
		inst.mu.Lock()
		changed := inst.bootID != st.BootID
		if changed {
			inst.bootID = st.BootID
			inst.resync = true
		}
		inst.mu.Unlock()
	})
	go m.historySyncLoop(ctx, inst, redisBus)

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

		for _, sym := range feedSymbols {
			sym := sym
			go func() {
				if err := inst.exchange.SubscribeCandles(sym, func(candle exchange.Candle) {
					payload := map[string]interface{}{
						"symbol":    sym,
						"timestamp": candle.Timestamp,
						"open":      candle.Open,
						"high":      candle.High,
						"low":       candle.Low,
						"close":     candle.Close,
						"volume":    candle.Volume,
					}
					_ = redisBus.PublishCandle(context.Background(), bus.CandleMessage{
						StrategyID: inst.ID,
						Symbol:     sym,
						Timestamp:  candle.Timestamp,
						Open:       candle.Open,
						High:       candle.High,
						Low:        candle.Low,
						Close:      candle.Close,
						Volume:     candle.Volume,
					})
					inst.hub.BroadcastJSON(map[string]interface{}{
						"type":        "candle",
						"strategy_id": inst.ID,
						"owner_id":    inst.OwnerID,
						"data":        payload,
					})
				}); err != nil {
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
				}
			}()
		}
	}

	go inst.readStdout()
	go inst.readStderr(stderr)
	return nil
}

func applyOrderFillToPosition(hub *ws.Hub, ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, executedQty float64, avgPrice float64, eventTime time.Time) {
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
		database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).
			Updates(map[string]interface{}{"amount": newAmt, "avg_price": newAvg, "updated_at": now})
		if hub != nil {
			pos.Amount = newAmt
			pos.AvgPrice = newAvg
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
	for scanner.Scan() {
		msg := scanner.Text()
		logger.Errorf("[%s ERROR] %s", inst.Name, msg)
		database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      "error",
			Message:    msg,
			CreatedAt:  time.Now(),
		})
		inst.hub.BroadcastJSON(map[string]interface{}{
			"type":        "error",
			"strategy_id": inst.ID,
			"owner_id":    inst.OwnerID,
			"error":       msg,
		})
	}
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

	if inst.redisCancel != nil {
		inst.redisCancel()
		inst.redisCancel = nil
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
			inst.mu.Unlock()

			if !need || strings.TrimSpace(bootID) == "" || len(symbols) == 0 {
				continue
			}

			ok := true
			historyBars := 200
			for _, sym := range symbols {
				candles, err := inst.exchange.FetchCandles(sym, "1m", historyBars)
				if err != nil || len(candles) == 0 {
					ok = false
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
				}
			}
			if ok {
				inst.mu.Lock()
				inst.resync = false
				inst.mu.Unlock()
			}
		}
	}
}

func (m *Manager) handleRedisSignal(inst *StrategyInstance, s bus.SignalMessage) {
	if inst == nil {
		return
	}
	symbol := strings.TrimSpace(s.Symbol)
	if symbol == "" {
		return
	}
	if !isAllowedSymbol(inst, symbol) {
		return
	}
	action := strings.ToLower(strings.TrimSpace(s.Action))
	if action == "" {
		action = "open"
	}
	if action != "open" {
		return
	}
	side := strings.ToLower(strings.TrimSpace(s.Side))
	if side == "" {
		side = "buy"
	}
	if side != "buy" && side != "sell" {
		return
	}
	amount := clampOrderAmount(inst, s.Amount)
	if amount <= 0 {
		return
	}
	m.placeOrderForInstance(inst, symbol, side, amount, 0, s.TakeProfit, s.StopLoss, strings.TrimSpace(s.SignalID))
}

func (m *Manager) placeOrderForInstance(inst *StrategyInstance, symbol string, side string, amount float64, price float64, takeProfit float64, stopLoss float64, signalID string) {
	if inst == nil {
		return
	}
	if !isAllowedSymbol(inst, symbol) {
		return
	}
	normalizedSide := strings.ToLower(strings.TrimSpace(side))
	if normalizedSide != "buy" && normalizedSide != "sell" {
		return
	}
	amount = clampOrderAmount(inst, amount)
	if amount <= 0 {
		return
	}

	maxPos := 1
	if v, ok := inst.Config["max_concurrent_positions"].(float64); ok && int(v) > 0 {
		maxPos = int(v)
	}

	if normalizedSide == "buy" {
		var openCount int64
		database.DB.Model(&models.StrategyPosition{}).
			Where("owner_id = ? AND strategy_id = ? AND status = ?", inst.OwnerID, inst.ID, "open").
			Count(&openCount)
		var inflightBuy int64
		database.DB.Model(&models.StrategyOrder{}).
			Where("owner_id = ? AND strategy_id = ? AND side = ? AND status IN ?", inst.OwnerID, inst.ID, "buy", []string{"requested", "new", "partially_filled"}).
			Count(&inflightBuy)
		if int(openCount) >= maxPos || int(openCount+inflightBuy) >= maxPos {
			return
		}
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
		database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
			Updates(map[string]interface{}{"status": "failed", "updated_at": time.Now()})
		database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      "error",
			Message:    fmt.Sprintf("Failed to place order: %v", err),
			CreatedAt:  time.Now(),
		})
		inst.hub.BroadcastJSON(map[string]interface{}{
			"type":        "error",
			"strategy_id": inst.ID,
			"owner_id":    inst.OwnerID,
			"error":       fmt.Sprintf("Failed to place order: %v", err),
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
		applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), symbol, normalizedSide, order.Amount, order.Price, order.Timestamp)
	}

	if normalizedSide == "buy" && (takeProfit > 0 || stopLoss > 0) {
		go m.monitorPositionTPStop(inst, symbol, takeProfit, stopLoss, signalID)
	}
}

func (m *Manager) monitorPositionTPStop(inst *StrategyInstance, symbol string, takeProfit float64, stopLoss float64, signalID string) {
	if inst == nil {
		return
	}
	sym := strings.TrimSpace(symbol)
	if sym == "" {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		candles, err := inst.exchange.FetchCandles(sym, "1m", 1)
		if err != nil || len(candles) == 0 {
			continue
		}
		last := candles[len(candles)-1]
		px := last.Close
		if px <= 0 {
			continue
		}
		hitTP := takeProfit > 0 && px >= takeProfit
		hitSL := stopLoss > 0 && px <= stopLoss
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
			applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), sym, strings.ToLower(order.Side), order.Amount, order.Price, order.Timestamp)
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
		applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), sym, "sell", order.Amount, order.Price, order.Timestamp)
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
	absPath, err := resolveStrategyPath(inst.Path)
	if err != nil {
		return nil, err
	}
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
