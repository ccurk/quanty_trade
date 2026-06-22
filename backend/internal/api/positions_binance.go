package api

// 历史已平仓位的"币安源"实现。
//
// 之前：closed positions 直接查 DB 的 StrategyPosition 表，依赖 strategy 进程能
//        100% 监听到 TP/SL 成交并写库。重启/网络抖动/部分平仓时可能漏记。
// 现在：在 ListPositions 的 closed 分支，优先从币安 userTrades 重建 closed positions
//        （与 paired_trades 同源），再用 (symbol + close_time) match DB
//        StrategyPosition 拿到 strategy_id/name attribution。
//
// 如果币安拉失败 → fallback 回 DB。

import (
	"math"
	"sort"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

// closedPositionsFromBinance 从币安拉过去 hours 小时的成交，配对成已平仓 Position 列表。
// 第二个返回值是 fetchError；非空 = 拉失败、调用方应 fallback DB。
func closedPositionsFromBinance(uid uint, hours int) ([]exchange.Position, string) {
	if stratMgr == nil || stratMgr.GetExchange() == nil {
		return nil, "no exchange"
	}
	bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return nil, "not usdm binance"
	}

	now := time.Now()
	since := now.Add(-time.Duration(hours) * time.Hour)

	// 1. 先用 income 锁定 24h 内有过平仓的 symbol（比直接遍历所有 symbol 快得多）
	events, err := bx.USDMIncomeHistory(uid, since, now, 1000)
	if err != nil {
		return nil, "income: " + err.Error()
	}
	symbolSet := map[string]struct{}{}
	for _, e := range events {
		if e.Symbol != "" && e.IncomeType == "REALIZED_PNL" {
			symbolSet[e.Symbol] = struct{}{}
		}
	}
	if len(symbolSet) == 0 {
		return []exchange.Position{}, ""
	}

	// 2. 每个 symbol 拉 userTrades 并按时间排序
	type fill struct {
		Time  int64
		Side  string // BUY / SELL
		Qty   float64
		Price float64
		PnL   float64
		Fee   float64
	}
	allFills := map[string][]fill{}
	for sym := range symbolSet {
		trades, err := bx.USDMUserTrades(uid, sym, since, now, 1000)
		if err != nil {
			continue // 单 symbol 失败不阻塞其它
		}
		for _, t := range trades {
			allFills[sym] = append(allFills[sym], fill{
				Time: t.Time, Side: t.Side, Qty: t.Qty, Price: t.Price,
				PnL: t.RealizedPnL, Fee: -math.Abs(t.Commission),
			})
		}
		sort.Slice(allFills[sym], func(i, j int) bool { return allFills[sym][i].Time < allFills[sym][j].Time })
	}

	// 3. running-position FIFO 配对（替代旧的朴素 1开:1平 状态机，那个会丢单 + 方向错配）。
	//    跟踪每个 symbol 的净持仓 qty：同向 fill 累计开仓（VWAP 进场价），反向 fill 平仓
	//    （累计 realized + 手续费 + VWAP 出场价）。持仓回到 0 → 记一条完整 round-trip；
	//    反向穿越 0 → 先平掉旧仓再用余量反向开新仓。支持部分平仓 / 加仓 / 反手。
	type chain struct {
		Symbol       string
		OpenTime     int64
		CloseTime    int64
		OpenSide     string // BUY/SELL（开仓方向）
		OpenPrice    float64
		Qty          float64
		ClosePrice   float64
		RealizedPnL  float64
		TotalCommFee float64
	}
	type rtState struct {
		active        bool
		openTime      int64
		openSide      string
		posQty        float64 // signed：+ 多 / - 空
		entryQtyAbs   float64
		entryNotional float64
		closeQtyAbs   float64
		closeNotional float64
		realized      float64
		commission    float64 // 负值
		lastClose     int64
	}
	var chains []chain
	for sym, fills := range allFills {
		var st rtState
		emit := func() {
			if !st.active || st.closeQtyAbs <= 0 {
				return
			}
			avgEntry := 0.0
			if st.entryQtyAbs > 0 {
				avgEntry = st.entryNotional / st.entryQtyAbs
			}
			avgClose := 0.0
			if st.closeQtyAbs > 0 {
				avgClose = st.closeNotional / st.closeQtyAbs
			}
			chains = append(chains, chain{
				Symbol: sym, OpenTime: st.openTime, CloseTime: st.lastClose,
				OpenSide: st.openSide, OpenPrice: avgEntry, Qty: st.closeQtyAbs,
				ClosePrice: avgClose, RealizedPnL: st.realized, TotalCommFee: st.commission,
			})
		}
		for i := range fills {
			f := fills[i]
			signed := f.Qty
			if strings.EqualFold(f.Side, "SELL") {
				signed = -f.Qty
			}
			if !st.active {
				st = rtState{
					active: true, openTime: f.Time, openSide: f.Side,
					posQty: signed, entryQtyAbs: f.Qty, entryNotional: f.Qty * f.Price,
					commission: f.Fee, realized: f.PnL,
				}
				continue
			}
			st.commission += f.Fee
			sameDir := (st.posQty > 0 && strings.EqualFold(f.Side, "BUY")) ||
				(st.posQty < 0 && strings.EqualFold(f.Side, "SELL"))
			if sameDir {
				st.posQty += signed
				st.entryQtyAbs += f.Qty
				st.entryNotional += f.Qty * f.Price
				continue
			}
			// 反向 fill = 平仓（可能部分 / 全平 / 反手穿越）
			st.realized += f.PnL
			closingQty := math.Min(f.Qty, math.Abs(st.posQty))
			st.closeQtyAbs += closingQty
			st.closeNotional += closingQty * f.Price
			st.lastClose = f.Time
			newPos := st.posQty + signed
			if math.Abs(newPos) < 1e-12 {
				emit()
				st = rtState{}
			} else if (st.posQty > 0) != (newPos > 0) {
				// 反手穿越：旧仓全平 + 余量反向开新仓
				emit()
				rem := math.Abs(newPos)
				st = rtState{
					active: true, openTime: f.Time, openSide: f.Side,
					posQty: newPos, entryQtyAbs: rem, entryNotional: rem * f.Price,
				}
			} else {
				st.posQty = newPos // 部分平仓，方向不变
			}
		}
	}

	// 4. 转换成 exchange.Position
	out := make([]exchange.Position, 0, len(chains))
	for _, c := range chains {
		direction := "long"
		if strings.EqualFold(c.OpenSide, "SELL") {
			direction = "short"
		}
		notional := c.Qty * c.OpenPrice
		retRate := 0.0
		if notional > 0 {
			retRate = (c.RealizedPnL / notional) * 100
		}
		pos := exchange.Position{
			Symbol:             bxDisplaySymbol(c.Symbol), // BILLUSDT → BILL/USDT
			Direction:          direction,
			Amount:             c.Qty,
			Price:              c.OpenPrice,
			ClosedQty:          c.Qty,
			AvgClosePrice:      c.ClosePrice,
			RealizedPnL:        c.RealizedPnL,
			RealizedReturnRate: retRate,
			ExchangeName:       bx.GetName(),
			Status:             "closed",
			OwnerID:            uid,
			OpenTime:           time.UnixMilli(c.OpenTime),
			CloseTime:          time.UnixMilli(c.CloseTime),
		}
		out = append(out, pos)
	}

	// 5. 用 (symbol + close_time ±5min) match DB StrategyPosition 补 strategy_id/name
	enrichStrategyAttribution(uid, out, hours)

	// 6. 按 close_time desc 排序（前端期望最新在上）
	sort.Slice(out, func(i, j int) bool { return out[i].CloseTime.After(out[j].CloseTime) })

	return out, ""
}

