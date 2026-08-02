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

// resolveBacktestMarket picks the symbol/timeframe a simulation will run on.
// Symbol precedence: request > config["symbol"]. Auto-symbol strategies keep
// config["symbol"] empty, which is why every backtest of such a strategy used
// to fail before the request could override it.
func (m *Manager) resolveBacktestMarket(id string, reqSymbol, reqTimeframe string) (string, string, error) {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return "", "", fmt.Errorf("strategy %s not found", id)
	}
	symbol := reqSymbol
	if symbol == "" {
		symbol, _ = inst.Config["symbol"].(string)
	}
	if symbol == "" {
		return "", "", fmt.Errorf("strategy has no fixed symbol (auto_symbols mode): pass \"symbol\" (e.g. \"BTC/USDT\") in the backtest request body")
	}
	timeframe := reqTimeframe
	if timeframe == "" {
		timeframe = "1m"
	}
	return symbol, timeframe, nil
}

// StartBacktest starts an asynchronous backtest and returns the Backtest row id.
func (m *Manager) StartBacktest(id string, symbol, timeframe string, startTime, endTime time.Time, initialBalance float64, userID uint) (uint, error) {
	symbol, timeframe, err := m.resolveBacktestMarket(id, symbol, timeframe)
	if err != nil {
		return 0, err
	}

	bt := &models.Backtest{
		StrategyID:     id,
		StartTime:      startTime,
		EndTime:        endTime,
		InitialBalance: initialBalance,
		Status:         "pending",
		Symbol:         symbol,
		Timeframe:      timeframe,
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

		result, err := m.runBacktestSimulation(id, symbol, timeframe, startTime, endTime, initialBalance, userID, bt.ID)
		if err != nil {
			bt.Status = "failed"
			bt.Error = err.Error()
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
func (m *Manager) Backtest(id string, symbol, timeframe string, startTime, endTime time.Time, initialBalance float64, userID uint) (*BacktestResult, error) {
	symbol, timeframe, err := m.resolveBacktestMarket(id, symbol, timeframe)
	if err != nil {
		return nil, err
	}

	bt := &models.Backtest{
		StrategyID:     id,
		StartTime:      startTime,
		EndTime:        endTime,
		InitialBalance: initialBalance,
		Status:         "running",
		Symbol:         symbol,
		Timeframe:      timeframe,
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

	result, err := m.runBacktestSimulation(id, symbol, timeframe, startTime, endTime, initialBalance, userID, bt.ID)
	if err != nil {
		bt.Status = "failed"
		bt.Error = err.Error()
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

func (m *Manager) runBacktestSimulation(id string, symbol, timeframe string, startTime, endTime time.Time, initialBalance float64, userID uint, backtestID uint) (*BacktestResult, error) {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("strategy %s not found", id)
	}

	if symbol == "" {
		return nil, fmt.Errorf("backtest symbol not resolved")
	}
	if timeframe == "" {
		timeframe = "1m"
	}
	candles, err := m.exchange.FetchHistoricalCandles(symbol, timeframe, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data: %v", err)
	}
	if len(candles) == 0 {
		return nil, fmt.Errorf("no historical data found for the given time range")
	}

	// Mirror the live start path (strategy_start.go): strategies abort with
	// "missing strategy_id" unless it is injected into the runtime config.
	// inst.Config holds only user params (symbol/windows/...), not identity.
	runCfg := make(map[string]interface{}, len(inst.Config)+3)
	for k, v := range inst.Config {
		runCfg[k] = v
	}
	runCfg["strategy_id"] = id
	runCfg["owner_id"] = inst.OwnerID
	// Let the strategy know it is being simulated. Wall-clock driven logic
	// (cooldowns, best-pick batching) can key off this to use candle time —
	// simulated feeds compress hours into seconds, so wall-clock timers that
	// are correct live would otherwise freeze trading after the first entry.
	runCfg["backtest"] = true
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
	var entryTime time.Time
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

	lev := 1
	if raw, ok := inst.Config["leverage"]; ok {
		if v, ok := raw.(float64); ok && int(v) > 0 {
			lev = int(v)
		}
	}

	// Hold-based exits, mirroring the live engine's three exit layers
	// (quick_trade_monitor.go): the platform TP/SL bracket, the hunger-mode
	// harvest and the unconditional max-hold timeout. Hold duration uses candle
	// time, matching the wall-clock semantics of the live monitor. Before these
	// were modelled, a backtest exercised only layer ① — useless for judging
	// exit-system changes, which live entirely in layers ② and ③.
	hungerEnabled, hungerAfter, hungerTPPct, hungerSLPct := resolveHungerMode(inst)
	maxHold := resolveMaxHoldTimeout(inst)

	closePosition := func(exitPrice float64) {
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
		entryTime = time.Time{}
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

		// Exit layer ①: close the open position when this candle's range hits
		// TP/SL. Live runs delegate this to the backend TP/SL monitor; without
		// modelling it here the backtest would just accumulate and mark-to-market
		// (which made a pumped meme look like +800%). Stop is checked before
		// target (pessimistic).
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
				closePosition(exitPrice)
			}
		}

		// Exit layers ② and ③, evaluated at candle close like the live 10s tick.
		// Hard timeout runs first (unconditional), then hunger mode: after
		// hunger_after the position is harvested once |roi| crosses the hunger
		// thresholds, where roi is margin-relative (price move × leverage).
		if positionAmount != 0 && !entryTime.IsZero() {
			held := candle.Timestamp.Sub(entryTime)
			if maxHold > 0 && held >= maxHold {
				closePosition(candle.Close)
			} else if hungerEnabled && hungerAfter > 0 && held >= hungerAfter {
				movePct := (candle.Close - entryPrice) / entryPrice * 100
				if positionAmount < 0 {
					movePct = -movePct
				}
				roi := movePct * float64(lev)
				if (hungerTPPct > 0 && roi >= hungerTPPct*100) || (hungerSLPct > 0 && roi <= -hungerSLPct*100) {
					closePosition(candle.Close)
				}
			}
		}

		select {
		case orderReq := <-orderChan:
			side, _ := orderReq["side"].(string)
			amount, _ := orderReq["amount"].(float64)
			tp, _ := orderReq["take_profit"].(float64)
			sl, _ := orderReq["stop_loss"].(float64)
			price := candle.Close
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
					entryTime = candle.Timestamp
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
