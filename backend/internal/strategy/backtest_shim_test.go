package strategy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
	"quanty_trade/internal/ws"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// uptrendExchange feeds a deterministic flat-then-rising series so the
// redis_signal_template SMA(10)>SMA(30) golden cross is guaranteed to fire.
// (MockExchange's default series is a monotonic downtrend at hourly stamps,
// which would never trigger a long entry and thus can't prove trades flow.)
type uptrendExchange struct{ *exchange.MockExchange }

func (uptrendExchange) FetchHistoricalCandles(symbol, timeframe string, start, end time.Time) ([]exchange.Candle, error) {
	var cs []exchange.Candle
	ts := start
	price := 100.0
	for i := 0; i < 35; i++ { // flat warmup, fills slow_window=30
		cs = append(cs, exchange.Candle{Timestamp: ts, Open: price, High: price + 1, Low: price - 1, Close: price, Volume: 1})
		ts = ts.Add(time.Hour)
	}
	for i := 0; i < 20; i++ { // uptrend -> fast SMA crosses above slow SMA
		price += 5
		cs = append(cs, exchange.Candle{Timestamp: ts, Open: price, High: price + 1, Low: price - 1, Close: price, Volume: 1})
		ts = ts.Add(time.Hour)
	}
	return cs, nil
}

// TestBacktestProducesTrades is a regression guard for the backtest transport
// bug: the harness fed candles over stdin while strategies ingest candles via
// the (Mini)Redis pub/sub channel, so every backtest returned 0 trades. With
// the stdin/stdout-backed backtest shim, a real backtest must now trade.
func TestBacktestProducesTrades(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.StrategyTemplate{}, &models.StrategyInstance{}, &models.StrategyVersion{}, &models.Backtest{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := database.DB
	database.DB = db
	defer func() { database.DB = prev }()

	tmplPath, err := filepath.Abs("../../../strategies/redis_signal_template.py")
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	raw, err := os.ReadFile(tmplPath)
	if err != nil {
		t.Fatalf("read strategy: %v", err)
	}
	code := strings.TrimSpace(string(raw))
	tmpl := models.StrategyTemplate{Name: "redis-sma-test", Path: tmplPath, Code: code, UpdatedAt: time.Now()}
	if err := db.Create(&tmpl).Error; err != nil {
		t.Fatalf("seed template: %v", err)
	}
	instID := "bt-test-instance"
	if err := db.Create(&models.StrategyInstance{ID: instID, Name: "TEST", TemplateID: tmpl.ID, Status: "stopped"}).Error; err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	m := NewManager(ws.NewHub(), uptrendExchange{&exchange.MockExchange{Name: "mock"}})
	m.instances[instID] = &StrategyInstance{
		ID:         instID,
		Name:       "TEST",
		TemplateID: tmpl.ID,
		Path:       tmplPath,
		Config: map[string]interface{}{
			"symbol":       "BTCUSDT",
			"fast_window":  float64(10),
			"slow_window":  float64(30),
			"trade_amount": float64(0.01),
		},
	}

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	res, err := m.runBacktestSimulation(instID, "BTCUSDT", "1h", start, start.Add(200*time.Hour), 10000, 1, 1)
	if err != nil {
		t.Fatalf("backtest failed: %v", err)
	}
	t.Logf("TotalTrades=%d FinalBalance=%.4f equityPoints=%d", res.TotalTrades, res.FinalBalance, len(res.EquityCurve))
	if res.TotalTrades < 1 {
		t.Fatalf("expected >=1 trade after transport fix, got %d (0 trades = transport still broken)", res.TotalTrades)
	}
	if res.TotalFees <= 0 {
		t.Fatalf("expected commission to be charged, got TotalFees=%.6f (fee model not applied)", res.TotalFees)
	}
}
