// Package equitydb persists balance observations into models.EquitySnapshot.
//
// It is a separate package on purpose, mirroring internal/marketmaker/markoutdb:
// internal/equity imports neither internal/database nor internal/models, and that
// stays true. internal/equity is emitted into from inside the quoting loop, so if
// it could reach the DB directly, a slow query would be one refactor away from
// sitting on the trading path. equitydb depends on equity, never the reverse.
package equitydb

import (
	"fmt"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/equity"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store writes equity.Snapshot into models.EquitySnapshot.
type Store struct{ db *gorm.DB }

// Install wires equity persistence up, and is a deliberate NO-OP unless the table
// already exists.
//
// equity_snapshots is NOT in database.go's AutoMigrate list, so the owner running
// scripts/equity_snapshots.sql IS the on-switch. Production migrations are the
// owner's call (台账 #8), and a feature that migrates itself on deploy takes that
// call away. Deploying this code without running the DDL changes nothing at all:
// no writes, no errors, no log spam — the reads keep working exactly as before.
//
// This matches markoutdb.Install; the two features deliberately share the
// convention rather than each inventing one.
//
// Returns true when the sink was installed.
func Install() bool {
	if database.DB == nil {
		return false
	}
	if !database.DB.Migrator().HasTable(&models.EquitySnapshot{}) {
		logger.Infof("[equity] equity_snapshots 表不存在,权益快照未启用 " +
			"(建表脚本: scripts/equity_snapshots.sql,建完重启即自动生效)")
		return false
	}
	equity.SetSink(&Store{db: database.DB}, 0)
	logger.Infof("[equity] 权益快照落库已启用")
	return true
}

// WriteEquitySnapshot inserts one immutable row. Called only from equity's sink
// goroutine — never from a quoting loop or an order path.
//
// ON CONFLICT DO NOTHING, not DO UPDATE: an observation, once taken, is history.
// Letting a later write overwrite an earlier one would reintroduce the mutable
// "current value" shape this table exists to avoid (台账 #42).
func (s *Store) WriteEquitySnapshot(r equity.Snapshot) error {
	// Refuse an unlabelled row outright. A balance with no venue is what caused
	// #18/#22 to triangulate two venues' money into one number; storing one here
	// would put that same trap into the table permanently.
	if r.Venue == "" || r.Asset == "" {
		return fmt.Errorf("权益快照缺少 venue/asset,拒绝落库")
	}
	if r.TakenAt.IsZero() {
		return fmt.Errorf("权益快照缺少 taken_at,拒绝落库")
	}
	row := models.EquitySnapshot{
		Venue:      r.Venue,
		Asset:      r.Asset,
		TakenAt:    r.TakenAt,
		Free:       r.Free,
		Total:      r.Total,
		Unrealized: r.Unrealized,
		Source:     r.Source,
		// Insert time, deliberately NOT copied from TakenAt: the gap between the
		// two is the sink queue's lag, and it is the first thing to look at when
		// rows are missing or arriving late.
		CreatedAt: time.Now(),
	}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}
