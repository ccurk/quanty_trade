package strategy

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

// —— per-symbol 连亏熔断（真实成交口径）@2026-09-19 S42 ——
//
// owner 直令：「如果一个 symbol 连续 3次亏损，直接拉黑 48h 。」
//
// 与 Majors 模板(template 1056)里那套 CB_* 熔断器的区别（重要，别把两者混为一谈）：
//   - 1056 数的是【影子单】：发出信号后自记 entry/tp/sl，用该币后续 K 线自判虚拟
//     胜负（_update_shadow），不是成交回报。模板注释自己写着「这是信号质量熔断而非
//     真实PnL熔断, 偏差已知且接受」。而且它只存在于 Majors，v40(template 1057)
//     里一个字都没有。
//   - 这里数的是 strategy_positions 里【真实已平仓】的 realized_pn_l。
//   - 这一版在 Go 侧，走 inst.Config() 原子快照 ⇒ 配置热生效、不需要重启。Python
//     侧的参数是 spawn 时从 argv 读一次的，改那个必须 stop+start；而在仓时
//     strategy_lifecycle.go:239 会拒绝 stop，等于改不动。
//
// 语义（无状态、可重启、不落库）：
//
//	某 symbol 最近连续 threshold 笔真实平仓全部亏损，且该连亏串的最后一笔平仓
//	落在 quarantine 窗口内 ⇒ 该 symbol 禁止开仓，直到「最后一笔平仓 + 窗口」到期。
//	到期后重新放行；再亏则连亏串变长、末笔时间刷新，重新隔离一个窗口；盈利则清零。
//
// 为什么不写成落库的 TTL 状态：部署/重启会把它清空（本系统一天要重启好几次），
// 而「最后一笔亏损的平仓时间」本身就是持久事实，从它直接推导隔离期更可靠，也让
// 这个功能天然跨重启存活。
//
// 闸门位置与 isBlacklistedSymbol 完全一致 —— 只挂在【开仓侧】的三个收口上
// （strategy_signal.go 信号闸 / strategy_position.go 下单口 / strategy_roi_monitor.go
// 无主仓位收养闸），没有任何一处挡平仓，所以拉黑困不住已有仓位。
const (
	lossStreakDefaultThreshold = 3
	lossStreakDefaultHours     = 48.0
	// lossStreakScanLimit 是回溯扫描的已平仓行数上限。连亏串本身很短（阈值默认 3），
	// 但行是按 close_time 全局倒序取的、多个 symbol 交错在一起，所以要留足余量让
	// 每个 symbol 都能定位到自己那几笔。
	//
	// 1600 是实测定出来的，不是拍的：2026-09-19T16:37Z 拿 Meme 实例的真实数据做过
	// 扫描上限敏感性对比 ——
	//   400  覆盖 32.1h（★ < 48h 隔离窗口，连亏串会数到窗口尽头还没找到打断的那笔）
	//   800  覆盖 79.3h
	//   1600 覆盖 83.9h ─┐ 收敛，再往上（3000）覆盖与结论都不变 ⇒ 全部历史已纳入
	//   3000 覆盖 83.9h ─┘
	// 隔离名单在四个档位上完全一致（13 个），但【连亏笔数会被少算】：400 时
	// MARSCOINUSDT 报 3（真值 5）、NEARUSDT 报 4（真值 6）—— 因为往回数到窗口尽头
	// 也没遇到打断的那笔，就停在了错误的数字上。判定不受影响，但日志里那个
	// 「连亏=N」会误导排障，所以取收敛值。
	lossStreakScanLimit = 1600
	// lossStreakCacheTTL 是一批候选里复用同一份连亏结论的时长。信号批次里每个候选
	// 都会问一次，不缓存就是每个候选一次 400 行查询。
	lossStreakCacheTTL = 20 * time.Second
)

// lossStreakBlacklistEnabled 读 config:loss_streak_blacklist_enabled。
// 键缺失时【默认开】—— 这是 owner 2026-09-19 明确要求的功能。
// 注意 getNumber 对 bool 返回 0，所以 bool 要单独走一条分支。
func lossStreakBlacklistEnabled(inst *StrategyInstance) bool {
	if inst == nil {
		return false
	}
	v, ok := inst.Config()["loss_streak_blacklist_enabled"]
	if !ok {
		return true
	}
	if b, isBool := v.(bool); isBool {
		return b
	}
	if s, isStr := v.(string); isStr {
		t := strings.ToLower(strings.TrimSpace(s))
		if t == "" {
			return true
		}
		return t != "false" && t != "0" && t != "off" && t != "no"
	}
	// 数字形式：0 视为关，其余视为开。
	if n := getNumber(v); n == 0 {
		return false
	}
	return true
}

// lossStreakThreshold 读 config:loss_streak_threshold，默认 3。
func lossStreakThreshold(inst *StrategyInstance) int {
	if inst == nil {
		return 0
	}
	if n := int(getNumber(inst.Config()["loss_streak_threshold"])); n > 0 {
		return n
	}
	return lossStreakDefaultThreshold
}

