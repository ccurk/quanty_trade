package marketmaker

import (
	"context"
	"math"
	"strings"
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
	setRunning(true)
	for _, p := range cfg.Pairs {
		ex, ok := execs[p.Exec]
		if !ok {
			logger.Warnf("[mm] pair %s: exec %q not in config, skipped", p.FeedSymbol, p.Exec)
			continue
		}
		go e.runPair(ctx, p, ex)
	}
	if !cfg.ObserveOnly {
		go e.runDeadMansSwitch(ctx) // 交易所侧死人开关(唯一能扛进程崩溃的兜底)
	}
	return e, nil
}

func (e *Engine) Stop() {
	setRunning(false)
	if e == nil {
		return
	}
	// 优雅关闭:撤掉所有残留挂单,绝不把裸单留在交易所(否则重启/崩溃后被人慢慢吃)。
	if !e.cfg.ObserveOnly {
		for _, p := range e.cfg.Pairs {
			if ex, ok := e.execs[p.Exec]; ok {
				e.cancelAll(ex, p.ExecSymbol)
			}
		}
		logger.Infof("[mm] Stop: 已撤所有挂单")
	}
	if e.stop != nil {
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

	// 启动先撤掉该 symbol 的所有残留挂单(上次运行/崩溃留下的孤儿单),报价前先 reconcile 干净。
	if !e.cfg.ObserveOnly {
		e.cancelAll(ex, p.ExecSymbol)
		logger.Infof("[mm] %s@%s 启动清残留挂单", p.ExecSymbol, ex.Name())
	}
	var devSince time.Time // exec-vs-ref 中价持续偏离的起始时刻(长时间偏移撤单用)

	// 成交/PnL 追踪 + 单日止损(仅 gate live;账户是用户自己的 gate key,my_trades=本引擎成交)。
	var tracker *pnlTracker
	var baseAsset string
	if !e.cfg.ObserveOnly && strings.EqualFold(ex.Name(), "gate") {
		tracker = newPnLTracker()
		baseAsset = strings.SplitN(p.ExecSymbol, "_", 2)[0]
	}
	var lastPoll, haltUntil time.Time

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
		feeBps, feeLive := MakerFeeBps(ex.Name(), p.ExecSymbol)
		bestGross := buyEdge
		if sellEdge > bestGross {
			bestGross = sellEdge
		}
		recordObserve(ObserveRow{
			Exchange: ex.Name(), Symbol: p.ExecSymbol, RefMid: refMid, ExecMid: eb.Mid(),
			ExecSpreadBps: eb.SpreadBps(), BuyEdgeBps: buyEdge, SellEdgeBps: sellEdge, FeeBps: feeBps, FeeLive: feeLive, NetBestEdgeBps: bestGross - 2*feeBps, Ts: time.Now(),
		})

		if !e.cfg.ObserveOnly {
			// 单日止损熔断中:停报价至次日 UTC。
			if !haltUntil.IsZero() && time.Now().Before(haltUntil) {
				continue
			}
			// 参考盘口过期就撤掉两边报价,绝不按陈旧参考挂单(防参考断流时裸报价)。
			if time.Since(ref.Ts) > staleAfter(p) {
				e.cancelAll(ex, p.ExecSymbol)
				continue
			}
			// 长时间偏移:exec 与 ref 中价持续偏离超阈值 → 撤单暂停(可能真错价/数据问题,
			// 按错价裸挂会被逆向吃穿);回到阈值内才恢复报价。
			midDiffBps := math.Abs(eb.Mid()-refMid) / refMid * 10000
			if midDiffBps > maxLiveDivergenceBps {
				if devSince.IsZero() {
					devSince = time.Now()
				}
				if time.Since(devSince) > maxDeviationDuration {
					e.cancelAll(ex, p.ExecSymbol)
					continue
				}
			} else {
				devSince = time.Time{}
			}
			// 成交/PnL 追踪 + 单日止损(每 ~10s 拉一次成交)。
			if tracker != nil && time.Since(lastPoll) > 10*time.Second {
				lastPoll = time.Now()
				if fills, ferr := gateMyTrades(p.ExecSymbol, 100); ferr == nil {
					tracker.apply(fills, baseAsset)
					pnl := tracker.mtmPnL(eb.Mid())
					recordMMPnL(ex.Name(), p.ExecSymbol, pnl)
					if e.cfg.MaxDailyLossUSD > 0 && pnl < -e.cfg.MaxDailyLossUSD {
						e.cancelAll(ex, p.ExecSymbol)
						haltUntil = nextUTCMidnight()
						logger.Errorf("[mm] %s@%s 触发单日止损 PnL=%.2f < -%.2f → 停报价至次日", p.ExecSymbol, ex.Name(), pnl, e.cfg.MaxDailyLossUSD)
						continue
					}
				}
			}
			e.quote(p, ex, ref, eb)
		}
	}
}

