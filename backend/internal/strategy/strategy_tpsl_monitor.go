package strategy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

var tpslGuardOnce sync.Once

// ownedTPSLOrderIDs 返回该仓位自己挂出的 TP/SL 单的 client_order_id 集合。
// 用于在多策略共用同一交易所账户时,把"别的策略挂在同一 symbol 上的保护单"排除掉 ——
// 否则会被当成重复单撤掉。返回空集表示无归属记录(老仓位/刚重启),调用方须放行全部,
// 宁可沿用旧的按 symbol 语义,也不能因为查不到记录就把仓位的保护单全部忽略。
func (m *Manager) ownedTPSLOrderIDs(positionID uint) map[string]bool {
	out := map[string]bool{}
	if database.DB == nil || positionID == 0 {
		return out
	}
	var ids []string
	err := database.DB.Model(&models.StrategyOrder{}).
		Where("position_id = ? AND purpose IN ? AND status NOT IN ?",
			positionID,
			[]string{"take_profit", "stop_loss", "tpsl"},
			[]string{"filled", "canceled", "cancelled", "expired", "failed", "rejected", "superseded"}).
		Pluck("client_order_id", &ids).Error
	if err != nil {
		return out
	}
	for _, id := range ids {
		if s := strings.TrimSpace(id); s != "" {
			out[s] = true
		}
	}
	return out
}

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

// tpslGapLogAt 记录"仓位缺少 TP/SL"告警的上次上报时刻,按 (owner, symbol) 限流。
var (
	tpslGapLogMu sync.Mutex
	tpslGapLogAt = map[string]time.Time{}
)

// shouldLogTPSLGap 每 (owner, symbol) 每 10 分钟只放行一次告警。
func (m *Manager) shouldLogTPSLGap(ownerID uint, symbol string) bool {
	k := fmt.Sprintf("%d|%s", ownerID, exchange.NormalizeSymbol(symbol))
	tpslGapLogMu.Lock()
	defer tpslGapLogMu.Unlock()
	if t, ok := tpslGapLogAt[k]; ok && time.Since(t) < 10*time.Minute {
		return false
	}
	tpslGapLogAt[k] = time.Now()
	return true
}

