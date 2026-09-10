package marketmaker

import (
	"context"
	"errors"
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
	// dm 是交易所侧死人开关的 arm 凭证(deadman.go)。报价前必须能从里面查到
	// "这个 symbol 最近一次 arm 成功"——查不到就不报价,见 deadmanGate。
	// nil(不经 Start 构造的 Engine)= 什么都没证实 = gate 场馆一律不报价。
	dm *deadmanArm
	// workers 只数【报价 worker】(runPair),因为只有它们会挂新单。
	// Stop() 要先等它们退干净再撤单,否则撤单扫一遍的同时还有 worker 在
	// reconcileSide 里挂新的 —— 优雅关闭结束后盘口上仍留着一张裸单。
	workers sync.WaitGroup
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
	e := &Engine{cfg: cfg, feed: feed, execs: execs, stop: cancel, dm: &deadmanArm{}}
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
		e.workers.Add(1)
		go func() {
			defer e.workers.Done()
			e.runPair(ctx, p, ex)
		}()
	}
	if !cfg.ObserveOnly {
		go e.runDeadMansSwitch(ctx) // 交易所侧死人开关(唯一能扛进程崩溃的兜底)
	}
	return e, nil
}

// shutdownDrainBudget / shutdownCancelBudget 是优雅关闭的两段硬上限。
//
// 【为什么必须有上限】关闭时大概率正被限流:撤单走 gateClassCritical,单发就可能
// 等名额 MaxWaitMs(默认 3s)+ 退避重试 MaxRetries(3)×MaxBackoffMs(2s)。
// 不封顶的话"撤干净"会把关闭拖到十几秒,而外面还有一把更硬的刀在等着。
//
// 【上限从哪来的:外面那把刀】容器收到 SIGTERM 后只有一个宽限期,到点就是 SIGKILL,
// 那时撤到哪算哪。docker 的默认宽限期是 10s,而 docker-compose.prod.yml 里没有配
// stop_grace_period(已 grep 确认),所以生产上就是这个 10s。预算必须整个装进去。
//
//	drain  2s:worker 的阻塞点是 refresh ticker(默认 1s)和一发在途 HTTP
//	          (gate.go 的 http.Client Timeout=10s)。2s 覆盖常见的 ticker 情形并留 1s 余量;
//	          卡在在途请求上就超时放行 —— 最坏是扫完之后又被挂上一张单,
//	          仍然远好于"为了等 worker 而根本没扫"。
//	cancel 5s:恰好等于 MaxWaitMs(3s)+MaxBackoffMs(2s),即【一发】撤单在最坏情况下
//	          走完"等名额+一次退避"所需的时间 —— 预算再小就等于保证第一发都撤不完。
//	          同时它只占 10s 宽限期的一半,把另一半留给 HTTP 服务收尾和进程自身退出。
//
// 两段加起来最坏 7s < 10s。超时【必须出声】:这是"没撤干净"的唯一线索,
// 台账 #125(软链断了静默 exit 0)/#115(Redis 认证失败不会挂)就是没出声的代价。
const (
	shutdownDrainBudget  = 2 * time.Second
	shutdownCancelBudget = 5 * time.Second
)

// Stop 是【唯一】的优雅关闭路径:停报价 → 撤光挂单 → 让 worker 退出。
// 在台账 #117 修好之前它从没被执行过(后端零信号处理,ctx 是永不 Done 的
// context.Background),接上 SIGTERM/SIGINT 之后它才真的会跑,见 cmd/main.go。
func (e *Engine) Stop() {
	setRunning(false)
	if e == nil {
		return
	}
	// 【顺序:先停 worker,再撤单】反过来的话,撤单扫一遍的同时 worker 还在按
	// 上一轮的目标价挂新单,扫完之后盘口上照样留着一张裸单 —— 优雅撤单等于白撤。
	if e.stop != nil {
		e.stop()
	}
	e.drainWorkers(shutdownDrainBudget)
	// 撤掉所有残留挂单,绝不把裸单留在交易所(否则重启/崩溃后被人慢慢吃)。
	if !e.cfg.ObserveOnly {
		e.sweepCancel(shutdownCancelBudget)
	}
}

