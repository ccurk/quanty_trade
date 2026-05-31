package strategy

import (
	"fmt"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
)

type TradeCloseMetrics struct {
	EntryPrice        float64
	ExitPrice         float64
	RealizedPnL       float64
	RealizedReturnPct float64
	RealizedNotional  float64
	OpenTime          time.Time
	CloseTime         time.Time
	HoldingDuration   time.Duration
}

type RuntimeNotifier interface {
	NotifyTradeOpened(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, takeProfit float64, stopLoss float64, status string)
	NotifyTradeClosed(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, status string, reason string, metrics *TradeCloseMetrics)
	NotifyStrategyStatus(ownerID uint, strategyID string, strategyName string, status string)
	NotifyAIOptimization(ownerID uint, strategyID string, strategyName string, status string, trigger string, summary string, detail string)
}

func (m *Manager) SetNotifier(notifier RuntimeNotifier) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.notifier = notifier
	m.mu.Unlock()
}

func (m *Manager) notifyTradeOpened(inst *StrategyInstance, symbol string, side string, qty float64, price float64, takeProfit float64, stopLoss float64, status string) {
	if m == nil || inst == nil {
		return
	}
	m.mu.RLock()
	notifier := m.notifier
	m.mu.RUnlock()
	if notifier == nil {
		return
	}
	notifier.NotifyTradeOpened(inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), symbol, side, qty, price, takeProfit, stopLoss, status)
}

func (m *Manager) notifyTradeClosed(inst *StrategyInstance, symbol string, side string, qty float64, price float64, status string, reason string, metrics *TradeCloseMetrics) {
	if m == nil || inst == nil {
		return
	}
	m.mu.RLock()
	notifier := m.notifier
	m.mu.RUnlock()
	if notifier == nil {
		return
	}
	notifier.NotifyTradeClosed(inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), symbol, side, qty, price, status, reason, metrics)
}

func (m *Manager) NotifyExternalTradeClosed(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, status string, reason string, metrics *TradeCloseMetrics) {
	if m == nil {
		return
	}
	m.mu.RLock()
	notifier := m.notifier
	m.mu.RUnlock()
	if notifier == nil {
		return
	}
	notifier.NotifyTradeClosed(ownerID, strategyID, strategyName, exchangeName, symbol, side, qty, price, status, reason, metrics)
}

func loadTradeCloseMetrics(ownerID uint, strategyID string, symbol string) *TradeCloseMetrics {
	if database.DB == nil || ownerID == 0 || strings.TrimSpace(strategyID) == "" || strings.TrimSpace(symbol) == "" {
		return nil
	}
	var pos models.StrategyPosition
	if err := database.DB.
		Where("owner_id = ? AND strategy_id = ? AND symbol = ?", ownerID, strategyID, symbol).
		Order("updated_at desc, close_time desc, id desc").
		First(&pos).Error; err != nil {
		return nil
	}
	metrics := &TradeCloseMetrics{
		EntryPrice:       pos.AvgPrice,
		ExitPrice:        pos.AvgClosePrice,
		RealizedPnL:      pos.RealizedPnL,
		RealizedNotional: pos.RealizedNotional,
		OpenTime:         pos.OpenTime,
		CloseTime:        pos.CloseTime,
	}
	if metrics.ExitPrice <= 0 {
		metrics.ExitPrice = pos.AvgClosePrice
	}
	if metrics.RealizedNotional > 0 {
		metrics.RealizedReturnPct = (metrics.RealizedPnL / metrics.RealizedNotional) * 100
	}
	if !metrics.CloseTime.IsZero() && !metrics.OpenTime.IsZero() && metrics.CloseTime.After(metrics.OpenTime) {
		metrics.HoldingDuration = metrics.CloseTime.Sub(metrics.OpenTime)
	}
	return metrics
}

func (m *Manager) notifyAIOptimization(inst *StrategyInstance, status string, trigger string, summary string, detail string) {
	if m == nil || inst == nil {
		return
	}
	m.mu.RLock()
	notifier := m.notifier
	m.mu.RUnlock()
	if notifier == nil {
		return
	}
	notifier.NotifyAIOptimization(inst.OwnerID, inst.ID, inst.Name, status, trigger, summary, detail)
}

func (m *Manager) findStrategyForCommand(target string) (*StrategyInstance, error) {
	if m == nil {
		return nil, fmt.Errorf("manager is nil")
	}
	key := strings.ToLower(strings.TrimSpace(target))
	if key == "" {
		return nil, fmt.Errorf("策略标识不能为空")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if inst, ok := m.instances[target]; ok && inst != nil {
		return inst, nil
	}

	var exactName *StrategyInstance
	var prefixID *StrategyInstance
	var fuzzyName *StrategyInstance
	for _, inst := range m.instances {
		if inst == nil {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(inst.ID))
		name := strings.ToLower(strings.TrimSpace(inst.Name))
		if id == key {
			return inst, nil
		}
		if prefixID == nil && strings.HasPrefix(id, key) {
			prefixID = inst
		}
		if name == key {
			if exactName != nil {
				return nil, fmt.Errorf("匹配到多个同名策略，请改用策略 ID")
			}
			exactName = inst
			continue
		}
		if fuzzyName == nil && strings.Contains(name, key) {
			fuzzyName = inst
		}
	}
	if exactName != nil {
		return exactName, nil
	}
	if prefixID != nil {
		return prefixID, nil
	}
	if fuzzyName != nil {
		return fuzzyName, nil
	}
	return nil, fmt.Errorf("未找到策略：%s", target)
}

func (m *Manager) FindStrategyForCommand(target string) (*StrategyInstance, error) {
	return m.findStrategyForCommand(target)
}
