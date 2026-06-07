package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// optimizer / cron-driven auto-optimization endpoints.
// Architecture: every 6h an external (Claude-side) routine fetches context via
// GET /api/admin/optimize/context, reasons about it, then POSTs a new template
// via /api/admin/optimize/apply which atomically creates a new StrategyTemplate
// row, rebinds the instance to it (clearing any StrategyVersionID), and restarts.

// === Endpoint 1: GET /api/admin/optimize/context?strategy_id=<id>&hours=24 ===

type optimizeContextSymbolSummary struct {
	Count    int     `json:"count"`
	Wins     int     `json:"wins"`
	PnL      float64 `json:"pnl"`
	Notional float64 `json:"notional"`
}

type optimizeTradesSummary struct {
	Count       int                                     `json:"count"`
	WinCount    int                                     `json:"win_count"`
	LossCount   int                                     `json:"loss_count"`
	WinRatePct  float64                                 `json:"win_rate_pct"`
	RealizedPnL float64                                 `json:"realized_pnl"`
	BySymbol    map[string]optimizeContextSymbolSummary `json:"by_symbol"`
	LongCount   int                                     `json:"long_count"`
	ShortCount  int                                     `json:"short_count"`
	LongPnL     float64                                 `json:"long_pnl"`
	ShortPnL    float64                                 `json:"short_pnl"`
}

