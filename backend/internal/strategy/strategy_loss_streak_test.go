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
