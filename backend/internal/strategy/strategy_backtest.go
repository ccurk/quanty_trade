package strategy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
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

	// Mirror the live start path (strategy_start.go): strategies abort with
	// "missing strategy_id" unless it is injected into the runtime config.
	// inst.Config holds only user params (symbol/windows/...), not identity.
	runCfg := make(map[string]interface{}, len(inst.Config)+2)
	for k, v := range inst.Config {
		runCfg[k] = v
	}
	runCfg["strategy_id"] = id
	runCfg["owner_id"] = inst.OwnerID
	configJSON, _ := json.Marshal(runCfg)
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
	// Surface the strategy subprocess's stderr. Without this a strategy that
	// crashes on startup (e.g. a Python exception) looks like a clean backtest
	// with 0 trades, which is how several wiring bugs stayed invisible.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer cmd.Process.Kill()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			emitStrategyLog(inst, "error", "[backtest python stderr] "+scanner.Text())
		}
	}()

	balance := initialBalance
	positionAmount := 0.0 // signed: >0 long, <0 short
	positionMargin := 0.0
	entryPrice := 0.0
	posTP := 0.0
	posSL := 0.0
	totalTrades := 0
	totalProfit := 0.0
	totalFees := 0.0
	equityCurve := make([]EquityPoint, 0)

	// Commission per fill, charged on notional (amount*price) regardless of
	// leverage — matches Binance USDM. Configurable; default ~taker fee.
	// Without this a backtest overstates profit exactly where real accounts
	// bleed (the live strategy lost ~23%, almost all of it fees).
	takerFee := 0.0004
	if raw, ok := inst.Config["taker_fee"]; ok {
		if v, ok := raw.(float64); ok && v >= 0 {
			takerFee = v
		}
	}

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
		candleMsg := map[string]interface{}{"type": "candle", "data": map[string]interface{}{
			"symbol":    symbol,
			"timestamp": candle.Timestamp,
			"open":      candle.Open,
			"high":      candle.High,
			"low":       candle.Low,
			"close":     candle.Close,
			"volume":    candle.Volume,
		}}
		json.NewEncoder(stdin).Encode(candleMsg)
		time.Sleep(10 * time.Millisecond)

		// Bracket exit: close the open position when this candle's range hits TP/SL.
		// Live runs delegate exits to the backend TP/SL monitor; without modelling
		// it here the backtest would just accumulate and mark-to-market (which made a
		// pumped meme look like +800%). Stop is checked before target (pessimistic).
		if positionAmount != 0 {
			exit, exitPrice := false, 0.0
			if positionAmount > 0 { // long
				if posSL > 0 && candle.Low <= posSL {
					exit, exitPrice = true, posSL
				} else if posTP > 0 && candle.High >= posTP {
					exit, exitPrice = true, posTP
				}
			} else { // short
				if posSL > 0 && candle.High >= posSL {
					exit, exitPrice = true, posSL
				} else if posTP > 0 && candle.Low <= posTP {
					exit, exitPrice = true, posTP
				}
			}
			if exit {
				amt := math.Abs(positionAmount)
				pnl := amt * (exitPrice - entryPrice)
				if positionAmount < 0 {
					pnl = amt * (entryPrice - exitPrice)
				}
				fee := amt * exitPrice * takerFee
				balance += positionMargin + pnl - fee
				totalFees += fee
				totalTrades++
				positionAmount, positionMargin, entryPrice, posTP, posSL = 0, 0, 0, 0, 0
			}
		}

		select {
		case orderReq := <-orderChan:
			side, _ := orderReq["side"].(string)
			amount, _ := orderReq["amount"].(float64)
			tp, _ := orderReq["take_profit"].(float64)
			sl, _ := orderReq["stop_loss"].(float64)
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
			// One position per symbol at a time (matches the backend's single-slot +
			// per-symbol cooldown model). New opens are ignored while in a position.
			if positionAmount == 0 && amount > 0 && (side == "buy" || side == "sell") {
				requiredMargin := (amount * price) / float64(lev)
				if balance >= requiredMargin {
					fee := amount * price * takerFee
					balance -= requiredMargin + fee
					totalFees += fee
					positionMargin = requiredMargin
					entryPrice = price
					posTP = tp
					posSL = sl
					if side == "buy" {
						positionAmount = amount
					} else {
						positionAmount = -amount
					}
					totalTrades++
				}
			}
		default:
		}

		currentEquity := balance
		if positionAmount > 0 {
			currentEquity = balance + positionMargin + positionAmount*(candle.Close-entryPrice)
		} else if positionAmount < 0 {
			currentEquity = balance + positionMargin + (-positionAmount)*(entryPrice-candle.Close)
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

	// Mark any still-open position to the last close (margin returned).
	lastClose := candles[len(candles)-1].Close
	finalBalance := balance
	if positionAmount > 0 {
		finalBalance = balance + positionMargin + positionAmount*(lastClose-entryPrice)
	} else if positionAmount < 0 {
		finalBalance = balance + positionMargin + (-positionAmount)*(entryPrice-lastClose)
	}
	totalProfit = finalBalance - initialBalance
	returnRate := (totalProfit / initialBalance) * 100
	return &BacktestResult{
		TotalTrades:    totalTrades,
		TotalProfit:    totalProfit,
		TotalFees:      totalFees,
		ReturnRate:     returnRate,
		InitialBalance: initialBalance,
		FinalBalance:   finalBalance,
		EquityCurve:    equityCurve,
	}, nil
}
