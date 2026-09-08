// Package marketmaker implements cross-exchange market making: watch a reference
// price on a FEED exchange (e.g. Binance) and quote/trade on a second-tier
// EXECUTION exchange (e.g. Coins.ph, MEXC). Both the feed source and the
// execution exchange are pluggable and chosen by config, so new exchanges are
// added by implementing one of the two small interfaces below and registering it.
//
// This module is deliberately decoupled from internal/exchange (the strategy
// engine's futures-oriented Exchange interface): market making needs top-of-book
// quoting/cancel/balances, not positions/TP-SL/candles.
package marketmaker

import "time"

// BookTicker is best bid/ask top-of-book for one symbol.
type BookTicker struct {
	Symbol string
	BidPx  float64
	BidQty float64
	AskPx  float64
	AskQty float64
	Ts     time.Time
}

// Mid returns the mid price; 0 if either side is missing.
func (b BookTicker) Mid() float64 {
	if b.BidPx <= 0 || b.AskPx <= 0 {
		return 0
	}
	return (b.BidPx + b.AskPx) / 2
}

// SpreadBps returns the book's own bid/ask spread in basis points; 0 if unknown.
func (b BookTicker) SpreadBps() float64 {
	m := b.Mid()
	if m <= 0 {
		return 0
	}
	return (b.AskPx - b.BidPx) / m * 10000
}

// OpenOrder is a resting order on the execution exchange.
type OpenOrder struct {
	ID    string
	Side  string // "BUY" / "SELL"
	Price float64
	Qty   float64
}

// SymbolFilter carries the exchange rounding rules + asset names for a symbol.
type SymbolFilter struct {
	BaseAsset   string  // e.g. BTC (the inventory asset)
	QuoteAsset  string  // e.g. USDT
	TickSize    float64 // price increment
	StepSize    float64 // quantity increment
	MinNotional float64 // minimum price*qty
}

// FeedSource is the REFERENCE market-data exchange being monitored (read-only).
type FeedSource interface {
	Name() string
	// SubscribeBookTicker streams best bid/ask for symbol until the returned stop
	// func is called. Implementations reconnect internally.
	SubscribeBookTicker(symbol string, cb func(BookTicker)) (stop func(), err error)
	// FetchBookTicker is a one-shot REST fallback / observer read.
	FetchBookTicker(symbol string) (BookTicker, error)
}

// ExecExchange is the second-tier exchange where quotes/orders are placed.
// Every method is credentialed except FetchBookTicker/SymbolFilter (public).
type ExecExchange interface {
	Name() string
	FetchBookTicker(symbol string) (BookTicker, error)
	SymbolFilter(symbol string) (SymbolFilter, error)
	// PlaceLimit places a LIMIT order. tif is GTC/IOC/FOK; postOnly requests a
	// maker-only order where supported. Returns the exchange order id.
	PlaceLimit(symbol, side string, price, qty float64, tif string, postOnly bool) (orderID string, err error)
	CancelOrder(symbol, orderID string) error
	OpenOrders(symbol string) ([]OpenOrder, error)
	// Balances returns free (available) balances keyed by asset symbol. On a
	// derivatives venue this is the signed POSITION per asset (negative = short).
	Balances() (map[string]float64, error)
	// SupportsShort reports whether the venue lets us sell without holding the
	// asset first (perps/margin). Spot venues return false: there the engine may
	// only sell inventory it already owns, which forces one-sided quoting whenever
	// the venue trades at a premium — the structural leak measured on gate.
	//
	// 它是【必要不充分】条件:引擎放开卖侧还要求 pair 配了 allow_short,且适配器
	// 实现了下面的 PerpRiskControls。判据只有一处 —— engine.go shortSideEnabled。
	SupportsShort() bool
}

// PerpRiskControls 是"允许在这个场馆挂裸卖单"的前置能力集合。
//
// 为什么它单独一个接口、而不是塞进 ExecExchange:现货适配器既不能也不该实现它,
// 而引擎需要在【运行时】判断某个适配器到底有没有这套东西 —— 类型断言就是那个探针。
// 这不是可选装饰,是闸门本身:断言失败 = 拒绝启动(engine.go shortSideBlockers)。
//
// 四个方法一一对应 state/strategy/gate-futures-mm-assessment-2026-09-08.md §4
// 里那四条"会把永续做市做到强平"的缺口:
//
//	ClosePosition   ① 单日止损只撤单不平仓。现货安全(拿着币而已),永续等于亏到
//	                  阈值后把腿绑起来、带着杠杆仓位裸奔到次日 UTC 零点。
//	MarginRatio     ③ 全包无保证金率概念。MaxPosition 是基础币/张数口径,和维持
//	                  保证金没有关系,拦不住强平。
//	SetLeverage     ⑥ 代码里从没设置过杠杆,开仓会落到账号默认档。
//	FundingPaidUSD  ⑤ 资金费(gate 实测 funding_interval=28800 秒)完全不进 PnL,
//	                  会重演"面板正、实际负"。
//
// 今天【没有任何适配器实现它】(hyperliquid.go 也没有),所以卖侧一定被拒 ——
// 这是有意的 fail-closed:写永续适配器的人把这四个方法实现出来,闸自己就开。
//
// 注意它挡不住"实现成空壳":四个方法返回 nil 也能通过断言。它挡的是【忘了做】,
// 不是【假装做了】。另外两条缺口是引擎级的,断言看不见,补齐前也别开:
//   - ② deadman.go 的死人开关只撤单不平仓,进程崩溃后持仓继续裸露;
//   - ④ 止损检查最快 10s 一次,且整段包在 `if err == nil` 里,接口报错时静默跳过。
type PerpRiskControls interface {
	// ClosePosition 用 reduce-only 单把该 symbol 的持仓平掉(幂等,已平返回 nil)。
	ClosePosition(symbol string) error
	// MarginRatio 返回当前保证金率,用于强平前刹车。
	MarginRatio(symbol string) (float64, error)
	// SetLeverage 显式设置杠杆倍数,不依赖账号默认档。
	SetLeverage(symbol string, x float64) error
	// FundingPaidUSD 返回累计已付/已收资金费(USD,付出为正),用于计入 PnL。
	FundingPaidUSD(symbol string) (float64, error)
}