// drainWorkers 等报价 worker 退出,最多等 budget。超时不是致命的,但必须出声。
func (e *Engine) drainWorkers(budget time.Duration) {
	done := make(chan struct{})
	go func() {
		e.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(budget):
		logger.Warnf("[mm] Stop: 报价 worker 在 %s 内没退干净(多半卡在一发在途请求上),仍继续撤单", budget)
	}
}

// sweepCancel 把每个 pair 的挂单撤掉,整体最多花 budget。
// 超时时【明说没撤干净】—— 静默放弃就是给自己留一个查不出来的裸单。
//
// 【为什么这里是"重试一次 + 如实报账"而不是记欠账】进程正在退出,没有下一个
// 周期可以还账,所以重试必须就地做完(预算由下面的 select 兜住);而"已撤所有
// 挂单"这句话是所有者事后判断"上一次退干净了没有"的唯一依据,撤单档读失败或
// 逐张撤失败时【绝不能】再打它 —— 那就是一句假绿。
func (e *Engine) sweepCancel(budget time.Duration) {
	done := make(chan struct{})
	var dirty []string // 只在 done 关闭之后读(超时分支不读),不需要额外同步
	go func() {
		defer close(done)
		for _, p := range e.cfg.Pairs {
			ex, ok := e.execs[p.Exec]
			if !ok {
				continue
			}
			out := e.cancelAll(ex, p.ExecSymbol)
			if !out.clean() {
				// 关闭是最后一次机会:再撤一发再下结论(限流下第一发被拒是常态)。
				logger.Warnf("[mm] Stop: %s@%s 撤单没被证实做成(%s),重试一次",
					p.ExecSymbol, ex.Name(), out.why())
				out = e.cancelAll(ex, p.ExecSymbol)
			}
			if !out.clean() {
				dirty = append(dirty, fmt.Sprintf("%s@%s(%s)", p.ExecSymbol, ex.Name(), out.why()))
			}
		}
	}()
	select {
	case <-done:
		if len(dirty) == 0 {
			logger.Infof("[mm] Stop: 已撤所有挂单")
			return
		}
		logger.Errorf("[mm] Stop: 撤单扫完了但【没撤干净】:%s —— 交易所上可能仍有残留挂单。"+
			"进程退出后唯一的兜底只剩交易所侧死人开关倒计时(仅 gate,且必须此前 arm 成功,"+
			"见 deadman.go),下次启动时 runPair 的开机清理会补撤,期间请人工核对盘口",
			strings.Join(dirty, "; "))
	case <-time.After(budget):
		logger.Errorf("[mm] Stop: 撤单没能在 %s 预算内跑完,放弃剩余部分继续退出 —— "+
			"交易所上可能仍有残留挂单,下次启动时 runPair 的开机清理会补撤,期间请人工核对", budget)
	}
}

// startupCancel*:开机清残留证实不了时的重试节奏。
//
// 前几发密一点(交易所偶发 5xx/超时几秒就过去了),之后退到慢档【长期】重试:
// 只要没证实盘口干净就永远不报价,但也永远保留自愈的机会 —— 一次启动瞬间的抖动
// 不该让这个 pair 到下次重启前都不工作,而 gate 侧一段十分钟的维护也不该。
// 慢档 30s 同时兼作日志节流:坏着的时候每 30s 一条 ERROR,不刷屏也不静默。
const (
	startupCancelFastTries = 3
	startupCancelFastGap   = 2 * time.Second
	startupCancelSlowGap   = 30 * time.Second
)

