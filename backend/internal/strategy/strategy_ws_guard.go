package strategy

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/exchange"
)

// WS 加速仓位守护。
//
// 决策路径不变：scanROILimits（5s 轮询）仍是保本/追踪止损/守护 TP/SL/ROI
// 限价的唯一裁决者。本文件只解决它的延迟：标记价流推来的价格若显示某个
// 在持 symbol 已经动了（≥wsGuardMoveGatePct），立即触发一次完整守护扫描，
// 把最坏 5s 的反应窗压到 ~1s。移动门 + 单飞 + 最小间隔三重限流，REST 负载
// 有界（每次触发 = 一次常规 tick 的成本）。
// 关闭开关：环境变量 WS_GUARD_DISABLED=1（引擎级；不走策略 config，因为
// 全市场流是进程级单例，不属于任何单个策略）。

const (
	wsGuardMoveGatePct = 0.0015                 // 0.15%：触发扫描的最小价格位移
	wsGuardMinInterval = 700 * time.Millisecond // 两次 WS 触发扫描的最小间隔
)

type wsGuardState struct {
	mu        sync.Mutex
	lastPrice map[string]float64  // symbol(大写无斜杠) -> 上次触发扫描时的标记价
	watched   map[string]struct{} // 当前有在持仓位的 symbol 集（scanROILimits 每轮重建）
	lastFire  time.Time
	inFlight  bool
}

// StartWSPositionGuard 启动标记价流并接到守护触发器上。usdm 之外的市场无效。
func (m *Manager) StartWSPositionGuard(ctx context.Context) {
	if os.Getenv("WS_GUARD_DISABLED") == "1" {
		return
	}
	bx, ok := m.exchange.(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return
	}
	bx.StartMarkPriceStream(ctx, m.onMarkTicks)
}

// setWSWatched 由 scanROILimits 每轮调用，重建"值得盯"的 symbol 集。
func (m *Manager) setWSWatched(symbols map[string]struct{}) {
	if m == nil {
		return
	}
	m.wsGuard.mu.Lock()
	defer m.wsGuard.mu.Unlock()
	if m.wsGuard.lastPrice == nil {
		m.wsGuard.lastPrice = map[string]float64{}
	}
	m.wsGuard.watched = symbols
	// 清掉已不在持仓集里的价格锚，防 map 无限膨胀。
	for sym := range m.wsGuard.lastPrice {
		if _, ok := symbols[sym]; !ok {
			delete(m.wsGuard.lastPrice, sym)
		}
	}
}

func (m *Manager) onMarkTicks(ticks []exchange.MarkTick) {
	m.wsGuard.mu.Lock()
	if len(m.wsGuard.watched) == 0 {
		m.wsGuard.mu.Unlock()
		return
	}
	fire := false
	for _, t := range ticks {
		sym := strings.ToUpper(t.Symbol)
		if _, ok := m.wsGuard.watched[sym]; !ok {
			continue
		}
		last := m.wsGuard.lastPrice[sym]
		if last <= 0 {
			m.wsGuard.lastPrice[sym] = t.Price
			continue
		}
		diff := t.Price - last
		if diff < 0 {
			diff = -diff
		}
		if diff/last >= wsGuardMoveGatePct {
			m.wsGuard.lastPrice[sym] = t.Price
			fire = true
		}
	}
	if !fire || m.wsGuard.inFlight || time.Since(m.wsGuard.lastFire) < wsGuardMinInterval {
		m.wsGuard.mu.Unlock()
		return
	}
	m.wsGuard.inFlight = true
	m.wsGuard.lastFire = time.Now()
	m.wsGuard.mu.Unlock()

	go func() {
		defer func() {
			m.wsGuard.mu.Lock()
			m.wsGuard.inFlight = false
			m.wsGuard.mu.Unlock()
		}()
		m.roiGuardTick()
	}()
}
