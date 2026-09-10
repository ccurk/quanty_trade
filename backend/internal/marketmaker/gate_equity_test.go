package marketmaker

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"quanty_trade/internal/equity"
)

// gate_equity_test.go 端到端验证台账 #42 那条读取路径:engine.go 每秒约 4 次调用的
// GateExchange.Balances(),现在会把它本来就收到、却一直丢掉的余额记下来。
//
// 走的是【真实的 Balances()】,不是仿造的调用:起一个假的 /spot/accounts,
// 让真实的签名、解码、emit 全跑一遍。

const gateAccountsJSON = `[
  {"currency":"USDT","available":"17.40","locked":"3.60"},
  {"currency":"SOL","available":"1.5","locked":"0"},
  {"currency":"ONG","available":"0","locked":"42.0"},
  {"currency":"DUST","available":"0","locked":"0"}
]`

func newFakeGate(t *testing.T, body string, onCall func()) (*GateExchange, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onCall != nil {
			onCall()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	ex := &GateExchange{
		baseURL: srv.URL,
		apiKey:  "test-key", // 假的:只是为了过 signed() 的非空检查,不出网
		secret:  "test-secret",
		http:    srv.Client(),
		filters: map[string]SymbolFilter{},
		// 本文件测的是"落库故障不拖累交易路径",不是限流。给一个够大的预算把
		// 限流器从被测范围里摘出去,否则下面 200 连打会被限流挡住,测出来的是
		// 限流器而不是 sink。限流本身另见 gate_ratelimit_test.go。
		// (Balances 花的是【查询池】,所以要放开的是 QueryRequests 而不只是 Requests。)
		limiter: newGateLimiter(RateLimitConfig{Requests: 1_000_000, QueryRequests: 1_000_000, WindowMs: 10000}),
	}
	return ex, srv.Close
}

type capture struct {
	mu   sync.Mutex
	rows []equity.Snapshot
}

func (c *capture) WriteEquitySnapshot(s equity.Snapshot) error {
	c.mu.Lock()
	c.rows = append(c.rows, s)
	c.mu.Unlock()
	return nil
}

func (c *capture) byAsset(a string) (equity.Snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.rows {
		if r.Asset == a {
			return r, true
		}
	}
	return equity.Snapshot{}, false
}

func (c *capture) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.rows)
}

func waitUntil(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时(%s): %s", d, what)
}

// TestGateBalancesRecordsWhatItAlreadyRead 是 #42 的正面证据:
// 同一份响应里的 locked 以前解都不解就丢了,现在 free/total 都落下来,
// 而且每行都带 venue 和来源端点。
func TestGateBalancesRecordsWhatItAlreadyRead(t *testing.T) {
	equity.ResetForTest()
	cap := &capture{}
	equity.SetSink(cap, 32)
	defer equity.SetSink(nil, 0)

	ex, closeFn := newFakeGate(t, gateAccountsJSON, nil)
	defer closeFn()

	bals, err := ex.Balances()
	if err != nil {
		t.Fatalf("Balances 失败: %v", err)
	}

	// 返回值必须【逐位不变】:报价路径的库存算法只认 available,
	// 这次改动不该动它分毫。
	if got := bals["USDT"]; got != 17.40 {
		t.Fatalf("USDT available 应为 17.40,得到 %v", got)
	}
	if got := bals["SOL"]; got != 1.5 {
		t.Fatalf("SOL available 应为 1.5,得到 %v", got)
	}
	if _, ok := bals["ONG"]; ok {
		t.Fatal("available=0 的资产不该进返回的 map(会改变库存算法的行为)")
	}

	waitUntil(t, 2*time.Second, "快照落地", func() bool { return cap.len() == 3 })

	// USDT:total 必须包含 locked —— 这就是"已经拿到手却丢了"的那部分。
	u, ok := cap.byAsset("USDT")
	if !ok {
		t.Fatal("没有 USDT 的快照")
	}
	if u.Free != 17.40 || u.Total != 21.00 {
		t.Fatalf("USDT 快照 free=%v total=%v,期望 17.40 / 21.00(= 17.40+3.60)", u.Free, u.Total)
	}
	if u.Venue != equity.VenueGateSpot {
		t.Fatalf("快照必须带场子标签,得到 venue=%q", u.Venue)
	}
	if u.Source != "GET /spot/accounts" {
		t.Fatalf("快照必须记下来源端点,得到 source=%q", u.Source)
	}
	if u.Unrealized != 0 {
		t.Fatalf("现货没有浮盈浮亏,应为 0,得到 %v", u.Unrealized)
	}

	// ONG 全部锁在挂单里:available=0,所以它【不在】返回的 map 里,
	// 但账上确实有 42 个。这正是只看 available 的视角看不见的那种钱 ——
	// 必须落库,否则权益会被系统性低估。
	o, ok := cap.byAsset("ONG")
	if !ok {
		t.Fatal("全部锁仓的资产也必须落库,否则权益被系统性低估")
	}
	if o.Free != 0 || o.Total != 42.0 {
		t.Fatalf("ONG 快照 free=%v total=%v,期望 0 / 42.0", o.Free, o.Total)
	}

	// 全零的资产不落库,免得把交易所返回的一长串空币种全存下来。
	if _, ok := cap.byAsset("DUST"); ok {
		t.Fatal("free 和 locked 都为 0 的资产不该落库")
	}
}

