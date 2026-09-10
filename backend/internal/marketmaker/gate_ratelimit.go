package marketmaker

import (
	"errors"
	"fmt"
	"strconv"
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
// 【交易所侧到底有几个配额池】—— 本文件最重要的口径,台账 #197 修正
//
// 【改之前错在哪】这里原本只有【一个】10 请求/10 秒的池,下单 + 撤单 + 读挂单 +
// 读余额四类请求全从它里面取名额。那是把一条【只管下单/改单的惩罚档】当成了
// 整个 UID 的总闸门。按官方口径重算:引擎 4 对 8 腿、refresh=1000ms,每个 10 秒
// 窗口要发约 80 个读(每 pair 每轮 Balances + OpenOrders 各一次),而报价类在单池
// 里只看得见 7 个名额 —— 约 91% 的读会被【我们自己】拒掉,盘口因此报不出去。
// 这正是 09-09 日报里"20 个周期 19 个盘口是空的"那条链的起点:
// 读被本地拒 → 余额避险 → cancelAll 走平 → 名额滑出窗口才挂回来。
//
// 【官方口径,逐条出处】gate 是三套互不相干的配额,不是一个:
//
//	① 下单/改单惩罚档 —— 10 请求 / 10 秒 / UID
//	   Gate 公告 40657 "API Rate Limit Rules Adjustments for Enhanced Spot
//	   Trading" 原文点名的端点是:"The order placement (POST /spot/orders) and
//	   order modification (PATCH /spot/orders/{order_id}) API endpoints",
//	   限额是 "a maximum of 10 requests per 10 seconds based on UID"。
//	   https://www.gate.com/announcements/article/40657/gate.io-api-rate-limit-rules-adjustments-for-enhanced-spot-trading
//	   ⚠️ 该公告【只点名 POST 与 PATCH】。撤单出现在成交率公式的分母里
//	   ("Fill Ratio = (Trade Volume in USDT)/(sum of new and modification and
//	   cancellation requests)")—— 那是【决定你掉不掉进惩罚档】的统计口径,
//	   不是这 10 个名额的占用者。两件事必须分开,混起来就是本条缺陷本身。
//	   (永续那条公告 39603 的措辞不同:"All requests, including successful and
//	   failed order placements, cancellations, and modifications…will be
//	   counted",档位 10r/10s 或 20r/10s。本文件只管现货,按 40657。)
//
//	② 撤单 —— 文档基线 5000r/s
//	   Gate API v4 文档 Frequency limit rule 表:
//	   "Spot | Cancel orders | 5000r/s | User ID | Canceling(all)orders,etc."
//	   同表亦见 github.com/gateio/rest-v4 README 的 API Performance 段。
//
//	③ 查询(读挂单 GET /spot/orders、读账户 GET /spot/accounts)—— 文档基线 900r/s
//	   同表:"Spot | Private endpoints | 900r/s | API Key | Trading history,
//	   fee rate,etc."
//
// 【我不确定的那一格,标出来】"查询走 200r/10s" 这个数我【没有】拿到官方原文。
// 我能读到的官方表格写的是 900r/s(≈9000/10s)。所以 ③ 的默认值取 200/10s
// (=20r/s):比我能引到的官方数字保守 45 倍,比引擎实际需要的 80/10s 宽 2.5 倍,
// 且可配(rate_limit.query_requests)。宁可自己先卡住,也不拿一个查不到出处的
// 数去撞交易所。② 同理默认取 500/10s,而不是照抄 5000r/s。
//
// 【三个池必须各自独立计窗,而不是"一个池分档"】原注释里"本地开两个窗口会和
// 交易所对不上账"这条只在【同一个交易所池】里成立。既然 ①②③ 在交易所那边就是
// 三本账,本地也必须记三本 —— 记成一本才是对不上账。
//
// 【预留名额(ReservedCancel)现在护的是哪一格】撤单本身已经不跟下单抢名额了,
// 但 cancelAll 的第一步是【读挂单】(types.go CancelPathReader),它和报价周期的
// 读挂单/读余额落在同一个查询池 ③。读storm 下饿死的就会是这一步:列表读不到,
// 一张也撤不掉。所以预留制原封不动地保留,只是搬到它现在真正有争用的那个池上:
//   - 池 ③ 查询:critical 看得见全部名额,quote 只看得见 QueryRequests−ReservedCancel。
//   - 池 ① 下单:不预留 —— 今天没有任何 critical 类的下单路径(站下只撤单不下单,
//     见 standdown.go),留 3 个给没有人的一档 = 白扔掉 30% 的报价预算。
//   - 池 ② 撤单:只有 critical 一类,预留无意义。
//
// 【熔断仍然是一个,且语义不动】429 是交易所对这个 UID 说"退一步",它不分池。
// 沿用原语义:熔断只挡报价类,撤单类绕过熔断但仍走限流器 —— binance 的 ban 窗口
// 是一刀切全拒(binance.go:826),照抄会让熔断本身变成"撤不掉单"的原因。
// 代价是被真封禁时会多打几个注定失败的撤单请求,远小于撤不掉单。
//
// 【自适应:别再靠猜】上面每个默认值都是我们【猜】的档位。gate 在 WS 应答信封的
// header 里回三个字段(x_gate_ratelimit_limit / x_gate_ratelimit_requests_remain /
// x_gate_ratelimit_reset_timestamp),那是交易所自己报的【当前真实档位和剩余】。
// observeRemote() 拿到就覆盖本地常量:档位以交易所为准,剩余为 0 时直接等到它给的
// reset 时刻。这样调档不再需要改配置发版,也不用再翻 26,704 条日志考古。
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

// gateBudget 是【交易所侧的配额池】,与 gateReqClass 是两根互相垂直的轴:
//
//	gateBudget    —— 这一发请求花的是【谁的】名额(交易所怎么记账,见文件头 ①②③)
//	gateReqClass  —— 这一发请求配享受什么【待遇】(能不能等、能不能绕过熔断)
//
// 原来只有 class 一根轴,于是"记谁的账"被迫和"什么待遇"绑在一起 —— 那就是
// #197 的形状:读挂单因为"待遇和下单一样"而被扣了下单的名额。
type gateBudget int

const (
	// gateBudgetOrder:POST /spot/orders、PATCH /spot/orders/{id}。
	// 这是公告 40657 点名的那 10/10s 惩罚档,也是四类请求里【唯一】进这个池的。
	gateBudgetOrder gateBudget = iota
	// gateBudgetCancel:DELETE /spot/orders/{id} 及批量撤单。文档基线 5000r/s。
	gateBudgetCancel
	// gateBudgetQuery:GET /spot/orders、GET /spot/accounts 等私有查询。
	gateBudgetQuery
	gateBudgetCount
)

// gateBudgetSpec 把"这个池是谁、装哪些端点、出处在哪"写进代码,而不是只留一个常量。
// 六个月后的人 grep 到某个池,能就地读到它为什么和别的池不在一起。
type gateBudgetSpec struct {
	name      string
	endpoints string
	source    string
}

var gateBudgetSpecs = [gateBudgetCount]gateBudgetSpec{
	gateBudgetOrder: {
		name:      "order",
		endpoints: "POST /spot/orders, PATCH /spot/orders/{id}",
		source:    "Gate 公告 40657:10 requests per 10 seconds based on UID(只点名下单与改单)",
	},
	gateBudgetCancel: {
		name:      "cancel",
		endpoints: "DELETE /spot/orders/{id}",
		source:    "Gate APIv4 Frequency limit rule 表:Spot | Cancel orders | 5000r/s | User ID",
	},
	gateBudgetQuery: {
		name:      "query",
		endpoints: "GET /spot/orders, GET /spot/accounts",
		source:    "Gate APIv4 Frequency limit rule 表:Spot | Private endpoints | 900r/s | API Key",
	},
}

func (b gateBudget) String() string {
	if b < 0 || b >= gateBudgetCount {
		return "unknown"
	}
	return gateBudgetSpecs[b].name
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
	// pools 每个交易所配额池一格,互不相干地各记各的窗口(见文件头 ①②③)。
	pools [gateBudgetCount]gateWindow
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

// gateWindow 是【一个】配额池的滑动窗口。
type gateWindow struct {
	// limit 是当前生效的窗口名额。初值来自配置(我们猜的档位),
	// 一旦交易所在应答里报了真实档位就被 observeRemote 覆盖。
	limit int
	// remoteLimit 记住交易所报过的档位;非 0 表示这个池的 limit 不再是猜的。
	// 单独留一个字段而不是只看 limit,是为了让日志/排障能一眼看出"这个数
	// 是交易所说的还是我们配的"—— 这正是这次要消灭的那种考古。
	remoteLimit int
	// grants 是窗口内已发放名额的时刻,升序。长度上限 = limit。
	grants []time.Time
	// blockedUntil 来自交易所报的"剩余 0 + reset 时刻":在此之前这个池一发不发。
	// 它比本地窗口权威 —— 本地窗口记的是我们【以为】发了多少,
	// 交易所记的是它【真的】收了多少(别的进程/别的 owner 也在花同一个 UID 的额度)。
	blockedUntil time.Time
}

func newGateLimiter(cfg RateLimitConfig) *gateLimiter {
	c := cfg.defaults()
	l := &gateLimiter{
		cfg:   c,
		now:   time.Now,
		sleep: time.Sleep,
	}
	l.pools[gateBudgetOrder].limit = c.Requests
	l.pools[gateBudgetCancel].limit = c.CancelRequests
	l.pools[gateBudgetQuery].limit = c.QueryRequests
	return l
}

// RateLimited 报告熔断是否打开。命名与 binance.go:183 的同名方法一致。
func (l *gateLimiter) RateLimited() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.banUntil.IsZero() && l.now().Before(l.banUntil)
}

// reservedIn 返回某个池里留给 critical 的名额。
//
// 只有查询池 ③ 需要预留:cancelAll 的读挂单(critical)和报价周期的读挂单/读余额
// (quote)在这里真的会抢。下单池 ① 今天没有 critical 的下单路径,撤单池 ② 只有
// critical —— 那两格预留就是白扔预算。详见文件头"预留名额现在护的是哪一格"。
func (l *gateLimiter) reservedIn(b gateBudget) int {
	if b == gateBudgetQuery {
		return l.cfg.ReservedCancel
	}
	return 0
}

// limitFor 返回某一档在某个池里能看到的名额上限:critical 看得见全部,quote 扣掉预留。
func (l *gateLimiter) limitFor(b gateBudget, class gateReqClass) int {
	lim := l.pools[b].limit
	if class == gateClassCritical {
		return lim
	}
	if r := l.reservedIn(b); r < lim {
		return lim - r
	}
	return 1 // 预留不许把报价类饿到 0,否则永远不报价(与 config.go defaults() 同一条防呆)
}

// reserve 尝试在指定池里当场占一个名额。占到返回 (true, 0);占不到返回 (false, 还需等多久)。
func (l *gateLimiter) reserve(b gateBudget, class gateReqClass) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	w := &l.pools[b]

	// 交易所说过"这个池已经空了、X 时刻才恢复"就以它为准,本地窗口无权覆盖。
	if !w.blockedUntil.IsZero() {
		if now.Before(w.blockedUntil) {
			return false, w.blockedUntil.Sub(now)
		}
		w.blockedUntil = time.Time{}
	}

	window := time.Duration(l.cfg.WindowMs) * time.Millisecond
	// 丢掉滑出窗口的旧记录。grants 升序,所以找到第一个还在窗口内的即可。
	cut := 0
	for cut < len(w.grants) && !w.grants[cut].After(now.Add(-window)) {
		cut++
	}
	w.grants = w.grants[cut:]

	lim := l.limitFor(b, class)
	if len(w.grants) < lim {
		w.grants = append(w.grants, now)
		return true, 0
	}
	// 名额满了。要降到 lim 以下,得等第 len-lim 个记录滑出窗口。
	oldest := w.grants[len(w.grants)-lim]
	wait := oldest.Add(window).Sub(now)
	if wait < 0 {
		wait = 0
	}
	return false, wait
}

