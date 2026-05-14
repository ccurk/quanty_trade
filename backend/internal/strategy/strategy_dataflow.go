package strategy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"quanty_trade/internal/bus"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"quanty_trade/internal/ws"
)

func (m *Manager) attachRedisIO(inst *StrategyInstance, redisBus *bus.RedisBus, logTrace bool) {
	if inst == nil || redisBus == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	inst.mu.Lock()
	if inst.redisCancel != nil {
		inst.redisCancel()
	}
	inst.redisCancel = cancel
	inst.mu.Unlock()

	_ = redisBus.SubscribeSignals(ctx, inst.ID, func(s bus.SignalMessage) {
		if strings.TrimSpace(s.StrategyID) == "" {
			s.StrategyID = inst.ID
		}
		if s.StrategyID != inst.ID {
			return
		}
		m.handleRedisSignal(inst, s)
	})
	_ = redisBus.SubscribeState(ctx, inst.ID, func(st bus.StateMessage) {
		if st.StrategyID != inst.ID {
			return
		}
		if strings.TrimSpace(st.BootID) == "" {
			return
		}
		typ := strings.ToLower(strings.TrimSpace(st.Type))
		if logTrace {
			emitStrategyLog(inst, "info", fmt.Sprintf("State recv type=%s boot_id=%s", typ, st.BootID))
		}
		now := time.Now()
		inst.mu.Lock()
		changed := inst.bootID != st.BootID
		if changed || typ == "ready" {
			inst.bootID = st.BootID
			inst.resync = true
		}
		if typ == "ready" {
			inst.Status = StatusRunning
			if !inst.stateReadySeen {
				inst.stateReadySeen = true
				inst.mu.Unlock()
				m.setStrategyStatus(inst, StatusRunning)
				emitStrategyLog(inst, "info", fmt.Sprintf("Strategy ready boot_id=%s", st.BootID))
				inst.mu.Lock()
			}
		}
		if typ == "heartbeat" {
			if !inst.heartbeatSeen {
				inst.heartbeatSeen = true
				inst.mu.Unlock()
				emitStrategyLog(inst, "info", fmt.Sprintf("Strategy heartbeat boot_id=%s", st.BootID))
				inst.mu.Lock()
			}
		}
		if typ == "ready" || typ == "heartbeat" {
			inst.lastHB = now
		}
		inst.mu.Unlock()
	})

	go func() {
		time.Sleep(10 * time.Second)
		inst.mu.Lock()
		seen := inst.stateReadySeen
		inst.mu.Unlock()
		if !seen {
			emitStrategyLog(inst, "info", "Waiting strategy ready (python state channel not received yet)")
		}
	}()

	go m.historySyncLoop(ctx, inst, redisBus)
	go m.healthMonitorLoop(ctx, inst)
	go m.waitProcessLoop(inst)
}

func (m *Manager) attachUserDataStream(inst *StrategyInstance) {
	if inst == nil {
		return
	}
	if ex, ok := inst.exchange.(interface {
		EnsureUserDataStream(ownerID uint, hub *ws.Hub) error
	}); ok {
		_ = ex.EnsureUserDataStream(inst.OwnerID, inst.hub)
	}
}

