package strategy

import (
	"testing"
	"time"

	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

var lossT0 = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func lp(sym string, openOff, closeOff time.Duration, pnl float64, src string) models.StrategyPosition {
	return models.StrategyPosition{
		Symbol:      sym,
		OpenTime:    lossT0.Add(openOff),
		CloseTime:   lossT0.Add(closeOff),
		RealizedPnL: pnl,
		PnLSource:   src,
	}
}

func runOf(t *testing.T, rows []models.StrategyPosition, sym string) symbolLossRun {
	t.Helper()
	runs := computeSymbolLossRuns(rows)
	run, ok := runs[exchange.NormalizeSymbol(sym)]
	if !ok {
		t.Fatalf("no run recorded for %s (runs=%v)", sym, runs)
	}
	return run
}

// 规则本体：最近连续 3 笔全亏 → streak=3，LastClose 取最近那一笔的平仓时间。
func TestComputeSymbolLossRuns_ThreeConsecutiveLosses(t *testing.T) {
	rows := []models.StrategyPosition{
		lp("BTC/USDT", 0, 10*time.Minute, +1.0, "exchange_income"), // 更早的一笔盈利
		lp("BTC/USDT", 1, 40*time.Minute, -0.5, "exchange_income"),
		lp("BTC/USDT", 2, 50*time.Minute, -0.7, "exchange_income"),
		lp("BTC/USDT", 3, 60*time.Minute, -0.9, "exchange_income"),
	}
	run := runOf(t, rows, "BTC/USDT")
	if run.Streak != 3 {
		t.Fatalf("streak = %d, want 3", run.Streak)
	}
	if want := lossT0.Add(60 * time.Minute); !run.LastClose.Equal(want) {
		t.Fatalf("LastClose = %v, want %v", run.LastClose, want)
	}
}

// 一笔盈利打断连亏串：只数最近那一段。
func TestComputeSymbolLossRuns_BrokenByWin(t *testing.T) {
	rows := []models.StrategyPosition{
		lp("ETH/USDT", 0, 10*time.Minute, -1.0, "exchange_income"),
		lp("ETH/USDT", 1, 20*time.Minute, -1.0, "exchange_income"),
		lp("ETH/USDT", 2, 30*time.Minute, +0.1, "exchange_income"), // 打断
		lp("ETH/USDT", 3, 40*time.Minute, -1.0, "exchange_income"),
		lp("ETH/USDT", 4, 50*time.Minute, -1.0, "exchange_income"),
	}
	if got := runOf(t, rows, "ETH/USDT").Streak; got != 2 {
		t.Fatalf("streak = %d, want 2", got)
	}
}

// 打平(0)按"没亏"处理，同样打断连亏串 —— 只有严格为负才算一笔亏损。
func TestComputeSymbolLossRuns_ZeroBreaksStreak(t *testing.T) {
	rows := []models.StrategyPosition{
		lp("XRP/USDT", 0, 10*time.Minute, -1.0, "exchange_income"),
		lp("XRP/USDT", 1, 20*time.Minute, 0.0, "exchange_income"),
		lp("XRP/USDT", 2, 30*time.Minute, -1.0, "exchange_income"),
	}
	if got := runOf(t, rows, "XRP/USDT").Streak; got != 1 {
		t.Fatalf("streak = %d, want 1", got)
	}
}

// 孪生行是同一笔平仓的两条记账，只能算一笔 —— 否则连亏串凭空翻倍、提前熔断。
func TestComputeSymbolLossRuns_TwinRowsCountOnce(t *testing.T) {
	rows := []models.StrategyPosition{
		lp("SOL/USDT", 0, 10*time.Minute, -0.9, "exchange_income"),
		lp("SOL/USDT", 1, 20*time.Minute, -0.6, "fill"),
		lp("SOL/USDT", 1, 20*time.Minute, -0.9, "exchange_income"), // 同一笔的孪生行
		lp("SOL/USDT", 2, 30*time.Minute, -0.6, "fill"),
		lp("SOL/USDT", 2, 30*time.Minute, -0.9, "exchange_income"),
	}
	if got := runOf(t, rows, "SOL/USDT").Streak; got != 3 {
		t.Fatalf("streak = %d, want 3 (twins must not double-count)", got)
	}
}

// 孪生行两条来源符号相反时，以交易所结算的 exchange_income 为准，
// 不能让"最有利"的那条记账掩盖真实亏损。
func TestComputeSymbolLossRuns_TwinConflictPrefersExchangeIncome(t *testing.T) {
	rows := []models.StrategyPosition{
		lp("BNB/USDT", 0, 10*time.Minute, +0.5, "fill"),
		lp("BNB/USDT", 0, 10*time.Minute, -1.0, "exchange_income"),
	}
	if got := runOf(t, rows, "BNB/USDT").Streak; got != 1 {
		t.Fatalf("streak = %d, want 1 (exchange_income wins the tie)", got)
	}
}

// pnl_source 为 ""/"unknown" 是【缺失值】而非 0：既不计一笔亏损，也不打断连亏串。
func TestComputeSymbolLossRuns_MissingPnLSourceSkipped(t *testing.T) {
	rows := []models.StrategyPosition{
		lp("DOGE/USDT", 0, 10*time.Minute, -1.0, "exchange_income"),
		lp("DOGE/USDT", 1, 20*time.Minute, 0.0, "unknown"), // 缺失值，必须跳过
		lp("DOGE/USDT", 2, 30*time.Minute, 0.0, ""),        // 缺失值，必须跳过
		lp("DOGE/USDT", 3, 40*time.Minute, -1.0, "exchange_income"),
		lp("DOGE/USDT", 4, 50*time.Minute, -1.0, "exchange_income"),
	}
	run := runOf(t, rows, "DOGE/USDT")
	if run.Streak != 3 {
		t.Fatalf("streak = %d, want 3 (missing values neither count nor break)", run.Streak)
	}
}

// 各 symbol 的连亏串互相独立。
func TestComputeSymbolLossRuns_PerSymbolIndependent(t *testing.T) {
	rows := []models.StrategyPosition{
		lp("AAA/USDT", 0, 10*time.Minute, -1.0, "exchange_income"),
		lp("BBB/USDT", 1, 20*time.Minute, +1.0, "exchange_income"),
		lp("AAA/USDT", 2, 30*time.Minute, -1.0, "exchange_income"),
		lp("BBB/USDT", 3, 40*time.Minute, -1.0, "exchange_income"),
	}
	runs := computeSymbolLossRuns(rows)
	a := runs[exchange.NormalizeSymbol("AAA/USDT")]
	b := runs[exchange.NormalizeSymbol("BBB/USDT")]
	if a.Streak != 2 {
		t.Fatalf("AAA streak = %d, want 2", a.Streak)
	}
	if b.Streak != 1 {
		t.Fatalf("BBB streak = %d, want 1", b.Streak)
	}
}

// 隔离期判定：连亏达阈值才隔离；隔离到「末笔平仓 + 窗口」为止，过期即放行。
func TestLossStreakBanUntil(t *testing.T) {
	window := 48 * time.Hour
	now := lossT0.Add(100 * time.Hour)

	cases := []struct {
		name      string
		run       symbolLossRun
		threshold int
		wantBan   bool
	}{
		{"达阈值且末笔在窗口内", symbolLossRun{Streak: 3, LastClose: now.Add(-1 * time.Hour)}, 3, true},
		{"达阈值但末笔已过窗口", symbolLossRun{Streak: 3, LastClose: now.Add(-49 * time.Hour)}, 3, false},
		{"恰好在窗口边界上（应放行）", symbolLossRun{Streak: 3, LastClose: now.Add(-window)}, 3, false},
		{"未达阈值", symbolLossRun{Streak: 2, LastClose: now.Add(-1 * time.Hour)}, 3, false},
		{"无平仓记录", symbolLossRun{Streak: 3}, 3, false},
		{"阈值<=0 视为关闭", symbolLossRun{Streak: 9, LastClose: now}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			until, banned := lossStreakBanUntil(tc.run, tc.threshold, window, now)
			if banned != tc.wantBan {
				t.Fatalf("banned = %v, want %v", banned, tc.wantBan)
			}
			if banned {
				if want := tc.run.LastClose.Add(window); !until.Equal(want) {
					t.Fatalf("until = %v, want %v", until, want)
				}
			}
		})
	}
}

