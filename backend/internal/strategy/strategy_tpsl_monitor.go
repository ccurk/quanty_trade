package strategy

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

var tpslGuardOnce sync.Once

func (m *Manager) StartTPSLGuardMonitor(ctx context.Context) {
	tpslGuardOnce.Do(func() {
		go m.runTPSLGuardMonitor(ctx)
	})
}

func (m *Manager) runTPSLGuardMonitor(ctx context.Context) {
	m.tpslGuardTick()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tpslGuardTick()
		}
	}
}

func (m *Manager) tpslGuardTick() {
	if m == nil || database.DB == nil {
		return
	}
	bx, ok := m.exchange.(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return
	}

	var openRows []models.StrategyPosition
	if err := database.DB.Where("status = ?", "open").Find(&openRows).Error; err != nil || len(openRows) == 0 {
		return
	}

	byOwner := map[uint][]models.StrategyPosition{}
	for _, r := range openRows {
		byOwner[r.OwnerID] = append(byOwner[r.OwnerID], r)
	}

	for uid, rows := range byOwner {
		posList, err := bx.FetchPositions(uid, "active")
		if err != nil {
			continue
		}
		activeBySymbol := map[string]exchange.Position{}
		for _, p := range posList {
			if p.Amount > 0 {
				activeBySymbol[strings.ToUpper(p.Symbol)] = p
			}
		}
		for _, row := range rows {
			active, ok := activeBySymbol[strings.ToUpper(row.Symbol)]
			if !ok {
				continue
			}
			m.mu.RLock()
			inst := m.instances[row.StrategyID]
			m.mu.RUnlock()
			if inst == nil {
				continue
			}

			algoOrders, err := bx.ListUSDMTPSLOpenOrders(uid, row.Symbol)
			if err != nil {
				emitStrategyLog(inst, "error", fmt.Sprintf("查询交易所止盈止损失败 symbol=%s err=%v", row.Symbol, err))
				continue
			}
			hasTP := false
			hasSL := false
			for _, ord := range algoOrders {
				typ := strings.ToUpper(strings.TrimSpace(ord.Type))
				if strings.Contains(typ, "TAKE_PROFIT") {
					hasTP = true
				}
				if typ == "STOP" || strings.Contains(typ, "STOP") {
					hasSL = true
				}
			}

			tp := row.TakeProfit
			sl := row.StopLoss
			side := "buy"
			if strings.EqualFold(active.Direction, "short") {
				side = "sell"
			}
			if tp <= 0 || sl <= 0 {
				tp, sl = resolveTPSLFromROI(inst, side, active.Price, tp, sl)
				_ = database.DB.Model(&models.StrategyPosition{}).
					Where("id = ?", row.ID).
					Updates(map[string]interface{}{"take_profit": tp, "stop_loss": sl, "updated_at": time.Now()}).Error
			}
			if tp <= 0 || sl <= 0 {
				emitStrategyLog(inst, "error", fmt.Sprintf("仓位缺少有效止盈止损配置 symbol=%s tp=%v sl=%v", row.Symbol, tp, sl))
				continue
			}
			if hasTP && hasSL {
				continue
			}

			if len(algoOrders) > 0 {
				_ = bx.CancelUSDMAlgoOpenOrders(uid, row.Symbol)
			}
			baseClientOrderID := models.GenerateUUID()
			created, err := bx.PlaceUSDMTPStopOrders(uid, baseClientOrderID, row.Symbol, tp, sl)
			if err != nil {
				emitStrategyLog(inst, "error", fmt.Sprintf("补设交易所止盈止损失败 symbol=%s tp=%v sl=%v err=%v", row.Symbol, tp, sl, err))
				continue
			}
			m.storeLinkedTPSLOrders(inst, row.ID, row.Symbol, baseClientOrderID, created)
			refs := make([]string, 0, len(created))
			for _, ref := range created {
				refs = append(refs, fmt.Sprintf("%s order_id=%d client_order_id=%s trigger=%s price=%s", ref.Kind, ref.AlgoID, ref.ClientAlgoID, ref.TriggerPrice, ref.ExecutionPrice))
			}
			emitStrategyLog(inst, "info", fmt.Sprintf("已补设交易所止盈止损 symbol=%s tp=%v sl=%v %s", row.Symbol, tp, sl, strings.Join(refs, " | ")))
		}
	}
}
