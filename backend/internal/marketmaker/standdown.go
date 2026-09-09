package marketmaker

import "time"

// standdown.go:限流熔断【持续】打开时,撤单站下,不再报价(台账 #109 续)。
//
// ─────────────────────────────────────────────────────────────────────────────
// 【为什么是撤掉而不是留着】——所有者拍板,理由记在这里
//
// 熔断打开 = 报价类请求在本地被直接快拒(gate.go signed() 开头那道门),也就是
// 【挂单已经无法移动】。留着 vs 撤掉,两边代价不对称:
//
//	撤掉:代价【有界】—— 花掉 ReservedCancel(默认 3)个预留名额,外加走平。最坏就这些。
//	留着:代价【无界】—— 一张动不了的陈价挂单,就是写给市场的一张免费期权;
//	      行情走得越快亏得越多,而"我们动不了它"和"行情急"高度正相关
//	      (惩罚档往往正是在行情最急、请求最密的时候踩上的)。
//
// 更根本的一条:做市的前提是能移动报价。移不动的时候我们已经不是在做市,
// 是在单边挂着 —— 那就不该继续挂。
//
// 撤单走 gateClassCritical(可等名额、可退避重试、绕过熔断),这正是那三个预留
// 名额被留出来的用途;并且 cancelAll 的读挂单那一步也已经改到同档
// (types.go CancelPathReader),否则站下动作本身会在熔断下空转。
// ─────────────────────────────────────────────────────────────────────────────

