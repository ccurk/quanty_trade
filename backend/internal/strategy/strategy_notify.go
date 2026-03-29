package strategy

import (
	"fmt"
	"strings"
)

type RuntimeNotifier interface {
	NotifyTradeOpened(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, takeProfit float64, stopLoss float64, status string)
	NotifyTradeClosed(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, status string, reason string)
	NotifyStrategyStatus(ownerID uint, strategyID string, strategyName string, status string)
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

func (m *Manager) notifyTradeClosed(inst *StrategyInstance, symbol string, side string, qty float64, price float64, status string, reason string) {
	if m == nil || inst == nil {
		return
	}
	m.mu.RLock()
	notifier := m.notifier
	m.mu.RUnlock()
	if notifier == nil {
		return
	}
	notifier.NotifyTradeClosed(inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), symbol, side, qty, price, status, reason)
}

func (m *Manager) NotifyExternalTradeClosed(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, status string, reason string) {
	if m == nil {
		return
	}
	m.mu.RLock()
	notifier := m.notifier
	m.mu.RUnlock()
	if notifier == nil {
		return
	}
	notifier.NotifyTradeClosed(ownerID, strategyID, strategyName, exchangeName, symbol, side, qty, price, status, reason)
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
