package marketmaker

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// failsafe_silence_test.go 锁两处【避险静默失效】的路径:
//
//	① engine.go cancelAll:读挂单失败 → 零日志 return;逐张撤单的错误 `_ =` 丢掉。
//	   它是【所有】避险路径的共同动作(参考流过期/持续偏离/单日止损/熔断站下/
//	   优雅关闭/开机清残留/余额避险),调用方全部认为"已撤干净"。
//	② deadman.go runDeadMansSwitch:每个 pair arm 失败只 Warnf,紧接着无条件打
//	   Info「死人开关启动」。凭据一错 → 交易所侧倒计时从未 arm 过,进程被 SIGKILL
//	   后挂单原样留在盘口。
//
// 这些测试全部在【沙箱】里跑:假交易所 + httptest,tradeHTTP 被换成一个恒报错的
// 传输层,任何一发真实出网请求都会失败而不是打到 gate。

// blockedTransport 是"测试里绝不许碰真实交易所"的硬闸。
type blockedTransport struct{}

func (blockedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("测试禁止访问真实交易所")
}

// blockRealExchange 把两条【host 写死在函数里、只认 env 凭据】的出网路径换掉。
// 名字叫 gate 的场馆在 runPair 里会走到它们:
//
//	tradeHTTP —— gateMyTrades(单日止损/markout 拉成交)
//	feeHTTP   —— MakerFeeBps(费率档,每个报价周期问一次,首次会真发请求)
//
// 不挡住,这两发就会带着测试里的假凭据打到 api.gateio.ws 上去。
func blockRealExchange(t *testing.T) {
	t.Helper()
	origTrade, origFee := tradeHTTP, feeHTTP
	blocked := &http.Client{Transport: blockedTransport{}, Timeout: time.Second}
	tradeHTTP, feeHTTP = blocked, blocked
	t.Cleanup(func() { tradeHTTP, feeHTTP = origTrade, origFee })
}

func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	var buf syncBuffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return &buf
}

// ── ① cancelAll:撤没撤成必须是可判定的数据 ──

// TestSweepCancelReportsUnprovenCancel:优雅关闭时撤单档读失败,绝不许再宣布
// 「已撤所有挂单」。那句话是所有者事后判断"进程退干净了没有"的唯一依据,
// 而读失败的时候我们【压根没看见盘口】。
func TestSweepCancelReportsUnprovenCancel(t *testing.T) {
	buf := captureLog(t)
	ex := &hedgeExec{cancelReadErr: fmt.Errorf("gate GET /spot/open_orders: i/o timeout")}
	e := &Engine{
		cfg:   Config{Pairs: []PairConfig{{Exec: "ex", ExecSymbol: "SOL_USDT"}}},
		execs: map[string]ExecExchange{"ex": ex},
	}
	e.sweepCancel(2 * time.Second)

	got := buf.String()
	if strings.Contains(got, "已撤所有挂单") {
		t.Fatalf("撤单档读失败时仍宣布「已撤所有挂单」—— 这是假绿,日志:\n%s", got)
	}
	if !strings.Contains(got, "[ERROR]") {
		t.Fatalf("撤单没被证实做成必须打 ERROR,实际日志:\n%s", got)
	}
}

// TestSweepCancelReportsFailedCancels:读到了挂单、但逐张撤单全部失败,
// 同样不许宣布已撤干净 —— 原来这些错误是 `_ =` 直接丢掉的。
func TestSweepCancelReportsFailedCancels(t *testing.T) {
	buf := captureLog(t)
	ex := &hedgeExec{
		cancelErr: fmt.Errorf("gate DELETE /spot/orders: HTTP 429"),
		resting:   []OpenOrder{{ID: "a", Side: "BUY"}, {ID: "b", Side: "SELL"}},
	}
	e := &Engine{
		cfg:   Config{Pairs: []PairConfig{{Exec: "ex", ExecSymbol: "SOL_USDT"}}},
		execs: map[string]ExecExchange{"ex": ex},
	}
	e.sweepCancel(2 * time.Second)

	got := buf.String()
	if strings.Contains(got, "已撤所有挂单") {
		t.Fatalf("两张单一张都没撤掉,却宣布「已撤所有挂单」,日志:\n%s", got)
	}
	if !strings.Contains(got, "[ERROR]") {
		t.Fatalf("撤单失败必须打 ERROR,实际日志:\n%s", got)
	}
}

// ── 沙箱假交易所:能编排"某个读在第几次开始失败" ──