type optimizeAccountSnapshot struct {
	Exchange      string  `json:"exchange"`
	Market        string  `json:"market"`
	OpenPositions int     `json:"open_positions"`
	OpenNotional  float64 `json:"open_notional"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
}

type optimizeContextResponse struct {
	StrategyID        string                 `json:"strategy_id"`
	StrategyName      string                 `json:"strategy_name"`
	OwnerID           uint                   `json:"owner_id"`
	TemplateID        uint                   `json:"current_template_id"`
	StrategyVersionID *uint                  `json:"current_version_id,omitempty"`
	Status            string                 `json:"status"`
	Config            map[string]interface{} `json:"config"`
	CurrentCode       string                 `json:"current_code"`
	CurrentCodeHash   string                 `json:"current_code_hash"`
	Account           optimizeAccountSnapshot `json:"account"`
	TradesWindow      optimizeTradesSummary  `json:"trades_window"`
	DailyPnL7d        []DailyPnLEntry        `json:"daily_pnl_7d"`
	WindowHours       int                    `json:"window_hours"`
	GeneratedAt       time.Time              `json:"generated_at"`
}

// GetOptimizeContext returns the full context the cron-side LLM needs to
// reason about a candidate optimization.
func GetOptimizeContext(c *gin.Context) {
	strategyID := strings.TrimSpace(c.Query("strategy_id"))
	if strategyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing strategy_id"})
		return
	}
	hours := 24
	if v := strings.TrimSpace(c.Query("hours")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 720 {
			hours = n
		}
	}

	var row models.StrategyInstance
	if err := database.DB.Preload("Template").Preload("StrategyVersion").
		Where("id = ?", strategyID).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "strategy not found"})
		return
	}

	code, err := resolveCodeForOptimize(&row)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	codeHash := sha256.Sum256([]byte(code))

	var cfg map[string]interface{}
	if strings.TrimSpace(row.Config) != "" {
		_ = json.Unmarshal([]byte(row.Config), &cfg)
	}

	// Trades in window
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	var positions []models.StrategyPosition
	_ = database.DB.Where(
		"owner_id = ? AND strategy_id = ? AND status = ? AND close_time >= ?",
		row.OwnerID, strategyID, "closed", since,
	).Find(&positions).Error

	trades := optimizeTradesSummary{BySymbol: map[string]optimizeContextSymbolSummary{}}
	for _, p := range positions {
		trades.Count++
		trades.RealizedPnL += p.RealizedPnL
		ss := trades.BySymbol[p.Symbol]
		ss.Count++
		ss.PnL += p.RealizedPnL
		ss.Notional += p.RealizedNotional
		if p.RealizedPnL > 0 {
			ss.Wins++
			trades.WinCount++
		} else if p.RealizedPnL < 0 {
			trades.LossCount++
		}
		trades.BySymbol[p.Symbol] = ss
		if strings.EqualFold(p.Direction, "long") {
			trades.LongCount++
			trades.LongPnL += p.RealizedPnL
		} else if strings.EqualFold(p.Direction, "short") {
			trades.ShortCount++
			trades.ShortPnL += p.RealizedPnL
		}
	}
	if trades.Count > 0 {
		trades.WinRatePct = float64(trades.WinCount) / float64(trades.Count) * 100
	}

	// Account snapshot
	acct := optimizeAccountSnapshot{}
	if stratMgr != nil && stratMgr.GetExchange() != nil {
		acct.Exchange = stratMgr.GetExchange().GetName()
		if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok {
			acct.Market = bx.Market()
			if ps, err := bx.FetchPositions(row.OwnerID, "active"); err == nil {
				acct.OpenPositions = len(ps)
				for _, p := range ps {
					acct.UnrealizedPnL += p.UnrealizedPnL
					cp := p.CurrentPrice
					if cp <= 0 {
						cp = p.Price
					}
					if cp > 0 {
						acct.OpenNotional += p.Amount * cp
					}
				}
			}
		}
	}

	resp := optimizeContextResponse{
		StrategyID:        strategyID,
		StrategyName:      row.Name,
		OwnerID:           row.OwnerID,
		TemplateID:        row.TemplateID,
		StrategyVersionID: row.StrategyVersionID,
		Status:            row.Status,
		Config:            cfg,
		CurrentCode:       code,
		CurrentCodeHash:   hex.EncodeToString(codeHash[:]),
		Account:           acct,
		TradesWindow:      trades,
		DailyPnL7d:        loadDailyPnLCalendar(row.OwnerID, 7),
		WindowHours:       hours,
		GeneratedAt:       time.Now(),
	}
	c.JSON(http.StatusOK, resp)
}

// === Endpoint 2: POST /api/admin/optimize/apply ===

type applyOptimizationRequest struct {
	StrategyID   string `json:"strategy_id" binding:"required"`
	Code         string `json:"code" binding:"required"`
	Name         string `json:"name"`         // optional, auto-generated
	Description  string `json:"description"`
	BaselineHash string `json:"baseline_hash"` // optional, race detection
}

// Required symbols the new code must contain (sanity check 1).
var optimizerMustHaveSymbols = []string{
	"on_market_message",
	"_emit_signal",
	"_append_bar",
	"self.pub.publish",
}

// Key numeric parameters whose magnitude may not drift > 2x per iteration.
var optimizerKeyParams = []string{
	"TP_RATIO", "SL_RATIO",
	"ATR_TP_MULT", "ATR_SL_MULT",
	"MIN_CONFIDENCE",
	"WARMUP_BARS",
	"VOLUME_RATIO_MIN",
	"MAX_BARS",
}

// ApplyOptimization handles cron-driven template swap. Three internal
// guards must pass before the new template is created and bound.
func ApplyOptimization(c *gin.Context) {
	var req applyOptimizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Guard 1: must-have symbols
	for _, sym := range optimizerMustHaveSymbols {
		if !strings.Contains(req.Code, sym) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("rejected: missing required symbol %q", sym),
				"guard": "must_have_symbols",
			})
			return
		}
	}

	// Guard 2: py_compile
	if err := pyCompileCheck(req.Code); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("rejected: py_compile failed: %v", err),
			"guard": "py_compile",
		})
		return
	}

	// Load current
	var row models.StrategyInstance
	if err := database.DB.Preload("Template").Preload("StrategyVersion").
		Where("id = ?", req.StrategyID).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "strategy not found"})
		return
	}
	oldCode, _ := resolveCodeForOptimize(&row)
	oldHashRaw := sha256.Sum256([]byte(oldCode))
	newHashRaw := sha256.Sum256([]byte(req.Code))
	oldHash := hex.EncodeToString(oldHashRaw[:])
	newHash := hex.EncodeToString(newHashRaw[:])
	if oldHash == newHash {
		c.JSON(http.StatusOK, gin.H{
			"status": "no_change",
			"hint":   "new code identical to current; nothing to do",
		})
		return
	}
	if req.BaselineHash != "" && req.BaselineHash != oldHash {
		c.JSON(http.StatusConflict, gin.H{
			"error": "baseline_hash mismatch — strategy changed since context fetched",
			"guard": "baseline_race",
		})
		return
	}

	// Guard 3: parameter drift
	if drift := paramDriftCheck(oldCode, req.Code); drift != "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "rejected: parameter drift too large — " + drift,
			"guard": "param_drift",
		})
		return
	}

	// Auto-generate template name if missing
	if strings.TrimSpace(req.Name) == "" {
		prefix := fmt.Sprintf("%s_auto_v", row.Name)
		nextN := nextAutoVersionNumber(prefix)
		req.Name = fmt.Sprintf("%s%d_%s", prefix, nextN, time.Now().Format("20060102-1504"))
	}

	// Transactional apply
	var newTemplateID uint
	wasVersionBound := row.StrategyVersionID != nil
	previousTemplateID := row.TemplateID
	dbErr := database.DB.Transaction(func(tx *gorm.DB) error {
		nt := models.StrategyTemplate{
			Name:         req.Name,
			Description:  req.Description,
			Code:         req.Code,
			Path:         fmt.Sprintf("db://template/%s", req.Name),
			TemplateType: "strategy",
			AuthorID:     row.OwnerID,
			IsDraft:      false,
			IsEnabled:    true,
		}
		if err := tx.Create(&nt).Error; err != nil {
			return err
		}
		newTemplateID = nt.ID
		if err := tx.Model(&models.StrategyInstance{}).
			Where("id = ?", req.StrategyID).
			Updates(map[string]interface{}{
				"template_id":         newTemplateID,
				"strategy_version_id": nil,
				"updated_at":          time.Now(),
			}).Error; err != nil {
			return err
		}
		summary := strings.TrimSpace(req.Description)
		if summary == "" {
			summary = "cron auto-optimize"
		}
		_ = tx.Create(&models.StrategyOptimizationRun{
			StrategyID:        req.StrategyID,
			OwnerID:           row.OwnerID,
			Status:            "applied",
			Trigger:           "cron_auto",
			Model:             "claude-cron",
			BaseCodeHash:      oldHash,
			CandidateCodeHash: newHash,
			Applied:           true,
			Summary:           summary,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}).Error
		return nil
	})
	if dbErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": dbErr.Error()})
		return
	}

	// Restart strategy if currently active
	needsRestart := row.Status == "running" || row.Status == "starting"
	if needsRestart && stratMgr != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("[OPTIMIZE] restart panic strategy=%s panic=%v", req.StrategyID, r)
				}
			}()
			if err := stratMgr.StopStrategy(req.StrategyID, true); err != nil {
				logger.Errorf("[OPTIMIZE] stop failed strategy=%s err=%v", req.StrategyID, err)
			}
			time.Sleep(2 * time.Second)
			if err := stratMgr.StartStrategy(req.StrategyID); err != nil {
				logger.Errorf("[OPTIMIZE] start failed strategy=%s err=%v", req.StrategyID, err)
			}
		}()
	}

	c.JSON(http.StatusOK, gin.H{
		"status":               "applied",
		"new_template_id":      newTemplateID,
		"previous_template_id": previousTemplateID,
		"new_template_name":    req.Name,
		"version_unbound":      wasVersionBound,
		"needs_restart":        needsRestart,
		"restart":              "scheduled async",
		"old_code_hash":        oldHash,
		"new_code_hash":        newHash,
	})
}

// === Helpers ===

// resolveCodeForOptimize picks the code currently in effect for the instance.
// Mirrors strategy/manager.go:resolveStrategySource but stays inside the api
// package to avoid widening that internal helper's visibility.
func resolveCodeForOptimize(row *models.StrategyInstance) (string, error) {
	if row == nil {
		return "", fmt.Errorf("nil strategy row")
	}
	if row.StrategyVersionID != nil && row.StrategyVersion.ID > 0 {
		if c := strings.TrimSpace(row.StrategyVersion.Code); c != "" {
			return c, nil
		}
	}
	if c := strings.TrimSpace(row.Template.Code); c != "" {
		return c, nil
	}
	return "", fmt.Errorf("no code available for strategy %s", row.ID)
}

// pyCompileCheck writes code to a temp file and runs `python3 -m py_compile`.
// Returns nil on syntax-valid Python, error otherwise.
func pyCompileCheck(code string) error {
	f, err := os.CreateTemp("", "claude_optim_*.py")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	if _, err := f.WriteString(code); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	cmd := exec.Command("python3", "-m", "py_compile", tmpPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// paramDriftCheck compares values of optimizerKeyParams between old/new code.
// Returns non-empty string describing a violation if any param changed by
// more than 2x in either direction; empty string means all clear.
func paramDriftCheck(oldCode, newCode string) string {
	for _, name := range optimizerKeyParams {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=\s*([+\-0-9eE\.]+)`)
		oldM := re.FindStringSubmatch(oldCode)
		newM := re.FindStringSubmatch(newCode)
		if len(oldM) < 2 || len(newM) < 2 {
			continue
		}
		oldVal, err1 := strconv.ParseFloat(oldM[1], 64)
		newVal, err2 := strconv.ParseFloat(newM[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if oldVal == 0 {
			continue
		}
		ratio := newVal / oldVal
		if ratio < 0.5 || ratio > 2.0 {
			return fmt.Sprintf("%s: %g → %g (ratio %.2fx)", name, oldVal, newVal, ratio)
		}
	}
	return ""
}

// nextAutoVersionNumber scans for templates named "<prefix>N_*" and returns N+1.
func nextAutoVersionNumber(prefix string) int {
	var rows []models.StrategyTemplate
	_ = database.DB.Where("name LIKE ?", prefix+"%").
		Order("created_at desc").
		Limit(10).
		Find(&rows).Error
	maxN := 0
	for _, r := range rows {
		rest := strings.TrimPrefix(r.Name, prefix)
		parts := strings.SplitN(rest, "_", 2)
		if len(parts) == 0 {
			continue
		}
		n, err := strconv.Atoi(parts[0])
		if err == nil && n > maxN {
			maxN = n
		}
	}
	return maxN + 1
}
