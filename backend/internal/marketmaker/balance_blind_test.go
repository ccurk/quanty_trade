package marketmaker

import (
	"errors"
	"fmt"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// balance_blind_test.go 锁死这一轮的裁决:【本地名额耗尽】≠【余额真读不到】。
//
//	① 本地限流拒绝(请求没出网)不得触发余额避险 —— 否则惩罚档下常态走平 =
//	   做市停业,实测 20 个周期里 19 个盘口是空的(标记在 TestStandDownEndToEnd)。
//	② 但豁免必须有时间边界:盲得够久就退回避险,并且【出声】。
//	   "不算故障"不许变成一个永远不会响的静默洞(台账 #125 / #115 那个病)。
//	③ 边界的值不是拍的:它必须明显大于惩罚档下"两次成功读余额"的常态最长间隔,
//	   那个间隔在 TestBalanceBlindLimitExceedsRoutineGap 里当场量出来。

// TestLocalQuotaDenialIsNotABalanceFailure 是本轮的红→绿闸门。
//
// 改动前:Balances() 返回 ErrGateRateLimited(本地名额耗尽,一发都没出网)
//
//	→ 走"余额读取失败,撤单避险" → cancelAll → 盘口走平。
//	而这一档恰恰是常态(台账 #84 的惩罚档),于是做市在惩罚档下等于停业。
//
// 改动后:那是"我们自己选择这一轮不去问",不是"问了答不上来" —— 本轮什么都不做,
//
//	挂单原地留着;持续限流由站下状态机接管(一件事一个主人)。
func TestLocalQuotaDenialIsNotABalanceFailure(t *testing.T) {
	denied := fmt.Errorf("%w: quote 类名额耗尽(10/10s 窗口,给撤单预留 3),本轮放弃", ErrGateRateLimited)
	ex := &hedgeExec{
		balErr:  denied,
		resting: []OpenOrder{{ID: "resting-bid", Side: "BUY", Price: 99.5, Qty: 1}},
	}
	e := &Engine{cfg: Config{}}
	p := PairConfig{FeedSymbol: "SOLUSDT", Exec: "gate", ExecSymbol: "SOL_USDT",
		SpreadBps: 20, OrderQty: 1, MaxPosition: 10}
	ref := BookTicker{BidPx: 99.95, AskPx: 100.05, Ts: time.Now()}
	eb := BookTicker{BidPx: 99.9, AskPx: 100.1, Ts: time.Now()}

	// 刚刚才读到过余额 —— 远没到盲区边界。
	blind := &blindClock{lastOK: time.Now()}
	e.quote(p, ex, ref, eb, 0, blind)

	ex.mu.Lock()
	defer ex.mu.Unlock()
	if ex.cancelReads != 0 {
		t.Fatalf("本地名额耗尽不是「余额读不到」,不该触发避险:撤单档的读被调用了 %d 次"+
			"(说明又把「我们没问」当成了「问了答不上来」)", ex.cancelReads)
	}
	if len(ex.cancelled) != 0 {
		t.Fatalf("本地名额耗尽的一轮撤了 %d 张单 —— 惩罚档下这就是常态走平/做市停业", len(ex.cancelled))
	}
	if len(ex.resting) != 1 {
		t.Fatalf("挂单必须原地留着等下一个能报价的周期,实际剩 %d 张", len(ex.resting))
	}
	if ex.placed != 0 {
		t.Fatalf("读不到余额的一轮不许挂新单,实际挂了 %d 张", ex.placed)
	}
}

// TestBalanceBlindLimitFiresAfterBoundary 是那条时间边界的闸门:
// 豁免不许无限期。盲过 balanceBlindWindows×窗口就退回撤单避险,而且必须是 ERROR ——
// 这是那个"静默洞"唯一会响的地方。
//
// 同时锁住它【只响一次】:超边界那一次会重新计时,否则之后每个周期都撤一遍,
// 把一次性的避险变成每秒一次的撤单风暴。
func TestBalanceBlindLimitFiresAfterBoundary(t *testing.T) {
	var buf syncBuffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	denied := fmt.Errorf("%w: quote 类名额耗尽,本轮放弃", ErrGateRateLimited)
	ex := &hedgeExec{
		balErr:  denied,
		resting: []OpenOrder{{ID: "stale-bid", Side: "BUY", Price: 99.5, Qty: 1}},
	}
	rep := &reportingExec{hedgeExec: ex, status: RateLimitStatus{WindowMs: 10000}}
	e := &Engine{cfg: Config{}}
	p := PairConfig{FeedSymbol: "SOLUSDT", Exec: "gate", ExecSymbol: "SOL_USDT",
		SpreadBps: 20, OrderQty: 1, MaxPosition: 10}
	ref := BookTicker{BidPx: 99.95, AskPx: 100.05, Ts: time.Now()}
	eb := BookTicker{BidPx: 99.9, AskPx: 100.1, Ts: time.Now()}

	limit := balanceBlindLimit(rep)
	if limit != balanceBlindWindows*10*time.Second {
		t.Fatalf("边界应为 %d×窗口=20s,实际 %s", balanceBlindWindows, limit)
	}

	// 已经盲了 limit+1s:再叫"我们没问"就是自欺 —— 我们确实不知道余额了。
	blind := &blindClock{lastOK: time.Now().Add(-limit - time.Second)}
	e.quote(p, rep, ref, eb, 0, blind)

	ex.mu.Lock()
	reads, cancels := ex.cancelReads, len(ex.cancelled)
	ex.mu.Unlock()
	if reads == 0 || cancels != 1 {
		t.Fatalf("盲过 %s 必须退回撤单避险(否则「不算故障」就是个永远不会响的静默洞):"+
			"撤单档读 %d 次、撤了 %d 张", limit, reads, cancels)
	}
	if !strings.Contains(buf.String(), "[ERROR]") || !strings.Contains(buf.String(), "不再当作节流") {
		t.Fatalf("越过盲区边界必须打 ERROR(它会进 Lark 告警),实际日志:\n%s", buf.String())
	}

	// 第二轮:时钟已被重新计时,同样的错误应当重新被豁免,不再撤单。
	ex.mu.Lock()
	ex.resting = []OpenOrder{{ID: "another", Side: "BUY", Price: 99.5, Qty: 1}}
	ex.mu.Unlock()
	e.quote(p, rep, ref, eb, 0, blind)
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if len(ex.cancelled) != 1 {
		t.Fatalf("超边界那一次必须重新计时,否则之后每个周期都撤一遍(撤单风暴):实际累计撤了 %d 张", len(ex.cancelled))
	}
}

// TestBlindClockTolerateSemantics 把判据逐条钉死,不依赖任何 IO。
func TestBlindClockTolerateSemantics(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	const limit = 20 * time.Second
	local := fmt.Errorf("%w: quote 类名额耗尽", ErrGateRateLimited)
	real := errors.New("gate GET /spot/accounts -> 500: internal error")

	// 没有记忆 → fail-safe:当作真读不到。
	if ok, _ := (*blindClock)(nil).tolerate(local, limit, now); ok {
		t.Fatal("nil 时钟不知道自己盲了多久,必须按最坏情况处理(避险)")
	}
	if ok, _ := (&blindClock{}).tolerate(local, limit, now); ok {
		t.Fatal("零值时钟 = 从来没读到过余额,不得豁免")
	}
	// 请求出网了才失败 → 我们确实不知道余额,不管盲了多久都要避险。
	if ok, _ := (&blindClock{lastOK: now}).tolerate(real, limit, now); ok {
		t.Fatal("非本地拒绝(请求真出网了)必须走避险 —— 那才是「问了但答不上来」")
	}
	// 本地拒绝且没超边界 → 豁免。
	if ok, d := (&blindClock{lastOK: now.Add(-19 * time.Second)}).tolerate(local, limit, now); !ok || d != 19*time.Second {
		t.Fatalf("盲 19s(<20s 边界)应豁免,得到 ok=%v blindFor=%s", ok, d)
	}
	// 到边界 → 不再豁免,并重新计时。
	b := &blindClock{lastOK: now.Add(-limit)}
	if ok, d := b.tolerate(local, limit, now); ok || d != limit {
		t.Fatalf("盲满 %s 必须退回避险,得到 ok=%v blindFor=%s", limit, ok, d)
	}
	if !b.lastOK.Equal(now) {
		t.Fatal("越界那一次必须重新计时,否则之后每个周期都会再撤一遍")
	}
	// 一次成功读之后重新计时。
	b.ok(now.Add(time.Minute))
	if ok, d := b.tolerate(local, limit, now.Add(time.Minute)); !ok || d != 0 {
		t.Fatalf("刚成功读到余额之后应重新豁免,得到 ok=%v blindFor=%s", ok, d)
	}
}

// TestBalanceBlindLimitTracksWindow:边界的单位是【限流窗口】,跟着 window_ms 调档走,
// 不写死秒数;适配器不报告窗口时退回内置默认 10s,绝不退化成 0(那等于豁免失效)。
func TestBalanceBlindLimitTracksWindow(t *testing.T) {
	if got := balanceBlindLimit(&hedgeExec{}); got != 20*time.Second {
		t.Fatalf("不报告限流状态的适配器应按默认窗口 10s 算,得到 %s", got)
	}
	rep := &reportingExec{hedgeExec: &hedgeExec{}, status: RateLimitStatus{WindowMs: 30000}}
	if got := balanceBlindLimit(rep); got != 60*time.Second {
		t.Fatalf("窗口调到 30s 时边界应自动变成 60s,得到 %s", got)
	}
	zero := &reportingExec{hedgeExec: &hedgeExec{}, status: RateLimitStatus{WindowMs: 0}}
	if got := balanceBlindLimit(zero); got != 20*time.Second {
		t.Fatalf("窗口报 0 时必须回落到默认 10s(否则边界=0,豁免直接失效),得到 %s", got)
	}
}

// TestBalanceBlindLimitExceedsRoutineGap 量出"惩罚档下两次成功读余额之间最长隔多久",
// 并要求边界对它留出至少 2 倍余量 —— 这就是 balanceBlindWindows=2 的依据本身。
//
// 边界要是贴着常态间隔,惩罚档下每隔十几秒就会误报一次"读不到余额"并撤单,
// 等于这一轮什么都没改。
func TestBalanceBlindLimitExceedsRoutineGap(t *testing.T) {
	for _, refreshMs := range []int{500, 1000, 2000} {
		srv := &fakeGateServer{hits: map[string]int{}}
		ts := httptest.NewServer(srv.handler())
		lim, clk := newTestLimiter(RateLimitConfig{}) // 默认档 = 台账 #84 的 10 请求/10 秒
		ex := &GateExchange{baseURL: ts.URL, apiKey: "k", secret: "s",
			http: ts.Client(), filters: map[string]SymbolFilter{}, limiter: lim}
		p := PairConfig{ExecSymbol: "SOL_USDT", SpreadBps: 20, OrderQty: 1, MaxPosition: 100, RefreshMs: refreshMs}

		lastOK := clk.now()
		var maxGap time.Duration
		var denied int
		const cycles = 300
		for i := 0; i < cycles; i++ {
			clk.advance(time.Duration(p.refresh()) * time.Millisecond)
			// 复刻 quote() 的报价类请求序列:Balances → OpenOrders →(缺腿时)补挂。
			if _, err := ex.Balances(); err != nil {
				if !errors.Is(err, ErrGateRateLimited) {
					t.Fatalf("假 gate 不该返回非限流错误: %v", err)
				}
				denied++
				if g := clk.now().Sub(lastOK); g > maxGap {
					maxGap = g
				}
				continue // 新语义:本地名额耗尽 = 这一轮不问,不避险
			}
			lastOK = clk.now()
			orders, err := ex.OpenOrders(p.ExecSymbol)
			if err != nil {
				continue
			}
			if len(orders) < 2 {
				_, _ = ex.PlaceLimit(p.ExecSymbol, "BUY", 99.5, 1, "GTC", true)
				_, _ = ex.PlaceLimit(p.ExecSymbol, "SELL", 100.5, 1, "GTC", true)
			}
		}
		ts.Close()

		limit := balanceBlindLimit(ex)
		t.Logf("refresh=%-5dms %d 个周期里余额读被本地挡下 %d 次(%.0f%%);两次成功读余额的最长间隔=%s;盲区边界=%s",
			refreshMs, cycles, denied, float64(denied)/float64(cycles)*100, maxGap, limit)
		if maxGap == 0 {
			t.Fatal("一次都没被挡下,这个测量没有意义 —— 检查限流器默认档是不是被改了")
		}
		if limit < 2*maxGap {
			t.Fatalf("盲区边界 %s 没有对常态最长间隔 %s 留出 2 倍余量:惩罚档下会周期性误报"+
				"「读不到余额」并撤单,等于没改", limit, maxGap)
		}
	}
}
