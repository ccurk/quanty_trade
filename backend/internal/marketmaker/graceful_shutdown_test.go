package marketmaker

import (
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// graceful_shutdown_test.go 是台账 #117 的闸门:【优雅撤单是死代码】。
//
// #117 的原文是两层:
//
//	外层(cmd/main.go):后端 0 处信号处理,mm.Stop() 挂在永不 Done 的
//	                   context.Background 上,生产里从没执行过 —— 由
//	                   cmd/shutdown_test.go 锁。
//	内层(本文件)  :就算 Stop() 真被调到,它自己也得撑得住关闭时的现场:
//	                 ① 得先把报价 worker 停掉再撤,否则边撤边挂,撤了等于没撤;
//	                 ② 关闭时大概率正被限流,撤单必须有硬上限,不能为了撤干净
//	                    把进程拖到被 SIGKILL。
//
// ── ① 顺序:先停 worker,再撤单 ──

// stopOrderExec 是一台"能把撤单卡住"的假交易所,用来把 Stop() 内部的时序摊开看:
// 撤单扫描一开始就停在那里,测试趁这段时间观察【报价 worker 还在不在挂新单】。
type stopOrderExec struct {
	mu     sync.Mutex
	placed int
	// armed 之前不卡:runPair 启动时那次"清残留挂单"也走同一个读口,
	// 卡住它会把引擎堵在启动阶段,测的就不是关闭了。
	armed                  bool
	placedWhenSweepStarted int

	sweepStarted chan struct{}
	release      chan struct{}
	once         sync.Once
}

func newStopOrderExec() *stopOrderExec {
	return &stopOrderExec{sweepStarted: make(chan struct{}), release: make(chan struct{})}
}

func (s *stopOrderExec) Name() string        { return shutdownExecName }
func (s *stopOrderExec) SupportsShort() bool { return false }

func (s *stopOrderExec) FetchBookTicker(string) (BookTicker, error) {
	return BookTicker{BidPx: 99.9, AskPx: 100.1, Ts: time.Now()}, nil
}

func (s *stopOrderExec) SymbolFilter(string) (SymbolFilter, error) {
	return SymbolFilter{BaseAsset: "SOL", QuoteAsset: "USDT", TickSize: 0.01, StepSize: 0.001, MinNotional: 1}, nil
}

func (s *stopOrderExec) Balances() (map[string]float64, error) {
	return map[string]float64{"SOL": 5, "USDT": 5000}, nil
}

// OpenOrders 恒返回空 → 每一轮都会想挂新单,这样"worker 还活着"可以直接用挂单数量看出来。
func (s *stopOrderExec) OpenOrders(string) ([]OpenOrder, error) { return nil, nil }

func (s *stopOrderExec) OpenOrdersForCancel(string) ([]OpenOrder, error) {
	s.mu.Lock()
	armed := s.armed
	if armed {
		s.placedWhenSweepStarted = s.placed
	}
	s.mu.Unlock()
	if !armed {
		return nil, nil
	}
	s.once.Do(func() { close(s.sweepStarted) })
	select {
	case <-s.release:
	case <-time.After(3 * time.Second): // 兜底,别把测试挂死
	}
	return nil, nil
}

func (s *stopOrderExec) CancelOrder(string, string) error { return nil }

func (s *stopOrderExec) PlaceLimit(string, string, float64, float64, string, bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.placed++
	return "id", nil
}

func (s *stopOrderExec) arm() {
	s.mu.Lock()
	s.armed = true
	s.mu.Unlock()
}

func (s *stopOrderExec) counts() (placed, atSweep int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.placed, s.placedWhenSweepStarted
}

var (
	_ ExecExchange     = (*stopOrderExec)(nil)
	_ CancelPathReader = (*stopOrderExec)(nil)
)

// pushFeed 订阅时立刻推一帧,让 runPair 拿得到参考盘口(refresh 很小、
// staleAfter 有 3s 下界,单次推送足够跑完一个短测试)。
type pushFeed struct{}

func (pushFeed) Name() string { return shutdownFeedName }
func (pushFeed) SubscribeBookTicker(_ string, cb func(BookTicker)) (func(), error) {
	cb(BookTicker{BidPx: 99.95, AskPx: 100.05, Ts: time.Now()})
	return func() {}, nil
}
func (pushFeed) FetchBookTicker(string) (BookTicker, error) {
	return BookTicker{BidPx: 99.95, AskPx: 100.05, Ts: time.Now()}, nil
}

const (
	shutdownFeedName = "shutdown-feed"
	shutdownExecName = "shutdown-exec"
)

var shutdownExecInstance *stopOrderExec

func init() {
	RegisterFeed(shutdownFeedName, func() (FeedSource, error) { return pushFeed{}, nil })
	RegisterExec(shutdownExecName, func(ExecConfig) (ExecExchange, error) { return shutdownExecInstance, nil })
}

// TestStopStopsQuotingBeforeCancelSweep:优雅关闭必须【先停报价再撤单】。
//
// 反过来(撤单扫一遍的同时 worker 还在挂新单)的后果就是:扫完之后盘口上照样
// 留着一张我们不再管理的裸单 —— 优雅撤单等于白撤,而这条路径存在的全部意义
// 就是"绝不把裸单留在交易所"。
//
// 测法:把撤单扫描卡在第一步(读挂单)上,看这段时间里挂单数量会不会继续涨。
func TestStopStopsQuotingBeforeCancelSweep(t *testing.T) {
	ex := newStopOrderExec()
	shutdownExecInstance = ex
	cfg := Config{
		Enabled: true, ObserveOnly: false, Feed: shutdownFeedName,
		Exec: []ExecConfig{{Name: shutdownExecName}},
		Pairs: []PairConfig{{
			FeedSymbol: "SOLUSDT", Exec: shutdownExecName, ExecSymbol: "SOL_USDT",
			SpreadBps: 20, OrderQty: 1, MaxPosition: 10, RefreshMs: 20,
		}},
	}
	eng, err := Start(cfg)
	if err != nil || eng == nil {
		t.Fatalf("引擎应当起来: eng=%v err=%v", eng, err)
	}

	// 等报价 worker 真的开始挂单(否则下面测的是一台没在跑的引擎)。
	deadline := time.Now().Add(2 * time.Second)
	for {
		if p, _ := ex.counts(); p >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("报价 worker 一直没挂单,这个测试测不到东西")
		}
		time.Sleep(5 * time.Millisecond)
	}

	ex.arm()
	stopped := make(chan struct{})
	go func() { defer close(stopped); eng.Stop() }()

	select {
	case <-ex.sweepStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() 一直没走到撤单扫描")
	}
	// 撤单扫描已经开始并被卡住。此刻 worker 若还活着,它每 20ms 就会再挂两张。
	time.Sleep(300 * time.Millisecond)
	placedDuringSweep, atSweep := ex.counts()
	close(ex.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() 没有返回")
	}

	if placedDuringSweep != atSweep {
		t.Fatalf("撤单扫描进行中报价 worker 还在挂单:扫描开始时 %d 张,300ms 后 %d 张 —— "+
			"这样撤完盘口上照样留着新挂的裸单,优雅撤单等于白撤(必须先停 worker 再撤)",
			atSweep, placedDuringSweep)
	}
	if final, _ := ex.counts(); final != placedDuringSweep {
		t.Fatalf("Stop() 返回之后仍有新单挂出:%d → %d", placedDuringSweep, final)
	}
}

