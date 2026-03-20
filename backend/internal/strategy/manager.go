package strategy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"quanty_trade/internal/ws"
	"sort"
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

	// cmd/stdin/stdout are the managed python process and pipes.
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// mu guards process state and pipes.
	mu sync.Mutex
	// hub is the websocket broadcaster for UI updates.
	hub *ws.Hub
	// exchange is the exchange implementation (mock/binance/etc.).
	exchange exchange.Exchange

	mgr *Manager

	selectorActiveSymbol string
	selectorLastSymbol   string
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

func pickBestSymbolForInstance(inst *StrategyInstance) (string, []string, error) {
	if inst == nil {
		return "", nil, fmt.Errorf("nil instance")
	}
	bx, ok := inst.exchange.(*exchange.BinanceExchange)
	if !ok {
		return "", nil, fmt.Errorf("exchange does not support selector")
	}

	cfg := inst.Config
	if raw, ok := inst.Config["selector_id"].(string); ok && strings.TrimSpace(raw) != "" {
		var sel models.StrategySelector
		if err := database.DB.Where("id = ? AND owner_id = ?", strings.TrimSpace(raw), inst.OwnerID).First(&sel).Error; err == nil {
			var selCfg map[string]interface{}
			if err := json.Unmarshal([]byte(sel.Config), &selCfg); err == nil {
				merged := make(map[string]interface{}, len(inst.Config)+len(selCfg))
				for k, v := range inst.Config {
					merged[k] = v
				}
				for k, v := range selCfg {
					if strings.HasPrefix(k, "selector_") {
						merged[k] = v
					}
				}
				cfg = merged
			}
		}
	}
	quote, _ := cfg["selector_quote"].(string)
	minPrice, _ := cfg["selector_min_price"].(float64)
	maxPrice, _ := cfg["selector_max_price"].(float64)
	minVol, _ := cfg["selector_min_quote_volume_24h"].(float64)
	maxSymbols := 5
	if v, ok := cfg["selector_max_symbols"].(float64); ok && int(v) > 0 {
		maxSymbols = int(v)
	}
	excludeStable := true
	if raw, ok := cfg["selector_exclude_stable"]; ok {
		if v, ok := raw.(bool); ok {
			excludeStable = v
		} else if v, ok := raw.(float64); ok {
			excludeStable = v != 0
		}
	}
	var baseAssets []string
	if raw, ok := cfg["selector_base_assets"]; ok {
		if xs, ok := raw.([]interface{}); ok {
			for _, it := range xs {
				if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
					baseAssets = append(baseAssets, s)
				}
			}
		} else if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
			for _, p := range strings.Split(s, ",") {
				if v := strings.TrimSpace(p); v != "" {
					baseAssets = append(baseAssets, v)
				}
			}
		}
	}

	cands, err := bx.FetchMarketSymbols(quote, minPrice, maxPrice, minVol, maxSymbols, excludeStable, baseAssets)
	if err != nil {
		return "", nil, err
	}
	symbols := make([]string, 0, len(cands))
	for _, c := range cands {
		symbols = append(symbols, c.Symbol)
	}
	if len(symbols) == 0 {
		return "", nil, fmt.Errorf("no symbols matched selector filters")
	}

	chosen := symbols[0]
	excludeLast := true
	if raw, ok := cfg["selector_exclude_last"]; ok {
		if v, ok := raw.(bool); ok {
			excludeLast = v
		} else if v, ok := raw.(float64); ok {
			excludeLast = v != 0
		}
	}
	if excludeLast && inst.selectorLastSymbol != "" && exchange.NormalizeSymbol(chosen) == exchange.NormalizeSymbol(inst.selectorLastSymbol) {
		if len(symbols) > 1 {
			chosen = symbols[1]
		}
	}
	return chosen, symbols, nil
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

	runCfg := make(map[string]interface{}, len(inst.Config)+4)
	for k, v := range inst.Config {
		runCfg[k] = v
	}

	mode := ""
	if raw, ok := inst.Config["symbol_mode"].(string); ok {
		mode = strings.ToLower(strings.TrimSpace(raw))
	}
	fixedSymbol := ""
	if raw, ok := inst.Config["symbol"].(string); ok {
		fixedSymbol = strings.TrimSpace(raw)
	}
	activeSymbol := fixedSymbol
	if mode == "selector" && fixedSymbol == "" {
		if chosen, symbols, err := pickBestSymbolForInstance(inst); err == nil && strings.TrimSpace(chosen) != "" {
			activeSymbol = chosen
			inst.selectorActiveSymbol = activeSymbol
			runCfg["symbol"] = activeSymbol
			inst.Config["active_symbol"] = activeSymbol
			inst.Config["symbols"] = symbols
			logger.Infof("[STRATEGY SELECT] id=%s owner=%d symbol=%s candidates=%d", inst.ID, inst.OwnerID, activeSymbol, len(symbols))
		} else if err != nil {
			logger.Errorf("[STRATEGY SELECT ERROR] id=%s owner=%d err=%v", inst.ID, inst.OwnerID, err)
		}
	} else if fixedSymbol != "" {
		inst.selectorActiveSymbol = ""
		inst.Config["active_symbol"] = ""
	}

	configJSON, _ := json.Marshal(runCfg)

	absPath, err := resolveStrategyPath(inst.Path)
	if err != nil {
		inst.mu.Unlock()
		return err
	}
	cmd := exec.Command("python3", absPath, string(configJSON))
	cmd.Dir = filepath.Dir(absPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		inst.mu.Unlock()
		return err
	}
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
	inst.stdin = stdin
	inst.stdout = stdout
	inst.Status = StatusRunning
	inst.mu.Unlock()

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

	// Start data feed (single active symbol)
	symbol := activeSymbol
	if symbol == "" {
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
		warmup := 50
		minWarmup := 50
		if v, ok := inst.Config["slow_window"].(float64); ok && int(v) > 0 {
			if int(v)+2 > minWarmup {
				minWarmup = int(v) + 2
			}
		}
		if v, ok := inst.Config["window"].(float64); ok && int(v) > 0 {
			if int(v)+2 > minWarmup {
				minWarmup = int(v) + 2
			}
		}
		if raw, ok := inst.Config["warmup_bars"]; ok {
			if v, ok := raw.(float64); ok {
				warmup = int(v)
			}
		} else {
			if v, ok := inst.Config["slow_window"].(float64); ok && int(v) > warmup {
				warmup = int(v)
			}
			if v, ok := inst.Config["window"].(float64); ok && int(v) > warmup {
				warmup = int(v)
			}
		}
		if warmup < minWarmup {
			warmup = minWarmup
		}

		if warmup > 0 {
			if candles, err := inst.exchange.FetchCandles(symbol, "1m", warmup); err == nil && len(candles) > 0 {
				logger.Infof("[STRATEGY WARMUP] id=%s owner=%d symbol=%s bars=%d", inst.ID, inst.OwnerID, symbol, len(candles))
				for _, candle := range candles {
					payload := map[string]interface{}{
						"symbol":    symbol,
						"timestamp": candle.Timestamp,
						"open":      candle.Open,
						"high":      candle.High,
						"low":       candle.Low,
						"close":     candle.Close,
						"volume":    candle.Volume,
					}
					_ = inst.SendData("candle", payload)
				}
			} else if err != nil {
				logger.Errorf("[STRATEGY WARMUP ERROR] id=%s owner=%d symbol=%s err=%v", inst.ID, inst.OwnerID, symbol, err)
			}
		}

		go func() {
			if err := inst.exchange.SubscribeCandles(symbol, func(candle exchange.Candle) {
				payload := map[string]interface{}{
					"symbol":    symbol,
					"timestamp": candle.Timestamp,
					"open":      candle.Open,
					"high":      candle.High,
					"low":       candle.Low,
					"close":     candle.Close,
					"volume":    candle.Volume,
				}
				inst.SendData("candle", payload)
				inst.hub.BroadcastJSON(map[string]interface{}{
					"type":        "candle",
					"strategy_id": inst.ID,
					"owner_id":    inst.OwnerID,
					"data":        payload,
				})
			}); err != nil {
				logger.Errorf("[STRATEGY SUBSCRIBE ERROR] id=%s owner=%d symbol=%s err=%v", inst.ID, inst.OwnerID, symbol, err)
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
			if sym, ok := inst.Config["symbol"].(string); ok && sym != "" {
				exPos, err := bx.FetchPositions(inst.OwnerID, "active")
				if err == nil {
					want := exchange.NormalizeSymbol(sym)
					for _, p := range exPos {
						if exchange.NormalizeSymbol(p.Symbol) == want && p.Amount > 0 {
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

	// Send stop command
	stopMsg := map[string]interface{}{"type": "stop"}
	json.NewEncoder(inst.stdin).Encode(stopMsg)

	if err := inst.cmd.Process.Kill(); err != nil {
		return err
	}

	inst.Status = StatusStopped
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

// SendData sends a message to the python process stdin.
// This is used for real-time feeds (candles) and backend-originated updates (orders).
func (inst *StrategyInstance) SendData(dataType string, data interface{}) error {

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.Status != StatusRunning {
		return fmt.Errorf("strategy not running")
	}

	msg := map[string]interface{}{
		"type": dataType,
		"data": data,
	}
	return json.NewEncoder(inst.stdin).Encode(msg)
}

func (inst *StrategyInstance) readStdout() {
	// Strategy stdout protocol:
	// - type=log: persisted to StrategyLog table + broadcast to frontend
	// - type=order: triggers PlaceOrder on exchange with correlation id
	//
	// Order risk controls enforced here:
	// - max_concurrent_positions (default 1) limits open positions per strategy
	// - inflight buy orders are counted to prevent order spamming
	scanner := bufio.NewScanner(inst.stdout)
	for scanner.Scan() {
		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			logger.Errorf("Error decoding strategy output: %v", err)
			continue
		}

		msgType, _ := msg["type"].(string)
		data := msg["data"]

		switch msgType {
		case "order":
			orderReq, _ := data.(map[string]interface{})
			symbol, _ := orderReq["symbol"].(string)
			side, _ := orderReq["side"].(string)
			amount, _ := orderReq["amount"].(float64)
			price, _ := orderReq["price"].(float64)

			maxPos := 1
			// Strategy-level guardrail: maximum number of concurrent open positions.
			// If not provided by config, default is 1.
			if v, ok := inst.Config["max_concurrent_positions"].(float64); ok && int(v) > 0 {
				maxPos = int(v)
			}

			normalizedSide := strings.ToLower(side)
			if normalizedSide == "buy" {
				// Count current open positions and inflight buy orders to prevent churn/abuse.
				var openCount int64
				database.DB.Model(&models.StrategyPosition{}).
					Where("owner_id = ? AND strategy_id = ? AND status = ?", inst.OwnerID, inst.ID, "open").
					Count(&openCount)
				var inflightBuy int64
				database.DB.Model(&models.StrategyOrder{}).
					Where("owner_id = ? AND strategy_id = ? AND side = ? AND status IN ?", inst.OwnerID, inst.ID, "buy", []string{"requested", "new", "partially_filled"}).
					Count(&inflightBuy)
				if int(openCount) >= maxPos {
					reason := fmt.Sprintf("Max concurrent positions reached (open=%d max=%d)", openCount, maxPos)
					logger.Warnf("[ORDER BLOCK] strategy=%s owner=%d symbol=%s side=%s amount=%v price=%v reason=%s", inst.ID, inst.OwnerID, symbol, normalizedSide, amount, price, reason)
					database.DB.Create(&models.StrategyLog{
						StrategyID: inst.ID,
						Level:      "error",
						Message:    reason,
						CreatedAt:  time.Now(),
					})
					inst.hub.BroadcastJSON(map[string]interface{}{
						"type":        "error",
						"strategy_id": inst.ID,
						"owner_id":    inst.OwnerID,
						"error":       reason,
					})
					continue
				}
				if int(openCount+inflightBuy) >= maxPos {
					reason := fmt.Sprintf("Max concurrent positions reached (open=%d inflight_buy=%d max=%d)", openCount, inflightBuy, maxPos)
					logger.Warnf("[ORDER BLOCK] strategy=%s owner=%d symbol=%s side=%s amount=%v price=%v reason=%s", inst.ID, inst.OwnerID, symbol, normalizedSide, amount, price, reason)
					database.DB.Create(&models.StrategyLog{
						StrategyID: inst.ID,
						Level:      "error",
						Message:    reason,
						CreatedAt:  time.Now(),
					})
					inst.hub.BroadcastJSON(map[string]interface{}{
						"type":        "error",
						"strategy_id": inst.ID,
						"owner_id":    inst.OwnerID,
						"error":       reason,
					})
					continue
				}
			}

			clientOrderID := models.GenerateUUID()
			logger.Infof("[ORDER REQUEST] strategy=%s owner=%d client_order_id=%s symbol=%s side=%s amount=%v price=%v", inst.ID, inst.OwnerID, clientOrderID, symbol, normalizedSide, amount, price)
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

			leverage := 0
			if orderReq != nil {
				if raw, ok := orderReq["leverage"]; ok {
					if v, ok := raw.(float64); ok && int(v) > 0 {
						leverage = int(v)
					}
				}
			}
			if leverage == 0 {
				if raw, ok := inst.Config["leverage"]; ok {
					if v, ok := raw.(float64); ok && int(v) > 0 {
						leverage = int(v)
					}
				}
			}
			if leverage > 0 {
				if ex, ok := inst.exchange.(interface {
					SetLeverage(ownerID uint, symbol string, leverage int) error
				}); ok {
					if err := ex.SetLeverage(inst.OwnerID, symbol, leverage); err != nil {
						logger.Errorf("[LEVERAGE ERROR] strategy=%s owner=%d symbol=%s leverage=%d err=%v", inst.ID, inst.OwnerID, symbol, leverage, err)
						database.DB.Create(&models.StrategyLog{
							StrategyID: inst.ID,
							Level:      "error",
							Message:    fmt.Sprintf("SetLeverage error: %v", err),
							CreatedAt:  time.Now(),
						})
						inst.hub.BroadcastJSON(map[string]interface{}{
							"type":        "error",
							"strategy_id": inst.ID,
							"owner_id":    inst.OwnerID,
							"error":       fmt.Sprintf("SetLeverage error: %v", err),
						})
					}
				}
			}

			order, err := inst.exchange.PlaceOrder(inst.OwnerID, clientOrderID, symbol, side, amount, price)
			if err != nil {
				database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
					Updates(map[string]interface{}{"status": "failed", "updated_at": time.Now()})
				logger.Errorf("[ORDER ERROR] strategy=%s owner=%d client_order_id=%s symbol=%s side=%s amount=%v price=%v err=%v", inst.ID, inst.OwnerID, clientOrderID, symbol, normalizedSide, amount, price, err)
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
				_ = inst.SendData("order", map[string]interface{}{
					"id":              clientOrderID,
					"client_order_id": clientOrderID,
					"symbol":          symbol,
					"side":            normalizedSide,
					"amount":          amount,
					"price":           price,
					"status":          "failed",
					"timestamp":       time.Now(),
				})
			} else {
				database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
					Updates(map[string]interface{}{
						"exchange_order_id": order.ID,
						"status":            order.Status,
						"executed_qty":      order.Amount,
						"avg_price":         order.Price,
						"updated_at":        time.Now(),
					})
				logger.Infof("[ORDER OK] strategy=%s owner=%d client_order_id=%s exchange_order_id=%s status=%s filled_qty=%v avg_price=%v", inst.ID, inst.OwnerID, clientOrderID, order.ID, order.Status, order.Amount, order.Price)
				inst.hub.BroadcastJSON(map[string]interface{}{
					"type": "order",
					"data": order,
				})
				inst.SendData("order", order)

				if strings.ToLower(order.Status) == "filled" {
					applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), symbol, normalizedSide, order.Amount, order.Price, order.Timestamp)
					if normalizedSide == "sell" {
						mode := ""
						if raw, ok := inst.Config["symbol_mode"].(string); ok {
							mode = strings.ToLower(strings.TrimSpace(raw))
						}
						if mode == "selector" && inst.mgr != nil && inst.selectorActiveSymbol != "" && exchange.NormalizeSymbol(inst.selectorActiveSymbol) == exchange.NormalizeSymbol(symbol) {
							var openCount int64
							database.DB.Model(&models.StrategyPosition{}).
								Where("owner_id = ? AND strategy_id = ? AND status = ?", inst.OwnerID, inst.ID, "open").
								Count(&openCount)
							if openCount == 0 {
								inst.selectorLastSymbol = inst.selectorActiveSymbol
								inst.selectorActiveSymbol = ""
								inst.Config["active_symbol"] = ""
								go func() {
									time.Sleep(200 * time.Millisecond)
									_ = inst.mgr.StopStrategy(inst.ID, false)
									_ = inst.mgr.StartStrategy(inst.ID)
								}()
							}
						}
					}
				}
			}
		case "log":
			logger.Infof("[%s LOG] %v", inst.Name, data)

			// Save to DB
			logMsg, _ := data.(string)
			database.DB.Create(&models.StrategyLog{
				StrategyID: inst.ID,
				Level:      "info",
				Message:    logMsg,
				CreatedAt:  time.Now(),
			})

			inst.hub.BroadcastJSON(map[string]interface{}{
				"type": "log",
				"data": data,
				"id":   inst.ID,
			})

		}
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
			if needsWrite && strings.TrimSpace(inst.Template.Code) != "" {
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
					absPath := filepath.Join(absDir, filename)
					if err := os.WriteFile(absPath, []byte(inst.Template.Code), 0o644); err == nil {
						path = absPath
						_ = database.DB.Model(&models.StrategyTemplate{}).Where("id = ?", inst.Template.ID).
							Updates(map[string]interface{}{"path": absPath, "updated_at": time.Now()}).Error
						logger.Infof("[SYNC PATH FIX] template_id=%d path=%s", inst.Template.ID, absPath)
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

func (m *Manager) SendToStrategy(id string, dataType string, data interface{}) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}
	return inst.SendData(dataType, data)
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
