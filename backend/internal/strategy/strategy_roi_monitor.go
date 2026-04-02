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

var roiGuardOnce sync.Once

func (m *Manager) StartROIGuardMonitor(ctx context.Context) {
	roiGuardOnce.Do(func() {
		go m.runROIGuardMonitor(ctx)
	})
}

func (m *Manager) runROIGuardMonitor(ctx context.Context) {
	m.roiGuardTick()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.roiGuardTick()
		}
	}
}

func (m *Manager) roiGuardTick() {
	if m == nil || database.DB == nil {
		return
	}
	bx, ok := m.exchange.(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return
	}

	var openRows []models.StrategyPosition
	if err := database.DB.Where("status = ?", "open").Find(&openRows).Error; err != nil {
		return
	}

	now := time.Now()
	byOwner := map[uint][]models.StrategyPosition{}
	for _, r := range openRows {
		byOwner[r.OwnerID] = append(byOwner[r.OwnerID], r)
	}
	ownerInstances := map[uint][]*StrategyInstance{}
	ownerInstanceByID := map[uint]map[string]*StrategyInstance{}
	m.mu.RLock()
	for _, inst := range m.instances {
		if inst == nil {
			continue
		}
		ownerInstances[inst.OwnerID] = append(ownerInstances[inst.OwnerID], inst)
		if ownerInstanceByID[inst.OwnerID] == nil {
			ownerInstanceByID[inst.OwnerID] = map[string]*StrategyInstance{}
		}
		ownerInstanceByID[inst.OwnerID][inst.ID] = inst
		if _, ok := byOwner[inst.OwnerID]; !ok {
			byOwner[inst.OwnerID] = nil
		}
	}
	m.mu.RUnlock()

	for uid, rows := range byOwner {
		ps, err := bx.FetchPositions(uid, "active")
		if err != nil {
			continue
		}
		posBySymbol := map[string]exchange.Position{}
		for _, p := range ps {
			posBySymbol[strings.ToUpper(p.Symbol)] = p
		}
		trackedRows := append([]models.StrategyPosition(nil), rows...)
		seenSymbols := map[string]struct{}{}
		for _, r := range trackedRows {
			seenSymbols[strings.ToUpper(r.Symbol)] = struct{}{}
		}
		for _, p := range ps {
			symKey := strings.ToUpper(p.Symbol)
			if _, ok := seenSymbols[symKey]; ok {
				continue
			}
			inst := findGuardStrategyInstance(ownerInstances[uid], p.Symbol)
			if inst == nil {
				continue
			}
			side := "buy"
			if strings.EqualFold(p.Direction, "short") {
				side = "sell"
			}
			tp, sl := resolveTPSLFromROI(inst, side, p.Price, 0, 0)
			synthetic := models.StrategyPosition{
				StrategyID:   inst.ID,
				StrategyName: inst.Name,
				OwnerID:      uid,
				Exchange:     bx.GetName(),
				Symbol:       p.Symbol,
				Amount:       p.Amount,
				AvgPrice:     p.Price,
				TakeProfit:   tp,
				StopLoss:     sl,
				Status:       "open",
				OpenTime:     p.OpenTime,
				UpdatedAt:    now,
			}
			trackedRows = append(trackedRows, synthetic)
			seenSymbols[symKey] = struct{}{}
			var existing models.StrategyPosition
			if err := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", uid, inst.ID, p.Symbol, "open").First(&existing).Error; err != nil {
				_ = database.DB.Create(&synthetic).Error
				emitStrategyLog(inst, "info", fmt.Sprintf("仓位守护补录持仓 symbol=%s entry=%0.8f tp=%0.8f sl=%0.8f", p.Symbol, p.Price, tp, sl))
			}
		}
		for _, r := range trackedRows {
			inst := ownerInstanceByID[uid][r.StrategyID]
			if inst == nil {
				inst = findGuardStrategyInstance(ownerInstances[uid], r.Symbol)
			}
			if inst == nil {
				continue
			}
			pos, ok := posBySymbol[strings.ToUpper(r.Symbol)]
			if !ok {
				continue
			}
			roi := pos.ReturnRate
			unpnl := pos.UnrealizedPnL
			currentPrice := pos.CurrentPrice
			if currentPrice <= 0 {
				currentPrice = pos.Price
			}
			side := "buy"
			if strings.EqualFold(pos.Direction, "short") {
				side = "sell"
			}
			tpPct := normalizedTPSLPct(inst, "take_profit_pct") * 100
			slPct := normalizedTPSLPct(inst, "stop_loss_pct") * 100
			tp := r.TakeProfit
			sl := r.StopLoss
			if tp <= 0 || sl <= 0 {
				rtp, rsl := resolveTPSLFromROI(inst, side, pos.Price, tp, sl)
				if tp <= 0 {
					tp = rtp
				}
				if sl <= 0 {
					sl = rsl
				}
			}
			reason := ""
			if currentPrice > 0 {
				if side == "buy" {
					if tp > 0 && currentPrice >= tp {
						reason = "guard_tp"
					}
					if reason == "" && sl > 0 && currentPrice <= sl {
						reason = "guard_sl"
					}
				} else {
					if tp > 0 && currentPrice <= tp {
						reason = "guard_tp"
					}
					if reason == "" && sl > 0 && currentPrice >= sl {
						reason = "guard_sl"
					}
				}
			}
			if reason == "" && tpPct > 0 && roi >= tpPct {
				reason = "guard_roi_tp"
			}
			if reason == "" && slPct > 0 && roi <= -slPct {
				reason = "guard_roi_sl"
			}
			if reason == "" {
				continue
			}
			if !m.tryMarkQuickClose(uid, strings.ToUpper(r.Symbol), now) {
				continue
			}
			emitStrategyLog(inst, "info", fmt.Sprintf("全局仓位守护触发：symbol=%s price=%0.8f roi=%0.4f%% pnl=%0.4f tp=%0.8f sl=%0.8f tp_pct=%0.2f%% sl_pct=%0.2f%% reason=%s，自动平仓", r.Symbol, currentPrice, roi, unpnl, tp, sl, tpPct, slPct, reason))
			if err := m.closePositionForInstance(inst, r.Symbol, reason, ""); err != nil {
				m.releaseQuickClose(uid, strings.ToUpper(r.Symbol))
				emitStrategyLog(inst, "error", fmt.Sprintf("全局仓位守护触发但平仓失败 symbol=%s reason=%s err=%v", r.Symbol, reason, err))
			}
		}
	}
}

func findGuardStrategyInstance(insts []*StrategyInstance, symbol string) *StrategyInstance {
	for _, inst := range insts {
		if inst == nil {
			continue
		}
		if isAllowedSymbol(inst, symbol) {
			return inst
		}
	}
	return nil
}
