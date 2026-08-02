package strategy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

// minuteFeedExchange serves REPRO_N synthetic 1m candles with a mild wave so
// indicator code has something to chew on.
type minuteFeedExchange struct {
	*exchange.MockExchange
	n int
}

func (m minuteFeedExchange) FetchHistoricalCandles(symbol, timeframe string, start, end time.Time) ([]exchange.Candle, error) {
	var cs []exchange.Candle
	ts := start
	price := 100.0
	for i := 0; i < m.n; i++ {
		delta := 0.3
		if i%7 < 3 {
			delta = -0.25
		}
		price += delta
		cs = append(cs, exchange.Candle{Timestamp: ts, Open: price - delta, High: price + 0.4, Low: price - 0.4, Close: price, Volume: 100})
		ts = ts.Add(time.Minute)
	}
	return cs, nil
}

// TestReproBacktestHang runs the real live strategy code + live config through
// runBacktestSimulation. REPRO_CODE / REPRO_CFG point at files dumped from the
// production API; REPRO_N sets the candle count. On timeout it dumps all
// goroutine stacks so the blocked line is visible.
func TestReproBacktestHang(t *testing.T) {
	codePath := os.Getenv("REPRO_CODE")
	cfgPath := os.Getenv("REPRO_CFG")
	if codePath == "" || cfgPath == "" {
		t.Skip("REPRO_CODE/REPRO_CFG not set")
	}
	nCandles := 120
	if v := os.Getenv("REPRO_N"); v != "" {
		var x int
		if _, err := json.Number(v).Int64(); err == nil {
			if n, err2 := json.Number(v).Int64(); err2 == nil {
				x = int(n)
			}
		}
		if x > 0 {
			nCandles = x
		}
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.StrategyTemplate{}, &models.StrategyInstance{}, &models.StrategyVersion{}, &models.Backtest{}, &models.StrategyLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := database.DB
	database.DB = db
	defer func() { database.DB = prev }()

	raw, err := os.ReadFile(codePath)
	if err != nil {
		t.Fatalf("read live code: %v", err)
	}
	code := strings.TrimSpace(string(raw))

	cfgRaw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read live config: %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatalf("parse live config: %v", err)
	}

	absCode, _ := filepath.Abs(codePath)
	tmpl := models.StrategyTemplate{Name: "live-repro", Path: absCode, Code: code, UpdatedAt: time.Now()}
	if err := db.Create(&tmpl).Error; err != nil {
		t.Fatalf("seed template: %v", err)
	}
	instID := "hang-repro-instance"
	if err := db.Create(&models.StrategyInstance{ID: instID, Name: "REPRO", TemplateID: tmpl.ID, Status: "stopped"}).Error; err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	m := NewManager(ws.NewHub(), minuteFeedExchange{&exchange.MockExchange{Name: "mock"}, nCandles})
	m.instances[instID] = &StrategyInstance{
		ID:         instID,
		Name:       "REPRO",
		TemplateID: tmpl.ID,
		Path:       absCode,
		Config:     cfg,
	}

	start := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Duration(nCandles) * time.Minute)

	type outcome struct {
		res *BacktestResult
		err error
	}
	done := make(chan outcome, 1)
	t0 := time.Now()
	go func() {
		res, err := m.runBacktestSimulation(instID, "BTC/USDT", "1m", start, end, 227, 1, 99)
		done <- outcome{res, err}
	}()

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("backtest errored after %s: %v", time.Since(t0), o.err)
		}
		t.Logf("completed in %s: trades=%d final=%.2f equity_points=%d", time.Since(t0), o.res.TotalTrades, o.res.FinalBalance, len(o.res.EquityCurve))
	case <-time.After(60 * time.Second):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("HANG after 60s with n=%d candles. goroutine dump:\n%s", nCandles, buf[:n])
	}
}