// 配置读取：缺键时默认 开 / 3 / 48h；给了键则按键走。
func TestLossStreakConfigAccessors(t *testing.T) {
	inst := &StrategyInstance{ID: "t"}
	inst.setConfig(map[string]interface{}{})
	if !lossStreakBlacklistEnabled(inst) {
		t.Fatal("default should be enabled (owner 2026-09-19 直令)")
	}
	if got := lossStreakThreshold(inst); got != 3 {
		t.Fatalf("default threshold = %d, want 3", got)
	}
	if got := lossStreakQuarantine(inst); got != 48*time.Hour {
		t.Fatalf("default quarantine = %v, want 48h", got)
	}

	inst.setConfig(map[string]interface{}{
		"loss_streak_blacklist_enabled": false,
		"loss_streak_threshold":         float64(5),
		"loss_streak_quarantine_hours":  float64(6),
	})
	if lossStreakBlacklistEnabled(inst) {
		t.Fatal("explicit false must disable")
	}
	if got := lossStreakThreshold(inst); got != 5 {
		t.Fatalf("threshold = %d, want 5", got)
	}
	if got := lossStreakQuarantine(inst); got != 6*time.Hour {
		t.Fatalf("quarantine = %v, want 6h", got)
	}

	// 字符串形式的开关（PATCH 走 JSON merge patch，值可能是字符串）。
	inst.setConfig(map[string]interface{}{"loss_streak_blacklist_enabled": "false"})
	if lossStreakBlacklistEnabled(inst) {
		t.Fatal(`"false" string must disable`)
	}
	inst.setConfig(map[string]interface{}{"loss_streak_blacklist_enabled": "true"})
	if !lossStreakBlacklistEnabled(inst) {
		t.Fatal(`"true" string must enable`)
	}
}

