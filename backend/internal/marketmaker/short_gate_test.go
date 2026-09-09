package marketmaker

import (
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

// short_gate_test.go 锁死"双边挂单"这一步的两件事:
//
//	① 闸关着(allow_short 未配)时,报价行为与改动前【逐位相同】—— 包括在一个
//	   完全有能力做空的永续场馆上,也照旧只能卖已持有的量;
//	② 闸开着但永续风控四件套缺失时,Start 直接【拒绝启动并报错】,而不是静默
//	   退回单边把人骗进"我以为双边在跑"。
//
// 顺带把 2026-09-08 评估 §3.1 里那条【推出来的、标注了"未实盘验证"】的结论
// 变成实测:空仓的永续账户挂不出卖单。见 TestShortGateClosedKeepsSpotBehavior
// 的 perp-flat 用例 —— 它就是那条推导的可执行形式。

// ---- 假交易所:只记账,不联网 ----

type placedOrder struct {
	Side     string
	Price    float64
	Qty      float64
	TIF      string
	PostOnly bool
}

// fakeExec 是一个可编排的 ExecExchange。short 决定 SupportsShort(),
// 它【不】实现 PerpRiskControls —— 代表"永续适配器写好了但风控没补"的状态。
type fakeExec struct {
	mu        sync.Mutex
	name      string
	short     bool
	bal       map[string]float64
	open      []OpenOrder
	filt      SymbolFilter
	book      BookTicker
	placed    []placedOrder
	cancelled []string
	nextID    int
}

func (f *fakeExec) Name() string        { return f.name }
func (f *fakeExec) SupportsShort() bool { return f.short }

func (f *fakeExec) FetchBookTicker(string) (BookTicker, error) { return f.book, nil }
func (f *fakeExec) SymbolFilter(string) (SymbolFilter, error)  { return f.filt, nil }

func (f *fakeExec) Balances() (map[string]float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]float64{}
	for k, v := range f.bal {
		out[k] = v
	}
	return out, nil
}

func (f *fakeExec) OpenOrders(string) ([]OpenOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]OpenOrder(nil), f.open...), nil
}