func (m *Manager) attachMarketData(inst *StrategyInstance, redisBus *bus.RedisBus, symbols []string, runCfg map[string]interface{}) error {
	if inst == nil {
		return nil
	}
	inst.mu.Lock()
	if len(inst.candleStops) > 0 {
		for _, stop := range inst.candleStops {
			if stop != nil {
				stop()
			}
		}
	}
	inst.candleStops = nil
	inst.candlePubCount = map[string]int{}
	inst.candleRxCount = map[string]int{}
	inst.lastCandleClose = map[string]float64{}
	inst.lastCandleAt = map[string]time.Time{}
	inst.lastCandleSeenAt = map[string]time.Time{}
	inst.candleEvent = map[string]string{}
	inst.candleEventInfo = map[string]string{}
	inst.candleEventAt = map[string]time.Time{}
	inst.mu.Unlock()
	if len(symbols) == 0 {
		logger.Warnf("[STRATEGY START WARN] id=%s owner=%d reason=no symbol in config", inst.ID, inst.OwnerID)
		database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      "error",
			Message:    "No symbol in config; strategy will not receive market data",
			CreatedAt:  time.Now(),
		})
		inst.hub.BroadcastJSON(map[string]interface{}{
			"type":        "error",
			"strategy_id": inst.ID,
			"owner_id":    inst.OwnerID,
			"error":       "No symbol in config; strategy will not receive market data",
		})
		return nil
	}

	runCfg["symbols"] = symbols
	if _, ok := runCfg["symbol"].(string); !ok || strings.TrimSpace(fmt.Sprintf("%v", runCfg["symbol"])) == "" {
		runCfg["symbol"] = symbols[0]
	}

	emitStrategyLog(inst, "info", fmt.Sprintf("Dataflow candles: exchange->redis ch=%s", redisBus.CandleChannel(inst.ID)))

	go func(syms []string) {
		time.Sleep(20 * time.Second)
		for _, s := range syms {
			n, reason := candleWaitReason(inst, s)
			if n == 0 {
				emitStrategyLog(inst, "info", fmt.Sprintf("Waiting first closed kline symbol=%s reason=%s", s, reason))
			}
		}
	}(append([]string(nil), symbols...))

	for _, sym := range symbols {
		sym := sym
		go func() {
			emitStrategyLog(inst, "info", fmt.Sprintf("SubscribeCandles start symbol=%s", sym))
			var (
				stop func()
				err  error
			)
			pollCtx, pollCancel := context.WithCancel(context.Background())
			go m.latestClosedCandleFallbackLoop(pollCtx, inst, redisBus, sym)
			if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok {
				stop, err = bx.SubscribeCandlesWithEvents(sym, func(candle exchange.Candle) {
					m.onExchangeCandle(inst, redisBus, sym, candle)
				}, func(event string, detail string, err error) {
					m.onCandleStreamEvent(inst, sym, event, detail, err)
				})
			} else {
				stop, err = inst.exchange.SubscribeCandles(sym, func(candle exchange.Candle) {
					m.onExchangeCandle(inst, redisBus, sym, candle)
				})
			}
			if err != nil {
				pollCancel()
				logger.Errorf("[STRATEGY SUBSCRIBE ERROR] id=%s owner=%d symbol=%s err=%v", inst.ID, inst.OwnerID, sym, err)
				database.DB.Create(&models.StrategyLog{
					StrategyID: inst.ID,
					Level:      "error",
					Message:    fmt.Sprintf("SubscribeCandles error: %v", err),
					CreatedAt:  time.Now(),
				})
				inst.hub.BroadcastJSON(map[string]interface{}{
					"type":        "error",
					"strategy_id": inst.ID,
					"owner_id":    inst.OwnerID,
					"error":       fmt.Sprintf("SubscribeCandles error: %v", err),
				})
				return
			}
			emitStrategyLog(inst, "info", fmt.Sprintf("SubscribeCandles ok symbol=%s", sym))
			combinedStop := func() {
				pollCancel()
				if stop != nil {
					stop()
				}
			}
			inst.mu.Lock()
			if inst.candleStops == nil {
				inst.candleStops = map[string]func(){}
			}
			if prev, ok := inst.candleStops[sym]; ok && prev != nil {
				prev()
			}
			inst.candleStops[sym] = combinedStop
			inst.mu.Unlock()
		}()
	}
	return nil
}

func candleWaitReason(inst *StrategyInstance, sym string) (int, string) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	rx := 0
	if inst.candleRxCount != nil {
		rx = inst.candleRxCount[sym]
	}
	pub := 0
	if inst.candlePubCount != nil {
		pub = inst.candlePubCount[sym]
	}
	event := ""
	if inst.candleEvent != nil {
		event = inst.candleEvent[sym]
	}
	detail := ""
	if inst.candleEventInfo != nil {
		detail = inst.candleEventInfo[sym]
	}
	eventAt := time.Time{}
	if inst.candleEventAt != nil {
		eventAt = inst.candleEventAt[sym]
	}
	lastClosedAt := time.Time{}
	if inst.lastCandleAt != nil {
		lastClosedAt = inst.lastCandleAt[sym]
	}
	reason := "no_ws_event_yet"
	switch event {
	case "dialing":
		reason = "dialing_ws"
	case "connected":
		reason = "ws_connected_waiting_payload"
	case "rx_raw_first":
		reason = "received_raw_ws_payload_waiting_parse"
	case "rx_first":
		reason = "received_kline_but_not_closed_yet"
	case "rx_first_closed":
		reason = "closed_kline_received_waiting_publish"
	case "connect_failed":
		reason = "ws_connect_failed"
	case "disconnected":
		reason = "ws_disconnected_reconnecting"
	case "unmarshal_failed":
		reason = "ws_payload_unmarshal_failed"
	}
	if strings.TrimSpace(detail) != "" {
		reason += " detail=" + detail
	}
	if !eventAt.IsZero() {
		reason += " event_at=" + eventAt.Format(time.RFC3339)
	}
	if !lastClosedAt.IsZero() {
		reason += " last_closed_at=" + lastClosedAt.Format(time.RFC3339)
	}
	reason += fmt.Sprintf(" rx=%d pub=%d", rx, pub)
	return rx, reason
}

