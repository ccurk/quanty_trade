package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"quanty_trade/internal/auth"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"quanty_trade/internal/secure"
	"quanty_trade/internal/strategy"

	"github.com/gin-gonic/gin"
)

var stratMgr *strategy.Manager

// SetManager injects the global strategy manager into API handlers.
// This is called once during server startup.
func SetManager(m *strategy.Manager) {
	stratMgr = m
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterRequest struct {
	Username string                 `json:"username" binding:"required"`
	Password string                 `json:"password" binding:"required"`
	Configs  map[string]interface{} `json:"configs"` // e.g. {"binance": {"apiKey": "...", "secret": "..."}}
}

func Register(c *gin.Context) {
	// Register creates a new user account.
	// Request body can include exchange credentials under configs (e.g. binance apiKey/apiSecret).
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if user already exists
	var existingUser models.User
	if err := database.DB.Where("username = ?", req.Username).First(&existingUser).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User already exists"})
		return
	}

	hashedPassword, _ := auth.HashPassword(req.Password)
	configsJSON, _ := json.Marshal(req.Configs)
	encryptedConfigs, err := secure.EncryptString(string(configsJSON))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to secure configs"})
		return
	}

	user := models.User{
		Username: req.Username,
		Password: hashedPassword,
		Role:     models.RoleUser,
		Configs:  encryptedConfigs,
	}

	if err := database.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to register user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "registered"})
}

func Login(c *gin.Context) {
	// Login validates username/password and returns a JWT token.
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	if err := database.DB.Where("username = ?", req.Username).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	if !auth.CheckPasswordHash(req.Password, user.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Username, string(user.Role))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user":  user,
	})
}

type CreateUserRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Role     string `json:"role"`
}

func CreateUser(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hashedPassword, _ := auth.HashPassword(req.Password)
	user := models.User{
		Username: req.Username,
		Password: hashedPassword,
		Role:     models.UserRole(req.Role),
	}

	if err := database.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	c.JSON(http.StatusOK, user)
}

func ListUsers(c *gin.Context) {
	var users []models.User
	database.DB.Order("id desc").Find(&users)
	c.JSON(http.StatusOK, users)
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func APILogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Stop timer
		latency := time.Since(start)

		if raw != "" {
			path = path + "?" + raw
		}

		userID, _ := c.Get("user_id")
		username, _ := c.Get("username")

		var uID uint
		if id, ok := userID.(uint); ok {
			uID = id
		}

		var uName string
		if name, ok := username.(string); ok {
			uName = name
		}

		logEntry := models.APILog{
			Method:     c.Request.Method,
			Path:       path,
			StatusCode: c.Writer.Status(),
			Latency:    latency.Nanoseconds(),
			ClientIP:   c.ClientIP(),
			UserID:     uID,
			Username:   uName,
			TraceID:    TraceID(c),
			CreatedAt:  time.Now(),
		}

		// Save to DB asynchronously to avoid blocking the request
		go func(entry models.APILog) {
			if err := database.DB.Create(&entry).Error; err != nil {
				logger.WithTrace(entry.TraceID).Errorf("APILog insert failed err=%v", err)
			}
		}(logEntry)
	}
}

