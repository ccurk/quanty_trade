package strategy

import (
	"math"
	"testing"
	"time"

	"quanty_trade/internal/models"

	"gorm.io/gorm"
)

// 台账 #91:qt-breakout-follow 51 行里 46 行 realized_pn_l=0,其中
// id=1897(STAR,平仓单 676 @ 0.08014)、id=1966(AKE,平仓单 2874 @ 0.0102008)
// 有真实 filled 平仓单却记 0,且 direction 记 long —— 多头只能 sell 平,buy 平
// 说明那两行实际是空头。按空头补回 +1.2897 后该策略从 -1.2208 变 +0.069:
// **符号翻转**,不是精度问题。
//
// 根因是 applyOrderFillToPosition 分不清"开仓成交"和"平仓成交":找不到 open 行时
// 它按 side 猜 —— buy 一律猜成开多。买入平空于是被写成一行 direction=long /
// realized_pn_l=0 的假仓。下面三个用例按 1897/1966 的真实数量与价格构造。
//
// 用 gorm 的 sqlite :memory: + newAdoptTestDB(adopt_guard_test.go),与本包既有
// DB 测试同一套路。

const (
	fillTestOwner    = uint(7)
	fillTestStrategy = "qt-breakout-follow"
)

func loadOnlyPosition(t *testing.T, db *gorm.DB) models.StrategyPosition {
	t.Helper()
	var rows []models.StrategyPosition
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load positions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 position row, got %d", len(rows))
	}
	return rows[0]
}

func assertMoney(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-8 {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// 开空仓且 tp/sl 都是 0 是真实存在的路径:resolveTPSLFromROI 在 entryPrice<=0
// (市价单回包没带成交价)时原样返回调用方的 0/0。旧代码此时一行都不写,账本
// 从此缺这条腿 —— 台账 #92「244 笔 filled entry vs 2,824 行 position」的一个来源。
func TestOpenShortFillWithoutTPSLStillRecordsShortPosition(t *testing.T) {
	db := newAdoptTestDB(t)
	openTime := time.Date(2026, 9, 8, 10, 49, 35, 0, time.UTC)

	applyOrderFillToPosition(nil, fillTestOwner, fillTestStrategy, fillTestStrategy, "Binance",
		"STAR/USDT", "sell", 676, 0.08998, 0, 0, openTime, "open")

	got := loadOnlyPosition(t, db)
	if got.Direction != "short" {
		t.Errorf("direction = %q, want \"short\" (卖出开仓只能是空头)", got.Direction)
	}
	if got.Status != "open" {
		t.Errorf("status = %q, want \"open\"", got.Status)
	}
	assertMoney(t, "amount", got.Amount, 676)
	assertMoney(t, "avg_price", got.AvgPrice, 0.08998)
}

// id=1897 的形状:STAR 空头 676 张,平仓单 buy 676 @ 0.08014。
// 旧代码:开仓那步不落行 → 平仓这步找不到 open 行 → 按 side=buy 猜成开多 →
// 写出 direction=long、realized_pn_l=0、status=open 的假仓。
func TestShortRoundTripRecordsRealizedPnLAndKeepsShortDirection(t *testing.T) {
	db := newAdoptTestDB(t)
	openTime := time.Date(2026, 9, 8, 10, 49, 35, 0, time.UTC)
	closeTime := openTime.Add(37 * time.Minute)

	applyOrderFillToPosition(nil, fillTestOwner, fillTestStrategy, fillTestStrategy, "Binance",
		"STAR/USDT", "sell", 676, 0.08998, 0, 0, openTime, "open")
	applyOrderFillToPosition(nil, fillTestOwner, fillTestStrategy, fillTestStrategy, "Binance",
		"STAR/USDT", "buy", 676, 0.08014, 0, 0, closeTime, "close")

	got := loadOnlyPosition(t, db)
	if got.Direction != "short" {
		t.Errorf("direction = %q, want \"short\" (buy 平仓 ⇒ 持仓是空头)", got.Direction)
	}
	if got.Status != "closed" {
		t.Errorf("status = %q, want \"closed\"", got.Status)
	}
	assertMoney(t, "amount", got.Amount, 0)
	assertMoney(t, "closed_qty", got.ClosedQty, 676)
	assertMoney(t, "avg_close_price", got.AvgClosePrice, 0.08014)
	// 空头盈亏 = qty * (entry - exit) = 676 * (0.08998 - 0.08014)
	assertMoney(t, "realized_pn_l", got.RealizedPnL, roundMoney8(676*(0.08998-0.08014)))
	assertMoney(t, "realized_notional", got.RealizedNotional, roundMoney8(676*0.08998))
}

// id=1966 的形状,但连开仓腿都不在库里(账本缺口)。平仓成交找不到归属时
// 唯一诚实的结果是"不写" —— 写一行 direction=long / realized_pn_l=0 的假仓
// 会同时污染方向统计和 PnL 汇总,而且看不出来。
func TestOrphanCloseFillCreatesNoPhantomLongPosition(t *testing.T) {
	db := newAdoptTestDB(t)
	closeTime := time.Date(2026, 9, 8, 21, 4, 11, 0, time.UTC)

	applyOrderFillToPosition(nil, fillTestOwner, fillTestStrategy, fillTestStrategy, "Binance",
		"AKE/USDT", "buy", 2874, 0.0102008, 0, 0, closeTime, "close")

	if n := countPositionRows(t, db); n != 0 {
		var rows []models.StrategyPosition
		_ = db.Find(&rows).Error
		for _, r := range rows {
			t.Logf("phantom row: direction=%q amount=%v avg_price=%v realized_pn_l=%v status=%q",
				r.Direction, r.Amount, r.AvgPrice, r.RealizedPnL, r.Status)
		}
		t.Errorf("orphan close fill created %d position row(s), want 0", n)
	}
}

// 守住现在就对的那一半:多头开平不能因为上面的修复而被弄反。
func TestLongRoundTripUnchanged(t *testing.T) {
	db := newAdoptTestDB(t)
	openTime := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)

	applyOrderFillToPosition(nil, fillTestOwner, fillTestStrategy, fillTestStrategy, "Binance",
		"BTC/USDT", "buy", 100, 1.0, 0, 0, openTime, "open")
	applyOrderFillToPosition(nil, fillTestOwner, fillTestStrategy, fillTestStrategy, "Binance",
		"BTC/USDT", "sell", 100, 1.1, 0, 0, openTime.Add(time.Hour), "close")

	got := loadOnlyPosition(t, db)
	if got.Direction != "long" {
		t.Errorf("direction = %q, want \"long\"", got.Direction)
	}
	if got.Status != "closed" {
		t.Errorf("status = %q, want \"closed\"", got.Status)
	}
	assertMoney(t, "realized_pn_l", got.RealizedPnL, roundMoney8(100*(1.1-1.0)))
}