func shouldPollLatestClosedCandle(inst *StrategyInstance, sym string, now time.Time) (bool, string) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	lastClosedAt := time.Time{}
	if inst.lastCandleAt != nil {
		lastClosedAt = inst.lastCandleAt[sym]
	}
	event := ""
	if inst.candleEvent != nil {
		event = inst.candleEvent[sym]
	}
	if !lastClosedAt.IsZero() {
		nextEligibleAt := lastClosedAt.Add(2*time.Minute + 3*time.Second)
		if now.Before(nextEligibleAt) {
			return false, ""
		}
		if event == "" {
			return true, "latest_closed_kline_stale"
		}
		return true, fmt.Sprintf("latest_closed_kline_stale event=%s", event)
	}
	if !inst.startedAt.IsZero() && now.Before(inst.startedAt.Add(25*time.Second)) {
		return false, ""
	}
	if event == "" {
		return true, "no_live_ws_event_yet"
	}
	return true, fmt.Sprintf("no_live_closed_kline_yet event=%s", event)
}

func latestClosedOneMinuteCandle(ex exchange.Exchange, sym string, now time.Time) (exchange.Candle, bool, error) {
	bars, err := ex.FetchCandles(sym, "1m", 3)
	if err != nil {
		return exchange.Candle{}, false, err
	}
	if len(bars) == 0 {
		return exchange.Candle{}, false, nil
	}
	cutoff := now.Truncate(time.Minute)
	var best exchange.Candle
	found := false
	for _, bar := range bars {
		if !bar.Timestamp.Before(cutoff) {
			continue
		}
		if !found || bar.Timestamp.After(best.Timestamp) {
			best = bar
			found = true
		}
	}
	return best, found, nil
}

func (m *Manager) latestClosedCandleFallbackLoop(ctx context.Context, inst *StrategyInstance, redisBus *bus.RedisBus, sym string) {
	if inst == nil || redisBus == nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		shouldPoll, reason := shouldPollLatestClosedCandle(inst, sym, now)
		if !shouldPoll {
			continue
		}
		candle, ok, err := latestClosedOneMinuteCandle(inst.exchange, sym, now)
		if err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("Fallback latest candle fetch failed symbol=%s reason=%s err=%v", sym, reason, err))
			continue
		}
		if !ok {
			continue
		}
		inst.mu.Lock()
		lastClosedAt := time.Time{}
		if inst.lastCandleAt != nil {
			lastClosedAt = inst.lastCandleAt[sym]
		}
		inst.mu.Unlock()
		if !lastClosedAt.IsZero() && !candle.Timestamp.After(lastClosedAt) {
			continue
		}
		emitStrategyLog(inst, "info", fmt.Sprintf("Fallback latest closed kline symbol=%s ts=%s close=%v reason=%s", sym, candle.Timestamp.Format(time.RFC3339), candle.Close, reason))
		m.onExchangeCandle(inst, redisBus, sym, candle)
	}
}

