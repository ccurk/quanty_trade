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
	cutoff := now.Add(-30 * time.Minute)

	var openRows []models.StrategyPosition
	if err := database.DB.Where("status = ? AND open_time <= ?", "open", cutoff).Find(&openRows).Error; err != nil {
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
		pnlBySymbol := map[string]float64{}
		for _, p := range ps {
			pnlBySymbol[strings.ToUpper(p.Symbol)] = p.UnrealizedPnL
		}
		for _, r := range rows {
			symKey := strings.ToUpper(r.Symbol)
			unpnl := pnlBySymbol[symKey]
			if unpnl <= 5 {
				continue
			}
			if !m.tryMarkQuickClose(uid, symKey, now) {
				continue
			}

			m.mu.RLock()
			inst := m.instances[r.StrategyID]
			m.mu.RUnlock()
			if inst == nil {
				continue
			}
			emitStrategyLog(inst, "info", fmt.Sprintf("快速交易期触发：持仓超过30分钟且未实现盈利>%0.2fUSDT，自动平仓 symbol=%s pnl=%0.4f", 5.0, r.Symbol, unpnl))
			_ = m.closePositionForInstance(inst, r.Symbol, "quick_trade", "")
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