type probeExec struct {
	mu sync.Mutex

	name string
	// balOKAfter:前 balOKAfter 次 Balances 报错(模拟"真的问了但答不上来"),之后成功。
	balFailFirst int
	balCalls     int
	// cancelReadOKFirst:前这么多次撤单档读成功,之后一律失败。
	// 用它把"开机清残留"和"运行中避险撤单"两件事分开编排。
	cancelReadOKFirst int
	cancelReads       int

	placed int
}

func (p *probeExec) Name() string        { return p.name }
func (p *probeExec) SupportsShort() bool { return false }

func (p *probeExec) FetchBookTicker(string) (BookTicker, error) {
	return BookTicker{BidPx: 99.9, AskPx: 100.1, Ts: time.Now()}, nil
}

func (p *probeExec) SymbolFilter(string) (SymbolFilter, error) {
	return SymbolFilter{BaseAsset: "SOL", QuoteAsset: "USDT", TickSize: 0.01, StepSize: 0.001, MinNotional: 1}, nil
}

func (p *probeExec) Balances() (map[string]float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.balCalls++
	if p.balCalls <= p.balFailFirst {
		// 不是 ErrGateRateLimited —— 请求真出网了却没拿到答案,即"我不知道自己的余额"。
		return nil, fmt.Errorf("gate GET /spot/accounts: i/o timeout")
	}
	return map[string]float64{"SOL": 10, "USDT": 1000}, nil
}

func (p *probeExec) OpenOrders(string) ([]OpenOrder, error) { return nil, nil }

func (p *probeExec) OpenOrdersForCancel(string) ([]OpenOrder, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelReads++
	if p.cancelReads > p.cancelReadOKFirst {
		return nil, fmt.Errorf("gate GET /spot/open_orders: i/o timeout")
	}
	return nil, nil
}

func (p *probeExec) CancelOrder(string, string) error { return nil }

func (p *probeExec) PlaceLimit(string, string, float64, float64, string, bool) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.placed++
	return "id", nil
}

func (p *probeExec) counts() (placed, cancelReads int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.placed, p.cancelReads
}

var (
	_ ExecExchange     = (*probeExec)(nil)
	_ CancelPathReader = (*probeExec)(nil)
)

const probeExecName = "probe-exec"

var probeExecInstance *probeExec

func init() {
	RegisterExec(probeExecName, func(ExecConfig) (ExecExchange, error) { return probeExecInstance, nil })
}

// startProbeEngine 起一台只连假交易所的引擎(feed 用 graceful_shutdown_test.go 里的
// pushFeed),跑 wait 之后停掉。
func startProbeEngine(t *testing.T, ex *probeExec, wait time.Duration) {
	t.Helper()
	blockRealExchange(t)
	probeExecInstance = ex
	cfg := Config{
		Enabled: true, ObserveOnly: false, Feed: shutdownFeedName,
		Exec: []ExecConfig{{Name: probeExecName}},
		Pairs: []PairConfig{{
			FeedSymbol: "SOLUSDT", Exec: probeExecName, ExecSymbol: "SOL_USDT",
			SpreadBps: 20, OrderQty: 1, MaxPosition: 10, RefreshMs: 20,
		}},
	}
	eng, err := Start(cfg)
	if err != nil || eng == nil {
		t.Fatalf("引擎应当起来: eng=%v err=%v", eng, err)
	}
	t.Cleanup(eng.Stop)
	time.Sleep(wait)
}

// TestQuotingRefusesUntilCancelProven 是本轮最要紧的那条:
// 撤单读失败 → 零日志返回 → 调用方以为撤干净了 → 触发条件一解除就【恢复报价】。
//
// 现场:第 1 个周期余额读失败 → 走 cancelAll 避险 → 撤单档读失败(什么都没撤成);
// 第 2 个周期起余额读恢复正常 → 避险的触发条件消失了。
// 改动前:引擎直接恢复报价,而那次避险到底撤没撤成【没有任何人问过】。
// 改动后:撤单没被证实撤干净之前,这个 pair 不许恢复报价,而且必须出声。
func TestQuotingRefusesUntilCancelProven(t *testing.T) {
	buf := captureLog(t)
	ex := &probeExec{
		name:              "probe-venue",
		balFailFirst:      1, // 只有第一轮读不到余额
		cancelReadOKFirst: 1, // 开机清残留那一次成功,之后的避险撤单一律读失败
	}
	startProbeEngine(t, ex, 400*time.Millisecond)

	placed, reads := ex.counts()
	if reads < 2 {
		t.Fatalf("测试没造出现场:撤单档读只被调用了 %d 次(至少要有开机清理 + 一次避险撤单)", reads)
	}
	if placed != 0 {
		t.Fatalf("那次避险撤单从没被证实做成,引擎却挂了 %d 张新单 —— "+
			"六条避险路径共用的 cancelAll 读失败时是零日志 return,调用方全都以为撤干净了。日志:\n%s",
			placed, buf.String())
	}
	if !strings.Contains(buf.String(), "[ERROR]") {
		t.Fatalf("撤单没被证实做成必须出声,实际日志:\n%s", buf.String())
	}
}

