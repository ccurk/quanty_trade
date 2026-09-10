package marketmaker

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gate_ratelimit_test.go 证明台账 #109 的限流真的生效,而不是"加了个结构体"。
//
// 时钟是注入的假时钟:窗口/退避都靠推进它来验证,单测里没有一次真实 sleep,
// 所以既确定又快(不会因为 CI 卡顿变成 flaky)。

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance 同时充当被注入的 sleep:限流器"睡"多久,假时钟就前进多久。
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newTestLimiter 造一个挂着假时钟的限流器。
func newTestLimiter(cfg RateLimitConfig) (*gateLimiter, *fakeClock) {
	l := newGateLimiter(cfg)
	c := newFakeClock()
	l.now = c.now
	l.sleep = c.advance
	return l, c
}

// TestGateLimiterWindowBlocksThenReleases 是最核心的一条:
// 打 N 个请求 → 窗口内被挡住 → 窗口滑过去 → 重新放行。
func TestGateLimiterWindowBlocksThenReleases(t *testing.T) {
	// 下单池 10 请求/10 秒(公告 40657 那一档)。下单池【不预留】——
	// 今天没有 critical 类的下单路径,所以报价类看得见全部 10 个(台账 #197)。
	l, clk := newTestLimiter(RateLimitConfig{Requests: 10, WindowMs: 10000, ReservedCancel: 3})

	const quotaForQuote = 10
	for i := 0; i < quotaForQuote; i++ {
		if err := l.acquire(gateBudgetOrder, gateClassQuote); err != nil {
			t.Fatalf("第 %d 个报价请求本应放行(预算 %d),却被拒: %v", i+1, quotaForQuote, err)
		}
	}
	// 第 11 个必须被挡住 —— 这就是"零限流"时代不存在的那道门。
	err := l.acquire(gateBudgetOrder, gateClassQuote)
	if err == nil {
		t.Fatalf("第 %d 个报价请求本应被限流挡住,却放行了", quotaForQuote+1)
	}
	if !errors.Is(err, ErrGateRateLimited) {
		t.Fatalf("被挡住时必须回 ErrGateRateLimited(调用方要能区分本地拒绝和交易所拒单),实际: %v", err)
	}

	// 窗口没滑完之前,仍然挡。9.9 秒时第一发还在窗口里。
	clk.advance(9900 * time.Millisecond)
	if err := l.acquire(gateBudgetOrder, gateClassQuote); err == nil {
		t.Fatal("窗口尚未滑过(9.9s < 10s),本应仍被挡住")
	}

	// 滑过 10 秒,最早那几发出窗,重新放行。
	clk.advance(200 * time.Millisecond)
	if err := l.acquire(gateBudgetOrder, gateClassQuote); err != nil {
		t.Fatalf("窗口已滑过,本应重新放行,却仍被拒: %v", err)
	}
}

// TestGateLimiterQueryPoolKeepsReservedQuota 验证预留名额那个设计取舍。
//
// 【为什么测的是查询池而不是下单池(台账 #197)】撤单本身已经不跟下单抢名额了
// (公告 40657 只把 POST/PATCH 算进那 10 个)。但 cancelAll 的第一步是【读挂单】,
// 它和报价周期的读挂单/读余额落在同一个查询池 —— 预留要护的就是这一步:
// 报价类的读把查询池打光时,cancelAll 仍然读得到列表。读不到 = 一张也撤不掉
// = 实盘裸露敞口,和原来那条不变式是同一条。
func TestGateLimiterQueryPoolKeepsReservedQuota(t *testing.T) {
	l, _ := newTestLimiter(RateLimitConfig{QueryRequests: 10, WindowMs: 10000, ReservedCancel: 3})

	// 报价类的读一路打到被拒为止(应当恰好放行 10-3=7 个)。
	granted := 0
	for i := 0; i < 50; i++ {
		if err := l.acquire(gateBudgetQuery, gateClassQuote); err != nil {
			break
		}
		granted++
	}
	if granted != 7 {
		t.Fatalf("报价类的读应当只看得见 10-3=7 个查询名额,实际放行 %d 个", granted)
	}

	// 此刻报价类已饿死,撤单路径的读必须还能连拿 3 个预留名额。
	for i := 0; i < 3; i++ {
		if err := l.acquire(gateBudgetQuery, gateClassCritical); err != nil {
			t.Fatalf("撤单路径第 %d 发读本应拿到预留名额,却被拒: %v", i+1, err)
		}
	}
	// 10 个全用完之后,撤单路径的读也该被拦 —— 预留是优先权,不是无限额度。
	if err := l.acquire(gateBudgetQuery, gateClassCritical); err == nil {
		t.Fatal("查询池 10 个名额已用尽,撤单路径的读也必须被挡(否则本地记账会超发)")
	}

	// 关键:查询池被打光的同时,下单池【一个名额都没被动过】。
	// 这就是 #197 的核心 —— 读和写不是一本账。
	for i := 0; i < 10; i++ {
		if err := l.acquire(gateBudgetOrder, gateClassQuote); err != nil {
			t.Fatalf("查询池耗尽不该影响下单池,第 %d 发下单却被拒: %v", i+1, err)
		}
	}
}

