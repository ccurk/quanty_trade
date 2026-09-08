// Package markoutdb persists marketmaker markout measurements.
//
// It exists as a separate package on purpose: internal/marketmaker imports
// NEITHER internal/database NOR internal/models, and that stays true. The trading
// engine's measurement code must not be able to reach the DB directly — if it
// could, a slow query would be one refactor away from sitting inside the quoting
// loop's mutex. markoutdb depends on marketmaker, never the other way round.
package markoutdb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/marketmaker"
	"quanty_trade/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store writes marketmaker.MarkoutRecord into models.MarkoutFill.
type Store struct {
	db *gorm.DB
	// params maps "exchange|symbol" to the parameter version live for that pair.
	// Built once at Install and never mutated afterwards, so WriteMarkout (which
	// runs on the sink goroutine) reads it without a lock.
	params map[string]paramRef
}

type paramRef struct {
	hash string
	id   *uint
}

// Install wires markout persistence up, and is a deliberate NO-OP unless the
// table already exists.
//
// The table is NOT in database.go's AutoMigrate list, so the owner running
// scripts/markout_persistence.sql IS the on-switch. That is the point: production
// migrations are the owner's call (ledger #8), and a feature that migrates itself
// on deploy takes that call away. Deploying this code with no DDL run changes
// nothing at all — no writes, no errors, no log spam.
//
// Returns true when the sink was installed.
func Install(cfg marketmaker.Config) bool {
	if database.DB == nil {
		return false
	}
	if !database.DB.Migrator().HasTable(&models.MarkoutFill{}) {
		logger.Infof("[mm-markout] markout_fills 表不存在,持久化未启用 " +
			"(建表脚本: scripts/markout_persistence.sql,建完重启即自动生效)")
		return false
	}
	s := &Store{db: database.DB, params: map[string]paramRef{}}
	for _, p := range cfg.Pairs {
		s.params[key(p.Exec, p.ExecSymbol)] = ensureParamVersion(database.DB, cfg, p)
	}
	marketmaker.SetMarkoutSink(s, 0)
	logger.Infof("[mm-markout] 持久化已启用,%d 个 pair 的参数版本已登记", len(s.params))
	return true
}

func key(exchange, symbol string) string { return exchange + "|" + symbol }

// WriteMarkout inserts one immutable row. Called only from marketmaker's sink
// goroutine — never from the quoting loop.
//
// ON CONFLICT DO NOTHING, not DO UPDATE: the engine re-polls the last 100 fills
// every ~10s, and a measurement, once taken, is history. Letting a later write
// overwrite an earlier one would reintroduce exactly the mutable-"current value"
// shape this table was designed to avoid.
func (s *Store) WriteMarkout(r marketmaker.MarkoutRecord) error {
	if r.FillID == "" || r.Symbol == "" {
		return fmt.Errorf("markout 记录缺少 fill_id/symbol,拒绝落库")
	}
	// Refuse to store a row whose derived bps disagrees with its own raw inputs.
	// Such a row would be worse than a missing one: it looks checkable and isn't.
	if err := r.Verify(1e-6); err != nil {
		return err
	}
	pref := s.params[key(r.Exchange, r.Symbol)]
	row := models.MarkoutFill{
		Exchange: r.Exchange, Symbol: r.Symbol, FillID: r.FillID,
		Side: r.Side, FillPx: r.FillPx, Amount: r.Amount, FeeBps: r.FeeBps,
		FillTs:         r.FillTs,
		MidAtFill:      r.MidAtFill,
		MidAtFillTs:    r.MidAtFillTs,
		MidAtFillLagMs: r.MidAtFillLagMs,
		Complete:       r.Complete,
		ParamHash:      pref.hash, ParamVersionID: pref.id,
		ResolvedAt: r.ResolvedAt,
		CreatedAt:  time.Now(),
	}
	for _, p := range r.Points {
		// A stale point keeps its evidence but writes NULL into markout_bps_*.
		// Storing the number with only a flag beside it would leave
		// AVG(markout_bps_5s) — the query everyone writes first — quietly wrong;
		// dropping the point entirely would make "how many did we discard"
		// uncountable. NULL + stale_* + lag_*_ms gives both.
		var bps *float64
		if !p.Stale {
			v := p.Bps
			bps = &v
			row.HorizonsDone++
		}
		switch p.Horizon {
		case "1s":
			row.Mid1s, row.MarkoutBps1s, row.Lag1sMs, row.Stale1s = p.Mid, bps, p.LagMs, p.Stale
		case "5s":
			row.Mid5s, row.MarkoutBps5s, row.Lag5sMs, row.Stale5s = p.Mid, bps, p.LagMs, p.Stale
		case "30s":
			row.Mid30s, row.MarkoutBps30s, row.Lag30sMs, row.Stale30s = p.Mid, bps, p.LagMs, p.Stale
		}
	}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// paramVersionStrategyID is the synthetic strategy id a market-making pair is
// registered under in strategy_param_versions, e.g. "mm:gate:ONG_USDT".
//
// Reusing that table rather than adding a second one is deliberate: it already
// has the append-only shape wanted here (dedup by config hash, [effective_from,
// effective_to) windows, monotonic seq/label), and it is what the attribution UI
// already knows how to read. The "mm:" prefix keeps these rows out of every
// strategy query, all of which scope by a real StrategyInstance id.
//
// What does NOT work: strategy.EnsureParamVersion, which reads config back off a
// StrategyInstance row. The market maker has no such row — it is configured by a
// JSON file behind $MARKETMAKER_CONFIG — so the registration is done here.
func paramVersionStrategyID(exchange, symbol string) string {
	return "mm:" + exchange + ":" + symbol
}

// canonicalPairParams renders the SECRET-FREE parameters that govern one pair.
//
// Secrets: ExecConfig (api_key/api_secret/passphrase) is never touched here, only
// PairConfig plus two global switches that change what the quotes mean. Hashing a
// credential into a durable table would be a slow leak with no upside.
//
// encoding/json sorts map keys, so the same parameter set always hashes the same.
func canonicalPairParams(cfg marketmaker.Config, p marketmaker.PairConfig) (string, string) {
	m := map[string]interface{}{
		"feed":              cfg.Feed,
		"observe_only":      cfg.ObserveOnly,
		"exec":              p.Exec,
		"exec_symbol":       p.ExecSymbol,
		"feed_symbol":       p.FeedSymbol,
		"spread_bps":        p.SpreadBps,
		"order_qty":         p.OrderQty,
		"max_position":      p.MaxPosition,
		"refresh_ms":        p.RefreshMs,
		"quote_anchor":      p.QuoteAnchor,
		"basis_half_life_s": p.BasisHalfLifeS,
		"basis_cap_bps":     p.BasisCapBps,
	}
	b, err := json.Marshal(m)
	if err != nil { // map[string]interface{} of scalars cannot fail; be explicit anyway
		return "", ""
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:])
}

