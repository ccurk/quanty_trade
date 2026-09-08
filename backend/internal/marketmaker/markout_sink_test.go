package marketmaker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// blockingSink 卡在 WriteMarkout 里不返回 —— 模拟 DB 挂起/慢查询/连接池耗尽。
// 这比"返回 error"更凶:返回错误是快速失败,挂起才是真正会把上游拖死的那种。
type blockingSink struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingSink() *blockingSink {
	return &blockingSink{entered: make(chan struct{}, 64), release: make(chan struct{})}
}

func (b *blockingSink) WriteMarkout(MarkoutRecord) error {
	b.entered <- struct{}{}
	<-b.release // 永远不返回,直到测试放行
	return nil
}

func (b *blockingSink) unblock() { b.once.Do(func() { close(b.release) }) }

type errSink struct {
	mu sync.Mutex
	n  int
}

func (e *errSink) WriteMarkout(MarkoutRecord) error {
	e.mu.Lock()
	e.n++
	e.mu.Unlock()
	return errors.New("DB 炸了")
}

type captureSink struct {
	mu   sync.Mutex
	rows []MarkoutRecord
}

func (c *captureSink) WriteMarkout(r MarkoutRecord) error {
	c.mu.Lock()
	c.rows = append(c.rows, r)
	c.mu.Unlock()
	return nil
}

func (c *captureSink) snapshot() []MarkoutRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]MarkoutRecord(nil), c.rows...)
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

// TestMarkoutSinkFailureDoesNotBlockTradingPath 是本轮最要紧的一条断言:
// 度量落库挂掉,不能拖慢交易路径。
//
// 为什么这样测就够:引擎报价循环(engine.go)每轮做的事按顺序是
//
//	FetchBookTicker → observeRow → markoutTracker.Observe → [每 10s] RecordFill → e.quote(下单)
//
// markout 只有 Observe / RecordFill 两个入口,且都在 e.quote 之前。只要这两个
// 在 sink 完全卡死时仍能立刻返回,e.quote 就不可能因为落库被推迟 —— 这里不去
// 启真引擎(那要交易所凭据和网络),而是把这两个入口按真实调用比例打满。
//
// 用【永不返回】的 sink 而不是返回 error 的:同步实现下 error 会立刻返回、测不出
// 任何东西;真正的危险是 DB 挂起。若哪天有人把 emit 改成同步写,这条会直接超时。
func TestMarkoutSinkFailureDoesNotBlockTradingPath(t *testing.T) {
	sink := newBlockingSink()
	defer sink.unblock()
	SetMarkoutSink(sink, 1) // 缓冲设成 1:写入端卡住后,第 3 笔起必然溢出
	defer SetMarkoutSink(nil, 0)

	before := MarkoutSinkCounters()

	tr := NewMarkoutTracker()
	const fills = 300
	base := time.Now()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		for i := 0; i < fills; i++ {
			t0 := base.Add(time.Duration(i) * time.Minute)
			tr.RecordFill("gate", "ONG_USDT", string(rune('a'+i%26))+time.Duration(i).String(),
				"buy", 100, 1, 20, t0)
			tr.Observe("ONG_USDT", 100.05, t0.Add(1*time.Second))
			tr.Observe("ONG_USDT", 100.10, t0.Add(5*time.Second))
			tr.Observe("ONG_USDT", 99.90, t0.Add(30*time.Second))
		}
		done <- time.Since(start)
	}()

	// 3 秒是"没被卡住"的粗判据,不是性能指标:sink 永不返回,同步写的话这里必然超时。
	select {
	case elapsed := <-done:
		t.Logf("sink 完全卡死时,%d 笔成交的观测/登记耗时 %s", fills, elapsed)
	case <-time.After(3 * time.Second):
		t.Fatal("落库 sink 卡住时,Observe/RecordFill 被一起拖住了 —— 报价与下单会被度量拖垮")
	}

	// 写入端确实是卡着的(不是"其实早就写完了所以没阻塞")。
	waitFor(t, time.Second, "写入 goroutine 进入 WriteMarkout", func() bool {
		return len(sink.entered) > 0
	})
	// 缓冲溢出必须表现为【丢弃并计数】,不是等待。
	after := MarkoutSinkCounters()
	if after.Dropped <= before.Dropped {
		t.Fatalf("缓冲满时应丢弃并计数,dropped 没涨(before=%d after=%d)", before.Dropped, after.Dropped)
	}
	if after.Enqueued-before.Enqueued != fills {
		t.Fatalf("应当每笔成交产生一条记录,得到 %d(期望 %d)", after.Enqueued-before.Enqueued, fills)
	}

	// 度量本身完全不受影响:统计口径与没装 sink 时一致。
	st := tr.Stats()
	if len(st) != 1 || st[0].Fills != fills {
		t.Fatalf("落库挂掉不该影响内存统计,得到 %+v", st)
	}
	approx(t, st[0].AvgByHz["30s"], -10, 0.01, "30s markout 不应被落库故障污染")
}

