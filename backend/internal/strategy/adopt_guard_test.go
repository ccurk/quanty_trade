package strategy

import (
	"testing"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAdoptTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.StrategyPosition{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prev })
	return db
}

// adoptFixture builds the one-symbol input the adoption loop consumes: an
// exchange net position nobody has claimed, plus the last order on that symbol.
func adoptFixture() (map[string]exchange.Position, map[string]models.StrategyOrder) {
	active := map[string]exchange.Position{
		"BTRUSDT": {
			Symbol:       "BTR/USDT",
			Direction:    "short",
			Amount:       611,
			Price:        0.04901,
			ExchangeName: "Binance",
			OpenTime:     time.Date(2026, 9, 8, 10, 49, 35, 0, time.UTC),
		},
	}
	orders := map[string]models.StrategyOrder{
		"BTRUSDT": {StrategyID: "sid-1", StrategyName: "qt-breakout-follow-v2", Symbol: "BTR/USDT"},
	}
	return active, orders
}

func countPositionRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&models.StrategyPosition{}).Count(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func TestMayAdoptUnclaimedPositionOnlyForLiveInstances(t *testing.T) {
	cases := []struct {
		name string
		inst *StrategyInstance
		want bool
	}{
		{"running adopts", &StrategyInstance{ID: "sid-1", Status: StatusRunning}, true},
		// starting counts as live: the python process can already be placing
		// orders before the status flips to running.
		{"starting adopts", &StrategyInstance{ID: "sid-1", Status: StatusStarting}, true},
		{"stopped does not adopt", &StrategyInstance{ID: "sid-1", Status: StatusStopped}, false},
		{"error does not adopt", &StrategyInstance{ID: "sid-1", Status: StatusError}, false},
		// nil = removed from the in-memory registry (RemoveStrategy), which is
		// how the deleted qt-breakout-follow instance looks today.
		{"missing instance does not adopt", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mayAdoptUnclaimedPosition(tc.inst); got != tc.want {
				t.Errorf("mayAdoptUnclaimedPosition = %v, want %v", got, tc.want)
			}
		})
	}
}

// Acceptance criterion 1: a stopped instance plus a live exchange net position
// on a symbol it last traded must produce no strategy_positions row.
func TestAdoptSkipsStoppedInstance(t *testing.T) {
	db := newAdoptTestDB(t)
	active, orders := adoptFixture()
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Name: "qt-breakout-follow-v2", Status: StatusStopped},
	}
	counted := map[string]struct{}{}
	countBy := map[string]int64{}

	adoptUnclaimedExchangePositions(2, time.Now(), active, orders, instLookup, counted, countBy)

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("stopped strategy adopted %d position row(s), want 0", n)
	}
	if countBy["sid-1"] != 0 {
		t.Errorf("open count for stopped strategy = %d, want 0", countBy["sid-1"])
	}
	// The symbol stays unclaimed, so a live strategy can still take it later.
	if _, ok := counted["BTRUSDT"]; ok {
		t.Error("declined symbol must not be marked as counted")
	}
}

func TestAdoptSkipsInstanceMissingFromRegistry(t *testing.T) {
	db := newAdoptTestDB(t)
	active, orders := adoptFixture()
	counted := map[string]struct{}{}
	countBy := map[string]int64{}

	adoptUnclaimedExchangePositions(2, time.Now(), active, orders, map[string]*StrategyInstance{}, counted, countBy)

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("unknown strategy adopted %d position row(s), want 0", n)
	}
}

// Acceptance criterion 2: adoption by a live strategy is unchanged.
func TestAdoptRunningInstanceStillCreatesRow(t *testing.T) {
	db := newAdoptTestDB(t)
	active, orders := adoptFixture()
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Name: "qt-breakout-follow-v2", Status: StatusRunning},
	}
	counted := map[string]struct{}{}
	countBy := map[string]int64{}
	now := time.Now()

	adoptUnclaimedExchangePositions(2, now, active, orders, instLookup, counted, countBy)

	var rows []models.StrategyPosition
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("running strategy created %d rows, want 1", len(rows))
	}
	got := rows[0]
	if got.StrategyID != "sid-1" || got.OwnerID != 2 || got.Symbol != "BTR/USDT" ||
		got.Direction != "short" || got.Amount != 611 || got.Status != "open" {
		t.Errorf("adopted row = %+v, want sid-1/owner2/BTR-USDT/short/611/open", got)
	}
	if countBy["sid-1"] != 1 {
		t.Errorf("open count = %d, want 1", countBy["sid-1"])
	}
	if _, ok := counted["BTRUSDT"]; !ok {
		t.Error("adopted symbol must be marked as counted")
	}
}

func TestAdoptStartingInstanceStillCreatesRow(t *testing.T) {
	db := newAdoptTestDB(t)
	active, orders := adoptFixture()
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Status: StatusStarting},
	}

	adoptUnclaimedExchangePositions(2, time.Now(), active, orders, instLookup, map[string]struct{}{}, map[string]int64{})

	if n := countPositionRows(t, db); n != 1 {
		t.Fatalf("starting strategy created %d rows, want 1", n)
	}
}

// A symbol already claimed by an open DB row is skipped before the guard runs,
// so a stopped strategy still holding a real position keeps its existing row.
func TestAdoptSkipsAlreadyCountedSymbol(t *testing.T) {
	db := newAdoptTestDB(t)
	active, orders := adoptFixture()
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Status: StatusRunning},
	}
	counted := map[string]struct{}{"BTRUSDT": {}}

	adoptUnclaimedExchangePositions(2, time.Now(), active, orders, instLookup, counted, map[string]int64{})

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("already-claimed symbol created %d rows, want 0", n)
	}
}

func TestAdoptSkipsOrderWithoutStrategyID(t *testing.T) {
	db := newAdoptTestDB(t)
	active, _ := adoptFixture()
	orders := map[string]models.StrategyOrder{
		"BTRUSDT": {StrategyID: "  ", Symbol: "BTR/USDT"},
	}

	adoptUnclaimedExchangePositions(2, time.Now(), active, orders, map[string]*StrategyInstance{}, map[string]struct{}{}, map[string]int64{})

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("untagged order created %d rows, want 0", n)
	}
}
