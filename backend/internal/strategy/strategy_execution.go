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

// confSizingMultiplier 按信号置信度线性插值仓位乘数：
// conf<=conf_lo → min_mult，conf>=conf_hi → max_mult，中间线性。
// conf_sizing_enabled 未开启或信号未带置信度时返回 (1, false)，行为与旧版完全一致。
func confSizingMultiplier(inst *StrategyInstance, confidence float64) (float64, bool) {
	if inst == nil || !getBool(inst.Config()["conf_sizing_enabled"]) || confidence <= 0 {
		return 1, false
	}
	lo := getNumber(inst.Config()["conf_sizing_conf_lo"])
	if lo <= 0 {
		lo = getNumber(inst.Config()["min_confidence"])
	}
	if lo <= 0 {
		lo = 0.40
	}
	hi := getNumber(inst.Config()["conf_sizing_conf_hi"])
	if hi <= lo {
		hi = lo + 0.15
	}
	minM := getNumber(inst.Config()["conf_sizing_min_mult"])
	if minM <= 0 {
		minM = 0.60
	}
	maxM := getNumber(inst.Config()["conf_sizing_max_mult"])
	if maxM <= 0 {
		maxM = 1.40
	}
	// 乘数带宽夹在 [0.2, 2]，且 max 不得低于 min：配置写反时按保守方向收敛。
	if minM < 0.20 {
		minM = 0.20
	}
	if minM > 1 {
		minM = 1
	}
	if maxM < minM {
		maxM = minM
	}
	if maxM > 2 {
		maxM = 2
	}
	t := (confidence - lo) / (hi - lo)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return minM + t*(maxM-minM), true
}

// confLeverage 按信号置信度在 [conf_leverage_min, conf_leverage_max] 之间线性插值杠杆：
// conf<=conf_lo → min，conf>=conf_hi → max，中间线性取整（四舍五入）。
// 带宽 conf_leverage_conf_lo/hi 未配时回落到 conf_sizing 的同一带宽，最后回落到 min_confidence。
// conf_leverage_enabled 未开启或信号未带置信度时返回 config 里的固定 leverage，行为与旧版完全一致。
// 注意：返回的是【目标上限】——下方按档位/可用额度选 levChosen 时仍可能被下调。
func confLeverage(inst *StrategyInstance, confidence float64) int {
	if inst == nil {
		return 1
	}
	lev := int(getNumber(inst.Config()["leverage"]))
	if lev <= 0 {
		lev = 1
	}
	if !getBool(inst.Config()["conf_leverage_enabled"]) || confidence <= 0 {
		return lev
	}
	lo := getNumber(inst.Config()["conf_leverage_conf_lo"])
	if lo <= 0 {
		lo = getNumber(inst.Config()["conf_sizing_conf_lo"])
	}
	if lo <= 0 {
		lo = getNumber(inst.Config()["min_confidence"])
	}
	if lo <= 0 {
		lo = 0.40
	}
	hi := getNumber(inst.Config()["conf_leverage_conf_hi"])
	if hi <= 0 {
		hi = getNumber(inst.Config()["conf_sizing_conf_hi"])
	}
	if hi <= lo {
		hi = lo + 0.15
	}
	minL := int(getNumber(inst.Config()["conf_leverage_min"]))
	if minL <= 0 {
		minL = 3
	}
	maxL := int(getNumber(inst.Config()["conf_leverage_max"]))
	if maxL <= 0 {
		maxL = 20
	}
	// 1 是 SetLeverage 的下界、125 是交易所上限（binance.go 同界）；配置写反时按保守方向收敛。
	if minL < 1 {
		minL = 1
	}
	if maxL < minL {
		maxL = minL
	}
	if maxL > 125 {
		maxL = 125
	}
	t := (confidence - lo) / (hi - lo)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return minL + int(t*float64(maxL-minL)+0.5)
}

