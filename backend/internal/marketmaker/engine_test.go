package marketmaker

import (
	"bytes"
	"encoding/json"
	"log"
	"math"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRoundToTick(t *testing.T) {
	// bid floors to tick, ask ceils to tick — never crossing the target inward.
	if got := roundToTick(100.037, 0.01, false); math.Abs(got-100.03) > 1e-9 {
		t.Fatalf("bid floor: got %v want 100.03", got)
	}
	if got := roundToTick(100.031, 0.01, true); math.Abs(got-100.04) > 1e-9 {
		t.Fatalf("ask ceil: got %v want 100.04", got)
	}
	if got := roundToTick(50, 0, false); got != 50 { // zero tick = passthrough
		t.Fatalf("zero tick passthrough: got %v", got)
	}
}

func TestRoundToStep(t *testing.T) {
	if got := roundToStep(1.2345, 0.001); math.Abs(got-1.234) > 1e-9 {
		t.Fatalf("step floor: got %v want 1.234", got)
	}
	if got := roundToStep(5, 0); got != 5 {
		t.Fatalf("zero step passthrough: got %v", got)
	}
}

func TestBookTickerMath(t *testing.T) {
	b := BookTicker{BidPx: 100, AskPx: 102}
	if b.Mid() != 101 {
		t.Fatalf("mid: got %v want 101", b.Mid())
	}
	if math.Abs(b.SpreadBps()-198.0198) > 0.01 {
		t.Fatalf("spread bps: got %v want ~198.02", b.SpreadBps())
	}
	if (BookTicker{BidPx: 0, AskPx: 102}).Mid() != 0 {
		t.Fatalf("missing side must give mid 0")
	}
}

func TestSkewedQuote(t *testing.T) {
	const refMid, half = 100.0, 0.001 // 10bps half-spread; tick=0 for exact math

	// invRatio=0 → symmetric around refMid (99.9 / 100.1).
	bid0, ask0 := skewedQuote(refMid, half, 0, 1.0, 0)
	if math.Abs((bid0+ask0)/2-refMid) > 1e-9 {
		t.Fatalf("inv=0 must be symmetric: bid=%v ask=%v", bid0, ask0)
	}
	if math.Abs(bid0-99.9) > 1e-9 || math.Abs(ask0-100.1) > 1e-9 {
		t.Fatalf("inv=0 quotes: got bid=%v ask=%v want 99.9/100.1", bid0, ask0)
	}

	// invRatio=1, frac=1 → center=refMid*(1-half): ask pulled in to ~mid (sell hard),
	// and BOTH quotes must sit below the inv=0 quotes (the whole book leans to sell).
	bid1, ask1 := skewedQuote(refMid, half, 1, 1.0, 0)
	if math.Abs(ask1-refMid) > 0.001 {
		t.Fatalf("inv=1 ask should sit ~mid (%.1f): got %v", refMid, ask1)
	}
	if ask1 >= ask0 || bid1 >= bid0 {
		t.Fatalf("full inventory must lower BOTH quotes: ask %v->%v bid %v->%v", ask0, ask1, bid0, bid1)
	}

	// Monotone: more inventory ⇒ lower ask (sell sooner) and lower bid (buy less).
	prevAsk, prevBid := math.Inf(1), math.Inf(1)
	for _, r := range []float64{0, 0.25, 0.5, 0.75, 1} {
		b, a := skewedQuote(refMid, half, r, 1.0, 0)
		if a > prevAsk || b > prevBid {
			t.Fatalf("skew not monotone at invRatio=%v: ask=%v(prev %v) bid=%v(prev %v)", r, a, prevAsk, b, prevBid)
		}
		prevAsk, prevBid = a, b
	}

	// invRatio>1 clamps to 1 (never over-skews past full).
	bHi, aHi := skewedQuote(refMid, half, 5, 1.0, 0)
	if math.Abs(aHi-ask1) > 1e-9 || math.Abs(bHi-bid1) > 1e-9 {
		t.Fatalf("invRatio>1 must clamp to 1: got bid=%v ask=%v", bHi, aHi)
	}
}

func TestRideToBook(t *testing.T) {
	// PORTAL 真实场景:gate 比 binance 贵。我们按 spread 算出的卖价 100.15 远在 gate 卖一
	// (100.33)之下 → 过去就是这样贱卖。ride 后卖价应顶到 gate 卖一−tick=100.32,吃满溢价;
	// 买价 99.85 已在 gate 买一(100.11)之下 → 不动。
	bid, ask := rideToBook(99.85, 100.15, 100.11, 100.33, 0.01)
	if math.Abs(ask-100.32) > 1e-9 {
		t.Fatalf("ask should ride up to venue ask-tick 100.32, got %v", ask)
	}
	if bid != 99.85 {
		t.Fatalf("bid should stay at fair floor 99.85, got %v", bid)
	}

	// 紧盘口(SOL 类):gate 卖一 100.02 比我们的 100.10 更低 → 不下调(不砍自己的价);
	// gate 买一 99.98 比我们的 99.90 更高 → 不上调(不追高)。两边原样 = 守住 fair 价差。
	bid, ask = rideToBook(99.90, 100.10, 99.98, 100.02, 0.01)
	if bid != 99.90 || ask != 100.10 {
		t.Fatalf("tight book must not tighten our quotes, got %v/%v", bid, ask)
	}

	// 折价所:gate 买一 100.30 高于我们的买价上限 → 不追(买价不得越过 fair 上限)。
	bid, ask = rideToBook(99.90, 100.50, 100.30, 100.40, 0.01)
	if bid != 99.90 {
		t.Fatalf("bid must not chase a premium venue bid, got %v", bid)
	}

	// post-only 安全:算出的卖价会穿过 gate 买一 → 抬到买一+tick,不成 taker。
	bid, ask = rideToBook(99.0, 99.5, 100.0, 100.2, 0.01)
	if ask <= 100.0 {
		t.Fatalf("ask must not cross into venue bid, got %v", ask)
	}

	// 盘口缺边(0)跳过 ride 与安全钳制。
	bid, ask = rideToBook(99.0, 101.0, 0, 0, 0.01)
	if bid != 99.0 || ask != 101.0 {
		t.Fatalf("zero book sides must pass through, got %v/%v", bid, ask)
	}
}

func TestExecRegistry(t *testing.T) {
	for _, name := range []string{"coinsph", "mexc", "gate", "kucoin"} {
		ex, err := NewExec(ExecConfig{Name: name})
		if err != nil || ex == nil || ex.Name() != name {
			t.Fatalf("exec %q not registered correctly: ex=%v err=%v", name, ex, err)
		}
	}
	if _, err := NewExec(ExecConfig{Name: "nope"}); err == nil {
		t.Fatalf("unknown exec must error")
	}
}

func TestQuoteAnchor(t *testing.T) {
	// 基差场景:执行所中价比参考所高 45bps —— 复刻 2026-09-08 实测的 ONG_USDT。
	refMid, execMid := 100.0, 100.45

	// 默认(未配置)锚参考所,保持历史行为不变
	var pRef PairConfig
	if got := pRef.anchorMid(refMid, execMid); got != refMid {
		t.Fatalf("默认应锚参考所 %.4f,得到 %.4f", refMid, got)
	}
	// 显式 "ref" 同样锚参考所
	pExplicit := PairConfig{QuoteAnchor: "ref"}
	if got := pExplicit.anchorMid(refMid, execMid); got != refMid {
		t.Fatalf(`"ref" 应锚参考所,得到 %.4f`, got)
	}
	// "exec" 锚执行所
	pExec := PairConfig{QuoteAnchor: "exec"}
	if got := pExec.anchorMid(refMid, execMid); got != execMid {
		t.Fatalf(`"exec" 应锚执行所 %.4f,得到 %.4f`, execMid, got)
	}
	// 执行所中价拿不到(0)时必须回退参考所,绝不能拿 0 当中心去报价
	if got := pExec.anchorMid(refMid, 0); got != refMid {
		t.Fatalf("execMid=0 应回退参考所,得到 %.4f", got)
	}

	// 效果验证:半价差 15bps。锚参考所时两条腿都落在执行所盘口下方(错误一侧);
	// 锚执行所时才骑在执行所中价两边。
	half := 15.0 / 10000
	_, askRef := skewedQuote(pRef.anchorMid(refMid, execMid), half, 0, 1.0, 0)
	bidExec, askExec := skewedQuote(pExec.anchorMid(refMid, execMid), half, 0, 1.0, 0)
	if askRef >= execMid {
		t.Fatalf("锚参考所时卖价 %.4f 本应低于执行所中价 %.4f(这正是被秒吃的原因)", askRef, execMid)
	}
	if !(bidExec < execMid && askExec > execMid) {
		t.Fatalf("锚执行所时应骑在 %.4f 两侧,得到 买 %.4f 卖 %.4f", execMid, bidExec, askExec)
	}
}

// TestRoundTripVsBestEdge 锁死"面板净边虚高 = |基差| − 半价差"这条推导(台账 #7)。
// 只要 |基差| > 半价差,单腿口径(NetBestEdgeBps)就比真实来回口径更高,
// 于是把"两个所本来就不同价"排成了"机会"。
func TestRoundTripVsBestEdge(t *testing.T) {
	const refMid, feeBps = 100.0, 2.0
	check := func(basisBps, spreadBps float64) (netBest, roundTrip float64) {
		execMid := refMid * (1 + basisBps/10000)
		half := spreadBps / 2 / 10000
		ebBid, ebAsk := execMid*(1-half), execMid*(1+half)
		buyEdge := (refMid - ebBid) / refMid * 10000
		sellEdge := (ebAsk - refMid) / refMid * 10000
		best := math.Max(buyEdge, sellEdge)
		return best - 2*feeBps, (buyEdge + sellEdge) - 2*feeBps
	}

	// ONG_USDT 实测形态:基差 45.2bps ≫ 半价差 11.5bps。
	netBest, roundTrip := check(45.2, 23.0)
	if netBest <= roundTrip {
		t.Fatalf("基差 > 半价差时单腿口径必须虚高: netBest=%.1f roundTrip=%.1f", netBest, roundTrip)
	}
	if diff := netBest - roundTrip; math.Abs(diff-(45.2-11.5)) > 0.5 {
		t.Fatalf("虚高幅度应≈|基差|−半价差=33.7bps,得到 %.1f", diff)
	}
	// 无基差时两个口径的差只剩"少算一条腿的收入",不该出现虚高。
	netBest0, roundTrip0 := check(0, 23.0)
	if netBest0 >= roundTrip0 {
		t.Fatalf("无基差时来回口径必须不低于单腿口径: netBest=%.1f roundTrip=%.1f", netBest0, roundTrip0)
	}
}

// bookFromBasis 按"基差 b + 价差 s"反造一个执行所盘口,用来重放台账 #7 的形态。
// 反造而不是硬编码价格,是为了让测试里的 b/s 是【输入】,从而能和记录里读回的
// b/s 直接对账 —— 这正是离线复核者要做的事。
func bookFromBasis(refMid, basisBps, spreadBps float64) BookTicker {
	execMid := refMid * (1 + basisBps/10000)
	half := spreadBps / 2 / 10000
	return BookTicker{BidPx: execMid * (1 - half), AskPx: execMid * (1 + half), Ts: time.Now()}
}

// TestObserveRecordIsOfflineVerifiable 是台账 #7 的收尾闸门。
//
// #7 上一轮卡住的原因不是结论存疑,而是【结论无法被第二个人复核】:observe 只写内存,
// 重启即失,"中位基差 45.2bps"只活在代码注释里。本测试锁死修复后的性质:
//
//  1. 每轮观测都落一条单行 JSON(经 logger → main.go 已把 log 接到 logs/server.log);
//  2. 记录里带【原始盘口】,所有 bps 都能只用原始数重算 —— 派生量对不上就能被抓出来;
//  3. 记录足以重算 #7 的核心不等式 虚高 = |b| − s/2。
//
// 注意这是【替代验证】:2026-09-08 那次实测的原始样本从未落盘,已经找不回来,
// 本测试用一段合成序列(中位 b=45.2bps、中位 s=23.0bps,与当时报告的形态一致)
// 证明链路可复核,而不是在重新证明那个 45.2。
func TestObserveRecordIsOfflineVerifiable(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	p := PairConfig{FeedSymbol: "ONGUSDT", Exec: "gate", ExecSymbol: "ONG_USDT"} // QuoteAnchor 空 = 历史默认
	// 费率取 gate 的默认 maker 档(fee_provider.go:31 defaultMakerBps["gate"]=10),
	// 这样下面那条"面板边 vs 真实来回边"的落差就是决策上的真实量级,不是随手编的。
	// 注意费率在"虚高"里会整段抵消(NetBest−RoundTrip = best−s),取多少都不影响该恒等式。
	const refMid, feeBps = 0.3184, 10.0
	refBook := BookTicker{BidPx: refMid * 0.99995, AskPx: refMid * 1.00005, Ts: time.Now()}

	// 101 个样本,中位数落在 i=50:b 中位 45.2bps、s 中位 23.0bps。
	const n = 101
	for i := 0; i < n; i++ {
		b := 45.2 + float64(i-50)*0.1
		s := 23.0 + float64(i-50)*0.2
		logObserve(p.observeRow("gate", "binance", refBook, bookFromBasis(refMid, b, s), feeBps, true, time.Now()))
	}

	// 无效盘口不得落记录 —— 否则 Inf/NaN 会让 json.Marshal 失败,记录无声消失。
	logObserve(p.observeRow("gate", "binance", BookTicker{}, bookFromBasis(refMid, 45.2, 23.0), feeBps, true, time.Now()))

	var rows []ObserveRow
	for _, line := range strings.Split(buf.String(), "\n") {
		_, payload, ok := strings.Cut(line, "[mm-observe] ")
		if !ok {
			continue
		}
		var r ObserveRow
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			t.Fatalf("落盘的记录必须是合法 JSON,解析失败: %v\n原文: %s", err, payload)
		}
		rows = append(rows, r)
	}
	if len(rows) != n {
		t.Fatalf("应落 %d 条有效记录(脏盘口那条必须被丢弃),实得 %d", n, len(rows))
	}

	for i, r := range rows {
		// (2) 只用原始盘口重算,必须与记录里的派生量一致。这是"可复核"的定义本身:
		// 复核者不必信任任何派生字段,四个原始报价就足以把整行推一遍。
		refM := (r.RefBidPx + r.RefAskPx) / 2
		execM := (r.ExecBidPx + r.ExecAskPx) / 2
		approx(t, r.RefMid, refM, 1e-12, "ref 中价可由原始盘口重算")
		approx(t, r.ExecMid, execM, 1e-12, "exec 中价可由原始盘口重算")
		approx(t, r.MidDiffBps, (execM-refM)/refM*10000, 1e-6, "基差 b 可由原始盘口重算")
		approx(t, r.ExecSpreadBps, (r.ExecAskPx-r.ExecBidPx)/execM*10000, 1e-6, "价差 s 可由原始盘口重算")
		approx(t, r.BuyEdgeBps, (refM-r.ExecBidPx)/refM*10000, 1e-6, "buyEdge 可重算")
		approx(t, r.SellEdgeBps, (r.ExecAskPx-refM)/refM*10000, 1e-6, "sellEdge 可重算")

		// (3) #7 的核心不等式:单腿口径相对来回口径的虚高。
		//
		// 上一轮我报的是 虚高 = |b| − s/2 —— 本测试把它跑出来后发现那是【一阶近似】,
		// 精确式还多一个交叉项。根因是两个量的基准不同:b 以参考所中价为分母,
		// s 以执行所中价为分母。代入 execMid = refMid(1+b/1e4) 展开:
		//
		//	sellEdge = b + (s/2)(1 + b/1e4)
		//	buyEdge  = (s/2)(1 + b/1e4) − b
		//	⟹ NetBest − RoundTrip = |b| − s/2 + b·s/20000   (b 带符号)
		//
		// 交叉项在 #7 的工作点上是 45.2×23.0/20000 ≈ 0.05bps,相对 33.7bps 可忽略,
		// 所以上轮的结论不变;但"≡"改成"≈",精确式以本测试为准。
		approx(t, r.NetBestEdgeBps-r.RoundTripNetBps,
			math.Abs(r.MidDiffBps)-r.ExecSpreadBps/2+r.MidDiffBps*r.ExecSpreadBps/20000,
			1e-6, "虚高 = |b| − s/2 + b·s/20000(精确式)")
		// 交叉项必须小到不影响决策,否则一阶近似就不能再拿来讲结论了。
		if cross := math.Abs(r.MidDiffBps * r.ExecSpreadBps / 20000); cross > 0.1 {
			t.Fatalf("第 %d 条:交叉项 %.4fbps 已不可忽略,一阶近似 |b|−s/2 不再成立", i, cross)
		}

		if r.QuoteAnchor != "ref" || r.AnchorMid != r.RefMid {
			t.Fatalf("第 %d 条:默认锚应记为 ref 且锚价=参考中价,得到 anchor=%q mid=%v", i, r.QuoteAnchor, r.AnchorMid)
		}
		if r.Feed != "binance" || r.Exchange != "gate" || r.Symbol != "ONG_USDT" {
			t.Fatalf("第 %d 条:身份字段缺失 feed=%q exec=%q sym=%q", i, r.Feed, r.Exchange, r.Symbol)
		}
	}

	// (1)+(3) 汇总层:复核者拿到的就是这些记录,按中位数聚合应还原报告里的形态。
	basis := make([]float64, n)
	spread := make([]float64, n)
	for i, r := range rows {
		basis[i], spread[i] = r.MidDiffBps, r.ExecSpreadBps
	}
	medB, medS := median(basis), median(spread)
	approx(t, medB, 45.2, 1e-6, "中位基差")
	approx(t, medS, 23.0, 1e-6, "中位价差")
	if medB <= medS/2 {
		t.Fatalf("台账 #7 形态要求 |中位基差| > 半价差,得到 %.2f vs %.2f", medB, medS/2)
	}
	// #7 真正的代价:面板读数与真实来回净边的落差。中位样本上,面板(单腿口径)显示
	// 几十 bps 的"机会",真实来回净边只剩个位数 —— 一点滑点/逆向选择就翻负。
	// 这是选品口径必须换成 RoundTripNetBps 的直接理由。
	mid := rows[n/2]
	if mid.RoundTripNetBps >= mid.NetBestEdgeBps/2 {
		t.Fatalf("中位样本上面板边应远高于真实来回边,得到 net_best=%.2f round_trip=%.2f",
			mid.NetBestEdgeBps, mid.RoundTripNetBps)
	}
	t.Logf("离线复核样本(n=%d): 中位基差=%.2fbps 中位半价差=%.2fbps 虚高=%.2fbps",
		n, medB, medS/2, medB-medS/2)
	t.Logf("中位样本口径落差: 面板 net_best=%.2fbps 真实 round_trip=%.2fbps(fee=%.0fbps/腿) 倍数=%.1fx",
		mid.NetBestEdgeBps, mid.RoundTripNetBps, mid.FeeBps, mid.NetBestEdgeBps/mid.RoundTripNetBps)
	// 把原始记录打出来,便于用注释里那条 grep|jq 管道在测试输出上直接演练。
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		t.Log(line)
	}
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2] // 只用于奇数长度样本
}
