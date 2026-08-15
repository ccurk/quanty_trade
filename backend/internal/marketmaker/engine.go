package marketmaker

import (
	"context"
	"math"
	"sync"
	"time"

	"quanty_trade/internal/logger"
)

type Engine struct {
	cfg   Config
	feed  FeedSource
	execs map[string]ExecExchange
	stop  context.CancelFunc
}

// Start builds the feed + exec adapters from config and launches one worker per
// pair. Returns (nil, nil) when disabled. In ObserveOnly mode (the default and
// the only mode wired today) workers ONLY measure and log the exec-vs-feed edge —
// no orders are ever placed. This is the data-validation phase: run it, read the
// [mm-observe] logs, and decide which exchange/pair actually has edge before any
// live quoting is built.
func Start(cfg Config) (*Engine, error) {
	if !cfg.Enabled {
		logger.Infof("[mm] disabled (config)")
		return nil, nil
	}
	feed, err := NewFeed(cfg.Feed)
	if err != nil {
		return nil, err
	}
	execs := map[string]ExecExchange{}
	for _, ec := range cfg.Exec {
		ex, err := NewExec(ec)
		if err != nil {
			return nil, err
		}
		execs[ec.Name] = ex
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{cfg: cfg, feed: feed, execs: execs, stop: cancel}
	mode := "LIVE-QUOTE"
	if cfg.ObserveOnly {
		mode = "OBSERVE-ONLY"
	}
	logger.Infof("[mm] start feed=%s execs=%d pairs=%d mode=%s", cfg.Feed, len(execs), len(cfg.Pairs), mode)
	for _, p := range cfg.Pairs {
		ex, ok := execs[p.Exec]
		if !ok {
			logger.Warnf("[mm] pair %s: exec %q not in config, skipped", p.FeedSymbol, p.Exec)
			continue
		}
		go e.runPair(ctx, p, ex)
	}
	return e, nil
}

func (e *Engine) Stop() {
	if e != nil && e.stop != nil {
		e.stop()
	}
}

// runPair keeps the latest feed book (WS) and, each refresh, compares the exec
// exchange's book against the reference mid and logs the capturable edge in bps.
func (e *Engine) runPair(ctx context.Context, p PairConfig, ex ExecExchange) {
	var mu sync.Mutex
	var latest BookTicker
	stop, err := e.feed.SubscribeBookTicker(p.FeedSymbol, func(bt BookTicker) {
		mu.Lock()
		latest = bt
		mu.Unlock()
	})
	if err != nil {
		logger.Warnf("[mm] pair %s: feed subscribe failed: %v", p.FeedSymbol, err)
		return
	}
	defer stop()

	tk := time.NewTicker(time.Duration(p.refresh()) * time.Millisecond)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		mu.Lock()
		ref := latest
		mu.Unlock()
		if ref.Mid() <= 0 {
			continue
		}
		eb, err := ex.FetchBookTicker(p.ExecSymbol)
		if err != nil || eb.Mid() <= 0 {
			continue
		}
		// 可捕获边(相对参考中价,bps):
		//   buyEdge  = 在执行所【买】(其 bid 下方)相对参考中价还有多少空间 → 低买潜力
		//   sellEdge = 在执行所【卖】(其 ask 上方)相对参考中价还有多少空间 → 高卖潜力
		// 两边都要扣执行所手续费才是净边。这些数就是"哪个所/哪个对机会大"的实测依据。
		refMid := ref.Mid()
		buyEdge := (refMid - eb.BidPx) / refMid * 10000
		sellEdge := (eb.AskPx - refMid) / refMid * 10000
		logger.Infof("[mm-observe] %s@%s ref=%s refMid=%.8f execMid=%.8f midDiff=%.1fbps execSpread=%.1fbps buyEdge=%.1fbps sellEdge=%.1fbps",
			p.ExecSymbol, ex.Name(), e.feed.Name(), refMid, eb.Mid(),
			(eb.Mid()-refMid)/refMid*10000, eb.SpreadBps(), buyEdge, sellEdge)

		if !e.cfg.ObserveOnly {
			// 参考盘口过期就撤掉两边报价,绝不按陈旧参考挂单(防参考断流时裸报价)。
			if time.Since(ref.Ts) > staleAfter(p) {
				e.cancelAll(ex, p.ExecSymbol)
				continue
			}
			e.quote(p, ex, ref)
		}
	}
}

func staleAfter(p PairConfig) time.Duration {
	d := 3 * time.Duration(p.refresh()) * time.Millisecond
	if d < 3*time.Second {
		d = 3 * time.Second
	}
	return d
}

