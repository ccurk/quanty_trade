package selector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"quanty_trade/internal/strategy"
)

type Manager struct {
	stratMgr *strategy.Manager
	mu       sync.Mutex
	running  bool
}

func NewManager(stratMgr *strategy.Manager) *Manager {
	return &Manager{stratMgr: stratMgr}
}

func (m *Manager) Start() {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()

	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			_ = m.ReconcileAll()
		}
	}()
}

func (m *Manager) ReconcileAll() error {
	var selectors []models.StrategySelector
	_ = database.DB.Where("status = ?", "running").Find(&selectors).Error
	for _, sel := range selectors {
		_ = m.Reconcile(sel.ID)
	}
	return nil
}

func (m *Manager) Reconcile(selectorID string) error {
	var sel models.StrategySelector
	if err := database.DB.Where("id = ?", selectorID).First(&sel).Error; err != nil {
		return err
	}
	if sel.Status != "running" {
		return nil
	}

	cfg := map[string]interface{}{}
	_ = json.Unmarshal([]byte(sel.Config), &cfg)

	bx, ok := m.stratMgr.GetExchange().(*exchange.BinanceExchange)
	if !ok {
		return fmt.Errorf("exchange does not support selector")
	}

	quote, _ := cfg["selector_quote"].(string)
	minPrice, _ := cfg["selector_min_price"].(float64)
	maxPrice, _ := cfg["selector_max_price"].(float64)
	minVol, _ := cfg["selector_min_quote_volume_24h"].(float64)
	maxSymbols := 5
	if v, ok := cfg["selector_max_symbols"].(float64); ok && int(v) > 0 {
		maxSymbols = int(v)
	}
	excludeStable := true
	if raw, ok := cfg["selector_exclude_stable"]; ok {
		if v, ok := raw.(bool); ok {
			excludeStable = v
		} else if v, ok := raw.(float64); ok {
			excludeStable = v != 0
		}
	}

	var baseAssets []string
	if raw, ok := cfg["selector_base_assets"]; ok {
		if xs, ok := raw.([]interface{}); ok {
			for _, it := range xs {
				if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
					baseAssets = append(baseAssets, s)
				}
			}
		} else if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
			for _, p := range strings.Split(s, ",") {
				if v := strings.TrimSpace(p); v != "" {
					baseAssets = append(baseAssets, v)
				}
			}
		}
	}

	var desired []string
	var fixedSymbols []string
	if raw, ok := cfg["selector_fixed_symbols"]; ok {
		if xs, ok := raw.([]interface{}); ok {
			for _, it := range xs {
				if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
					fixedSymbols = append(fixedSymbols, strings.TrimSpace(s))
				}
			}
		} else if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
			for _, p := range strings.Split(s, ",") {
				if v := strings.TrimSpace(p); v != "" {
					fixedSymbols = append(fixedSymbols, v)
				}
			}
		}
	}
	if len(fixedSymbols) > 0 {
		desired = append(desired, fixedSymbols...)
	} else {
		cands, err := bx.FetchMarketSymbols(quote, minPrice, maxPrice, minVol, maxSymbols, excludeStable, baseAssets)
		if err != nil {
			return err
		}
		desired = make([]string, 0, len(cands))
		for _, c := range cands {
			desired = append(desired, c.Symbol)
		}
	}
	sort.Strings(desired)

	var children []models.StrategySelectorChild
	_ = database.DB.Where("selector_id = ?", selectorID).Find(&children).Error
	childBySymbol := map[string]models.StrategySelectorChild{}
	for _, ch := range children {
		childBySymbol[exchange.NormalizeSymbol(ch.Symbol)] = ch
	}

	template := models.StrategyTemplate{}
	if err := database.DB.Where("id = ?", sel.ExecutorTemplate).First(&template).Error; err != nil {
		return err
	}

	// Ensure template.Path points to a .py file (not a directory)
	fixedPath := strings.TrimSpace(template.Path)
	needFix := fixedPath == ""
	if !needFix {
		if fi, err := os.Stat(fixedPath); err != nil || (err == nil && fi.IsDir()) {
			needFix = true
		}
	}
	if needFix {
		filename := fmt.Sprintf("%s_%d.py", template.Name, template.AuthorID)
		filename = strings.ReplaceAll(filename, " ", "_")
		filename = filepath.Base(filename)
		strategiesDir := conf.C().Paths.StrategiesDir
		if strategiesDir == "" {
			strategiesDir = conf.Path("strategies")
		}
		absDir, err := filepath.Abs(strategiesDir)
		if err == nil {
			_ = os.MkdirAll(absDir, 0o755)
			candidate := ""
			if strings.TrimSpace(template.Code) != "" {
				absPath := filepath.Join(absDir, filename)
				if err := os.WriteFile(absPath, []byte(template.Code), 0o644); err == nil {
					candidate = absPath
				}
			} else {
				entries, _ := os.ReadDir(absDir)
				prefix := strings.ToLower(strings.ReplaceAll(template.Name, " ", "_")) + "_"
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					n := strings.ToLower(e.Name())
					if strings.HasSuffix(n, ".py") && strings.HasPrefix(n, prefix) {
						candidate = filepath.Join(absDir, e.Name())
						break
					}
				}
			}
			if candidate != "" {
				fixedPath = candidate
				_ = database.DB.Model(&models.StrategyTemplate{}).Where("id = ?", template.ID).
					Updates(map[string]interface{}{"path": candidate, "updated_at": time.Now()}).Error
				logger.Infof("[SELECTOR PATH FIX] template_id=%d path=%s", template.ID, candidate)
			}
		}
	}
	if fixedPath == "" {
		return fmt.Errorf("selector executor template has no valid .py file path")
	}

	desiredSet := map[string]struct{}{}
	for _, sym := range desired {
		desiredSet[exchange.NormalizeSymbol(sym)] = struct{}{}
		if ch, ok := childBySymbol[exchange.NormalizeSymbol(sym)]; ok {
			_ = m.stratMgr.StartStrategy(ch.StrategyID)
			continue
		}

		childCfg := map[string]interface{}{}
		for k, v := range cfg {
			if strings.HasPrefix(k, "selector_") {
				continue
			}
			if k == "symbols" || k == "symbol" || k == "symbol_mode" {
				continue
			}
			childCfg[k] = v
		}
		childCfg["symbol"] = sym
		childCfg["symbol_mode"] = "fixed"
		childCfg["managed_by"] = selectorID

		cfgBytes, _ := json.Marshal(childCfg)

		id := models.GenerateUUID()
		name := fmt.Sprintf("%s %s", sel.Name, sym)
		inst := models.StrategyInstance{
			ID:         id,
			Name:       name,
			TemplateID: sel.ExecutorTemplate,
			OwnerID:    sel.OwnerID,
			Config:     string(cfgBytes),
			Status:     "stopped",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if err := database.DB.Create(&inst).Error; err != nil {
			continue
		}

		m.stratMgr.AddStrategy(inst.ID, inst.Name, fixedPath, inst.OwnerID, childCfg)
		_ = database.DB.Create(&models.StrategySelectorChild{
			SelectorID: selectorID,
			StrategyID: inst.ID,
			Symbol:     sym,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}).Error
		_ = m.stratMgr.StartStrategy(inst.ID)
	}

	for _, ch := range children {
		if _, ok := desiredSet[exchange.NormalizeSymbol(ch.Symbol)]; ok {
			continue
		}
		var openPos int64
		_ = database.DB.Model(&models.StrategyPosition{}).
			Where("owner_id = ? AND symbol = ? AND status = ?", sel.OwnerID, ch.Symbol, "open").
			Count(&openPos).Error
		if openPos > 0 {
			continue
		}
		_ = m.stratMgr.StopStrategy(ch.StrategyID, false)
		_ = database.DB.Where("id = ?", ch.ID).Delete(&models.StrategySelectorChild{}).Error
	}

	database.DB.Model(&models.StrategySelector{}).Where("id = ?", selectorID).Updates(map[string]interface{}{
		"updated_at": time.Now(),
	})
	return nil
}