// ── ② Stop() 真的会撤单,而且每个 pair 都撤 ──

// TestStopCancelsEveryPair:优雅关闭要把【每个】配置的 pair 都扫一遍。
// 这也是 #117 那条链的终点:信号 → mm.Stop() → cancelAll → 撤单档读 → 逐张撤。
func TestStopCancelsEveryPair(t *testing.T) {
	a := &hedgeExec{resting: []OpenOrder{{ID: "a1", Side: "BUY"}, {ID: "a2", Side: "SELL"}}}
	b := &hedgeExec{resting: []OpenOrder{{ID: "b1", Side: "SELL"}}}
	e := &Engine{
		cfg: Config{Pairs: []PairConfig{
			{Exec: "ex-a", ExecSymbol: "SOL_USDT"},
			{Exec: "ex-b", ExecSymbol: "ONG_USDT"},
		}},
		execs: map[string]ExecExchange{"ex-a": a, "ex-b": b},
	}
	e.Stop()

	for name, ex := range map[string]*hedgeExec{"ex-a": a, "ex-b": b} {
		ex.mu.Lock()
		reads, left := ex.cancelReads, len(ex.resting)
		ex.mu.Unlock()
		if reads == 0 {
			t.Fatalf("%s:优雅关闭时撤单档的读一次都没被调用 —— 这个 pair 根本没被扫到", name)
		}
		if left != 0 {
			t.Fatalf("%s:优雅关闭后仍有 %d 张挂单留在交易所", name, left)
		}
	}
}