// TestGateLimiterCriticalWaitsForSlot 验证撤单类会【等】名额而不是直接放弃,
// 且等待有上限、不会无限堆积。
func TestGateLimiterCriticalWaitsForSlot(t *testing.T) {
	// 2 请求/1 秒、不预留,便于把池打满。
	l, clk := newTestLimiter(RateLimitConfig{
		Requests: 2, WindowMs: 1000, ReservedCancel: 0, MaxWaitMs: 3000,
	})
	start := clk.now()
	for i := 0; i < 2; i++ {
		if err := l.acquire(gateBudgetOrder, gateClassCritical); err != nil {
			t.Fatalf("前 2 发本应放行: %v", err)
		}
	}
	// 第 3 发:池满,但撤单类应当等到窗口滑过后拿到名额(而不是报错)。
	if err := l.acquire(gateBudgetOrder, gateClassCritical); err != nil {
		t.Fatalf("撤单类本应等待名额而不是放弃: %v", err)
	}
	if waited := clk.now().Sub(start); waited < 1000*time.Millisecond {
		t.Fatalf("撤单类应当真的等满一个窗口(1s),实际只等了 %s", waited)
	}

	// 报价类在同样的情况下【绝不等待】,立刻失败。
	l2, clk2 := newTestLimiter(RateLimitConfig{
		Requests: 2, WindowMs: 1000, ReservedCancel: 0, MaxWaitMs: 3000,
	})
	for i := 0; i < 2; i++ {
		_ = l2.acquire(gateBudgetOrder, gateClassQuote)
	}
	t0 := clk2.now()
	if err := l2.acquire(gateBudgetOrder, gateClassQuote); err == nil {
		t.Fatal("报价类不该等待,应当立刻失败让引擎跳过本轮")
	}
	if clk2.now() != t0 {
		t.Fatal("报价类不该消耗任何等待时间(陈价等不起)")
	}
}

// TestGateLimiterWaitIsCapped 证明退避/等待有上限:窗口大到永远等不来名额时,
// 撤单类必须在 MaxWaitMs 后放弃,而不是无限期堵住后面的撤单。
func TestGateLimiterWaitIsCapped(t *testing.T) {
	l, clk := newTestLimiter(RateLimitConfig{
		Requests: 1, WindowMs: 60000, ReservedCancel: 0, MaxWaitMs: 500,
	})
	if err := l.acquire(gateBudgetOrder, gateClassCritical); err != nil {
		t.Fatalf("第 1 发本应放行: %v", err)
	}
	start := clk.now()
	if err := l.acquire(gateBudgetOrder, gateClassCritical); err == nil {
		t.Fatal("等待超过上限后必须放弃,否则撤单会无限堆积")
	}
	if waited := clk.now().Sub(start); waited > 500*time.Millisecond {
		t.Fatalf("等待上限是 500ms,实际等了 %s", waited)
	}
}