// TestMarkoutSinkErrorsAreCountedNotPropagated:写入返回错误只被计数,
// 不冒泡、不重试、不影响统计。重试是有意不做的 —— 见 SetMarkoutSink 里的注释。
func TestMarkoutSinkErrorsAreCountedNotPropagated(t *testing.T) {
	sink := &errSink{}
	SetMarkoutSink(sink, 16)
	defer SetMarkoutSink(nil, 0)

	before := MarkoutSinkCounters()
	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "SOL_USDT", "f1", "sell", 100, 1, 20, t0)
	tr.Observe("SOL_USDT", 99.95, t0.Add(1*time.Second))
	tr.Observe("SOL_USDT", 99.90, t0.Add(5*time.Second))
	tr.Observe("SOL_USDT", 99.80, t0.Add(30*time.Second))

	waitFor(t, 2*time.Second, "失败计数增加", func() bool {
		return MarkoutSinkCounters().Failed > before.Failed
	})
	// 只断言本 sink 自己被调了一次:written/failed 是进程级计数,
	// 上一条用例卸载 sink 后残留的 writer goroutine 也可能改它们,
	// 拿全局值做相等断言会成为跨用例的偶发失败(实测到过一次)。
	sink.mu.Lock()
	n := sink.n
	sink.mu.Unlock()
	if n != 1 {
		t.Fatalf("应当只尝试写一次、不重试,实际调了 %d 次", n)
	}
	if st := tr.Stats(); len(st) != 1 || st[0].Fills != 1 {
		t.Fatalf("写入失败不该影响内存统计,得到 %+v", st)
	}
}

// TestMarkoutRecordIsOfflineVerifiable 锁死"落盘的行能被第二个人独立算回来"。
//
// 这是本轮要解决的漂移问题的形式化:报告里的 markout 一直是离线按公式重写一遍算的
// (quote-anchor-decision-2026-09-09.md §6),线上那份从没落过盘。两份公式一旦分叉
// 没人会发现。现在每行都带着 side/fill_px/mid,bps 只是它们的函数 —— 复核方可以
// 不信 bps 那一列,自己重算。这里用【手写的算式】而不是调 MarkoutBps 去比对,
// 否则等于拿同一份实现自证。
func TestMarkoutRecordIsOfflineVerifiable(t *testing.T) {
	sink := &captureSink{}
	SetMarkoutSink(sink, 16)
	defer SetMarkoutSink(nil, 0)

	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "PORTAL_USDT", "f-verify", "sell", 200, 3, 20, t0)
	tr.Observe("PORTAL_USDT", 199.80, t0.Add(1200*time.Millisecond)) // 晚 200ms 到
	tr.Observe("PORTAL_USDT", 199.60, t0.Add(5*time.Second))
	tr.Observe("PORTAL_USDT", 201.00, t0.Add(30*time.Second))

	waitFor(t, 2*time.Second, "记录落到 sink", func() bool { return len(sink.snapshot()) == 1 })
	rec := sink.snapshot()[0]

	if !rec.Complete || len(rec.Points) != len(MarkoutHorizons) {
		t.Fatalf("三个 horizon 都采齐了,应为完整记录: %+v", rec)
	}
	if rec.FillID != "f-verify" || rec.Side != "sell" || rec.FillPx != 200 || rec.FeeBps != 20 {
		t.Fatalf("成交标识/口径没原样落下来: %+v", rec)
	}
	if err := rec.Verify(1e-9); err != nil {
		t.Fatalf("记录自洽性检查失败: %v", err)
	}

	byH := map[string]MarkoutPoint{}
	for _, p := range rec.Points {
		byH[p.Horizon] = p
	}
	// 手算:卖单,markout = -(mid-fill)/fill*1e4。1s 时 199.80 → +10bps。
	approx(t, byH["1s"].Bps, 10, 1e-6, "1s 手算")
	approx(t, byH["5s"].Bps, 20, 1e-6, "5s 手算")
	approx(t, byH["30s"].Bps, -50, 1e-6, "30s 手算")
	// 采样滞后要记下来:1s 的样本晚了 200ms,这条记录测的其实是 1.2s。
	if byH["1s"].LagMs != 200 {
		t.Fatalf("1s 采样滞后应为 200ms,得到 %dms", byH["1s"].LagMs)
	}
	if byH["1s"].Mid != 199.80 || !byH["1s"].SampleTs.Equal(t0.Add(1200*time.Millisecond)) {
		t.Fatalf("原始中价/样本时刻没落下来: %+v", byH["1s"])
	}

	// 篡改任一原始量,Verify 必须抓到 —— 否则"可复核"是空话。
	bad := rec
	bad.FillPx = 201
	if err := bad.Verify(1e-9); err == nil {
		t.Fatal("原始量被改过之后 Verify 仍然通过,复核就失去意义了")
	}
}

