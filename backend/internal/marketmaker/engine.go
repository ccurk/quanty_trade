package marketmaker

import (
	"context"
	"fmt"
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
	// 做空侧闸门。放在建 ctx / setRunning / 起 goroutine 之前:拒绝时整个模块一个
	// 协程都没起、running 仍为 false、一张单都没下。
	//
	// 为什么是"拒绝启动"而不是"忽略该配置退回单边":静默退回在永续上最坏的形态是
	// —— 人以为双边在跑、实际只在单边堆多头,再撞上单日止损【只撤单不平仓】那条
	// (见下面 MaxDailyLossUSD 分支),就是带杠杆裸奔到次日。宁可后端起不来。
	// 调用方 app/runtime.go:54 把这里的 error 打成 ERROR 日志(并进 Lark 告警),
	// 进程本身不退,其它模块照常跑。
	for _, p := range cfg.Pairs {
		if !p.AllowShort {
			continue
		}
		ex, ok := execs[p.Exec]
		if !ok {
			continue // exec 名字都对不上,下面 pair 循环会 warn 并跳过
		}
		if miss := shortSideBlockers(ex); len(miss) > 0 {
			return nil, fmt.Errorf("pair %s@%s 配了 allow_short 但前置条件不满足:%s。"+
				"永续风控四件套(止损 reduce-only 平仓/保证金率监控/显式杠杆/资金费入账)补齐前"+
				"不要开这个开关,详见 state/strategy/gate-futures-mm-assessment-2026-09-08.md §4",
				p.ExecSymbol, p.Exec, strings.Join(miss, ";"))
		}
		logger.Infof("[mm] pair %s@%s 双边报价已放开(allow_short),卖侧不再受已持有库存限制", p.ExecSymbol, p.Exec)
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
		// 这条过去是无声的:接了永续适配器,引擎仍按现货规则把卖量钳在已持有量上,
		// 空仓时一张卖单都挂不出来,日志里看不出任何异常。现在说出来。
		if ex.SupportsShort() && !p.AllowShort {
			logger.Infof("[mm] pair %s@%s:场馆支持做空但 allow_short 未开,卖侧仍按现货规则钳在已持有量上(空仓=只挂买单)", p.ExecSymbol, p.Exec)
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

// shortSideBlockers 列出"这个场馆的卖侧还不能放开"的原因。空 = 可以放开。
// 只在 pair 配了 allow_short 时才问它 —— 没配就是历史行为,不需要理由。
//
// 两条判据都必须是【运行时探针】而不是写死的开关,否则闸门本身就成了另一处
// "写了没人调"的死代码 —— 这一轮修的正是那种东西(SupportsShort 全仓库零调用)。
func shortSideBlockers(ex ExecExchange) []string {
	var miss []string
	if !ex.SupportsShort() {
		miss = append(miss, fmt.Sprintf("场馆 %s 不支持做空(SupportsShort()=false)", ex.Name()))
	}
	if _, ok := ex.(PerpRiskControls); !ok {
		miss = append(miss, fmt.Sprintf("适配器 %s 未实现 PerpRiskControls(缺强平/保证金率监控)", ex.Name()))
	}
	return miss
}

// shortSideEnabled 是"卖量能不能超过已持有量"的唯一判据。fail-closed:
// 任何一条不满足都退回现货规则。Start 已经把不满足的配置挡在门外,这里再判一次
// 是因为 Engine 可以不经 Start 构造(测试、将来的其它入口),闸不能只有一道。
func shortSideEnabled(p PairConfig, ex ExecExchange) bool {
	return p.AllowShort && len(shortSideBlockers(ex)) == 0
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
	// 每 symbol 的基差估计(台账 #7)。未配 basis_half_life_s 时为 nil = 关闭,
	// 下面所有取值都退化成 0,行为与改动前完全一致。
	basis := newBasisEWMA(p.BasisHalfLifeS, p.BasisCapBps)

	// 成交/PnL 追踪 + 单日止损(仅 gate live;账户是用户自己的 gate key,my_trades=本引擎成交)。
	var tracker *pnlTracker
	var baseAsset string
	if !e.cfg.ObserveOnly && strings.EqualFold(ex.Name(), "gate") {
		tracker = newPnLTracker()
		baseAsset = strings.SplitN(p.ExecSymbol, "_", 2)[0]
	}
	var lastPoll, haltUntil time.Time
	var sdGate standDownGate // 限流熔断站下状态机(每个 pair 一份),见 standdown.go

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
		feeBps, feeLive := MakerFeeBps(ex.Name(), p.ExecSymbol)
		// 一条记录同时供三处消费:面板(内存)、离线复核(server.log 里的单行 JSON)、
		// 单测。三者共用 observeRow 这一份口径,不再各算各的。
		// 先取修正、再推进估计:本轮报价只用【本轮之前】的样本。这样线上口径与
		// 离线回放(basis.go 顶部那组数字)是同一个,A/B 时两边能对上账。
		corrBps := basis.correctionBps()
		row := p.observeRow(ex.Name(), e.feed.Name(), ref, eb, feeBps, feeLive, corrBps, time.Now())
		basis.update(row.MidDiffBps, time.Now())
		logObserve(row)
		recordObserve(row)
		// 喂 markout:上面那两个 edge 测的是【报价时刻】的边,可以永远为正而 PnL 永远为负。
		// markout 测【成交之后】价格往哪走,是唯一能把"手续费太贵"和"被逆向选择"分开的指标。
		// 搭这趟已有的行情轮询,不额外发请求。
		//
		// 基准必须是【成交发生的那个所】的中价(eb.Mid()),不能是参考所中价:
		// markoutBps 算的是 (midAfter - fillPx)/fillPx,fillPx 来自 gate。拿 binance 中价当
		// midAfter,基差 b 会原封不动进结果 —— 买单恒 -b、卖单恒 +b,与价格实际漂移无关。
		// 2026-09-08 实测 ONG_USDT b=+45.2bps,足以把 markout 整体淹掉;那样它就无法回答
		// 它被造出来要回答的问题(逆向选择 vs 费率),也无法用来 A/B 报价锚点。
		markoutTracker.Observe(p.ExecSymbol, eb.Mid(), time.Now())

		if !e.cfg.ObserveOnly {
			// 单日止损熔断中:停报价至次日 UTC。
			if !haltUntil.IsZero() && time.Now().Before(haltUntil) {
				continue
			}
			// 限流熔断持续打开 → 撤单站下(standdown.go)。
			//
			// 【位置】必须排在所有报价类 IO 之前。判据只来自限流器自身的状态,
			// 不依赖任何会被熔断挡住的请求;要是像原来的余额避险那样排在
			// quote() 内部 OpenOrders 之后,熔断一开第一发就被本地拒、整段永远
			// 走不到 —— 那就成了第二条死代码(台账 #117 同病)。
			if e.stepStandDown(&sdGate, ex, p, time.Now()) {
				continue
			}
			// 参考盘口过期就撤掉两边报价,绝不按陈旧参考挂单(防参考断流时裸报价)。
			if time.Since(ref.Ts) > staleAfter(p) {
				e.cancelAll(ex, p.ExecSymbol)
				continue
			}
			// 长时间偏移:exec 与 ref 中价持续偏离超阈值 → 撤单暂停(可能真错价/数据问题,
			// 按错价裸挂会被逆向吃穿);回到阈值内才恢复报价。
			// 用【残差】而不是原始基差:开了修正之后,持续水位差已经被平移掉,
			// 再拿原始 |b| 卡门会让 ONG 这类品种常年顶在阈值上被撤单,修正等于空转。
			// 未开修正时 corrBps=0,本式与改动前逐位相同。见 liveDivergenceBps。
			midDiffBps := liveDivergenceBps(refMid, eb.Mid(), corrBps)
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
					// 同一批成交喂给 markout(内部按 fillID 去重)。
					// 只有拿到交易所给的成交时刻才喂 —— 没有时刻就算不出 markout,
					// 硬用轮询时刻会把 1s horizon 测成噪音。
					for _, f := range fills {
						if !f.Ts.IsZero() {
							markoutTracker.RecordFill(ex.Name(), p.ExecSymbol, f.ID, f.Side, f.Price, f.Amount, feeBps, f.Ts)
						}
					}
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
			e.quote(p, ex, ref, eb, corrBps)
		}
	}
}

// stepStandDown 跑一轮站下状态机并执行对应动作。返回 true = 本轮不报价。
//
// 适配器不实现 RateLimitReporter(coinsph/mexc/kucoin 等)时恒返回 false,
// 整条路径不参与,行为与改动前逐位相同。
func (e *Engine) stepStandDown(g *standDownGate, ex ExecExchange, p PairConfig, now time.Time) bool {
	rep, ok := ex.(RateLimitReporter)
	if !ok {
		return false
	}
	st := rep.RateLimitStatus()
	window := time.Duration(st.WindowMs) * time.Millisecond
	if window <= 0 {
		// 适配器没报窗口就按内置默认(config.go defaults() 的 10s)算,
		// 绝不退化成 0 —— 那会让恢复条件变成"立刻恢复",防抖直接失效。
		window = 10 * time.Second
	}
	downAfter := standDownAfter(p)

	switch g.decide(st.BreakerOpenSince, window, downAfter, now) {
	case standDownEnter:
		e.cancelAll(ex, p.ExecSymbol)
		logger.Errorf("[mm-standdown] %s@%s 限流熔断已持续打开 %s(阈值 %s):撤单站下 —— 报价移不动时不再挂单",
			p.ExecSymbol, ex.Name(), now.Sub(st.BreakerOpenSince).Truncate(time.Millisecond), downAfter)
		return true
	case standDownHold:
		// 站下期间每轮打一发报价类探针。
		//
		// 【它是必需品,不是装饰】熔断未到点时这发请求在本地就被拒(零配额、
		// 不出网);banUntil 到点后它就是限流器头注里说的那发半开探针 —— 成功
		// 即 onSuccess() → openSince 清零 → 状态机的"健康"开始计时。
		// 没有它,站下之后引擎不再发任何报价类请求,onSuccess() 永远不会被调用,
		// openSince 永远不清零,于是永久卡在站下 —— 活锁。
		//
		// 顺带:探针通了却还读到挂单,说明站下那一刻的撤单没撤干净(当时多半正被
		// 限流)。站下的全部意义就是盘口上不留单,所以补撤。
		if !g.shouldProbe(now, window) {
			return true
		}
		if orders, err := ex.OpenOrders(p.ExecSymbol); err == nil && len(orders) > 0 {
			logger.Warnf("[mm-standdown] %s@%s 站下中仍有 %d 张残留挂单,补撤", p.ExecSymbol, ex.Name(), len(orders))
			e.cancelAll(ex, p.ExecSymbol)
		}
		return true
	case standDownExit:
		logger.Infof("[mm-standdown] %s@%s 限流已连续 %s(%d×窗口)未再熔断:恢复报价",
			p.ExecSymbol, ex.Name(), time.Duration(standUpHealthyWindows)*window, standUpHealthyWindows)
	}
	return false
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
func (e *Engine) quote(p PairConfig, ex ExecExchange, ref, eb BookTicker, corrBps float64) {
	// 卖侧是"只能卖已持有"(现货)还是"可以卖到 −MaxPosition"(永续)。
	// 全函数只在这里判一次,下面三处(持仓口径/卖量上限/库存偏移下界)共用同一个结论,
	// 免得三处各判各的、将来漂成不一致。
	shortOK := shortSideEnabled(p, ex)

	filt, err := ex.SymbolFilter(p.ExecSymbol)
	if err != nil {
		logger.Warnf("[mm] %s@%s filter 读取失败,本轮不报价: %v", p.ExecSymbol, ex.Name(), err)
		return
	}
	refMid := ref.Mid()
	half := p.SpreadBps / 10000.0

	// 【顺序:余额在挂单之前】两个读都是 gateClassQuote,持续限流下会一起失败,
	// 所以谁排前面谁的失败分支才会被执行。原来 OpenOrders 排在前面,它的分支是
	// 「本轮不动单 → return」,于是下面那条「余额读失败就 cancelAll 避险」永远走不到 ——
	// 一条写了却从没执行过的安全路径(和台账 #117「优雅撤单是死代码」同一个病)。
	// 把避险的那条排到前面,限流时才真的会撤单。
	//
	// 两个读之间没有数据依赖:baseHeld 需要余额和卖单量【都】拿到才算得出,
	// 先读哪个都不影响结果,只影响快照的时间偏斜方向(可忽略,原来也偏)。
	bals, err := ex.Balances()
	if err != nil {
		logger.Warnf("[mm] %s@%s 余额读取失败,撤单避险: %v", p.ExecSymbol, ex.Name(), err)
		e.cancelAll(ex, p.ExecSymbol)
		return
	}

	// 挂单必须读到:卖单里锁着的 SOL 仍是你的持仓。否则"挂卖→可用余额变少→
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

	// 现货持仓 = 可用余额 + 自己卖单里锁着的量(去掉这一项就会 thrash)。
	//
	// 放开做空后这一项必须【不加】:永续的 Balances() 返回的是清算所里的有符号持仓
	// (hyperliquid.go Balances 注释),挂着的卖单不从里面扣,再加一次就把持仓算大了 ——
	// 库存偏移和库存闸会一起偏向一侧。也不会 thrash,因为持仓本来就不随挂单变。
	baseHeld := bals[filt.BaseAsset]
	if curAsk != nil && !shortOK {
		baseHeld += curAsk.Qty
	}

	// 库存偏移:持仓越满,报价中枢越往下压 → 卖价更贴中价(易成交、卸货)、买价更远(少接货),
	// 把仓位往中性(现货目标持仓=0)拽回,对冲趋势里单向堆货。空仓时退化成对称报价。
	invRatio := 0.0
	if p.MaxPosition > 0 {
		invRatio = baseHeld / p.MaxPosition
	}
	// 现货余额恒非负,invRatio 本来就不会为负;这里把它钉死,是为了让"闸关着时
	// 行为逐位不变"不依赖于余额接口的善意 —— 任何来源的负值都退回历史的 0。
	// 闸开着时留住负号:持空仓 → 中枢上移 → 买价贴中价(早点买回来)、卖价推远
	// (少继续做空),把仓位往中性拽,和多头方向对称。
	if !shortOK && invRatio < 0 {
		invRatio = 0
	}
	// 报价中心:默认锚参考所中价(历史行为);配了 quote_anchor:"exec" 就锚执行所自身中价;
	// 配了 basis_half_life_s 则在参考所中价上叠加基差修正 corrBps(台账 #7 的修复形态)。
	// 基差大于半价差的品种,不修正会让两条腿同时挂在执行所盘口的错误一侧 —— 见 basis.go 注释。
	anchor := p.basisAdjustedMid(refMid, eb.Mid(), corrBps)
	bidPx, askPx := skewedQuote(anchor, half, invRatio, inventorySkewFrac, filt.TickSize)
	// 吃满执行所盘口价差:执行所卖一比"公允+spread"更贵时,把卖单顶到其卖一下方 1 tick
	// (捕获整段溢价,而不是按固定 spread 自己砍价贱卖);买一更便宜时同理下探。同时严格
	// 留在盘口内 → post-only 不会被 POC_FILL_IMMEDIATELY 拒。这是"面板正、成交负"的正解:
	// 面板量的是盘口既有价差,过去我们却挂在它里面把便宜货让了出去。
	bidPx, askPx = rideToBook(bidPx, askPx, eb.BidPx, eb.AskPx, filt.TickSize)
	if bidPx <= 0 || askPx <= 0 || askPx <= bidPx {
		return
	}

	// 买量钳到"上限−持仓",防止在接近上限时又买满一整单冲破 cap(order_qty≈½cap 时最多溢出
	// 50%)。baseHeld 在永续上有符号,持空仓时这一项自然放大到 order_qty,买侧无需改。
	bidQty := roundToStep(minf(p.OrderQty, p.MaxPosition-baseHeld), filt.StepSize)
	// 卖量上限:现货是"已持有的量";放开做空后是"上限 + 持仓"(baseHeld 带符号),
	// 即允许一路卖到 −MaxPosition,与买侧的 +MaxPosition 对称。
	askCap := baseHeld
	if shortOK {
		askCap = p.MaxPosition + baseHeld
	}
	askQty := roundToStep(minf(p.OrderQty, askCap), filt.StepSize)

	// 库存闸:总持仓达上限不再买;卖侧由 askQty>0 兜住 —— 现货上它等价于"无库存不挂卖",
	// 放开做空后等价于"空到 −上限就不再卖"。两边都要过最小名义额。
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

// cancelAll 撤掉该 symbol 的全部挂单。它是本模块【所有】避险路径的共同动作
// (参考流过期/持续偏离/单日止损/优雅关闭/熔断站下),所以它的第一步——读挂单——
// 必须和撤单同档,不能走会被熔断挡住的报价档。见 types.go CancelPathReader。
func (e *Engine) cancelAll(ex ExecExchange, symbol string) {
	orders, err := cancelPathOpenOrders(ex, symbol)
	if err != nil {
		return
	}
	for _, o := range orders {
		_ = ex.CancelOrder(symbol, o.ID)
	}
}

// cancelPathOpenOrders 用撤单档读挂单;适配器没实现这一档就退回普通读(行为不变)。
func cancelPathOpenOrders(ex ExecExchange, symbol string) ([]OpenOrder, error) {
	if r, ok := ex.(CancelPathReader); ok {
		return r.OpenOrdersForCancel(symbol)
	}
	return ex.OpenOrders(symbol)
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
// center: center = refMid*(1 - half*skewFrac*invRatio), invRatio clamped to [−1,1].
// More base held (higher invRatio) lowers the center → ask nears mid (lean to sell
// down), bid recedes (buy less). invRatio=0 → symmetric quotes around refMid.
//
// 下界从 0 放宽到 −1 是为了让空头方向的库存偏移生效:钳在 0 时,任何空仓都被当成
// 平仓处理,报价永远对称 —— 没有任何价格上的力把空头往中性拽回,只有硬上限拦着,
// 到了 −MaxPosition 直接停手。公式本身对负值就是对的,只是过去到不了。
// 上界仍是 1(engine_test.go TestSkewedQuote 锁着这条)。
// 谁能喂进负值由 quote() 的 shortOK 决定,现货路径进不来。
func skewedQuote(refMid, half, invRatio, skewFrac, tick float64) (bid, ask float64) {
	if invRatio < -1 {
		invRatio = -1
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