// TestLossStreakTwinRowMerging 锁死 2026-09-19 实测到的去重键缺陷。
//
// 背景：孪生行（exchange_income / fill 双账路径）的 **open_time 并不相等**，
// 实测 APT/USDT 的两条行 open_time 差 94.7 秒、close_time 只差 6 秒。
// 我第一版用 (symbol, open_time) 当去重键 ⇒ 漏并 ⇒ 同一笔亏损记两遍。
// 两个方向都会错：
//
//	虚高：同一笔亏损数两遍，连亏串翻倍提早触发（APT 报 5、真值 3）
//	虚低：孪生对里 fill 那条是正的时，没合并进来的正数会把连亏串提前打断
//	      （AKE/DRIFT/PONS 报 2、真值 3）⇒ 该拉黑的没拉黑
//
// 所以这个测试同时覆盖"合并且不重复计数"和"负的孪生不能被正的那条洗白"。
func TestLossStreakTwinRowMerging(t *testing.T) {
	const sym = "APT/USDT"

	// (1) 一笔亏损的两个记账路径，open_time 差 95 秒、close_time 差 6 秒
	//     ⇒ 必须只算 1 笔，且取 exchange_income 的值。
	rows := []models.StrategyPosition{
		lp(sym, 0, 100*time.Second, -0.6449, "exchange_income"),
		lp(sym, -95*time.Second, 94*time.Second, -0.6448, "fill"),
	}
	if got := runOf(t, rows, sym); got.Streak != 1 {
		t.Fatalf("孪生行应合并成 1 笔，得 %d", got.Streak)
	}

	// (2) 两笔真实连亏，各带一个孪生 ⇒ 必须是 2，不是 4。
	rows = []models.StrategyPosition{
		lp(sym, 0, 100*time.Second, -0.64, "exchange_income"),
		lp(sym, -95*time.Second, 94*time.Second, -0.64, "fill"),
		lp(sym, -3*time.Hour, -3*time.Hour+100*time.Second, -1.36, "exchange_income"),
		lp(sym, -3*time.Hour-64*time.Second, -3*time.Hour+94*time.Second, -1.36, "fill"),
	}
	if got := runOf(t, rows, sym); got.Streak != 2 {
		t.Fatalf("两笔真实连亏+各自孪生 应得 2，得 %d", got.Streak)
	}

	// (3) 负的 exchange_income 不能被正的同笔 fill 洗白：
	//     真实结果是一笔亏损，连亏串不能因为那条 +0.5 而中断。
	rows = []models.StrategyPosition{
		lp(sym, 0, 100*time.Second, -1.0, "exchange_income"),
		lp(sym, -95*time.Second, 94*time.Second, +0.5, "fill"),
		lp(sym, -3*time.Hour, -3*time.Hour+100*time.Second, -2.0, "exchange_income"),
		lp(sym, -3*time.Hour, -3*time.Hour+100*time.Second, -2.0, "fill"),
		lp(sym, -6*time.Hour, -6*time.Hour+100*time.Second, -3.0, "exchange_income"),
		lp(sym, -6*time.Hour, -6*time.Hour+100*time.Second, -3.0, "fill"),
	}
	if got := runOf(t, rows, sym); got.Streak != 3 {
		t.Fatalf("负孪生不得被正孪生洗白，应得 3，得 %d", got.Streak)
	}

	// (4) 同来源的两笔平仓即使时间很近也**不能**合并（那是两次真实平仓）。
	rows = []models.StrategyPosition{
		lp(sym, 0, 100*time.Second, -1.0, "exchange_income"),
		lp(sym, 0, 101*time.Second, -1.0, "exchange_income"),
	}
	if got := runOf(t, rows, sym); got.Streak != 2 {
		t.Fatalf("同来源邻近平仓应保持 2 笔，得 %d", got.Streak)
	}

	// (5) 超出容差的异来源两笔是两次独立平仓，不合并。
	rows = []models.StrategyPosition{
		lp(sym, 0, 100*time.Second, -1.0, "exchange_income"),
		lp(sym, 0, 100*time.Second+lossStreakTwinWindow+time.Second, -1.0, "fill"),
	}
	if got := runOf(t, rows, sym); got.Streak != 2 {
		t.Fatalf("超出孪生容差应保持 2 笔，得 %d", got.Streak)
	}
}
