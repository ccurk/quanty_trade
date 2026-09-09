package marketmaker

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// standdown_test.go 证明三件事,一件都不许靠口述:
//
//	① engine.quote() 里那条「余额读失败就 cancelAll 避险」的路径【现在真的可达】。
//	   改动前它排在 OpenOrders 之后,持续限流下永远先在 OpenOrders 早返回 ——
//	   写了却从没执行过。TestQuoteBalanceHedgePathIsReachable 是这条的红→绿闸门。
//	② cancelAll 的读挂单那一步走撤单档,熔断打开时仍能读到并撤掉挂单;
//	   走报价档的话它在熔断下是空转 —— 所有避险路径的共同动作会一起失效。
//	③ 「限流 → 站下 → 恢复」整条链路在一个假 gate 上端到端跑得通,
//	   且两个参数(站下阈值、恢复防抖)按 standdown.go 里写的依据生效。

// ── ① / ② 用的假交易所:能分别编排"报价档读"和"撤单档读"的成败 ──

// hedgeExec 把 ExecExchange 的每个读口拆开控制,用来定位【哪一个读的失败分支
// 才会被执行】—— 这正是 ① 要问的问题。
type hedgeExec struct {
	mu sync.Mutex

	balErr        error // Balances() 的返回错误(nil = 成功)
	openErr       error // OpenOrders()(报价档)的返回错误
	cancelReadErr error // OpenOrdersForCancel()(撤单档)的返回错误

	resting   []OpenOrder
	cancelled []string
	placed    int
	// cancelReads 记录撤单档读被调用了几次 —— cancelAll 是否真的开始工作。
	cancelReads int
}

func (h *hedgeExec) Name() string        { return "hedge-fake" }
func (h *hedgeExec) SupportsShort() bool { return false }

func (h *hedgeExec) FetchBookTicker(string) (BookTicker, error) {
	return BookTicker{BidPx: 99.9, AskPx: 100.1, Ts: time.Now()}, nil
}

func (h *hedgeExec) SymbolFilter(string) (SymbolFilter, error) {
	return SymbolFilter{BaseAsset: "SOL", QuoteAsset: "USDT", TickSize: 0.01, StepSize: 0.001, MinNotional: 1}, nil
}

func (h *hedgeExec) Balances() (map[string]float64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.balErr != nil {
		return nil, h.balErr
	}
	return map[string]float64{"SOL": 10, "USDT": 1000}, nil
}

func (h *hedgeExec) OpenOrders(string) ([]OpenOrder, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.openErr != nil {
		return nil, h.openErr
	}
	return append([]OpenOrder(nil), h.resting...), nil
}

// OpenOrdersForCancel 让 hedgeExec 实现 CancelPathReader —— 模拟 gate 上撤单档
// 绕过熔断的那条腿:报价档读失败时它仍可能成功。
func (h *hedgeExec) OpenOrdersForCancel(string) ([]OpenOrder, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cancelReads++
	if h.cancelReadErr != nil {
		return nil, h.cancelReadErr
	}
	return append([]OpenOrder(nil), h.resting...), nil
}

func (h *hedgeExec) CancelOrder(_, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cancelled = append(h.cancelled, id)
	for i := range h.resting {
		if h.resting[i].ID == id {
			h.resting = append(h.resting[:i], h.resting[i+1:]...)
			break
		}
	}
	return nil
}

func (h *hedgeExec) PlaceLimit(string, string, float64, float64, string, bool) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.placed++
	return "id", nil
}

var (
	_ ExecExchange     = (*hedgeExec)(nil)
	_ CancelPathReader = (*hedgeExec)(nil)
)

