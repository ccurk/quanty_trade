package marketmaker

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// 一个 ONG_USDT 的【压力工作点】,不是中位数,别当实测结论引用:基差 88.8bps、
// 价差 19.0bps(半价差 9.5bps)。72h 实测(949,534 条,见 basis.go)ONG 的中位基差是
// +37.91、p90 是 +123.63,88.8 落在这个区间的上半段 —— 选它是因为下面几条断言要的是
// "基差远大于半价差"这个工作区,取中位数反而试不出错边。
const (
	ongBasisBps  = 88.8
	ongSpreadBps = 19.0
)

// TestBasisDisabledByDefault:没配 basis_half_life_s 就必须逐位保持老行为。
// 这条比后面几条更重要 —— 这是实盘下单逻辑,默认路径不许被这次改动动到。
func TestBasisDisabledByDefault(t *testing.T) {
	var p PairConfig
	if b := newBasisEWMA(p.BasisHalfLifeS, p.BasisCapBps); b != nil {
		t.Fatalf("未配置时必须关闭(nil),得到 %+v", b)
	}
	var off *basisEWMA
	off.update(ongBasisBps, time.Now()) // nil receiver 必须安全
	if got := off.correctionBps(); got != 0 {
		t.Fatalf("关闭时修正必须恒为 0,得到 %v", got)
	}

	// 必须用 float64 变量而不是 const:无类型常量会被编译期按精确算术折叠,
	// 折出来的 88.80000000000001 和运行期的 88.80000000000052 不等,
	// 那是常量折叠的差,不是本函数的差 —— 拿它当基准会误伤。
	var refMid, execMid float64 = 100.0, 100.888
	if got := p.basisAdjustedMid(refMid, execMid, 0); got != p.anchorMid(refMid, execMid) {
		t.Fatalf("corr=0 时 basisAdjustedMid 必须与 anchorMid 逐位相同:%v vs %v", got, p.anchorMid(refMid, execMid))
	}
	// 风控闸门同理:corr=0 时必须与改动前的 |b| 表达式逐位相同。
	want := math.Abs(execMid-refMid) / refMid * 10000
	if got := liveDivergenceBps(refMid, execMid, 0); got != want {
		t.Fatalf("corr=0 时偏离量必须与原式逐位相同:%v vs %v", got, want)
	}
}

// TestBasisEWMAWarmupAndHalfLife 锁死估计量本身的两条性质:
// 预热前不修正(而不是拿 0 当"基差为零"),以及半衰期真的是半衰期。
func TestBasisEWMAWarmupAndHalfLife(t *testing.T) {
	b := newBasisEWMA(60, 0)
	if got := b.correctionBps(); got != 0 {
		t.Fatalf("喂样本之前必须不修正,得到 %v", got)
	}
	t0 := time.Unix(0, 0)
	b.update(0, t0) // 首样本直接作为种子
	if got := b.correctionBps(); got != 0 {
		t.Fatalf("种子 0 之后应为 0,得到 %v", got)
	}
	// 从 0 朝 100 走一个半衰期,应该正好走一半。
	b.update(100, t0.Add(60*time.Second))
	approx(t, b.correctionBps(), 50, 1e-9, "一个半衰期应走完一半")
	b.update(100, t0.Add(120*time.Second))
	approx(t, b.correctionBps(), 75, 1e-9, "两个半衰期应走完 3/4")

	// 时钟回拨/同刻重复喂不得推进(alpha 会变负,把估计推飞)。
	before := b.correctionBps()
	b.update(-9999, t0.Add(120*time.Second))
	b.update(-9999, t0.Add(30*time.Second))
	if got := b.correctionBps(); got != before {
		t.Fatalf("dt<=0 不得推进估计:%v → %v", before, got)
	}
	// 脏数据(NaN/Inf)不得污染估计 —— 一旦进去就再也出不来。
	b.update(math.NaN(), t0.Add(300*time.Second))
	b.update(math.Inf(1), t0.Add(600*time.Second))
	if got := b.correctionBps(); got != before {
		t.Fatalf("NaN/Inf 不得进入估计:%v → %v", before, got)
	}
}