// TestGateLimiterBreaker 验证熔断:连续 N 次 429 打开,成功后关闭。
func TestGateLimiterBreaker(t *testing.T) {
	l, clk := newTestLimiter(RateLimitConfig{
		Requests: 100, WindowMs: 10000, BreakerFails: 3, BreakerCoolMs: 30000,
	})
	for i := 0; i < 2; i++ {
		l.on429(0)
		if l.RateLimited() {
			t.Fatalf("连续 %d 次 429 未达阈值 3,熔断不该打开", i+1)
		}
	}
	l.on429(0)
	if !l.RateLimited() {
		t.Fatal("连续 3 次 429 达到阈值,熔断必须打开")
	}
	// 冷却没到,仍然打开。
	clk.advance(29 * time.Second)
	if !l.RateLimited() {
		t.Fatal("冷却期(30s)未到,熔断应仍打开")
	}
	// 冷却到点自动恢复(时间驱动,与 binance 的 ban 窗口一致)。
	clk.advance(2 * time.Second)
	if l.RateLimited() {
		t.Fatal("冷却期已过,熔断应自动恢复")
	}
	// 一次成功彻底清零计数,否则下一次单发 429 会立刻又把熔断顶开。
	l.onSuccess()
	l.on429(0)
	if l.RateLimited() {
		t.Fatal("成功之后计数应清零,单次 429 不该直接打开熔断")
	}
}

// TestGateLimiterBreakerHonorsRetryAfter 验证交易所给了 Retry-After 就听它的。
func TestGateLimiterBreakerHonorsRetryAfter(t *testing.T) {
	l, clk := newTestLimiter(RateLimitConfig{BreakerFails: 1, BreakerCoolMs: 30000})
	l.on429(5 * time.Second)
	if !l.RateLimited() {
		t.Fatal("阈值为 1,一次 429 就该打开熔断")
	}
	clk.advance(6 * time.Second)
	if l.RateLimited() {
		t.Fatal("Retry-After=5s 应当覆盖默认的 30s 冷却")
	}
}

func TestGateBackoffCapped(t *testing.T) {
	l, _ := newTestLimiter(RateLimitConfig{MaxBackoffMs: 1000})
	if got := l.backoffFor(0, 0); got != gateBackoffBase {
		t.Fatalf("首跳应为 %s,实际 %s", gateBackoffBase, got)
	}
	if got := l.backoffFor(1, 0); got != 2*gateBackoffBase {
		t.Fatalf("第二跳应翻倍,实际 %s", got)
	}
	// 翻到第 5 跳是 6.4s,必须被 1s 上限钳住。
	if got := l.backoffFor(5, 0); got != time.Second {
		t.Fatalf("退避必须被 MaxBackoffMs 钳住在 1s,实际 %s", got)
	}
	// 交易所给的 Retry-After 同样受上限约束,否则一个大值会堵死撤单队列。
	if got := l.backoffFor(0, time.Minute); got != time.Second {
		t.Fatalf("超大 Retry-After 也必须被钳到 1s,实际 %s", got)
	}
}

func TestGateIsRateLimited(t *testing.T) {
	if !gateIsRateLimited(429, nil) {
		t.Fatal("HTTP 429 是权威信号")
	}
	if !gateIsRateLimited(400, []byte(`{"label":"TOO_MANY_REQUESTS"}`)) {
		t.Fatal("body 里的限流标签应当兜底识别")
	}
	if gateIsRateLimited(400, []byte(`{"label":"INVALID_PARAM"}`)) {
		t.Fatal("普通业务错误不能被误判成限流(否则会白白触发熔断)")
	}
}

func TestGateRetryAfterParse(t *testing.T) {
	if got := gateRetryAfter("3"); got != 3*time.Second {
		t.Fatalf("Retry-After: 3 应解析为 3s,实际 %s", got)
	}
	if got := gateRetryAfter(""); got != 0 {
		t.Fatalf("空 Retry-After 应为 0,实际 %s", got)
	}
	if got := gateRetryAfter("not-a-number"); got != 0 {
		t.Fatalf("无法解析时应为 0,实际 %s", got)
	}
}

