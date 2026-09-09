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

var adoptOpenTime = time.Date(2026, 9, 8, 10, 49, 35, 0, time.UTC)

// adoptFixture builds the one-symbol input the adoption loop consumes: an
// exchange net position nobody has claimed, plus the entry leg that opened it.
// Real numbers: the BTR/USDT short 611 @ 0.04901 the reconcile loop actually saw.
func adoptFixture() (map[string]exchange.Position, func() map[string][]models.StrategyOrder) {
	active := map[string]exchange.Position{
		"BTRUSDT": {
			Symbol:       "BTR/USDT",
			Direction:    "short",
			Amount:       611,
			Price:        0.04901,
			ExchangeName: "Binance",
			OpenTime:     adoptOpenTime,
		},
	}
	return active, entryOrders(models.StrategyOrder{
		StrategyID: "sid-1", StrategyName: "qt-breakout-follow-v2",
		OwnerID: 2, Symbol: "BTR/USDT", Side: "sell",
		Purpose: "entry", ExecutedQty: 611, RequestedAt: adoptOpenTime.Add(-time.Second),
	})
}

func entryOrders(ords ...models.StrategyOrder) func() map[string][]models.StrategyOrder {
	m := map[string][]models.StrategyOrder{}
	for _, o := range ords {
		m[exchange.NormalizeSymbol(o.Symbol)] = append(m[exchange.NormalizeSymbol(o.Symbol)], o)
	}
	return func() map[string][]models.StrategyOrder { return m }
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
	active, entries := adoptFixture()
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Name: "qt-breakout-follow-v2", Status: StatusStopped},
	}
	counted := map[string]struct{}{}
	countBy := map[string]int64{}

	adoptUnclaimedExchangePositions(2, time.Now(), active, entries, instLookup, counted, countBy)

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
	active, entries := adoptFixture()
	counted := map[string]struct{}{}
	countBy := map[string]int64{}

	adoptUnclaimedExchangePositions(2, time.Now(), active, entries, map[string]*StrategyInstance{}, counted, countBy)

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("unknown strategy adopted %d position row(s), want 0", n)
	}
}

// Acceptance criterion 2: adoption by the live strategy that opened it is
// unchanged, and the row is born tagged pnl_source=unknown.
func TestAdoptRunningInstanceStillCreatesRow(t *testing.T) {
	db := newAdoptTestDB(t)
	active, entries := adoptFixture()
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Name: "qt-breakout-follow-v2", Status: StatusRunning},
	}
	counted := map[string]struct{}{}
	countBy := map[string]int64{}
	now := time.Now()

	adoptUnclaimedExchangePositions(2, now, active, entries, instLookup, counted, countBy)

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
	if got.PnLSource != "unknown" {
		t.Errorf("adopted row pnl_source = %q, want \"unknown\"", got.PnLSource)
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
	active, entries := adoptFixture()
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Status: StatusStarting},
	}

	adoptUnclaimedExchangePositions(2, time.Now(), active, entries, instLookup, map[string]struct{}{}, map[string]int64{})

	if n := countPositionRows(t, db); n != 1 {
		t.Fatalf("starting strategy created %d rows, want 1", n)
	}
}

// A symbol already claimed by an open DB row is skipped before the guard runs,
// so a stopped strategy still holding a real position keeps its existing row.
func TestAdoptSkipsAlreadyCountedSymbol(t *testing.T) {
	db := newAdoptTestDB(t)
	active, entries := adoptFixture()
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Status: StatusRunning},
	}
	counted := map[string]struct{}{"BTRUSDT": {}}

	adoptUnclaimedExchangePositions(2, time.Now(), active, entries, instLookup, counted, map[string]int64{})

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("already-claimed symbol created %d rows, want 0", n)
	}
}

func TestAdoptSkipsOrderWithoutStrategyID(t *testing.T) {
	db := newAdoptTestDB(t)
	active, _ := adoptFixture()
	entries := entryOrders(models.StrategyOrder{
		StrategyID: "  ", OwnerID: 2, Symbol: "BTR/USDT", Side: "sell",
		Purpose: "entry", ExecutedQty: 611, RequestedAt: adoptOpenTime.Add(-time.Second),
	})

	adoptUnclaimedExchangePositions(2, time.Now(), active, entries, map[string]*StrategyInstance{}, map[string]struct{}{}, map[string]int64{})

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("untagged order created %d rows, want 0", n)
	}
}

// #31 core: the closing side must not inherit the position. The only order on
// the symbol is a BUY (which closes a short); no entry leg opened it, so nobody
// adopts — even though the strategy is running.
func TestAdoptDeclinesWhenOnlyClosingSideIsKnown(t *testing.T) {
	db := newAdoptTestDB(t)
	active, _ := adoptFixture()
	// A buy leg on a short position is a close, not an open.
	entries := entryOrders(models.StrategyOrder{
		StrategyID: "sid-closer", StrategyName: "qt-breakout-follow-v2", OwnerID: 2,
		Symbol: "BTR/USDT", Side: "buy", Purpose: "entry", ExecutedQty: 611,
		RequestedAt: adoptOpenTime.Add(time.Minute),
	})
	instLookup := map[string]*StrategyInstance{
		"sid-closer": {ID: "sid-closer", Status: StatusRunning},
	}

	adoptUnclaimedExchangePositions(2, time.Now(), active, entries, instLookup, map[string]struct{}{}, map[string]int64{})

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("closing side adopted %d rows, want 0", n)
	}
}

