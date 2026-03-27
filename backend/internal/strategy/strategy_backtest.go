package strategy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
)

// StartBacktest starts an asynchronous backtest and returns the Backtest row id.
func (m *Manager) StartBacktest(id string, startTime, endTime time.Time, initialBalance float64, userID uint) (uint, error) {
	m.mu.RLock()
	_, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("strategy %s not found", id)
	}

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
func (m *Manager) Backtest(id string, startTime, endTime time.Time, initialBalance float64, userID uint) (*BacktestResult, error) {
	m.mu.RLock()
	_, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("strategy %s not found", id)
	}

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
	candles, err := m.exchange.FetchHistoricalCandles(symbol, "1h", startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %v", err)
	}
	if len(candles) == 0 {
		return nil, fmt.Errorf("no historical data found for the given time range")
	}

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

	balance := initialBalance
	positionAmount := 0.0
	positionMargin := 0.0
	entryPrice := 0.0
	totalTrades := 0
	totalProfit := 0.0
	equityCurve := make([]EquityPoint, 0)

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

	lastProgressEmit := time.Now()
	for _, candle := range candles {
		candleMsg := map[string]interface{}{"type": "candle", "data": candle}
		json.NewEncoder(stdin).Encode(candleMsg)
		time.Sleep(10 * time.Millisecond)

		select {
		case orderReq := <-orderChan:
			side, _ := orderReq["side"].(string)
			amount, _ := orderReq["amount"].(float64)
			price := candle.Close
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
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{"type": "order", "data": map[string]interface{}{"id": simOrderID, "client_order_id": simOrderID, "symbol": symbol, "side": "buy", "amount": amount, "price": price, "status": "filled", "timestamp": candle.Timestamp}})
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{"type": "position", "data": map[string]interface{}{"symbol": symbol, "qty": positionAmount, "avg_price": entryPrice, "status": "open"}})
				} else {
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{"type": "order", "data": map[string]interface{}{"id": simOrderID, "client_order_id": simOrderID, "symbol": symbol, "side": "buy", "amount": amount, "price": price, "status": "rejected", "timestamp": candle.Timestamp}})
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
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{"type": "order", "data": map[string]interface{}{"id": simOrderID, "client_order_id": simOrderID, "symbol": symbol, "side": "sell", "amount": amount, "price": price, "status": "filled", "timestamp": candle.Timestamp}})
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{"type": "position", "data": map[string]interface{}{"symbol": symbol, "qty": positionAmount, "avg_price": entryPrice, "status": func() string {
						if positionAmount > 0 {
							return "open"
						}
						return "closed"
					}()}})
				} else {
					_ = json.NewEncoder(stdin).Encode(map[string]interface{}{"type": "order", "data": map[string]interface{}{"id": simOrderID, "client_order_id": simOrderID, "symbol": symbol, "side": "sell", "amount": amount, "price": price, "status": "rejected", "timestamp": candle.Timestamp}})
				}
			}
		default:
		}

		currentEquity := balance
		if positionAmount > 0 {
			currentEquity = balance + positionMargin + (positionAmount * (candle.Close - entryPrice))
		}
		equityCurve = append(equityCurve, EquityPoint{Timestamp: candle.Timestamp, Equity: currentEquity})
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