func TestRateLimitConfigDefaults(t *testing.T) {
	d := RateLimitConfig{}.defaults()
	if d.Requests != 10 || d.WindowMs != 10000 {
		t.Fatalf("默认应为台账 #84 实测的 10 请求/10 秒,实际 %d/%dms", d.Requests, d.WindowMs)
	}
	// 只改窗口预算的人不该被迫把 7 个字段全填一遍。
	partial := RateLimitConfig{Requests: 20}.defaults()
	if partial.Requests != 20 || partial.WindowMs != 10000 || partial.ReservedCancel != 3 {
		t.Fatalf("逐字段填默认失效: %+v", partial)
	}
	// 三个池各有各的默认值(台账 #197):下单 10、撤单 500、查询 200,共用一个窗口。
	if d.CancelRequests != 500 || d.QueryRequests != 200 {
		t.Fatalf("撤单/查询池默认应为 500/200,实际 %d/%d", d.CancelRequests, d.QueryRequests)
	}
	// 预留名额不能吃光它所在的那个池(查询池),否则报价类的读永远拿不到名额。
	starved := RateLimitConfig{QueryRequests: 2, ReservedCancel: 5}.defaults()
	if starved.ReservedCancel != 1 {
		t.Fatalf("预留应被钳到 QueryRequests-1=1,实际 %d", starved.ReservedCancel)
	}
}

// ── 端到端:走真实的 signed() 打一个假 gate,验证 429 识别 / 重试 / 熔断反馈 ──

func newRateLimitedGate(t *testing.T, status int, body string, cfg RateLimitConfig) (*GateExchange, *int32, *fakeClock, func()) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	l, clk := newTestLimiter(cfg)
	ex := &GateExchange{
		baseURL: srv.URL,
		apiKey:  "test-key",
		secret:  "test-secret",
		http:    srv.Client(),
		filters: map[string]SymbolFilter{},
		limiter: l,
	}
	return ex, &hits, clk, srv.Close
}

// TestGateSignedQuoteDoesNotRetry:报价类吃到 429 只打一发,不重试。
// 重试一个陈价既浪费额度又抢撤单的名额。
func TestGateSignedQuoteDoesNotRetry(t *testing.T) {
	ex, hits, _, closeFn := newRateLimitedGate(t, 429, `{"label":"TOO_MANY_REQUESTS"}`,
		RateLimitConfig{Requests: 10, WindowMs: 10000, MaxRetries: 3})
	defer closeFn()

	if _, err := ex.PlaceLimit("SOL_USDT", "buy", 100, 1, "GTC", true); err == nil {
		t.Fatal("交易所回 429,下单必须报错")
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("报价类不该重试,应只打 1 发,实际 %d 发", n)
	}
}

// TestGateSignedCancelRetriesWithBackoff:撤单类吃到 429 会退避重试到上限次数。
func TestGateSignedCancelRetriesWithBackoff(t *testing.T) {
	ex, hits, _, closeFn := newRateLimitedGate(t, 429, `{"label":"TOO_MANY_REQUESTS"}`,
		RateLimitConfig{Requests: 100, WindowMs: 10000, MaxRetries: 3, MaxBackoffMs: 1000, BreakerFails: 99})
	defer closeFn()

	if err := ex.CancelOrder("SOL_USDT", "12345"); err == nil {
		t.Fatal("一直 429 时撤单最终必须报错")
	}
	// 1 次首发 + 3 次重试 = 4 发,且必须【就此打住】(退避有上限)。
	if n := atomic.LoadInt32(hits); n != 4 {
		t.Fatalf("撤单应为 1 首发 + MaxRetries(3) = 4 发,实际 %d 发", n)
	}
}

// TestGateSignedBreakerBlocksQuoteButNotCancel 是熔断那条分歧的证明:
// 熔断打开后报价类本地直接拒(一发不出网),撤单类照样出网。
func TestGateSignedBreakerBlocksQuoteButNotCancel(t *testing.T) {
	ex, hits, _, closeFn := newRateLimitedGate(t, 429, `{"label":"TOO_MANY_REQUESTS"}`,
		RateLimitConfig{Requests: 100, WindowMs: 10000, MaxRetries: 0, BreakerFails: 1, BreakerCoolMs: 30000})
	defer closeFn()

	// 先打一发把熔断顶开。
	_, _ = ex.PlaceLimit("SOL_USDT", "buy", 100, 1, "GTC", true)
	if !ex.limiter.RateLimited() {
		t.Fatal("BreakerFails=1,一次 429 后熔断必须打开")
	}
	before := atomic.LoadInt32(hits)

	// 报价类:本地拒绝,不出网。
	if _, err := ex.PlaceLimit("SOL_USDT", "buy", 100, 1, "GTC", true); !errors.Is(err, ErrGateRateLimited) {
		t.Fatalf("熔断打开时报价类应被本地拒绝,实际: %v", err)
	}
	if n := atomic.LoadInt32(hits); n != before {
		t.Fatalf("熔断打开时报价类不该出网,却多打了 %d 发", n-before)
	}

	// 撤单类:必须绕过熔断出网 —— 否则熔断自己成了"撤不掉单"的原因。
	_ = ex.CancelOrder("SOL_USDT", "12345")
	if n := atomic.LoadInt32(hits); n <= before {
		t.Fatal("熔断打开时撤单必须仍能出网(挂着的单撤不掉 = 裸露敞口)")
	}
}