// quote maintains one post-only bid + one post-only ask around the reference mid,
// inventory-capped and fail-safe: any read error (filters/balances/open-orders)
// aborts this cycle rather than quoting blind. Cancel-replace only when the target
// moved more than a tick, to avoid thrashing.
func (e *Engine) quote(p PairConfig, ex ExecExchange, ref BookTicker) {
	filt, err := ex.SymbolFilter(p.ExecSymbol)
	if err != nil {
		logger.Warnf("[mm] %s@%s filter 读取失败,本轮不报价: %v", p.ExecSymbol, ex.Name(), err)
		return
	}
	refMid := ref.Mid()
	half := p.SpreadBps / 10000.0
	bidPx := roundToTick(refMid*(1-half), filt.TickSize, false) // 买价向下取整到 tick
	askPx := roundToTick(refMid*(1+half), filt.TickSize, true)  // 卖价向上取整到 tick
	if bidPx <= 0 || askPx <= 0 || askPx <= bidPx {
		return
	}

	bals, err := ex.Balances()
	if err != nil {
		logger.Warnf("[mm] %s@%s 余额读取失败,撤单避险: %v", p.ExecSymbol, ex.Name(), err)
		e.cancelAll(ex, p.ExecSymbol)
		return
	}
	baseHeld := bals[filt.BaseAsset]

	bidQty := roundToStep(p.OrderQty, filt.StepSize)
	askQty := roundToStep(minf(p.OrderQty, baseHeld), filt.StepSize) // 现货只能卖出已持有的量

	// 库存闸:持仓已达上限不再买;无库存不挂卖;两边都要过最小名义额。
	wantBid := baseHeld < p.MaxPosition && bidQty > 0 && bidPx*bidQty >= filt.MinNotional
	wantAsk := askQty > 0 && askPx*askQty >= filt.MinNotional

	orders, err := ex.OpenOrders(p.ExecSymbol)
	if err != nil {
		logger.Warnf("[mm] %s@%s openOrders 读取失败,本轮不动单: %v", p.ExecSymbol, ex.Name(), err)
		return
	}
	var curBid, curAsk *OpenOrder
	for i := range orders {
		switch {
		case orders[i].Side == "BUY" && curBid == nil:
			curBid = &orders[i]
		case orders[i].Side == "SELL" && curAsk == nil:
			curAsk = &orders[i]
		default:
			_ = ex.CancelOrder(p.ExecSymbol, orders[i].ID) // 同侧多余挂单清掉,每侧只留一张
		}
	}
	tol := filt.TickSize
	e.reconcileSide(ex, p.ExecSymbol, "BUY", bidPx, bidQty, wantBid, curBid, tol, filt.StepSize)
	e.reconcileSide(ex, p.ExecSymbol, "SELL", askPx, askQty, wantAsk, curAsk, tol, filt.StepSize)
}

// reconcileSide keeps at most one resting order on a side matching the target.
func (e *Engine) reconcileSide(ex ExecExchange, symbol, side string, px, qty float64, want bool, cur *OpenOrder, priceTol, stepTol float64) {
	if !want {
		if cur != nil {
			_ = ex.CancelOrder(symbol, cur.ID)
		}
		return
	}
	if cur != nil && absf(cur.Price-px) <= priceTol && absf(cur.Qty-qty) <= stepTol/2+1e-12 {
		return // 已有合适的挂单,不动
	}
	if cur != nil {
		if err := ex.CancelOrder(symbol, cur.ID); err != nil {
			logger.Warnf("[mm] %s@%s cancel %s failed: %v", symbol, ex.Name(), side, err)
			return
		}
	}
	if id, err := ex.PlaceLimit(symbol, side, px, qty, "GTC", true); err != nil {
		logger.Warnf("[mm] %s@%s place %s %.8f x %.8f failed: %v", symbol, ex.Name(), side, px, qty, err)
	} else {
		logger.Infof("[mm-quote] %s@%s %s %.8f x %.8f id=%s", symbol, ex.Name(), side, px, qty, id)
	}
}

func (e *Engine) cancelAll(ex ExecExchange, symbol string) {
	orders, err := ex.OpenOrders(symbol)
	if err != nil {
		return
	}
	for _, o := range orders {
		_ = ex.CancelOrder(symbol, o.ID)
	}
}

func roundToTick(px, tick float64, up bool) float64 {
	if tick <= 0 {
		return px
	}
	n := px / tick
	if up {
		return math.Ceil(n) * tick
	}
	return math.Floor(n) * tick
}

func roundToStep(qty, step float64) float64 {
	if step <= 0 {
		return qty
	}
	return math.Floor(qty/step) * step
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func absf(a float64) float64 {
	if a < 0 {
		return -a
	}
	return a
}