// ensureParamVersion returns the parameter version live for this pair, appending
// a new one when the params changed since last boot.
//
// Failure is non-fatal by design: a missing version stamp degrades the row to its
// ParamHash (which is computed locally and always present), and losing an
// attribution row must never stop a measurement — let alone a trade.
func ensureParamVersion(db *gorm.DB, cfg marketmaker.Config, p marketmaker.PairConfig) paramRef {
	canonical, hash := canonicalPairParams(cfg, p)
	if hash == "" {
		return paramRef{}
	}
	sid := paramVersionStrategyID(p.Exec, p.ExecSymbol)

	var current models.StrategyParamVersion
	err := db.Where("strategy_id = ? AND is_current = ?", sid, true).
		Order("effective_from desc, id desc").First(&current).Error
	switch {
	case err == nil && current.ConfigHash == hash:
		id := current.ID
		return paramRef{hash: hash, id: &id} // unchanged params, no write
	case err != nil && err != gorm.ErrRecordNotFound:
		logger.Errorf("[mm-markout] 读参数版本失败 %s: %v", sid, err)
		return paramRef{hash: hash}
	}

	now := time.Now()
	prevSeq := 0
	if err == nil {
		prevSeq = current.Seq
	} else {
		var last models.StrategyParamVersion
		if db.Where("strategy_id = ?", sid).Order("seq desc, id desc").
			First(&last).Error == nil {
			prevSeq = last.Seq
		}
	}
	next := models.StrategyParamVersion{
		StrategyID:    sid,
		StrategyName:  "marketmaker:" + p.ExecSymbol,
		Seq:           prevSeq + 1,
		Label:         fmt.Sprintf("v%d", prevSeq+1),
		ConfigHash:    hash,
		ConfigJSON:    canonical,
		Source:        "marketmaker",
		EffectiveFrom: now,
		IsCurrent:     true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	txErr := db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(&models.StrategyParamVersion{}).
			Where("strategy_id = ? AND is_current = ?", sid, true).
			Updates(map[string]interface{}{
				"is_current":   false,
				"effective_to": now,
				"updated_at":   now,
			}).Error; e != nil {
			return e
		}
		return tx.Create(&next).Error
	})
	if txErr != nil {
		logger.Errorf("[mm-markout] 登记参数版本失败 %s: %v", sid, txErr)
		return paramRef{hash: hash}
	}
	logger.Infof("[mm-markout] 参数版本 %s %s hash=%s", sid, next.Label, hash[:8])
	id := next.ID
	return paramRef{hash: hash, id: &id}
}