// usdmAvailableUSDT 是可注入接缝：单测替换它即可喂入确定的可用余额，从而覆盖
// 「可用余额被吃到初始保证金不足下限 → 拒单」这条只在低余额下才走到的分支。
var usdmAvailableUSDT = func(bx *exchange.BinanceExchange, ownerID uint) (float64, error) {
	return bx.USDMAvailableUSDT(ownerID)
}

// usdmWalletEquity 是同款可注入接缝，返回「权益」= Wallet + Unrealized
// （即 /fapi/v2/balance 的 balance + crossUnPnl，与 /fapi/v2/account 的
// totalMarginBalance 同值）。和 usdmAvailableUSDT 共用同一次 REST 与同一个
// 5s 缓存，所以切乘基不增加任何请求。
var usdmWalletEquity = func(bx *exchange.BinanceExchange, ownerID uint) (exchange.USDMBalance, error) {
	return bx.USDMWalletEquity(ownerID)
}

func resolveUSDMOrderAmount(inst *StrategyInstance, bx *exchange.BinanceExchange, symbol string, amount float64, price float64, stopLoss float64, confidence float64) (float64, error) {
	if inst == nil || bx == nil {
		return 0, nil
	}
	lev := confLeverage(inst, confidence)
	mode := strings.ToLower(strings.TrimSpace(getString(inst.Config()["order_amount_mode"])))
	if mode == "" {
		mode = "notional"
	}
	minNotional := getNumber(inst.Config()["min_order_notional"])
	if minNotional <= 0 {
		minNotional = 5
	}

	getPx := func() (float64, error) {
		if price > 0 {
			return price, nil
		}
		inst.mu.Lock()
		px := 0.0
		if inst.lastCandleClose != nil {
			px = inst.lastCandleClose[symbol]
		}
		inst.mu.Unlock()
		if px > 0 {
			return px, nil
		}
		return bx.LastPrice(symbol)
	}

	px, err := getPx()
	if err != nil || px <= 0 {
		emitStrategyLog(inst, "error", fmt.Sprintf("跳过开仓：获取价格失败 symbol=%s err=%v", symbol, err))
		return 0, fmt.Errorf("price unavailable")
	}
	// 杠杆按【止损宽度】封顶 —— 让止损夹永不咬合。
	//
	// 止损夹在 strategy_position.go:508（`minSL := entryPx * (1 - 0.3/levUsed)`）只保证
	// 「单笔止损 ≤ 该仓保证金的 30%」，但它【靠收紧止损】做到：ATR 止损比 0.3/levUsed 宽时，
	// 止损被拉近到 0.3/levUsed。实测 2026-09-21 的 4015 条 long 信号：ATR 止损距离中位
	// 1.954%、p90 3.662%；而 lev=20（conf=1.0，占信号 31%）时夹=1.500% ⇒ 75.8% 的入场
	// 止损被压到 1.500%，p90 那档等于把止损砍掉 59% —— 止损落进币自己的噪声带里，
	// 于是「先破刀」成为常态（实测 76%），而 R:R 仍是 1.250（止盈没跟着动）。
	//
	// 修法不是放宽夹（那样单笔风险就不封顶了），而是【降杠杆】使 0.3/lev ≥ ATR 止损距离：
	//   · 止损留在策略设计的宽度上（不再被噪声打掉）
	//   · 单笔止损金额 = 名义×止损宽 = (权益×pct×lev)×止损宽 = 权益×pct×0.3 —— 逐笔恒定
	//     （这正是 risk-budget 口径：风险由 pct 决定，与杠杆和波动率都解耦）
	//   · max_initial_margin_usdt / min_initial_margin_usdt / avail 可行性闸门全部照旧
	// 窄止损的币不受影响：capLev 高于 confLeverage 时不生效，杠杆仍按置信度浮动。
	// 下限取 conf_leverage_min（默认 3，与 owner「杠杆 3~20x」口径一致）；ATR 止损宽于
	// 0.3/3=10% 的极少数信号仍会被夹，这是为尊重杠杆下限付的代价（实测约 0.7% 的信号）。
	if stopLoss > 0 && px > 0 {
		d := stopLoss - px
		if d < 0 {
			d = -d
		}
		if d > 0 {
			capLev := int(0.3 / (d / px))
			floorLev := int(getNumber(inst.Config()["conf_leverage_min"]))
			if floorLev < 1 {
				floorLev = 3
			}
			if capLev < floorLev {
				capLev = floorLev
			}
			if capLev < lev {
				emitStrategyLog(inst, "info", fmt.Sprintf(
					"杠杆按止损宽度封顶 symbol=%s confLev=%d→%d 止损宽=%.4f%% 夹=%.4f%%",
					symbol, lev, capLev, 100*d/px, 100*0.3/float64(capLev)))
				lev = capLev
			}
		}
	}
	// avail 与 equity 是两件事，别合并：
	//   avail  = 可用余额，只用于【可行性上限】（下方 avail×lev×0.95：能不能下得出去）；
	//   equity = 权益 = Wallet + Unrealized，用作【乘基】（下多大）。
	// 2026-09-21 用户直令：乘基由 avail 换成 equity。理由是 avail = 钱包 − 已占用保证金，
	// 于是同一个 pct 在空仓时给出大单、满仓时给出小单 —— 尺寸随占用度漂移，且每次换手都
	// 重新定一次基（平掉一仓→avail 变大→下一仓更大）。equity 不含占用度，只随净值变。
	avail := 0.0
	if v, err := usdmAvailableUSDT(bx, inst.OwnerID); err == nil && v > 0 {
		avail = v
	}
	equity := 0.0
	var equityErr error
	if bal, err := usdmWalletEquity(bx, inst.OwnerID); err == nil {
		equity = bal.Wallet + bal.Unrealized
	} else {
		equityErr = err
	}
	desiredNotional := amount * px
	if mode == "percent_balance" {
		// 乘基取不到时要说清是【取数失败】还是【权益真的<=0】—— 两者在日志里长得一样
		// 会让人去查错方向（旧代码只打一句"计算后<=0"）。
		if equity <= 0 {
			if equityErr != nil {
				emitStrategyLog(inst, "error", fmt.Sprintf("跳过开仓：读取权益失败 symbol=%s err=%v", symbol, equityErr))
			} else {
				emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：权益<=0 symbol=%s equity=%.4f", symbol, equity))
			}
			return 0, nil
		}
		pct := getNumber(inst.Config()["order_amount_pct"])
		if pct <= 0 {
			pct = amount / 100
		}
		if pct > 1 {
			pct = 1
		}
		mult, confSized := confSizingMultiplier(inst, confidence)
		if confSized {
			basePct := pct
			pct = pct * mult
			// 有效 pct 夹在 [0.05, 0.75]：与人工调参共用同一物理边界。
			if pct > 0.75 {
				pct = 0.75
			}
			if pct < 0.05 {
				pct = 0.05
			}
			emitStrategyLog(inst, "info", fmt.Sprintf("置信度动态仓位 symbol=%s conf=%.4f mult=%.2f pct=%.4f→%.4f", symbol, confidence, mult, basePct, pct))
		}
		maxInit := getNumber(inst.Config()["max_initial_margin_usdt"])
		initialMargin := 0.0
		if getBool(inst.Config()["order_pct_exclude_leverage"]) {
			// 保守开关：名义 = 权益×pct，不乘杠杆；杠杆只决定保证金占用（= 名义/杠杆）。
			notional := equity * pct
			if maxInit > 0 && notional > maxInit*float64(lev) {
				notional = maxInit * float64(lev)
			}
			if notional <= 0 {
				emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：按权益百分比计算后的名义<=0 symbol=%s equity=%.4f pct=%.4f", symbol, equity, pct))
				return 0, nil
			}
			initialMargin = notional / float64(lev)
			desiredNotional = notional
		} else {
			// 默认（2026-07-20 用户直令）：与币安百分比滑杆同语义——
			// pct 视为初始保证金占【权益】的比例，名义 = 保证金×杠杆，后端算出最终币数量传给交易所。
			initial := equity * pct
			if maxInit > 0 && initial > maxInit {
				initial = maxInit
			}
			if initial <= 0 {
				emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：按权益百分比计算后的初始保证金<=0 symbol=%s equity=%.4f pct=%.4f", symbol, equity, pct))
				return 0, nil
			}
			initialMargin = initial
			desiredNotional = initial * float64(lev)
		}
		// 初始保证金下限（默认 20U，2026-09-21 用户直令）：权益被吃到算出来的保证金
		// 不足下限时宁可不做——不靠"抬量到交易所最小值"把单子凑出来。
		minInit := getNumber(inst.Config()["min_initial_margin_usdt"])
		if minInit <= 0 {
			minInit = 20
		}
		if initialMargin < minInit {
			emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：初始保证金低于下限 symbol=%s margin=%.4f min=%.2f equity=%.2f pct=%.4f lev=%d", symbol, initialMargin, minInit, equity, pct, lev))
			return 0, nil
		}
		if confSized {
			// 缩量不得击穿单笔名义下限（默认 20U）：低置信度是少开，不是开出无意义的粉尘单。
			floorN := getNumber(inst.Config()["conf_sizing_min_notional_usdt"])
			if floorN <= 0 {
				floorN = 20
			}
			if desiredNotional < floorN {
				desiredNotional = floorN
			}
		}
	} else if mode == "notional" {
		desiredNotional = amount
	}
	if desiredNotional < minNotional {
		desiredNotional = minNotional
	}
	levChosen := lev
	finalNotional := 0.0
	for l := lev; l >= 1; l-- {
		availCap := 0.0
		if avail > 0 {
			availCap = avail * float64(l) * 0.95
		}
		remCap := 0.0
		if capN, err := bx.USDMMaxNotionalForLeverage(inst.OwnerID, symbol, l); err == nil && capN > 0 {
			if posAmt, _, markPx, e2 := bx.USDMPositionAmtCached(inst.OwnerID, symbol); e2 == nil && markPx > 0 && posAmt != 0 {
				curN := math.Abs(posAmt) * markPx
				rem := capN - curN
				if rem > 0 {
					remCap = rem * 0.98
				}
			} else {
				remCap = capN * 0.98
			}
		}
		maxNotional := 0.0
		if availCap > 0 && remCap > 0 {
			if availCap < remCap {
				maxNotional = availCap
			} else {
				maxNotional = remCap
			}
		} else if availCap > 0 {
			maxNotional = availCap
		} else if remCap > 0 {
			maxNotional = remCap
		}
		if maxNotional <= 0 {
			continue
		}
		if maxNotional >= desiredNotional {
			levChosen = l
			finalNotional = desiredNotional
			break
		}
		if maxNotional >= minNotional {
			levChosen = l
			finalNotional = maxNotional
			break
		}
	}
	if finalNotional <= 0 {
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：当前杠杆档位剩余额度不足 symbol=%s desired=%0.4f min=%0.4f lev=%d", symbol, desiredNotional, minNotional, lev))
		return 0, nil
	}
	if levChosen != lev {
		emitStrategyLog(inst, "info", fmt.Sprintf("自动调整杠杆：因档位上限约束 symbol=%s lev=%d->%d", symbol, lev, levChosen))
	}
	ensureExchangeLeverage(inst, bx, symbol, levChosen)
	amount = finalNotional / px
	amount = clampOrderAmount(inst, amount)
	if amount <= 0 {
		return 0, nil
	}
	if amount*px < minNotional {
		emitStrategyLog(inst, "info", fmt.Sprintf("跳过开仓：名义价值过小 symbol=%s notional=%0.4f min_notional=%0.2f", symbol, amount*px, minNotional))
		return 0, nil
	}
	return amount, nil
}