// TestBasisCapClamps:上限是防呆闸。参考所断流/品种下架/乘数配错时基差会跑到
// 几百上千 bps,不设限就是拿脏数据去平移报价中心。
func TestBasisCapClamps(t *testing.T) {
	t0 := time.Unix(0, 0)
	b := newBasisEWMA(1, 0) // capBps=0 → 用默认 200
	b.update(5000, t0)
	if got := b.correctionBps(); got != defaultBasisCapBps {
		t.Fatalf("默认上限应为 %v,得到 %v", defaultBasisCapBps, got)
	}
	neg := newBasisEWMA(1, 50)
	neg.update(-5000, t0)
	if got := neg.correctionBps(); got != -50 {
		t.Fatalf("负向也要被钳到 -50,得到 %v", got)
	}
}

// TestBasisCorrectionFixesWrongSideQuoting 是台账 #7 的收尾断言。
//
// #7 的故障不是"基差存在",是"两条腿一起落在执行所盘口的同一侧"。所以验收标准
// 只能是报价的位置,不是任何中间统计量:修之前卖价必须整个掉进执行所买盘里
// (被秒吃),修之后必须骑在执行所盘口两侧。
func TestBasisCorrectionFixesWrongSideQuoting(t *testing.T) {
	const refMid = 0.3184
	execBook := bookFromBasis(refMid, ongBasisBps, ongSpreadBps)
	execMid := execBook.Mid()
	half := (ongSpreadBps / 2) / 10000 // 半价差 9.5bps,即引擎按 spread_bps=9.5 报价

	// 现状:锚参考所、无修正。基差 88.8bps ≫ 半价差 9.5bps,两条腿一起在错的一侧。
	p := PairConfig{ExecSymbol: "ONG_USDT"}
	bid0, ask0 := skewedQuote(p.basisAdjustedMid(refMid, execMid, 0), half, 0, inventorySkewFrac, 0)
	if ask0 >= execBook.BidPx {
		t.Fatalf("复现失败:无修正时卖价 %.8f 本应扎进执行所买盘 %.8f 之下(结构性只卖不买)", ask0, execBook.BidPx)
	}
	if bid0 >= execBook.BidPx {
		t.Fatalf("复现失败:无修正时买卖两腿应同侧,买价 %.8f 也该在买一 %.8f 之下", bid0, execBook.BidPx)
	}

	// 修复:开启基差修正,喂满一天半实测水位(88.8bps)直到收敛。
	pFix := PairConfig{ExecSymbol: "ONG_USDT", BasisHalfLifeS: 60}
	est := newBasisEWMA(pFix.BasisHalfLifeS, pFix.BasisCapBps)
	t0 := time.Unix(0, 0)
	for i := 0; i < 600; i++ { // 600 秒 = 10 个半衰期,足够收敛
		est.update(ongBasisBps, t0.Add(time.Duration(i)*time.Second))
	}
	corr := est.correctionBps()
	approx(t, corr, ongBasisBps, 0.5, "常数基差下修正项应收敛到基差本身")

	bid1, ask1 := skewedQuote(pFix.basisAdjustedMid(refMid, execMid, corr), half, 0, inventorySkewFrac, 0)
	if !(bid1 < execMid && ask1 > execMid) {
		t.Fatalf("修正后应骑在执行所中价 %.8f 两侧,得到 买 %.8f 卖 %.8f", execMid, bid1, ask1)
	}
	// 更严的一条:两条腿都要落在执行所盘口【之外】,否则 post-only 会被拒。
	if bid1 >= execBook.AskPx || ask1 <= execBook.BidPx {
		t.Fatalf("修正后两腿应在盘口 [%.8f, %.8f] 之外,得到 买 %.8f 卖 %.8f",
			execBook.BidPx, execBook.AskPx, bid1, ask1)
	}
}

// TestBasisCorrectionKeepsRiskGateUsable:ONG 的基差(中位 +88.8、p90 +145.6)高过
// maxLiveDivergenceBps=100。如果风控仍拿原始 |b| 卡门,最该修的品种会常年被撤单,
// 修正项根本没机会生效 —— 修复对它恰好是空转。所以闸门必须看残差。
func TestBasisCorrectionKeepsRiskGateUsable(t *testing.T) {
	const refMid = 100.0
	execMid := refMid * (1 + 145.6/10000) // ONG 的 p90 水位

	if got := liveDivergenceBps(refMid, execMid, 0); got <= maxLiveDivergenceBps {
		t.Fatalf("复现失败:无修正时偏离 %.2fbps 本应超过闸门 %v", got, float64(maxLiveDivergenceBps))
	}
	// 修正跟上水位之后,残差应远在闸门之内 —— 报价没错价,不该被撤。
	if got := liveDivergenceBps(refMid, execMid, 145.6); got > 1e-9 {
		t.Fatalf("修正跟上时残差应≈0,得到 %.4f", got)
	}
	// 但闸门不能被架空:修正之上再来一次突发错价,照样要触发。
	dislocated := refMid * (1 + (145.6+250)/10000)
	if got := liveDivergenceBps(refMid, dislocated, 145.6); got <= maxLiveDivergenceBps {
		t.Fatalf("突发错价仍必须触发闸门,残差只有 %.2fbps", got)
	}
}