// TestQuoteBalanceHedgePathIsReachable 是本轮的红→绿闸门(任务 ②)。
//
// 场景就是持续限流下的真实形态:两个读【都】是 gateClassQuote,都会失败。
// 谁排在前面,谁的失败分支才会被执行。
//
//	改动前(OpenOrders 在前):走「本轮不动单 → return」,一张单都不撤 ——
//	                          陈价挂单原地留在盘口上,而这正是我们撤不动它的时候;
//	                          「余额读失败就撤单避险」那段代码永远不执行。
//	改动后(Balances 在前):走「撤单避险」,cancelAll 真的跑起来。
//
// 断言的是"撤单档的读被调用过" —— 即 cancelAll 确实进入了工作状态,
// 而不是只看有没有撤成功(限流下本来就可能撤不成,那是另一回事)。
func TestQuoteBalanceHedgePathIsReachable(t *testing.T) {
	limited := fmt.Errorf("%w: 熔断打开(连续 429),报价类暂停出网", ErrGateRateLimited)
	ex := &hedgeExec{
		balErr:  limited,
		openErr: limited,
		resting: []OpenOrder{{ID: "resting-bid", Side: "BUY", Price: 99.5, Qty: 1}},
	}
	e := &Engine{cfg: Config{}}
	p := PairConfig{FeedSymbol: "SOLUSDT", Exec: "gate", ExecSymbol: "SOL_USDT",
		SpreadBps: 20, OrderQty: 1, MaxPosition: 10}
	ref := BookTicker{BidPx: 99.95, AskPx: 100.05, Ts: time.Now()}
	eb := BookTicker{BidPx: 99.9, AskPx: 100.1, Ts: time.Now()}

	e.quote(p, ex, ref, eb, 0)

	ex.mu.Lock()
	reads, cancels := ex.cancelReads, len(ex.cancelled)
	ex.mu.Unlock()
	if reads == 0 {
		t.Fatal("持续限流下「余额读失败 → cancelAll 避险」这条路径必须可达:" +
			"撤单档的读一次都没被调用,说明本轮又在 OpenOrders 早返回了(这条安全路径仍是死代码)")
	}
	if cancels != 1 {
		t.Fatalf("避险路径应当把那张陈价挂单撤掉,实际撤了 %d 张", cancels)
	}
	if ex.placed != 0 {
		t.Fatalf("读失败的一轮绝不允许挂新单,实际挂了 %d 张", ex.placed)
	}
}

// TestCancelAllUsesCancelClassRead 锁死 cancelAll 的读挂单必须走撤单档。
//
// 走报价档的话,熔断打开时那一步在本地就被拒 → 拿不到列表 → 一张也撤不掉,
// 于是【所有】以 cancelAll 为动作的避险路径(参考流过期/持续偏离/单日止损/
// 优雅关闭/熔断站下)在最该生效的时刻集体空转。
func TestCancelAllUsesCancelClassRead(t *testing.T) {
	ex := &hedgeExec{
		openErr: fmt.Errorf("%w: 熔断打开", ErrGateRateLimited), // 报价档:被熔断挡住
		resting: []OpenOrder{{ID: "a", Side: "BUY"}, {ID: "b", Side: "SELL"}},
	}
	(&Engine{}).cancelAll(ex, "SOL_USDT")

	ex.mu.Lock()
	defer ex.mu.Unlock()
	if ex.cancelReads != 1 {
		t.Fatalf("cancelAll 必须用撤单档读挂单,实际调用 %d 次", ex.cancelReads)
	}
	if len(ex.cancelled) != 2 {
		t.Fatalf("熔断打开时 cancelAll 仍必须撤掉全部 2 张挂单,实际撤了 %d 张(这就是空转)", len(ex.cancelled))
	}
}

// ── ③ 站下状态机:参数与防抖 ──

func TestStandDownAfterDerivation(t *testing.T) {
	// 默认 refresh=1000ms → 5 个报价周期 = 5s。
	if got := standDownAfter(PairConfig{}); got != 5*time.Second {
		t.Fatalf("默认配置下站下阈值应为 5s(5×refresh),实际 %s", got)
	}
	// refresh 很小时被 5s 下界兜住,免得阈值掉进 429 抖动的量级。
	if got := standDownAfter(PairConfig{RefreshMs: 200}); got != 5*time.Second {
		t.Fatalf("refresh=200ms 时应被 5s 下界兜住,实际 %s", got)
	}
	// refresh 大时随之放大:判据是"连续 5 轮挂不出单",不是一个绝对秒数。
	if got := standDownAfter(PairConfig{RefreshMs: 3000}); got != 15*time.Second {
		t.Fatalf("refresh=3000ms 时应为 15s,实际 %s", got)
	}
	// 站下阈值必须远小于默认冷却(30s),否则陈价会在盘口躺满整个惩罚窗口。
	if standDownAfter(PairConfig{}) >= time.Duration(RateLimitConfig{}.defaults().BreakerCoolMs)*time.Millisecond {
		t.Fatal("站下阈值不得达到默认冷却 30s —— 那等于把敞口留满整个惩罚窗口")
	}
}

