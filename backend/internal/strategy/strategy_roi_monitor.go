package strategy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

var roiGuardOnce sync.Once
var roiStopLossScanOnce sync.Once

func (m *Manager) StartROIGuardMonitor(ctx context.Context) {
	roiGuardOnce.Do(func() {
		go m.runROIGuardMonitor(ctx)
	})
}

func (m *Manager) StartROISLScanMonitor(ctx context.Context) {
	roiStopLossScanOnce.Do(func() {
		go m.runROISLScanMonitor(ctx)
	})
}

func (m *Manager) runROIGuardMonitor(ctx context.Context) {
	m.roiGuardTick()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.roiGuardTick()
		}
	}
}

func (m *Manager) runROISLScanMonitor(ctx context.Context) {
	m.roiStopLossTick()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.roiStopLossTick()
		}
	}
}

func (m *Manager) roiGuardTick() {
	m.scanROILimits(false)
}

func (m *Manager) roiStopLossTick() {
	m.scanROILimits(true)
}

func (m *Manager) scanROILimits(stopLossOnly bool) {
	if m == nil || database.DB == nil {
		return
	}
	bx, ok := m.exchange.(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return
	}

	var openRows []models.StrategyPosition
	if err := database.DB.Where("status = ?", "open").Find(&openRows).Error; err != nil {
		return
	}

	now := time.Now()
	byOwner := map[uint][]models.StrategyPosition{}
	for _, r := range openRows {
		byOwner[r.OwnerID] = append(byOwner[r.OwnerID], r)
	}
	ownerInstances := map[uint][]*StrategyInstance{}
	ownerInstanceByID := map[uint]map[string]*StrategyInstance{}
	m.mu.RLock()
	for _, inst := range m.instances {
		if inst == nil {
			continue
		}
		ownerInstances[inst.OwnerID] = append(ownerInstances[inst.OwnerID], inst)
		if ownerInstanceByID[inst.OwnerID] == nil {
			ownerInstanceByID[inst.OwnerID] = map[string]*StrategyInstance{}
		}
		ownerInstanceByID[inst.OwnerID][inst.ID] = inst
		if _, ok := byOwner[inst.OwnerID]; !ok {
			byOwner[inst.OwnerID] = nil
		}
	}
	m.mu.RUnlock()

	// watchedAll: 本轮所有 owner 的在持 symbol 全集，喂给 WS 守护加速器与
	// 金字塔计数清理（strategy_ws_guard.go / strategy_pyramid.go）。
	watchedAll := map[string]struct{}{}
	// #57 收养幂等闸的账户视图: 共用一个交易所账户的 owner 集合。单向持仓下
	// 一个 symbol 在账户上只有一个净仓,DB 里也只允许一行 open 行,查重必须跨
	// owner 查整个账户,否则 A owner 的入场行挡不住 B owner 的收养(BEAT 08-17
	// 双行互搏实证:入场行 main + 收养行 fade 各自 cancel/replace 对方 TP/SL 单)。
	acctUIDs := map[string][]uint{}
	for uid := range byOwner {
		k := bx.AccountKey(uid)
		acctUIDs[k] = append(acctUIDs[k], uid)
	}
	// owner 迭代排序: map 随机序会让"哪行先认领守护权"每 tick 抖动;固定升序后,
	// 同净仓多行(历史遗留)恒由同一行驱动,另一行静默等 close-sync 清理。
	ownerIDs := make([]uint, 0, len(byOwner))
	for uid := range byOwner {
		ownerIDs = append(ownerIDs, uid)
	}
	sort.Slice(ownerIDs, func(i, j int) bool { return ownerIDs[i] < ownerIDs[j] })
	// guardianClaimed: (账户,symbol) → 本 tick 已有一行在驱动 TP/SL。第二行跳过,
	// 防双守护用各自 config 互撤重挂交易所条件单(重复单检测风暴+瞬时无单空窗)。
	guardianClaimed := map[string]struct{}{}
	// 账户级收养去重:共用一个交易所账户(同 API key)的多个 owner,在单向持仓
	// 下每个 symbol 只有一个净仓,只应有一行 DB 持仓行来守护它。按 (account,symbol)
	// 记账,防止各 owner 的扫描把同一个净仓各收养一行(一仓多行、PnL 散落的根)。
	// 先用已存在的开仓行播种(entry 建的行是权威归属),收养时再补进去。
	trackedByAccount := map[string]map[string]struct{}{}
	markAcctTracked := func(acct, sym string) {
		if trackedByAccount[acct] == nil {
			trackedByAccount[acct] = map[string]struct{}{}
		}
		trackedByAccount[acct][sym] = struct{}{}
	}
	acctTracked := func(acct, sym string) bool {
		if m, ok := trackedByAccount[acct]; ok {
			_, ok = m[sym]
			return ok
		}
		return false
	}
	for uid, rows := range byOwner {
		acct := bx.AccountKey(uid)
		for _, r := range rows {
			// 只用"确实能被某个 instance 守护"的行占坑。若该行的策略已停/被拉黑
			// (guard 循环解析 inst 会得到 nil 而跳过它),就【不】标记 tracked,好让
			// 同账户其他 owner 去收养它 —— 否则一个无法守护的陈旧行会把别人的收养也
			// 抑制掉,导致这个净仓无人守护(CR P1)。解析方式与下方 guard 循环一致。
			inst := ownerInstanceByID[uid][r.StrategyID]
			if inst == nil {
				inst = findGuardStrategyInstance(ownerInstances[uid], r.Symbol)
			}
			if inst != nil {
				markAcctTracked(acct, strings.ToUpper(r.Symbol))
			}
		}
	}
	for _, uid := range ownerIDs {
		rows := byOwner[uid]
		acct := bx.AccountKey(uid)
		ps, err := bx.FetchPositions(uid, "active")
		if err != nil {
			continue
		}
		posBySymbol := map[string]exchange.Position{}
		for _, p := range ps {
			posBySymbol[strings.ToUpper(p.Symbol)] = p
			watchedAll[strings.ToUpper(p.Symbol)] = struct{}{}
		}
		trackedRows := append([]models.StrategyPosition(nil), rows...)
		seenSymbols := map[string]struct{}{}
		for _, r := range trackedRows {
			seenSymbols[strings.ToUpper(r.Symbol)] = struct{}{}
		}
		for _, p := range ps {
			symKey := strings.ToUpper(p.Symbol)
			if _, ok := seenSymbols[symKey]; ok {
				continue
			}
			// 同账户已有一行(别的 owner 的开仓行或先前收养)在守护这个净仓,不再重复收养。
			// 那一行会在它自己 owner 的守护循环里被处理,净仓恰好被守护一次。
			if acctTracked(acct, symKey) {
				continue
			}
			inst := findGuardStrategyInstance(ownerInstances[uid], p.Symbol)
			if inst == nil {
				continue
			}
			// #57 收养幂等闸(账户级): 该净仓只要在共享账户的任何 owner 名下已有
			// open 行(入场行或先前收养行),就绝不再造第二行。本 tick 的 openRows
			// 快照可能落后于刚落库的入场行(开仓后 ~3s 内的收养竞态窗),所以这里
			// 必须现查 DB 而不是只信快照;已有行由其 owner 的守护循环接管。
			var dupCount int64
			if err := database.DB.Model(&models.StrategyPosition{}).
				Where("owner_id IN ? AND symbol = ? AND status = ?", acctUIDs[acct], p.Symbol, "open").
				Count(&dupCount).Error; err == nil && dupCount > 0 {
				markAcctTracked(acct, symKey)
				continue
			}
			side := "buy"
			if strings.EqualFold(p.Direction, "short") {
				side = "sell"
			}
			tp, sl := resolveTPSLFromROI(inst, side, p.Price, 0, 0)
			synthetic := models.StrategyPosition{
				StrategyID:   inst.ID,
				StrategyName: inst.Name,
				OwnerID:      uid,
				Exchange:     bx.GetName(),
				Symbol:       p.Symbol,
				Direction:    p.Direction,
				Amount:       p.Amount,
				AvgPrice:     p.Price,
				TakeProfit:   tp,
				StopLoss:     sl,
				Status:       "open",
				OpenTime:     p.OpenTime,
				UpdatedAt:    now,
			}
			trackedRows = append(trackedRows, synthetic)
			seenSymbols[symKey] = struct{}{}
			markAcctTracked(acct, symKey)
			var existing models.StrategyPosition
			if err := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", uid, inst.ID, p.Symbol, "open").First(&existing).Error; err != nil {
				_ = database.DB.Create(&synthetic).Error
				emitStrategyLog(inst, "info", fmt.Sprintf("仓位守护补录持仓 symbol=%s entry=%0.8f tp=%0.8f sl=%0.8f", p.Symbol, p.Price, tp, sl))
			}
		}
		for _, r := range trackedRows {
			inst := ownerInstanceByID[uid][r.StrategyID]
			if inst == nil {
				inst = findGuardStrategyInstance(ownerInstances[uid], r.Symbol)
			}
			if inst == nil {
				continue
			}
			pos, ok := posBySymbol[strings.ToUpper(r.Symbol)]
			if !ok {
				continue
			}
			// #57 守护唯一化: 每 (账户,symbol) 净仓每 tick 只由一行驱动出场管理。
			// owner 升序+行序(DB 主键升序,入场行先于后造的收养行)保证恒定同一行
			// 当选;重复行不烧认领、不动单,等 close-sync 收敛。
			claimKey := acct + "\x00" + strings.ToUpper(r.Symbol)
			if _, dup := guardianClaimed[claimKey]; dup {
				continue
			}
			guardianClaimed[claimKey] = struct{}{}
			roi := pos.ReturnRate
			unpnl := pos.UnrealizedPnL
			currentPrice := pos.CurrentPrice
			if currentPrice <= 0 {
				currentPrice = pos.Price
			}
			side := "buy"
			if strings.EqualFold(pos.Direction, "short") {
				side = "sell"
			}
			tpPct := normalizedTPSLPct(inst, "take_profit_pct") * 100
			slPct := normalizedTPSLPct(inst, "stop_loss_pct") * 100
			tp := r.TakeProfit
			sl := r.StopLoss
			if tp <= 0 || sl <= 0 {
				rtp, rsl := resolveTPSLFromROI(inst, side, pos.Price, tp, sl)
				if tp <= 0 {
					tp = rtp
				}
				if sl <= 0 {
					sl = rsl
				}
			}
			// 出场推移（只在 5s 守护里做，分钟止损扫描不动）：先保本、再移动止盈，
			// 最后看赢家金字塔（默认关，config 显式开启；亏损仓在函数内硬拒）。
			if !stopLossOnly {
				m.maybeMoveBreakeven(inst, uid, r, pos, side, currentPrice, tp)
				m.maybeTrail(inst, uid, r, pos, side, currentPrice, tp)
				m.maybePyramid(inst, uid, r, pos, side, currentPrice)
			}
			reason := ""
			if !stopLossOnly {
				if currentPrice > 0 {
					if side == "buy" {
						if tp > 0 && currentPrice >= tp {
							reason = "guard_tp"
						}
						if reason == "" && sl > 0 && currentPrice <= sl {
							reason = "guard_sl"
						}
					} else {
						if tp > 0 && currentPrice <= tp {
							reason = "guard_tp"
						}
						if reason == "" && sl > 0 && currentPrice >= sl {
							reason = "guard_sl"
						}
					}
				}
			}
			if !stopLossOnly && reason == "" && tpPct > 0 && roi >= tpPct {
				reason = "guard_roi_tp"
			}
			if reason == "" && slPct > 0 && roi <= -slPct {
				if stopLossOnly {
					reason = "minute_roi_sl_scan"
				} else {
					reason = "guard_roi_sl"
				}
			}
			if reason == "" {
				continue
			}
			if !m.tryMarkQuickClose(uid, strings.ToUpper(r.Symbol), now) {
				continue
			}
			if stopLossOnly {
				emitStrategyLog(inst, "info", fmt.Sprintf("定时仓位扫描触发止损收益率平仓：symbol=%s price=%0.8f roi=%0.4f%% pnl=%0.4f sl=%0.8f sl_pct=%0.2f%% reason=%s，自动平仓并撤销止盈止损单", r.Symbol, currentPrice, roi, unpnl, sl, slPct, reason))
			} else {
				emitStrategyLog(inst, "info", fmt.Sprintf("全局仓位守护触发：symbol=%s price=%0.8f roi=%0.4f%% pnl=%0.4f tp=%0.8f sl=%0.8f tp_pct=%0.2f%% sl_pct=%0.2f%% reason=%s，自动平仓", r.Symbol, currentPrice, roi, unpnl, tp, sl, tpPct, slPct, reason))
			}
			if err := m.closePositionForInstance(inst, r.Symbol, reason, ""); err != nil {
				m.releaseQuickClose(uid, strings.ToUpper(r.Symbol))
				if stopLossOnly {
					emitStrategyLog(inst, "error", fmt.Sprintf("定时仓位扫描触发止损收益率平仓失败 symbol=%s reason=%s err=%v", r.Symbol, reason, err))
				} else {
					emitStrategyLog(inst, "error", fmt.Sprintf("全局仓位守护触发但平仓失败 symbol=%s reason=%s err=%v", r.Symbol, reason, err))
				}
			}
		}
	}
	if !stopLossOnly {
		m.setWSWatched(watchedAll)
		m.prunePyramidCounters(watchedAll)
	}
}

func findGuardStrategyInstance(insts []*StrategyInstance, symbol string) *StrategyInstance {
	for _, inst := range insts {
		if inst == nil {
			continue
		}
		// 策略组模式：blacklist 是组内币池互斥的边界（main 拉黑专家池），
		// 无主仓位收养时同样要尊重，否则会把专家池的仓位错认给 main。
		if isBlacklistedSymbol(inst, symbol) {
			continue
		}
		if isAllowedSymbol(inst, symbol) {
			return inst
		}
	}
	return nil
}