// enrichStrategyAttribution 把 binance 重建的 closed positions 用 (symbol+close_time)
// 反查我们 DB 里的 StrategyPosition 表，拿到 strategy_id/strategy_name 填回去。
// match 容差 ±5 分钟（binance 时间可能跟我们落库时间有偏差）。
func enrichStrategyAttribution(uid uint, positions []exchange.Position, hours int) {
	if database.DB == nil || len(positions) == 0 {
		return
	}
	since := time.Now().Add(-time.Duration(hours+1) * time.Hour)
	var rows []models.StrategyPosition
	_ = database.DB.
		Where("owner_id = ? AND status = ? AND close_time >= ?", uid, "closed", since).
		Find(&rows).Error
	if len(rows) == 0 {
		return
	}

	// 按 normalized symbol 索引
	type dbRef struct {
		StrategyID   string
		StrategyName string
		CloseTime    time.Time
	}
	bySym := map[string][]dbRef{}
	for _, r := range rows {
		ns := exchange.NormalizeSymbol(r.Symbol)
		bySym[ns] = append(bySym[ns], dbRef{StrategyID: r.StrategyID, StrategyName: r.StrategyName, CloseTime: r.CloseTime})
	}

	tolerance := 5 * time.Minute
	for i := range positions {
		p := &positions[i]
		ns := exchange.NormalizeSymbol(p.Symbol)
		refs := bySym[ns]
		if len(refs) == 0 {
			continue
		}
		bestIdx := -1
		bestDiff := tolerance + time.Hour // 初始大值
		for j, r := range refs {
			diff := r.CloseTime.Sub(p.CloseTime)
			if diff < 0 {
				diff = -diff
			}
			if diff <= tolerance && diff < bestDiff {
				bestIdx = j
				bestDiff = diff
			}
		}
		if bestIdx >= 0 {
			p.StrategyID = refs[bestIdx].StrategyID
			p.StrategyName = refs[bestIdx].StrategyName
			// 消费掉这条 ref 避免重复 match
			refs = append(refs[:bestIdx], refs[bestIdx+1:]...)
			bySym[ns] = refs
		}
	}
}

// bxDisplaySymbol 把币安 raw symbol (BILLUSDT) 转成平台展示形式 (BILL/USDT)。
// 复用 exchange 包里现有逻辑（如果有），否则简单拼接。
func bxDisplaySymbol(raw string) string {
	raw = strings.TrimSpace(strings.ToUpper(raw))
	if raw == "" {
		return raw
	}
	// 简化：常见 quote 后缀切一下，不能识别就原样返回
	for _, quote := range []string{"USDT", "USDC", "BUSD", "BTC", "ETH"} {
		if strings.HasSuffix(raw, quote) {
			base := strings.TrimSuffix(raw, quote)
			if base != "" {
				return base + "/" + quote
			}
		}
	}
	return raw
}
