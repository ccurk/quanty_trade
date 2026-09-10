package marketmaker

import (
	"fmt"
	"time"

	"quanty_trade/internal/logger"
)

// ─────────────────────────────────────────────────────────────────────────────
// cancel_proof.go:【「撤单读失败」不等于「盘口是干净的」】
//
// 改之前 cancelAll 长这样:
//
//	orders, err := cancelPathOpenOrders(ex, symbol)
//	if err != nil { return }                                       // ← 零日志、零返回值
//	for _, o := range orders { _ = ex.CancelOrder(symbol, o.ID) }   // ← 每张的错误直接丢
//
// 它是本模块【所有】避险路径的共同动作,今天有八个调用点:
//
//	engine.go:171 优雅关闭   engine.go:225 开机清残留   engine.go:307 参考流过期
//	engine.go:321 持续偏离   engine.go:343 单日止损     engine.go:375 熔断站下
//	engine.go:395 站下补撤   engine.go:575 余额读不到(quote 内)
//
// 八个调用点全都建立在同一个假设上:"调用返回了 = 撤干净了"。而 cancelAll 从来
// 没有承诺过这件事 —— 在调用方眼里,【读挂单失败】和【盘口上本来就没有单】
// 一模一样,两条路都是"什么都没发生,然后返回"。
//
// 这和台账 #227 是同一个病:那次是「读余额失败长得像余额是 0」,这次是
// 「读挂单失败长得像没有挂单」。修法也用同一招 —— 让结论成为【数据】:
// cancelOutcome 带着"我到底看没看见盘口、看见的撤没撤掉"的凭证,
// 零值是"没证实",而不是靠调用方的善意。
// ─────────────────────────────────────────────────────────────────────────────

// cancelOutcome 是一次 cancelAll 的凭证。
//
// 字段全部不导出,且【零值 = 未证实】(read=false)。手搓的、反序列化来的、
// 将来某个新入口忘了填的 cancelOutcome,clean() 一律是 false —— 也就是"不知道",
// 而"不知道"在这里必须和"没撤干净"走同一条路。
type cancelOutcome struct {
	read   bool  // 撤单档读挂单成功 = 我们真的看见了盘口上有什么
	seen   int   // 看见几张
	failed int   // 其中撤单失败几张
	err    error // 最后一个错误(读的或撤的),只用于日志
}

// clean 是唯一的判据:看见了盘口,并且看见的每一张都撤掉了。
func (o cancelOutcome) clean() bool { return o.read && o.failed == 0 }

// why 供日志说明"这一次到底做成了什么"。
func (o cancelOutcome) why() string {
	switch {
	case !o.read:
		return fmt.Sprintf("撤单档读挂单失败,压根没看见盘口: %v", o.err)
	case o.failed > 0:
		return fmt.Sprintf("看见 %d 张,其中 %d 张没撤掉: %v", o.seen, o.failed, o.err)
	default:
		return fmt.Sprintf("已确认撤掉 %d 张", o.seen)
	}
}

// cancelDebt 是一个 pair 的【撤单欠账】:有一次避险撤单没被证实做成。
//
// 【为什么要记账,而不是当场重试完事】避险撤单的六条触发路径都是"条件成立 → 撤单
// → 本轮不报价",条件一解除就恢复报价。撤单没做成时,那个条件照样会解除
// (参考流恢复了、偏离回到阈值内了、余额读回来了),于是引擎会带着【可能还留在
// 盘口上的陈价挂单】恢复报价 —— 而它以为自己是从空盘口开始的。
// 欠账把这件事变成一个跨周期的状态:没还清,就不许恢复报价。
type cancelDebt struct {
	since  time.Time // 第一次没证实的时刻;零值 = 不欠账
	reason string    // 当初是哪条避险路径触发的
	say    sayEvery
}

func (d *cancelDebt) open() bool { return d != nil && !d.since.IsZero() }

// cancelDebtSayEvery:欠账期间重复喊话的间隔。
//
// 报价周期默认 1s,不节流的话这条 ERROR 会按秒刷屏;而容器日志只留 50m×3
// (实测约 2 天,见台账),刷屏等于把别的证据挤出取证窗口。
const cancelDebtSayEvery = 30 * time.Second

// sayEvery 是个最小的日志节流器:第一次立刻说,之后每 interval 说一次。
type sayEvery struct{ last time.Time }

func (s *sayEvery) due(now time.Time, interval time.Duration) bool {
	if s.last.IsZero() || now.Sub(s.last) >= interval {
		s.last = now
		return true
	}
	return false
}

// note 把一次 cancelAll 的凭证记进欠账,并按需喊话。返回 out.clean()。
func (d *cancelDebt) note(out cancelOutcome, ex ExecExchange, p PairConfig, reason string, now time.Time) bool {
	if d == nil {
		d = &cancelDebt{} // 没有记忆的调用方(测试/一次性入口):照样喊,只是不记账
	}
	if out.clean() {
		if d.open() {
			logger.Infof("[mm] %s@%s 撤单欠账已还清(起因:%s,欠了 %s):%s",
				p.ExecSymbol, ex.Name(), d.reason, now.Sub(d.since).Truncate(time.Second), out.why())
			*d = cancelDebt{}
		}
		return true
	}
	if !d.open() {
		d.since, d.reason = now, reason
	}
	if d.say.due(now, cancelDebtSayEvery) {
		logger.Errorf("[mm] %s@%s 避险撤单没被证实做成(起因:%s,已欠 %s):%s —— "+
			"盘口上可能还留着挂单;在证实撤干净之前这个 pair 不恢复报价",
			p.ExecSymbol, ex.Name(), d.reason, now.Sub(d.since).Truncate(time.Second), out.why())
	}
	return false
}

// hedgeCancel 是【所有】避险撤单的唯一入口:撤一次,并把"撤没撤成"记进欠账。
// 返回 true = 这一刻盘口被证实是干净的。
//
// 新增避险路径请一律走这里。cancelAll 只负责动作,不负责"有没有人相信它做成了"。
func (e *Engine) hedgeCancel(d *cancelDebt, ex ExecExchange, p PairConfig, reason string, now time.Time) bool {
	return d.note(e.cancelAll(ex, p.ExecSymbol), ex, p, reason, now)
}

// repayCancelDebt 还账:没欠就什么都不做(不发任何请求);欠着就再撤一次。
// 返回 true = 此刻没有未证实的撤单,可以报价。
func (e *Engine) repayCancelDebt(d *cancelDebt, ex ExecExchange, p PairConfig, now time.Time) bool {
	if !d.open() {
		return true
	}
	return e.hedgeCancel(d, ex, p, d.reason, now)
}
