package strategy

import (
	"strings"
	"testing"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newParamVersionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.StrategyInstance{}, &models.StrategyParamVersion{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prev })
	return db
}

func TestEnsureParamVersionBootstrapIsIdempotentAndBackdated(t *testing.T) {
	db := newParamVersionTestDB(t)

	created := time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local)
	inst := models.StrategyInstance{
		ID: "sid-1", Name: "趋势A", OwnerID: 7,
		Config:    `{"stop_loss_pct":0.015,"take_profit_pct":0.03}`,
		CreatedAt: created,
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	v1, err := EnsureParamVersion("sid-1", "bootstrap", "")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if v1 == nil || v1.Label != "v1" || v1.Seq != 1 || !v1.IsCurrent {
		t.Fatalf("v1 = %+v, want label v1 seq 1 current", v1)
	}
	// Backdating is what lets every pre-existing trade land in v1 instead of
	// falling into the "unversioned" bucket.
	if !v1.EffectiveFrom.Equal(created) {
		t.Errorf("effective_from = %v, want instance created_at %v", v1.EffectiveFrom, created)
	}

	// Same config -> no new row, no window churn.
	again, err := EnsureParamVersion("sid-1", "put_config", "laoxu")
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if again.ID != v1.ID {
		t.Fatalf("unchanged config opened a new version %d (was %d)", again.ID, v1.ID)
	}
	var n int64
	db.Model(&models.StrategyParamVersion{}).Where("strategy_id = ?", "sid-1").Count(&n)
	if n != 1 {
		t.Fatalf("version rows = %d, want 1", n)
	}
}

func TestEnsureParamVersionOpensNewVersionAndClosesPrevious(t *testing.T) {
	db := newParamVersionTestDB(t)

	inst := models.StrategyInstance{
		ID: "sid-1", Name: "趋势A", OwnerID: 7,
		Config:    `{"stop_loss_pct":0.015}`,
		CreatedAt: time.Now().Add(-24 * time.Hour),
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	v1, err := EnsureParamVersion("sid-1", "bootstrap", "")
	if err != nil {
		t.Fatalf("ensure v1: %v", err)
	}

	if err := db.Model(&models.StrategyInstance{}).Where("id = ?", "sid-1").
		Update("config", `{"stop_loss_pct":0.025}`).Error; err != nil {
		t.Fatalf("update config: %v", err)
	}
	v2, err := EnsureParamVersion("sid-1", "patch_config", "laoxu")
	if err != nil {
		t.Fatalf("ensure v2: %v", err)
	}
	if v2.ID == v1.ID || v2.Seq != 2 || v2.Label != "v2" || v2.Actor != "laoxu" {
		t.Fatalf("v2 = %+v, want a distinct seq-2 row authored by laoxu", v2)
	}
	if v2.ChangedJSON == `{}` || v2.ChangedJSON == "" {
		t.Errorf("v2.ChangedJSON = %q, want the stop_loss_pct diff", v2.ChangedJSON)
	}

	var reloaded models.StrategyParamVersion
	if err := db.Where("id = ?", v1.ID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload v1: %v", err)
	}
	if reloaded.IsCurrent {
		t.Errorf("v1 still marked current after v2 opened")
	}
	// Windows must abut exactly, or a trade opened in the seam is attributed to
	// neither version and silently disappears from the report.
	if reloaded.EffectiveTo == nil || !reloaded.EffectiveTo.Equal(v2.EffectiveFrom) {
		t.Errorf("v1.effective_to = %v, want == v2.effective_from %v", reloaded.EffectiveTo, v2.EffectiveFrom)
	}

	if got := currentParamVersionID("sid-1"); got == nil || *got != v2.ID {
		t.Errorf("currentParamVersionID = %v, want %d", got, v2.ID)
	}
}

// The API key is a secret and is not a trading parameter — rotating it must not
// split a parameter set into two buckets.
func TestEnsureParamVersionIgnoresSecretAndKeyOrder(t *testing.T) {
	db := newParamVersionTestDB(t)

	inst := models.StrategyInstance{
		ID: "sid-1", OwnerID: 7,
		Config:    `{"auto_optimize_api_key":"sk-old","stop_loss_pct":0.015}`,
		CreatedAt: time.Now(),
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	v1, err := EnsureParamVersion("sid-1", "bootstrap", "")
	if err != nil {
		t.Fatalf("ensure v1: %v", err)
	}
	if v1.ConfigJSON == "" || strings.Contains(v1.ConfigJSON, "sk-old") {
		t.Fatalf("secret leaked into the version row: %s", v1.ConfigJSON)
	}

	// New secret, different key order, same parameters.
	if err := db.Model(&models.StrategyInstance{}).Where("id = ?", "sid-1").
		Update("config", `{"stop_loss_pct":0.015,"auto_optimize_api_key":"sk-new"}`).Error; err != nil {
		t.Fatalf("update config: %v", err)
	}
	v2, err := EnsureParamVersion("sid-1", "put_config", "laoxu")
	if err != nil {
		t.Fatalf("ensure v2: %v", err)
	}
	if v2.ID != v1.ID {
		t.Fatalf("secret rotation / key reorder opened a spurious version %d", v2.ID)
	}
}

// A config write must NOT backdate the first version to the instance's creation
// time: the parameters in force before that write were different, so backdating
// would attribute every older trade to params it never traded under. Those
// trades belong in the honest "unversioned" bucket instead.
func TestEnsureParamVersionDoesNotBackdateWhenConfigJustChanged(t *testing.T) {
	db := newParamVersionTestDB(t)

	created := time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local)
	inst := models.StrategyInstance{
		ID: "sid-1", OwnerID: 7,
		Config:    `{"stop_loss_pct":0.025}`,
		CreatedAt: created,
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	v1, err := EnsureParamVersion("sid-1", "patch_config", "laoxu")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if v1.EffectiveFrom.Equal(created) || v1.EffectiveFrom.Before(created.Add(time.Hour)) {
		t.Fatalf("effective_from = %v: a config write backdated the window to the instance's creation", v1.EffectiveFrom)
	}
	if v1.Source != "patch_config" || v1.Actor != "laoxu" {
		t.Errorf("v1 = source %q actor %q, want the caller's values preserved", v1.Source, v1.Actor)
	}
}