// ensureExchangeLeverage 把交易所侧 per-symbol 杠杆对齐到目标值。
// 交易所杠杆是粘性的：手动改过或新币默认档（常见 20X）会一直生效，旧逻辑只在
// 档位降级时调 SetLeverage，导致实际保证金占用/ROI 按 20X 而非 config 杠杆计算
// （实证：config=3 时 VANRY 持仓显示 20X、AKE 显示 10X）。
// 结果按进程周期缓存，每个 symbol×lev 只发一次 REST；失败不阻塞下单
// （按现有交易所杠杆继续），下一单自动重试。
func ensureExchangeLeverage(inst *StrategyInstance, bx *exchange.BinanceExchange, symbol string, lev int) {
	if inst == nil || bx == nil || lev < 1 {
		return
	}
	key := exchange.NormalizeSymbol(symbol)
	inst.orderMu.Lock()
	if inst.leverageSet == nil {
		inst.leverageSet = map[string]int{}
	}
	done := inst.leverageSet[key] == lev
	inst.orderMu.Unlock()
	if done {
		return
	}
	if err := bx.SetLeverage(inst.OwnerID, symbol, lev); err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("对齐交易所杠杆失败（按现有杠杆继续下单） symbol=%s lev=%d err=%v", symbol, lev, err))
		return
	}
	inst.orderMu.Lock()
	inst.leverageSet[key] = lev
	inst.orderMu.Unlock()
	emitStrategyLog(inst, "info", fmt.Sprintf("交易所杠杆已对齐 symbol=%s lev=%d", symbol, lev))
}