// TestStartupCancelUnprovenBlocksQuoting:开机清残留读失败时不许开始报价。
// 那一步的语义是"上一轮/崩溃留下的孤儿单已经清掉了",读失败时它一张也没清,
// 而 blindClock 那句"此刻盘口是空的"也就成了假话。
func TestStartupCancelUnprovenBlocksQuoting(t *testing.T) {
	buf := captureLog(t)
	ex := &probeExec{name: "probe-venue", cancelReadOKFirst: 0} // 一次都读不成
	startProbeEngine(t, ex, 300*time.Millisecond)

	if placed, _ := ex.counts(); placed != 0 {
		t.Fatalf("开机清残留一次都没成功,引擎却挂了 %d 张单(等于在一堆来路不明的孤儿单上加挂)。日志:\n%s",
			placed, buf.String())
	}
	if !strings.Contains(buf.String(), "[ERROR]") {
		t.Fatalf("开机清残留失败必须出声,实际日志:\n%s", buf.String())
	}
}

// ── ② 死人开关:arm 没上就不许报价 ──

// gateProbeExec 和 probeExec 一样,只是 Name() 报 "gate" —— 死人开关只覆盖 gate。
type gateProbeExec struct{ *probeExec }

func (gateProbeExec) Name() string { return "gate" }

const gateProbeExecName = "gate-probe-exec"

var gateProbeInstance *probeExec

func init() {
	RegisterExec(gateProbeExecName, func(ExecConfig) (ExecExchange, error) {
		return gateProbeExec{probeExec: gateProbeInstance}, nil
	})
}

// startGateProbeEngine 起一台 exec 是 gate 场馆的引擎,死人开关指向假交易所 srv。
func startGateProbeEngine(t *testing.T, ex *probeExec, srvURL string, wait time.Duration) {
	t.Helper()
	blockRealExchange(t)
	t.Setenv("MM_GATE_API_KEY", "sandbox-fake-key")
	t.Setenv("MM_GATE_API_SECRET", "sandbox-fake-secret")
	orig := deadmanHost
	deadmanHost = srvURL
	t.Cleanup(func() { deadmanHost = orig })

	gateProbeInstance = ex
	cfg := Config{
		Enabled: true, ObserveOnly: false, Feed: shutdownFeedName,
		Exec: []ExecConfig{{Name: gateProbeExecName}},
		Pairs: []PairConfig{{
			FeedSymbol: "SOLUSDT", Exec: gateProbeExecName, ExecSymbol: "SOL_USDT",
			SpreadBps: 20, OrderQty: 1, MaxPosition: 10, RefreshMs: 20,
		}},
	}
	eng, err := Start(cfg)
	if err != nil || eng == nil {
		t.Fatalf("引擎应当起来: eng=%v err=%v", eng, err)
	}
	t.Cleanup(eng.Stop)
	time.Sleep(wait)
}

// TestDeadmanArmFailureBlocksQuoting:交易所侧倒计时 arm 不上时不许报价。
//
// arm 失败 = 进程被 SIGKILL 之后【没有任何东西】保证挂单会被撤掉。
// 改动前:只 Warnf 一行,紧接着无条件打 Info「死人开关启动」,然后照常报价。
func TestDeadmanArmFailureBlocksQuoting(t *testing.T) {
	buf := captureLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"label":"INVALID_KEY"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	ex := &probeExec{name: "gate", cancelReadOKFirst: 1000}
	startGateProbeEngine(t, ex, srv.URL, 400*time.Millisecond)

	got := buf.String()
	if placed, _ := ex.counts(); placed != 0 {
		t.Fatalf("死人开关一次都没 arm 成功,引擎却挂了 %d 张单 —— "+
			"进程被 SIGKILL 后这些单会原样留在盘口。日志:\n%s", placed, got)
	}
	if strings.Contains(got, "死人开关启动") {
		t.Fatalf("一个 pair 都没 arm 上却打了「死人开关启动」—— 这就是假绿,日志:\n%s", got)
	}
	if !strings.Contains(got, "[ERROR]") {
		t.Fatalf("arm 失败必须是 ERROR(它是唯一能扛进程崩溃的兜底),实际日志:\n%s", got)
	}
}