// TestGateBalancesUnaffectedByStuckSink 是"写入失败不影响交易路径"的
// 【端到端】版本:落库 sink 完全挂死时,报价路径每秒 4 次的这个调用
// 必须照常按时返回、照常返回正确的余额。
//
// equity 包里那条(TestSinkFailureDoesNotBlockTradingPath)测的是 Emit 本身;
// 这一条测的是真实调用点,防止有人日后在 Balances() 里把 emit 包成同步等待。
func TestGateBalancesUnaffectedByStuckSink(t *testing.T) {
	equity.ResetForTest()
	stuck := newStuckSink()
	defer stuck.unblock()
	equity.SetSink(stuck, 1) // 缓冲 1:必然溢出
	defer equity.SetSink(nil, 0)

	var calls int
	ex, closeFn := newFakeGate(t, gateAccountsJSON, func() { calls++ })
	defer closeFn()

	// 模拟报价循环:每秒 4 次,连打 200 轮。
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		for i := 0; i < 200; i++ {
			if _, err := ex.Balances(); err != nil {
				t.Errorf("落库卡死时 Balances 不该出错: %v", err)
				break
			}
		}
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		t.Logf("落库 sink 卡死时,200 次 Balances() 耗时 %s(仍在正常量级)", elapsed)
	case <-time.After(5 * time.Second):
		t.Fatal("落库卡住时 Balances() 被一起拖住 —— 报价与下单会被度量拖垮")
	}

	if calls != 200 {
		t.Fatalf("每次 Balances 都该真的打一次接口,得到 %d 次", calls)
	}
	// 最后一次的返回值仍然正确:度量故障没有污染交易路径读到的数。
	bals, err := ex.Balances()
	if err != nil || bals["USDT"] != 17.40 {
		t.Fatalf("落库卡死时余额仍应正确,得到 %v err=%v", bals, err)
	}
	if st := equity.Counters(); st.Dropped == 0 {
		t.Fatalf("缓冲满时应丢弃并计数,counters=%+v", st)
	}
}

type stuckSink struct {
	release chan struct{}
	once    sync.Once
}

func newStuckSink() *stuckSink { return &stuckSink{release: make(chan struct{})} }

func (s *stuckSink) WriteEquitySnapshot(equity.Snapshot) error {
	<-s.release // 永不返回
	return nil
}

func (s *stuckSink) unblock() { s.once.Do(func() { close(s.release) }) }
