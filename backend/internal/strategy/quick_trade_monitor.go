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

var quickTradeOnce sync.Once

func (m *Manager) StartQuickTradeMonitor(ctx context.Context) {
	quickTradeOnce.Do(func() {
		go m.runQuickTradeMonitor(ctx)
	})
}

func (m *Manager) runQuickTradeMonitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.quickTradeTick()
		}
	}
}

func (m *Manager) quickTradeTick() {
	if m == nil || database.DB == nil {
		return
	}
	bx, ok := m.exchange.(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return
	}

	now := time.Now()

	var openRows []models.StrategyPosition
	if err := database.DB.Where("status = ?", "open").Find(&openRows).Error; err != nil {
		return
	}
	if len(openRows) == 0 {
		return
	}

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
			enabled, after, hungerTPPct, hungerSLPct := resolveHungerMode(inst)
			if !enabled || after <= 0 || r.OpenTime.IsZero() || now.Sub(r.OpenTime) < after {
				continue
			}
			symKey := strings.ToUpper(r.Symbol)
			pos, ok := posBySymbol[symKey]
			if !ok {
				continue
			}
			roi := pos.ReturnRate
			unpnl := pos.UnrealizedPnL
			reason := ""
			if hungerTPPct > 0 && roi >= hungerTPPct*100 {
				reason = "hunger_tp"
			}
			if reason == "" && hungerSLPct > 0 && roi <= -hungerSLPct*100 {
				reason = "hunger_sl"
			}
			if reason == "" {
				continue
			}
			if !m.tryMarkQuickClose(uid, symKey, now) {
				continue
			}
			emitStrategyLog(inst, "info", fmt.Sprintf("饥饿模式触发：持仓时长=%s symbol=%s roi=%0.4f%% pnl=%0.4f tp=%0.2f%% sl=%0.2f%%，自动平仓", now.Sub(r.OpenTime).Round(time.Second), r.Symbol, roi, unpnl, hungerTPPct*100, hungerSLPct*100))
			if err := m.closePositionForInstance(inst, r.Symbol, reason, ""); err != nil {
				m.releaseQuickClose(uid, symKey)
				emitStrategyLog(inst, "error", fmt.Sprintf("饥饿模式触发但平仓失败 symbol=%s reason=%s err=%v", r.Symbol, reason, err))
			}
		}
	}
}

func (m *Manager) tryMarkQuickClose(uid uint, symbol string, now time.Time) bool {
	if m == nil || uid == 0 || symbol == "" {
		return false
	}
	m.quickCloseMu.Lock()
	defer m.quickCloseMu.Unlock()
	if m.quickCloseAt == nil {
		m.quickCloseAt = make(map[string]time.Time)
	}
	key := fmt.Sprintf("%d:%s", uid, symbol)
	if t, ok := m.quickCloseAt[key]; ok && now.Sub(t) < 2*time.Minute {
		return false
	}
	m.quickCloseAt[key] = now
	return true
}

func (m *Manager) releaseQuickClose(uid uint, symbol string) {
	if m == nil || uid == 0 || symbol == "" {
		return
	}
	m.quickCloseMu.Lock()
	defer m.quickCloseMu.Unlock()
	if m.quickCloseAt == nil {
		return
	}
	delete(m.quickCloseAt, fmt.Sprintf("%d:%s", uid, symbol))
}
