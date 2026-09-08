package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAttributionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.StrategyParamVersion{},
		&models.StrategyPosition{},
		&models.StrategyOrder{},
		&models.ExchangeFill{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prev })
	return db
}

// The whole point of the two-tier resolution: a position with NO stamp still
// lands in the right version bucket via its open_time, and a position older than
// every recorded version falls into "unversioned" instead of being silently
// counted against v1.
func TestAttributionBucketsStampedWindowedAndUnversioned(t *testing.T) {
	db := newAttributionTestDB(t)
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	v1End := base.Add(24 * time.Hour)

	v1 := models.StrategyParamVersion{
		StrategyID: "sid-1", StrategyName: "趋势A", OwnerID: 7, Seq: 1, Label: "v1",
		ConfigHash: "h1", EffectiveFrom: base, EffectiveTo: &v1End, IsCurrent: false,
	}
	v2 := models.StrategyParamVersion{
		StrategyID: "sid-1", StrategyName: "趋势A", OwnerID: 7, Seq: 2, Label: "v2-atr2.5",
		Note: "止损从 1.5ATR 放宽到 2.5ATR", ConfigHash: "h2",
		EffectiveFrom: v1End, IsCurrent: true,
	}
	if err := db.Create(&v1).Error; err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if err := db.Create(&v2).Error; err != nil {
		t.Fatalf("create v2: %v", err)
	}

	mk := func(open time.Time, pnl float64, notional float64, stamp *uint) models.StrategyPosition {
		return models.StrategyPosition{
			StrategyID: "sid-1", StrategyName: "趋势A", OwnerID: 7,
			Exchange: "Binance", Symbol: "BTC/USDT", Direction: "long",
			Status: "closed", ParamVersionID: stamp, ClosedQty: 1,
			RealizedPnL: pnl, RealizedNotional: notional,
			OpenTime: open, CloseTime: open.Add(time.Hour),
		}
	}
	positions := []models.StrategyPosition{
		// v1 window, no stamp -> resolved by open_time
		mk(base.Add(2*time.Hour), 10, 1000, nil),
		mk(base.Add(5*time.Hour), -4, 1000, nil),
		// v2 window, stamped
		mk(v1End.Add(2*time.Hour), 30, 1000, &v2.ID),
		mk(v1End.Add(4*time.Hour), 20, 1000, &v2.ID),
		// before any version exists -> unversioned
		mk(base.Add(-48*time.Hour), -100, 1000, nil),
		// another owner must never leak into owner 7's numbers
		func() models.StrategyPosition {
			p := mk(v1End.Add(3*time.Hour), 999, 1000, &v2.ID)
			p.OwnerID = 8
			return p
		}(),
	}
	for i := range positions {
		if err := db.Create(&positions[i]).Error; err != nil {
			t.Fatalf("create position %d: %v", i, err)
		}
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/stats/strategy-attribution", nil)
	c.Set("user_id", uint(7))
	GetStrategyAttribution(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp AttributionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}

	byLabel := map[string]AttributionRow{}
	for _, r := range resp.Rows {
		byLabel[r.VersionLabel] = r
	}
	if len(byLabel) != 3 {
		t.Fatalf("expected 3 buckets, got %d: %+v", len(byLabel), resp.Rows)
	}

	if got := byLabel["v1"]; got.Trades != 2 || got.NetPnL != 6 || got.Wins != 1 || got.Losses != 1 {
		t.Errorf("v1 bucket = %+v, want trades=2 net=6 wins=1 losses=1", got)
	}
	if got := byLabel["v2-atr2.5"]; got.Trades != 2 || got.NetPnL != 50 || got.AvgPnL != 25 {
		t.Errorf("v2 bucket = %+v, want trades=2 net=50 avg=25", got)
	}
	if got := byLabel["v2-atr2.5"]; got.VersionNote == "" {
		t.Errorf("v2 bucket lost the owner's note: %+v", got)
	}
	if got := byLabel["unversioned"]; got.Trades != 1 || got.NetPnL != -100 || got.ParamVersionID != 0 {
		t.Errorf("unversioned bucket = %+v, want trades=1 net=-100 id=0", got)
	}
	for _, r := range resp.Rows {
		if r.NetPnL == 999 {
			t.Fatalf("owner 8's trade leaked into owner 7's attribution: %+v", r)
		}
	}
}

// The acceptance criterion for the fee work: net expectancy must be a real
// number and the fee must not be zero. Before exchange_fills existed, fee was
// not stored anywhere in the platform, so net_avg_pnl could only ever equal
// gross — which is exactly how a backtest that deducts 4bps ended up being
// compared against live numbers that deducted nothing.
func TestAttributionNetExpectancyDeductsRealExchangeFee(t *testing.T) {
	db := newAttributionTestDB(t)
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	v1 := models.StrategyParamVersion{
		StrategyID: "sid-1", StrategyName: "趋势A", OwnerID: 7, Seq: 1, Label: "v1",
		ConfigHash: "h1", EffectiveFrom: base, IsCurrent: true,
	}
	if err := db.Create(&v1).Error; err != nil {
		t.Fatalf("create v1: %v", err)
	}

	// Two closed positions, +10 and -4 gross.
	for i, pnl := range []float64{10, -4} {
		p := models.StrategyPosition{
			StrategyID: "sid-1", StrategyName: "趋势A", OwnerID: 7,
			Status: "closed", ClosedQty: 1, ParamVersionID: &v1.ID,
			RealizedPnL: pnl, RealizedNotional: 1000,
			OpenTime:  base.Add(time.Duration(i+1) * time.Hour),
			CloseTime: base.Add(time.Duration(i+2) * time.Hour),
		}
		if err := db.Create(&p).Error; err != nil {
			t.Fatalf("create position: %v", err)
		}
	}

	// Both legs of each round trip. The entry order matters: its fill carries
	// realized_pnl = 0 but is still charged commission, and it is the leg the
	// collection path is most likely to drop.
	orders := []models.StrategyOrder{
		{StrategyID: "sid-1", OwnerID: 7, Purpose: "entry", Status: "filled",
			ClientOrderID: "coid-1", ExchangeOrderID: "1001",
			ParamVersionID: &v1.ID, RequestedAt: base.Add(time.Hour)},
		{StrategyID: "sid-1", OwnerID: 7, Purpose: "close", Status: "filled",
			ClientOrderID: "coid-2", ExchangeOrderID: "1002",
			ParamVersionID: &v1.ID, RequestedAt: base.Add(2 * time.Hour)},
	}
	for i := range orders {
		if err := db.Create(&orders[i]).Error; err != nil {
			t.Fatalf("create order: %v", err)
		}
	}

	fills := []models.ExchangeFill{
		// entry leg: realized_pnl 0, commission 0.40 — must still be counted
		{Exchange: "Binance", Symbol: "BTCUSDT", TradeID: "1", OrderID: "1001",
			QuoteQty: 1000, RealizedPnL: 0, Commission: 0.40, CommissionAsset: "USDT",
			IsMaker: false, TradeTime: base.Add(time.Hour)},
		// close leg
		{Exchange: "Binance", Symbol: "BTCUSDT", TradeID: "2", OrderID: "1002",
			QuoteQty: 1000, RealizedPnL: 10, Commission: 0.40, CommissionAsset: "USDT",
			IsMaker: true, TradeTime: base.Add(2 * time.Hour)},
		// a BNB-paid fill: excluded from the USDT sum, but counted so the gap shows
		{Exchange: "Binance", Symbol: "BTCUSDT", TradeID: "3", OrderID: "1002",
			QuoteQty: 500, RealizedPnL: -4, Commission: 0.002, CommissionAsset: "BNB",
			IsMaker: false, TradeTime: base.Add(2 * time.Hour)},
		// a fill from an order this platform never placed (shared account): must
		// NOT be attributed to us — it joins to no strategy_order.
		{Exchange: "Binance", Symbol: "ETHUSDT", TradeID: "4", OrderID: "9999",
			QuoteQty: 5000, RealizedPnL: -50, Commission: 99, CommissionAsset: "USDT",
			IsMaker: false, TradeTime: base.Add(2 * time.Hour)},
	}
	for i := range fills {
		if err := db.Create(&fills[i]).Error; err != nil {
			t.Fatalf("create fill: %v", err)
		}
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/stats/strategy-attribution", nil)
	c.Set("user_id", uint(7))
	GetStrategyAttribution(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp AttributionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}
	if len(resp.Rows) != 1 {
		t.Fatalf("expected 1 bucket, got %+v", resp.Rows)
	}
	got := resp.Rows[0]

	if got.GrossPnL != 6 {
		t.Errorf("gross = %v, want 6", got.GrossPnL)
	}
	// acceptance: fee is not zero
	if got.FeeUSDT != 0.8 {
		t.Errorf("fee_usdt = %v, want 0.8 (both legs, USDT only)", got.FeeUSDT)
	}
	if got.FeeFills != 3 {
		t.Errorf("fee_fills = %v, want 3 matched fills", got.FeeFills)
	}
	if got.FeeOtherAssetFills != 1 {
		t.Errorf("fee_other_asset_fills = %v, want 1 (the BNB fill)", got.FeeOtherAssetFills)
	}
	if got.MakerFills != 1 {
		t.Errorf("maker_fills = %v, want 1", got.MakerFills)
	}
	// acceptance: net expectancy is a real number and differs from gross
	if got.NetPnL != 5.2 {
		t.Errorf("net_pnl = %v, want 6 - 0.8 = 5.2", got.NetPnL)
	}
	if got.NetAvgPnL != 2.6 {
		t.Errorf("net_avg_pnl = %v, want 2.6", got.NetAvgPnL)
	}
	if got.NetAvgPnL == got.AvgPnL {
		t.Errorf("net expectancy still equals gross expectancy (%v) — fee was not deducted", got.AvgPnL)
	}
	// the shared account's foreign fill must not have leaked in
	if got.FeeUSDT > 90 {
		t.Errorf("a fill from an order we never placed was charged to us: fee=%v", got.FeeUSDT)
	}
	if resp.Scope == "" {
		t.Error("response carries no 口径 string; every reported number must ship its WHERE clause")
	}
}

// 2006 of the ~2771 rows marked 'closed' on the live database have closed_qty=0
// and never actually traded. Counting them would multiply the trade count and
// divide expectancy by the same factor, which is precisely the failure mode the
// attribution table exists to prevent.
func TestAttributionExcludesGhostClosedRows(t *testing.T) {
	db := newAttributionTestDB(t)
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	v1 := models.StrategyParamVersion{
		StrategyID: "sid-1", OwnerID: 7, Seq: 1, Label: "v1",
		EffectiveFrom: base, IsCurrent: true,
	}
	if err := db.Create(&v1).Error; err != nil {
		t.Fatalf("create v1: %v", err)
	}
	rows := []models.StrategyPosition{
		{StrategyID: "sid-1", OwnerID: 7, Status: "closed", ClosedQty: 1,
			RealizedPnL: 10, RealizedNotional: 100,
			OpenTime: base.Add(time.Hour), CloseTime: base.Add(2 * time.Hour)},
		// ghost: marked closed, never traded
		{StrategyID: "sid-1", OwnerID: 7, Status: "closed", ClosedQty: 0,
			RealizedPnL: 0, RealizedNotional: 0,
			OpenTime: base.Add(time.Hour), CloseTime: base.Add(2 * time.Hour)},
		{StrategyID: "sid-1", OwnerID: 7, Status: "closed", ClosedQty: 0,
			RealizedPnL: 0, RealizedNotional: 0,
			OpenTime: base.Add(time.Hour), CloseTime: base.Add(2 * time.Hour)},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("create position: %v", err)
		}
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/stats/strategy-attribution", nil)
	c.Set("user_id", uint(7))
	GetStrategyAttribution(c)

	var resp AttributionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Rows) != 1 || resp.Rows[0].Trades != 1 {
		t.Fatalf("rows = %+v, want exactly the 1 real trade", resp.Rows)
	}
	if resp.Rows[0].AvgPnL != 10 {
		t.Errorf("avg_pnl = %v, want 10; ghosts diluted the expectancy",
			resp.Rows[0].AvgPnL)
	}
}

// close_time filtering must not change which VERSION a trade belongs to.
func TestAttributionWindowFiltersOnCloseTime(t *testing.T) {
	db := newAttributionTestDB(t)
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	v1 := models.StrategyParamVersion{
		StrategyID: "sid-1", OwnerID: 7, Seq: 1, Label: "v1",
		EffectiveFrom: base, IsCurrent: true,
	}
	if err := db.Create(&v1).Error; err != nil {
		t.Fatalf("create v1: %v", err)
	}
	for i, d := range []time.Duration{2 * time.Hour, 50 * time.Hour} {
		p := models.StrategyPosition{
			StrategyID: "sid-1", OwnerID: 7, Status: "closed", ClosedQty: 1,
			RealizedPnL: float64(i + 1), RealizedNotional: 100,
			OpenTime: base.Add(d), CloseTime: base.Add(d + time.Hour),
		}
		if err := db.Create(&p).Error; err != nil {
			t.Fatalf("create position: %v", err)
		}
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/stats/strategy-attribution?from=2026-09-02&to=2026-09-05", nil)
	c.Set("user_id", uint(7))
	GetStrategyAttribution(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp AttributionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Rows) != 1 || resp.Rows[0].Trades != 1 || resp.Rows[0].NetPnL != 2 {
		t.Fatalf("rows = %+v, want the single trade closed inside the window", resp.Rows)
	}
}
