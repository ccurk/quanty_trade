package marketmaker

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// gate_ratelimit.go 给 gate 下单路径补上 binance 那条腿早就有、gate 一直没有的
// 那套防护(台账 #109)。对照组是 internal/exchange/binance.go:91-226 + 866-896:
//
//	binance:  rateLimitMu 护住几个标量 → RateLimited() 请求前本地快速拒绝
//	          → 429 时读 Retry-After / body.retryAfter → setBan(until) → 窗口内全拒
//	gate :    同样的 mu + 同样的 RateLimited() 快拒 + 同样的 Retry-After → setBan
//
// 【唯一一处刻意的分歧】binance 不需要令牌桶/滑窗,因为它在每个响应头里回
// X-MBX-USED-WEIGHT-1m,用量是【交易所告诉你的】,本地只需在 80% 时协作性 sleep
// (binance.go:858-861)。gate 不回这个头,而 台账 #84 实测的约束是一个硬窗口
// ——10 请求 / 10 秒(UID 级)。所以 gate 这边必须自己【前置】算账:多一个滑动窗口。
// 其余部分(命名、熔断语义、Retry-After 解析、ban 窗口)一律照 binance 抄,
// 两条腿保持同一套心智模型。
//
// ─────────────────────────────────────────────────────────────────────────────
// 【为什么撤单不能和下单共用一个额度池】——本文件最重要的一个取舍
//
// 做市引擎 refresh_ms=1000(config.go PairConfig.refresh),每轮要 OpenOrders +
// Balances + 撤 N + 挂 N,需求约 4~6 请求/秒,而惩罚档预算是 1 请求/秒。
// 桶【长期是空的】,这正是 #84 量到 25%+ 拒单率的成因。
//
// 若两类请求共用一个池:报价刷新会把额度吃干,撤单排在后面拿不到名额 →
// 挂在盘口的单撤不掉 → 实盘裸露的方向性敞口。这不是概率事件,是必然:
// 报价请求的到达率恒定高于预算,先到先得的池对撤单就是饥饿。
//
// 取舍:【按类分档,给撤单留固定预留名额】(ReservedCancel,默认 10 里留 3)。
//   - gateClassCritical(撤单/救腿):看得见【全部】10 个名额,并且可以等
//     (最多 MaxWaitMs),因为撤单【必须成功】,晚 200ms 撤掉远好于撤不掉。
//   - gateClassQuote(下单/读挂单/读余额):只看得见 10−3=7 个名额,且【从不等待】,
//     等到名额时价格早就陈了,引擎下一轮本来就会用新价重算。
//
// 没选"撤单单独开一个窗口"的原因:交易所那 10 个名额是【一个】UID 池,
// 本地开两个独立窗口 = 本地记的账和交易所记的账对不上,加起来会超发。
// 预留制在同一个池上分级,总量恒等于交易所口径。
//
// 另一处同源取舍:【熔断打开时,撤单类照样放行】。binance 的 ban 窗口是一刀切
// 全拒(binance.go:826),照抄到 gate 会亲手制造上面那个裸奔场景 —— 熔断本身
// 变成"撤不掉单"的原因。所以这里熔断只挡报价类;撤单类绕过熔断、但仍走限流器。
// 代价是被真封禁时会多打几个注定失败的撤单请求 —— 这个代价远小于撤不掉单。
// ─────────────────────────────────────────────────────────────────────────────

// ErrGateRateLimited 是【本地】限流器/熔断拒绝的请求,尚未发出去。
// 与交易所回的 429 区分开:本地拒绝意味着这一发请求没有消耗任何配额。
var ErrGateRateLimited = errors.New("gate: 本地限流拒绝")

// gateReqClass 把请求分成两档,决定它能看到多少名额、能不能等、能不能绕过熔断。
type gateReqClass int

const (
	// gateClassQuote:做市报价周期里的请求(下单、读挂单、读余额)。
	// 被限流时【失败即放弃】——引擎下一轮会用新价重算,重试一个陈价没有意义。
	gateClassQuote gateReqClass = iota
	// gateClassCritical:撤单/救腿。可等、可重试、可绕过熔断。
	gateClassCritical
)

func (c gateReqClass) String() string {
	if c == gateClassCritical {
		return "critical"
	}
	return "quote"
}

