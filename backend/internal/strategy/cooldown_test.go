package strategy

import (
	"testing"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Regression for the max_concurrent_positions>=2 cooldown bypass: LastEntryAt
// was only recorded for the single globally-newest entry row, so a symbol whose
// entry was covered by a later entry on ANOTHER symbol lost its re-entry
// cooldown entirely.
func TestPerSymbolCooldownSurvivesNewerOtherSymbolEntry(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.StrategyOrder{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := database.DB
	database.DB = db
	defer func() { database.DB = prev }()

	inst := &StrategyInstance{
		ID:      "cd-test",
		OwnerID: 1,
		Config:  map[string]interface{}{"symbol_reentry_cooldown_minutes": float64(30)},
	}
	now := time.Now()
	// A entered 5 minutes ago, then B entered 1 minute ago (B is globally newest).
	db.Create(&models.StrategyOrder{StrategyID: inst.ID, OwnerID: 1, Symbol: "AAAUSDT", Side: "buy", Purpose: "entry", Status: "filled", ClientOrderID: "a1", RequestedAt: now.Add(-5 * time.Minute)})
	db.Create(&models.StrategyOrder{StrategyID: inst.ID, OwnerID: 1, Symbol: "BBBUSDT", Side: "buy", Purpose: "entry", Status: "filled", ClientOrderID: "b1", RequestedAt: now.Add(-1 * time.Minute)})

	stats := loadRecentEntryStats(inst, 30)
	if stats["AAAUSDT"].LastEntryAt.IsZero() {
		t.Fatalf("AAAUSDT LastEntryAt is zero — per-symbol cooldown tracking regressed (only newest row marked)")
	}
	if ok, remain, _ := canOpenSymbolByCooldown(inst, stats["AAAUSDT"]); ok {
		t.Fatalf("AAAUSDT re-entry allowed 5min into a 30min cooldown (bypass regressed)")
	} else if remain <= 0 || remain > 30*time.Minute {
		t.Fatalf("unreasonable remaining cooldown: %v", remain)
	}
	// B (the newest) must also be in cooldown — unchanged behavior.
	if ok, _, _ := canOpenSymbolByCooldown(inst, stats["BBBUSDT"]); ok {
		t.Fatalf("BBBUSDT re-entry allowed 1min into a 30min cooldown")
	}
	// A symbol with no entries stays allowed.
	if ok, _, _ := canOpenSymbolByCooldown(inst, stats["CCCUSDT"]); !ok {
		t.Fatalf("CCCUSDT (never entered) should not be in cooldown")
	}
}
