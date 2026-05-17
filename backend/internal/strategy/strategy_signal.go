package strategy

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"quanty_trade/internal/bus"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

type symbolEntryStats struct {
	RecentCount      int
	ConsecutiveCount int
	LastEntryAt      time.Time
}

func normalizeStrategySide(raw string) string {
	side := strings.ToLower(strings.TrimSpace(raw))
	switch side {
	case "buy", "long", "bull", "up", "多", "做多", "看多":
		return "buy"
	case "sell", "short", "bear", "down", "空", "做空", "看空", "低":
		return "sell"
	default:
		return ""
	}
}

func parseNonNaturalEntrySequence(raw string) []string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '|', '/', '\\', '、', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if side := normalizeStrategySide(part); side != "" {
			out = append(out, side)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, r := range text {
		if side := normalizeStrategySide(string(r)); side != "" {
			out = append(out, side)
		}
	}
	return out
}

func flipStrategySide(side string) string {
	switch normalizeStrategySide(side) {
	case "buy":
		return "sell"
	case "sell":
		return "buy"
	default:
		return ""
	}
}

func resolveNonNaturalEntrySide(inst *StrategyInstance, slotIndex int, anchorSide string) (string, bool) {
	if inst == nil || slotIndex <= 1 || !getBool(inst.Config["non_natural_entry_enabled"]) {
		return "", false
	}
	seq := parseNonNaturalEntrySequence(getString(inst.Config["non_natural_entry_sequence"]))
	if len(seq) < 2 || slotIndex > len(seq) {
		return "", false
	}
	anchor := normalizeStrategySide(anchorSide)
	base := normalizeStrategySide(seq[0])
	target := normalizeStrategySide(seq[slotIndex-1])
	if anchor == "" || base == "" || target == "" {
		return "", false
	}
	if target == base {
		return anchor, true
	}
	return flipStrategySide(anchor), true
}

func remapTPSLBySide(inst *StrategyInstance, side string, entryPrice float64, takeProfit float64, stopLoss float64) (float64, float64) {
	tp, sl := resolveTPSLFromROI(inst, side, entryPrice, 0, 0)
	rewardDist := math.Abs(takeProfit - entryPrice)
	riskDist := math.Abs(stopLoss - entryPrice)
	if side == "buy" {
		if tp <= 0 && rewardDist > 0 {
			tp = entryPrice + rewardDist
		}
		if sl <= 0 && riskDist > 0 {
			sl = entryPrice - riskDist
		}
	} else if side == "sell" {
		if tp <= 0 && rewardDist > 0 {
			tp = entryPrice - rewardDist
		}
		if sl <= 0 && riskDist > 0 {
			sl = entryPrice + riskDist
		}
	}
	return tp, sl
}

func parseClockMinutes(raw string) (int, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	hour := int(getNumber(parts[0]))
	minute := int(getNumber(parts[1]))
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func resolveEntryTimeWindows(inst *StrategyInstance, now time.Time) (bool, string, bool) {
	if inst == nil {
		return true, "", false
	}
	raw := strings.TrimSpace(getString(inst.Config["entry_time_windows"]))
	if raw == "" {
		return true, "", false
	}
	current := now.Hour()*60 + now.Minute()
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '|', '\n', '\r':
			return true
		default:
			return false
		}
	})
	normalized := make([]string, 0, len(parts))
	validFound := false
	for _, part := range parts {
		seg := strings.TrimSpace(part)
		if seg == "" {
			continue
		}
		bounds := strings.Split(seg, "-")
		if len(bounds) != 2 {
			continue
		}
		start, okStart := parseClockMinutes(bounds[0])
		end, okEnd := parseClockMinutes(bounds[1])
		if !okStart || !okEnd {
			continue
		}
		validFound = true
		normalized = append(normalized, fmt.Sprintf("%s-%s", strings.TrimSpace(bounds[0]), strings.TrimSpace(bounds[1])))
		if start == end {
			return true, strings.Join(normalized, ","), true
		}
		if start < end {
			if current >= start && current < end {
				return true, strings.Join(normalized, ","), true
			}
			continue
		}
		if current >= start || current < end {
			return true, strings.Join(normalized, ","), true
		}
	}
	return !validFound, strings.Join(normalized, ","), validFound
}

