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
			if !inst.stateReadySeen {
				inst.stateReadySeen = true
				inst.mu.Unlock()
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
		inst.mu.Lock()
		rx := inst.candleRxCount
		inst.mu.Unlock()
		for _, s := range syms {
			n := 0
			if rx != nil {
				n = rx[s]
			}
			if n == 0 {
				emitStrategyLog(inst, "info", fmt.Sprintf("Waiting first closed kline symbol=%s (Binance kline only triggers on close)", s))
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
			if stop != nil {
				inst.mu.Lock()
				if inst.candleStops == nil {
					inst.candleStops = map[string]func(){}
				}
				if prev, ok := inst.candleStops[sym]; ok && prev != nil {
					prev()
				}
				inst.candleStops[sym] = stop
				inst.mu.Unlock()
			}
		}()
	}
	return nil
}

func (m *Manager) onExchangeCandle(inst *StrategyInstance, redisBus *bus.RedisBus, sym string, candle exchange.Candle) {
	inst.mu.Lock()
	if inst.candleRxCount == nil {
		inst.candleRxCount = map[string]int{}
	}
	inst.candleRxCount[sym]++
	if inst.lastCandleClose == nil {
		inst.lastCandleClose = map[string]float64{}
	}
	if inst.lastCandleAt == nil {
		inst.lastCandleAt = map[string]time.Time{}
	}
	inst.lastCandleClose[sym] = candle.Close
	inst.lastCandleAt[sym] = candle.Timestamp
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