// acquire 从指定池取一个名额。报价类不等待、失败即返回;撤单类最多等 MaxWaitMs。
func (l *gateLimiter) acquire(b gateBudget, class gateReqClass) error {
	ok, wait := l.reserve(b, class)
	if ok {
		return nil
	}
	if class != gateClassCritical {
		l.mu.Lock()
		lim, remote := l.pools[b].limit, l.pools[b].remoteLimit
		l.mu.Unlock()
		src := "本地配置"
		if remote > 0 {
			src = "交易所应答"
		}
		return fmt.Errorf("%w: %s 池 %s 类名额耗尽(%d/%ds 窗口,档位来自%s,端点 %s),本轮放弃",
			ErrGateRateLimited, b, class, lim, l.cfg.WindowMs/1000, src, gateBudgetSpecs[b].endpoints)
	}

	maxWait := time.Duration(l.cfg.MaxWaitMs) * time.Millisecond
	var waited time.Duration
	for {
		if wait <= 0 {
			// 名额刚好到点却没拿到(并发抢占),退让一小步再试,避免忙等空转。
			wait = 5 * time.Millisecond
		}
		if waited+wait > maxWait {
			return fmt.Errorf("%w: %s 池 %s 类等名额超过 %s 上限,放弃(避免撤单无限堆积)",
				ErrGateRateLimited, b, class, maxWait)
		}
		l.sleep(wait)
		waited += wait
		if ok, w := l.reserve(b, class); ok {
			return nil
		} else {
			wait = w
		}
	}
}