// TestStandDownGateTiming 逐帧验状态机:阈值前不站下、阈值到站下、
// 恢复要等满 2 个限流窗口的【连续】健康,中途再熔断则健康计时清零重来。
func TestStandDownGateTiming(t *testing.T) {
	const window = 10 * time.Second
	const downAfter = 5 * time.Second
	t0 := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	var g standDownGate

	// 未熔断:正常报价。
	if a := g.decide(time.Time{}, window, downAfter, t0); a != standDownNone {
		t.Fatalf("未熔断时应正常报价,得到 %s", a)
	}
	// 熔断刚打开:还不到阈值,【不】站下 —— 短暂 429 抖动不该触发撤单。
	open := t0.Add(1 * time.Second)
	if a := g.decide(open, window, downAfter, open.Add(4900*time.Millisecond)); a != standDownNone {
		t.Fatalf("熔断打开 4.9s(<5s 阈值)不该站下,得到 %s", a)
	}
	// 到阈值:站下。
	if a := g.decide(open, window, downAfter, open.Add(downAfter)); a != standDownEnter {
		t.Fatalf("熔断持续 5s 达阈值必须站下,得到 %s", a)
	}
	// 站下中,熔断仍开:保持。
	now := open.Add(downAfter + time.Second)
	if a := g.decide(open, window, downAfter, now); a != standDownHold {
		t.Fatalf("熔断未恢复应保持站下,得到 %s", a)
	}

	// 熔断关闭(openSince 归零 = 有过一次真实成功),开始攒健康时长。
	healthyFrom := now.Add(time.Second)
	if a := g.decide(time.Time{}, window, downAfter, healthyFrom); a != standDownHold {
		t.Fatalf("刚恢复健康还没攒够时长,不该立刻站起来,得到 %s", a)
	}
	// 1 个窗口(10s)不够 —— 短于一个窗口的安静可能整个落在上一个窗口的尾巴里。
	if a := g.decide(time.Time{}, window, downAfter, healthyFrom.Add(19*time.Second)); a != standDownHold {
		t.Fatalf("健康 19s(<2×窗口)不该恢复,得到 %s", a)
	}
	// 攒够 2 个窗口:恢复。
	if a := g.decide(time.Time{}, window, downAfter, healthyFrom.Add(20*time.Second)); a != standDownExit {
		t.Fatalf("健康满 2×窗口=20s 应恢复报价,得到 %s", a)
	}
	if a := g.decide(time.Time{}, window, downAfter, healthyFrom.Add(21*time.Second)); a != standDownNone {
		t.Fatalf("恢复之后应回到正常报价,得到 %s", a)
	}
}

// TestStandDownRecoveryDebounceResets 是防抖的核心:健康被打断就重新计时,
// 否则"每隔 19 秒健康一下"也能攒够 20 秒,等于在熔断边缘反复横跳。
func TestStandDownRecoveryDebounceResets(t *testing.T) {
	const window = 10 * time.Second
	const downAfter = 5 * time.Second
	t0 := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	var g standDownGate
	open := t0
	g.decide(open, window, downAfter, open.Add(downAfter)) // 站下
	// 健康 19s……
	g.decide(time.Time{}, window, downAfter, open.Add(10*time.Second))
	if a := g.decide(time.Time{}, window, downAfter, open.Add(29*time.Second)); a != standDownHold {
		t.Fatalf("19s 健康不该恢复,得到 %s", a)
	}
	// ……被一次新的熔断打断。
	reopen := open.Add(30 * time.Second)
	if a := g.decide(reopen, window, downAfter, reopen); a != standDownHold {
		t.Fatalf("重新熔断时应保持站下,得到 %s", a)
	}
	// 打断后再健康 19s,累计早已超过 20s,但【连续】没到 → 仍不恢复。
	g.decide(time.Time{}, window, downAfter, reopen.Add(time.Second))
	if a := g.decide(time.Time{}, window, downAfter, reopen.Add(20*time.Second)); a != standDownHold {
		t.Fatal("健康计时必须在重新熔断时清零重来:累计够了但连续不够,不得恢复(这就是防横跳)")
	}
	// 连续满 20s 才放行。
	if a := g.decide(time.Time{}, window, downAfter, reopen.Add(21*time.Second)); a != standDownExit {
		t.Fatalf("连续健康满 20s 应恢复,得到 %s", a)
	}
}