// lossStreakQuarantine 读 config:loss_streak_quarantine_hours，默认 48h。
func lossStreakQuarantine(inst *StrategyInstance) time.Duration {
	if inst == nil {
		return 0
	}
	if h := getNumber(inst.Config()["loss_streak_quarantine_hours"]); h > 0 {
		return time.Duration(h * float64(time.Hour))
	}
	return time.Duration(lossStreakDefaultHours * float64(time.Hour))
}

// pnlSourceRank 是记账来源的可信度排序（见 models.StrategyPosition.PnLSource 注释）：
// exchange_income 是交易所自己结算的，最可信；fill 是本系统自算的。
func pnlSourceRank(src string) int {
	switch src {
	case "exchange_income":
		return 2
	case "fill":
		return 1
	}
	return 0
}

// symbolLossRun 是单个 symbol 的连亏结论。
type symbolLossRun struct {
	// Streak 是最近连续亏损笔数（从最近一笔往回数）。
	Streak int
	// LastClose 是该连亏串里最近一笔的平仓时间；Streak<=0 时无意义。
	LastClose time.Time
}

// loadSymbolLossRuns 扫最近 lossStreakScanLimit 笔已平仓（全局 close_time 倒序），
// 为每个 symbol 求出「从最近一笔往回数连亏了几笔」。
func loadSymbolLossRuns(inst *StrategyInstance) map[string]symbolLossRun {
	if inst == nil || database.DB == nil {
		return nil
	}
	var rows []models.StrategyPosition
	if err := database.DB.
		Where("owner_id = ? AND strategy_id = ? AND status = ? AND closed_qty > 0",
			inst.OwnerID, inst.ID, "closed").
		Order("close_time desc, id desc").
		Limit(lossStreakScanLimit).
		Find(&rows).Error; err != nil {
		return nil
	}
	return computeSymbolLossRuns(rows)
}

// lossStreakTwinWindow 是判定"两条行是同一笔平仓的孪生记账"的平仓时间容差。
//
// 为什么需要容差、而不是一个相等的主键：孪生行（exchange_income / fill 双账路径）
// 记录的 **open_time 并不相等**。2026-09-19 实测 APT/USDT：
//
//	id=3872 open=06:28:09.435 close=06:35:25 pnl=-0.6449 src=exchange_income
//	id=3870 open=06:26:34.761 close=06:35:19 pnl=-0.6448 src=fill          ← open 差 94.7s
//
// 我第一版用 (symbol, open_time) 当去重键，**漏并**了这些对 ⇒ 同一笔亏损被记两遍。
// 后果实测（1600 行窗口、threshold=3）：
//   - 虚高：APT/USDT 报「连亏=5」真值 3；NEAR 6→5、STRK 5→4、牛来 5→3、CROSS 4→3 …
//   - 虚低：AKE/DRIFT/PONS 报 2、真值 3 —— 因为某些孪生对里 fill 那条是【正】的
//     （+0.5），没被合并进来 ⇒ 那条正的把连亏串提前打断了。**净效果是漏判 3 个 symbol**。
//
// 修法：只跨**记账来源**配对合并（同一来源的两条行绝不合并，那是两次真实平仓），
// 且平仓时间相差在容差内。孪生对的 close_time 实测只差 6 秒，120s 留足余量。
const lossStreakTwinWindow = 120 * time.Second

