package strategy

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
)

var tpslGuardOnce sync.Once

const (
	// 15s*20 = 每 5 分钟做一次全账户孤儿保护单清扫（openOrders 全量接口 weight≈40）
	tpslOrphanSweepEvery = 20
	// 只清挂了这么久仍无对应持仓的保护单，避免误杀刚入场、行还没落库的窗口
	tpslOrphanMinAge = 10 * time.Minute
	// 同符号两次补挂的最小间隔。刚挂上又"看不见"多半是上游列表/对账在抖动，
	// 追着挂只会制造委托 churn（实证 2026-07-21：每 15s 重挂一对直至 -4045）
	tpslGuardMinReplaceGap = 60 * time.Second
)

func (m *Manager) StartTPSLGuardMonitor(ctx context.Context) {
	tpslGuardOnce.Do(func() {
		go m.runTPSLGuardMonitor(ctx)
	})
}

func (m *Manager) runTPSLGuardMonitor(ctx context.Context) {
	lastPlace := map[string]time.Time{}
	m.tpslGuardTick(lastPlace)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tpslGuardTick(lastPlace)
			tick++
			if tick%tpslOrphanSweepEvery == 0 {
				m.tpslOrphanSweep()
			}
		}
	}
}

func (m *Manager) tpslGuardTick(lastPlace map[string]time.Time) {
	if m == nil || database.DB == nil {
		return
	}
	bx, ok := m.exchange.(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return
	}

	var openRows []models.StrategyPosition
	if err := database.DB.Where("status = ?", "open").Find(&openRows).Error; err != nil || len(openRows) == 0 {
		return
	}

	byOwner := map[uint][]models.StrategyPosition{}
	for _, r := range openRows {
		byOwner[r.OwnerID] = append(byOwner[r.OwnerID], r)
	}

	for uid, rows := range byOwner {
		posList, err := bx.FetchPositions(uid, "active")
		if err != nil {
			continue
		}
		activeBySymbol := map[string]exchange.Position{}
		for _, p := range posList {
			if p.Amount > 0 {
				activeBySymbol[strings.ToUpper(p.Symbol)] = p
			}
		}
		for _, row := range rows {
			active, ok := activeBySymbol[strings.ToUpper(row.Symbol)]
			if !ok {
				continue
			}
			m.mu.RLock()
			inst := m.instances[row.StrategyID]
			m.mu.RUnlock()
			if inst == nil {
				continue
			}

			// Serialize with the entry-side placement path so we don't race
			// in between Place and storeLinkedTPSLOrders. Held across the
			// list -> decide -> cancel -> re-place window because all of
			// those decisions hinge on the current set of algo orders.
			tpslUnlock := m.lockTPSL(uid, row.Symbol)
			func() {
				defer tpslUnlock()
				algoOrders, err := bx.ListUSDMTPSLOpenOrders(uid, row.Symbol)
				if err != nil {
					emitStrategyLog(inst, "error", fmt.Sprintf("查询交易所止盈止损失败 symbol=%s err=%v", row.Symbol, err))
					return
				}
				tpCount := 0
				slCount := 0
				for _, ord := range algoOrders {
					typ := strings.ToUpper(strings.TrimSpace(ord.Type))
					if strings.Contains(typ, "TAKE_PROFIT") {
						tpCount++
					} else if strings.Contains(typ, "STOP") {
						slCount++
					}
				}

				tp := row.TakeProfit
				sl := row.StopLoss
				side := "buy"
				if strings.EqualFold(active.Direction, "short") {
					side = "sell"
				}
				if tp <= 0 || sl <= 0 {
					tp, sl = resolveTPSLFromROI(inst, side, active.Price, tp, sl)
					_ = database.DB.Model(&models.StrategyPosition{}).
						Where("id = ?", row.ID).
						Updates(map[string]interface{}{"take_profit": tp, "stop_loss": sl, "updated_at": time.Now()}).Error
				}
				if tp <= 0 || sl <= 0 {
					emitStrategyLog(inst, "error", fmt.Sprintf("仓位缺少有效止盈止损配置 symbol=%s tp=%v sl=%v", row.Symbol, tp, sl))
					return
				}
				// 健康态 = 恰好一张 TP + 一张 SL。≥2 张同腿说明历史残留在堆积
				//（陈旧 closePosition 单会以旧价格误平新仓，还占 -4045 额度），
				// 与缺腿一样都要清空重挂唯一一对。
				if tpCount == 1 && slCount == 1 {
					return
				}

				placeKey := fmt.Sprintf("%d|%s", uid, strings.ToUpper(strings.TrimSpace(row.Symbol)))
				if last, ok := lastPlace[placeKey]; ok && time.Since(last) < tpslGuardMinReplaceGap {
					emitStrategyLog(inst, "warn", fmt.Sprintf("止盈止损守护限速 symbol=%s tp腿=%d sl腿=%d 距上次补挂不足%ds，本轮跳过（疑似上游持仓/列表抖动）", row.Symbol, tpCount, slCount, int(tpslGuardMinReplaceGap.Seconds())))
					return
				}

				if len(algoOrders) > 0 {
					if cErr := bx.CancelUSDMAlgoOpenOrders(uid, row.Symbol); cErr != nil {
						// 撤不掉就不能盲目再挂：旧单残留 + 新单 = 双倍风险与 -4045
						emitStrategyLog(inst, "error", fmt.Sprintf("清理旧止盈止损失败 symbol=%s tp腿=%d sl腿=%d err=%v，本轮不补挂", row.Symbol, tpCount, slCount, cErr))
						return
					}
				}
				baseClientOrderID := models.GenerateUUID()
				created, err := bx.PlaceUSDMTPStopOrders(uid, baseClientOrderID, row.Symbol, tp, sl)
				if err != nil {
					emitStrategyLog(inst, "error", fmt.Sprintf("补设交易所止盈止损失败 symbol=%s tp=%v sl=%v err=%v", row.Symbol, tp, sl, err))
					return
				}
				lastPlace[placeKey] = time.Now()
				m.storeLinkedTPSLOrders(inst, row.ID, row.Symbol, baseClientOrderID, created)
				refs := make([]string, 0, len(created))
				for _, ref := range created {
					refs = append(refs, fmt.Sprintf("%s order_id=%d client_order_id=%s trigger=%s price=%s", ref.Kind, ref.AlgoID, ref.ClientAlgoID, ref.TriggerPrice, ref.ExecutionPrice))
				}
				emitStrategyLog(inst, "info", fmt.Sprintf("已补设交易所止盈止损 symbol=%s tp=%v sl=%v 补挂前tp腿=%d sl腿=%d %s", row.Symbol, tp, sl, tpCount, slCount, strings.Join(refs, " | ")))
			}()
		}
	}
}

