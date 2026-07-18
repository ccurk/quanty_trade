package strategy

import (
	"fmt"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

// SymbolRotateResult reports what a rotation actually applied.
type SymbolRotateResult struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Skipped []string `json:"skipped"` // "SYMBOL:reason"
	Symbols []string `json:"symbols"` // resulting live feed set
}

// emitSymbolAudit writes a greppable, structured symbol-rotation audit line so a
// cron can attribute "does rotation itself add alpha" after the fact.
func (m *Manager) emitSymbolAudit(inst *StrategyInstance, event, sym, reason string) {
	emitStrategyLog(inst, "info", fmt.Sprintf("[SYMBOL_ROTATE] actor=api event=%s symbol=%s reason=%s", event, sym, reason))
}

// hasOpenSymbolPosition reports whether the instance currently holds an open
// position on the symbol. Rotation must never drop such a symbol.
func hasOpenSymbolPosition(inst *StrategyInstance, sym string) bool {
	q := func(s string) int64 {
		var n int64
		_ = database.DB.Model(&models.StrategyPosition{}).
			Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?",
				inst.OwnerID, inst.ID, s, "open").
			Count(&n).Error
		return n
	}
	norm := exchange.NormalizeSymbol(sym)
	if q(norm) > 0 {
		return true
	}
	if !strings.EqualFold(norm, sym) && q(sym) > 0 {
		return true
	}
	return false
}

// addSymbolToRunning subscribes a new symbol on a running instance and appends
// it to the feed set, triggering a history backfill so its warmup fills fast.
// No-op (returns false) if the symbol is already in the feed.
func (m *Manager) addSymbolToRunning(inst *StrategyInstance, sym, reason string) bool {
	sym = strings.TrimSpace(sym)
	if sym == "" {
		return false
	}
	inst.mu.Lock()
	for _, s := range inst.feedSymbols {
		if strings.EqualFold(s, sym) {
			inst.mu.Unlock()
			return false
		}
	}
	if _, ok := inst.candleStops[sym]; ok {
		inst.mu.Unlock()
		return false
	}
	inst.feedSymbols = append(inst.feedSymbols, sym)
	inst.resync = true // re-publish history for the feed → seeds the new symbol's warmup
	inst.resyncNextAt = time.Time{}
	inst.mu.Unlock()

	m.subscribeOneSymbol(inst, m.redisBus, sym, 0)
	m.emitSymbolAudit(inst, "add", sym, reason)
	return true
}

// removeSymbolFromRunning tears down a symbol's candle feed and drops it from the
// feed set. Refuses (returns a reason) when the symbol holds an open position.
func (m *Manager) removeSymbolFromRunning(inst *StrategyInstance, sym, reason string) (bool, string) {
	sym = strings.TrimSpace(sym)
	if sym == "" {
		return false, "empty"
	}
	if hasOpenSymbolPosition(inst, sym) {
		return false, "has_open_position"
	}
	inst.mu.Lock()
	if stop, ok := inst.candleStops[sym]; ok && stop != nil {
		stop()
	}
	delete(inst.candleStops, sym)
	delete(inst.candlePubCount, sym)
	delete(inst.candleRxCount, sym)
	delete(inst.lastCandleClose, sym)
	delete(inst.lastCandleAt, sym)
	delete(inst.lastCandleSeenAt, sym)
	delete(inst.candleEvent, sym)
	delete(inst.candleEventInfo, sym)
	delete(inst.candleEventAt, sym)
	kept := inst.feedSymbols[:0]
	for _, s := range inst.feedSymbols {
		if !strings.EqualFold(s, sym) {
			kept = append(kept, s)
		}
	}
	inst.feedSymbols = kept
	inst.mu.Unlock()

	m.emitSymbolAudit(inst, "remove", sym, reason)
	return true, ""
}

// RotateSymbols applies a symbol rotation to a RUNNING instance: remove the given
// symbols (unless they hold an open position) then add the given ones. This is
// the platform mechanism the (phase-1) rotation cron drives — it does NOT decide
// which symbols to rotate.
func (m *Manager) RotateSymbols(instanceID string, add, remove []string, reason string) (SymbolRotateResult, error) {
	m.mu.RLock()
	inst := m.instances[instanceID]
	m.mu.RUnlock()
	if inst == nil {
		return SymbolRotateResult{}, fmt.Errorf("strategy %s not running", instanceID)
	}
	if strings.TrimSpace(reason) == "" {
		reason = "rotation"
	}
	res := SymbolRotateResult{}
	for _, s := range remove {
		if ok, why := m.removeSymbolFromRunning(inst, s, reason); ok {
			res.Removed = append(res.Removed, s)
		} else {
			res.Skipped = append(res.Skipped, s+":"+why)
		}
	}
	for _, s := range add {
		if m.addSymbolToRunning(inst, s, reason) {
			res.Added = append(res.Added, s)
		} else {
			res.Skipped = append(res.Skipped, s+":exists")
		}
	}
	inst.mu.Lock()
	res.Symbols = append([]string(nil), inst.feedSymbols...)
	inst.mu.Unlock()
	return res, nil
}

// RunningSymbols returns the instance's current live feed symbol set (the
// in-memory set, which differs from the persisted config when auto-selected).
func (m *Manager) RunningSymbols(instanceID string) ([]string, error) {
	m.mu.RLock()
	inst := m.instances[instanceID]
	m.mu.RUnlock()
	if inst == nil {
		return nil, fmt.Errorf("strategy %s not running", instanceID)
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return append([]string(nil), inst.feedSymbols...), nil
}
