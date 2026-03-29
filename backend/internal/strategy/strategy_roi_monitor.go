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
	ticker := time.NewTicker(1 * time.Minute)
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
	if err := database.DB.Where("status = ?", "open").Find(&openRows).Error; err != nil || len(openRows) == 0 {
		return
	}

	now := time.Now()
	byOwner := map[uint][]models.StrategyPosition{}
	for _, r := range openRows {
		byOwner[r.OwnerID] = append(byOwner[r.OwnerID], r)
	}

	for uid, rows := range byOwner {
		ps, err := bx.FetchPositions(uid, "active")
		if err != nil {
			continue
		}
		posBySymbol := map[string]exchange.Position{}
		for _, p := range ps {
			posBySymbol[strings.ToUpper(p.Symbol)] = p
		}
		for _, r := range rows {
			m.mu.RLock()
			inst := m.instances[r.StrategyID]
			m.mu.RUnlock()
			if inst == nil {
				continue
			}
			pos, ok := posBySymbol[strings.ToUpper(r.Symbol)]
			if !ok {
				continue
			}
			roi := pos.ReturnRate
			unpnl := pos.UnrealizedPnL
			tpPct := getNumber(inst.Config["take_profit_pct"]) * 100
			slPct := getNumber(inst.Config["stop_loss_pct"]) * 100
			reason := ""
			if tpPct > 0 && roi >= tpPct {
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
			emitStrategyLog(inst, "info", fmt.Sprintf("ROI巡检触发：symbol=%s roi=%0.4f%% pnl=%0.4f tp=%0.2f%% sl=%0.2f%%，自动平仓", r.Symbol, roi, unpnl, tpPct, slPct))
			if err := m.closePositionForInstance(inst, r.Symbol, reason, ""); err != nil {
				emitStrategyLog(inst, "error", fmt.Sprintf("ROI巡检触发但平仓失败 symbol=%s reason=%s err=%v", r.Symbol, reason, err))
			}
		}
	}
}
