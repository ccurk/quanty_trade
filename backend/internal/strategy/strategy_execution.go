package strategy

import (
	"fmt"
	"math"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

func resolveUSDMOrderAmount(inst *StrategyInstance, bx *exchange.BinanceExchange, symbol string, amount float64, price float64) (float64, error) {
	if inst == nil || bx == nil {
		return 0, nil
	}
	lev := int(getNumber(inst.Config["leverage"]))
	if lev <= 0 {
		lev = 1
	}
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

	px, err := getPx()
	if err != nil || px <= 0 {
		emitStrategyLog(inst, "error", fmt.Sprintf("跳过开仓：获取价格失败 symbol=%s err=%v", symbol, err))
		return 0, fmt.Errorf("price unavailable")
	}
	avail := 0.0
	if v, err := bx.USDMAvailableUSDT(inst.OwnerID); err == nil && v > 0 {
		avail = v
	}
	desiredNotional := amount * px
	if mode == "percent_balance" {
		pct := getNumber(inst.Config["order_amount_pct"])
		if pct <= 0 {
			pct = amount / 100
		}
		if pct > 1 {
			pct = 1
		}
		initial := avail * pct
		maxInit := getNumber(inst.Config["max_initial_margin_usdt"])
		if maxInit > 0 && initial > maxInit {
			initial = maxInit
		}
		if initial <= 0 {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：按余额百分比计算后的初始保证金<=0 symbol=%s", symbol))
			return 0, nil
		}
		desiredNotional = initial * float64(lev)
	} else if mode == "notional" {
		desiredNotional = amount
	}
	if desiredNotional < minNotional {
		desiredNotional = minNotional
	}
	levChosen := lev
	finalNotional := 0.0
	for l := lev; l >= 1; l-- {
		availCap := 0.0
		if avail > 0 {
			availCap = avail * float64(l) * 0.95
		}
		remCap := 0.0
		if capN, err := bx.USDMMaxNotionalForLeverage(inst.OwnerID, symbol, l); err == nil && capN > 0 {
			if posAmt, _, markPx, e2 := bx.USDMPositionAmt(inst.OwnerID, symbol); e2 == nil && markPx > 0 && posAmt != 0 {
				curN := math.Abs(posAmt) * markPx
				rem := capN - curN
				if rem > 0 {
					remCap = rem * 0.98
				}
			} else {
				remCap = capN * 0.98
			}
		}
		maxNotional := 0.0
		if availCap > 0 && remCap > 0 {
			if availCap < remCap {
				maxNotional = availCap
			} else {
				maxNotional = remCap
			}
		} else if availCap > 0 {
			maxNotional = availCap
		} else if remCap > 0 {
			maxNotional = remCap
		}
		if maxNotional <= 0 {
			continue
		}
		if maxNotional >= desiredNotional {
			levChosen = l
			finalNotional = desiredNotional
			break
		}
		if maxNotional >= minNotional {
			levChosen = l
			finalNotional = maxNotional
			break
		}
	}
	if finalNotional <= 0 {
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：当前杠杆档位剩余额度不足 symbol=%s desired=%0.4f min=%0.4f lev=%d", symbol, desiredNotional, minNotional, lev))
		return 0, nil
	}
	if levChosen != lev {
		_ = bx.SetLeverage(inst.OwnerID, symbol, levChosen)
		emitStrategyLog(inst, "info", fmt.Sprintf("自动调整杠杆：因档位上限约束 symbol=%s lev=%d->%d", symbol, lev, levChosen))
	}
	amount = finalNotional / px
	amount = clampOrderAmount(inst, amount)
	if amount <= 0 {
		return 0, nil
	}
	if amount*px < minNotional {
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：名义价值过小 symbol=%s notional=%0.4f min_notional=%0.2f", symbol, amount*px, minNotional))
		return 0, nil
	}
	return amount, nil
}

func normalizedTPSLPct(inst *StrategyInstance, key string) float64 {
	if inst == nil {
		return 0
	}
	pct := getNumber(inst.Config[key])
	if pct > 1 {
		pct = pct / 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}

func hasEffectiveTPSL(inst *StrategyInstance, takeProfit float64, stopLoss float64) bool {
	if takeProfit > 0 && stopLoss > 0 {
		return true
	}
	return normalizedTPSLPct(inst, "take_profit_pct") > 0 && normalizedTPSLPct(inst, "stop_loss_pct") > 0
}

func resolveTPSLFromROI(inst *StrategyInstance, side string, entryPrice float64, takeProfit float64, stopLoss float64) (float64, float64) {
	if inst == nil || entryPrice <= 0 {
		return takeProfit, stopLoss
	}
	lev := int(getNumber(inst.Config["leverage"]))
	if lev <= 0 {
		lev = 1
	}
	dir := strings.ToLower(strings.TrimSpace(side))
	if dir == "long" {
		dir = "buy"
	}
	if dir == "short" {
		dir = "sell"
	}
	if dir != "buy" && dir != "sell" {
		return takeProfit, stopLoss
	}

	tpPct := normalizedTPSLPct(inst, "take_profit_pct")
	slPct := normalizedTPSLPct(inst, "stop_loss_pct")
	if tpPct <= 0 && slPct <= 0 {
		return takeProfit, stopLoss
	}

	offset := func(pct float64) float64 {
		if pct <= 0 {
			return 0
		}
		return pct / float64(lev)
	}

	if off := offset(tpPct); off > 0 {
		if dir == "buy" {
			takeProfit = entryPrice * (1 + off)
		} else {
			takeProfit = entryPrice * (1 - off)
		}
	}
	if off := offset(slPct); off > 0 {
		if dir == "buy" {
			stopLoss = entryPrice * (1 - off)
		} else {
			stopLoss = entryPrice * (1 + off)
		}
	}
	return takeProfit, stopLoss
}

func resolveHungerMode(inst *StrategyInstance) (bool, time.Duration, float64, float64) {
	if inst == nil {
		return false, 0, 0, 0
	}
	enabled := true
	if _, ok := inst.Config["hunger_mode_enabled"]; ok {
		enabled = getBool(inst.Config["hunger_mode_enabled"])
	}
	afterMinutes := int(getNumber(inst.Config["hunger_after_minutes"]))
	if afterMinutes <= 0 {
		afterMinutes = 30
	}

	derivePct := func(raw float64, fallbackKey string) float64 {
		if raw > 0 {
			return raw
		}
		base := normalizedTPSLPct(inst, fallbackKey)
		if base > 0 && base < 0.03 {
			return base
		}
		return 0.03
	}

	tpPct := derivePct(normalizedTPSLPct(inst, "hunger_take_profit_pct"), "take_profit_pct")
	slPct := derivePct(normalizedTPSLPct(inst, "hunger_stop_loss_pct"), "stop_loss_pct")
	return enabled, time.Duration(afterMinutes) * time.Minute, tpPct, slPct
}

func (m *Manager) closeUSDMPosition(inst *StrategyInstance, bx *exchange.BinanceExchange, sym string) error {
	m.stopPositionTPStopMonitor(inst, sym)
	if err := bx.CancelUSDMAllSymbolOrders(inst.OwnerID, sym); err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("平仓前撤销该交易对全部委托失败 symbol=%s err=%v", sym, err))
	}
	order, _, _, err := bx.ClosePositionOrder(sym, inst.OwnerID)
	if err != nil {
		return err
	}
	if order == nil {
		if err := bx.CancelUSDMAllSymbolOrders(inst.OwnerID, sym); err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("平仓后撤销该交易对全部委托失败 symbol=%s err=%v", sym, err))
		}
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
	go func(ownerID uint, symbol string) {
		if err := bx.CancelUSDMAllSymbolOrders(ownerID, symbol); err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("平仓后立即撤销该交易对全部委托失败 symbol=%s err=%v", symbol, err))
		}
	}(inst.OwnerID, sym)
	go func(ownerID uint, symbol string) {
		deadline := time.Now().Add(45 * time.Second)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for time.Now().Before(deadline) {
			amt, _, _, e := bx.USDMPositionAmt(ownerID, symbol)
			if e == nil && amt == 0 {
				if err := bx.CancelUSDMAllSymbolOrders(ownerID, symbol); err != nil {
					emitStrategyLog(inst, "error", fmt.Sprintf("仓位归零后撤销该交易对全部委托失败 symbol=%s err=%v", symbol, err))
				}
				return
			}
			<-ticker.C
		}
	}(inst.OwnerID, sym)
	return nil
}

func (m *Manager) closeSpotPosition(inst *StrategyInstance, sym string) error {
	m.stopPositionTPStopMonitor(inst, sym)
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
	return nil
}
