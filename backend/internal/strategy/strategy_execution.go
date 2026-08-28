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

// confSizingMultiplier 按信号置信度线性插值仓位乘数：
// conf<=conf_lo → min_mult，conf>=conf_hi → max_mult，中间线性。
// conf_sizing_enabled 未开启或信号未带置信度时返回 (1, false)，行为与旧版完全一致。
func confSizingMultiplier(inst *StrategyInstance, confidence float64) (float64, bool) {
	if inst == nil || !getBool(inst.Config["conf_sizing_enabled"]) || confidence <= 0 {
		return 1, false
	}
	lo := getNumber(inst.Config["conf_sizing_conf_lo"])
	if lo <= 0 {
		lo = getNumber(inst.Config["min_confidence"])
	}
	if lo <= 0 {
		lo = 0.40
	}
	hi := getNumber(inst.Config["conf_sizing_conf_hi"])
	if hi <= lo {
		hi = lo + 0.15
	}
	minM := getNumber(inst.Config["conf_sizing_min_mult"])
	if minM <= 0 {
		minM = 0.60
	}
	maxM := getNumber(inst.Config["conf_sizing_max_mult"])
	if maxM <= 0 {
		maxM = 1.40
	}
	// 乘数带宽夹在 [0.2, 2]，且 max 不得低于 min：配置写反时按保守方向收敛。
	if minM < 0.20 {
		minM = 0.20
	}
	if minM > 1 {
		minM = 1
	}
	if maxM < 1 {
		maxM = 1
	}
	if maxM > 2 {
		maxM = 2
	}
	t := (confidence - lo) / (hi - lo)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return minM + t*(maxM-minM), true
}

func resolveUSDMOrderAmount(inst *StrategyInstance, bx *exchange.BinanceExchange, symbol string, amount float64, price float64, confidence float64) (float64, error) {
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
		mult, confSized := confSizingMultiplier(inst, confidence)
		if confSized {
			basePct := pct
			pct = pct * mult
			// 有效 pct 夹在 [0.05, 0.75]：与人工调参共用同一物理边界。
			if pct > 0.75 {
				pct = 0.75
			}
			if pct < 0.05 {
				pct = 0.05
			}
			emitStrategyLog(inst, "info", fmt.Sprintf("置信度动态仓位 symbol=%s conf=%.4f mult=%.2f pct=%.4f→%.4f", symbol, confidence, mult, basePct, pct))
		}
		maxInit := getNumber(inst.Config["max_initial_margin_usdt"])
		if getBool(inst.Config["order_pct_exclude_leverage"]) {
			// 保守开关：名义 = 余额×pct，不乘杠杆；杠杆只决定保证金占用（= 名义/杠杆）。
			notional := avail * pct
			if maxInit > 0 && notional > maxInit*float64(lev) {
				notional = maxInit * float64(lev)
			}
			if notional <= 0 {
				emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：按余额百分比计算后的名义<=0 symbol=%s", symbol))
				return 0, nil
			}
			desiredNotional = notional
		} else {
			// 默认（2026-07-20 用户直令）：与币安百分比滑杆同语义——
			// pct 视为初始保证金占比，名义 = 保证金×杠杆，后端算出最终币数量传给交易所。
			initial := avail * pct
			if maxInit > 0 && initial > maxInit {
				initial = maxInit
			}
			if initial <= 0 {
				emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：按余额百分比计算后的初始保证金<=0 symbol=%s", symbol))
				return 0, nil
			}
			desiredNotional = initial * float64(lev)
		}
		if confSized {
			// 缩量不得击穿单笔名义下限（默认 20U）：低置信度是少开，不是开出无意义的粉尘单。
			floorN := getNumber(inst.Config["conf_sizing_min_notional_usdt"])
			if floorN <= 0 {
				floorN = 20
			}
			if desiredNotional < floorN {
				desiredNotional = floorN
			}
		}
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
			if posAmt, _, markPx, e2 := bx.USDMPositionAmtCached(inst.OwnerID, symbol); e2 == nil && markPx > 0 && posAmt != 0 {
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
		emitStrategyLog(inst, "info", fmt.Sprintf("自动调整杠杆：因档位上限约束 symbol=%s lev=%d->%d", symbol, lev, levChosen))
	}
	ensureExchangeLeverage(inst, bx, symbol, levChosen)
	amount = finalNotional / px
	// 最小手数救援：高价币（单枚价接近或超过名义额）算出的数量 < 一手时，交易所侧
	// roundDownToStep 会取整到 0 → "quantity too small" 拒单，且该错误不含币名、
	// 候选层只见"候选开仓失败"（实证：MVLL 26.9U/枚，conf-sizing 后名义 23.4U →
	// 0.87 枚，两案同型）。数量已达一手的 2/3 时向上凑整一手（名义膨胀 ≤1.5×，
	// 保证金影响可忽略）；不足 2/3 则显式跳过——凑整会把仓位放大到 sizing 意图之外。
	if minLot := bx.MinTradableQty(symbol); minLot > 0 && amount < minLot {
		if amount >= minLot*2.0/3.0 {
			emitStrategyLog(inst, "info", fmt.Sprintf("最小手数凑整 symbol=%s qty=%.8f→%.8f 名义=%.2f→%.2f", symbol, amount, minLot, amount*px, minLot*px))
			amount = minLot
		} else {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：数量不足最小手数 symbol=%s qty=%.8f minLot=%.8f 名义=%.2f 一手名义=%.2f", symbol, amount, minLot, amount*px, minLot*px))
			return 0, nil
		}
	}
	amount = clampOrderAmount(inst, amount)
	if amount <= 0 {
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：数量钳制后<=0 symbol=%s", symbol))
		return 0, nil
	}
	if amount*px < minNotional {
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：名义价值过小 symbol=%s notional=%0.4f min_notional=%0.2f", symbol, amount*px, minNotional))
		return 0, nil
	}
	return amount, nil
}