func symbolReentryCooldown(inst *StrategyInstance) time.Duration {
	if inst == nil {
		return 0
	}
	minutes := int(getNumber(inst.Config["symbol_reentry_cooldown_minutes"]))
	if minutes <= 0 {
		return 0
	}
	return time.Duration(minutes) * time.Minute
}

func maxConsecutiveEntriesPerSymbol(inst *StrategyInstance) int {
	if inst == nil {
		return 0
	}
	if v := int(getNumber(inst.Config["max_consecutive_entries_per_symbol"])); v > 0 {
		return v
	}
	return 0
}

func consecutiveEntryCount(inst *StrategyInstance, symbol string) int {
	if inst == nil || database.DB == nil {
		return 0
	}
	symKey := exchange.NormalizeSymbol(symbol)
	if symKey == "" {
		return 0
	}
	limit := maxConsecutiveEntriesPerSymbol(inst)
	if limit <= 0 {
		return 0
	}
	lookback := limit + 20
	if lookback < 20 {
		lookback = 20
	}
	var rows []models.StrategyOrder
	_ = database.DB.Where("owner_id = ? AND strategy_id = ? AND purpose = ? AND status IN ?", inst.OwnerID, inst.ID, "entry", []string{"requested", "new", "partially_filled", "filled"}).
		Order("requested_at desc, id desc").
		Limit(lookback).
		Find(&rows).Error
	count := 0
	for _, row := range rows {
		rowSym := exchange.NormalizeSymbol(row.Symbol)
		if rowSym == "" {
			continue
		}
		if rowSym != symKey {
			break
		}
		count++
	}
	return count
}

func canOpenSymbolByConsecutiveLimit(inst *StrategyInstance, symbol string) (bool, int, int) {
	limit := maxConsecutiveEntriesPerSymbol(inst)
	if limit <= 0 {
		return true, 0, 0
	}
	count := consecutiveEntryCount(inst, symbol)
	return count < limit, count, limit
}

func loadRecentEntryStats(inst *StrategyInstance, limit int) map[string]symbolEntryStats {
	if inst == nil || database.DB == nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	var rows []models.StrategyOrder
	_ = database.DB.Where("owner_id = ? AND strategy_id = ? AND purpose = ? AND status IN ?", inst.OwnerID, inst.ID, "entry", []string{"requested", "new", "partially_filled", "filled"}).
		Order("requested_at desc, id desc").
		Limit(limit).
		Find(&rows).Error
	stats := make(map[string]symbolEntryStats, len(rows))
	for idx, row := range rows {
		symKey := exchange.NormalizeSymbol(row.Symbol)
		if symKey == "" {
			continue
		}
		item := stats[symKey]
		item.RecentCount++
		if idx == 0 {
			item.LastEntryAt = row.RequestedAt
		}
		stats[symKey] = item
	}
	prevSym := ""
	for _, row := range rows {
		symKey := exchange.NormalizeSymbol(row.Symbol)
		if symKey == "" {
			continue
		}
		if prevSym != "" && symKey != prevSym {
			break
		}
		item := stats[symKey]
		item.ConsecutiveCount++
		stats[symKey] = item
		prevSym = symKey
	}
	return stats
}

