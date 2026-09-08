package strategy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"

	"gorm.io/gorm"
)

// paramVersionIgnoredKeys are config keys that must NOT open a new parameter
// version. They either carry a secret (never persist it a second time) or churn
// without changing trading behaviour — letting them cut a version would shatter
// one real parameter set into many tiny buckets with no statistical power.
var paramVersionIgnoredKeys = map[string]bool{
	"auto_optimize_api_key": true,
}

// canonicalParamConfig returns (canonical JSON, sha256 hex) for a config blob.
// encoding/json marshals map keys in sorted order, so the same parameter set
// always hashes the same regardless of the key order the caller sent.
func canonicalParamConfig(raw string) (string, string, error) {
	cfg := map[string]interface{}{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return "", "", err
		}
	}
	for k := range paramVersionIgnoredKeys {
		delete(cfg, k)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}

// diffParamConfig lists the keys whose value differs between two canonical
// configs, as {"key": {"from": ..., "to": ...}}.
func diffParamConfig(oldJSON, newJSON string) string {
	var oldCfg, newCfg map[string]interface{}
	_ = json.Unmarshal([]byte(oldJSON), &oldCfg)
	_ = json.Unmarshal([]byte(newJSON), &newCfg)
	changed := map[string]interface{}{}
	for k, nv := range newCfg {
		ov, had := oldCfg[k]
		if !had || !jsonValueEqual(ov, nv) {
			changed[k] = map[string]interface{}{"from": ov, "to": nv}
		}
	}
	for k, ov := range oldCfg {
		if _, still := newCfg[k]; !still {
			changed[k] = map[string]interface{}{"from": ov, "to": nil}
		}
	}
	b, err := json.Marshal(changed)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func jsonValueEqual(a, b interface{}) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// EnsureParamVersion records the strategy's current parameter set as a version
// and returns the row that is live from now on.
//
// It is idempotent by config hash: calling it repeatedly with an unchanged
// config returns the existing current row and writes nothing. Only a genuine
// change closes the previous window (effective_to = now) and appends a new row.
//
// source is "bootstrap" | "put_config" | "patch_config"; actor is the username
// that made the change ("" for bootstrap).
//
// Failure is non-fatal by design — the caller has already persisted the config,
// and losing an attribution row must never fail a config write or an order.
func EnsureParamVersion(strategyID, source, actor string) (*models.StrategyParamVersion, error) {
	if database.DB == nil || strings.TrimSpace(strategyID) == "" {
		return nil, nil
	}
	// Read the instance back rather than taking it as an argument: every caller
	// has just written Config, and re-reading the single row keeps one source of
	// truth for what "current params" means at every call site.
	var inst models.StrategyInstance
	if err := database.DB.Where("id = ?", strategyID).First(&inst).Error; err != nil {
		return nil, err
	}
	canonical, hash, err := canonicalParamConfig(inst.Config)
	if err != nil {
		return nil, err
	}

	var current models.StrategyParamVersion
	err = database.DB.Where("strategy_id = ? AND is_current = ?", inst.ID, true).
		Order("effective_from desc, id desc").First(&current).Error
	switch {
	case err == nil && current.ConfigHash == hash:
		return &current, nil
	case err != nil && err != gorm.ErrRecordNotFound:
		return nil, err
	}

	now := time.Now()
	effectiveFrom := now
	prevSeq := 0
	prevCanonical := ""
	if err == gorm.ErrRecordNotFound {
		// No live version. Seq still continues past any already-closed rows.
		var last models.StrategyParamVersion
		hasHistory := database.DB.Where("strategy_id = ?", inst.ID).
			Order("seq desc, id desc").First(&last).Error == nil
		switch {
		case hasHistory:
			prevSeq = last.Seq
			prevCanonical = last.ConfigJSON
		case source == "bootstrap" && !inst.CreatedAt.IsZero():
			// First sight of a strategy that predates this table, with nothing
			// changed just now: backdate to the instance's creation so its whole
			// history lands in v1 instead of reading as "unversioned".
			//
			// This backdating is deliberately NOT done when a config write
			// triggered the first row: the params in force before that write were
			// by definition different, and pretending otherwise would attribute
			// every older trade to parameters it never traded under. Those trades
			// stay "unversioned" — an honest gap beats a confident wrong number.
			effectiveFrom = inst.CreatedAt
		}
	} else {
		prevSeq = current.Seq
		prevCanonical = current.ConfigJSON
	}

	next := models.StrategyParamVersion{
		StrategyID:    inst.ID,
		StrategyName:  inst.Name,
		OwnerID:       inst.OwnerID,
		Seq:           prevSeq + 1,
		ConfigHash:    hash,
		ConfigJSON:    canonical,
		ChangedJSON:   diffParamConfig(prevCanonical, canonical),
		Source:        source,
		Actor:         actor,
		EffectiveFrom: effectiveFrom,
		IsCurrent:     true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	next.Label = defaultParamVersionLabel(next.Seq)

	txErr := database.DB.Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(&models.StrategyParamVersion{}).
			Where("strategy_id = ? AND is_current = ?", inst.ID, true).
			Updates(map[string]interface{}{
				"is_current":   false,
				"effective_to": next.EffectiveFrom,
				"updated_at":   now,
			}).Error; e != nil {
			return e
		}
		return tx.Create(&next).Error
	})
	if txErr != nil {
		return nil, txErr
	}
	logger.Infof("参数版本: strategy=%s %s hash=%s source=%s changed=%s",
		inst.ID, next.Label, hash[:8], source, next.ChangedJSON)
	return &next, nil
}

// currentParamVersionID returns the id of the parameter version live right now
// for this strategy, for stamping onto a new order/position row. It returns nil
// (leave the column NULL) whenever the answer is not known — a missing stamp
// degrades to window matching at query time, a WRONG stamp would not.
func currentParamVersionID(strategyID string) *uint {
	if database.DB == nil || strings.TrimSpace(strategyID) == "" {
		return nil
	}
	var row models.StrategyParamVersion
	if err := database.DB.Select("id").
		Where("strategy_id = ? AND is_current = ?", strategyID, true).
		Order("effective_from desc, id desc").First(&row).Error; err != nil {
		return nil
	}
	id := row.ID
	return &id
}

func defaultParamVersionLabel(seq int) string {
	return "v" + strconv.Itoa(seq)
}