// TestIncompleteMarkoutIsStillRecorded:采不齐的成交也要落一条(Complete=false),
// 而不是像原来那样静默丢弃 —— 否则"样本少"到底是没成交还是没记上,事后分不出来。
//
// 白盒构造:直接摆好"样本窗口整段落后于 horizon 目标时刻"的状态再调 resolveLocked。
// 之所以不能靠 Observe 造出来,见 TestStaleSampleResolvesCompleteWithBigLag ——
// Observe 每次都会追加一个 ts=now 的新样本,那个样本永远 ≥ 所有已到点的 target。
func TestIncompleteMarkoutIsStillRecorded(t *testing.T) {
	sink := &captureSink{}
	SetMarkoutSink(sink, 16)
	defer SetMarkoutSink(nil, 0)

	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "MOVE_USDT", "f-gap", "buy", 100, 1, 20, t0)
	tr.Observe("MOVE_USDT", 100.05, t0.Add(1*time.Second)) // 只采到 1s,之后行情断了

	// 行情断流:窗口里只剩 1s 之前的样本,5s/30s 永远采不到。
	tr.mu.Lock()
	tr.mids["MOVE_USDT"] = []midSample{{ts: t0.Add(2 * time.Second), mid: 100.05}}
	tr.resolveLocked("MOVE_USDT", t0.Add(3*time.Minute)) // 超过 30s+2min,判超时
	tr.mu.Unlock()

	if n := tr.PendingCount(); n != 0 {
		t.Fatalf("超时的成交应被清出 pending,还剩 %d", n)
	}
	waitFor(t, 2*time.Second, "残缺记录落到 sink", func() bool { return len(sink.snapshot()) == 1 })
	rec := sink.snapshot()[0]
	if rec.Complete {
		t.Fatal("采不齐的记录必须标成 Complete=false,否则统计会把它当完整样本")
	}
	if len(rec.Points) != 1 || rec.Points[0].Horizon != "1s" {
		t.Fatalf("应只保留实际采到的 1s 那一点,得到 %+v", rec.Points)
	}
	if err := rec.Verify(1e-9); err != nil {
		t.Fatalf("残缺记录里已有的那一点也必须自洽: %v", err)
	}
}

// TestStaleSampleResolvesCompleteWithBigLag 记录一个【既有】行为,顺带说明
// 为什么 lag 必须逐条落库。
//
// resolveLocked 取的是"target 之后的第一个样本",对样本有多晚【不设上限】。
// 于是行情断流 5 分钟后恢复的第一个中价,会同时把 1s/5s/30s 三个 horizon 全结算掉:
// 这笔成交在 Stats() 里和一笔真的 30s 样本一模一样,分不出来。
// (markout_test.go 里 TestStaleFillsAreDropped 的名字有误导:那笔并没有被丢弃,
// 是被【用 5 分钟后的价】结算了,PendingCount 归零是因为它完成了。)
//
// 本轮不改这个判定 —— 那是测量口径变更,该由所有者拍板。这里做的是让它
// 【可见】:每个 horizon 的 lag_*_ms 都落库,复核方加一句
// `WHERE lag_30s_ms < 2000` 就能把这类样本剔掉。落库之前,这件事在事后完全不可查。
func TestStaleSampleResolvesCompleteWithBigLag(t *testing.T) {
	sink := &captureSink{}
	SetMarkoutSink(sink, 16)
	defer SetMarkoutSink(nil, 0)

	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "MOVE_USDT", "f-stale", "buy", 100, 1, 20, t0)
	tr.Observe("MOVE_USDT", 100.05, t0.Add(5*time.Minute)) // 断流恢复后的第一个样本

	waitFor(t, 2*time.Second, "记录落到 sink", func() bool { return len(sink.snapshot()) == 1 })
	rec := sink.snapshot()[0]
	if !rec.Complete || len(rec.Points) != 3 {
		t.Fatalf("既有行为是三个 horizon 一次性结算,得到 complete=%v points=%d", rec.Complete, len(rec.Points))
	}
	for _, p := range rec.Points {
		if p.LagMs < 4*60*1000 {
			t.Fatalf("%s 的采样滞后应有数分钟,得到 %dms —— lag 落不下来这类样本就无法剔除",
				p.Horizon, p.LagMs)
		}
	}
}

