package strategy

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"quanty_trade/internal/bus"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

func (m *Manager) runOrderWorker() {
	for req := range m.orderCh {
		func() {
			defer func() { recover() }()
			m.placeOrderForInstance(req.inst, req.symbol, req.side, req.amount, req.price, req.takeProfit, req.stopLoss, req.signalID)
		}()
	}
}

func (m *Manager) placeOrderForInstance(inst *StrategyInstance, symbol string, side string, amount float64, price float64, takeProfit float64, stopLoss float64, signalID string) {
	if inst == nil {
		return
	}
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
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：交易对不在允许列表 symbol=%s", symbol))
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
	if !hasEffectiveTPSL(inst, takeProfit, stopLoss) {
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：缺少止盈止损，拒绝开仓 symbol=%s side=%s amount=%v tp=%v sl=%v", symbol, normalizedSide, amount, takeProfit, stopLoss))
		return
	}

	if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		if err := bx.SupportsSymbol(symbol); err != nil {
			inst.orderMu.Lock()
			if inst.invalidSymbol == nil {
				inst.invalidSymbol = map[string]time.Time{}
			}
			inst.invalidSymbol[exchange.NormalizeSymbol(symbol)] = time.Now().Add(10 * time.Minute)
			inst.orderMu.Unlock()
			emitStrategyLog(inst, "error", fmt.Sprintf("跳过开仓：当前市场不支持该交易对 symbol=%s err=%v", symbol, err))
			return
		}
		resolvedAmount, err := resolveUSDMOrderAmount(inst, bx, symbol, amount, price)
		if err != nil || resolvedAmount <= 0 {
			return
		}
		amount = resolvedAmount
	}

	maxPos := 1
	if v := int(getNumber(inst.Config["max_concurrent_positions"])); v > 0 {
		maxPos = v
	}

	var rb *bus.RedisBus
	if inst.mgr != nil {
		inst.mgr.mu.RLock()
		rb = inst.mgr.redisBus
		inst.mgr.mu.RUnlock()
	}

	strategyOpenCount := int64(0)
	accountSymbolOpen := false
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
				emitStrategyLog(inst, "error", fmt.Sprintf("跳过开仓：当前市场不支持该交易对 symbol=%s", symbol))
				return
			}
			inst.orderMu.Unlock()
			return
		}
	}
	inst.orderMu.Unlock()

	var openRows []models.StrategyPosition
	_ = database.DB.Where("owner_id = ? AND strategy_id = ? AND status = ?", inst.OwnerID, inst.ID, "open").Find(&openRows).Error
	openSymbols := map[string]struct{}{}
	for _, row := range openRows {
		key := exchange.NormalizeSymbol(row.Symbol)
		if key == "" {
			continue
		}
		openSymbols[key] = struct{}{}
		if key == exSymbolKey {
			accountSymbolOpen = true
		}
	}
	strategyOpenCount = int64(len(openSymbols))
	var pendingOrders []models.StrategyOrder
	pendingCutoff := time.Now().Add(-2 * time.Minute)
	_ = database.DB.Where("owner_id = ? AND strategy_id = ? AND requested_at >= ? AND status IN ?", inst.OwnerID, inst.ID, pendingCutoff, []string{"requested", "new", "partially_filled"}).
		Order("requested_at desc").
		Find(&pendingOrders).Error
	pendingSymbols := map[string]struct{}{}
	for _, ord := range pendingOrders {
		key := exchange.NormalizeSymbol(ord.Symbol)
		if key == "" {
			continue
		}
		if _, ok := openSymbols[key]; ok {
			continue
		}
		pendingSymbols[key] = struct{}{}
		if key == exSymbolKey {
			accountSymbolOpen = true
		}
	}
	strategyOpenCount += int64(len(pendingSymbols))
	if ps, err := inst.exchange.FetchPositions(inst.OwnerID, "active"); err == nil {
		for _, p := range ps {
			if math.Abs(p.Amount) <= 0 {
				continue
			}
			if exchange.NormalizeSymbol(p.Symbol) == exSymbolKey {
				accountSymbolOpen = true
			}
		}
	}
	if maxPos > 0 && strategyOpenCount >= int64(maxPos) {
		inst.orderMu.Lock()
		if inst.lastSkipLogAt == nil {
			inst.lastSkipLogAt = map[string]time.Time{}
		}
		k := "max_pos_exchange"
		if t, ok := inst.lastSkipLogAt[k]; ok && time.Since(t) < 10*time.Second {
			inst.orderMu.Unlock()
			return
		}
		inst.lastSkipLogAt[k] = time.Now()
		inst.orderMu.Unlock()
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：该策略当前已持仓%d个，达到最大并发仓位%d strategy=%s symbol=%s", strategyOpenCount, maxPos, inst.ID, symbol))
		return
	}
	if accountSymbolOpen {
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
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：该交易对在账户中已有持仓 symbol=%s", symbol))
		return
	}

	acquiredSlot := false
	if maxPos > 0 && rb != nil {
		ok, _, err := rb.AcquireOpenSlot(context.Background(), inst.ID, maxPos, 6*time.Hour)
		if err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("跳过开仓：获取并发仓位锁失败 err=%v", err))
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
			redisOpenCount, _ := rb.GetOpenCount(context.Background(), inst.ID)
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：达到最大并发仓位 strategy=%s symbol=%s 本地占用=%d Redis占用=%d 最大=%d", inst.ID, symbol, strategyOpenCount, redisOpenCount, maxPos))
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

		errMsg := fmt.Sprintf("开仓失败：%v", err)
		if strings.Contains(errMsg, "\"code\":-2019") {
			errMsg = fmt.Sprintf("开仓失败：保证金不足 (%v)", err)
		} else if strings.Contains(errMsg, "\"code\":-4164") {
			errMsg = fmt.Sprintf("开仓失败：notional 小于最小下单额 (%v)", err)
		} else if strings.Contains(errMsg, "\"code\":-2027") {
			lev := int(getNumber(inst.Config["leverage"]))
			if lev <= 0 {
				lev = 1
			}
			errMsg = fmt.Sprintf("开仓失败：当前杠杆下持仓上限超出，请降低杠杆或下单金额(数量) lev=%d (%v)", lev, err)
		} else if strings.Contains(errMsg, "\"code\":-1003") {
			errMsg = fmt.Sprintf("开仓失败：IP 限流/封禁 (%v)", err)
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
			errMsg = fmt.Sprintf("开仓失败：当前市场不支持该交易对，10分钟内跳过 symbol (%v)", err)
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
	effectiveTakeProfit, effectiveStopLoss := resolveTPSLFromROI(inst, normalizedSide, order.Price, takeProfit, stopLoss)
	emitStrategyLog(inst, "info", fmt.Sprintf("开仓下单成功 symbol=%s side=%s status=%s order_id=%s client_order_id=%s qty=%v price=%v", symbol, normalizedSide, strings.ToLower(order.Status), order.ID, order.ClientOrderID, order.Amount, order.Price))
	inst.hub.BroadcastJSON(map[string]interface{}{"type": "order", "data": order})

	if strings.ToLower(order.Status) == "filled" {
		applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), symbol, normalizedSide, order.Amount, order.Price, effectiveTakeProfit, effectiveStopLoss, order.Timestamp)
	}

	if effectiveTakeProfit > 0 || effectiveStopLoss > 0 {
		if strings.ToLower(order.Status) != "filled" {
			emitStrategyLog(inst, "info", fmt.Sprintf("开仓单初始状态未成交，继续等待成交后补设交易所止盈止损 symbol=%s status=%s client_order_id=%s", symbol, strings.ToLower(order.Status), order.ClientOrderID))
		}
		go m.tryPlaceExchangeTPStop(inst, symbol, effectiveTakeProfit, effectiveStopLoss, clientOrderID, signalID)
	}
}

