package strategy

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"quanty_trade/internal/bus"
	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
)

func (m *Manager) enqueueSignalForSelection(inst *StrategyInstance, s bus.SignalMessage) {
	if m == nil || inst == nil {
		return
	}
	m.sigMu.Lock()
	key := inst.ID
	m.pendingSignals[key] = append(m.pendingSignals[key], s)
	isFirst := len(m.pendingSignals[key]) == 1
	wait := m.signalBatchWait
	m.sigMu.Unlock()
	if isFirst {
		go func() {
			time.Sleep(wait)
			m.processSignalBatch(inst.ID)
		}()
	}
}

func (m *Manager) processSignalBatch(strategyID string) {
	m.mu.RLock()
	inst := m.instances[strategyID]
	m.mu.RUnlock()
	if inst == nil {
		return
	}
	m.sigMu.Lock()
	batch := m.pendingSignals[strategyID]
	delete(m.pendingSignals, strategyID)
	m.sigMu.Unlock()
	if len(batch) == 0 {
		return
	}

	latestBySymbol := map[string]bus.SignalMessage{}
	for _, sig := range batch {
		sym := strings.TrimSpace(sig.Symbol)
		if sym == "" || !isAllowedSymbol(inst, sym) {
			continue
		}
		prev, ok := latestBySymbol[sym]
		if !ok || sig.GeneratedAt.After(prev.GeneratedAt) {
			latestBySymbol[sym] = sig
		}
	}

	filtered := make([]bus.SignalMessage, 0, len(latestBySymbol))
	now := time.Now()
	for sym, sig := range latestBySymbol {
		cAt := time.Time{}
		inst.mu.Lock()
		if inst.lastCandleAt != nil {
			cAt = inst.lastCandleAt[sym]
		}
		inst.mu.Unlock()
		ok := false
		if !cAt.IsZero() {
			early := cAt.Add(-10 * time.Second)
			late := cAt.Add(10 * time.Second)
			ok = sig.GeneratedAt.After(early) && sig.GeneratedAt.Before(late)
		} else {
			ok = sig.GeneratedAt.After(now.Add(-10 * time.Second))
		}
		if ok {
			filtered = append(filtered, sig)
		}
	}
	if len(filtered) == 0 {
		return
	}

	pxCache := map[string]float64{}
	type cand struct {
		s  bus.SignalMessage
		rr float64
		ok bool
		px float64
	}
	cands := make([]cand, 0, len(filtered))
	for _, sig := range filtered {
		sym := strings.TrimSpace(sig.Symbol)
		if sym == "" || !isAllowedSymbol(inst, sym) {
			continue
		}
		inst.mu.Lock()
		px := 0.0
		if inst.lastCandleClose != nil {
			px = inst.lastCandleClose[sym]
		}
		inst.mu.Unlock()
		if px <= 0 {
			px = pxCache[sym]
		}
		if px <= 0 {
			continue
		}
		rr := 0.0
		side := strings.ToLower(strings.TrimSpace(sig.Side))
		if side == "long" || side == "buy" || side == "" {
			if sig.TakeProfit > px && sig.StopLoss < px && sig.StopLoss > 0 {
				rr = (sig.TakeProfit - px) / (px - sig.StopLoss)
			}
		} else {
			if sig.TakeProfit < px && sig.StopLoss > px {
				rr = (px - sig.TakeProfit) / (sig.StopLoss - px)
			}
		}
		ok := rr > 0
		cands = append(cands, cand{s: sig, rr: rr, ok: ok, px: px})
	}
	if len(cands) == 0 {
		return
	}

	sort.Slice(cands, func(i, j int) bool { return cands[i].rr > cands[j].rr })
	chosen := false
	for i := 0; i < len(cands); i++ {
		sig := cands[i].s
		amount := clampOrderAmount(inst, sig.Amount)
		side := strings.ToLower(strings.TrimSpace(sig.Side))
		if side == "long" {
			side = "buy"
		} else if side == "short" {
			side = "sell"
		} else if side == "" {
			side = "buy"
		}
		if m.tryPlaceCandidate(inst, sig.Symbol, side, amount, sig.TakeProfit, sig.StopLoss, strings.TrimSpace(sig.SignalID)) {
			chosen = true
			for j := 0; j < len(cands); j++ {
				if j == i {
					continue
				}
				emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: better candidate selected symbol=%s rr=%0.4f", cands[j].s.Symbol, cands[j].rr))
			}
			break
		}
		emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: candidate failed constraints symbol=%s rr=%0.4f", sig.Symbol, cands[i].rr))
	}
	if !chosen {
		emitStrategyLog(inst, "info", "Skip signal: all candidates failed")
	}
}

func (m *Manager) tryPlaceCandidate(inst *StrategyInstance, symbol string, side string, amount float64, tp float64, sl float64, signalID string) bool {
	if inst == nil {
		return false
	}
	m.enqueueOrderForInstance(inst, symbol, side, amount, 0, tp, sl, signalID)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var ord models.StrategyOrder
		err := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ?", inst.OwnerID, inst.ID, symbol).
			Order("requested_at desc").
			First(&ord).Error
		if err == nil && ord.ID > 0 {
			st := strings.ToLower(strings.TrimSpace(ord.Status))
			if st == "filled" || st == "new" || st == "requested" {
				return true
			}
			if st == "failed" || st == "rejected" {
				return false
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func (m *Manager) handleRedisSignal(inst *StrategyInstance, s bus.SignalMessage) {
	if inst == nil {
		return
	}
	logSignal := getBool(inst.Config["log_signal"]) || getBool(inst.Config["log_redis"]) || getBool(inst.Config["debug"])
	symbol := strings.TrimSpace(s.Symbol)
	if symbol == "" {
		if logSignal {
			emitStrategyLog(inst, "info", "Skip signal: empty symbol")
		}
		return
	}
	if !isAllowedSymbol(inst, symbol) {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: symbol not allowed symbol=%s", symbol))
		}
		return
	}
	action := strings.ToLower(strings.TrimSpace(s.Action))
	if action == "" {
		action = "open"
	}
	if action != "open" {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: unsupported action=%s", action))
		}
		return
	}
	side := strings.ToLower(strings.TrimSpace(s.Side))
	if side == "long" {
		side = "buy"
	} else if side == "short" {
		side = "sell"
	} else if side == "auto" || side == "both" {
		side = ""
	}
	if side == "" {
		side = strings.ToLower(strings.TrimSpace(getString(inst.Config["default_open_side"])))
		if side == "" {
			side = "buy"
		}
		if side == "long" {
			side = "buy"
		} else if side == "short" {
			side = "sell"
		}
	}
	if side != "buy" && side != "sell" {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: invalid side=%s", side))
		}
		return
	}
	amount := clampOrderAmount(inst, s.Amount)
	if amount <= 0 {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: amount invalid raw=%v", s.Amount))
		}
		return
	}
	if !(s.TakeProfit > 0 && s.StopLoss > 0) {
		emitStrategyLog(inst, "info", fmt.Sprintf("Skip signal: 缺少止盈止损，拒绝开仓 symbol=%s side=%s amount=%v tp=%v sl=%v", symbol, side, amount, s.TakeProfit, s.StopLoss))
		return
	}
	if logSignal {
		emitStrategyLog(inst, "info", fmt.Sprintf("Recv signal: symbol=%s side=%s amount=%v tp=%v sl=%v signal_id=%s", symbol, side, amount, s.TakeProfit, s.StopLoss, strings.TrimSpace(s.SignalID)))
	}
	m.enqueueSignalForSelection(inst, s)
}