func canOpenSymbolByCooldown(inst *StrategyInstance, stats symbolEntryStats) (bool, time.Duration, time.Time) {
	cooldown := symbolReentryCooldown(inst)
	if cooldown <= 0 || stats.LastEntryAt.IsZero() {
		return true, 0, stats.LastEntryAt
	}
	elapsed := time.Since(stats.LastEntryAt)
	if elapsed >= cooldown {
		return true, 0, stats.LastEntryAt
	}
	return false, cooldown - elapsed, stats.LastEntryAt
}

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
	timeMismatch := make([]string, 0, len(latestBySymbol))
	now := time.Now()
	for sym, sig := range latestBySymbol {
		cAt := time.Time{}
		seenAt := time.Time{}
		inst.mu.Lock()
		if inst.lastCandleAt != nil {
			cAt = inst.lastCandleAt[sym]
		}
		if inst.lastCandleSeenAt != nil {
			seenAt = inst.lastCandleSeenAt[sym]
		}
		inst.mu.Unlock()
		ok := false
		baseAt := cAt
		if !seenAt.IsZero() {
			baseAt = seenAt
		}
		if !baseAt.IsZero() {
			early := baseAt.Add(-10 * time.Second)
			late := baseAt.Add(10 * time.Second)
			ok = sig.GeneratedAt.After(early) && sig.GeneratedAt.Before(late)
		} else {
			ok = sig.GeneratedAt.After(now.Add(-10 * time.Second))
		}
		if ok {
			filtered = append(filtered, sig)
		} else if len(timeMismatch) < 8 {
			baseText := "none"
			if !baseAt.IsZero() {
				baseText = baseAt.Format(time.RFC3339)
			}
			candleText := "none"
			if !cAt.IsZero() {
				candleText = cAt.Format(time.RFC3339)
			}
			seenText := "none"
			if !seenAt.IsZero() {
				seenText = seenAt.Format(time.RFC3339)
			}
			timeMismatch = append(timeMismatch, fmt.Sprintf("%s(gen=%s base=%s candle=%s seen=%s)", sym, sig.GeneratedAt.Format(time.RFC3339), baseText, candleText, seenText))
		}
	}
	if len(filtered) == 0 {
		if len(timeMismatch) > 0 {
			emitStrategyLog(inst, "info", fmt.Sprintf("本批信号因时间窗不匹配被过滤 batch=%d latest_symbols=%d details=%s", len(batch), len(latestBySymbol), strings.Join(timeMismatch, "；")))
		} else {
			emitStrategyLog(inst, "info", fmt.Sprintf("本批信号因时间窗不匹配被过滤 batch=%d latest_symbols=%d", len(batch), len(latestBySymbol)))
		}
		return
	}

	pxCache := map[string]float64{}
	noPriceSymbols := make([]string, 0, len(filtered))
	type cand struct {
		s                bus.SignalMessage
		rr               float64
		score            float64
		ok               bool
		px               float64
		side             string
		amount           float64
		recentCount      int
		consecutiveCount int
		lastEntryAt      time.Time
	}
	recentStats := loadRecentEntryStats(inst, 30)
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
			if len(noPriceSymbols) < 8 {
				noPriceSymbols = append(noPriceSymbols, sig.Symbol)
			}
			continue
		}
		rr := 0.0
		side := normalizeStrategySide(sig.Side)
		if side == "buy" || side == "" {
			if sig.TakeProfit > px && sig.StopLoss < px && sig.StopLoss > 0 {
				rr = (sig.TakeProfit - px) / (px - sig.StopLoss)
			}
		} else {
			if sig.TakeProfit < px && sig.StopLoss > px {
				rr = (px - sig.TakeProfit) / (sig.StopLoss - px)
			}
		}
		ok := rr > 0
		amount := clampOrderAmount(inst, sig.Amount)
		if side == "" {
			side = "buy"
		}
		stats := recentStats[exchange.NormalizeSymbol(sig.Symbol)]
		penalty := 1.0 + float64(stats.RecentCount)*0.35 + float64(stats.ConsecutiveCount)*0.5
		score := rr
		if penalty > 1 {
			score = rr / penalty
		}
		cands = append(cands, cand{
			s:                sig,
			rr:               rr,
			score:            score,
			ok:               ok,
			px:               px,
			side:             side,
			amount:           amount,
			recentCount:      stats.RecentCount,
			consecutiveCount: stats.ConsecutiveCount,
			lastEntryAt:      stats.LastEntryAt,
		})
	}
	if len(cands) == 0 {
		if len(noPriceSymbols) > 0 {
			emitStrategyLog(inst, "info", fmt.Sprintf("本批信号未生成候选：缺少最新价格 symbols=%s", strings.Join(noPriceSymbols, ",")))
		} else {
			emitStrategyLog(inst, "info", fmt.Sprintf("本批信号未生成候选：filtered=%d latest_symbols=%d", len(filtered), len(latestBySymbol)))
		}
		return
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		if cands[i].rr != cands[j].rr {
			return cands[i].rr > cands[j].rr
		}
		if cands[i].consecutiveCount != cands[j].consecutiveCount {
			return cands[i].consecutiveCount < cands[j].consecutiveCount
		}
		if cands[i].recentCount != cands[j].recentCount {
			return cands[i].recentCount < cands[j].recentCount
		}
		if cands[i].lastEntryAt.Equal(cands[j].lastEntryAt) {
			return cands[i].s.Symbol < cands[j].s.Symbol
		}
		if cands[i].lastEntryAt.IsZero() {
			return true
		}
		if cands[j].lastEntryAt.IsZero() {
			return false
		}
		return cands[i].lastEntryAt.Before(cands[j].lastEntryAt)
	})
	preview := make([]string, 0, len(cands))
	for i := 0; i < len(cands) && i < 8; i++ {
		preview = append(preview, fmt.Sprintf("%s(适配度=%0.4f,优先级=%0.4f,近期=%d,连开=%d)", cands[i].s.Symbol, cands[i].rr, cands[i].score, cands[i].recentCount, cands[i].consecutiveCount))
	}
	emitStrategyLog(inst, "info", fmt.Sprintf("同批信号排序完成，共%d个候选：%s", len(cands), strings.Join(preview, "，")))

	maxPos := 1
	if v := int(getNumber(inst.Config["max_concurrent_positions"])); v > 0 {
		maxPos = v
	}
	openCount := 0
	openSymbols := map[string]struct{}{}
	anchorSide := ""
	var openRows []models.StrategyPosition
	_ = database.DB.Where("owner_id = ? AND strategy_id = ? AND status = ?", inst.OwnerID, inst.ID, "open").Order("open_time asc, id asc").Find(&openRows).Error
	for _, p := range openRows {
		key := exchange.NormalizeSymbol(p.Symbol)
		if key == "" {
			continue
		}
		if anchorSide == "" {
			anchorSide = normalizeStrategySide(p.Direction)
		}
		openSymbols[key] = struct{}{}
		openCount++
	}
	var pendingRows []models.StrategyOrder
	pendingCutoff := time.Now().Add(-2 * time.Minute)
	_ = database.DB.Where("owner_id = ? AND strategy_id = ? AND requested_at >= ? AND status IN ?", inst.OwnerID, inst.ID, pendingCutoff, []string{"requested", "new", "partially_filled"}).
		Order("requested_at desc").
		Find(&pendingRows).Error
	for _, ord := range pendingRows {
		key := exchange.NormalizeSymbol(ord.Symbol)
		if key == "" {
			continue
		}
		if anchorSide == "" {
			anchorSide = normalizeStrategySide(ord.Side)
		}
		if _, ok := openSymbols[key]; ok {
			continue
		}
		openSymbols[key] = struct{}{}
		openCount++
	}
	if inst.exchange != nil {
		if ps, err := inst.exchange.FetchPositions(inst.OwnerID, "active"); err == nil {
			for _, p := range ps {
				if math.Abs(p.Amount) <= 0 {
					continue
				}
				key := exchange.NormalizeSymbol(p.Symbol)
				if _, ok := openSymbols[key]; ok {
					continue
				}
				if !isAllowedSymbol(inst, p.Symbol) {
					continue
				}
				openSymbols[key] = struct{}{}
				openCount++
			}
		}
	}
	availableSlots := maxPos - openCount
	if availableSlots <= 0 {
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过本批信号：当前已持仓%d个，达到最大并发仓位%d", openCount, maxPos))
		return
	}

	selected := 0
	filledCount := 0
	pendingCount := 0
	selectedSymbols := map[string]struct{}{}
	if allowedNow, windows, configured := resolveEntryTimeWindows(inst, time.Now()); !allowedNow {
		windowText := strings.TrimSpace(getString(inst.Config["entry_time_windows"]))
		if windows != "" {
			windowText = windows
		}
		if configured {
			emitStrategyLog(inst, "info", fmt.Sprintf("本批信号空跑：当前时间不在有效开仓范围内 windows=%s now=%s", windowText, time.Now().Format("15:04")))
			return
		}
	}
	for i := 0; i < len(cands); i++ {
		if selected >= availableSlots {
			break
		}
		c := cands[i]
		sig := c.s
		symKey := exchange.NormalizeSymbol(sig.Symbol)
		if _, ok := openSymbols[symKey]; ok {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过候选：%s 已有持仓", sig.Symbol))
			continue
		}
		if _, ok := selectedSymbols[symKey]; ok {
			continue
		}
		if ok, remain, _ := canOpenSymbolByCooldown(inst, recentStats[symKey]); !ok {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过候选：%s 仍在重复开仓冷却期 remaining=%s", sig.Symbol, remain.Truncate(time.Second)))
			continue
		}
		if ok, count, limit := canOpenSymbolByConsecutiveLimit(inst, sig.Symbol); !ok {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过候选：%s 已连续开仓%d次，达到限制%d次", sig.Symbol, count, limit))
			continue
		}
		if c.amount <= 0 || (c.side != "buy" && c.side != "sell") || !c.ok {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过候选：%s 条件无效 side=%s amount=%0.6f px=%0.8f tp=%0.8f sl=%0.8f rr=%0.4f", sig.Symbol, c.side, c.amount, c.px, sig.TakeProfit, sig.StopLoss, c.rr))
			continue
		}
		slotIndex := openCount + selected + 1
		side := c.side
		naturalSide := side
		takeProfit := sig.TakeProfit
		stopLoss := sig.StopLoss
		if slotIndex == 1 && anchorSide == "" {
			anchorSide = naturalSide
		}
		if forcedSide, ok := resolveNonNaturalEntrySide(inst, slotIndex, anchorSide); ok {
			if forcedSide != side {
				takeProfit, stopLoss = remapTPSLBySide(inst, forcedSide, c.px, sig.TakeProfit, sig.StopLoss)
				emitStrategyLog(inst, "info", fmt.Sprintf("非自然开仓顺延：slot=%d symbol=%s anchor=%s direction=%s->%s", slotIndex, sig.Symbol, anchorSide, side, forcedSide))
			}
			side = forcedSide
		}
		if side == "buy" {
			if !(takeProfit > c.px && stopLoss > 0 && stopLoss < c.px) {
				emitStrategyLog(inst, "info", fmt.Sprintf("跳过候选：%s 非自然开仓后止盈止损无效 side=%s tp=%v sl=%v px=%v", sig.Symbol, side, takeProfit, stopLoss, c.px))
				continue
			}
		} else if !(takeProfit > 0 && takeProfit < c.px && stopLoss > c.px) {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过候选：%s 非自然开仓后止盈止损无效 side=%s tp=%v sl=%v px=%v", sig.Symbol, side, takeProfit, stopLoss, c.px))
			continue
		}
		res := m.tryPlaceCandidate(inst, sig.Symbol, side, c.amount, takeProfit, stopLoss, strings.TrimSpace(sig.SignalID))
		if res == candidatePlaceFilled || res == candidatePlacePending {
			selected++
			selectedSymbols[symKey] = struct{}{}
			openSymbols[symKey] = struct{}{}
			if res == candidatePlaceFilled {
				filledCount++
				emitStrategyLog(inst, "info", fmt.Sprintf("候选开仓成功：%s 方向=%s 适配度=%0.4f，当前已占用 %d/%d 个仓位", sig.Symbol, side, c.rr, openCount+selected, maxPos))
			} else {
				pendingCount++
				emitStrategyLog(inst, "info", fmt.Sprintf("候选开仓请求已提交：%s 方向=%s 适配度=%0.4f，已预留 %d/%d 个仓位，等待成交确认", sig.Symbol, side, c.rr, openCount+selected, maxPos))
			}
			continue
		}
		emitStrategyLog(inst, "info", fmt.Sprintf("候选开仓失败：%s 方向=%s 适配度=%0.4f", sig.Symbol, side, c.rr))
	}
	if selected == 0 {
		emitStrategyLog(inst, "info", "本批信号未找到可开仓标的")
		return
	}
	if selected < len(cands) {
		emitStrategyLog(inst, "info", fmt.Sprintf("本批信号处理完成：已成交%d个，请求已提交%d个，剩余候选因仓位或条件限制被跳过", filledCount, pendingCount))
	} else {
		emitStrategyLog(inst, "info", fmt.Sprintf("本批信号处理完成：已成交%d个，请求已提交%d个", filledCount, pendingCount))
	}
}