const (
	// maxLiveDivergenceBps: exec 与 ref 中价偏离超过此值(1%)视为异常。
	maxLiveDivergenceBps = 100
	// maxDeviationDuration: 持续偏离超此时长即撤单暂停,避免按错价/陈价裸挂被逆向吃穿。
	maxDeviationDuration = 30 * time.Second
	// requoteToleranceFrac: 目标价与现有挂单价的偏移小于"半价差×此比例"时不撤挂重下。
	// 否则参考中价每秒微抖(几 bps)就撤挂追价,纯烧下单/撤单限流且无收益。
	// 0.25 = 目标移动超过 1/4 半价差才重挂;价差 10bps 时带宽≈2.5bps。
	requoteToleranceFrac = 0.25
	// inventorySkewFrac: 库存偏移强度。报价中枢下压量 = 半价差 × 此值 × (持仓/上限)。
	// 持仓越满,卖价越贴中价(易成交、卸货)、买价越远(少接货),把仓位往中性拽回,
	// 对冲趋势里单向堆货。1.0=满仓时卖价≈中价、买价≈2 个价差之下;0=关闭偏移。
	inventorySkewFrac = 1.0
)

func staleAfter(p PairConfig) time.Duration {
	d := 3 * time.Duration(p.refresh()) * time.Millisecond
	if d < 3*time.Second {
		d = 3 * time.Second
	}
	return d
}