func (m *Manager) onExchangeCandle(inst *StrategyInstance, redisBus *bus.RedisBus, sym string, candle exchange.Candle) {
	inst.mu.Lock()
	if inst.candleRxCount == nil {
		inst.candleRxCount = map[string]int{}
	}
	if inst.lastCandleAt != nil {
		if last := inst.lastCandleAt[sym]; !last.IsZero() && !candle.Timestamp.After(last) {
			inst.mu.Unlock()
			return
		}
	}
	inst.candleRxCount[sym]++
	if inst.lastCandleClose == nil {
		inst.lastCandleClose = map[string]float64{}
	}
	if inst.lastCandleAt == nil {
		inst.lastCandleAt = map[string]time.Time{}
	}
	if inst.lastCandleSeenAt == nil {
		inst.lastCandleSeenAt = map[string]time.Time{}
	}
	inst.lastCandleClose[sym] = candle.Close
	inst.lastCandleAt[sym] = candle.Timestamp
	inst.lastCandleSeenAt[sym] = time.Now()
	rxN := inst.candleRxCount[sym]
	inst.mu.Unlock()
	if rxN == 1 {
		emitStrategyLog(inst, "info", fmt.Sprintf("Exchange candle first symbol=%s ts=%s close=%v", sym, candle.Timestamp.Format(time.RFC3339), candle.Close))
	}

	payload := map[string]interface{}{
		"symbol":    sym,
		"timestamp": candle.Timestamp,
		"open":      candle.Open,
		"high":      candle.High,
		"low":       candle.Low,
		"close":     candle.Close,
		"volume":    candle.Volume,
	}
	pubErr := redisBus.PublishCandle(context.Background(), bus.CandleMessage{
		StrategyID: inst.ID,
		Symbol:     sym,
		Timestamp:  candle.Timestamp,
		Open:       candle.Open,
		High:       candle.High,
		Low:        candle.Low,
		Close:      candle.Close,
		Volume:     candle.Volume,
	})
	if pubErr != nil {
		logger.Errorf("[REDIS PUBLISH ERROR] id=%s owner=%d symbol=%s err=%v", inst.ID, inst.OwnerID, sym, pubErr)
		emitStrategyLog(inst, "error", fmt.Sprintf("Redis publish candle failed symbol=%s err=%v", sym, pubErr))
	} else {
		inst.mu.Lock()
		if inst.candlePubCount == nil {
			inst.candlePubCount = map[string]int{}
		}
		inst.candlePubCount[sym]++
		pubN := inst.candlePubCount[sym]
		inst.mu.Unlock()
		if pubN == 1 {
			emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish candle first ch=%s symbol=%s ts=%s close=%v", redisBus.CandleChannel(inst.ID), sym, candle.Timestamp.Format(time.RFC3339), candle.Close))
		}
		logRedis := getBool(inst.Config["log_redis"])
		logEvery := int(getNumber(inst.Config["log_candle_every"]))
		if logEvery <= 0 {
			logEvery = 60
		}
		if logRedis {
			if pubN%logEvery == 0 {
				emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish candle ok ch=%s symbol=%s ts=%s close=%v", redisBus.CandleChannel(inst.ID), sym, candle.Timestamp.Format(time.RFC3339), candle.Close))
			}
		}
	}
	inst.hub.BroadcastJSON(map[string]interface{}{
		"type":        "candle",
		"strategy_id": inst.ID,
		"owner_id":    inst.OwnerID,
		"data":        payload,
	})
}

func (m *Manager) onCandleStreamEvent(inst *StrategyInstance, sym string, event string, detail string, err error) {
	inst.mu.Lock()
	if inst.candleEvent == nil {
		inst.candleEvent = map[string]string{}
	}
	if inst.candleEventInfo == nil {
		inst.candleEventInfo = map[string]string{}
	}
	if inst.candleEventAt == nil {
		inst.candleEventAt = map[string]time.Time{}
	}
	inst.candleEvent[sym] = event
	if err != nil {
		inst.candleEventInfo[sym] = fmt.Sprintf("%s err=%v", detail, err)
	} else {
		inst.candleEventInfo[sym] = detail
	}
	inst.candleEventAt[sym] = time.Now()
	inst.mu.Unlock()
	switch event {
	case "dialing":
		emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS dialing symbol=%s url=%s", sym, detail))
	case "connected":
		emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS connected symbol=%s url=%s", sym, detail))
	case "connect_failed":
		emitStrategyLog(inst, "error", fmt.Sprintf("Binance WS connect failed symbol=%s url=%s err=%v", sym, detail, err))
	case "disconnected":
		emitStrategyLog(inst, "error", fmt.Sprintf("Binance WS disconnected symbol=%s url=%s err=%v", sym, detail, err))
	case "rx_raw_first":
		emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS recv first raw symbol=%s %s", sym, detail))
	case "rx_first":
		emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS recv first kline symbol=%s %s", sym, detail))
	case "rx_first_closed":
		emitStrategyLog(inst, "info", fmt.Sprintf("Binance WS recv first closed kline symbol=%s %s", sym, detail))
	case "unmarshal_failed":
		emitStrategyLog(inst, "error", fmt.Sprintf("Binance WS unmarshal failed symbol=%s err=%s", sym, detail))
	default:
		emitStrategyLog(inst, "error", fmt.Sprintf("Binance WS unknown event symbol=%s event=%s detail=%s err=%v", sym, event, detail, err))
	}
}