// gateRemoteLimitSane 是 observeRemote 接受的档位区间。
// 上界不是洁癖:一个解析错位(把 reset_timestamp 当成 limit 读)会返回十亿量级,
// 照单全收就等于把限流器关掉。超出区间即整条应答不可信,一个字段都不采纳。
const gateRemoteLimitSane = 100000

// observeRemote 用【交易所自己报的】档位和剩余额度覆盖本地猜的常量。
//
// 输入来自 gate 应答里的三个字段(WS 见 gate_ws.go gateWSAck.Header,REST 见
// gate.go signedOnce 读的同名响应头):
//
//	limit   = x_gate_ratelimit_limit             当前档位的名额总数
//	remain  = x_gate_ratelimit_requests_remain   这个窗口还剩几个
//	resetAt = x_gate_ratelimit_reset_timestamp   额度重算的时刻
//
// 为什么这是本轮最值钱的一段:档位是 gate 按成交率动态调的(台账 #84),
// 我们配的 10/10s 只是【某一天量到的那一档】。有了这三个字段,档位变化本地
// 立刻知道,既不用改配置发版,也不用再靠翻日志考古"现在是哪一档"。
//
// limit<=0 或超出 gateRemoteLimitSane 视为没读到,静默忽略(不改任何状态)。
func (l *gateLimiter) observeRemote(b gateBudget, limit, remain int, resetAt time.Time) {
	if b < 0 || b >= gateBudgetCount || limit <= 0 || limit > gateRemoteLimitSane {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := &l.pools[b]
	w.remoteLimit = limit
	if w.limit != limit {
		// 交易所口径一律照收,包括调大 —— 这个数不是我们猜的,而是它自己报的。
		w.limit = limit
		if len(w.grants) > limit {
			w.grants = w.grants[len(w.grants)-limit:]
		}
	}
	// remain==0:交易所说这个池这一刻是空的。本地窗口可能还以为有富余(同一个 UID
	// 上还有别的进程在花额度,台账里那条"多 owner 共享单账户"),以它为准直接堵到 reset。
	if remain <= 0 && !resetAt.IsZero() && resetAt.After(l.now()) {
		w.blockedUntil = resetAt
	}
}

// remoteLimitFor 报告某个池的档位是否已经由交易所应答确认过(0 = 还在用本地猜的常量)。
// 给日志/排障用:回答"当前档位是什么"从此是 grep 一行的事。
func (l *gateLimiter) remoteLimitFor(b gateBudget) int {
	if b < 0 || b >= gateBudgetCount {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pools[b].remoteLimit
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

// gateRateLimitHint 是从 gate 应答里解出的"当前档位 + 剩余额度 + 何时重算"。
// OK=false 表示这条应答没带(或带坏了)这组字段,调用方原样忽略即可。
type gateRateLimitHint struct {
	Limit   int
	Remain  int
	ResetAt time.Time
	OK      bool
}

// parseGateRateLimitHint 把三个字段的【原始字符串】解成 hint。
//
// 为什么收字符串而不是数字:gate 在 WS 信封里把它们当字符串发(header 里其余字段
// 如 status 也是字符串),REST 侧则是 HTTP 头,两边天然都是字符串。统一在这里解,
// 两条出口共用同一份解析和同一套防呆。
//
// remain 缺失时按 -1 传入(= 未知,不触发 blockedUntil);limit 缺失/解不出即 OK=false。
func parseGateRateLimitHint(limit, remain, reset string) gateRateLimitHint {
	lim, err := strconv.Atoi(strings.TrimSpace(limit))
	if err != nil || lim <= 0 {
		return gateRateLimitHint{}
	}
	h := gateRateLimitHint{Limit: lim, Remain: -1, OK: true}
	if v, err := strconv.Atoi(strings.TrimSpace(remain)); err == nil && v >= 0 {
		h.Remain = v
	}
	h.ResetAt = gateParseResetTimestamp(reset)
	return h
}

// gateParseResetTimestamp 解 x_gate_ratelimit_reset_timestamp。
//
// 【单位是猜的,所以按量级判】官方文档只列了字段名,没写单位,我没有核实过。
// 按量级分支是唯一不会把秒当成毫秒(早 1000 倍解封 = 超发)或反过来
// (晚 1000 倍解封 = 冻住半小时)的做法:
//
//	>= 1e15 微秒 / >= 1e12 毫秒 / >= 1e9 秒 / 其余 视为解不出,返回零值
func gateParseResetTimestamp(s string) time.Time {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || v <= 0 {
		return time.Time{}
	}
	switch {
	case v >= 1e15:
		return time.UnixMicro(v)
	case v >= 1e12:
		return time.UnixMilli(v)
	case v >= 1e9:
		return time.Unix(v, 0)
	}
	return time.Time{}
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