// TestStandDownSkippedForAdaptersWithoutLimiter:不报告限流状态的适配器
// (coinsph/mexc/kucoin)完全不参与站下,行为与改动前逐位相同。
func TestStandDownSkippedForAdaptersWithoutLimiter(t *testing.T) {
	ex := &hedgeExec{} // 没实现 RateLimitReporter
	var g standDownGate
	if (&Engine{}).stepStandDown(&g, ex, PairConfig{ExecSymbol: "SOL_USDT"}, time.Now()) {
		t.Fatal("适配器不报告限流状态时不得站下")
	}
	if g.down {
		t.Fatal("不参与站下的适配器不该改变状态机")
	}
}

// TestBreakerOpenSinceOnlyClearedBySuccess 锁死恢复判据的来源:
// openSince 不随冷却到点自动清零,只被一次真实成功清零。
func TestBreakerOpenSinceOnlyClearedBySuccess(t *testing.T) {
	l, clk := newTestLimiter(RateLimitConfig{BreakerFails: 1, BreakerCoolMs: 30000})
	if !l.breakerOpenSince().IsZero() {
		t.Fatal("未熔断时 openSince 应为零值")
	}
	l.on429(0)
	first := l.breakerOpenSince()
	if first.IsZero() {
		t.Fatal("熔断打开后 openSince 必须打点")
	}
	// 后续 429 把 banUntil 往后推,但【本段】起点不动。
	clk.advance(2 * time.Second)
	l.on429(0)
	if !l.breakerOpenSince().Equal(first) {
		t.Fatal("同一段熔断内 openSince 不得被后续 429 推后(否则永远等不到站下阈值)")
	}
	// 冷却到点:banUntil 失效,但"是否真的恢复"仍未知 → openSince 保持。
	clk.advance(40 * time.Second)
	if l.RateLimited() {
		t.Fatal("冷却已过,banUntil 应失效")
	}
	if l.breakerOpenSince().IsZero() {
		t.Fatal("冷却到点不等于恢复:探针还没成功过,openSince 不该清零(否则会在冷却边界上反复宣布恢复)")
	}
	// 一次真实成功才算恢复。
	l.onSuccess()
	if !l.breakerOpenSince().IsZero() {
		t.Fatal("一次成功之后 openSince 必须清零")
	}
}

// ── ③ 端到端:真 GateExchange + 真 Engine + 假 gate 服务器 ──

// fakeGateServer 是一个够用的假 gate:记挂单、认 POST/DELETE,并且可以整体切到 429。
type fakeGateServer struct {
	mu      sync.Mutex
	limited bool
	orders  []OpenOrder
	nextID  int
	hits    map[string]int
}

func (s *fakeGateServer) setLimited(v bool) {
	s.mu.Lock()
	s.limited = v
	s.mu.Unlock()
}

func (s *fakeGateServer) openCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.orders)
}