// computeSymbolLossRuns 是 loadSymbolLossRuns 的纯函数内核（不碰 DB），便于单测。
//
// 两个必须做的数据卫生动作，出处是台账里已实测过的坑：
//  1. pnl_source 为 ""/"unknown" 的行是【缺失值】而非 0（见 models.StrategyPosition
//     的字段注释）。把它当 0 会让一笔未知盈亏冒充"打平"，白白打断连亏串。
//  2. 孪生行（pnl_source 双账路径 exchange_income / fill）按「同 symbol + 不同来源 +
//     平仓时间邻近」配对合并，否则同一笔亏损被记两遍、连亏串翻倍、提前触发熔断。
//     合并时先按来源可信度取优（exchange_income 是交易所自己结算的，最可信；fill 是
//     本系统自算的），同一来源内再取 MAX。**不能无脑取 MAX**：一条 -1.0(exchange_income)
//     和一条 +0.5(fill) 取 MAX 会得到 +0.5，让"最有利"的那条记账掩盖一笔真实亏损。
func computeSymbolLossRuns(rows []models.StrategyPosition) map[string]symbolLossRun {
	type posClose struct {
		sym   string
		pnl   float64
		close time.Time
		rank  int
	}
	// 按 symbol 分桶，桶内顺序扫描找可配对的孪生行。行已按 close_time desc 取回，
	// 所以同一笔的两个孪生必然在桶内相邻位置附近，线性扫描足够。
	bySym := make(map[string][]int, 64)
	list := make([]posClose, 0, len(rows))
	for _, r := range rows {
		sym := exchange.NormalizeSymbol(r.Symbol)
		if sym == "" {
			continue
		}
		if r.PnLSource == "" || r.PnLSource == "unknown" {
			continue
		}
		if r.CloseTime.IsZero() {
			continue
		}
		rank := pnlSourceRank(r.PnLSource)
		merged := false
		for _, i := range bySym[sym] {
			// 只跨来源配对：同来源的两条行是两次真实平仓，绝不合并。
			if list[i].rank == rank {
				continue
			}
			d := list[i].close.Sub(r.CloseTime)
			if d < 0 {
				d = -d
			}
			if d > lossStreakTwinWindow {
				continue
			}
			switch {
			case rank > list[i].rank:
				list[i].pnl, list[i].rank = r.RealizedPnL, rank
			case rank == list[i].rank && r.RealizedPnL > list[i].pnl:
				list[i].pnl = r.RealizedPnL
			}
			if r.CloseTime.After(list[i].close) {
				list[i].close = r.CloseTime
			}
			merged = true
			break
		}
		if merged {
			continue
		}
		bySym[sym] = append(bySym[sym], len(list))
		list = append(list, posClose{sym: sym, pnl: r.RealizedPnL, close: r.CloseTime, rank: rank})
	}

	sort.Slice(list, func(i, j int) bool { return list[i].close.After(list[j].close) })

	// closed 标记「该 symbol 的连亏串已经定案」——即往回数时已经遇到过一个非亏损。
	// 必须用独立的 map，不能拿 runs 里有没有键当"已定案"：那样第一笔之后每一笔都会
	// 被当成已定案而跳过，连亏串永远停在 1、熔断器一辈子不触发。
	runs := make(map[string]symbolLossRun, 16)
	closed := make(map[string]bool, 16)
	for _, p := range list {
		if closed[p.sym] {
			continue
		}
		run := runs[p.sym]
		if p.pnl < 0 {
			run.Streak++
			if run.LastClose.IsZero() {
				run.LastClose = p.close
			}
			runs[p.sym] = run
			continue
		}
		// pnl >= 0 打断连亏串。打平(0)算"没亏"—— 只有严格为负才计一笔亏损。
		// 只标记定案、**不要**把已累计的连亏串清掉：清掉会让"亏亏亏盈亏亏"这种
		// 最近两笔连亏的情况被后面的盈利行覆盖成 0。
		closed[p.sym] = true
	}
	return runs
}

type lossRunCacheEntry struct {
	runs   map[string]symbolLossRun
	loaded time.Time
}

var (
	lossRunMu    sync.Mutex
	lossRunCache = map[string]lossRunCacheEntry{}
)

func cachedSymbolLossRuns(inst *StrategyInstance) map[string]symbolLossRun {
	lossRunMu.Lock()
	e, ok := lossRunCache[inst.ID]
	lossRunMu.Unlock()
	if ok && time.Since(e.loaded) < lossStreakCacheTTL {
		return e.runs
	}
	runs := loadSymbolLossRuns(inst)
	if runs == nil {
		// 查询失败时缓存一个空表，避免每个候选都重打一次失败的查询。
		runs = map[string]symbolLossRun{}
	}
	lossRunMu.Lock()
	lossRunCache[inst.ID] = lossRunCacheEntry{runs: runs, loaded: time.Now()}
	lossRunMu.Unlock()
	return runs
}

// symbolLossStreakBan 返回该 symbol 是否因连亏处于隔离中、隔离截止时间、连亏笔数。
// 与 isBlacklistedSymbol 一样只读 inst.Config() 内存快照 ⇒ 配置改动下一次调用即生效。
func symbolLossStreakBan(inst *StrategyInstance, symbol string) (bool, time.Time, int) {
	if inst == nil || !lossStreakBlacklistEnabled(inst) {
		return false, time.Time{}, 0
	}
	threshold := lossStreakThreshold(inst)
	if threshold <= 0 {
		return false, time.Time{}, 0
	}
	sym := exchange.NormalizeSymbol(symbol)
	if sym == "" {
		return false, time.Time{}, 0
	}
	run, ok := cachedSymbolLossRuns(inst)[sym]
	if !ok {
		return false, time.Time{}, 0
	}
	until, banned := lossStreakBanUntil(run, threshold, lossStreakQuarantine(inst), time.Now())
	if !banned {
		return false, time.Time{}, 0
	}
	return true, until, run.Streak
}

// lossStreakBanUntil 是隔离判定的纯函数内核（不碰 DB、不读时钟），便于单测。
// 返回 (截止时间, 是否在隔离中)。
func lossStreakBanUntil(run symbolLossRun, threshold int, window time.Duration, now time.Time) (time.Time, bool) {
	if threshold <= 0 || window <= 0 || run.Streak < threshold || run.LastClose.IsZero() {
		return time.Time{}, false
	}
	until := run.LastClose.Add(window)
	if !now.Before(until) {
		// 隔离期已过，重新放行。若再亏，连亏串会更长、末笔时间刷新，自动再隔离一个窗口。
		return time.Time{}, false
	}
	return until, true
}

// lossStreakBanReason 给日志用的一行说明（监控按这行 grep 熔断拦截）。
func lossStreakBanReason(streak int, until time.Time) string {
	return fmt.Sprintf("连亏=%d 隔离至=%s", streak, until.UTC().Format(time.RFC3339))
}
