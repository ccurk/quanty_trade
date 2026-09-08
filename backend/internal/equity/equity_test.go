package equity

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// blockingSink 卡在 WriteEquitySnapshot 里不返回 —— 模拟 DB 挂起/慢查询/连接池耗尽。
// 这比"返回 error"更凶:返回错误是快速失败,挂起才是真正会把上游拖死的那种。
type blockingSink struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingSink() *blockingSink {
	return &blockingSink{entered: make(chan struct{}, 64), release: make(chan struct{})}
}

func (b *blockingSink) WriteEquitySnapshot(Snapshot) error {
	b.entered <- struct{}{}
	<-b.release // 永远不返回,直到测试放行
	return nil
}

func (b *blockingSink) unblock() { b.once.Do(func() { close(b.release) }) }

type errSink struct {
	mu sync.Mutex
	n  int
}

func (e *errSink) WriteEquitySnapshot(Snapshot) error {
	e.mu.Lock()
	e.n++
	e.mu.Unlock()
	return errors.New("DB 炸了")
}

func (e *errSink) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.n
}

type captureSink struct {
	mu   sync.Mutex
	rows []Snapshot
}

func (c *captureSink) WriteEquitySnapshot(s Snapshot) error {
	c.mu.Lock()
	c.rows = append(c.rows, s)
	c.mu.Unlock()
	return nil
}

func (c *captureSink) snapshot() []Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Snapshot(nil), c.rows...)
}

// waitFor 轮询等条件成立,超时即失败。用于等异步写入落地,
// 不用 sleep 一个固定值(那要么慢要么在慢机器上抖)。
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
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

// TestSinkFailureDoesNotBlockTradingPath 是本轮最要紧的一条断言:
// 权益落库挂掉,不能拖慢交易路径。
//
// 为什么这样测就够:Emit 的两个调用点都在下单【之前】的读余额路径上 ——
// marketmaker/gate.go Balances()(engine.go 每秒约 4 次,读完才算持仓、才报价)
// 和 exchange/binance.go usdmBalanceUSDT()(开仓定量)。只要 Emit 在 sink
// 完全卡死时仍能立刻返回,这两处就不可能因为落库被推迟。
// 端到端走真实 Balances() 的那条在 marketmaker 包里
// (TestGateBalancesUnaffectedByStuckSink)。
//
// 用【永不返回】的 sink 而不是返回 error 的:同步实现下 error 会立刻返回、测不出
// 任何东西;真正的危险是 DB 挂起。若哪天有人把 Emit 改成同步写,这条会直接超时。
func TestSinkFailureDoesNotBlockTradingPath(t *testing.T) {
	ResetForTest()
	sink := newBlockingSink()
	defer sink.unblock()
	SetSink(sink, 1) // 缓冲设成 1:写入端卡住后,第 3 条起必然溢出
	defer SetSink(nil, 0)

	// 每条都用不同的 asset,绕开降频 —— 这里要测的是"卡住时会不会阻塞",
	// 不是降频;让降频把它们吞掉就等于什么都没测。
	const reads = 300
	base := time.Now()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		for i := 0; i < reads; i++ {
			Emit(Snapshot{
				Venue: VenueGateSpot, Asset: "A" + time.Duration(i).String(),
				TakenAt: base.Add(time.Duration(i) * time.Second),
				Free:    float64(i), Total: float64(i) + 1,
				Source: "GET /spot/accounts",
			})
		}
		done <- time.Since(start)
	}()

	// 3 秒是"没被卡住"的粗判据,不是性能指标:sink 永不返回,同步写的话这里必然超时。
	select {
	case elapsed := <-done:
		t.Logf("sink 完全卡死时,%d 次余额观测耗时 %s", reads, elapsed)
	case <-time.After(3 * time.Second):
		t.Fatal("落库 sink 卡住时,Emit 被一起拖住了 —— 报价与下单会被度量拖垮")
	}

	// 写入端确实是卡着的(不是"其实早就写完了所以没阻塞")。
	waitFor(t, time.Second, "写入 goroutine 进入 WriteEquitySnapshot", func() bool {
		return len(sink.entered) > 0
	})
	// 缓冲溢出必须表现为【丢弃并计数】,不是等待。
	st := Counters()
	if st.Dropped == 0 {
		t.Fatalf("缓冲满时应丢弃并计数,dropped=0 (counters=%+v)", st)
	}
	if st.Observed != reads {
		t.Fatalf("应当每次读余额产生一次观测,得到 %d(期望 %d)", st.Observed, reads)
	}
}