// gateBackoffBase 是 429 退避的首跳,之后翻倍直到 MaxBackoffMs 封顶。
// 不做成配置项:调档时该动的是窗口预算,不是退避首跳。
const gateBackoffBase = 200 * time.Millisecond

// gateLimiter 是滑动窗口限流器 + 429 熔断。
//
// 用滑动窗口而不是令牌桶:约束的原文就是"10 请求 / 10 秒"这个窗口。
// 令牌桶(速率 1/s、桶深 10)在最坏情况下允许 10 秒内发出 20 发(先爆 10 发
// 再匀速补 10 发),正好在被惩罚的账号上超发一倍。滑窗是这条约束的精确模型。
type gateLimiter struct {
	cfg RateLimitConfig

	// mu 护住下面全部字段。等价于 binance 的 rateLimitMu(binance.go:91),
	// 同样是为了消除并发读写标量的撕裂读 —— 撕裂成远期时间会冻结全部撤单。
	mu sync.Mutex
	// grants 是窗口内已发放名额的时刻,升序。长度上限 = cfg.Requests。
	grants []time.Time
	// consecutive429 连续 429 计数;任何一次成功清零。
	consecutive429 int
	// banUntil 是熔断打开到期时刻。语义与 binance 的 requestBanUntil 一致。
	banUntil time.Time
	// openSince 是当前这一【段】连续熔断的起点,只被 onSuccess() 清零。
	//
	// 它和 banUntil 问的不是同一个问题:
	//   banUntil  —— "这一刻请求会不会被本地快拒"(逐次冷却,到点自动失效);
	//   openSince —— "这段限流已经持续了多久没有真正恢复"。
	// 差别全在冷却到点、探针尚未成功的那段空窗:banUntil 已失效,但我们对
	// "交易所是否肯收我们的请求"仍然一无所知。用 banUntil 判"恢复"会让引擎在
	// 每个冷却边界上宣布一次恢复、又被下一发 429 立刻打回来 —— 熔断边缘横跳。
	// 所以只认【真实成功】这一个恢复证据。engine.go 的站下状态机读的就是它。
	openSince time.Time

	// now/sleep 只为单测注入假时钟;生产走 time.Now / time.Sleep。
	now   func() time.Time
	sleep func(time.Duration)
}

func newGateLimiter(cfg RateLimitConfig) *gateLimiter {
	return &gateLimiter{
		cfg:   cfg.defaults(),
		now:   time.Now,
		sleep: time.Sleep,
	}
}

// RateLimited 报告熔断是否打开。命名与 binance.go:183 的同名方法一致。
func (l *gateLimiter) RateLimited() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.banUntil.IsZero() && l.now().Before(l.banUntil)
}

// limitFor 返回某一档能看到的名额上限:撤单类看得见全部,报价类扣掉预留。
func (l *gateLimiter) limitFor(class gateReqClass) int {
	if class == gateClassCritical {
		return l.cfg.Requests
	}
	return l.cfg.Requests - l.cfg.ReservedCancel
}

// reserve 尝试当场占一个名额。占到返回 (true, 0);占不到返回 (false, 还需等多久)。
func (l *gateLimiter) reserve(class gateReqClass) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	window := time.Duration(l.cfg.WindowMs) * time.Millisecond
	// 丢掉滑出窗口的旧记录。grants 升序,所以找到第一个还在窗口内的即可。
	cut := 0
	for cut < len(l.grants) && !l.grants[cut].After(now.Add(-window)) {
		cut++
	}
	l.grants = l.grants[cut:]

	lim := l.limitFor(class)
	if len(l.grants) < lim {
		l.grants = append(l.grants, now)
		return true, 0
	}
	// 名额满了。要降到 lim 以下,得等第 len-lim 个记录滑出窗口。
	oldest := l.grants[len(l.grants)-lim]
	wait := oldest.Add(window).Sub(now)
	if wait < 0 {
		wait = 0
	}
	return false, wait
}