// tpslOrphanSweep 清理"无持仓却仍挂着"的 TP/SL 保护单。持仓被交易所侧平掉后
// closePosition 单不会自动消失；撤单失败、进程崩溃窗口、-4120 回退的按数量
// algo 单都会留下孤儿。孤儿累积吃掉每符号 10 张 stop 单额度（-4045），
// 也会在同符号再次开仓时以陈旧价格误触发。
func (m *Manager) tpslOrphanSweep() {
	if m == nil || database.DB == nil {
		return
	}
	bx, ok := m.exchange.(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return
	}
	m.mu.RLock()
	owners := map[uint]struct{}{}
	for _, inst := range m.instances {
		if inst != nil && inst.OwnerID > 0 {
			owners[inst.OwnerID] = struct{}{}
		}
	}
	m.mu.RUnlock()
	tpslPurposes := []string{"take_profit", "stop_loss", "tpsl"}
	for uid := range owners {
		// 常规类可全账户列出；algo 类接口 symbol 必填（无全账户列表），
		// 用本库登记的未终态 TP/SL 单符号补齐候选。
		refs, err := bx.ListUSDMTPSLOpenOrdersAllSymbols(uid)
		if err != nil {
			continue
		}
		posList, err := bx.FetchPositions(uid, "active")
		if err != nil {
			continue // 持仓视图不可信时绝不动手
		}
		keep := map[string]struct{}{}
		for _, p := range posList {
			if p.Amount > 0 {
				keep[exchange.NormalizeSymbol(p.Symbol)] = struct{}{}
			}
		}
		var openRows []models.StrategyPosition
		if err := database.DB.Where("owner_id = ? AND status = ?", uid, "open").Find(&openRows).Error; err != nil {
			continue
		}
		for _, r := range openRows {
			keep[exchange.NormalizeSymbol(r.Symbol)] = struct{}{}
		}

		nowMs := time.Now().UnixMilli()
		candidates := map[string]string{} // normalized → 原始符号（供撤单/加锁）
		young := map[string]bool{}        // 该符号存在挂龄不足的单 → 本轮不动
		for _, ref := range refs {
			n := exchange.NormalizeSymbol(ref.Symbol)
			if _, ok := candidates[n]; !ok {
				candidates[n] = ref.Symbol
			}
			if ref.CreatedAtMs > 0 && nowMs-ref.CreatedAtMs < tpslOrphanMinAge.Milliseconds() {
				young[n] = true
			}
		}
		var orderRows []models.StrategyOrder
		_ = database.DB.Where("owner_id = ? AND purpose IN ? AND status = ?", uid, tpslPurposes, "open").
			Find(&orderRows).Error
		for _, r := range orderRows {
			n := exchange.NormalizeSymbol(r.Symbol)
			if _, ok := candidates[n]; !ok {
				candidates[n] = r.Symbol
			}
			if time.Since(r.RequestedAt) < tpslOrphanMinAge {
				young[n] = true
			}
		}

		for n, sym := range candidates {
			if _, ok := keep[n]; ok {
				continue
			}
			if young[n] {
				continue
			}
			unlock := m.lockTPSL(uid, sym)
			orders, lErr := bx.ListUSDMTPSLOpenOrders(uid, sym)
			if lErr == nil && len(orders) == 0 {
				// 交易所已无该符号的保护单：把本库还挂着 open 的登记行结转，
				// 避免它每 5 分钟都进候选。
				_ = database.DB.Model(&models.StrategyOrder{}).
					Where("owner_id = ? AND symbol = ? AND purpose IN ? AND status = ?", uid, sym, tpslPurposes, "open").
					Update("status", "superseded").Error
			}
			if lErr != nil || len(orders) == 0 {
				unlock()
				continue
			}
			cErr := bx.CancelUSDMAlgoOpenOrders(uid, sym)
			unlock()
			logger.Warnf("[TPSL SWEEP] 清理孤儿止盈止损 owner=%d symbol=%s orders=%d err=%v", uid, sym, len(orders), cErr)
		}
	}
}
