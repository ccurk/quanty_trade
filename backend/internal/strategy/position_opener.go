package strategy

import (
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

// 归属归开仓方,不归平仓方(台账 #31 的裁决)。
//
// 盈亏属于仓位,而仓位是开仓方建立的:开仓方决定了方向、规模、入场时机和承担的
// 风险敞口,平仓方只是终结它。按"最后下单的策略"归属会让业绩数字随执行顺序变化
// —— 同一个净仓谁碰巧先动谁就得分,那个字段就不再是"这条策略赚了多少"而是
// "谁手快",不能用来做任何决策。这正是幽灵收养的病根:把"最后动过它的人"
// 当成"它的主人"。
//
// 查不出开仓方时不许猜:PositionOpener 返回 ok=false,调用方必须拒绝收养。
const (
	// openerLookback 是往前找开仓腿的时间窗。注意 exchange.Position.OpenTime 在
	// USDM 上取的是币安 positionRisk 的 updateTime(binance.go:1763),即"最后一次
	// 变动"而不是"开仓时刻" —— 加过仓的净仓,这个时间戳会晚于真正的开仓腿。所以
	// 窗口取得宽,真正的判据是"方向一致 + 开仓方唯一"。
	openerLookback = 24 * time.Hour
	// openerForwardGrace 吸收交易所时间戳与本地 requested_at 之间的偏移。
	openerForwardGrace = 2 * time.Minute
	// openerScanLimit 给账户级扫描一个硬上限,免得订单表长大后拖垮 2s 对账循环。
	openerScanLimit = 2000
)

// PositionOpener picks, out of one symbol's candidate entry orders, the single
// strategy that opened a net position of the given direction.
//
// ok=false means "cannot tell" and is a first-class answer: either no entry leg
// matches, or two different strategies both opened in that direction into the
// same net position (a shared exchange account in one-way mode nets them into a
// single position that genuinely cannot be split). Callers must decline to adopt
// rather than pick one — guessing here is exactly the defect this replaces.
func PositionOpener(entries []models.StrategyOrder, direction string, openTime time.Time) (models.StrategyOrder, bool) {
	if openTime.IsZero() {
		return models.StrategyOrder{}, false
	}
	wantSide := "buy"
	if strings.EqualFold(strings.TrimSpace(direction), "short") {
		wantSide = "sell"
	}
	earliest := openTime.Add(-openerLookback)
	latest := openTime.Add(openerForwardGrace)

	var opener models.StrategyOrder
	found := false
	for _, ord := range entries {
		if !strings.EqualFold(strings.TrimSpace(ord.Side), wantSide) {
			continue
		}
		sid := strings.TrimSpace(ord.StrategyID)
		if sid == "" {
			continue
		}
		if ord.RequestedAt.Before(earliest) || ord.RequestedAt.After(latest) {
			continue
		}
		if !found {
			opener, found = ord, true
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(opener.StrategyID), sid) {
			// 两条策略都往同一个方向开过这个 symbol:净仓是它们合并出来的,
			// 拆不到某一条头上。返回"查不出来",让调用方标 unknown。
			return models.StrategyOrder{}, false
		}
	}
	return opener, found
}

// LoadRecentEntryOrdersBySymbol reads the recent filled entry legs of every
// owner, keyed by normalized symbol.
//
// Deliberately NOT scoped to one owner: on this deployment several app owners
// share a single Binance account, and the measured case is precisely the
// cross-owner one — owner1/Meme opens the net position while owner2's strategy
// is the one about to adopt it. Scoping the lookup per owner would hide the real
// opener and let the adoption go through anyway.
func LoadRecentEntryOrdersBySymbol() map[string][]models.StrategyOrder {
	out := map[string][]models.StrategyOrder{}
	if database.DB == nil {
		return out
	}
	var rows []models.StrategyOrder
	err := database.DB.
		Where("purpose = ? AND executed_qty > 0 AND requested_at >= ?", "entry", time.Now().Add(-openerLookback)).
		Order("requested_at desc").
		Limit(openerScanLimit).
		Find(&rows).Error
	if err != nil {
		return out
	}
	for _, ord := range rows {
		symKey := exchange.NormalizeSymbol(ord.Symbol)
		if symKey == "" {
			continue
		}
		out[symKey] = append(out[symKey], ord)
	}
	return out
}