// TestStopObserveOnlyDoesNotCancel:观察模式下从没下过单,关闭时也不该去动交易所。
func TestStopObserveOnlyDoesNotCancel(t *testing.T) {
	ex := &hedgeExec{resting: []OpenOrder{{ID: "not-ours", Side: "BUY"}}}
	e := &Engine{
		cfg:   Config{ObserveOnly: true, Pairs: []PairConfig{{Exec: "ex", ExecSymbol: "SOL_USDT"}}},
		execs: map[string]ExecExchange{"ex": ex},
	}
	e.Stop()
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if ex.cancelReads != 0 || len(ex.resting) != 1 {
		t.Fatal("observe_only 模式下 Stop 不得碰交易所")
	}
}

// ── ③ 撤单必须有硬上限:不能为了撤干净而被 SIGKILL ──

// hangingExec 的撤单档读永不返回 —— 就是"关闭时正被限流、撤单卡在等名额上"的极端形态。
type hangingExec struct{ *hedgeExec }

func (h *hangingExec) OpenOrdersForCancel(string) ([]OpenOrder, error) {
	select {} // 永远卡住
}

// TestSweepCancelIsBounded:撤单卡死时 sweepCancel 必须在预算内放弃并【出声】。
// 静默放弃 = 给自己留一个查不出来的残留挂单(台账 #125 静默 exit 0 那个病)。
func TestSweepCancelIsBounded(t *testing.T) {
	var buf syncBuffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	ex := &hangingExec{hedgeExec: &hedgeExec{}}
	e := &Engine{
		cfg:   Config{Pairs: []PairConfig{{Exec: "ex", ExecSymbol: "SOL_USDT"}}},
		execs: map[string]ExecExchange{"ex": ex},
	}
	start := time.Now()
	e.sweepCancel(150 * time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("撤单卡死时 sweepCancel 必须按预算放弃,实际花了 %s —— 拖过宽限期就是被 SIGKILL", elapsed)
	}
	if !strings.Contains(buf.String(), "[ERROR]") || !strings.Contains(buf.String(), "残留挂单") {
		t.Fatalf("撤单没跑完必须打 ERROR 说明可能有残留挂单,实际日志:\n%s", buf.String())
	}
}

// TestShutdownBudgetsFitContainerGrace 把预算的依据钉在测试里:
// 两段预算加起来必须装进容器的 SIGTERM 宽限期(docker 默认 10s,
// docker-compose.prod.yml 未配 stop_grace_period),否则撤到一半就被 SIGKILL。
// 同时 cancel 段不得小于"一发撤单最坏情况下等名额+一次退避"的耗时,
// 否则等于保证第一发都撤不完。
func TestShutdownBudgetsFitContainerGrace(t *testing.T) {
	const containerGrace = 10 * time.Second
	if shutdownDrainBudget+shutdownCancelBudget >= containerGrace {
		t.Fatalf("drain %s + cancel %s 已经吃满容器宽限期 %s,关闭会被 SIGKILL 截断",
			shutdownDrainBudget, shutdownCancelBudget, containerGrace)
	}
	rl := RateLimitConfig{}.defaults()
	oneCancelWorst := time.Duration(rl.MaxWaitMs+rl.MaxBackoffMs) * time.Millisecond
	if shutdownCancelBudget < oneCancelWorst {
		t.Fatalf("撤单预算 %s 小于单发撤单最坏耗时 %s(等名额 %dms + 一次退避 %dms),第一发就撤不完",
			shutdownCancelBudget, oneCancelWorst, rl.MaxWaitMs, rl.MaxBackoffMs)
	}
}