// clearStaleOrders 反复清残留,直到【证实】盘口干净为止。
// 返回 false 只有一个原因:ctx 结束(引擎在关闭)——那就别报价了,直接退出 worker。
func (e *Engine) clearStaleOrders(ctx context.Context, p PairConfig, ex ExecExchange) bool {
	for try := 1; ; try++ {
		out := e.cancelAll(ex, p.ExecSymbol)
		if out.clean() {
			logger.Infof("[mm] %s@%s 启动清残留挂单:%s", p.ExecSymbol, ex.Name(), out.why())
			return true
		}
		logger.Errorf("[mm] %s@%s 启动清残留挂单没被证实做成(第 %d 次):%s —— "+
			"证实盘口干净之前这个 pair 不报价(上一轮/崩溃留下的孤儿单可能还在)",
			p.ExecSymbol, ex.Name(), try, out.why())
		gap := startupCancelFastGap
		if try >= startupCancelFastTries {
			gap = startupCancelSlowGap
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(gap):
		}
	}
}

// dmGateState 是"死人开关这道闸"在一个 pair 上的记忆:从什么时候开始被挡、上次喊过没。
type dmGateState struct {
	since time.Time
	say   sayEvery
}

// deadmanGateSayEvery:被挡期间重复喊话的间隔(理由同 cancelDebtSayEvery)。
const deadmanGateSayEvery = 30 * time.Second