// ensureExchangeLeverage 把交易所侧 per-symbol 杠杆对齐到目标值。
// 交易所杠杆是粘性的：手动改过或新币默认档（常见 20X）会一直生效，旧逻辑只在
// 档位降级时调 SetLeverage，导致实际保证金占用/ROI 按 20X 而非 config 杠杆计算
// （实证：config=3 时 VANRY 持仓显示 20X、AKE 显示 10X）。
// 结果按进程周期缓存，每个 symbol×lev 只发一次 REST；失败不阻塞下单
// （按现有交易所杠杆继续），下一单自动重试。
func ensureExchangeLeverage(inst *StrategyInstance, bx *exchange.BinanceExchange, symbol string, lev int) {
	if inst == nil || bx == nil || lev < 1 {
		return
	}
	key := exchange.NormalizeSymbol(symbol)
	inst.orderMu.Lock()
	if inst.leverageSet == nil {
		inst.leverageSet = map[string]int{}
	}
	done := inst.leverageSet[key] == lev
	inst.orderMu.Unlock()
	if done {
		return
	}
	if err := bx.SetLeverage(inst.OwnerID, symbol, lev); err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("对齐交易所杠杆失败（按现有杠杆继续下单） symbol=%s lev=%d err=%v", symbol, lev, err))
		return
	}
	inst.orderMu.Lock()
	inst.leverageSet[key] = lev
	inst.orderMu.Unlock()
	emitStrategyLog(inst, "info", fmt.Sprintf("交易所杠杆已对齐 symbol=%s lev=%d", symbol, lev))
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

// resolveMaxHoldTimeout returns the hard per-position max holding time. A
// position that has been open at least this long is force-closed regardless of
// PnL by the quick-trade monitor (unlike hunger mode, which only tightens TP/SL
// and still waits for a price condition). 0 (the default) disables it —
// force-closing is destructive, so it is strictly opt-in via config
// max_hold_minutes.
func resolveMaxHoldTimeout(inst *StrategyInstance) time.Duration {
	if inst == nil {
		return 0
	}
	minutes := int(getNumber(inst.Config["max_hold_minutes"]))
	if minutes <= 0 {
		return 0
	}
	return time.Duration(minutes) * time.Minute
}

