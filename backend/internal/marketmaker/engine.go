package marketmaker

import (
	"context"
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
			// TODO(下一阶段): 实盘双边挂/撤报价。需要库存管理、撤单/改单、风控与去重,
			// 且必须先用上面 observe 的实测边验证有净利后再接 —— 现在故意不下单。
			logger.Warnf("[mm] live-quote 尚未实现,%s 继续 observe-only", p.ExecSymbol)
		}
	}
}