func DeleteUser(c *gin.Context) {
	id := c.Param("id")
	if id == "1" { // Prevent deleting the initial admin
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete main admin"})
		return
	}
	if err := database.DB.Delete(&models.User{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

type PnLPeriodSummary struct {
	StartTime         time.Time `json:"start_time"`
	EndTime           time.Time `json:"end_time,omitempty"`
	GrossProfit       float64   `json:"gross_profit"`
	GrossLoss         float64   `json:"gross_loss"`
	RealizedPnL       float64   `json:"realized_pnl"`
	RealizedNotional  float64   `json:"realized_notional"`
	RealizedReturnPct float64   `json:"realized_return_pct"`
	UnrealizedPnL     float64   `json:"unrealized_pnl"`
	TotalPnL          float64   `json:"total_pnl"`
}

type DailyPnLEntry struct {
	Day               string  `json:"day"`
	RealizedPnL       float64 `json:"realized_pnl"`
	RealizedNotional  float64 `json:"realized_notional"`
	RealizedReturnPct float64 `json:"realized_return_pct"`
	Trades            int     `json:"trades"`
}

type PnLSummaryResponse struct {
	UpdatedAt     time.Time         `json:"updated_at"`
	UnrealizedPnL float64           `json:"unrealized_pnl"`
	Day           PnLPeriodSummary  `json:"day"`
	Week          PnLPeriodSummary  `json:"week"`
	Month         PnLPeriodSummary  `json:"month"`
	Custom        *PnLPeriodSummary `json:"custom,omitempty"`
	CustomLabel   string            `json:"custom_label,omitempty"`
	Calendar      []DailyPnLEntry   `json:"calendar,omitempty"`
}

type DashboardResponse struct {
	UpdatedAt time.Time `json:"updated_at"`

	Account struct {
		Exchange string `json:"exchange"`
		Market   string `json:"market"`
		UserID   uint   `json:"user_id"`
	} `json:"account"`

	PnL PnLSummaryResponse `json:"pnl"`

	Positions struct {
		OpenCount     int     `json:"open_count"`
		OpenSymbols   int     `json:"open_symbols"`
		OpenNotional  float64 `json:"open_notional"`
		UnrealizedPnL float64 `json:"unrealized_pnl"`
	} `json:"positions"`

	Orders struct {
		Total     int64 `json:"total"`
		Filled    int64 `json:"filled"`
		Rejected  int64 `json:"rejected"`
		Failed    int64 `json:"failed"`
		Requested int64 `json:"requested"`
		New       int64 `json:"new"`
	} `json:"orders"`

	Strategies struct {
		Running int64 `json:"running"`
		Stopped int64 `json:"stopped"`
		Error   int64 `json:"error"`
		Total   int64 `json:"total"`
	} `json:"strategies"`
}

func GetDashboard(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := userID.(uint)

	now := time.Now()
	rangePreset := strings.TrimSpace(c.Query("range"))
	startRaw := strings.TrimSpace(c.Query("start"))
	endRaw := strings.TrimSpace(c.Query("end"))
	if rangePreset == "" && startRaw == "" && endRaw == "" {
		if snap, ok := getDashboardSnapshot(uid); ok {
			c.JSON(http.StatusOK, snap)
			return
		}
	}
	resp, err := buildDashboardResponse(uid, now, dashboardBuildOptions{
		RangePreset:     rangePreset,
		StartRaw:        startRaw,
		EndRaw:          endRaw,
		IncludeCalendar: true,
		CalendarDays:    60,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func GetPnLSummary(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := userID.(uint)

	now := time.Now()
	loc := now.Location()
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	weekOffset := (int(now.Weekday()) + 6) % 7
	startWeek := startDay.AddDate(0, 0, -weekOffset)
	startMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

	unrealized := 0.0
	if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		if ps, err := bx.FetchPositions(uid, "active"); err == nil {
			for _, p := range ps {
				unrealized += p.UnrealizedPnL
			}
		}
	}

	period := func(start time.Time) PnLPeriodSummary {
		var row struct {
			GrossProfit      float64
			GrossLoss        float64
			RealizedPnL      float64
			RealizedNotional float64
		}
		_ = database.DB.Model(&models.StrategyPosition{}).
			Select(`
				COALESCE(SUM(CASE WHEN realized_pn_l > 0 THEN realized_pn_l ELSE 0 END), 0) AS gross_profit,
				COALESCE(SUM(CASE WHEN realized_pn_l < 0 THEN realized_pn_l ELSE 0 END), 0) AS gross_loss,
				COALESCE(SUM(realized_pn_l), 0) AS realized_pnl,
				COALESCE(SUM(realized_notional), 0) AS realized_notional
			`).
			Where("owner_id = ? AND status = ? AND close_time >= ?", uid, "closed", start).
			Scan(&row).Error

		ret := 0.0
		if row.RealizedNotional > 0 {
			ret = (row.RealizedPnL / row.RealizedNotional) * 100
		}
		return PnLPeriodSummary{
			StartTime:         start,
			GrossProfit:       row.GrossProfit,
			GrossLoss:         row.GrossLoss,
			RealizedPnL:       row.RealizedPnL,
			RealizedNotional:  row.RealizedNotional,
			RealizedReturnPct: ret,
			UnrealizedPnL:     unrealized,
			TotalPnL:          row.RealizedPnL + unrealized,
		}
	}

	resp := PnLSummaryResponse{
		UpdatedAt:     now,
		UnrealizedPnL: unrealized,
		Day:           period(startDay),
		Week:          period(startWeek),
		Month:         period(startMonth),
	}
	c.JSON(http.StatusOK, resp)
}

type SaveTemplateRequest struct {
	Name         string `json:"name" binding:"required"`
	Description  string `json:"description"`
	Code         string `json:"code" binding:"required"`
	IsDraft      bool   `json:"is_draft"`
	TemplateType string `json:"template_type"`
}

type UpdateTemplateRequest struct {
	Name         string `json:"name" binding:"required"`
	Description  string `json:"description"`
	Code         string `json:"code" binding:"required"`
	IsDraft      bool   `json:"is_draft"`
	TemplateType string `json:"template_type"`
}

func validateLegacyStrategyCode(code string) []string {
	c := strings.ToLower(code)
	missing := make([]string, 0)

	if !strings.Contains(c, "basestrategy") {
		missing = append(missing, "继承 BaseStrategy")
	}
	if !strings.Contains(c, "def on_candle") {
		missing = append(missing, "def on_candle")
	}
	if !strings.Contains(c, "def on_order") {
		missing = append(missing, "def on_order")
	}

	hasOpen := strings.Contains(c, ".buy(") ||
		strings.Contains(c, ".buy_for(") ||
		strings.Contains(c, "send_order(\"buy") ||
		strings.Contains(c, "send_order('buy") ||
		strings.Contains(c, "send_order_for(")
	if !hasOpen {
		missing = append(missing, "开仓下单 buy()/send_order(buy)")
	}

	hasClose := strings.Contains(c, ".close_position(") ||
		strings.Contains(c, ".close_position_for(") ||
		strings.Contains(c, ".sell(") ||
		strings.Contains(c, ".sell_for(") ||
		strings.Contains(c, "send_order(\"sell") ||
		strings.Contains(c, "send_order('sell") ||
		strings.Contains(c, "send_order_for(")
	if !hasClose {
		missing = append(missing, "平仓 close_position()/sell()/send_order(sell)")
	}

	if !strings.Contains(c, "strategy.run(") {
		missing = append(missing, "main 中调用 strategy.run()")
	}

	return missing
}

func validateRedisStrategyCode(code string) []string {
	c := strings.ToLower(code)
	missing := make([]string, 0)

	hasRedis := strings.Contains(c, "miniredis") || strings.Contains(c, "redis") || strings.Contains(c, "pubsub")
	if !hasRedis {
		missing = append(missing, "Redis 模式策略（使用 MiniRedis/Redis PubSub）")
	}

	hasCandleSub := strings.Contains(c, ":candle:") || strings.Contains(c, "_candle_ch") || strings.Contains(c, ".subscribe(")
	if !hasCandleSub {
		missing = append(missing, "订阅 candle channel（:candle: / subscribe）")
	}

	hasSignalPub := strings.Contains(c, ":signal:") || strings.Contains(c, "_signal_ch") || strings.Contains(c, ".publish(")
	if !hasSignalPub {
		missing = append(missing, "发布 signal（:signal: / publish）")
	}

	hasStateReady := strings.Contains(c, ":state:") || strings.Contains(c, "_state_ch")
	hasReadyType := strings.Contains(c, "\"type\": \"ready\"") || strings.Contains(c, "'type': 'ready'") || strings.Contains(c, "\"type\":\"ready\"") || strings.Contains(c, "'type':'ready'")
	if !(hasStateReady && hasReadyType) {
		missing = append(missing, "启动上报 ready（state channel + type=ready）")
	}

	hasRun := strings.Contains(c, "def run") && (strings.Contains(c, "if __name__") || strings.Contains(c, "if __name__ ==") || strings.Contains(c, "if __name__=="))
	if !hasRun {
		missing = append(missing, "main 中调用 run()（if __name__ == '__main__'）")
	}

	return missing
}

func validateStrategyCode(code string) []string {
	legacyMissing := validateLegacyStrategyCode(code)
	if len(legacyMissing) == 0 {
		return nil
	}
	redisMissing := validateRedisStrategyCode(code)
	if len(redisMissing) == 0 {
		return nil
	}

	out := make([]string, 0, len(legacyMissing)+len(redisMissing)+2)
	out = append(out, "未匹配任一策略协议：BaseStrategy 或 RedisSignal")
	out = append(out, "BaseStrategy 协议缺少："+strings.Join(legacyMissing, "、"))
	out = append(out, "RedisSignal 协议缺少："+strings.Join(redisMissing, "、"))
	return out
}

func SaveTemplate(c *gin.Context) {
	var req SaveTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// log debug
	log.Printf("SaveTemplate req: %+v", req)

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模板名称不能为空"})
		return
	}
	templateType := "strategy"

	userIDValue, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userID, ok := userIDValue.(uint)
	if !ok || userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	if !req.IsDraft && templateType == "strategy" {
		missing := validateStrategyCode(req.Code)
		if len(missing) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   fmt.Sprintf("策略模板缺少必备实现: %s", strings.Join(missing, "、")),
				"missing": missing,
			})
			return
		}
	}

	// Find if existing template with same name by same author
	var template models.StrategyTemplate
	err := database.DB.Where("name = ? AND author_id = ?", req.Name, userID).First(&template).Error

	absPath := strings.TrimSpace(template.Path)
	if absPath == "" {
		absPath = fmt.Sprintf("db://template/%s_%d", strings.ReplaceAll(req.Name, " ", "_"), userID)
	}

	if err == nil {
		// Update existing
		template.Name = req.Name
		template.Description = req.Description
		template.Code = req.Code
		template.IsDraft = req.IsDraft
		template.Path = absPath
		template.TemplateType = templateType
		if err := database.DB.Save(&template).Error; err != nil {
			logger.WithTrace(TraceID(c)).Errorf("SaveTemplate update failed err=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		// Create new
		template = models.StrategyTemplate{
			Name:         req.Name,
			Description:  req.Description,
			Path:         absPath,
			TemplateType: templateType,
			AuthorID:     userID,
			IsPublic:     false,
			IsDraft:      req.IsDraft,
			Code:         req.Code,
		}
		if err := database.DB.Create(&template).Error; err != nil {
			logger.WithTrace(TraceID(c)).Errorf("SaveTemplate create failed err=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, template)
}

func UpdateTemplate(c *gin.Context) {
	id := c.Param("id")
	var req UpdateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模板名称不能为空"})
		return
	}
	templateType := "strategy"

	userIDValue, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userID, ok := userIDValue.(uint)
	if !ok || userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userRole, _ := c.Get("role")

	var template models.StrategyTemplate
	if err := database.DB.Where("id = ?", id).First(&template).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template not found"})
		return
	}
	if template.AuthorID != userID && userRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	if !req.IsDraft && templateType == "strategy" {
		missing := validateStrategyCode(req.Code)
		if len(missing) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   fmt.Sprintf("策略模板缺少必备实现: %s", strings.Join(missing, "、")),
				"missing": missing,
			})
			return
		}
	}

	absPath := strings.TrimSpace(template.Path)
	if absPath == "" {
		absPath = fmt.Sprintf("db://template/%s_%d", strings.ReplaceAll(req.Name, " ", "_"), template.AuthorID)
	}

	template.Name = req.Name
	template.Description = req.Description
	template.Code = req.Code
	template.IsDraft = req.IsDraft
	template.Path = absPath
	template.TemplateType = templateType
	template.UpdatedAt = time.Now()

	if err := database.DB.Save(&template).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, template)
}

