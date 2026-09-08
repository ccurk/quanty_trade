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
// 2026-09-08 用服务器上 949,534 条 [mm-observe] 记录实测,窗口
// 2026-09-05 17:56 → 09-08 17:55 UTC(~72h,4 个 symbol,每 symbol ~23.7 万条)。
// 复核方式见 basis_replay_test.go —— 那是可重跑的,不是一次性算完写进注释的数。
//
//	symbol        中位 b   中位|b|  中位 s   |b|>s/2 占比   p10_b    p90_b
//	ONG_USDT      +37.91   38.03    14.80      88.5%       +4.21   +123.63
//	MOVE_USDT      +1.66   20.22    21.10      66.9%      −30.05    +38.25
//	PORTAL_USDT    +4.41    6.54    13.10      47.4%       −6.26    +16.80
//	SOL_USDT        0.00    0.96     1.00      67.5%       −1.89     +1.93
//
// 这批数是【纯净的】:线上跑的镜像 d933d91 早于本文件,corrBps 恒为 0,
// 所以观测里不含修正自身的反馈。回放因此是干净的对照。
//
// 【一】基差不是一个常数水位 —— 之前那版注释在这点上是错的,拿证据纠正:
// 同一批数据上,"用整段窗口的中位数做一次固定水平位移"(非因果,即水平位移的上界)
// 对 |残差|>s/2 占比的效果是:
//
//	ONG 88.5% → 89.9%(更差)   MOVE 66.9% → 68.0%(更差)
//	PORTAL 47.4% → 44.2%       SOL 67.5% → 67.5%(不变)
//
// 四个品种没有一个能靠固定位移改善。ONG 的 b 从 p10 +4.21 摆到 p90 +123.63,
// 号是稳的(常年 gate 贵)但量级差 30 倍。所以"gate 比 binance 贵 0.9% 的固定水位"
// 这个说法不成立;能改善的只有【跟踪型】估计,而跟踪就有吃掉行情的风险。
//
// 【二】那到底哪个品种修了安全?这要看基差出现之后是谁向谁收敛(lead-lag,
// TestBasisLeadLagOnObserveLog)。β_exec = 执行所朝参考所收敛的比例(负=会回来),
// β_ref = 参考所朝执行所收敛的比例(正=参考所在追,即执行所领先):
//
//	symbol        β_exec(60s)  β_ref(60s)   读法
//	ONG_USDT        −0.013      +0.010      两边都不动 → 基差不含信息,修掉不损失什么
//	MOVE_USDT       −0.038      +0.161      参考所在追执行所 → 含真信号,修掉是【误伤】
//	PORTAL_USDT     −0.358      +0.039      执行所会自己回来 → 修掉基本安全
//	SOL_USDT        −0.956      −0.006      执行所几乎全额回归 → 安全,但只有 1 个 tick,无意义
//
// 结论跟"零均值 vs 单边"那套分法不一样:该不该修不看 b 的均值,看 b 有没有预测力。
// ONG 是唯一"基差又大又不含信息"的品种 —— 它才是 #7 该修的那个,而且只有它。
// MOVE 的 β_ref +0.161 是反例:它的 |残差|>s/2 占比也能从 66.9% 降到 12.0%(半衰期 60s),
// 但那个"改善"正是在吃 binance 尚未反映的信息,把它当疗效就修反了。
//
// 为什么不直接锚执行所中价(quote_anchor:"exec",214fd25 已支持):
// 那等于把 b 整个抹掉,连 MOVE 那份有预测力的分量一起扔掉,退化成单所做市。
// EWMA 修正是它的连续推广 —— 半衰期→0 逼近锚执行所,半衰期→∞ 就是现状锚参考所。
//
// 【三】半衰期取多少,这批数据仍然定不出来,如实说:
// |残差|>s/2 占比随半衰期【单调下降】,一路测到 1s 都没有极小值
// (ONG:1s 13.2% / 10s 20.5% / 60s 29.1% / 300s 43.1% / 1800s 60.2%)。
// 单调就意味着"按这个判据最优 = 半衰期取到最短 = 放弃参考所",判据自己塌了。
// 所以它只能证明"该修",定不了档位 —— defaultBasisCapBps 之外不设默认半衰期,
// 配置模板(conf/marketmaker.example.json)里显式写 0 = 关闭。
// lead-lag 只能给出一个【下界】:ONG 在 60s 尺度上信息量已≈0,取 60 不会吃掉信息。
// 真正能定档的是 markout A/B(markout.go 已上线),那需要有成交才做得了。

// defaultBasisCapBps 是修正项绝对值的默认上限。防呆用:参考所断流、品种下架、
// 合约乘数配错时基差会跑到几百上千 bps,不设限就等于拿脏数据去平移报价中心。
// 200 是实测覆盖值 —— 72h 窗口里最脏的 ONG p90 也只有 123.6bps,取 200 留了余量
// 又不至于放任离谱值。
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