// betterTPSLRow 在同一交易所仓位的多条重复记账行里挑出该被管理的那条:
// 已配好 TP/SL 的优先(否则会拿一条空配置去反复"补挂"并报错),同等条件下取较新的。
func betterTPSLRow(a, b models.StrategyPosition) models.StrategyPosition {
	aHas := a.TakeProfit > 0 && a.StopLoss > 0
	bHas := b.TakeProfit > 0 && b.StopLoss > 0
	if aHas != bHas {
		if aHas {
			return a
		}
		return b
	}
	if a.ID >= b.ID {
		return a
	}
	return b
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

	// 交易所侧每个 (账户, symbol) 只有一个仓位,但库里可能有多行 —— 多 owner/多策略
	// 共用同一个交易所账户时的重复记账。逐行管理有两个后果:同一个仓位被重复挂 TP/SL
	// (触发"检测到重复止盈止损单"的撤挂循环),以及那些 tp=0/sl=0 的重复行每 15s 刷一条
	// error(2026-09-05 我踏马来了/USDT 六小时刷了 432 次)。按 (owner, symbol) 去重,
	// 保留信息最全的那一行,一个仓位只管理一次。
	best := map[string]models.StrategyPosition{}
	for _, r := range openRows {
		k := fmt.Sprintf("%d|%s", r.OwnerID, exchange.NormalizeSymbol(r.Symbol))
		if cur, ok := best[k]; ok {
			best[k] = betterTPSLRow(cur, r)
		} else {
			best[k] = r
		}
	}
	byOwner := map[uint][]models.StrategyPosition{}
	for _, r := range best {
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
		// 该 owner 的实例集,供 strategy_id 解析不到时按 symbol 兜底(与 scanROILimits 一致):
		// 策略被停/删后其在持仓的交易所止损仍需维护,否则无人再补挂(CR P2)。
		var ownerInsts []*StrategyInstance
		m.mu.RLock()
		for _, in := range m.instances {
			if in != nil && in.OwnerID == uid {
				ownerInsts = append(ownerInsts, in)
			}
		}
		m.mu.RUnlock()
		for _, row := range rows {
			active, ok := activeBySymbol[strings.ToUpper(row.Symbol)]
			if !ok {
				continue
			}
			m.mu.RLock()
			inst := m.instances[row.StrategyID]
			m.mu.RUnlock()
			if inst == nil {
				inst = findGuardStrategyInstance(ownerInsts, row.Symbol)
			}
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
				// 只认本仓位自己挂的单。多策略共用一个交易所账户时,交易所侧同一 symbol
				// 会同时存在别的策略的 TP/SL;若按 symbol 全量计数,会把对方的单当成
				// "重复单"撤掉重建,两个策略互相踩、每十几秒循环一轮,且撤到重挂之间
				// 仓位无保护(2026-08-29 NIL/USDT 实例)。以 client_order_id 归属过滤。
				mine := m.ownedTPSLOrderIDs(row.ID)
				owned := make([]exchange.USDMAlgoOrder, 0, len(algoOrders))
				for _, ord := range algoOrders {
					if len(mine) == 0 || mine[strings.TrimSpace(ord.ClientAlgoID)] {
						owned = append(owned, ord)
					}
				}
				if len(mine) > 0 && len(owned) != len(algoOrders) {
					emitStrategyLog(inst, "info", fmt.Sprintf("忽略他方止盈止损单 symbol=%s 本仓=%d 账户总计=%d", row.Symbol, len(owned), len(algoOrders)))
				}
				algoOrders = owned
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
				hasTP := tpCount > 0
				hasSL := slCount > 0
				// 每仓位只应有一对 TP/SL。多于一对（历史 bug 曾因查单端点 404 无限
				// 补挂）→ 走下面的撤光重建路径自愈。
				if tpCount > 1 || slCount > 1 {
					emitStrategyLog(inst, "info", fmt.Sprintf("检测到重复止盈止损单 symbol=%s tp=%d sl=%d，撤销全部后重建", row.Symbol, tpCount, slCount))
					hasTP = false
					hasSL = false
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
					// 15s 一轮的巡检里,这条会一直命中同一个仓位 —— 必须限流,否则一个
					// 无配置的仓位就能把 strategy_logs 刷爆(432 条/6 小时的实测值)。
					// 限流只降低重复度,不掩盖问题:仓位真裸奔时每 10 分钟仍会报一次。
					if m.shouldLogTPSLGap(uid, row.Symbol) {
						emitStrategyLog(inst, "error", fmt.Sprintf("仓位缺少有效止盈止损配置 symbol=%s tp=%v sl=%v", row.Symbol, tp, sl))
					}
					return
				}
				if hasTP && hasSL {
					return
				}

				// 逐单撤销(只撤 algoOrders 里已过滤为"本仓位自己的"那些);不能用
				// CancelUSDMAlgoOpenOrders(按 symbol 全撤),那会连别的策略的保护单一起撤掉。
				for _, ord := range algoOrders {
					_ = bx.CancelUSDMAlgoOrderByRef(uid, row.Symbol, fmt.Sprint(ord.AlgoID), ord.ClientAlgoID)
				}
				baseClientOrderID := models.GenerateUUID()
				created, err := bx.PlaceUSDMTPStopOrders(uid, baseClientOrderID, row.Symbol, tp, sl)
				switch {
				case errors.Is(err, exchange.ErrStopLossBreached):
					// 价格已穿越止损位 → 止损条件其实已满足,但挂单挂不上去(挂上即触发),
					// 仓位处于无保护状态。立即市价平仓兑现止损,防止亏损继续扩大。
					emitStrategyLog(inst, "error", fmt.Sprintf("止损价已被穿越,立即市价平仓 symbol=%s sl=%v", row.Symbol, sl))
					if cerr := bx.ClosePosition(row.Symbol, uid); cerr != nil {
						emitStrategyLog(inst, "error", fmt.Sprintf("止损失效后市价平仓失败,请人工介入 symbol=%s err=%v", row.Symbol, cerr))
					} else {
						emitStrategyLog(inst, "info", fmt.Sprintf("止损失效已市价平仓 symbol=%s", row.Symbol))
						// 只清本仓位残留的另一腿,别动其他策略在同 symbol 上的保护单。
						for _, ord := range algoOrders {
							_ = bx.CancelUSDMAlgoOrderByRef(uid, row.Symbol, fmt.Sprint(ord.AlgoID), ord.ClientAlgoID)
						}
					}
					return
				case exchange.IsBenignOrderMiss(err):
					// 仓位已被平掉(DB 落后于交易所)。补挂本就该跳过,不是故障。
					emitStrategyLog(inst, "info", fmt.Sprintf("补设止盈止损跳过:仓位已不存在 symbol=%s", row.Symbol))
					return
				case err != nil:
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
