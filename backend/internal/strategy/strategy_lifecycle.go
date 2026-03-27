package strategy

import (
	"fmt"
	"strings"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

func (m *Manager) StartStrategy(id string) error {
	if m == nil {
		return fmt.Errorf("nil manager")
	}
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}
	inst.mu.Lock()
	if inst.Status == StatusRunning || inst.Status == StatusStarting {
		inst.mu.Unlock()
		return nil
	}
	inst.mu.Unlock()
	m.setStrategyStatus(inst, StatusStarting)
	select {
	case m.startCh <- id:
	default:
		m.setStrategyStatus(inst, StatusError)
		return fmt.Errorf("strategy start queue is full")
	}
	return nil
}

func (m *Manager) StopStrategy(id string, force bool) error {
	if m == nil {
		return fmt.Errorf("nil manager")
	}
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}
	inst.mu.Lock()
	if inst.Status == StatusStopped {
		inst.mu.Unlock()
		return nil
	}
	inst.stopping = true
	inst.mu.Unlock()
	select {
	case m.stopCh <- stopReq{id: id, force: force}:
	default:
		inst.mu.Lock()
		inst.stopping = false
		inst.mu.Unlock()
		return fmt.Errorf("strategy stop queue is full")
	}
	return nil
}

func (m *Manager) runStartWorker() {
	for id := range m.startCh {
		func() {
			defer func() { recover() }()
			if err := m.startStrategyNow(id); err != nil {
				m.markStrategyStartFailed(id, err)
			}
		}()
	}
}

func (m *Manager) runStopWorker() {
	for req := range m.stopCh {
		func() {
			defer func() { recover() }()
			if err := m.stopStrategyNow(req.id, req.force); err != nil {
				m.markStrategyStopFailed(req.id, err)
			}
		}()
	}
}

func (m *Manager) startStrategyNow(id string) error {
	inst, err := m.getStartableStrategy(id)
	if err != nil {
		return err
	}
	if inst == nil {
		return nil
	}
	plan, err := m.buildStrategyStartPlan(inst)
	if err != nil {
		return err
	}
	if plan == nil {
		return nil
	}
	proc, err := m.startStrategyProcess(inst, plan)
	if err != nil {
		return err
	}
	if proc == nil {
		return nil
	}
	m.activateStartedStrategy(inst, plan, proc)
	m.attachRedisIO(inst, plan.redisBus, plan.logTrace)
	m.syncStrategyDebugConfig(inst)
	m.attachUserDataStream(inst)
	_ = m.attachMarketData(inst, plan.redisBus, plan.feedSymbols, plan.runCfg)
	go inst.readStdout()
	go inst.readStderr(proc.stderr)
	return nil
}

func (m *Manager) markStrategyStartFailed(id string, err error) {
	if m == nil {
		return
	}
	m.mu.RLock()
	inst := m.instances[id]
	m.mu.RUnlock()
	if inst == nil {
		return
	}
	m.setStrategyStatus(inst, StatusError)
	if err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("Strategy start failed: %v", err))
	}
}

func (m *Manager) stopStrategyNow(id string, force bool) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}
	if !force {
		if err := m.validateStrategyCanStop(inst); err != nil {
			return err
		}
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.Status != StatusRunning && inst.Status != StatusStarting {
		inst.stopping = false
		return nil
	}
	if inst.redisCancel != nil {
		inst.redisCancel()
		inst.redisCancel = nil
	}
	if len(inst.candleStops) > 0 {
		for _, stop := range inst.candleStops {
			if stop != nil {
				stop()
			}
		}
		inst.candleStops = nil
	}
	if inst.cmd != nil && inst.cmd.Process != nil {
		if err := inst.cmd.Process.Kill(); err != nil {
			inst.stopping = false
			return err
		}
	}
	inst.Status = StatusStopped
	inst.stopping = false
	go m.setStrategyStatus(inst, StatusStopped)
	return nil
}

func (m *Manager) validateStrategyCanStop(inst *StrategyInstance) error {
	if inst == nil {
		return nil
	}
	var openCount int64
	database.DB.Model(&models.StrategyPosition{}).
		Where("owner_id = ? AND strategy_id = ? AND status = ?", inst.OwnerID, inst.ID, "open").
		Count(&openCount)
	if openCount > 0 {
		return fmt.Errorf("strategy has open positions; close positions before stopping")
	}
	bx, ok := m.exchange.(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return nil
	}
	syms := parseSymbolsValue(inst.Config["symbols"])
	if len(syms) == 0 {
		if sym, ok := inst.Config["symbol"].(string); ok && strings.TrimSpace(sym) != "" {
			syms = []string{strings.TrimSpace(sym)}
		}
	}
	if len(syms) == 0 {
		return nil
	}
	exPos, err := bx.FetchPositions(inst.OwnerID, "active")
	if err != nil {
		return nil
	}
	want := map[string]struct{}{}
	for _, s := range syms {
		want[exchange.NormalizeSymbol(s)] = struct{}{}
	}
	for _, p := range exPos {
		if _, ok := want[exchange.NormalizeSymbol(p.Symbol)]; ok && p.Amount > 0 {
			return fmt.Errorf("strategy has open positions on exchange; close positions before stopping")
		}
	}
	return nil
}

func (m *Manager) markStrategyStopFailed(id string, err error) {
	if m == nil {
		return
	}
	m.mu.RLock()
	inst := m.instances[id]
	m.mu.RUnlock()
	if inst == nil {
		return
	}
	inst.mu.Lock()
	inst.stopping = false
	inst.mu.Unlock()
	if err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("Strategy stop failed: %v", err))
	}
}