// ── 台账 #197:读写分池 —— 用引擎真实节拍造现场 ──────────────────────────

// engineReadsPerWindow 是引擎在【一个 10 秒窗口】里发出的读请求数。
// 不是拍的:marketmaker.json 里 4 个 pair、refresh_ms=1000,quote() 每轮
// 先 Balances() 再 OpenOrders()(engine.go 那段"余额在挂单之前"),
// 4 pair × 2 读 × 10 轮 = 80。这正是 #197 里"约 91% 的读被本地拒"的现场。
const (
	engineQuotePairs = 4
	engineCycles     = 10 // 10 秒窗口 / refresh_ms=1000
	engineReadsPer   = 2  // Balances + OpenOrders
)

// driveEngineReads 按引擎节拍打满一个窗口的读,返回 (放行数, 被拒数)。
// 每轮之间把假时钟推进 1 秒 —— 和 refresh_ms=1000 一致。
func driveEngineReads(l *gateLimiter, b gateBudget, clk *fakeClock) (granted, denied int) {
	for c := 0; c < engineCycles; c++ {
		for p := 0; p < engineQuotePairs; p++ {
			for r := 0; r < engineReadsPer; r++ {
				if err := l.acquire(b, gateClassQuote); err != nil {
					denied++
				} else {
					granted++
				}
			}
		}
		clk.advance(time.Second)
	}
	return
}

// TestEngineReadsStarveInSinglePoolButPassWhenSplit 是 #197 的正面证据:
// 同一批 80 个读,挤在原来那个 10/10s 单池里被拒掉九成,分池之后一个不拒。
//
// 【改前】用 QueryRequests=10 / ReservedCancel=3 复刻旧几何:一个 10 请求/10 秒的
// 池,报价类只看得见 7 个 —— 这与改动前 acquire(class) 的行为逐位一致。
// 【改后】查询池走默认 200/10s(见 config.go defaults 及 gate_ratelimit.go 头注出处)。
func TestEngineReadsStarveInSinglePoolButPassWhenSplit(t *testing.T) {
	const wantTotal = engineQuotePairs * engineCycles * engineReadsPer // 80

	// 改前:单池几何。
	oldL, oldClk := newTestLimiter(RateLimitConfig{QueryRequests: 10, WindowMs: 10000, ReservedCancel: 3})
	gotOK, gotDenied := driveEngineReads(oldL, gateBudgetQuery, oldClk)
	if gotOK+gotDenied != wantTotal {
		t.Fatalf("现场应当是 %d 个读,实际 %d", wantTotal, gotOK+gotDenied)
	}
	// 7 个名额撑满整个窗口,其余全拒 —— 即 73/80 ≈ 91%。
	if gotDenied != wantTotal-7 {
		t.Fatalf("单池下应当只放行 7 个读、拒掉 %d 个,实际放行 %d 拒 %d",
			wantTotal-7, gotOK, gotDenied)
	}
	t.Logf("改前(单池 10/10s,报价类可见 7):放行 %d / 拒 %d(拒单率 %.1f%%)",
		gotOK, gotDenied, 100*float64(gotDenied)/float64(wantTotal))

	// 改后:读走自己的查询池。
	newL, newClk := newTestLimiter(RateLimitConfig{WindowMs: 10000})
	gotOK, gotDenied = driveEngineReads(newL, gateBudgetQuery, newClk)
	if gotDenied != 0 {
		t.Fatalf("分池后 %d 个读应当【一个不拒】(查询池默认 200/窗口),实际拒了 %d 个",
			wantTotal, gotDenied)
	}
	if gotOK != wantTotal {
		t.Fatalf("分池后应当放行全部 %d 个读,实际 %d", wantTotal, gotOK)
	}

	// 而且这 80 个读没有占用下单池的任何一个名额:10 个下单名额原封不动。
	placed := 0
	for i := 0; i < 20; i++ {
		if err := newL.acquire(gateBudgetOrder, gateClassQuote); err != nil {
			break
		}
		placed++
	}
	if placed != 10 {
		t.Fatalf("下单池应当完好无损地剩 10 个名额(公告 40657 那一档),实际只拿到 %d", placed)
	}
}

