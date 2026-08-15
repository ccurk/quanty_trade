package strategy

import (
	"fmt"
	"math"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

// emitExitAudit writes a structured, greppable engine-exit audit line to the
// strategy log (actor=engine) for cron post-analysis. reason is an enum, e.g.
// breakeven_moved / trailing_moved / max_hold_timeout / hunger_tp / hunger_sl /
// guard_tp / guard_sl / guard_roi_tp / guard_roi_sl.
func (m *Manager) emitExitAudit(inst *StrategyInstance, symbol, reason string, roi, pnl float64, extra string) {
	if inst == nil {
		return
	}
	emitStrategyLog(inst, "info", fmt.Sprintf("[EXIT_AUDIT] actor=engine reason=%s symbol=%s roi=%.4f%% pnl=%.4f %s", reason, symbol, roi, pnl, extra))
}

// breakevenFeeBuffer is added past entry so the breakeven stop still covers a
// round-trip taker fee (~0.066%) plus a little slippage.
const breakevenFeeBuffer = 0.0010

// exitATR resolves the ATR used by exit engineering: the value captured at entry
// if present, else reverse-derived from the stop distance (|entry-sl|/atr_sl_mult).
func exitATR(inst *StrategyInstance, row models.StrategyPosition, entry float64) float64 {
	atr := row.AtrAbs
	if atr <= 0 {
		slMult := getNumber(inst.Config["atr_sl_mult"])
		if slMult <= 0 {
			slMult = 1.0
		}
		atr = math.Abs(entry-row.StopLoss) / slMult
	}
	return atr
}

// computeBreakevenSL decides whether to move the stop to breakeven and returns
// the new stop price. Pure/unit-testable. shouldMove is false when disabled, not
// yet profitable enough, when the new stop would not improve on the current one,
// or when it would immediately trigger against the current price.
func computeBreakevenSL(side string, entry, currentPrice, curSL, atr, trigAtr, feeBuf float64) (float64, bool) {
	if trigAtr <= 0 || atr <= 0 || entry <= 0 || currentPrice <= 0 {
		return 0, false
	}
	profitDist := currentPrice - entry
	if side == "sell" {
		profitDist = entry - currentPrice
	}
	if profitDist < trigAtr*atr {
		return 0, false
	}
	if side == "sell" {
		newSL := entry * (1 - feeBuf)
		if curSL > 0 && newSL >= curSL {
			return 0, false
		}
		if currentPrice >= newSL {
			return 0, false
		}
		return newSL, true
	}
	newSL := entry * (1 + feeBuf)
	if newSL <= curSL {
		return 0, false
	}
	if currentPrice <= newSL {
		return 0, false
	}
	return newSL, true
}

// computeTrailSL returns the trailing stop price and whether to ratchet it.
// Trailing activates once unrealized profit reaches activationAtr×ATR; then the
// stop follows currentPrice pulled back by callbackPct (%), only ever moving in
// the profitable direction and never crossing the current price. Pure/testable.
func computeTrailSL(side string, entry, currentPrice, curSL, atr, activationAtr, callbackPct float64) (float64, bool) {
	if callbackPct <= 0 || atr <= 0 || entry <= 0 || currentPrice <= 0 {
		return 0, false
	}
	profitDist := currentPrice - entry
	if side == "sell" {
		profitDist = entry - currentPrice
	}
	if activationAtr > 0 && profitDist < activationAtr*atr {
		return 0, false // not activated yet
	}
	cb := callbackPct / 100.0
	if side == "sell" {
		newSL := currentPrice * (1 + cb)
		if curSL > 0 && newSL >= curSL { // only ratchet a short's stop down
			return 0, false
		}
		if currentPrice >= newSL {
			return 0, false
		}
		return newSL, true
	}
	newSL := currentPrice * (1 - cb)
	if newSL <= curSL { // only ratchet a long's stop up
		return 0, false
	}
	if currentPrice <= newSL {
		return 0, false
	}
	return newSL, true
}

// applyStopMove re-places the exchange TP + a new SL under lockTPSL (the proven
// tpsl-guard sequence), best-effort restores the original SL on failure, and
// writes the new SL back to the position row so the 15s guard does not revert
// it. Returns true on success.
func (m *Manager) applyStopMove(inst *StrategyInstance, uid uint, row models.StrategyPosition, tpVal, newSL float64) bool {
	bx, ok := m.exchange.(*exchange.BinanceExchange)
	if !ok {
		return false
	}
	unlock := m.lockTPSL(uid, row.Symbol)
	defer unlock()
	// 棘轮护栏:止损只许收紧、不许放宽。调用方(保本→追踪→金字塔)在同一轮里对 row
	// 的值拷贝依次移动,前一步写了 DB 却没写回 row,金字塔又按旧的宽止损重算 → 会把
	// 刚收紧的止损又放宽(trailing 关时永久)。这里以【DB 当前止损】为准判定,放宽方向
	// 的移动直接拒绝,与传入的 row 快照是否陈旧无关(CR P0)。
	if newSL > 0 {
		var cur models.StrategyPosition
		if err := database.DB.Select("stop_loss", "direction").Where("id = ?", row.ID).First(&cur).Error; err == nil && cur.StopLoss > 0 {
			dir := strings.ToLower(strings.TrimSpace(cur.Direction))
			if dir == "" {
				dir = strings.ToLower(strings.TrimSpace(row.Direction))
			}
			loosens := newSL < cur.StopLoss // long: 止损越高越紧
			if dir == "sell" || dir == "short" {
				loosens = newSL > cur.StopLoss // short: 止损越低越紧
			}
			if loosens {
				emitStrategyLog(inst, "info", fmt.Sprintf("拒绝放宽止损(棘轮): symbol=%s dir=%s 当前SL=%.8f 拟移至=%.8f", row.Symbol, dir, cur.StopLoss, newSL))
				return false
			}
		}
	}
	_ = bx.CancelUSDMAlgoOpenOrders(uid, row.Symbol)
	baseClientOrderID := models.GenerateUUID()
	created, err := bx.PlaceUSDMTPStopOrders(uid, baseClientOrderID, row.Symbol, tpVal, newSL)
	if err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("移动止损失败 symbol=%s new_sl=%.8f err=%v，尝试恢复原止损", row.Symbol, newSL, err))
		// DB stop_loss is NOT updated on failure (still old), so the 15s tpsl
		// guard also re-places the original stop as a second safety net.
		if _, e := bx.PlaceUSDMTPStopOrders(uid, models.GenerateUUID(), row.Symbol, tpVal, row.StopLoss); e != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("恢复原止损也失败 symbol=%s err=%v（15s 守护会兜底补挂）", row.Symbol, e))
		}
		return false
	}
	m.storeLinkedTPSLOrders(inst, row.ID, row.Symbol, baseClientOrderID, created)
	_ = database.DB.Model(&models.StrategyPosition{}).Where("id = ?", row.ID).
		Updates(map[string]interface{}{"stop_loss": newSL, "updated_at": time.Now()}).Error
	return true
}