// deadmanGate 回答:这一轮能不能报价。false = 不能,交易所侧倒计时没被证实 arm 上。
//
// 【判据是数据,不是逻辑】它只问 e.dm 里有没有"这个 symbol 最近一次 arm 成功"的
// 记录、且还在覆盖期内。没有记录一律算没覆盖 —— 包括:从没 arm 成功过、arm 连续
// 失败到上一次成功已经过期、死人开关那个 goroutine 压根没起来、甚至 Engine 不经
// Start 构造(e.dm 为 nil)。这样"忘了 arm"和"arm 报错"自动同归一路,
// 而不是各写各的判断。
func (e *Engine) deadmanGate(p PairConfig, ex ExecExchange, st *dmGateState, now time.Time) bool {
	if !deadmanCovers(ex) {
		return true // 这个场馆本来就没有交易所侧死人开关,不归这道闸管
	}
	if e.dm.covered(p.ExecSymbol, now) {
		if !st.since.IsZero() {
			logger.Infof("[mm] %s@%s 死人开关已 arm 上(挡了 %s),恢复报价",
				p.ExecSymbol, ex.Name(), now.Sub(st.since).Truncate(time.Second))
			*st = dmGateState{}
		}
		return true
	}
	if st.since.IsZero() {
		st.since = now
	}
	blocked := now.Sub(st.since)
	if st.say.due(now, deadmanGateSayEvery) {
		if blocked < deadmanTimeout {
			// 刚启动的头一两秒还没轮到第一次 arm,这是正常的(worker 比死人开关先起)。
			logger.Warnf("[mm] %s@%s 交易所侧死人开关还没证实 arm 上(%s),本轮不报价",
				p.ExecSymbol, ex.Name(), blocked.Truncate(time.Second))
		} else {
			logger.Errorf("[mm] %s@%s 交易所侧死人开关已 %s 没能 arm 上:拒绝报价 —— "+
				"没有它,进程被 SIGKILL 之后挂单会原样留在盘口(见 deadman.go)。"+
				"先查 MM_GATE_API_KEY/SECRET 的撤单权限和到 %s 的连通性",
				p.ExecSymbol, ex.Name(), blocked.Truncate(time.Second), deadmanHost)
		}
	}
	return false
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
	//
	// 【必须证实,证实不了就不开工】读挂单失败时 cancelAll 一张也撤不掉,而它长得
	// 和"盘口本来就是空的"一模一样。没证实就开始报价 = 在一堆来路不明的孤儿单上面
	// 加挂;下面 blindClock 那句"此刻盘口是空的、我们知道自己的状态"也就成了假话。
	if !e.cfg.ObserveOnly && !e.clearStaleOrders(ctx, p, ex) {
		return
	}
	var devSince time.Time // exec-vs-ref 中价持续偏离的起始时刻(长时间偏移撤单用)
	var debt cancelDebt    // 撤单欠账(每个 pair 一份),见 cancel_proof.go
	var dmGate dmGateState // 死人开关这道闸在本 pair 上的记忆,见 deadman.go
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
	// 余额盲区时钟(每个 pair 一份),见 blindClock。从启动这一刻开始计时:
	// 上面刚做过一次开机清残留,盘口是空的,"此刻我们知道自己的状态"是成立的。
	// 不能留零值 —— 零值的语义是"从来没读到过",第一轮读失败就会立刻走避险撤一次空单。
	blind := &blindClock{lastOK: time.Now()}

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
			now := time.Now()
			// 单日止损熔断中:停报价至次日 UTC。
			if !haltUntil.IsZero() && now.Before(haltUntil) {
				// 【停业不等于盘口是空的】止损那一刻的撤单如果没被证实做成,
				// 原来这一整天都不会再有人去撤它 —— 下面每个周期都在这里 continue。
				e.repayCancelDebt(&debt, ex, p, now)
				continue
			}
			// 限流熔断持续打开 → 撤单站下(standdown.go)。
			//
			// 【位置】必须排在所有报价类 IO 之前。判据只来自限流器自身的状态,
			// 不依赖任何会被熔断挡住的请求;要是像原来的余额避险那样排在
			// quote() 内部 OpenOrders 之后,熔断一开第一发就被本地拒、整段永远
			// 走不到 —— 那就成了第二条死代码(台账 #117 同病)。
			if e.stepStandDown(&sdGate, ex, p, &debt, now) {
				continue
			}
			// 参考盘口过期就撤掉两边报价,绝不按陈旧参考挂单(防参考断流时裸报价)。
			if time.Since(ref.Ts) > staleAfter(p) {
				e.hedgeCancel(&debt, ex, p, "参考流过期", now)
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
					e.hedgeCancel(&debt, ex, p, "exec/ref 中价持续偏离", now)
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
						// 撤没撤成会记进欠账:上面那条 haltUntil 分支每个周期都会接着还,
						// 否则"停业"只是不再报价,盘口上的单可能躺一整天。
						e.hedgeCancel(&debt, ex, p, "单日止损", now)
						haltUntil = nextUTCMidnight()
						logger.Errorf("[mm] %s@%s 触发单日止损 PnL=%.2f < -%.2f → 停报价至次日", p.ExecSymbol, ex.Name(), pnl, e.cfg.MaxDailyLossUSD)
						continue
					}
				}
			}
			// 恢复报价的前提有两条,都必须是【被证实的数据】,不是"没人报错":
			//   ① 上一次避险撤单确实撤干净了(cancel_proof.go);
			//   ② 交易所侧死人开关确实 arm 上了(deadman.go)——
			//      没有它,进程被 SIGKILL 之后挂单会原样留在盘口。
			if !e.repayCancelDebt(&debt, ex, p, now) {
				continue
			}
			if !e.deadmanGate(p, ex, &dmGate, now) {
				continue
			}
			// quote 内部那条"余额读不到就撤单避险"也是一次 cancelAll,
			// 同样要记账 —— 否则下一轮余额读回来了就会带着没撤成的单恢复报价。
			if out := e.quote(p, ex, ref, eb, corrBps, blind); out != nil {
				debt.note(*out, ex, p, "余额读不到", time.Now())
			}
		}
	}
}

