package strategy

// 「仓位损失」(position failure) —— 不是一个新指标，是对「什么算失败」的一次修订。
//
//	旧口径：realized_pn_l > 0 即算赢，于是一笔 +0.30U 的平仓算赢。
//	新口径：realized_pn_l <= FailurePnLThresholdUSDT 即算失败，**即使它没亏钱**。
//
// 为什么需要它：手续费按腿收，一笔毛额 +0.30U 的平仓净额是亏的（往返约 10bps）。
// 旧口径把这种单记成赢，胜率回答的其实是「方向对不对」，而不是「这笔赚回自己了没」。
// 两者恰好在系统流血的地方分叉 —— 一个持续「小赚大亏」的配置，旧口径能读成「胜率过半」。
//
// 为什么是【独立计数器】而不是并进 Losses：Losses 参与 gross_loss、盈亏比、破损胜率
// 这些**金额**口径，把 +0.30U 塞进 gross_loss 会让「亏损总额」变成假的。所以
// wins/losses/gross_profit/gross_loss 全部保持符号口径不变，FailureX 另立一列。
//
// 为什么可配：这是**绝对** USDT 线，必须跟着仓位尺寸走。本系统的尺寸不是常数 ——
// 2026-09-21 乘基由可用余额改成权益（commit ee9a391），单仓保证金一天之内 +74%。
// 线不跟着动，同一个 0.5 在大仓上是「没赚头」、在小仓上就成了「大赚」。
const FailurePnLThresholdUSDT = 0.5

// FailureThresholdUSDT 取该策略的覆盖值（config 键 failure_pnl_threshold_usdt），
// 缺键时回落包默认值。
//
// 「缺键」与「显式填 0」必须区分开：填 0 是合法配置（失败 = 不赚钱即 pnl<=0），
// 不能与「没配」混为一谈 —— 只有后者才用默认值。
func FailureThresholdUSDT(cfg map[string]interface{}) float64 {
	if cfg != nil {
		if raw, ok := cfg["failure_pnl_threshold_usdt"]; ok && raw != nil {
			return getNumber(raw)
		}
	}
	return FailurePnLThresholdUSDT
}

// IsPositionFailure 是「仓位损失」的唯一判据。集中放在这里，是为了让它全仓库可 grep、
// 且只有一个符号方向（<=，含端点）—— 这条线此前散成三份各写一次的 pnl>0，已经漂移过。
func IsPositionFailure(realizedPnL, threshold float64) bool {
	return realizedPnL <= threshold
}
