package marketmaker

import (
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
	// 10 请求/10 秒、给撤单留 3 → 报价类只看得见 7 个名额。
	l, clk := newTestLimiter(RateLimitConfig{Requests: 10, WindowMs: 10000, ReservedCancel: 3})

	const quotaForQuote = 7
	for i := 0; i < quotaForQuote; i++ {
		if err := l.acquire(gateClassQuote); err != nil {
			t.Fatalf("第 %d 个报价请求本应放行(预算 %d),却被拒: %v", i+1, quotaForQuote, err)
		}
	}
	// 第 8 个必须被挡住 —— 这就是"零限流"时代不存在的那道门。
	err := l.acquire(gateClassQuote)
	if err == nil {
		t.Fatalf("第 %d 个报价请求本应被限流挡住,却放行了", quotaForQuote+1)
	}
	if !errors.Is(err, ErrGateRateLimited) {
		t.Fatalf("被挡住时必须回 ErrGateRateLimited(调用方要能区分本地拒绝和交易所拒单),实际: %v", err)
	}

	// 窗口没滑完之前,仍然挡。9.9 秒时第一发还在窗口里。
	clk.advance(9900 * time.Millisecond)
	if err := l.acquire(gateClassQuote); err == nil {
		t.Fatal("窗口尚未滑过(9.9s < 10s),本应仍被挡住")
	}

	// 滑过 10 秒,最早那几发出窗,重新放行。
	clk.advance(200 * time.Millisecond)
	if err := l.acquire(gateClassQuote); err != nil {
		t.Fatalf("窗口已滑过,本应重新放行,却仍被拒: %v", err)
	}
}

// TestGateLimiterCancelKeepsReservedQuota 验证本文件最重要的那个设计取舍:
// 报价类把自己那份额度打光之后,撤单【仍然拿得到名额】。
// 这一条如果不成立,就等于挂着的单撤不掉 = 实盘裸露敞口。
func TestGateLimiterCancelKeepsReservedQuota(t *testing.T) {
	l, _ := newTestLimiter(RateLimitConfig{Requests: 10, WindowMs: 10000, ReservedCancel: 3})

	// 报价类一路打到被拒为止(应当恰好放行 7 个)。
	granted := 0
	for i := 0; i < 50; i++ {
		if err := l.acquire(gateClassQuote); err != nil {
			break
		}
		granted++
	}
	if granted != 7 {
		t.Fatalf("报价类应当只看得见 10-3=7 个名额,实际放行 %d 个", granted)
	}

	// 此刻报价类已饿死,撤单必须还能连拿 3 个预留名额。
	for i := 0; i < 3; i++ {
		if err := l.acquire(gateClassCritical); err != nil {
			t.Fatalf("撤单第 %d 发本应拿到预留名额(报价类不该饿死撤单),却被拒: %v", i+1, err)
		}
	}
	// 10 个全用完之后,撤单也该被拦 —— 预留是给撤单优先,不是给它无限额度。
	if err := l.acquire(gateClassCritical); err == nil {
		t.Fatal("整个 UID 池 10 个名额已用尽,撤单也必须被挡(否则本地记账会超发)")
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
		if err := l.acquire(gateClassCritical); err != nil {
			t.Fatalf("前 2 发本应放行: %v", err)
		}
	}
	// 第 3 发:池满,但撤单类应当等到窗口滑过后拿到名额(而不是报错)。
	if err := l.acquire(gateClassCritical); err != nil {
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
		_ = l2.acquire(gateClassQuote)
	}
	t0 := clk2.now()
	if err := l2.acquire(gateClassQuote); err == nil {
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
	if err := l.acquire(gateClassCritical); err != nil {
		t.Fatalf("第 1 发本应放行: %v", err)
	}
	start := clk.now()
	if err := l.acquire(gateClassCritical); err == nil {
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
	// 预留名额不能吃光预算,否则报价类永远拿不到名额。
	starved := RateLimitConfig{Requests: 2, ReservedCancel: 5}.defaults()
	if starved.ReservedCancel != 1 {
		t.Fatalf("预留应被钳到 Requests-1=1,实际 %d", starved.ReservedCancel)
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