// stepStandDown 跑一轮站下状态机并执行对应动作。返回 true = 本轮不报价。
//
// 适配器不实现 RateLimitReporter(coinsph/mexc/kucoin 等)时恒返回 false,
// 整条路径不参与,行为与改动前逐位相同。
//
// d 是本 pair 的撤单欠账(cancel_proof.go):站下的那次撤单和站下期间的补撤都
// 可能没做成,而站下的全部意义就是盘口上不留单 —— 没证实就得记账,并且在还清
// 之前不许恢复报价(恢复的判据在 runPair 里)。
func (e *Engine) stepStandDown(g *standDownGate, ex ExecExchange, p PairConfig, d *cancelDebt, now time.Time) bool {
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
		e.hedgeCancel(d, ex, p, "限流熔断站下", now)
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
		// 探针读失败【不】记欠账:站下期间它本来就该在本地被拒,那是设计,不是"不知道"。
		// 但探针通了、看见残留单、补撤又没成 —— 那就是真的没撤干净,必须记账。
		if orders, err := ex.OpenOrders(p.ExecSymbol); err == nil && len(orders) > 0 {
			logger.Warnf("[mm-standdown] %s@%s 站下中仍有 %d 张残留挂单,补撤", p.ExecSymbol, ex.Name(), len(orders))
			e.hedgeCancel(d, ex, p, "站下期间补撤残留挂单", now)
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

// ─────────────────────────────────────────────────────────────────────────────
// 【「本地名额耗尽」≠「余额真读不到」】—— 所有者拍板,理由与边界记在这里
//
// 余额避险(读不到余额就 cancelAll)这条路径的语义是:【我不知道自己的仓位/余额,
// 所以不敢再挂单】。而 gate 的本地限流拒绝(ErrGateRateLimited,两种形态:报价类
// 名额耗尽 / 熔断打开时报价类快拒)根本不是"不知道"—— 那一发请求【压根没出网】,
// 是我们自己选择了这一轮不去问。把"没问"当成"问了但答不上来",是把一个我们主动
// 做的节流决策误读成了外部故障。
//
// 代价是实测出来的(standdown_test.go TestStandDownEndToEnd):恢复后 20 个周期里
// 19 个周期盘口是空的 —— 链路是 本地名额耗尽 → 触发余额避险 → 撤单走平 → 名额滑出
// 窗口再挂回来。惩罚档下几乎常态走平,而惩罚档恰恰是常态(台账 #84),等于做市停业。
//
// 而"持续限流"这个情况【已经有专门的主人】:上一轮做的站下状态机(standdown.go)
// 显式接管了它,熔断持续 5s 就撤单站下。一件事一个主人,余额避险不该再兼职处理它。
//
// 所以判据改成:只有【真的问了、而答不上来】才算读失败(请求发出去了但失败/超时/
// 返回异常,含交易所回的 429 —— 那是它拒绝回答,不是我们没问)。本地挡下来的那种
// 交给站下判断。
// ─────────────────────────────────────────────────────────────────────────────

// blindClock 记住"上一次真的读到余额"的时刻,每个 pair 一份,由 runPair 持有。
//
// 它存在的唯一理由是给上面那条豁免加一个【时间边界】:本地名额如果耗尽得【够久】,
// 久到我们其实已经不知道自己的余额了,那"不算故障"就会变成一个永远不会响的静默洞
// (台账 #125 软链断了静默 exit 0、#115 Redis 认证失败不会挂,都是这个病)。
type blindClock struct {
	lastOK time.Time
}

// balanceBlindWindows 是那条时间边界,以【限流窗口】为单位:连续这么久没有一次
// 成功的余额读,就不再豁免,退回撤单避险。
//
// 【下界:为什么不能更短】惩罚档下报价类需求本来就高于预算,名额耗尽是常态,
// 两次成功读余额之间【本来就会】隔一段。这个间隔是量出来的,不是拍的
// (standdown_test.go TestBalanceBlindLimitExceedsRoutineGap,默认档 10 请求/10s):
//
//	refresh=500ms  → 最长间隔 8.5s
//	refresh=1000ms → 最长间隔 7s
//	refresh=2000ms → 最长间隔 4s
//
// 边界取 1 个窗口(10s)就贴着常态上界了,抖一下就误判,等于这一轮白改。取 2 个窗口
// (默认 20s)才对常态最坏值留出 2 倍以上余量。
//
// 【上界:为什么不能更长/为什么它一定会响】滑动窗口每过一个窗口必然放出
// QueryRequests−ReservedCancel(默认 197)个查询名额(台账 #197 分池后,余额读花的
// 是查询池,不再和下单抢那 10 个),所以"连续 2 个窗口一次都没轮到"
// 在正常节流下【不可能发生】。越过这条边界,原因只可能是别的:熔断长期打开(那是
// 站下的场景,而站下阈值 5s 远早于此,轮不到这里)、限流器状态被写坏、或者时钟/
// 协程卡死。那些都是真故障,该撤单。也就是说这条边界不是拍脑袋的超时,是"节流
// 在物理上解释不了了"的那个点。
//
// 单位跟着窗口走(和 standUpHealthyWindows 同一套口径):调档改 window_ms 时它自动
// 跟着变,不用改代码。
const balanceBlindWindows = 2

// balanceBlindLimit 返回这个适配器上的盲区上限。
// 适配器不报告窗口就按内置默认 10s 算(config.go defaults()),绝不退化成 0 ——
// 那会让边界变成"立刻超时",豁免直接失效。
func balanceBlindLimit(ex ExecExchange) time.Duration {
	window := 10 * time.Second
	if rep, ok := ex.(RateLimitReporter); ok {
		if w := time.Duration(rep.RateLimitStatus().WindowMs) * time.Millisecond; w > 0 {
			window = w
		}
	}
	return balanceBlindWindows * window
}

// tolerate 回答:这一次余额读失败,要不要豁免(本轮什么都不做,不撤单)。
//
// 返回 false 的三种情况,都必须落到撤单避险上:
//   - 不是本地拒绝 → 请求真出网了却没拿到答案 → 我们确实不知道余额;
//   - 没有记忆(nil / 零值)→ fail-safe:不知道自己盲了多久,就当作盲了;
//   - 盲得超过边界 → 见 balanceBlindWindows。
//
// 超边界那一次会把时钟【重新计时】:否则之后每一个周期都会再撤一遍,
// 把一次性的避险变成每秒一次的撤单风暴。重新计时后它每隔一个 limit 响一次,
// 既有界又不会静默。
func (b *blindClock) tolerate(err error, limit time.Duration, now time.Time) (bool, time.Duration) {
	if b == nil || b.lastOK.IsZero() {
		return false, 0
	}
	blindFor := now.Sub(b.lastOK)
	if !errors.Is(err, ErrGateRateLimited) {
		return false, blindFor
	}
	if blindFor >= limit {
		b.lastOK = now
		return false, blindFor
	}
	return true, blindFor
}

func (b *blindClock) ok(now time.Time) {
	if b != nil {
		b.lastOK = now
	}
}

// quote maintains one post-only bid + one post-only ask around an inventory-skewed
// reservation center (the more base held, the lower the center → lean to sell down),
// clamped to never cross the exec book (post-only would reject), inventory-capped and
// fail-safe: any read error (filters/balances/open-orders) aborts this cycle rather
// than quoting blind. Cancel-replace only when the target moved more than the requote
// band (a fraction of the half-spread), to avoid thrashing.
//
// blind 是余额盲区时钟(见 blindClock),nil = 调用方没有记忆 → 任何余额读失败都
// 按"真读不到"处理(fail-safe 那一侧)。
//
// 【返回值】非 nil = 本轮走了"余额读不到 → 撤单避险"那条路,里面是那次撤单的凭证
// (cancel_proof.go)。调用方必须拿它记欠账:撤没撤成不能只由 quote 自己知道,
// 否则下一轮余额读回来了,引擎就会带着可能没撤掉的单恢复报价。
// nil = 本轮没做过避险撤单。
func (e *Engine) quote(p PairConfig, ex ExecExchange, ref, eb BookTicker, corrBps float64, blind *blindClock) *cancelOutcome {
	// 卖侧是"只能卖已持有"(现货)还是"可以卖到 −MaxPosition"(永续)。
	// 全函数只在这里判一次,下面三处(持仓口径/卖量上限/库存偏移下界)共用同一个结论,
	// 免得三处各判各的、将来漂成不一致。
	shortOK := shortSideEnabled(p, ex)

	filt, err := ex.SymbolFilter(p.ExecSymbol)
	if err != nil {
		logger.Warnf("[mm] %s@%s filter 读取失败,本轮不报价: %v", p.ExecSymbol, ex.Name(), err)
		return nil
	}
	refMid := ref.Mid()
	half := p.SpreadBps / 10000.0

	// 【顺序:余额在挂单之前】两个读都走查询池的 gateClassQuote,持续限流下会一起失败,
	// 所以谁排前面谁的失败分支才会被执行。原来 OpenOrders 排在前面,它的分支是
	// 「本轮不动单 → return」,于是下面那条「余额读失败就 cancelAll 避险」永远走不到 ——
	// 一条写了却从没执行过的安全路径(和台账 #117「优雅撤单是死代码」同一个病)。
	// 把避险的那条排到前面,限流时才真的会撤单。
	//
	// 两个读之间没有数据依赖:baseHeld 需要余额和卖单量【都】拿到才算得出,
	// 先读哪个都不影响结果,只影响快照的时间偏斜方向(可忽略,原来也偏)。
	bals, err := ex.Balances()
	if err != nil {
		// 「本地名额耗尽」≠「余额真读不到」,见 blindClock 上方那段。
		limit := balanceBlindLimit(ex)
		if ok, blindFor := blind.tolerate(err, limit, time.Now()); ok {
			logger.Debugf("[mm] %s@%s 本轮没去读余额(本地限流,已 %s 未读到,边界 %s):不报价、不撤单,持续限流交给站下: %v",
				p.ExecSymbol, ex.Name(), blindFor.Truncate(time.Millisecond), limit, err)
			return nil
		} else if blindFor >= limit {
			// 越过边界:本地名额把余额读挡了这么久,已经不能再叫"我们没问"了。
			// 这条必须是 ERROR —— 它是那个静默洞唯一会响的地方。
			logger.Errorf("[mm] %s@%s 已连续 %s 读不到余额(边界 %s,%d×限流窗口),不再当作节流:撤单避险: %v",
				p.ExecSymbol, ex.Name(), blindFor.Truncate(time.Millisecond), limit, balanceBlindWindows, err)
		} else {
			logger.Warnf("[mm] %s@%s 余额读取失败,撤单避险: %v", p.ExecSymbol, ex.Name(), err)
		}
		out := e.cancelAll(ex, p.ExecSymbol)
		return &out
	}
	blind.ok(time.Now())

	// 挂单必须读到:卖单里锁着的 SOL 仍是你的持仓。否则"挂卖→可用余额变少→
	// 下轮卖量算小→判定量不符→撤挂重下"会每秒死循环(thrash)。
	orders, err := ex.OpenOrders(p.ExecSymbol)
	if err != nil {
		logger.Warnf("[mm] %s@%s openOrders 读取失败,本轮不动单: %v", p.ExecSymbol, ex.Name(), err)
		return nil
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
		return nil
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
	return nil
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

// cancelAll 撤掉该 symbol 的全部挂单,并【返回凭证】(cancel_proof.go)。
// 它是本模块【所有】避险路径的共同动作(参考流过期/持续偏离/单日止损/优雅关闭/
// 熔断站下/开机清残留/余额读不到),所以它的第一步——读挂单——必须和撤单同档,
// 不能走会被熔断挡住的报价档。见 types.go CancelPathReader。
//
// 【返回值必须被消费】读挂单失败时它一张也撤不掉,而"撤不掉"和"本来就没单"
// 在调用方眼里长得一模一样;逐张撤单的失败同理。避险路径请走 e.hedgeCancel,
// 别直接调它 —— 那里会把没证实的撤单记成欠账,并挡住"恢复报价"。
func (e *Engine) cancelAll(ex ExecExchange, symbol string) cancelOutcome {
	orders, err := cancelPathOpenOrders(ex, symbol)
	if err != nil {
		return cancelOutcome{err: err}
	}
	out := cancelOutcome{read: true, seen: len(orders)}
	for _, o := range orders {
		if err := ex.CancelOrder(symbol, o.ID); err != nil {
			out.failed++
			out.err = err
		}
	}
	return out
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