// TestErrorsAreCountedNotPropagated:写入返回错误只被计数,不冒泡、不重试。
// 不重试是有意的 —— 见 SetSink 里的注释。
func TestErrorsAreCountedNotPropagated(t *testing.T) {
	ResetForTest()
	sink := &errSink{}
	// 缓冲必须 > reads:这里要测的是"失败怎么计数",不是缓冲溢出。
	// 缓冲小于条数时会先丢一批,把断言变成在测降频/丢弃(那是另外两条的事)。
	SetSink(sink, 64)
	defer SetSink(nil, 0)

	const reads = 20
	for i := 0; i < reads; i++ {
		Emit(Snapshot{
			Venue: VenueBinanceUSDM, Asset: "A" + time.Duration(i).String(),
			TakenAt: time.Now(), Free: 1, Total: 2, Source: "GET /fapi/v2/balance",
		})
	}
	waitFor(t, 2*time.Second, "全部写入尝试完成", func() bool { return sink.count() == reads })

	st := Counters()
	if st.Failed != int64(reads) {
		t.Fatalf("失败应逐条计数,failed=%d(期望 %d)", st.Failed, reads)
	}
	if st.Written != 0 {
		t.Fatalf("全部失败时 written 应为 0,得到 %d", st.Written)
	}
	// 不重试:尝试次数恰好等于条数,没有额外的重排队。
	if sink.count() != reads {
		t.Fatalf("不应重试,WriteEquitySnapshot 被调用 %d 次(期望 %d)", sink.count(), reads)
	}
}

// TestEmitRefusesUnlabelledSnapshot:没有 venue 的余额【不落库】。
//
// 这不是输入校验洁癖。#18/#22 撞车的真因就是一个没标场子的数被拿去和另一个
// 场子的数三角定位;把这种行存进表里,等于把那个陷阱永久固化。
func TestEmitRefusesUnlabelledSnapshot(t *testing.T) {
	ResetForTest()
	sink := &captureSink{}
	SetSink(sink, 16)
	defer SetSink(nil, 0)

	Emit(Snapshot{Venue: "", Asset: "USDT", TakenAt: time.Now(), Free: 236})
	Emit(Snapshot{Venue: VenueGateSpot, Asset: "", TakenAt: time.Now(), Free: 17})
	// 一条合法的,证明 sink 本身是通的(否则上面两条"没写"什么也证明不了)。
	Emit(Snapshot{Venue: VenueGateSpot, Asset: "USDT", TakenAt: time.Now(), Free: 17, Total: 17})

	waitFor(t, time.Second, "合法快照落地", func() bool { return len(sink.snapshot()) == 1 })
	if st := Counters(); st.Observed != 1 {
		t.Fatalf("无 venue/asset 的快照连观测都不该计,observed=%d(期望 1)", st.Observed)
	}
	if got := sink.snapshot()[0].Venue; got != VenueGateSpot {
		t.Fatalf("落地的那条 venue=%q", got)
	}
}