// Shared exchange account: the opener belongs to another app owner. This owner's
// live strategy must not mint a row for it — the measured case is 48 of the 49
// resolvable ghost rows, all opened by owner1/Meme.
func TestAdoptDeclinesWhenOpenerBelongsToAnotherOwner(t *testing.T) {
	db := newAdoptTestDB(t)
	active, _ := adoptFixture()
	entries := entryOrders(models.StrategyOrder{
		StrategyID: "sid-meme", StrategyName: "Meme", OwnerID: 1,
		Symbol: "BTR/USDT", Side: "sell", Purpose: "entry", ExecutedQty: 611,
		RequestedAt: adoptOpenTime.Add(-time.Second),
	})
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Status: StatusRunning},
	}

	adoptUnclaimedExchangePositions(2, time.Now(), active, entries, instLookup, map[string]struct{}{}, map[string]int64{})

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("cross-owner opener adopted %d rows under owner 2, want 0", n)
	}
}

// ...and the opener's own owner does adopt it on its turn round the loop.
func TestAdoptCreditsTheOpeningOwner(t *testing.T) {
	db := newAdoptTestDB(t)
	active, _ := adoptFixture()
	entries := entryOrders(models.StrategyOrder{
		StrategyID: "sid-meme", StrategyName: "Meme", OwnerID: 1,
		Symbol: "BTR/USDT", Side: "sell", Purpose: "entry", ExecutedQty: 611,
		RequestedAt: adoptOpenTime.Add(-time.Second),
	})
	instLookup := map[string]*StrategyInstance{
		"sid-meme": {ID: "sid-meme", Name: "Meme", Status: StatusRunning},
	}

	adoptUnclaimedExchangePositions(1, time.Now(), active, entries, instLookup, map[string]struct{}{}, map[string]int64{})

	var rows []models.StrategyPosition
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	if len(rows) != 1 || rows[0].StrategyID != "sid-meme" || rows[0].OwnerID != 1 {
		t.Fatalf("adopted rows = %+v, want one row for sid-meme/owner1", rows)
	}
}

// Two strategies opened the same direction into one netted position: the split
// does not exist, so neither may claim it.
func TestAdoptDeclinesWhenTwoStrategiesOpened(t *testing.T) {
	db := newAdoptTestDB(t)
	active, _ := adoptFixture()
	entries := entryOrders(
		models.StrategyOrder{StrategyID: "sid-1", OwnerID: 2, Symbol: "BTR/USDT", Side: "sell",
			Purpose: "entry", ExecutedQty: 300, RequestedAt: adoptOpenTime.Add(-2 * time.Minute)},
		models.StrategyOrder{StrategyID: "sid-2", OwnerID: 2, Symbol: "BTR/USDT", Side: "sell",
			Purpose: "entry", ExecutedQty: 311, RequestedAt: adoptOpenTime.Add(-time.Minute)},
	)
	instLookup := map[string]*StrategyInstance{
		"sid-1": {ID: "sid-1", Status: StatusRunning},
		"sid-2": {ID: "sid-2", Status: StatusRunning},
	}

	adoptUnclaimedExchangePositions(2, time.Now(), active, entries, instLookup, map[string]struct{}{}, map[string]int64{})

	if n := countPositionRows(t, db); n != 0 {
		t.Fatalf("ambiguous opener adopted %d rows, want 0", n)
	}
}

func TestPositionOpener(t *testing.T) {
	long := models.StrategyOrder{StrategyID: "a", OwnerID: 1, Side: "buy", RequestedAt: adoptOpenTime}
	short := models.StrategyOrder{StrategyID: "b", OwnerID: 1, Side: "sell", RequestedAt: adoptOpenTime}
	stale := models.StrategyOrder{StrategyID: "c", OwnerID: 1, Side: "sell", RequestedAt: adoptOpenTime.Add(-48 * time.Hour)}

	cases := []struct {
		name    string
		entries []models.StrategyOrder
		dir     string
		open    time.Time
		wantID  string
		wantOK  bool
	}{
		{"short takes the sell leg", []models.StrategyOrder{long, short}, "short", adoptOpenTime, "b", true},
		{"long takes the buy leg", []models.StrategyOrder{long, short}, "long", adoptOpenTime, "a", true},
		{"no matching side", []models.StrategyOrder{long}, "short", adoptOpenTime, "", false},
		{"no entries at all", nil, "long", adoptOpenTime, "", false},
		{"outside the lookback window", []models.StrategyOrder{stale}, "short", adoptOpenTime, "", false},
		// An unknown open time gives the window nothing to anchor on, so the
		// answer is "cannot tell" rather than "the most recent one".
		{"zero open time refuses", []models.StrategyOrder{short}, "short", time.Time{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PositionOpener(tc.entries, tc.dir, tc.open)
			if ok != tc.wantOK || got.StrategyID != tc.wantID {
				t.Errorf("PositionOpener = (%q, %v), want (%q, %v)", got.StrategyID, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}