// TestGateLimiterAdoptsRemoteLimit 验证限流器按【交易所报的】档位自适应,
// 而不是死守我们配的常量 —— 这是把 gate_ws.go 那三个被丢掉的字段接回来的目的。
func TestGateLimiterAdoptsRemoteLimit(t *testing.T) {
	l, clk := newTestLimiter(RateLimitConfig{Requests: 10, WindowMs: 10000})

	// 交易所说下单档位其实是 20 —— 照收,报价类立刻能看到 20 个。
	l.observeRemote(gateBudgetOrder, 20, 20, time.Time{})
	if got := l.remoteLimitFor(gateBudgetOrder); got != 20 {
		t.Fatalf("档位应当以交易所应答为准(20),实际 %d", got)
	}
	granted := 0
	for i := 0; i < 30; i++ {
		if err := l.acquire(gateBudgetOrder, gateClassQuote); err != nil {
			break
		}
		granted++
	}
	if granted != 20 {
		t.Fatalf("交易所报 20 档位就该放行 20 发,实际 %d", granted)
	}

	// 交易所改口说档位降到 5:必须【立刻收紧】,而不是等配置改完发版。
	// (此刻本地窗口里已经有 20 发,所以无论如何都发不出去 —— 这一步只验档位被改。)
	l.observeRemote(gateBudgetOrder, 5, 5, time.Time{})
	if got := l.remoteLimitFor(gateBudgetOrder); got != 5 {
		t.Fatalf("档位调低也必须照收(5),实际 %d", got)
	}
	clk.advance(11 * time.Second) // 让旧的 20 发全部滑出窗口
	granted = 0
	for i := 0; i < 30; i++ {
		if err := l.acquire(gateBudgetOrder, gateClassQuote); err != nil {
			break
		}
		granted++
	}
	if granted != 5 {
		t.Fatalf("档位降到 5 之后一个窗口只该放行 5 发,实际 %d", granted)
	}

	// 明显不可信的档位(比如把 reset_timestamp 误读成 limit)必须整条丢弃,
	// 否则等于把限流器关掉。
	before := l.remoteLimitFor(gateBudgetOrder)
	l.observeRemote(gateBudgetOrder, 1757500000, 1, time.Time{})
	if got := l.remoteLimitFor(gateBudgetOrder); got != before {
		t.Fatalf("超出合理区间的档位必须丢弃,档位却被改成 %d", got)
	}
}

// TestGateLimiterHonorsRemoteReset 单独验"剩余为 0"这条路径:
// 交易所说这个池空了,就等到【它给的】reset 时刻,而不是等本地窗口自己算的。
//
// 【为什么这条必须以交易所为准】同一个 UID 上不止我们一个进程在花额度
// (台账里那条"多 owner 共享单账户")。本地窗口记的是"我们以为自己发了多少",
// 只有交易所记的才是"它真的收了多少"。
func TestGateLimiterHonorsRemoteReset(t *testing.T) {
	// 全新的限流器:本地窗口一发没占,唯一能挡住请求的就是交易所报的剩余额度。
	l, clk := newTestLimiter(RateLimitConfig{Requests: 10, WindowMs: 10000})

	l.observeRemote(gateBudgetOrder, 10, 0, clk.now().Add(3*time.Second))
	if err := l.acquire(gateBudgetOrder, gateClassQuote); err == nil {
		t.Fatal("交易所说剩余为 0,本地必须停发(哪怕本地窗口还空着)")
	}
	clk.advance(2 * time.Second)
	if err := l.acquire(gateBudgetOrder, gateClassQuote); err == nil {
		t.Fatal("还没到交易所给的 reset 时刻,应当仍然停发")
	}
	clk.advance(1500 * time.Millisecond)
	if err := l.acquire(gateBudgetOrder, gateClassQuote); err != nil {
		t.Fatalf("过了交易所给的 reset 时刻应当放行,实际: %v", err)
	}

	// 已经过期的 reset 时刻不该把池锁死(否则一次时钟偏斜就冻住下单)。
	l2, clk2 := newTestLimiter(RateLimitConfig{Requests: 10, WindowMs: 10000})
	l2.observeRemote(gateBudgetOrder, 10, 0, clk2.now().Add(-time.Minute))
	if err := l2.acquire(gateBudgetOrder, gateClassQuote); err != nil {
		t.Fatalf("reset 时刻已过去,不该锁池: %v", err)
	}
}

