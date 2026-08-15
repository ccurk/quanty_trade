package strategy

import (
	"fmt"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

// 赢家金字塔加仓（默认关闭，per-strategy config 显式开启）。
//
// 硬规则（代码级，不可配置）：只允许对【浮盈中】的仓位同向加仓一次上限
// pyramid_max_adds 次；roi<=0 一律拒绝——摊平/马丁在本引擎是禁区，这条
// 判断永远先于任何 config。加仓后止损按新均价重锚（棘轮方向，只紧不松），
// 止盈保持原绝对价不动（合并仓位更早锁利，偏保守）。
//
// config 键（策略级）：
//   pyramid_enabled      bool    默认 false
//   pyramid_trigger_roi  float   触发加仓的最小 roi%（默认 6.0，=价格变动%×杠杆）
//   pyramid_add_frac     float   加仓量 = 原仓位数量 × frac（默认 0.5）
//   pyramid_max_adds     int     每仓最多加几次（默认 1）
//
// 加仓计数在内存（uid|symbol 键，仓位关闭/消失时清除）。重启丢计数的最坏
// 情形＝对仍在盈利线上的仓位多加一次，且受 max_adds 与 roi 门再约束，有界。

const (
	pyramidDefaultTriggerROI = 6.0
	pyramidDefaultAddFrac    = 0.5
	pyramidDefaultMaxAdds    = 1
)

func pyramidKey(uid uint, symbol string) string {
	return fmt.Sprintf("%d|%s", uid, strings.ToUpper(symbol))
}

// prunePyramidCounters 清掉已无持仓的计数（watched = 当前在持 symbol 大写集）。
func (m *Manager) prunePyramidCounters(watched map[string]struct{}) {
	m.pyramidMu.Lock()
	defer m.pyramidMu.Unlock()
	for k := range m.pyramidAdds {
		parts := strings.SplitN(k, "|", 2)
		if len(parts) != 2 {
			delete(m.pyramidAdds, k)
			continue
		}
		if _, ok := watched[parts[1]]; !ok {
			delete(m.pyramidAdds, k)
		}
	}
}

// maybePyramid 在 5s 守护的逐仓循环里被调用（决策与执行同路径，不走 WS 快扫，
// 加仓不需要亚秒级）。
func (m *Manager) maybePyramid(inst *StrategyInstance, uid uint, row models.StrategyPosition, pos exchange.Position, side string, currentPrice float64) {
	if inst == nil || !getBool(inst.Config["pyramid_enabled"]) {
		return
	}
	roi := pos.ReturnRate
	if roi <= 0 {
		return // 硬规则：亏损仓永不加仓（马丁禁区），先于一切配置判断
	}
	trigger := getNumber(inst.Config["pyramid_trigger_roi"])
	if trigger <= 0 {
		trigger = pyramidDefaultTriggerROI
	}
	if roi < trigger {
		return
	}
	maxAdds := int(getNumber(inst.Config["pyramid_max_adds"]))
	if maxAdds <= 0 {
		maxAdds = pyramidDefaultMaxAdds
	}
	frac := getNumber(inst.Config["pyramid_add_frac"])
	if frac <= 0 || frac > 1 {
		frac = pyramidDefaultAddFrac
	}
	key := pyramidKey(uid, row.Symbol)
	m.pyramidMu.Lock()
	done := m.pyramidAdds[key]
	if done >= maxAdds {
		m.pyramidMu.Unlock()
		return
	}
	m.pyramidAdds[key] = done + 1 // 先占位防并发双加；下单失败再回滚
	m.pyramidMu.Unlock()

	qty := row.Amount * frac
	if qty <= 0 || currentPrice <= 0 {
		m.rollbackPyramidCount(key)
		return
	}
	cid := fmt.Sprintf("pyr%s", models.GenerateUUID()[:18])
	ord, err := m.exchange.PlaceOrder(uid, cid, row.Symbol, side, qty, 0)
	if err != nil {
		m.rollbackPyramidCount(key)
		emitStrategyLog(inst, "error", fmt.Sprintf("金字塔加仓下单失败 symbol=%s qty=%0.6f err=%v", row.Symbol, qty, err))
		return
	}

	// 用【真实成交量/价】回写,而非请求量+标记价——PlaceOrder 可能把市价单向上取整到
	// min-notional,用请求量会低估真实仓位、污染 SL 重锚与后续 frac 加仓基数(CR P1)。
	fillQty := qty
	if ord.Amount > 0 {
		fillQty = ord.Amount
	}
	fillPx := currentPrice
	if ord.Price > 0 {
		fillPx = ord.Price
	}
	oldAmt, oldAvg := row.Amount, row.AvgPrice
	newAmt := oldAmt + fillQty
	newAvg := (oldAvg*oldAmt + fillPx*fillQty) / newAmt
	newSL := row.StopLoss
	if newSL > 0 && oldAvg > 0 {
		newSL = row.StopLoss + (newAvg - oldAvg)
	}
	_ = database.DB.Model(&models.StrategyPosition{}).Where("id = ?", row.ID).
		Updates(map[string]interface{}{"amount": newAmt, "avg_price": newAvg, "updated_at": time.Now()}).Error
	row.Amount, row.AvgPrice = newAmt, newAvg
	if newSL > 0 {
		m.applyStopMove(inst, uid, row, row.TakeProfit, newSL)
	}
	m.emitExitAudit(inst, row.Symbol, "pyramid_add", roi, pos.UnrealizedPnL,
		fmt.Sprintf("adds=%d/%d qty=%0.6f avg=%0.8f->%0.8f sl=%0.8f->%0.8f", done+1, maxAdds, qty, oldAvg, newAvg, row.StopLoss, newSL))
	emitStrategyLog(inst, "info", fmt.Sprintf("金字塔加仓 symbol=%s side=%s roi=%0.2f%% qty=%0.6f 新均价=%0.8f 加仓次数=%d/%d", row.Symbol, side, roi, qty, newAvg, done+1, maxAdds))
}

func (m *Manager) rollbackPyramidCount(key string) {
	m.pyramidMu.Lock()
	if m.pyramidAdds[key] > 0 {
		m.pyramidAdds[key]--
	}
	m.pyramidMu.Unlock()
}