// TestDeadmanArmTimeoutBlocksQuoting:arm 超时(不是 4xx,是压根没回)也算没 arm 上。
func TestDeadmanArmTimeoutBlocksQuoting(t *testing.T) {
	buf := captureLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(400 * time.Millisecond) // 比下面的 client 超时长
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	origHTTP := deadmanHTTP
	deadmanHTTP = &http.Client{Timeout: 50 * time.Millisecond}
	t.Cleanup(func() { deadmanHTTP = origHTTP })

	ex := &probeExec{name: "gate", cancelReadOKFirst: 1000}
	startGateProbeEngine(t, ex, srv.URL, 400*time.Millisecond)

	if placed, _ := ex.counts(); placed != 0 {
		t.Fatalf("arm 超时(交易所侧倒计时状态未知)时挂了 %d 张单,日志:\n%s", placed, buf.String())
	}
}

// TestDeadmanArmSuccessAllowsQuoting 是这道闸的【反向闸门】:arm 成功之后必须照常
// 报价。否则这次修复就成了一把砖 —— 所有者恢复引擎(#54 → #52)时会一张单都挂不出来。
func TestDeadmanArmSuccessAllowsQuoting(t *testing.T) {
	var armed int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		armed++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"triggerTime":1}`))
	}))
	defer srv.Close()

	ex := &probeExec{name: "gate", cancelReadOKFirst: 1000}
	startGateProbeEngine(t, ex, srv.URL, 500*time.Millisecond)

	mu.Lock()
	gotArms := armed
	mu.Unlock()
	if gotArms == 0 {
		t.Fatal("死人开关一次都没去 arm")
	}
	if placed, _ := ex.counts(); placed == 0 {
		t.Fatal("死人开关 arm 成功之后引擎仍然一张单都不挂 —— 这道闸变成了砖")
	}
}

// ── ③ 两份凭证本身的口径:零值必须是"未证实" ──

// TestCancelOutcomeZeroValueIsNotClean:手搓的/忘了填的 cancelOutcome 一律不算干净。
// 这是台账 #227 那一招的复刻 —— 闸判的是数据,而数据的零值站在安全的那一侧。
func TestCancelOutcomeZeroValueIsNotClean(t *testing.T) {
	if (cancelOutcome{}).clean() {
		t.Fatal("零值 cancelOutcome 必须是「未证实」")
	}
	if (cancelOutcome{read: true, seen: 2, failed: 1}).clean() {
		t.Fatal("读到了但有单没撤掉,不算干净")
	}
	if !(cancelOutcome{read: true, seen: 0}).clean() {
		t.Fatal("读到了、盘口本来就没单,应当算干净")
	}
}

// TestDeadmanArmCoverage:覆盖是【有时限的】——最后一次 arm 成功之后,交易所那边
// 最多再等 deadmanTimeout 就到点。所以连续 arm 失败不需要另写计数逻辑,
// 覆盖自己会过期。
func TestDeadmanArmCoverage(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	var d *deadmanArm
	if d.covered("SOL_USDT", t0) {
		t.Fatal("nil 凭证必须视为没覆盖(Engine 不经 Start 构造时就是这个形态)")
	}
	d = &deadmanArm{}
	if d.covered("SOL_USDT", t0) {
		t.Fatal("从没 arm 成功过 = 没覆盖")
	}
	d.mark("SOL_USDT", t0)
	if !d.covered("SOL_USDT", t0.Add(deadmanTimeout-time.Second)) {
		t.Fatal("倒计时还没到点就该算有覆盖(一次偶发刷新失败不该立刻掐掉报价)")
	}
	if d.covered("SOL_USDT", t0.Add(deadmanTimeout)) {
		t.Fatal("超过倒计时长度还没有新的成功 = 交易所侧已经到点 = 没覆盖")
	}
	if d.covered("ONG_USDT", t0) {
		t.Fatal("覆盖是按 symbol 记的,别的 symbol 不该跟着沾光")
	}
}