func TestParseGateRateLimitHint(t *testing.T) {
	// 秒级时间戳。
	h := parseGateRateLimitHint("10", "3", "1757500000")
	if !h.OK || h.Limit != 10 || h.Remain != 3 {
		t.Fatalf("三个字段都该解出来,实际 %+v", h)
	}
	if h.ResetAt.Unix() != 1757500000 {
		t.Fatalf("秒级时间戳解错: %s", h.ResetAt)
	}
	// 毫秒级时间戳(单位官方没写,靠量级判)。
	if got := gateParseResetTimestamp("1757500000123"); got.UnixMilli() != 1757500000123 {
		t.Fatalf("毫秒级时间戳解错: %s", got)
	}
	// 没带 limit = 这条应答没有限流信息,整条不采纳。
	if h := parseGateRateLimitHint("", "3", "1757500000"); h.OK {
		t.Fatal("没有 limit 时不该声称解出了档位")
	}
	// remain 缺失 ≠ remain 为 0:必须是"未知"(-1),否则会把正常应答误判成额度耗尽。
	if h := parseGateRateLimitHint("10", "", ""); !h.OK || h.Remain != -1 {
		t.Fatalf("remain 缺失应为未知(-1),实际 %+v", h)
	}
	if got := gateParseResetTimestamp("not-a-number"); !got.IsZero() {
		t.Fatalf("解不出的时间戳应为零值,实际 %s", got)
	}
}

// TestGateWSAckCarriesRateLimitFields 钉死那三个字段【真的进了信封】——
// 它们此前被 gateWSAck 的 header 结构体整个丢掉,导致"当前是哪一档"只能靠翻日志。
func TestGateWSAckCarriesRateLimitFields(t *testing.T) {
	raw := []byte(`{"request_id":"qt-1","header":{"status":"200","channel":"spot.order_place",
		"x_gate_ratelimit_limit":"10","x_gate_ratelimit_requests_remain":"4",
		"x_gate_ratelimit_reset_timestamp":"1757500000"},"data":{"result":{"id":"1"}}}`)
	var ack gateWSAck
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatalf("解不动应答: %v", err)
	}
	h := ack.Header.rateLimitHint()
	if !h.OK || h.Limit != 10 || h.Remain != 4 || h.ResetAt.Unix() != 1757500000 {
		t.Fatalf("三个限流字段必须从信封里解出来,实际 %+v", h)
	}
	// 官方文档里 reset 那个字段写作 x_gat_...(疑似笔误),两种拼法都要认。
	raw2 := []byte(`{"request_id":"qt-2","header":{"status":"200",
		"x_gate_ratelimit_limit":"10","x_gat_ratelimit_reset_timestamp":"1757500001"}}`)
	var ack2 gateWSAck
	if err := json.Unmarshal(raw2, &ack2); err != nil {
		t.Fatalf("解不动应答: %v", err)
	}
	if got := ack2.Header.rateLimitHint(); got.ResetAt.Unix() != 1757500001 {
		t.Fatalf("官方文档那个少一个 e 的拼法也必须认,实际 %+v", got)
	}
	// 没带这些字段的老应答:OK=false,限流器原样退回本地常量,不能因此变坏。
	var ack3 gateWSAck
	if err := json.Unmarshal([]byte(`{"request_id":"qt-3","header":{"status":"200"}}`), &ack3); err != nil {
		t.Fatalf("解不动应答: %v", err)
	}
	if ack3.Header.rateLimitHint().OK {
		t.Fatal("应答没带限流字段时不该声称解出了档位")
	}
}