type TestCodeRequest struct {
	Code string `json:"code" binding:"required"`
}

func TestCode(c *gin.Context) {
	var req TestCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Temporary file for testing
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("test_%d.py", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, []byte(req.Code), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create temp test file"})
		return
	}
	defer os.Remove(tmpFile)

	// Run python -m py_compile to check syntax
	cmd := exec.Command("python3", "-m", "py_compile", tmpFile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"valid": false,
			"error": stderr.String(),
		})
		return
	}

	if missing := validateStrategyCode(req.Code); len(missing) > 0 {
		c.JSON(http.StatusOK, gin.H{
			"valid": false,
			"error": fmt.Sprintf("策略模板缺少必备实现: %s", strings.Join(missing, "、")),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"valid": true})
}

// Strategy Square (Templates)

func ListTemplates(c *gin.Context) {
	var templates []models.StrategyTemplate
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")
	onlyEnabled := c.Query("only_enabled") == "true"

	query := database.DB.Preload("Author")
	if userRole != "admin" {
		// Users see public templates OR their own templates
		query = query.Where("is_public = ? OR author_id = ?", true, userID.(uint))
	}

	if onlyEnabled {
		query = query.Where("is_enabled = ?", true)
	}
	// Admin sees everything

	query.Order("created_at desc").Find(&templates)
	c.JSON(http.StatusOK, templates)
}