// TestMidAtFillIsCapturedFromBeforeTheFill:成交时刻的中价必须取【成交之前】
// 最后一个样本。取之后的样本会把成交后的漂移算进"拿到的边",两个量互相污染,
// 那就再也分不清"报价挂得好"和"成交后走运"。
func TestMidAtFillIsCapturedFromBeforeTheFill(t *testing.T) {
	sink := &captureSink{}
	SetMarkoutSink(sink, 16)
	defer SetMarkoutSink(nil, 0)

	tr := NewMarkoutTracker()
	t0 := time.Now()
	// 成交前后各喂样本;登记发生在成交后 300ms(引擎轮询滞后的真实形态)
	tr.Observe("ONG_USDT", 100.00, t0.Add(-800*time.Millisecond))
	tr.Observe("ONG_USDT", 100.02, t0.Add(-200*time.Millisecond)) // 成交前最后一个
	tr.Observe("ONG_USDT", 100.50, t0.Add(300*time.Millisecond))  // 成交之后,不能用
	tr.RecordFill("gate", "ONG_USDT", "f-mid", "buy", 99.90, 1, 20, t0)
	tr.Observe("ONG_USDT", 100.05, t0.Add(1*time.Second))
	tr.Observe("ONG_USDT", 100.10, t0.Add(5*time.Second))
	tr.Observe("ONG_USDT", 100.20, t0.Add(30*time.Second))

	waitFor(t, 2*time.Second, "记录落到 sink", func() bool { return len(sink.snapshot()) == 1 })
	rec := sink.snapshot()[0]
	if rec.MidAtFill != 100.02 {
		t.Fatalf("成交时中价应取成交前最后一个样本 100.02,得到 %v", rec.MidAtFill)
	}
	if rec.MidAtFillLagMs != 200 {
		t.Fatalf("该样本比成交早 200ms,得到 %dms", rec.MidAtFillLagMs)
	}
	// 拿到的边 =(成交时中价 − 成交价)/成交价,买单为正说明买得比中价便宜。
	approx(t, (rec.MidAtFill-rec.FillPx)/rec.FillPx*10000, 12.01, 0.02, "成交时拿到的边")

	// 成交时刻没有样本覆盖时留零值,不能瞎填一个后来的价。
	tr2 := NewMarkoutTracker()
	tr2.RecordFill("gate", "NEW_USDT", "f-nomid", "buy", 100, 1, 20, t0)
	tr2.Observe("NEW_USDT", 100.05, t0.Add(1*time.Second))
	tr2.Observe("NEW_USDT", 100.05, t0.Add(5*time.Second))
	tr2.Observe("NEW_USDT", 100.05, t0.Add(30*time.Second))
	waitFor(t, 2*time.Second, "第二条记录落到 sink", func() bool { return len(sink.snapshot()) == 2 })
	if got := sink.snapshot()[1]; got.MidAtFill != 0 || got.MidAtFillLagMs != 0 {
		t.Fatalf("成交时刻无样本时应留零值,得到 mid=%v lag=%d", got.MidAtFill, got.MidAtFillLagMs)
	}
}

// TestMarkoutEmitIsNoOpWithoutSink:没装 sink 时 emit 不能 panic、不能阻塞。
// 这是默认生产状态(表还没建),必须是彻底的 no-op。
func TestMarkoutEmitIsNoOpWithoutSink(t *testing.T) {
	SetMarkoutSink(nil, 0)
	before := MarkoutSinkCounters()
	tr := NewMarkoutTracker()
	t0 := time.Now()
	tr.RecordFill("gate", "NIL_USDT", "f1", "buy", 100, 1, 20, t0)
	tr.Observe("NIL_USDT", 100.05, t0.Add(1*time.Second))
	tr.Observe("NIL_USDT", 100.05, t0.Add(5*time.Second))
	tr.Observe("NIL_USDT", 100.05, t0.Add(30*time.Second))
	after := MarkoutSinkCounters()
	// enqueued/dropped 由 emit 同步累加,可以做精确差值断言;
	// written/failed 由 writer goroutine 异步累加,不在这里断言(见上一条用例的注释)。
	if after.Enqueued-before.Enqueued != 1 || after.Dropped-before.Dropped != 1 {
		t.Fatalf("无 sink 时应记一次入队+一次丢弃,得到 enq=%d drop=%d",
			after.Enqueued-before.Enqueued, after.Dropped-before.Dropped)
	}
}