func (s *fakeGateServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/api/v4")
		s.hits[r.Method+" "+path]++
		w.Header().Set("Content-Type", "application/json")

		// 交易对信息是公开端点,不受 UID 限流影响(且引擎侧本来就有缓存)。
		if strings.HasPrefix(path, "/spot/currency_pairs/") {
			_, _ = w.Write([]byte(`{"base":"SOL","quote":"USDT","amount_precision":3,"precision":2,"min_quote_amount":"1"}`))
			return
		}
		if s.limited {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"label":"TOO_MANY_REQUESTS"}`))
			return
		}
		switch {
		case path == "/spot/accounts":
			_, _ = w.Write([]byte(`[{"currency":"SOL","available":"10","locked":"0"},{"currency":"USDT","available":"5000","locked":"0"}]`))
		case path == "/spot/orders" && r.Method == http.MethodGet:
			out := make([]map[string]string, 0, len(s.orders))
			for _, o := range s.orders {
				out = append(out, map[string]string{"id": o.ID, "side": strings.ToLower(o.Side),
					"price": fmt.Sprintf("%f", o.Price), "amount": fmt.Sprintf("%f", o.Qty)})
			}
			b, _ := json.Marshal(out)
			_, _ = w.Write(b)
		case path == "/spot/orders" && r.Method == http.MethodPost:
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			s.nextID++
			id := fmt.Sprintf("ord-%d", s.nextID)
			// 价量必须原样记住并回读,否则引擎下一轮读回 0 价会判定"不匹配"
			// 而撤挂重下,把测试变成一台 thrash 机器(见 requoteToleranceFrac)。
			s.orders = append(s.orders, OpenOrder{ID: id, Side: strings.ToUpper(req["side"]),
				Price: atof(req["price"]), Qty: atof(req["amount"])})
			_, _ = w.Write([]byte(`{"id":"` + id + `"}`))
		case strings.HasPrefix(path, "/spot/orders/") && r.Method == http.MethodDelete:
			id := strings.TrimPrefix(path, "/spot/orders/")
			for i := range s.orders {
				if s.orders[i].ID == id {
					s.orders = append(s.orders[:i], s.orders[i+1:]...)
					break
				}
			}
			_, _ = w.Write([]byte(`{"id":"` + id + `"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
}

// TestStandDownEndToEnd 把一次完整的「限流 → 站下 → 恢复」在假 gate 上跑出来,
// 并把每一步的观测量打进 t.Log —— 这份输出就是"系统各步做了什么"的实跑证据,
// 不是口述。断言只锁性质,叙事交给日志。
func TestStandDownEndToEnd(t *testing.T) {
	srv := &fakeGateServer{hits: map[string]int{}}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	// 限流器用【默认档】(台账 #84 的 10 请求/10 秒、5 连 429 熔断、冷却 30s),
	// 挂假时钟,全程没有一次真实 sleep。
	lim, clk := newTestLimiter(RateLimitConfig{})
	ex := &GateExchange{
		baseURL: ts.URL, apiKey: "k", secret: "s",
		http: ts.Client(), filters: map[string]SymbolFilter{}, limiter: lim,
	}
	e := &Engine{cfg: Config{}}
	p := PairConfig{FeedSymbol: "SOLUSDT", Exec: "gate", ExecSymbol: "SOL_USDT",
		SpreadBps: 20, OrderQty: 1, MaxPosition: 100, RefreshMs: 1000}
	ref := BookTicker{BidPx: 99.95, AskPx: 100.05, Ts: clk.now()}
	eb := BookTicker{BidPx: 99.90, AskPx: 100.10, Ts: clk.now()}

	var g standDownGate
	var trace []string
	// tick 复刻 runPair 每个 refresh 周期里与本轮相关的那两步:先跑站下状态机,
	// 没被拦下才进 quote()。顺序与 engine.go 里一致。
	tick := func() {
		clk.advance(time.Duration(p.refresh()) * time.Millisecond)
		if e.stepStandDown(&g, ex, p, clk.now()) {
			return
		}
		e.quote(p, ex, ref, eb, 0)
	}
	note := func(phase string) {
		st := ex.RateLimitStatus()
		openFor := "—"
		if !st.BreakerOpenSince.IsZero() {
			openFor = clk.now().Sub(st.BreakerOpenSince).Truncate(time.Millisecond).String()
		}
		trace = append(trace, fmt.Sprintf("t=%-7s %-26s 站下=%-5v 熔断持续=%-8s 盘口挂单=%d",
			clk.now().Sub(clk.now().Truncate(time.Hour)).Truncate(time.Millisecond), phase, g.down, openFor, srv.openCount()))
	}

	// ── 阶段 A:一切正常,双边报价挂上去 ──
	for i := 0; i < 2; i++ {
		tick()
	}
	note("A 正常报价")
	if srv.openCount() != 2 {
		t.Fatalf("正常状态下应挂出双边 2 张单,实际 %d 张", srv.openCount())
	}

	// ── 阶段 B:交易所开始整体 429 ──
	srv.setLimited(true)
	downAt := -1
	for i := 0; i < 40 && downAt < 0; i++ {
		tick()
		if g.down {
			downAt = i
		}
	}
	note("B 限流→站下")
	if downAt < 0 {
		t.Fatal("持续限流下必须站下")
	}
	openSince := ex.RateLimitStatus().BreakerOpenSince
	if openSince.IsZero() {
		t.Fatal("站下时熔断应处于打开状态")
	}
	// 站下不得早于阈值:短暂抖动不该触发撤单。
	// (站下发生在某个 tick 上,所以允许 1 个 refresh 周期的粒度误差。)
	if lag := clk.now().Sub(openSince); lag < standDownAfter(p) {
		t.Fatalf("站下过早:熔断才持续 %s,阈值是 %s —— 抖动会被误判", lag, standDownAfter(p))
	}
	if lag := clk.now().Sub(openSince); lag > standDownAfter(p)+2*time.Duration(p.refresh())*time.Millisecond {
		t.Fatalf("站下过晚:熔断已持续 %s,阈值 %s", lag, standDownAfter(p))
	}

	// ── 阶段 C:限流继续。站下期间一张新单都不许挂 ──
	placedBefore := srv.hits["POST /spot/orders"]
	for i := 0; i < 10; i++ {
		tick()
	}
	note("C 站下中")
	if srv.hits["POST /spot/orders"] != placedBefore {
		t.Fatalf("站下期间绝不允许挂新单,却多挂了 %d 张", srv.hits["POST /spot/orders"]-placedBefore)
	}

	// ── 阶段 D:交易所恢复。等冷却过去 + 探针成功 + 连续健康达标 ──
	//
	// 站下的全部意义是盘口上不留陈价挂单,所以要盯的是"整个站下期间有没有真的
	// 走平过"。注意站下那一刻交易所还在整体 429,撤单根本发不出去 —— 真正把单撤掉
	// 的是站下期间探针成功之后那次补撤。这正是补撤存在的理由,这里把它量出来。
	srv.setLimited(false)
	flatDuringStandDown := false
	upAt := -1
	for i := 0; i < 200 && upAt < 0; i++ {
		tick()
		if g.down && srv.openCount() == 0 {
			flatDuringStandDown = true
		}
		if !g.down {
			upAt = i
		}
	}
	note("D 恢复报价")
	if upAt < 0 {
		t.Fatal("交易所恢复后必须重新站起来(卡在站下 = 活锁)")
	}
	if !flatDuringStandDown {
		t.Fatal("站下期间必须真的走平(盘口上不留陈价挂单),否则站下等于没做")
	}

	// 恢复之后必须重新开始报价 —— 不是"能报"而是"确实报了"。
	quotedAfterRecovery := srv.hits["POST /spot/orders"] - placedBefore
	for i := 0; i < 3; i++ {
		tick()
	}
	note("E 报价已重建")
	if quotedAfterRecovery == 0 {
		t.Fatal("恢复之后必须重新挂单")
	}

	// ── 阶段 F:恢复后的稳态,只观测不断言 ──
	// 报价周期的报价类需求(Balances+OpenOrders,再加改价时的挂单)本来就高于
	// 惩罚档预算(7 个名额/10 秒),所以恢复后仍会周期性撞上"本地名额耗尽"
	// → 触发余额避险 → 撤单走平 → 名额滑出窗口后再挂回来。这不是站下逻辑的
	// 抖动(g.down 全程为 false),是台账 #84 那条"需求 > 预算"的直接表现,
	// 根治办法是压需求(限制在管 symbol 数),不在本轮范围内。把它量出来备查。
	flatCycles := 0
	for i := 0; i < 20; i++ {
		tick()
		if srv.openCount() == 0 {
			flatCycles++
		}
		if g.down {
			t.Fatal("交易所已恢复,站下状态机不该再被顶开(那才是横跳)")
		}
	}
	note("F 恢复后稳态")

	t.Log("──── 限流 → 站下 → 恢复 全过程(假 gate,假时钟)────")
	for _, line := range trace {
		t.Log(line)
	}
	t.Logf("站下发生在限流开始后第 %d 个 refresh 周期;恢复发生在交易所解除限流后第 %d 个周期", downAt+1, upAt+1)
	t.Logf("恢复后重新挂出 %d 张单;站下期间是否真的走平: %v", quotedAfterRecovery, flatDuringStandDown)
	t.Logf("恢复后 20 个周期里有 %d 个周期盘口是空的(需求 > 惩罚档预算的直接表现,非站下抖动)", flatCycles)
	t.Logf("参数:站下阈值=%s(5×refresh) 恢复防抖=%s(%d×限流窗口 %dms)",
		standDownAfter(p), time.Duration(standUpHealthyWindows)*time.Duration(ex.RateLimitStatus().WindowMs)*time.Millisecond,
		standUpHealthyWindows, ex.RateLimitStatus().WindowMs)
	for _, k := range []string{"GET /spot/accounts", "GET /spot/orders", "POST /spot/orders", "DELETE /spot/orders/ord-1"} {
		t.Logf("假 gate 收到 %-28s %d 次", k, srv.hits[k])
	}
}

// TestStandDownProbeBreaksLivelock 单独锁住站下期间那发探针的必要性:
// 没有它,站下之后引擎不再发任何报价类请求 → onSuccess() 永不被调用 →
// openSince 永不清零 → 永久卡在站下。
func TestStandDownProbeBreaksLivelock(t *testing.T) {
	srv := &fakeGateServer{hits: map[string]int{}, limited: true}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	lim, clk := newTestLimiter(RateLimitConfig{BreakerFails: 1, BreakerCoolMs: 5000})
	ex := &GateExchange{baseURL: ts.URL, apiKey: "k", secret: "s",
		http: ts.Client(), filters: map[string]SymbolFilter{}, limiter: lim}
	e := &Engine{cfg: Config{}}
	p := PairConfig{ExecSymbol: "SOL_USDT", RefreshMs: 1000}

	// 顶开熔断并把状态机推到站下。
	_, _ = ex.OpenOrders("SOL_USDT")
	if ex.RateLimitStatus().BreakerOpenSince.IsZero() {
		t.Fatal("一次 429(BreakerFails=1)应打开熔断")
	}
	var g standDownGate
	clk.advance(standDownAfter(p))
	if !e.stepStandDown(&g, ex, p, clk.now()) || !g.down {
		t.Fatal("熔断持续过阈值应站下")
	}

	// 交易所恢复。此后引擎【只】跑站下逻辑,不进 quote() —— 唯一的出网机会
	// 就是 hold 分支里那发探针。它必须把 openSince 清掉。
	srv.setLimited(false)
	beforeProbe := srv.hits["GET /spot/orders"]
	for i := 0; i < 30 && !ex.RateLimitStatus().BreakerOpenSince.IsZero(); i++ {
		clk.advance(time.Second)
		e.stepStandDown(&g, ex, p, clk.now())
	}
	if !ex.RateLimitStatus().BreakerOpenSince.IsZero() {
		t.Fatal("站下期间必须有探针出网并清掉熔断,否则永久卡在站下(活锁)")
	}
	if srv.hits["GET /spot/orders"] <= beforeProbe {
		t.Fatal("探针一次都没出网")
	}
}

// TestStandDownEnterUsesCancelClass 证明站下这一动作确实走撤单档:
// 熔断打开时报价档全被本地拒,但撤单请求仍必须出网。
func TestStandDownEnterUsesCancelClass(t *testing.T) {
	var buf syncBuffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	// openErr 非空 = 报价档此刻被熔断挡在本地。站下若走报价档读挂单,这里就撤不掉。
	ex := &hedgeExec{
		openErr: fmt.Errorf("%w: 熔断打开", ErrGateRateLimited),
		resting: []OpenOrder{{ID: "x", Side: "BUY"}},
	}
	rep := &reportingExec{hedgeExec: ex, status: RateLimitStatus{
		BreakerOpenSince: time.Now().Add(-time.Minute), WindowMs: 10000,
	}}
	var g standDownGate
	if !(&Engine{}).stepStandDown(&g, rep, PairConfig{ExecSymbol: "SOL_USDT"}, time.Now()) {
		t.Fatal("熔断已持续 1 分钟,必须站下")
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if len(ex.cancelled) != 1 {
		t.Fatalf("站下必须撤掉挂在盘口的单,实际撤了 %d 张", len(ex.cancelled))
	}
	if !strings.Contains(buf.String(), "[mm-standdown]") {
		t.Fatal("站下是会改变实盘持仓姿态的动作,必须留下日志")
	}
}

// reportingExec = hedgeExec + 可编排的限流状态。
type reportingExec struct {
	*hedgeExec
	status RateLimitStatus
}

func (r *reportingExec) RateLimitStatus() RateLimitStatus { return r.status }

var _ RateLimitReporter = (*reportingExec)(nil)

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