// quote maintains one post-only bid + one post-only ask around an inventory-skewed
// reservation center (the more base held, the lower the center → lean to sell down),
// clamped to never cross the exec book (post-only would reject), inventory-capped and
// fail-safe: any read error (filters/balances/open-orders) aborts this cycle rather
// than quoting blind. Cancel-replace only when the target moved more than the requote
// band (a fraction of the half-spread), to avoid thrashing.
func (e *Engine) quote(p PairConfig, ex ExecExchange, ref, eb BookTicker) {
	filt, err := ex.SymbolFilter(p.ExecSymbol)
	if err != nil {
		logger.Warnf("[mm] %s@%s filter 读取失败,本轮不报价: %v", p.ExecSymbol, ex.Name(), err)
		return
	}
	refMid := ref.Mid()
	half := p.SpreadBps / 10000.0

	// 先读挂单:卖单里锁着的 SOL 仍是你的持仓,必须先拿到它。否则"挂卖→可用余额变少→
	// 下轮卖量算小→判定量不符→撤挂重下"会每秒死循环(thrash)。
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

	bals, err := ex.Balances()
	if err != nil {
		logger.Warnf("[mm] %s@%s 余额读取失败,撤单避险: %v", p.ExecSymbol, ex.Name(), err)
		e.cancelAll(ex, p.ExecSymbol)
		return
	}
	// 持仓 = 可用余额 + 自己卖单里锁着的量(去掉这一项就会 thrash)。
	baseHeld := bals[filt.BaseAsset]
	if curAsk != nil {
		baseHeld += curAsk.Qty
	}

	// 库存偏移:持仓越满,报价中枢越往下压 → 卖价更贴中价(易成交、卸货)、买价更远(少接货),
	// 把仓位往中性(现货目标持仓=0)拽回,对冲趋势里单向堆货。空仓时退化成对称报价。
	invRatio := 0.0
	if p.MaxPosition > 0 {
		invRatio = baseHeld / p.MaxPosition
	}
	bidPx, askPx := skewedQuote(refMid, half, invRatio, inventorySkewFrac, filt.TickSize)
	// 吃满执行所盘口价差:执行所卖一比"公允+spread"更贵时,把卖单顶到其卖一下方 1 tick
	// (捕获整段溢价,而不是按固定 spread 自己砍价贱卖);买一更便宜时同理下探。同时严格
	// 留在盘口内 → post-only 不会被 POC_FILL_IMMEDIATELY 拒。这是"面板正、成交负"的正解:
	// 面板量的是盘口既有价差,过去我们却挂在它里面把便宜货让了出去。
	bidPx, askPx = rideToBook(bidPx, askPx, eb.BidPx, eb.AskPx, filt.TickSize)
	if bidPx <= 0 || askPx <= 0 || askPx <= bidPx {
		return
	}

	// 买量钳到"上限−持仓",防止在接近上限时又买满一整单冲破 cap(order_qty≈½cap 时最多溢出
	// 50%);卖量最多卖出已持有的量(现货)。
	bidQty := roundToStep(minf(p.OrderQty, p.MaxPosition-baseHeld), filt.StepSize)
	askQty := roundToStep(minf(p.OrderQty, baseHeld), filt.StepSize)

	// 库存闸:总持仓达上限不再买;无库存不挂卖;两边都要过最小名义额。
	wantBid := baseHeld < p.MaxPosition && bidQty > 0 && bidPx*bidQty >= filt.MinNotional
	wantAsk := askQty > 0 && askPx*askQty >= filt.MinNotional

	// 只有目标价相对现有挂单移动超过"半价差×requoteToleranceFrac"才撤挂重下;
	// 至少 1 个 tick。避免参考中价每秒微抖就撤挂追价(白烧限流、无收益)。
	tol := filt.TickSize
	if band := refMid * half * requoteToleranceFrac; band > tol {
		tol = band
	}
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

// rideToBook widens post-only quotes out to the exec venue's own book so we capture
// its full spread instead of undercutting it: if the venue ask sits above our target
// ask, ride up to venue_ask−tick (sell into the venue's richer offer, not below it);
// if the venue bid sits below our target bid, ride down to venue_bid+tick. Both moves
// go AWAY from aggression → strictly better fill price, never worse. Then clamp inside
// the book so post-only never crosses/takes. Zero/missing book sides skip that step.
//
// This is the fix for "panel shows +bps but fills are −bps": the panel measured the
// venue's existing book spread; a fixed spread_bps quote sat INSIDE it and gave the
// edge away. Riding to the book captures the spread the panel actually measured.
func rideToBook(bidPx, askPx, ebBid, ebAsk, tick float64) (bid, ask float64) {
	bid, ask = bidPx, askPx
	if ebAsk > 0 && ebAsk-tick > ask {
		ask = ebAsk - tick // 执行所卖一更贵 → 顶上去吃溢价
	}
	if ebBid > 0 && ebBid+tick < bid {
		bid = ebBid + tick // 执行所买一更便宜 → 下探占便宜
	}
	// post-only 安全:严格留在盘口内,绝不穿价成 taker。
	if ebBid > 0 && ask <= ebBid {
		ask = ebBid + tick
	}
	if ebAsk > 0 && bid >= ebAsk {
		bid = ebAsk - tick
	}
	return
}

// skewedQuote prices a post-only bid/ask around an inventory-skewed reservation
// center: center = refMid*(1 - half*skewFrac*invRatio), invRatio clamped to [0,1].
// More base held (higher invRatio) lowers the center → ask nears mid (lean to sell
// down), bid recedes (buy less). invRatio=0 → symmetric quotes around refMid.
func skewedQuote(refMid, half, invRatio, skewFrac, tick float64) (bid, ask float64) {
	if invRatio < 0 {
		invRatio = 0
	} else if invRatio > 1 {
		invRatio = 1
	}
	center := refMid * (1 - half*skewFrac*invRatio)
	bid = roundToTick(center*(1-half), tick, false) // 买价向下取整到 tick
	ask = roundToTick(center*(1+half), tick, true)  // 卖价向上取整到 tick
	return
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