func (f *fakeExec) CancelOrder(_, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	for i := range f.open {
		if f.open[i].ID == id {
			f.open = append(f.open[:i], f.open[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeExec) PlaceLimit(_, side string, price, qty float64, tif string, postOnly bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placed = append(f.placed, placedOrder{Side: side, Price: price, Qty: qty, TIF: tif, PostOnly: postOnly})
	f.nextID++
	return "fake-" + strings.Repeat("x", f.nextID), nil
}

func (f *fakeExec) snapshot() []placedOrder {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]placedOrder(nil), f.placed...)
}

// fakeRiskyExec 是"永续风控四件套已经补齐"的适配器 —— 唯一的区别就是它实现了
// PerpRiskControls。方法体是空壳:闸门是类型断言,挡的是【忘了做】,不是【假装做了】。
type fakeRiskyExec struct{ *fakeExec }

func (f *fakeRiskyExec) ClosePosition(string) error             { return nil }
func (f *fakeRiskyExec) MarginRatio(string) (float64, error)    { return 1, nil }
func (f *fakeRiskyExec) SetLeverage(string, float64) error      { return nil }
func (f *fakeRiskyExec) FundingPaidUSD(string) (float64, error) { return 0, nil }

// newSafePerp 造一个"支持做空 + 四件套齐全"的场馆。
func newSafePerp(baseHeld float64) *fakeRiskyExec {
	return &fakeRiskyExec{fakeExec: newFake(fakeSafeName, true, baseHeld)}
}

// 编译期断言:四件套齐了才算 PerpRiskControls,缺一个都过不了这一行。
var _ PerpRiskControls = (*fakeRiskyExec)(nil)

type fakeFeed struct{}

func (fakeFeed) Name() string { return "fake-feed" }
func (fakeFeed) SubscribeBookTicker(string, func(BookTicker)) (func(), error) {
	return func() {}, nil // 永不推送 → runPair 里 ref.Mid()<=0,一轮都不报价
}
func (fakeFeed) FetchBookTicker(string) (BookTicker, error) { return BookTicker{}, nil }

const (
	fakeSpotName = "fake-spot"
	fakePerpName = "fake-perp"      // SupportsShort()=true,但没有 PerpRiskControls
	fakeSafeName = "fake-perp-safe" // SupportsShort()=true 且四件套齐
	fakeFeedName = "fake-feed"
	fakeExecSym  = "SOL_USDT"
	fakeFeedSym  = "SOLUSDT"
)

// 每个 registry 名字挂一个可被测试改写的实例,这样 Start 拿到的就是测试手里那一个。
var fakeExecs = map[string]*fakeExec{}

func init() {
	RegisterFeed(fakeFeedName, func() (FeedSource, error) { return fakeFeed{}, nil })
	for _, n := range []string{fakeSpotName, fakePerpName} {
		name := n
		RegisterExec(name, func(ExecConfig) (ExecExchange, error) { return fakeExecs[name], nil })
	}
}

// newFake 造一个盘口/精度都确定的场馆。exec 盘口 99.98/100.02,参考中价 100.00。
func newFake(name string, short bool, baseHeld float64) *fakeExec {
	return &fakeExec{
		name:  name,
		short: short,
		bal:   map[string]float64{"SOL": baseHeld, "USDT": 100000},
		filt:  SymbolFilter{BaseAsset: "SOL", QuoteAsset: "USDT", TickSize: 0.01, StepSize: 0.001, MinNotional: 5},
		book:  BookTicker{Symbol: fakeExecSym, BidPx: 99.98, BidQty: 50, AskPx: 100.02, AskQty: 50, Ts: time.Now()},
	}
}

// testPair:半价差 20bps、单量 1、上限 10。所有期望值都能手算,见各用例注释。
func testPair(allowShort bool) PairConfig {
	return PairConfig{
		FeedSymbol: fakeFeedSym, Exec: "x", ExecSymbol: fakeExecSym,
		SpreadBps: 20, OrderQty: 1, MaxPosition: 10, AllowShort: allowShort,
	}
}

func refBook() BookTicker {
	return BookTicker{Symbol: fakeFeedSym, BidPx: 99.99, AskPx: 100.01, Ts: time.Now()}
}

// sides 把下过的单按方向摊平成 "BUY@99.80x1.000" 便于整体比对。
func sides(orders []placedOrder) map[string]placedOrder {
	out := map[string]placedOrder{}
	for _, o := range orders {
		out[o.Side] = o
	}
	return out
}

func wantOrder(t *testing.T, got map[string]placedOrder, side string, px, qty float64) {
	t.Helper()
	o, ok := got[side]
	if !ok {
		t.Fatalf("应挂 %s 单,实际没挂(共 %d 张)", side, len(got))
	}
	if math.Abs(o.Price-px) > 1e-6 || math.Abs(o.Qty-qty) > 1e-6 {
		t.Fatalf("%s 单应为 %.4f x %.4f,实得 %.4f x %.4f", side, px, qty, o.Price, o.Qty)
	}
	if o.TIF != "GTC" || !o.PostOnly {
		t.Fatalf("%s 单必须是 post-only GTC,实得 tif=%s postOnly=%v", side, o.TIF, o.PostOnly)
	}
}

func mustNoSide(t *testing.T, got map[string]placedOrder, side, why string) {
	t.Helper()
	if o, ok := got[side]; ok {
		t.Fatalf("%s:不该挂 %s 单,却挂了 %.4f x %.4f", why, side, o.Price, o.Qty)
	}
}

// ---- ① 闸关着:行为与改动前逐位相同 ----

func TestShortGateClosedKeepsSpotBehavior(t *testing.T) {
	e := &Engine{}

	// (a) 现货空仓:只挂买单。这是改动前的行为,也是改动后的行为。
	// 手算:invRatio=0 → center=100 → bid=100*(1-0.002)=99.80、ask=100.20;
	// rideToBook 不动(exec 卖一 100.02 比 100.20 便宜、买一 99.98 比 99.80 贵);
	// askQty=min(1, baseHeld=0)=0 → wantAsk=false。
	spotFlat := newFake(fakeSpotName, false, 0)
	e.quote(testPair(false), spotFlat, refBook(), spotFlat.book, 0, nil)
	got := sides(spotFlat.snapshot())
	wantOrder(t, got, "BUY", 99.80, 1)
	mustNoSide(t, got, "SELL", "现货空仓")

	// (b) 现货有库存 5:双边都挂 —— "只能挂买单"从来不是无条件成立的。
	// 老徐 2026-09-09 用 66,083 张真实挂单量到的每个 symbol 都有卖距(invRatio 0.20~0.94),
	// 就是这条路径。手算:invRatio=0.5 → center=100*(1-0.002*0.5)=99.9 →
	// bid=floor(99.7002)=99.70、ask=ceil(100.0998)=100.10;askQty=min(1,5)=1。
	spotHeld := newFake(fakeSpotName, false, 5)
	e.quote(testPair(false), spotHeld, refBook(), spotHeld.book, 0, nil)
	got = sides(spotHeld.snapshot())
	wantOrder(t, got, "BUY", 99.70, 1)
	wantOrder(t, got, "SELL", 100.10, 1)

	// (c) 关键用例:场馆【完全有能力】做空(SupportsShort=true)且风控四件套齐全,
	// 但 allow_short 没开 → 行为必须和 (a) 一模一样。闸关着 = 什么都没变。
	perpFlat := newSafePerp(0)
	e.quote(testPair(false), perpFlat, refBook(), perpFlat.book, 0, nil)
	got = sides(perpFlat.snapshot())
	wantOrder(t, got, "BUY", 99.80, 1)
	mustNoSide(t, got, "SELL", "闸关着的永续空仓")

	// (d) 把 2026-09-08 评估 §3.1 那条【推出来、标注未验证】的结论测出来:
	// 永续持空仓(baseHeld=-5)时 askQty 为负 → 仍然只挂买单,而且 invRatio 被
	// 钳回 0 → 报价与空仓时逐位相同(空头方向的库存偏移完全失效)。
	perpShort := newSafePerp(-5)
	e.quote(testPair(false), perpShort, refBook(), perpShort.book, 0, nil)
	got = sides(perpShort.snapshot())
	wantOrder(t, got, "BUY", 99.80, 1) // 与 (a)(c) 同价 = 负持仓被当成 0
	mustNoSide(t, got, "SELL", "闸关着的永续空头仓")
}

// TestShortGateClosedStillLocksInventoryFromRestingAsk 守住现货那条防 thrash 的口径:
// 挂在卖单里的量必须算回持仓,否则"挂卖→可用余额变少→卖量算小→撤挂重下"每秒死循环。
// 闸关着时这一项必须原样保留。
func TestShortGateClosedStillLocksInventoryFromRestingAsk(t *testing.T) {
	ex := newFake(fakeSpotName, false, 3)
	ex.open = []OpenOrder{{ID: "old-ask", Side: "SELL", Price: 100.10, Qty: 1}}
	(&Engine{}).quote(testPair(false), ex, refBook(), ex.book, 0, nil)

	// baseHeld = 可用 3 + 卖单锁着的 1 = 4 → invRatio=0.4 → center=99.92 →
	// bid=floor(99.72016)=99.72。若漏加那 1,baseHeld=3 → invRatio=0.3 → 买价会是 99.74。
	got := sides(ex.snapshot())
	wantOrder(t, got, "BUY", 99.72, 1)
	// 目标卖价 ceil(100.11984)=100.12,与现有挂单 100.10 相差 0.02,在重挂带宽
	// (max(tick, refMid·half·0.25)=0.05)之内且量相同 → 不该撤挂重下。
	if len(ex.cancelled) != 0 {
		t.Fatalf("卖单在重挂带宽内且量相同,不该撤单重挂,实际撤了 %v", ex.cancelled)
	}
}

// ---- ② 闸开着但风控缺失:拒绝启动,而不是裸奔 ----

func TestShortGateOpenWithoutRiskControlsRefusesToStart(t *testing.T) {
	fakeExecs[fakePerpName] = newFake(fakePerpName, true, 0) // 永续,但没有 PerpRiskControls
	cfg := Config{
		Enabled: true, ObserveOnly: false, Feed: fakeFeedName,
		Exec:  []ExecConfig{{Name: fakePerpName}},
		Pairs: []PairConfig{func() PairConfig { p := testPair(true); p.Exec = fakePerpName; return p }()},
	}

	eng, err := Start(cfg)
	if err == nil {
		if eng != nil {
			eng.Stop()
		}
		t.Fatal("风控四件套缺失时 Start 必须报错拒绝启动,却起来了")
	}
	if eng != nil {
		t.Fatalf("拒绝启动时不得返回 Engine,实得 %v", eng)
	}
	// 报错必须指名缺的是什么,否则运维只会看到"起不来"而不知道该补什么。
	if !strings.Contains(err.Error(), "PerpRiskControls") || !strings.Contains(err.Error(), "allow_short") {
		t.Fatalf("错误信息应点名 allow_short 与 PerpRiskControls,实得: %v", err)
	}

	// 拒绝 = 什么都没发生:没起协程、没下单、running 仍为 false。
	if orders := fakeExecs[fakePerpName].snapshot(); len(orders) != 0 {
		t.Fatalf("拒绝启动时不得下任何单,实得 %v", orders)
	}
	if _, running := ObserveSnapshot(); running {
		t.Fatal("拒绝启动后 running 必须仍为 false")
	}

	// 就算有人绕过 Start 直接构造 Engine,quote 里的第二道闸也必须拦住:
	// 卖侧退回现货规则 → 空仓挂不出卖单。
	p := testPair(true)
	ex := fakeExecs[fakePerpName]
	if shortSideEnabled(p, ex) {
		t.Fatal("风控缺失时 shortSideEnabled 必须为 false")
	}
	(&Engine{}).quote(p, ex, refBook(), ex.book, 0, nil)
	mustNoSide(t, sides(ex.snapshot()), "SELL", "风控缺失、绕过 Start")
}

// TestShortGateOpenOnSpotRefusesToStart:现货场馆配 allow_short 同样拒绝启动 ——
// 这里缺的是场馆能力,不是风控。
func TestShortGateOpenOnSpotRefusesToStart(t *testing.T) {
	fakeExecs[fakeSpotName] = newFake(fakeSpotName, false, 0)
	cfg := Config{
		Enabled: true, Feed: fakeFeedName,
		Exec:  []ExecConfig{{Name: fakeSpotName}},
		Pairs: []PairConfig{func() PairConfig { p := testPair(true); p.Exec = fakeSpotName; return p }()},
	}
	eng, err := Start(cfg)
	if eng != nil {
		eng.Stop()
	}
	if err == nil {
		t.Fatal("现货场馆配 allow_short 必须拒绝启动")
	}
	if !strings.Contains(err.Error(), "SupportsShort") {
		t.Fatalf("错误信息应点名 SupportsShort()=false,实得: %v", err)
	}
}

// TestShortSideBlockersEnumeratesBothGates 锁死闸门的两条判据都在,且都能被单独触发。
func TestShortSideBlockersEnumeratesBothGates(t *testing.T) {
	spot := newFake(fakeSpotName, false, 0) // 既不支持做空,也没风控 → 两条都缺
	perp := newFake(fakePerpName, true, 0)  // 支持做空,缺风控 → 一条
	safe := newSafePerp(0)                  // 都齐 → 零

	if miss := shortSideBlockers(spot); len(miss) != 2 {
		t.Fatalf("现货应同时缺两条,实得 %v", miss)
	}
	if miss := shortSideBlockers(perp); len(miss) != 1 || !strings.Contains(miss[0], "PerpRiskControls") {
		t.Fatalf("永续无风控应只缺 PerpRiskControls,实得 %v", miss)
	}
	if miss := shortSideBlockers(safe); len(miss) != 0 {
		t.Fatalf("四件套齐全时不该有阻塞项,实得 %v", miss)
	}
	// allow_short 没开时,即使条件全满足也不放开 —— 开关本身是第三道。
	if shortSideEnabled(testPair(false), safe) {
		t.Fatal("allow_short=false 时不得放开卖侧")
	}
	if !shortSideEnabled(testPair(true), safe) {
		t.Fatal("三条都满足时应放开卖侧,否则闸永远打不开")
	}
}

// ---- 闸开且条件满足:这才是本轮要交付的能力 ----

func TestShortGateOpenQuotesBothSides(t *testing.T) {
	e := &Engine{}

	// (a) 空仓的永续:双边都挂。这正是 (c) 用例挂不出来的那张卖单。
	// 手算与现货空仓同价(invRatio=0),差别只在 askCap:0 → MaxPosition+0=10。
	flat := newSafePerp(0)
	e.quote(testPair(true), flat, refBook(), flat.book, 0, nil)
	got := sides(flat.snapshot())
	wantOrder(t, got, "BUY", 99.80, 1)
	wantOrder(t, got, "SELL", 100.20, 1)

	// (b) 持空仓 -5:库存偏移必须朝【相反】方向拉 —— 买价上抬(早点买回来)、
	// 卖价推远(少继续做空)。手算:invRatio=-0.5 → center=100*(1+0.001)=100.1 →
	// bid=floor(99.8998)=99.89、ask=ceil(100.3002)=100.31。
	short := newSafePerp(-5)
	e.quote(testPair(true), short, refBook(), short.book, 0, nil)
	got = sides(short.snapshot())
	wantOrder(t, got, "BUY", 99.89, 1)
	wantOrder(t, got, "SELL", 100.31, 1)

	// (c) 空到上限 -10:卖侧关闭(不再加空),买侧仍开(往回买)。这就是 −MaxPosition
	// 那道对称的库存闸。
	capped := newSafePerp(-10)
	e.quote(testPair(true), capped, refBook(), capped.book, 0, nil)
	got = sides(capped.snapshot())
	mustNoSide(t, got, "SELL", "已空到 −MaxPosition")
	if _, ok := got["BUY"]; !ok {
		t.Fatal("空到上限时必须还能买回来,否则仓位出不去")
	}

	// (d) 永续上挂着的卖单【不得】再加回持仓:Balances() 返回的是清算所持仓,
	// 挂单不从里面扣。加了会把 -5 当成 -4,报价整体偏一侧。
	// 把已有卖单放在一个明显偏离的价上,逼它撤挂重下,再看新价是 -5 的还是 -4 的。
	withAsk := newSafePerp(-5)
	withAsk.open = []OpenOrder{{ID: "stale", Side: "SELL", Price: 105, Qty: 1}}
	e.quote(testPair(true), withAsk, refBook(), withAsk.book, 0, nil)
	got = sides(withAsk.snapshot())
	wantOrder(t, got, "SELL", 100.31, 1) // -5 的价;若误加成 -4 会是 100.29
	wantOrder(t, got, "BUY", 99.89, 1)   // 同理,-4 会是 99.87
}

// TestSkewedQuoteNegativeInventory 单独锁住钳位下界的放宽,并确认上界没被顺手改坏
// (engine_test.go TestSkewedQuote 已经锁了上界,这里只补负半轴)。
func TestSkewedQuoteNegativeInventory(t *testing.T) {
	const refMid, half = 100.0, 0.001

	bid0, ask0 := skewedQuote(refMid, half, 0, 1.0, 0)
	bidS, askS := skewedQuote(refMid, half, -0.5, 1.0, 0)
	if bidS <= bid0 || askS <= ask0 {
		t.Fatalf("持空仓应把两条腿一起上移(买回来),实得 bid %v->%v ask %v->%v", bid0, bidS, ask0, askS)
	}
	// 对称性:−r 与 +r 相对中价应是镜像。
	bidL, askL := skewedQuote(refMid, half, 0.5, 1.0, 0)
	if math.Abs((bidS-bid0)-(bid0-bidL)) > 1e-9 || math.Abs((askS-ask0)-(ask0-askL)) > 1e-9 {
		t.Fatalf("多空两侧的库存偏移应镜像对称: -0.5 得 %v/%v,+0.5 得 %v/%v", bidS, askS, bidL, askL)
	}
	// 下界钳到 −1:再空也不会把中枢推得更远。
	bidC, askC := skewedQuote(refMid, half, -1, 1.0, 0)
	bidX, askX := skewedQuote(refMid, half, -7, 1.0, 0)
	if math.Abs(bidC-bidX) > 1e-9 || math.Abs(askC-askX) > 1e-9 {
		t.Fatalf("invRatio<-1 必须钳到 -1,实得 %v/%v vs %v/%v", bidX, askX, bidC, askC)
	}
}