func (m *Manager) closeUSDMPosition(inst *StrategyInstance, bx *exchange.BinanceExchange, sym string) error {
	m.stopPositionTPStopMonitor(inst, sym)
	var pos models.StrategyPosition
	_ = database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", inst.OwnerID, inst.ID, sym, "open").
		Order("updated_at desc, id desc").
		First(&pos).Error
	fallbackMetrics := BuildTradeCloseMetricsFromPosition(&pos, pos.Amount, 0, time.Time{})
	if bx != nil && fallbackMetrics != nil && fallbackMetrics.EntryPrice <= 0 {
		if amt, entryPx, _, _, err := bx.USDMPositionInfo(inst.OwnerID, sym); err == nil {
			if entryPx > 0 {
				fallbackMetrics.EntryPrice = entryPx
			}
			if fallbackMetrics.RealizedNotional <= 0 && math.Abs(amt) > 0 && entryPx > 0 {
				fallbackMetrics.RealizedNotional = math.Abs(amt) * entryPx
			}
		}
	}
	if found, canceled, err := m.cancelLinkedTPSLOrders(inst.OwnerID, inst.ID, sym); err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("平仓前撤销关联止盈止损失败 symbol=%s canceled=%d found=%d err=%v", sym, canceled, found, err))
	} else if found > 0 {
		emitStrategyLog(inst, "info", fmt.Sprintf("平仓前撤销关联止盈止损完成 symbol=%s canceled=%d found=%d", sym, canceled, found))
	}
	if summary, err := bx.CancelUSDMAllSymbolOrdersDetailed(inst.OwnerID, sym); err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("平仓前撤销该交易对全部委托失败 symbol=%s err=%v", sym, err))
	} else {
		emitStrategyLog(inst, "info", fmt.Sprintf("平仓前撤单完成 symbol=%s 普通委托=%d/%d 条件委托=%d/%d", sym, summary.NormalCanceled, summary.NormalFound, summary.AlgoCanceled, summary.AlgoFound))
	}
	order, _, _, err := bx.ClosePositionOrder(sym, inst.OwnerID)
	if err != nil {
		return err
	}
	if order == nil {
		if summary, err := bx.CancelUSDMAllSymbolOrdersDetailed(inst.OwnerID, sym); err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("平仓后撤销该交易对全部委托失败 symbol=%s err=%v", sym, err))
		} else {
			emitStrategyLog(inst, "info", fmt.Sprintf("平仓后撤单完成 symbol=%s 普通委托=%d/%d 条件委托=%d/%d", sym, summary.NormalCanceled, summary.NormalFound, summary.AlgoCanceled, summary.AlgoFound))
		}
		return nil
	}
	database.DB.Create(&models.StrategyOrder{
		PositionID:      pos.ID,
		StrategyID:      inst.ID,
		StrategyName:    inst.Name,
		OwnerID:         inst.OwnerID,
		Exchange:        bx.GetName(),
		Symbol:          sym,
		Side:            strings.ToLower(order.Side),
		Purpose:         "close",
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
	var closeMetrics *TradeCloseMetrics
	if strings.ToLower(order.Status) == "filled" {
		applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), sym, strings.ToLower(order.Side), order.Amount, order.Price, 0, 0, order.Timestamp)
		fallbackMetrics = BuildTradeCloseMetricsFromPosition(&pos, order.Amount, order.Price, order.Timestamp)
		closeMetrics = MergeTradeCloseMetrics(loadTradeCloseMetrics(inst.OwnerID, inst.ID, sym), fallbackMetrics)
	}
	m.notifyTradeClosed(inst, sym, strings.ToLower(order.Side), order.Amount, order.Price, strings.ToLower(order.Status), "strategy_close", closeMetrics)
	go func(ownerID uint, symbol string) {
		if summary, err := bx.CancelUSDMAllSymbolOrdersDetailed(ownerID, symbol); err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("平仓后立即撤销该交易对全部委托失败 symbol=%s err=%v", symbol, err))
		} else {
			emitStrategyLog(inst, "info", fmt.Sprintf("平仓后立即撤单完成 symbol=%s 普通委托=%d/%d 条件委托=%d/%d", symbol, summary.NormalCanceled, summary.NormalFound, summary.AlgoCanceled, summary.AlgoFound))
		}
	}(inst.OwnerID, sym)
	go func(ownerID uint, symbol string) {
		deadline := time.Now().Add(45 * time.Second)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for time.Now().Before(deadline) {
			amt, _, _, e := bx.USDMPositionAmtCached(ownerID, symbol)
			if e == nil && amt == 0 {
				if summary, err := bx.CancelUSDMAllSymbolOrdersDetailed(ownerID, symbol); err != nil {
					emitStrategyLog(inst, "error", fmt.Sprintf("仓位归零后撤销该交易对全部委托失败 symbol=%s err=%v", symbol, err))
				} else {
					emitStrategyLog(inst, "info", fmt.Sprintf("仓位归零后撤单完成 symbol=%s 普通委托=%d/%d 条件委托=%d/%d", symbol, summary.NormalCanceled, summary.NormalFound, summary.AlgoCanceled, summary.AlgoFound))
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
	if found, canceled, err := m.cancelLinkedTPSLOrders(inst.OwnerID, inst.ID, sym); err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("平仓前撤销关联止盈止损失败 symbol=%s canceled=%d found=%d err=%v", sym, canceled, found, err))
	} else if found > 0 {
		emitStrategyLog(inst, "info", fmt.Sprintf("平仓前撤销关联止盈止损完成 symbol=%s canceled=%d found=%d", sym, canceled, found))
	}
	if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() != "usdm" {
		if err := bx.CancelPrePositionOpenOrders(inst.OwnerID, sym); err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("平仓前撤销该交易对未成交委托失败 symbol=%s err=%v", sym, err))
		} else {
			emitStrategyLog(inst, "info", fmt.Sprintf("平仓前撤销该交易对未成交委托完成 symbol=%s", sym))
		}
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
	fallbackMetrics := BuildTradeCloseMetricsFromPosition(&pos, pos.Amount, 0, time.Time{})
	clientOrderID := models.GenerateUUID()
	database.DB.Create(&models.StrategyOrder{
		PositionID:    pos.ID,
		StrategyID:    inst.ID,
		StrategyName:  inst.Name,
		OwnerID:       inst.OwnerID,
		Exchange:      inst.exchange.GetName(),
		Symbol:        sym,
		Side:          "sell",
		Purpose:       "close",
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
	var closeMetrics *TradeCloseMetrics
	if strings.ToLower(order.Status) == "filled" {
		applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), sym, "sell", order.Amount, order.Price, 0, 0, order.Timestamp)
		fallbackMetrics = BuildTradeCloseMetricsFromPosition(&pos, order.Amount, order.Price, order.Timestamp)
		closeMetrics = MergeTradeCloseMetrics(loadTradeCloseMetrics(inst.OwnerID, inst.ID, sym), fallbackMetrics)
	}
	m.notifyTradeClosed(inst, sym, "sell", order.Amount, order.Price, strings.ToLower(order.Status), "strategy_close", closeMetrics)
	return nil
}