func normalizedTPSLPct(inst *StrategyInstance, key string) float64 {
	if inst == nil {
		return 0
	}
	pct := getNumber(inst.Config()[key])
	if pct > 1 {
		pct = pct / 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}

func hasEffectiveTPSL(inst *StrategyInstance, takeProfit float64, stopLoss float64) bool {
	if takeProfit > 0 && stopLoss > 0 {
		return true
	}
	return normalizedTPSLPct(inst, "take_profit_pct") > 0 && normalizedTPSLPct(inst, "stop_loss_pct") > 0
}

func resolveTPSLFromROI(inst *StrategyInstance, side string, entryPrice float64, takeProfit float64, stopLoss float64) (float64, float64) {
	if inst == nil || entryPrice <= 0 {
		return takeProfit, stopLoss
	}
	lev := int(getNumber(inst.Config()["leverage"]))
	if lev <= 0 {
		lev = 1
	}
	dir := strings.ToLower(strings.TrimSpace(side))
	if dir == "long" {
		dir = "buy"
	}
	if dir == "short" {
		dir = "sell"
	}
	if dir != "buy" && dir != "sell" {
		return takeProfit, stopLoss
	}

	tpPct := normalizedTPSLPct(inst, "take_profit_pct")
	slPct := normalizedTPSLPct(inst, "stop_loss_pct")
	if tpPct <= 0 && slPct <= 0 {
		return takeProfit, stopLoss
	}

	offset := func(pct float64) float64 {
		if pct <= 0 {
			return 0
		}
		return pct / float64(lev)
	}

	if off := offset(tpPct); off > 0 {
		if dir == "buy" {
			takeProfit = entryPrice * (1 + off)
		} else {
			takeProfit = entryPrice * (1 - off)
		}
	}
	if off := offset(slPct); off > 0 {
		if dir == "buy" {
			stopLoss = entryPrice * (1 - off)
		} else {
			stopLoss = entryPrice * (1 + off)
		}
	}
	return takeProfit, stopLoss
}

func resolveHungerMode(inst *StrategyInstance) (bool, time.Duration, float64, float64) {
	if inst == nil {
		return false, 0, 0, 0
	}
	enabled := true
	if _, ok := inst.Config()["hunger_mode_enabled"]; ok {
		enabled = getBool(inst.Config()["hunger_mode_enabled"])
	}
	afterMinutes := int(getNumber(inst.Config()["hunger_after_minutes"]))
	if afterMinutes <= 0 {
		afterMinutes = 30
	}

	derivePct := func(raw float64, fallbackKey string) float64 {
		if raw > 0 {
			return raw
		}
		base := normalizedTPSLPct(inst, fallbackKey)
		if base > 0 && base < 0.03 {
			return base
		}
		return 0.03
	}

	tpPct := derivePct(normalizedTPSLPct(inst, "hunger_take_profit_pct"), "take_profit_pct")
	slPct := derivePct(normalizedTPSLPct(inst, "hunger_stop_loss_pct"), "stop_loss_pct")
	return enabled, time.Duration(afterMinutes) * time.Minute, tpPct, slPct
}

// resolveMaxHoldTimeout returns the hard per-position max holding time. A
// position that has been open at least this long is force-closed regardless of
// PnL by the quick-trade monitor (unlike hunger mode, which only tightens TP/SL
// and still waits for a price condition). 0 (the default) disables it —
// force-closing is destructive, so it is strictly opt-in via config
// max_hold_minutes.
func resolveMaxHoldTimeout(inst *StrategyInstance) time.Duration {
	if inst == nil {
		return 0
	}
	minutes := int(getNumber(inst.Config()["max_hold_minutes"]))
	if minutes <= 0 {
		return 0
	}
	return time.Duration(minutes) * time.Minute
}

func (m *Manager) closeUSDMPosition(inst *StrategyInstance, bx *exchange.BinanceExchange, sym string) error {
	m.stopPositionTPStopMonitor(inst, sym)
	var pos models.StrategyPosition
	_ = database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", inst.OwnerID, inst.ID, sym, "open").
		Order("updated_at desc, id desc").
		First(&pos).Error
	fallbackMetrics := BuildTradeCloseMetricsFromPosition(&pos, pos.Amount, 0, time.Time{})
	if bx != nil && fallbackMetrics != nil && fallbackMetrics.EntryPrice <= 0 {
		if amt, entryPx, _, _, err := bx.USDMPositionInfo(inst.OwnerID, sym); err == nil {
			if entryPx > 0 {
				fallbackMetrics.EntryPrice = entryPx
			}
			if fallbackMetrics.RealizedNotional <= 0 && math.Abs(amt) > 0 && entryPx > 0 {
				fallbackMetrics.RealizedNotional = math.Abs(amt) * entryPx
			}
		}
	}
	if found, canceled, err := m.cancelLinkedTPSLOrders(inst.OwnerID, inst.ID, sym); err != nil {
		// 要撤的单已不存在(已成交/已撤,币安 -2011)属幂等成功,目的已达成,记 info 不记 error。
		lvl := "error"
		if exchange.IsBenignOrderMiss(err) {
			lvl = "info"
		}
		emitStrategyLog(inst, lvl, fmt.Sprintf("平仓前撤销关联止盈止损失败 symbol=%s canceled=%d found=%d err=%v", sym, canceled, found, err))
	} else if found > 0 {
		emitStrategyLog(inst, "info", fmt.Sprintf("平仓前撤销关联止盈止损完成 symbol=%s canceled=%d found=%d", sym, canceled, found))
	}
	if summary, err := bx.CancelUSDMAllSymbolOrdersDetailed(inst.OwnerID, sym); err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("平仓前撤销该交易对全部委托失败 symbol=%s err=%v", sym, err))
	} else {
		emitStrategyLog(inst, "info", fmt.Sprintf("平仓前撤单完成 symbol=%s 普通委托=%d/%d 条件委托=%d/%d", sym, summary.NormalCanceled, summary.NormalFound, summary.AlgoCanceled, summary.AlgoFound))
	}
	order, _, _, err := bx.ClosePositionOrder(sym, inst.OwnerID)
	if err != nil {
		return err
	}
	if order == nil {
		if summary, err := bx.CancelUSDMAllSymbolOrdersDetailed(inst.OwnerID, sym); err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("平仓后撤销该交易对全部委托失败 symbol=%s err=%v", sym, err))
		} else {
			emitStrategyLog(inst, "info", fmt.Sprintf("平仓后撤单完成 symbol=%s 普通委托=%d/%d 条件委托=%d/%d", sym, summary.NormalCanceled, summary.NormalFound, summary.AlgoCanceled, summary.AlgoFound))
		}
		return nil
	}
	database.DB.Create(&models.StrategyOrder{
		PositionID:      pos.ID,
		StrategyID:      inst.ID,
		StrategyName:    inst.Name,
		OwnerID:         inst.OwnerID,
		Exchange:        bx.GetName(),
		Symbol:          sym,
		Side:            strings.ToLower(order.Side),
		Purpose:         "close",
		OrderType:       "market",
		ClientOrderID:   order.ClientOrderID,
		ExchangeOrderID: order.ID,
		Status:          strings.ToLower(order.Status),
		RequestedQty:    order.Amount,
		Price:           0,
		ExecutedQty:     order.Amount,
		AvgPrice:        order.Price,
		RequestedAt:     time.Now(),
		UpdatedAt:       time.Now(),
	})
	inst.hub.BroadcastJSON(map[string]interface{}{"type": "order", "data": order})
	var closeMetrics *TradeCloseMetrics
	if strings.ToLower(order.Status) == "filled" {
		applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), sym, strings.ToLower(order.Side), order.Amount, order.Price, 0, 0, order.Timestamp, "close")
		fallbackMetrics = BuildTradeCloseMetricsFromPosition(&pos, order.Amount, order.Price, order.Timestamp)
		closeMetrics = MergeTradeCloseMetrics(loadTradeCloseMetrics(inst.OwnerID, inst.ID, sym), fallbackMetrics)
	}
	m.notifyTradeClosed(inst, sym, strings.ToLower(order.Side), order.Amount, order.Price, strings.ToLower(order.Status), "strategy_close", closeMetrics)
	go func(ownerID uint, symbol string) {
		if summary, err := bx.CancelUSDMAllSymbolOrdersDetailed(ownerID, symbol); err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("平仓后立即撤销该交易对全部委托失败 symbol=%s err=%v", symbol, err))
		} else {
			emitStrategyLog(inst, "info", fmt.Sprintf("平仓后立即撤单完成 symbol=%s 普通委托=%d/%d 条件委托=%d/%d", symbol, summary.NormalCanceled, summary.NormalFound, summary.AlgoCanceled, summary.AlgoFound))
		}
	}(inst.OwnerID, sym)
	go func(ownerID uint, symbol string) {
		deadline := time.Now().Add(45 * time.Second)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for time.Now().Before(deadline) {
			amt, _, _, e := bx.USDMPositionAmtCached(ownerID, symbol)
			if e == nil && amt == 0 {
				if summary, err := bx.CancelUSDMAllSymbolOrdersDetailed(ownerID, symbol); err != nil {
					emitStrategyLog(inst, "error", fmt.Sprintf("仓位归零后撤销该交易对全部委托失败 symbol=%s err=%v", symbol, err))
				} else {
					emitStrategyLog(inst, "info", fmt.Sprintf("仓位归零后撤单完成 symbol=%s 普通委托=%d/%d 条件委托=%d/%d", symbol, summary.NormalCanceled, summary.NormalFound, summary.AlgoCanceled, summary.AlgoFound))
				}
				return
			}
			<-ticker.C
		}
		// 45s 内没等到归零 ⇒ 有残仓。必须留痕：实测 VANA 残留 0.01 时这里是静默
		// 退出，死仓占住 max_concurrent_positions 槽位且无人发现。
		if amt, _, _, e := bx.USDMPositionAmt(ownerID, symbol); e == nil && amt != 0 {
			emitStrategyLog(inst, "error", fmt.Sprintf("平仓后 45s 仓位未归零 symbol=%s 残留数量=%v", symbol, amt))
		}
	}(inst.OwnerID, sym)
	return nil
}

