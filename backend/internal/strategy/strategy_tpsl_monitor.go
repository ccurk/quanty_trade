package strategy

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

var tpslGuardOnce sync.Once

func (m *Manager) StartTPSLGuardMonitor(ctx context.Context) {
	tpslGuardOnce.Do(func() {
		go m.runTPSLGuardMonitor(ctx)
	})
}

func (m *Manager) runTPSLGuardMonitor(ctx context.Context) {
	m.tpslGuardTick()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tpslGuardTick()
		}
	}
}

func (m *Manager) tpslGuardTick() {
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
		seenSym := map[string]struct{}{}
		for _, row := range rows {
			// 同一 symbol 的重复台账行（reconcile 收养/入场路径竞态产物）只
			// 处理一次：否则一轮内会按行数重复补挂 TP/SL（2026-07-19 实测
			// ROBO/USDT 同 tick 78ms 内挂出两对）。
			dedupKey := exchange.NormalizeSymbol(row.Symbol)
			if _, dup := seenSym[dedupKey]; dup {
				continue
			}
			seenSym[dedupKey] = struct{}{}
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
				hasTP := false
				hasSL := false
				for _, ord := range algoOrders {
					typ := strings.ToUpper(strings.TrimSpace(ord.Type))
					if strings.Contains(typ, "TAKE_PROFIT") {
						hasTP = true
					}
					if typ == "STOP" || strings.Contains(typ, "STOP") {
						hasSL = true
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
				// 恰好一对且两腿齐全才算健康。>2 张 = 历史堆积的多余对
				// （重复行竞态/盲区撤单年代的残留），必须落入下方
				// 全类型清扫+重挂，否则残单永久滞留并占用账户级
				// stop 单额度（-4045 的温床）。
				if hasTP && hasSL && len(algoOrders) <= 2 {
					return
				}

				// 挂单节流闸：90s 内本守护刚挂过却又被判"缺失/脏"，说明
				// 列表或撤单环节在说谎（查询吞错/跨列表重复计数/撤单失败），
				// 此时盲目重挂只会制造 15s 一对的风暴——跳过本轮等待复核。
				brakeKey := fmt.Sprintf("%d|%s", uid, exchange.NormalizeSymbol(row.Symbol))
				m.tpslPlaceMu.Lock()
				lastPlace, placedBefore := m.tpslLastPlace[brakeKey]
				m.tpslPlaceMu.Unlock()
				if placedBefore && time.Since(lastPlace) < 90*time.Second {
					emitStrategyLog(inst, "info", fmt.Sprintf("止盈止损90s内已挂过但再次被判缺失 symbol=%s tp在=%v sl在=%v 张数=%d，跳过重挂待下轮复核", row.Symbol, hasTP, hasSL, len(algoOrders)))
					return
				}

				// 越位腿预检：价格已穿过触发价的腿挂上去必被 -2021 拒单，
				// 反复硬挂=永动失败循环。只挂仍有效的腿；两腿全越位时
				// 不挂单，交由 ROI 守护按超限平仓处置。
				mark := active.CurrentPrice
				if mark <= 0 {
					mark = active.Price
				}
				tpPlace, slPlace := tp, sl
				if mark > 0 {
					if side == "buy" {
						if tpPlace <= mark {
							tpPlace = 0
						}
						if slPlace >= mark {
							slPlace = 0
						}
					} else {
						if tpPlace >= mark {
							tpPlace = 0
						}
						if slPlace <= mark {
							slPlace = 0
						}
					}
				}
				if tpPlace <= 0 && slPlace <= 0 {
					emitStrategyLog(inst, "error", fmt.Sprintf("止盈止损位均已被价格越过 symbol=%s mark=%v tp=%v sl=%v，不挂单等待ROI守护处置", row.Symbol, mark, tp, sl))
					return
				}
				if tpPlace != tp || slPlace != sl {
					emitStrategyLog(inst, "info", fmt.Sprintf("部分止盈止损位已被价格越过 symbol=%s mark=%v 原tp=%v 原sl=%v，仅补挂有效腿", row.Symbol, mark, tp, sl))
				}

				if len(algoOrders) > 0 {
					// 必须用全类型清扫：TP/SL 实际是普通条件单，algo-only 撤单
					// 清不掉它们，残单逐轮堆积直至 -4045（2026-07-19 实测）。
					_ = bx.CancelUSDMTPSLOpenOrders(uid, row.Symbol)
				}
				m.tpslPlaceMu.Lock()
				if m.tpslLastPlace == nil {
					m.tpslLastPlace = map[string]time.Time{}
				}
				m.tpslLastPlace[brakeKey] = time.Now()
				m.tpslPlaceMu.Unlock()
				baseClientOrderID := models.GenerateUUID()
				created, err := bx.PlaceUSDMTPStopOrders(uid, baseClientOrderID, row.Symbol, tpPlace, slPlace)
				if err != nil {
					emitStrategyLog(inst, "error", fmt.Sprintf("补设交易所止盈止损失败 symbol=%s tp=%v sl=%v err=%v", row.Symbol, tp, sl, err))
					return
				}
				m.storeLinkedTPSLOrders(inst, row.ID, row.Symbol, baseClientOrderID, created)
				refs := make([]string, 0, len(created))
				for _, ref := range created {
					refs = append(refs, fmt.Sprintf("%s order_id=%d client_order_id=%s trigger=%s price=%s", ref.Kind, ref.AlgoID, ref.ClientAlgoID, ref.TriggerPrice, ref.ExecutionPrice))
				}
				emitStrategyLog(inst, "info", fmt.Sprintf("已补设交易所止盈止损 symbol=%s tp=%v sl=%v %s", row.Symbol, tp, sl, strings.Join(refs, " | ")))
			}()
		}
	}
}