// standDownAfter 是"熔断【持续】打开多久才站下"的阈值。
//
// 【下界:为什么不能一开就撤】熔断在连续 BreakerFails(默认 5)次 429 后才打开,
// 而报价周期默认 refresh=1000ms、每轮需要 4~6 发请求(gate_ratelimit.go 头注),
// 所以 5 连拒最快约 1 秒内就能凑齐 —— 一次短暂的 429 抖动足以把熔断顶开。
// 阈值取 ≤2s 就等于"熔断一开就站下":把一次 1 秒的抖动升级成 撤单 + 走平 +
// 恢复后重挂,反而多烧掉本就稀缺的撤单额度,正是所有者点名要避免的那种浪费。
//
// 【上界:为什么不能等满冷却】默认冷却 BreakerCoolMs=30s。等满一个冷却期才站下,
// 意味着最坏情况下陈价在盘口上躺满 30 秒 —— 那正是上面那张免费期权最贵的形态。
//
// 【取值】max(5×refresh, 5s)。默认配置下 = 5 秒 = 连续 5 个报价周期一张新单都
// 挂不出去。用报价周期而不是拍一个绝对秒数,与 staleAfter() 同一套口径:两者问的
// 是同一个问题 ——"我们已经连续几轮没能更新盘口上的东西了"。5s 也远小于 30s 冷却,
// 敞口不会被留满整个惩罚窗口。5s 下界是给 refresh 配得很小(如 200ms)的情形兜底,
// 免得阈值掉进抖动量级。
func standDownAfter(p PairConfig) time.Duration {
	d := 5 * time.Duration(p.refresh()) * time.Millisecond
	if d < 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// standUpHealthyWindows 是恢复报价所需的"连续健康"时长,以【限流窗口】为单位。
//
// 【为什么恢复要比站下保守得多】两边代价仍然不对称,只是方向反过来:
//
//	站早了:立刻又吃一串 429 → 又站下一次。每次往返都要再花一遍撤单额度,
//	        还会把 consecutive429 重新顶到阈值 —— 这就是"在熔断边缘反复横跳"。
//	站晚了:只是少赚一段价差。有界、可逆、不产生敞口。
//
// 所以:站下快(5s),恢复慢(默认 20s),明确的滞回。
//
// 【为什么单位是限流窗口】窗口(rate_limit.window_ms,默认 10s)就是这条约束本身
// 的时间尺度。短于一个窗口的"安静"可能整个落在上一个窗口的尾巴里,不构成任何
// 证据 —— 一个窗口是证据的下限,取 2 个是留余量。窗口调档时这个阈值自动跟着走,
// 不用改代码。
//
// 【健康的判据】"熔断没打开"必须由一次【真实成功】来认定,而不是由 banUntil 到点
// 来认定:gateLimiter.openSince 只被 onSuccess() 清零。冷却到点但探针还没成功的
// 那段空窗,状态机仍算"没恢复"。详见 types.go RateLimitStatus.BreakerOpenSince。
const standUpHealthyWindows = 2

// standDownAction 是状态机每轮给引擎的指令。
type standDownAction int

const (
	standDownNone  standDownAction = iota // 正常:照常报价
	standDownEnter                        // 本轮进入站下:撤单走平
	standDownHold                         // 站下中:只打探针,不报价
	standDownExit                         // 连续健康达标:本轮起恢复报价
)

func (a standDownAction) String() string {
	switch a {
	case standDownEnter:
		return "enter"
	case standDownHold:
		return "hold"
	case standDownExit:
		return "exit"
	}
	return "none"
}

// standDownGate 是单个 pair 的站下状态机。零值 = 正常报价中。
//
// 状态转移是【纯函数式】的(decide 不做任何 IO),这样"什么时候站下"和"站下时
// 做什么"能分开测:前者用假时钟逐帧验,后者用假 gate 端到端验。
type standDownGate struct {
	down bool
	// healthySince 是"熔断连续没打开"的起点;零值 = 此刻不健康。
	healthySince time.Time
	// lastProbe 是上一发恢复探针的时刻,见 shouldProbe。
	lastProbe time.Time
}

// shouldProbe 决定站下期间这一轮要不要打探针,并在放行时打点。
//
// 【为什么必须节流】探针走报价档,吃的是 Requests−ReservedCancel(默认 7)个名额。
// 按 refresh(默认 1 秒)每轮打一发,熔断一解除就是 1 发/秒 —— 10 秒窗口里 7 个
// 报价名额全被探针吃光。端到端跑出来的结果就是:状态机宣布恢复了,quote() 的第一发
// OpenOrders 却撞上"名额耗尽",连着几轮报不出价。恢复动作自己把恢复堵住了。
//
// 每个限流窗口最多 1 发:占 7 个报价名额里的 1 个,把另外 6 个原封不动留给恢复后的
// 报价。代价是恢复的探测粒度变成一个窗口(默认 10s)—— 反正恢复本来就要等满
// standUpHealthyWindows 个窗口的连续健康,这点粒度落在同一个量级里。
func (g *standDownGate) shouldProbe(now time.Time, window time.Duration) bool {
	if !g.lastProbe.IsZero() && now.Sub(g.lastProbe) < window {
		return false
	}
	g.lastProbe = now
	return true
}

// decide 每个报价周期调一次。
//
//	openSince: 熔断连续打开的起点(零值 = 此刻不在熔断里),来自 RateLimitStatus;
//	window   : 限流窗口,恢复防抖的单位;
//	downAfter: 站下阈值,见 standDownAfter。
func (g *standDownGate) decide(openSince time.Time, window, downAfter time.Duration, now time.Time) standDownAction {
	if openSince.IsZero() {
		if g.healthySince.IsZero() {
			g.healthySince = now
		}
	} else {
		// 只要还在熔断里,健康计时就清零重来 —— 半程被打断不算数,
		// 否则"每隔 19 秒健康一下"也能攒够 20 秒,防抖就白做了。
		g.healthySince = time.Time{}
	}

	if g.down {
		if !g.healthySince.IsZero() && now.Sub(g.healthySince) >= time.Duration(standUpHealthyWindows)*window {
			g.down = false
			return standDownExit
		}
		return standDownHold
	}
	if !openSince.IsZero() && now.Sub(openSince) >= downAfter {
		g.down = true
		return standDownEnter
	}
	return standDownNone
}