type candidatePlaceResult string

const (
	candidatePlaceFailed  candidatePlaceResult = "failed"
	candidatePlacePending candidatePlaceResult = "pending"
	candidatePlaceFilled  candidatePlaceResult = "filled"
)

func (m *Manager) tryPlaceCandidate(inst *StrategyInstance, symbol string, side string, amount float64, tp float64, sl float64, signalID string) candidatePlaceResult {
	if inst == nil {
		return candidatePlaceFailed
	}
	requestedAfter := time.Now().Add(-200 * time.Millisecond)
	m.enqueueOrderForInstance(inst, symbol, side, amount, 0, tp, sl, signalID)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var ord models.StrategyOrder
		err := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND requested_at >= ?", inst.OwnerID, inst.ID, symbol, requestedAfter).
			Order("requested_at desc").
			First(&ord).Error
		if err == nil && ord.ID > 0 {
			st := strings.ToLower(strings.TrimSpace(ord.Status))
			if st == "filled" {
				return candidatePlaceFilled
			}
			if st == "new" || st == "requested" || st == "partially_filled" {
				return candidatePlacePending
			}
			if st == "failed" || st == "rejected" || st == "canceled" || st == "expired" {
				return candidatePlaceFailed
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return candidatePlaceFailed
}

func (m *Manager) handleRedisSignal(inst *StrategyInstance, s bus.SignalMessage) {
	if inst == nil {
		return
	}
	logSignal := getBool(inst.Config["log_signal"]) || getBool(inst.Config["log_redis"]) || getBool(inst.Config["debug"])
	symbol := strings.TrimSpace(s.Symbol)
	if symbol == "" {
		if logSignal {
			emitStrategyLog(inst, "info", "跳过信号：symbol 为空")
		}
		return
	}
	if !isAllowedSymbol(inst, symbol) {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过信号：交易对不在允许列表 symbol=%s", symbol))
		}
		return
	}
	action := strings.ToLower(strings.TrimSpace(s.Action))
	if action == "" {
		action = "open"
	}
	if action != "open" {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过信号：暂不支持的动作 action=%s", action))
		}
		return
	}
	side := strings.ToLower(strings.TrimSpace(s.Side))
	if side == "auto" || side == "both" {
		side = ""
	} else {
		side = normalizeStrategySide(side)
	}
	if side == "" {
		side = normalizeStrategySide(getString(inst.Config["default_open_side"]))
		if side == "" {
			side = "buy"
		}
	}
	if side != "buy" && side != "sell" {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过信号：方向无效 side=%s", side))
		}
		return
	}
	amount := clampOrderAmount(inst, s.Amount)
	if amount <= 0 {
		if logSignal {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过信号：下单数量无效 amount=%v", s.Amount))
		}
		return
	}
	if !(s.TakeProfit > 0 && s.StopLoss > 0) {
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过信号：缺少止盈止损，拒绝开仓 symbol=%s side=%s amount=%v tp=%v sl=%v", symbol, side, amount, s.TakeProfit, s.StopLoss))
		return
	}
	if logSignal {
		emitStrategyLog(inst, "info", fmt.Sprintf("收到开仓信号：symbol=%s side=%s amount=%v tp=%v sl=%v signal_id=%s", symbol, side, amount, s.TakeProfit, s.StopLoss, strings.TrimSpace(s.SignalID)))
	}
	m.enqueueSignalForSelection(inst, s)
}
