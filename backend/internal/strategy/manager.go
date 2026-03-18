package strategy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
	"quanty_trade/internal/ws"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

func resolveStrategyPath(p string) (string, error) {
	if filepath.IsAbs(p) {
		return p, nil
	}
	base := os.Getenv("STRATEGIES_DIR")
	if base == "" {
		return filepath.Abs(p)
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	joined := filepath.Clean(filepath.Join(absBase, p))
	rel, err := filepath.Rel(absBase, joined)
	if err != nil {
		return "", err
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
		return joined, nil
	}
	return "", fmt.Errorf("invalid strategy path: %s", p)
}

type StrategyStatus string

const (
	StatusRunning StrategyStatus = "running"
	StatusStopped StrategyStatus = "stopped"
	StatusError   StrategyStatus = "error"
)

type StrategyInstance struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Path      string                 `json:"path"`
	Config    map[string]interface{} `json:"config"`
	Status    StrategyStatus         `json:"status"`
	OwnerID   uint                   `json:"owner_id"`
	CreatedAt time.Time              `json:"created_at"`
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	mu        sync.Mutex
	hub       *ws.Hub
	exchange  exchange.Exchange
}

type Manager struct {
	instances map[string]*StrategyInstance
	mu        sync.RWMutex
	hub       *ws.Hub
	exchange  exchange.Exchange
}

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

func NewManager(hub *ws.Hub, ex exchange.Exchange) *Manager {
	return &Manager{
		instances: make(map[string]*StrategyInstance),
		hub:       hub,
		exchange:  ex,
	}
}

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
	}
	m.instances[id] = inst
	return inst
}

func (m *Manager) StartStrategy(id string) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.Status == StatusRunning {
		return nil
	}

	configJSON, _ := json.Marshal(inst.Config)

	absPath, err := resolveStrategyPath(inst.Path)
	if err != nil {
		return err
	}
	cmd := exec.Command("python3", absPath, string(configJSON))
	cmd.Dir = filepath.Dir(absPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	inst.cmd = cmd
	inst.stdin = stdin
	inst.stdout = stdout
	inst.Status = StatusRunning

	// Start data feed
	symbol, _ := inst.Config["symbol"].(string)
	if symbol != "" {
		go inst.exchange.SubscribeCandles(symbol, func(candle exchange.Candle) {
			inst.SendData("candle", candle)
		})
	}

	go inst.readStdout()
	go inst.readStderr(stderr)
	return nil
}

func (inst *StrategyInstance) readStderr(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		fmt.Printf("[%s ERROR] %s\n", inst.Name, scanner.Text())
	}
}

func (m *Manager) StopStrategy(id string) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("strategy %s not found", id)
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
	scanner := bufio.NewScanner(inst.stdout)
	for scanner.Scan() {
		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			fmt.Printf("Error decoding strategy output: %v\n", err)
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

			order, err := inst.exchange.PlaceOrder(symbol, side, amount, price)
			if err != nil {
				inst.hub.BroadcastJSON(map[string]interface{}{
					"type":  "error",
					"error": fmt.Sprintf("Failed to place order: %v", err),
				})
			} else {
				inst.hub.BroadcastJSON(map[string]interface{}{
					"type": "order",
					"data": order,
				})
				inst.SendData("order", order)
			}
		case "log":
			fmt.Printf("[%s LOG] %v\n", inst.Name, data)

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

			m.instances[inst.ID] = &StrategyInstance{
				ID:        inst.ID,
				Name:      inst.Name,
				Path:      inst.Template.Path,
				Config:    config,
				Status:    StatusStopped,
				OwnerID:   inst.OwnerID,
				CreatedAt: inst.CreatedAt,
				hub:       m.hub,
				exchange:  m.exchange,
			}

		}
	}
	return nil
}

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

func (m *Manager) GetExchange() exchange.Exchange {
	return m.exchange
}

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

		result, err := m.runBacktestSimulation(id, startTime, endTime, initialBalance)
		if err != nil {
			bt.Status = "failed"
			database.DB.Save(bt)
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
	}()

	return bt.ID, nil
}

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

	result, err := m.runBacktestSimulation(id, startTime, endTime, initialBalance)
	if err != nil {
		bt.Status = "failed"
		database.DB.Save(bt)
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

	return result, nil
}

func (m *Manager) runBacktestSimulation(id string, startTime, endTime time.Time, initialBalance float64) (*BacktestResult, error) {
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

			if side == "buy" {
				cost := amount * price
				if balance >= cost {
					balance -= cost
					positionAmount += amount
					totalTrades++
				}
			} else if side == "sell" {
				if positionAmount >= amount {
					balance += amount * price
					positionAmount -= amount
					totalTrades++
				}
			}
		default:
			// No order this time
		}

		// Calculate current equity
		currentEquity := balance + (positionAmount * candle.Close)
		equityCurve = append(equityCurve, EquityPoint{
			Timestamp: candle.Timestamp,
			Equity:    currentEquity,
		})
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