// TestBasisEWMAAveragesNoiseButTracksLevel 锁死"只修持续分量"这个选型依据本身。
//
// 注意口径:这里锁的是【估计量本身的性质】——"零均值输入不该产生修正、有水位的
// 输入该跟住水位"。这是选 EWMA 而不是直接锚执行所的理由,赌注写成断言。
//
// 它【不】等于"线上 MOVE 就是零均值噪声、修它就安全"。72h 实测(见 basis.go)显示
// MOVE 的 β_ref 是 +0.161,即它的基差含真信号,修掉是误伤 —— 该不该对某个品种开修正,
// 看的是 lead-lag,不是这条单测。
func TestBasisEWMAAveragesNoiseButTracksLevel(t *testing.T) {
	t0 := time.Unix(0, 0)
	feed := func(samples []float64) float64 {
		b := newBasisEWMA(60, 0)
		for i, v := range samples {
			b.update(v, t0.Add(time.Duration(i)*time.Second))
		}
		return b.correctionBps()
	}
	// 零均值方波噪声 ±35bps:修正项应被压到远小于噪声幅度。
	noise := make([]float64, 1200)
	for i := range noise {
		if i%2 == 0 {
			noise[i] = 35
		} else {
			noise[i] = -35
		}
	}
	if got := math.Abs(feed(noise)); got > 5 {
		t.Fatalf("零均值噪声不该被当成基差修掉,修正项 %.2fbps 过大", got)
	}
	// 同样幅度的噪声叠在 +88.8 的水位上:修正项应跟住水位。
	level := make([]float64, 1200)
	copy(level, noise)
	for i := range level {
		level[i] += ongBasisBps
	}
	approx(t, feed(level), ongBasisBps, 5, "噪声叠在持续水位上时应跟住水位")
}

// TestExampleConfigCarriesBasisKnobs 守的是 #7 卡了三轮的那个具体毛病:
// 代码里把 basis_half_life_s 读得好好的,配置模板里却【一个字都没有】,
// 于是线上永远取默认 0 = 关闭,修复上线即空转,而且没有任何报错能提示这件事。
//
// 为什么这条必须是自动化断言而不是"注意一下":encoding/json 对未知字段是【静默忽略】的。
// 把 basis_half_life_s 拼错成 basis_halflife_s,配置照样解析成功、照样跑,
// 只是永远为 0 —— 人眼复核是抓不住这种错的,只有断言能。
func TestExampleConfigCarriesBasisKnobs(t *testing.T) {
	const path = "../../conf/marketmaker.example.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到配置模板 %s: %v", path, err)
	}
	// 先按【文本】断言键名存在。走完 json 解析再断言是不够的:键名拼错时
	// 解析依然成功、字段依然是零值,和"故意配 0"分不开。
	for _, key := range []string{"basis_half_life_s", "basis_cap_bps", "quote_anchor"} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("配置模板里找不到 %q —— 线上就是这么变成永远关闭的", key)
		}
	}

	t.Setenv("MARKETMAKER_CONFIG", path)
	cfg := LoadConfigFromEnv()
	if len(cfg.Pairs) == 0 {
		t.Fatal("模板必须能被 LoadConfigFromEnv 解析出 pairs")
	}
	// 模板发布态必须是【关闭】:开关要可见、可 grep、可改,但不能替所有者做决定。
	// 档位目前定不出来(判据随半衰期单调下降,见 basis_replay_test.go),只能等成交后 A/B。
	for _, p := range cfg.Pairs {
		if p.BasisHalfLifeS != 0 {
			t.Fatalf("%s: 模板默认必须关闭(0),得到 %v", p.ExecSymbol, p.BasisHalfLifeS)
		}
		if p.BasisCapBps != defaultBasisCapBps {
			t.Fatalf("%s: 模板的上限应显式写成 %v,得到 %v", p.ExecSymbol, defaultBasisCapBps, p.BasisCapBps)
		}
		if newBasisEWMA(p.BasisHalfLifeS, p.BasisCapBps) != nil {
			t.Fatalf("%s: 模板配置下估计量必须是 nil(关闭)", p.ExecSymbol)
		}
	}
}
