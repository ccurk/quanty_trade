package marketmaker

import (
	"math"
	"time"
)

// 每 symbol 的基差修正(台账 #7 的修复形态)。
//
// 问题:报价中心锚在参考所(binance)中价,但执行所(gate)有自己的价格水位。
// 水位差 b 超过半价差 s/2 时,买卖两条腿会一起落到执行所盘口的同一侧 ——
// 卖单扎进买盘被秒吃、买单挂在天上永不成交,结构性只卖不买,每挂一轮亏一轮。
//
// 2026-09-09 用服务器上 467,006 条 [mm-observe] 记录实测
// (2026-09-07 05:41 → 09-08 16:47,4 个 symbol,每 symbol ~11.7 万条):
//
//	symbol        中位 b   中位|b|  中位 s   |b|>s/2 占比   p10_b    p90_b
//	ONG_USDT      +88.8     88.8     19.0      94.7%       +19.7   +145.6
//	MOVE_USDT      +2.7     19.7     21.6      62.1%       −30.6    +38.8
//	PORTAL_USDT    +0.9      4.7     17.2      27.7%        −8.1    +10.9
//	SOL_USDT        0.0      1.0      1.0      65.2%        −1.9     +1.9
//
// 关键发现:基差不是一种东西,是两种叠加,而且只有一种该修。
//   - ONG 是【持续单边】:p10 都还是 +19.7,一天半里就没翻过号 —— gate 常年比
//     binance 贵约 0.9%。这才是 #7 描述的那个结构性错边。
//   - MOVE 的中位 b 只有 +2.7,却有 62% 的样本 |b|>s/2:它是【零均值噪声】
//     (p10 −30.6 / p90 +38.8 基本对称)。这部分本来就该由参考所解释 ——
//     它正是"跨所价格发现"本身,修掉等于把优势一起扔了。
//   - SOL 的 |b| 中位 1.0bps、s 中位 1.0bps,比例上超标但绝对值就是一个 tick,
//     没有经济意义。只看比例会把它误判成故障。
//
// EWMA 恰好只吃持续分量:噪声在窗口内平均掉,水位差被跟上。
// 同一批数据上回放(半衰期 60s、上限 200bps、每条只用【当刻之前】的估计),
// |b − 修正| > s/2 的占比:
//
//	ONG 94.7% → 25.3%    MOVE 62.1% → 13.2%    PORTAL 27.7% → 14.7%    SOL 65.2% → 63.8%
//
// 为什么不直接锚执行所中价(quote_anchor:"exec",214fd25 已支持):
// 那等于把 b 整个抹掉,包括上面那份零均值的价格发现分量,退化成单所做市。
// EWMA 修正是它的连续推广 —— 半衰期→0 就是锚执行所,半衰期→∞ 就是现状锚参考所。
// 一个机制覆盖两端,代码里不用分叉。
//
// 半衰期取多少【不是这批数据选出来的】,必须说清:上面那个指标随半衰期单调下降
// (10s 时 ONG 已到 16.4%),因为半衰期→0 时它按构造恒等于 0。
// 所以它只能证明"该修",定不了档位。默认 60s 是一个取舍:比 60s 快的行情仍由
// binance 解释(保住跨所价格发现),比 60s 慢的水位差交给修正项吸收。
// 真正能定档的是 markout A/B(markout.go 已上线),那需要有成交才做得了。

// defaultBasisCapBps 是修正项绝对值的默认上限。防呆用:参考所断流、品种下架、
// 合约乘数配错时基差会跑到几百上千 bps,不设限就等于拿脏数据去平移报价中心。
// 200 是实测覆盖值 —— ONG 的 p90 是 145.6bps,取 200 留了余量又不至于放任离谱值。
const defaultBasisCapBps = 200.0

// basisEWMA 是一个 pair 的基差估计量。每个 pair 一个 goroutine(engine.go runPair),
// 所以它不带锁 —— 只在自己那条 goroutine 里读写。
type basisEWMA struct {
	halfLife time.Duration
	capBps   float64
	val      float64
	seeded   bool
	last     time.Time
}

// newBasisEWMA 返回 nil 表示【关闭】(halfLifeS<=0,即默认)。
// 返回 nil 而不是"一个恒返回 0 的对象",是为了让"没配就是老行为"这件事在调用点
// 一眼可见;下面所有方法都对 nil receiver 安全。
func newBasisEWMA(halfLifeS, capBps float64) *basisEWMA {
	if halfLifeS <= 0 {
		return nil
	}
	if capBps <= 0 {
		capBps = defaultBasisCapBps
	}
	return &basisEWMA{
		halfLife: time.Duration(halfLifeS * float64(time.Second)),
		capBps:   capBps,
	}
}

// correctionBps 返回当前该叠加到参考所中价上的修正(bps)。
// 未启用或还没喂过任何样本时返回 0 —— 即退回历史行为,而不是拿 0 当"基差为零"用。
func (b *basisEWMA) correctionBps() float64 {
	if b == nil || !b.seeded {
		return 0
	}
	return math.Max(-b.capBps, math.Min(b.capBps, b.val))
}

// update 用一条新观测推进估计。
//
// 衰减按【真实经过时间】而不是样本数:refresh_ms 是每个 pair 可配的,并且取行情
// 失败的那一轮会被整个跳过。按样本数算的话,"半衰期 60"到底等于多少秒说不清,
// 线上和离线回放也就对不上账。
func (b *basisEWMA) update(bps float64, now time.Time) {
	if b == nil || math.IsNaN(bps) || math.IsInf(bps, 0) {
		return
	}
	if !b.seeded {
		b.val, b.seeded, b.last = bps, true, now
		return
	}
	dt := now.Sub(b.last)
	if dt <= 0 {
		return // 时钟回拨或同一时刻重复喂:不推进,否则 alpha 会变负把估计推飞
	}
	b.last = now
	alpha := 1 - math.Exp(math.Log(0.5)*float64(dt)/float64(b.halfLife))
	b.val += alpha * (bps - b.val)
}

// liveDivergenceBps 是撤单风控该盯的那个偏离量:报价中心与执行所中价还差多远。
//
// 开了基差修正之后,它必须是【残差】|b − b̂| 而不是原始 |b|。否则 ONG 这种
// 常年 +88bps(p90 +145.6)的品种会一直顶在 maxLiveDivergenceBps=100 上被撤单,
// 修正项根本没机会生效 —— 修复对最该修的那个品种恰好是空转。
//
// 关掉修正时(corrBps=0,默认)本式与改动前【逐位】相同,风控行为不变
// (TestBasisDisabledByDefault 就按位相等断言,不给 1e-9 的容差)。
// 语义也没被放松:这个闸门防的是"按错价裸挂",而持续水位差修正之后就不再是错价;
// 突然的错价会让残差立刻变大,照样触发。
func liveDivergenceBps(refMid, execMid, corrBps float64) float64 {
	if refMid <= 0 {
		return 0
	}
	return math.Abs((execMid-refMid)/refMid*10000 - corrBps)
}