func ToggleTemplateEnabled(c *gin.Context) {
	id := c.Param("id")
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")

	var template models.StrategyTemplate
	if err := database.DB.First(&template, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template not found"})
		return
	}

	// Permission check
	if template.AuthorID != userID.(uint) && userRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	template.IsEnabled = !template.IsEnabled
	if err := database.DB.Save(&template).Error; err != nil {
		logger.WithTrace(TraceID(c)).Errorf("ToggleTemplateEnabled save failed err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "is_enabled": template.IsEnabled})
}

func ListPublicTemplates(c *gin.Context) {
	var templates []models.StrategyTemplate
	database.DB.Preload("Author").Where("is_public = ?", true).Order("created_at desc").Find(&templates)
	c.JSON(http.StatusOK, templates)
}

func DeleteTemplate(c *gin.Context) {
	id := c.Param("id")
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")

	var template models.StrategyTemplate
	if err := database.DB.Where("id = ?", id).First(&template).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template not found"})
		return
	}

	// Permission check: author or admin
	if template.AuthorID != userID.(uint) && userRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	if err := database.DB.Delete(&template).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete template"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

type PublishTemplateRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Path        string `json:"path" binding:"required"`
}

func PublishTemplate(c *gin.Context) {
	var req PublishTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	template := models.StrategyTemplate{
		Name:        req.Name,
		Description: req.Description,
		Path:        req.Path,
		AuthorID:    userID.(uint),
		IsPublic:    true,
	}

	if err := database.DB.Create(&template).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to publish template"})
		return
	}

	c.JSON(http.StatusOK, template)
}

type ReferenceTemplateRequest struct {
	TemplateID uint   `json:"template_id" binding:"required"`
	Name       string `json:"name" binding:"required"`
	Config     string `json:"config"`
}

func ReferenceTemplate(c *gin.Context) {
	var req ReferenceTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	instance := models.StrategyInstance{
		ID:         models.GenerateUUID(), // We'll need to add this
		Name:       req.Name,
		TemplateID: req.TemplateID,
		OwnerID:    userID.(uint),
		Config:     req.Config,
		Status:     "stopped",
	}

	if err := database.DB.Create(&instance).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reference template"})
		return
	}

	// Add to in-memory manager
	var config map[string]interface{}
	json.Unmarshal([]byte(instance.Config), &config)

	// Fetch template to get the path
	var template models.StrategyTemplate
	database.DB.First(&template, instance.TemplateID)

	stratMgr.AddStrategy(instance.ID, instance.Name, template.Path, userID.(uint), config)

	c.JSON(http.StatusOK, instance)
}
