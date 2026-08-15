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
	// Balances returns free (available) balances keyed by asset symbol.
	Balances() (map[string]float64, error)
}