// TestDownsampling 锁住三条降频规则。不降频的话按每秒 4 次读 × 全账户资产
// 约 175 万行/天(算式见 scripts/equity_snapshots.sql §2),这三条把它砍到千行量级。
func TestDownsampling(t *testing.T) {
	ResetForTest()
	sink := &captureSink{}
	SetSink(sink, 64)
	defer SetSink(nil, 0)

	t0 := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	emit := func(at time.Time, free float64) {
		Emit(Snapshot{Venue: VenueGateSpot, Asset: "USDT", TakenAt: at,
			Free: free, Total: free, Source: "GET /spot/accounts"})
	}

	// ❶ 速率上限:一分钟内即使值一直在变,也只留第一条。
	//    这条模拟真实的每秒 4 次读取。
	for i := 0; i < 240; i++ {
		emit(t0.Add(time.Duration(i)*250*time.Millisecond), 100+float64(i))
	}
	waitFor(t, time.Second, "首条落地", func() bool { return len(sink.snapshot()) >= 1 })
	if n := len(sink.snapshot()); n != 1 {
		t.Fatalf("60s 内 240 次读取应只落 1 行,得到 %d", n)
	}

	// ❷ 过了速率上限但值没动 → 不写。安静的账户几乎不产生行。
	emit(t0.Add(61*time.Second), 100)
	emit(t0.Add(122*time.Second), 100)
	if n := len(sink.snapshot()); n != 1 {
		t.Fatalf("值没变不应写新行,得到 %d 行", n)
	}

	// ❸ 过了速率上限且值动了 → 写。
	emit(t0.Add(183*time.Second), 250)
	waitFor(t, time.Second, "变化行落地", func() bool { return len(sink.snapshot()) == 2 })

	// ❹ 心跳:即使值一直没动,超过 15 分钟也必须写一行。
	//    没有这条,"余额没变"和"我们不再观测了"是同一段空白。
	emit(t0.Add(183*time.Second+heartbeatInterval), 250)
	waitFor(t, time.Second, "心跳行落地", func() bool { return len(sink.snapshot()) == 3 })

	rows := sink.snapshot()
	if rows[0].Free != 100 || rows[1].Free != 250 || rows[2].Free != 250 {
		t.Fatalf("落地的行不对: %+v", rows)
	}
	st := Counters()
	if st.Suppressed == 0 || st.Suppressed <= st.Enqueued {
		t.Fatalf("绝大多数观测应被降频吞掉,counters=%+v", st)
	}
	t.Logf("降频效果: 观测 %d 次 → 落库 %d 行", st.Observed, st.Enqueued)
}

// TestDownsamplingIsPerVenueAndAsset:降频是逐 (venue, asset) 的。
// 若键里漏了 venue,binance 的读数会把 gate 的压掉 —— 一个场子的钱会凭空消失。
func TestDownsamplingIsPerVenueAndAsset(t *testing.T) {
	ResetForTest()
	sink := &captureSink{}
	SetSink(sink, 16)
	defer SetSink(nil, 0)

	now := time.Now()
	Emit(Snapshot{Venue: VenueGateSpot, Asset: "USDT", TakenAt: now, Free: 17, Total: 17})
	Emit(Snapshot{Venue: VenueBinanceUSDM, Asset: "USDT", TakenAt: now, Free: 236, Total: 240})
	Emit(Snapshot{Venue: VenueGateSpot, Asset: "SOL", TakenAt: now, Free: 1.5, Total: 1.5})

	waitFor(t, time.Second, "三条都落地", func() bool { return len(sink.snapshot()) == 3 })
}

// TestEmitIsNoOpWithoutSink:没装 sink 时 Emit 仍然安全,且不做任何 IO。
// 这正是"跑了这份代码但没建表"的生产状态 —— 必须是彻底的 no-op。
func TestEmitIsNoOpWithoutSink(t *testing.T) {
	ResetForTest()
	SetSink(nil, 0)
	Emit(Snapshot{Venue: VenueGateSpot, Asset: "USDT", TakenAt: time.Now(), Free: 17, Total: 17})
	st := Counters()
	if st.Observed != 1 || st.Enqueued != 1 || st.Dropped != 1 {
		t.Fatalf("无 sink 时应当观测到、然后丢弃并计数,counters=%+v", st)
	}
	if st.Written != 0 || st.Failed != 0 {
		t.Fatalf("无 sink 时不该有任何写入结果,counters=%+v", st)
	}
}
