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
		emitStrategyLog(inst, "error", fmt.Sprintf("Skip order: 获取价格失败 symbol=%s err=%v", symbol, err))
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
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip order: percent_balance computed initial margin <=0 symbol=%s", symbol))
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
		emitStrategyLog(inst, "info", fmt.Sprintf("Skip order: leverage bracket remaining cap insufficient symbol=%s desired=%0.4f min=%0.4f lev=%d", symbol, desiredNotional, minNotional, lev))
		return 0, nil
	}
	if levChosen != lev {
		_ = bx.SetLeverage(inst.OwnerID, symbol, levChosen)
		emitStrategyLog(inst, "info", fmt.Sprintf("Auto adjust leverage due to bracket cap symbol=%s lev=%d->%d", symbol, lev, levChosen))
	}
	amount = finalNotional / px
	amount = clampOrderAmount(inst, amount)
	if amount <= 0 {
		return 0, nil
	}
	if amount*px < minNotional {
		emitStrategyLog(inst, "info", fmt.Sprintf("Skip order: notional too small symbol=%s notional=%0.4f min_notional=%0.2f", symbol, amount*px, minNotional))
		return 0, nil
	}
	return amount, nil
}

func (m *Manager) closeUSDMPosition(inst *StrategyInstance, bx *exchange.BinanceExchange, sym string) error {
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
	go func(ownerID uint, symbol string) {
		deadline := time.Now().Add(45 * time.Second)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for time.Now().Before(deadline) {
			amt, _, _, e := bx.USDMPositionAmt(ownerID, symbol)
			if e == nil && amt == 0 {
				_ = bx.CancelUSDMAlgoOpenOrders(ownerID, symbol)
				_ = bx.CancelPrePositionOpenOrders(ownerID, symbol)
				return
			}
			<-ticker.C
		}
	}(inst.OwnerID, sym)
	return nil
}

func (m *Manager) closeSpotPosition(inst *StrategyInstance, sym string) error {
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