// maybeMoveBreakeven moves the stop to breakeven once unrealized profit reaches
// breakeven_trigger_atr × ATR, exactly once per position (BreakevenMoved flag).
// No-op when disabled or already moved.
func (m *Manager) maybeMoveBreakeven(inst *StrategyInstance, uid uint, row models.StrategyPosition, pos exchange.Position, side string, currentPrice, tpResolved float64) {
	if inst == nil || row.BreakevenMoved {
		return
	}
	trig := getNumber(inst.Config["breakeven_trigger_atr"])
	if trig <= 0 {
		return
	}
	entry := pos.Price
	if entry <= 0 {
		entry = row.AvgPrice
	}
	atr := exitATR(inst, row, entry)
	newSL, ok := computeBreakevenSL(side, entry, currentPrice, row.StopLoss, atr, trig, breakevenFeeBuffer)
	if !ok {
		return
	}
	tpVal := row.TakeProfit
	if tpVal <= 0 {
		tpVal = tpResolved
	}
	if m.applyStopMove(inst, uid, row, tpVal, newSL) {
		_ = database.DB.Model(&models.StrategyPosition{}).Where("id = ?", row.ID).
			Update("breakeven_moved", true).Error
		m.emitExitAudit(inst, row.Symbol, "breakeven_moved", pos.ReturnRate, pos.UnrealizedPnL,
			fmt.Sprintf("entry=%.8f old_sl=%.8f new_sl=%.8f atr=%.8f trig_atr=%.2f", entry, row.StopLoss, newSL, atr, trig))
	}
}

// maybeTrail ratchets the stop toward price once trailing is enabled and
// activated (trailing_activation_atr × ATR of profit), keeping the fixed TP as
// an upper cap. This is a synthetic trailing stop that reuses the proven
// cancel/replace path; it delivers the same "lock in profit on a pullback"
// behaviour as a native TRAILING_STOP_MARKET without a new exchange order type.
func (m *Manager) maybeTrail(inst *StrategyInstance, uid uint, row models.StrategyPosition, pos exchange.Position, side string, currentPrice, tpResolved float64) {
	if inst == nil || !getBool(inst.Config["trailing_enabled"]) {
		return
	}
	cb := getNumber(inst.Config["trailing_callback_pct"])
	act := getNumber(inst.Config["trailing_activation_atr"])
	entry := pos.Price
	if entry <= 0 {
		entry = row.AvgPrice
	}
	atr := exitATR(inst, row, entry)
	newSL, ok := computeTrailSL(side, entry, currentPrice, row.StopLoss, atr, act, cb)
	if !ok {
		return
	}
	tpVal := row.TakeProfit
	if tpVal <= 0 {
		tpVal = tpResolved
	}
	if m.applyStopMove(inst, uid, row, tpVal, newSL) {
		m.emitExitAudit(inst, row.Symbol, "trailing_moved", pos.ReturnRate, pos.UnrealizedPnL,
			fmt.Sprintf("entry=%.8f new_sl=%.8f atr=%.8f cb=%.2f%% act_atr=%.2f", entry, newSL, atr, cb, act))
	}
}