func (m *Manager) closeSpotPosition(inst *StrategyInstance, sym string) error {
	m.stopPositionTPStopMonitor(inst, sym)
	if found, canceled, err := m.cancelLinkedTPSLOrders(inst.OwnerID, inst.ID, sym); err != nil {
		// 要撤的单已不存在(已成交/已撤,币安 -2011)属幂等成功,目的已达成,记 info 不记 error。
		lvl := "error"
		if exchange.IsBenignOrderMiss(err) {
			lvl = "info"
		}
		emitStrategyLog(inst, lvl, fmt.Sprintf("平仓前撤销关联止盈止损失败 symbol=%s canceled=%d found=%d err=%v", sym, canceled, found, err))
	} else if found > 0 {
		emitStrategyLog(inst, "info", fmt.Sprintf("平仓前撤销关联止盈止损完成 symbol=%s canceled=%d found=%d", sym, canceled, found))
	}
	if bx, ok := inst.exchange.(*exchange.BinanceExchange); ok && bx.Market() != "usdm" {
		if err := bx.CancelPrePositionOpenOrders(inst.OwnerID, sym); err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("平仓前撤销该交易对未成交委托失败 symbol=%s err=%v", sym, err))
		} else {
			emitStrategyLog(inst, "info", fmt.Sprintf("平仓前撤销该交易对未成交委托完成 symbol=%s", sym))
		}
	}
	var pos models.StrategyPosition
	if err := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", inst.OwnerID, inst.ID, sym, "open").
		Order("open_time desc").
		First(&pos).Error; err != nil {
		return nil
	}
	if pos.Amount <= 0 {
		return nil
	}
	fallbackMetrics := BuildTradeCloseMetricsFromPosition(&pos, pos.Amount, 0, time.Time{})
	clientOrderID := models.GenerateUUID()
	database.DB.Create(&models.StrategyOrder{
		PositionID:    pos.ID,
		StrategyID:    inst.ID,
		StrategyName:  inst.Name,
		OwnerID:       inst.OwnerID,
		Exchange:      inst.exchange.GetName(),
		Symbol:        sym,
		Side:          "sell",
		Purpose:       "close",
		OrderType:     "market",
		ClientOrderID: clientOrderID,
		Status:        "requested",
		RequestedQty:  pos.Amount,
		Price:         0,
		RequestedAt:   time.Now(),
		UpdatedAt:     time.Now(),
	})
	order, err := inst.exchange.PlaceOrder(inst.OwnerID, clientOrderID, sym, "sell", pos.Amount, 0)
	if err != nil {
		database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
			Updates(map[string]interface{}{"status": "failed", "updated_at": time.Now()})
		return err
	}
	database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
		Updates(map[string]interface{}{
			"exchange_order_id": order.ID,
			"status":            order.Status,
			"executed_qty":      order.Amount,
			"avg_price":         order.Price,
			"updated_at":        time.Now(),
		})
	inst.hub.BroadcastJSON(map[string]interface{}{"type": "order", "data": order})
	var closeMetrics *TradeCloseMetrics
	if strings.ToLower(order.Status) == "filled" {
		applyOrderFillToPosition(inst.hub, inst.OwnerID, inst.ID, inst.Name, inst.exchange.GetName(), sym, "sell", order.Amount, order.Price, 0, 0, order.Timestamp, "close")
		fallbackMetrics = BuildTradeCloseMetricsFromPosition(&pos, order.Amount, order.Price, order.Timestamp)
		closeMetrics = MergeTradeCloseMetrics(loadTradeCloseMetrics(inst.OwnerID, inst.ID, sym), fallbackMetrics)
	}
	m.notifyTradeClosed(inst, sym, "sell", order.Amount, order.Price, strings.ToLower(order.Status), "strategy_close", closeMetrics)
	return nil
}