func (m *Manager) tryPlaceExchangeTPStop(inst *StrategyInstance, symbol string, takeProfit float64, stopLoss float64, baseClientOrderID string, signalID string) {
	if inst == nil {
		return
	}
	_ = database.DB.Model(&models.StrategyPosition{}).
		Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", inst.OwnerID, inst.ID, symbol, "open").
		Updates(map[string]interface{}{"take_profit": takeProfit, "stop_loss": stopLoss, "updated_at": time.Now()}).Error
	useExchange := true
	if v, ok := inst.Config["use_exchange_tpsl"]; ok {
		useExchange = getBool(v)
	}
	if useExchange {
		if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
			positionReady := false
			positionAmt := 0.0
			entryPx := 0.0
			levUsed := 0.0
			for i := 0; i < 120; i++ {
				var ord models.StrategyOrder
				err := database.DB.Where("owner_id = ? AND strategy_id = ? AND client_order_id = ?", inst.OwnerID, inst.ID, baseClientOrderID).
					Order("requested_at desc").
					First(&ord).Error
				if err == nil {
					st := strings.ToLower(strings.TrimSpace(ord.Status))
					if st == "filled" {
						positionReady = true
					}
					if st == "failed" || st == "rejected" || st == "canceled" || st == "expired" {
						emitStrategyLog(inst, "info", fmt.Sprintf("开仓单未成交完成，跳过设置交易所止盈止损 symbol=%s status=%s client_order_id=%s", symbol, st, baseClientOrderID))
						return
					}
				}
				amt, px, _, lev, posErr := bx.USDMPositionInfo(inst.OwnerID, symbol)
				if posErr == nil && amt != 0 {
					positionReady = true
					positionAmt = amt
					entryPx = px
					levUsed = lev
				}
				if positionReady && positionAmt != 0 && entryPx > 0 {
					var lastErr error
					var pos models.StrategyPosition
					errDB := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", inst.OwnerID, inst.ID, symbol, "open").First(&pos).Error
					if errDB != nil || pos.Amount <= 0 {
						now := time.Now()
						newPos := models.StrategyPosition{
							StrategyID:   inst.ID,
							StrategyName: inst.Name,
							OwnerID:      inst.OwnerID,
							Exchange:     bx.GetName(),
							Symbol:       symbol,
							Amount:       math.Abs(positionAmt),
							AvgPrice:     entryPx,
							TakeProfit:   takeProfit,
							StopLoss:     stopLoss,
							Status:       "open",
							OpenTime:     now,
							UpdatedAt:    now,
						}
						_ = database.DB.Create(&newPos).Error
					}
					side := "buy"
					if positionAmt < 0 {
						side = "sell"
					}
					takeProfit, stopLoss = resolveTPSLFromROI(inst, side, entryPx, takeProfit, stopLoss)
					if entryPx > 0 && stopLoss > 0 {
						if levUsed <= 0 {
							levUsed = float64(int(getNumber(inst.Config["leverage"])))
						}
						if levUsed <= 0 {
							levUsed = 1
						}
						if positionAmt > 0 {
							minSL := entryPx * (1 - 0.3/levUsed)
							maxSL := entryPx
							if stopLoss < minSL {
								stopLoss = minSL
							} else if stopLoss > maxSL {
								stopLoss = maxSL
							}
						} else {
							minSL := entryPx
							maxSL := entryPx * (1 + 0.3/levUsed)
							if stopLoss < minSL {
								stopLoss = minSL
							} else if stopLoss > maxSL {
								stopLoss = maxSL
							}
						}
					}
					created, e := bx.PlaceUSDMTPStopOrders(inst.OwnerID, baseClientOrderID, symbol, takeProfit, stopLoss)
					lastErr = e
					if lastErr == nil {
						_ = database.DB.Model(&models.StrategyPosition{}).
							Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", inst.OwnerID, inst.ID, symbol, "open").
							Updates(map[string]interface{}{"take_profit": takeProfit, "stop_loss": stopLoss, "updated_at": time.Now()}).Error
						if len(created) == 0 {
							emitStrategyLog(inst, "info", fmt.Sprintf("已设置止盈止损 symbol=%s tp=%v sl=%v", symbol, takeProfit, stopLoss))
						} else {
							refs := make([]string, 0, len(created))
							for _, ref := range created {
								refs = append(refs, fmt.Sprintf("%s order_id=%d client_order_id=%s trigger=%s price=%s", ref.Kind, ref.AlgoID, ref.ClientAlgoID, ref.TriggerPrice, ref.ExecutionPrice))
							}
							emitStrategyLog(inst, "info", fmt.Sprintf("已设置止盈止损 symbol=%s tp=%v sl=%v %s", symbol, takeProfit, stopLoss, strings.Join(refs, " | ")))
						}
						m.monitorPositionTPStop(inst, symbol, takeProfit, stopLoss, signalID)
						return
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
				time.Sleep(1 * time.Second)
			}
			emitStrategyLog(inst, "info", fmt.Sprintf("开仓单在等待期内未形成有效持仓，回退为本地监控止盈止损 symbol=%s client_order_id=%s", symbol, baseClientOrderID))
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
	ctx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	if inst.tpslCancel == nil {
		inst.tpslCancel = map[string]context.CancelFunc{}
	}
	if old, ok := inst.tpslCancel[sym]; ok && old != nil {
		old()
	}
	inst.tpslCancel[sym] = cancel
	inst.mu.Unlock()
	go m.runPositionTPStopMonitor(ctx, inst, sym, takeProfit, stopLoss, signalID)
}

func (m *Manager) stopPositionTPStopMonitor(inst *StrategyInstance, symbol string) {
	if inst == nil {
		return
	}
	sym := strings.TrimSpace(symbol)
	if sym == "" {
		return
	}
	inst.mu.Lock()
	cancel := context.CancelFunc(nil)
	if inst.tpslCancel != nil {
		cancel = inst.tpslCancel[sym]
		delete(inst.tpslCancel, sym)
	}
	inst.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *Manager) runPositionTPStopMonitor(ctx context.Context, inst *StrategyInstance, sym string, takeProfit float64, stopLoss float64, signalID string) {
	defer m.stopPositionTPStopMonitor(inst, sym)
	isShort := false
	if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		if amt, entryPx, _, levUsed, err := bx.USDMPositionInfo(inst.OwnerID, sym); err == nil {
			if amt < 0 {
				isShort = true
			}
			side := "buy"
			if isShort {
				side = "sell"
			}
			takeProfit, stopLoss = resolveTPSLFromROI(inst, side, entryPx, takeProfit, stopLoss)
			if entryPx > 0 && stopLoss > 0 {
				if levUsed <= 0 {
					levUsed = float64(int(getNumber(inst.Config["leverage"])))
				}
				if levUsed <= 0 {
					levUsed = 1
				}
				if !isShort {
					minSL := entryPx * (1 - 0.3/levUsed)
					maxSL := entryPx
					if stopLoss < minSL {
						stopLoss = minSL
					} else if stopLoss > maxSL {
						stopLoss = maxSL
					}
				} else {
					minSL := entryPx
					maxSL := entryPx * (1 + 0.3/levUsed)
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		inst.mu.Lock()
		px := 0.0
		if inst.lastCandleClose != nil {
			px = inst.lastCandleClose[sym]
		}
		inst.mu.Unlock()
		hit := false
		reason := ""
		if px > 0 {
			if !isShort {
				if takeProfit > 0 && px >= takeProfit {
					hit = true
					reason = "tp"
				}
				if stopLoss > 0 && px <= stopLoss {
					hit = true
					reason = "sl"
				}
			} else {
				if takeProfit > 0 && px <= takeProfit {
					hit = true
					reason = "tp"
				}
				if stopLoss > 0 && px >= stopLoss {
					hit = true
					reason = "sl"
				}
			}
		}
		if !hit {
			if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
				if amt, entryPx, markPx, levUsed, err := bx.USDMPositionInfo(inst.OwnerID, sym); err == nil && entryPx > 0 && levUsed > 0 && amt != 0 {
					pnl := (markPx - entryPx) * amt
					initial := (math.Abs(amt) * entryPx) / levUsed
					if initial > 0 {
						roi := (pnl / initial) * 100
						slPct := getNumber(inst.Config["stop_loss_pct"]) * 100
						tpPct := getNumber(inst.Config["take_profit_pct"]) * 100
						if tpPct > 0 && roi >= tpPct {
							hit = true
							reason = "roi_tp"
						}
						if slPct > 0 && roi <= -slPct {
							hit = true
							reason = "roi_sl"
						}
					}
				} else if err == nil && amt == 0 {
					return
				}
			}
		}
		if hit {
			if err := m.closePositionForInstance(inst, sym, reason, signalID); err != nil {
				emitStrategyLog(inst, "error", fmt.Sprintf("本地止盈止损触发但平仓失败 symbol=%s reason=%s err=%v", sym, reason, err))
				continue
			}
			emitStrategyLog(inst, "info", fmt.Sprintf("本地止盈止损触发并平仓 symbol=%s reason=%s", sym, reason))
			return
		}
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
		return m.closeUSDMPosition(inst, bx, sym)
	}
	_ = reason
	_ = signalID
	return m.closeSpotPosition(inst, sym)
}