// acquire 取一个名额。报价类不等待、失败即返回;撤单类最多等 MaxWaitMs。
func (l *gateLimiter) acquire(class gateReqClass) error {
	ok, wait := l.reserve(class)
	if ok {
		return nil
	}
	if class != gateClassCritical {
		return fmt.Errorf("%w: %s 类名额耗尽(%d/%ds 窗口,给撤单预留 %d),本轮放弃",
			ErrGateRateLimited, class, l.cfg.Requests, l.cfg.WindowMs/1000, l.cfg.ReservedCancel)
	}

	maxWait := time.Duration(l.cfg.MaxWaitMs) * time.Millisecond
	var waited time.Duration
	for {
		if wait <= 0 {
			// 名额刚好到点却没拿到(并发抢占),退让一小步再试,避免忙等空转。
			wait = 5 * time.Millisecond
		}
		if waited+wait > maxWait {
			return fmt.Errorf("%w: %s 类等名额超过 %s 上限,放弃(避免撤单无限堆积)",
				ErrGateRateLimited, class, maxWait)
		}
		l.sleep(wait)
		waited += wait
		if ok, w := l.reserve(class); ok {
			return nil
		} else {
			wait = w
		}
	}
}

// onSuccess 由任一成功响应调用:清零连续 429 并关闭熔断。
// 这同时就是熔断的"半开"语义 —— banUntil 到点后放行的第一发请求即是探针,
// 成功则彻底恢复,再吃 429 则 consecutive429 仍在阈值上、立刻重新打开。
func (l *gateLimiter) onSuccess() {
	l.mu.Lock()
	l.consecutive429 = 0
	l.banUntil = time.Time{}
	l.openSince = time.Time{}
	l.mu.Unlock()
}

// breakerOpenSince 返回当前这一段连续熔断的起点;零值 = 不在熔断里。
// 引擎经 GateExchange.RateLimitStatus() 读它,用来决定是否撤单站下。
func (l *gateLimiter) breakerOpenSince() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.openSince
}

// on429 记一次被限流拒单。连续次数达到 BreakerFails 即打开熔断,
// 冷却时长优先用交易所给的 retryAfter,没有则用 BreakerCoolMs。
// 与 binance.go:868-891 同构,只是默认冷却从 2 分钟改成 30 秒(见 RateLimitConfig 注释)。
func (l *gateLimiter) on429(retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consecutive429++
	if l.consecutive429 < l.cfg.BreakerFails {
		return
	}
	cool := retryAfter
	if cool <= 0 {
		cool = time.Duration(l.cfg.BreakerCoolMs) * time.Millisecond
	}
	l.banUntil = l.now().Add(cool)
	// 只在【本段】第一次打开时打点。后面每次 429 都会把 banUntil 往后推,
	// 但这一段的起点不动 —— 站下判据要的是"持续了多久",不是"上次何时被推后"。
	if l.openSince.IsZero() {
		l.openSince = l.now()
	}
}

// backoffFor 返回第 attempt 次重试(从 0 起)前该睡多久:200ms 起翻倍,MaxBackoffMs 封顶。
// 交易所给了 Retry-After 就听它的,但同样受 MaxBackoffMs 封顶 —— 上限是硬的,
// 否则一个大 Retry-After 会把这条撤单和它后面的全堵住。
func (l *gateLimiter) backoffFor(attempt int, retryAfter time.Duration) time.Duration {
	max := time.Duration(l.cfg.MaxBackoffMs) * time.Millisecond
	d := retryAfter
	if d <= 0 {
		d = gateBackoffBase << attempt
	}
	if d > max {
		d = max
	}
	return d
}

// gateIsRateLimited 判断一次响应是不是被限流拒了。
// HTTP 429 是权威信号(与 binance.go:868 的 resp.StatusCode == 429 同源)。
// 标签匹配只是兜底:gate 在部分端点上会把限流原因写进 body 的 label 字段,
// 这几个串是【尽力而为】的补充,不是我核实过的完整清单 —— 判定主要靠状态码。
func gateIsRateLimited(status int, body []byte) bool {
	if status == 429 {
		return true
	}
	s := strings.ToUpper(string(body))
	return strings.Contains(s, "TOO_MANY_REQUESTS") ||
		strings.Contains(s, "RATE_LIMIT") ||
		strings.Contains(s, "REQUEST_RATE_LIMIT")
}

// gateRetryAfter 从响应头解出建议等待时长;没有或解不出返回 0。
// 与 binance.go:870-875 同样只认秒为单位的 Retry-After。
func gateRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	var secs float64
	if _, err := fmt.Sscanf(header, "%f", &secs); err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}
