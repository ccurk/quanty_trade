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
	"sort"
	"strconv"
	"strings"
	"time"

	"quanty_trade/internal/auth"
	"quanty_trade/internal/conf"
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

func ListStrategies(c *gin.Context) {
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")
	isAdmin := userRole == "admin"
	c.JSON(http.StatusOK, stratMgr.ListStrategies(userID.(uint), isAdmin))
}

type CreateStrategyRequest struct {
	Name       string `json:"name" binding:"required"`
	TemplateID uint   `json:"template_id" binding:"required"`
	Config     string `json:"config" binding:"required"`
}

func CreateStrategy(c *gin.Context) {
	var req CreateStrategyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	instance := models.StrategyInstance{
		ID:         models.GenerateUUID(),
		Name:       req.Name,
		TemplateID: req.TemplateID,
		OwnerID:    userID.(uint),
		Config:     req.Config,
		Status:     "stopped",
	}

	if err := database.DB.Create(&instance).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create strategy"})
		return
	}

	var config map[string]interface{}
	json.Unmarshal([]byte(instance.Config), &config)

	var template models.StrategyTemplate
	database.DB.First(&template, instance.TemplateID)

	stratMgr.AddStrategy(instance.ID, instance.Name, template.Path, userID.(uint), config)

	c.JSON(http.StatusOK, instance)
}

func ListPositions(c *gin.Context) {
	// ListPositions returns strategy-scoped positions derived from StrategyPosition table.
	// status=active maps to open, status=closed maps to closed.
	status := c.DefaultQuery("status", "active") // active or closed
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")
	pageRaw := strings.TrimSpace(c.Query("page"))
	pageSizeRaw := strings.TrimSpace(c.Query("page_size"))
	usePaging := pageRaw != "" || pageSizeRaw != ""
	page := 1
	pageSize := 20
	if pageRaw != "" {
		if v, err := strconv.Atoi(pageRaw); err == nil && v > 0 {
			page = v
		}
	}
	if pageSizeRaw != "" {
		if v, err := strconv.Atoi(pageSizeRaw); err == nil && v > 0 {
			pageSize = v
		}
	}
	if pageSize > 200 {
		pageSize = 200
	}

	if status == "active" {
		if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
			exPos, err := bx.FetchPositions(userID.(uint), "active")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}

			var instRows []models.StrategyInstance
			_ = database.DB.Where("owner_id = ?", userID.(uint)).Order("updated_at desc").Find(&instRows).Error
			findStrategyForSymbol := func(sym string) (string, string) {
				want := exchange.NormalizeSymbol(sym)
				for _, si := range instRows {
					var cfg map[string]interface{}
					if err := json.Unmarshal([]byte(si.Config), &cfg); err != nil {
						continue
					}
					if v, ok := cfg["symbol"].(string); ok && exchange.NormalizeSymbol(v) == want {
						return si.ID, si.Name
					}
				}
				return "", ""
			}

			type stratInfo struct {
				StrategyName string
				OpenTime     time.Time
				AvgPrice     float64
			}
			bySymbol := map[string]stratInfo{}
			var rows []models.StrategyPosition
			_ = database.DB.Where("owner_id = ? AND status = ?", userID.(uint), "open").Find(&rows).Error
			for _, p := range rows {
				bySymbol[strings.ToUpper(p.Symbol)] = stratInfo{StrategyName: p.StrategyName, OpenTime: p.OpenTime, AvgPrice: p.AvgPrice}
			}

			out := make([]exchange.Position, 0, len(exPos))
			for _, p := range exPos {
				key := strings.ToUpper(p.Symbol)
				if info, ok := bySymbol[key]; ok {
					p.StrategyName = info.StrategyName
					if !info.OpenTime.IsZero() {
						p.OpenTime = info.OpenTime
					}
					if p.Price == 0 && info.AvgPrice > 0 {
						p.Price = info.AvgPrice
					}
				} else {
					strategyID, strategyName := findStrategyForSymbol(p.Symbol)
					if strategyID != "" {
						now := time.Now()
						pos := models.StrategyPosition{
							StrategyID:   strategyID,
							StrategyName: strategyName,
							OwnerID:      userID.(uint),
							Exchange:     bx.GetName(),
							Symbol:       p.Symbol,
							Amount:       p.Amount,
							AvgPrice:     p.Price,
							Status:       "open",
							OpenTime:     p.OpenTime,
							UpdatedAt:    now,
						}
						_ = database.DB.Create(&pos).Error
						p.StrategyName = strategyName
						bySymbol[key] = stratInfo{StrategyName: strategyName, OpenTime: pos.OpenTime, AvgPrice: pos.AvgPrice}
					}
				}
				out = append(out, p)
			}
			sort.Slice(out, func(i, j int) bool { return out[i].OpenTime.After(out[j].OpenTime) })
			if !usePaging {
				c.JSON(http.StatusOK, out)
				return
			}
			total := len(out)
			start := (page - 1) * pageSize
			if start > total {
				start = total
			}
			end := start + pageSize
			if end > total {
				end = total
			}
			type resp struct {
				Items    []exchange.Position `json:"items"`
				Total    int                 `json:"total"`
				Page     int                 `json:"page"`
				PageSize int                 `json:"page_size"`
			}
			c.JSON(http.StatusOK, resp{Items: out[start:end], Total: total, Page: page, PageSize: pageSize})
			return
		}
	}

	query := database.DB.Model(&models.StrategyPosition{})
	if userRole != "admin" {
		query = query.Where("owner_id = ?", userID.(uint))
	}
	if status == "active" {
		query = query.Where("status = ?", "open")
	} else if status == "closed" {
		query = query.Where("status = ?", "closed")
	}

	var rows []models.StrategyPosition
	total := int64(0)
	if usePaging {
		_ = query.Count(&total).Error
		query = query.Offset((page - 1) * pageSize).Limit(pageSize)
	}
	if err := query.Order("open_time desc").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	positions := make([]exchange.Position, 0, len(rows))
	for _, p := range rows {
		realizedRet := 0.0
		if p.RealizedNotional > 0 {
			realizedRet = (p.RealizedPnL / p.RealizedNotional) * 100
		}
		pos := exchange.Position{
			Symbol:             p.Symbol,
			Amount:             p.Amount,
			Price:              p.AvgPrice,
			ClosedQty:          p.ClosedQty,
			AvgClosePrice:      p.AvgClosePrice,
			RealizedPnL:        p.RealizedPnL,
			RealizedReturnRate: realizedRet,
			StrategyName:       p.StrategyName,
			ExchangeName:       p.Exchange,
			Status: func() string {
				if p.Status == "open" {
					return "active"
				}
				return "closed"
			}(),
			OwnerID:   p.OwnerID,
			OpenTime:  p.OpenTime,
			CloseTime: p.CloseTime,
		}
		positions = append(positions, pos)
	}

	sort.Slice(positions, func(i, j int) bool { return positions[i].OpenTime.After(positions[j].OpenTime) })
	if !usePaging {
		c.JSON(http.StatusOK, positions)
		return
	}
	type resp struct {
		Items    []exchange.Position `json:"items"`
		Total    int64               `json:"total"`
		Page     int                 `json:"page"`
		PageSize int                 `json:"page_size"`
	}
	c.JSON(http.StatusOK, resp{Items: positions, Total: total, Page: page, PageSize: pageSize})
}

func ListMarketSymbols(c *gin.Context) {
	quote := strings.TrimSpace(c.Query("quote"))
	minPrice, _ := strconv.ParseFloat(strings.TrimSpace(c.Query("min_price")), 64)
	maxPrice, _ := strconv.ParseFloat(strings.TrimSpace(c.Query("max_price")), 64)
	minVol, _ := strconv.ParseFloat(strings.TrimSpace(c.Query("min_quote_volume_24h")), 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(c.Query("limit")))
	excludeStable := c.DefaultQuery("exclude_stable", "true") != "false"
	baseAssetsRaw := strings.TrimSpace(c.Query("base_assets"))
	var baseAssets []string
	if baseAssetsRaw != "" {
		for _, p := range strings.Split(baseAssetsRaw, ",") {
			if s := strings.TrimSpace(p); s != "" {
				baseAssets = append(baseAssets, s)
			}
		}
	}

	ex, ok := stratMgr.GetExchange().(*exchange.BinanceExchange)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "exchange does not support symbol selection"})
		return
	}
	out, err := ex.FetchMarketSymbols(quote, minPrice, maxPrice, minVol, limit, excludeStable, baseAssets)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
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

type PnLSummaryResponse struct {
	UpdatedAt     time.Time         `json:"updated_at"`
	UnrealizedPnL float64           `json:"unrealized_pnl"`
	Day           PnLPeriodSummary  `json:"day"`
	Week          PnLPeriodSummary  `json:"week"`
	Month         PnLPeriodSummary  `json:"month"`
	Custom        *PnLPeriodSummary `json:"custom,omitempty"`
	CustomLabel   string            `json:"custom_label,omitempty"`
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

	var pnlResp PnLSummaryResponse
	func() {
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

		period := func(start time.Time, end time.Time) PnLPeriodSummary {
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
				Where("owner_id = ? AND status = ? AND close_time >= ? AND close_time <= ?", uid, "closed", start, end).
				Scan(&row).Error

			ret := 0.0
			if row.RealizedNotional > 0 {
				ret = (row.RealizedPnL / row.RealizedNotional) * 100
			}
			return PnLPeriodSummary{
				StartTime:         start,
				EndTime:           end,
				GrossProfit:       row.GrossProfit,
				GrossLoss:         row.GrossLoss,
				RealizedPnL:       row.RealizedPnL,
				RealizedNotional:  row.RealizedNotional,
				RealizedReturnPct: ret,
				UnrealizedPnL:     unrealized,
				TotalPnL:          row.RealizedPnL + unrealized,
			}
		}

		pnlResp = PnLSummaryResponse{
			UpdatedAt:     now,
			UnrealizedPnL: unrealized,
			Day:           period(startDay, now),
			Week:          period(startWeek, now),
			Month:         period(startMonth, now),
		}

		customStart := time.Time{}
		customEnd := time.Time{}
		if startRaw != "" {
			if t, err := time.Parse(time.RFC3339, startRaw); err == nil {
				customStart = t
			} else if t, err := time.Parse("2006-01-02T15:04", startRaw); err == nil {
				customStart = t
			}
		}
		if endRaw != "" {
			if t, err := time.Parse(time.RFC3339, endRaw); err == nil {
				customEnd = t
			} else if t, err := time.Parse("2006-01-02T15:04", endRaw); err == nil {
				customEnd = t
			}
		}

		if rangePreset != "" || (!customStart.IsZero() && !customEnd.IsZero()) {
			label := rangePreset
			if rangePreset != "" {
				d := time.Duration(0)
				switch strings.ToLower(rangePreset) {
				case "1m":
					d = time.Minute
					label = "近 1 分钟"
				case "5m":
					d = 5 * time.Minute
					label = "近 5 分钟"
				case "1h":
					d = time.Hour
					label = "近 1 小时"
				case "1d":
					d = 24 * time.Hour
					label = "近 1 天"
				case "1w":
					d = 7 * 24 * time.Hour
					label = "近 1 周"
				case "1mo":
					d = 30 * 24 * time.Hour
					label = "近 1 个月"
				}
				if d > 0 {
					customStart = now.Add(-d)
					customEnd = now
				}
			} else {
				label = "自定义范围"
			}
			if !customStart.IsZero() && !customEnd.IsZero() && customEnd.After(customStart) {
				p := period(customStart, customEnd)
				pnlResp.Custom = &p
				pnlResp.CustomLabel = label
			}
		}
	}()

	openCount := 0
	openSymbols := 0
	openNotional := 0.0
	unrealized := 0.0
	if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		if ps, err := bx.FetchPositions(uid, "active"); err == nil {
			openCount = len(ps)
			syms := map[string]struct{}{}
			for _, p := range ps {
				syms[strings.ToUpper(p.Symbol)] = struct{}{}
				unrealized += p.UnrealizedPnL
				cp := p.CurrentPrice
				if cp <= 0 {
					cp = p.Price
				}
				if cp > 0 {
					openNotional += p.Amount * cp
				}
			}
			openSymbols = len(syms)
		}
	}

	var ordersAgg struct {
		Total     int64
		Filled    int64
		Rejected  int64
		Failed    int64
		Requested int64
		New       int64
	}
	_ = database.DB.Model(&models.StrategyOrder{}).
		Select(`
			COUNT(*) AS total,
			SUM(CASE WHEN status = 'filled' THEN 1 ELSE 0 END) AS filled,
			SUM(CASE WHEN status = 'rejected' THEN 1 ELSE 0 END) AS rejected,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed,
			SUM(CASE WHEN status = 'requested' THEN 1 ELSE 0 END) AS requested,
			SUM(CASE WHEN status = 'new' THEN 1 ELSE 0 END) AS new
		`).
		Where("owner_id = ?", uid).
		Scan(&ordersAgg).Error

	var stratAgg struct {
		Running int64
		Stopped int64
		Error   int64
		Total   int64
	}
	_ = database.DB.Model(&models.StrategyInstance{}).
		Select(`
			COUNT(*) AS total,
			SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END) AS running,
			SUM(CASE WHEN status = 'stopped' THEN 1 ELSE 0 END) AS stopped,
			SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) AS error
		`).
		Where("owner_id = ?", uid).
		Scan(&stratAgg).Error

	resp := DashboardResponse{
		UpdatedAt: now,
		PnL:       pnlResp,
	}
	resp.Account.Exchange = stratMgr.GetExchange().GetName()
	if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok {
		resp.Account.Market = bx.Market()
	}
	resp.Account.UserID = uid
	resp.Positions.OpenCount = openCount
	resp.Positions.OpenSymbols = openSymbols
	resp.Positions.OpenNotional = openNotional
	resp.Positions.UnrealizedPnL = unrealized
	resp.Orders.Total = ordersAgg.Total
	resp.Orders.Filled = ordersAgg.Filled
	resp.Orders.Rejected = ordersAgg.Rejected
	resp.Orders.Failed = ordersAgg.Failed
	resp.Orders.Requested = ordersAgg.Requested
	resp.Orders.New = ordersAgg.New
	resp.Strategies.Total = stratAgg.Total
	resp.Strategies.Running = stratAgg.Running
	resp.Strategies.Stopped = stratAgg.Stopped
	resp.Strategies.Error = stratAgg.Error

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

func ClosePosition(c *gin.Context) {
	// ClosePosition closes the latest open position for the user and symbol by placing a sell market order.
	// The order is persisted into StrategyOrder first, then updated after PlaceOrder returns.
	symbol := c.Query("symbol")
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Symbol is required"})
		return
	}

	userID, _ := c.Get("user_id")
	if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		uid := userID.(uint)

		var existing models.StrategyPosition
		hasExisting := database.DB.Where("owner_id = ? AND symbol = ? AND status = ?", uid, symbol, "open").
			Order("open_time desc").
			First(&existing).Error == nil

		findStrategyForSymbol := func(sym string) (string, string) {
			var instRows []models.StrategyInstance
			_ = database.DB.Where("owner_id = ?", uid).Order("updated_at desc").Find(&instRows).Error
			want := exchange.NormalizeSymbol(sym)
			for _, si := range instRows {
				var cfg map[string]interface{}
				if err := json.Unmarshal([]byte(si.Config), &cfg); err != nil {
					continue
				}
				if v, ok := cfg["symbol"].(string); ok && exchange.NormalizeSymbol(v) == want {
					return si.ID, si.Name
				}
				if raw, ok := cfg["symbols"]; ok {
					if xs, ok := raw.([]interface{}); ok {
						for _, it := range xs {
							if s, ok := it.(string); ok && exchange.NormalizeSymbol(s) == want {
								return si.ID, si.Name
							}
						}
					} else if xs, ok := raw.([]string); ok {
						for _, s := range xs {
							if exchange.NormalizeSymbol(s) == want {
								return si.ID, si.Name
							}
						}
					}
				}
			}
			return "", ""
		}

		order, entryPrice, signedAmt, err := bx.ClosePositionOrder(symbol, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if order == nil {
			c.JSON(http.StatusOK, gin.H{"status": "success"})
			return
		}

		strategyID := ""
		strategyName := ""
		openTime := time.Now()
		prevRealizedPnL := 0.0
		prevRealizedNotional := 0.0
		if hasExisting {
			strategyID = existing.StrategyID
			strategyName = existing.StrategyName
			openTime = existing.OpenTime
			prevRealizedPnL = existing.RealizedPnL
			prevRealizedNotional = existing.RealizedNotional
			if entryPrice == 0 {
				entryPrice = existing.AvgPrice
			}
		} else {
			strategyID, strategyName = findStrategyForSymbol(symbol)
		}
		if strategyID == "" {
			strategyID = "manual"
			strategyName = "manual"
		}

		qty := order.Amount
		exitPrice := order.Price
		realized := 0.0
		if signedAmt >= 0 {
			realized = qty * (exitPrice - entryPrice)
		} else {
			realized = qty * (entryPrice - exitPrice)
		}
		realizedNotional := qty * entryPrice

		now := time.Now()
		database.DB.Create(&models.StrategyOrder{
			StrategyID:      strategyID,
			StrategyName:    strategyName,
			OwnerID:         uid,
			Exchange:        bx.GetName(),
			Symbol:          symbol,
			Side:            strings.ToLower(order.Side),
			OrderType:       "market",
			ClientOrderID:   order.ClientOrderID,
			ExchangeOrderID: order.ID,
			Status:          order.Status,
			RequestedQty:    qty,
			Price:           0,
			ExecutedQty:     qty,
			AvgPrice:        exitPrice,
			RequestedAt:     now,
			UpdatedAt:       now,
		})

		if hasExisting {
			newClosedQty := existing.ClosedQty + qty
			newAvgClose := existing.AvgClosePrice
			if newClosedQty > 0 {
				newAvgClose = ((existing.AvgClosePrice * existing.ClosedQty) + (exitPrice * qty)) / newClosedQty
			}
			database.DB.Model(&models.StrategyPosition{}).Where("id = ?", existing.ID).
				Updates(map[string]interface{}{
					"amount":            0,
					"avg_price":         entryPrice,
					"closed_qty":        newClosedQty,
					"avg_close_price":   newAvgClose,
					"realized_pn_l":     prevRealizedPnL + realized,
					"realized_notional": prevRealizedNotional + realizedNotional,
					"status":            "closed",
					"close_time":        order.Timestamp,
					"updated_at":        now,
				})
		} else {
			database.DB.Create(&models.StrategyPosition{
				StrategyID:       strategyID,
				StrategyName:     strategyName,
				OwnerID:          uid,
				Exchange:         bx.GetName(),
				Symbol:           symbol,
				Amount:           0,
				AvgPrice:         entryPrice,
				ClosedQty:        qty,
				AvgClosePrice:    exitPrice,
				RealizedPnL:      realized,
				RealizedNotional: realizedNotional,
				Status:           "closed",
				OpenTime:         openTime,
				CloseTime:        order.Timestamp,
				UpdatedAt:        now,
			})
		}

		if strategyID != "" && strategyID != "manual" {
			notify := *order
			notify.Symbol = symbol
			notify.Side = strings.ToLower(notify.Side)
			if notify.Amount <= 0 {
				notify.Amount = qty
			}
			if notify.Price <= 0 {
				notify.Price = exitPrice
			}
			notify.Status = strings.ToLower(strings.TrimSpace(notify.Status))
			if notify.Status == "" || notify.Status == "new" || notify.Status == "open" || notify.Status == "partially_filled" || notify.Status == "requested" {
				notify.Status = "filled"
			}
			_ = stratMgr.SendToStrategy(strategyID, "order", &notify)
		}

		c.JSON(http.StatusOK, gin.H{"status": "success"})
		return
	}
	var pos models.StrategyPosition
	if err := database.DB.Where("owner_id = ? AND symbol = ? AND status = ?", userID.(uint), symbol, "open").
		Order("open_time desc").
		First(&pos).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Open position not found"})
		return
	}

	clientOrderID := models.GenerateUUID()
	database.DB.Create(&models.StrategyOrder{
		StrategyID:    pos.StrategyID,
		StrategyName:  pos.StrategyName,
		OwnerID:       userID.(uint),
		Exchange:      pos.Exchange,
		Symbol:        pos.Symbol,
		Side:          "sell",
		OrderType:     "market",
		ClientOrderID: clientOrderID,
		Status:        "requested",
		RequestedQty:  pos.Amount,
		Price:         0,
		RequestedAt:   time.Now(),
		UpdatedAt:     time.Now(),
	})

	order, err := stratMgr.GetExchange().PlaceOrder(userID.(uint), clientOrderID, pos.Symbol, "sell", pos.Amount, 0)
	if err != nil {
		database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
			Updates(map[string]interface{}{"status": "failed", "updated_at": time.Now()})
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
		Updates(map[string]interface{}{
			"exchange_order_id": order.ID,
			"status":            order.Status,
			"updated_at":        time.Now(),
		})

	if pos.StrategyID != "" && pos.StrategyID != "manual" {
		notify := *order
		notify.Side = strings.ToLower(notify.Side)
		if notify.Amount <= 0 {
			notify.Amount = pos.Amount
		}
		notify.Status = strings.ToLower(strings.TrimSpace(notify.Status))
		if notify.Status == "" || notify.Status == "new" || notify.Status == "open" || notify.Status == "partially_filled" || notify.Status == "requested" {
			notify.Status = "filled"
		}
		_ = stratMgr.SendToStrategy(pos.StrategyID, "order", &notify)
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func StartStrategy(c *gin.Context) {

	id := c.Param("id")
	if err := stratMgr.StartStrategy(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "started"})
}

type BacktestRequest struct {
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	InitialBalance float64   `json:"initial_balance"`
}

func BacktestStrategy(c *gin.Context) {
	// BacktestStrategy triggers a backtest run.
	// async=true runs it in background and stores progress/results in DB and via websocket events.
	id := c.Param("id")
	async := c.Query("async") == "true"
	var req BacktestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.StartTime.IsZero() {
		req.StartTime = time.Now().AddDate(0, 0, -7) // Default 7 days ago
	}
	if req.EndTime.IsZero() {
		req.EndTime = time.Now()
	}
	if req.InitialBalance <= 0 {
		req.InitialBalance = 10000.0 // Default 10000 USDT
	}

	userID, _ := c.Get("user_id")

	if async {
		taskID, err := stratMgr.StartBacktest(id, req.StartTime, req.EndTime, req.InitialBalance, userID.(uint))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "queued", "task_id": taskID})
		return
	}

	result, err := stratMgr.Backtest(id, req.StartTime, req.EndTime, req.InitialBalance, userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func ListBacktests(c *gin.Context) {
	strategyID := c.Query("strategy_id")
	userID, _ := c.Get("user_id")

	var backtests []models.Backtest
	query := database.DB.Where("user_id = ?", userID)
	if strategyID != "" {
		query = query.Where("strategy_id = ?", strategyID)
	}
	query.Order("created_at desc").Find(&backtests)

	c.JSON(http.StatusOK, backtests)
}

func GetBacktest(c *gin.Context) {
	id := c.Param("id")
	userID, _ := c.Get("user_id")

	var bt models.Backtest
	if err := database.DB.Where("id = ? AND user_id = ?", id, userID).First(&bt).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backtest not found"})
		return
	}

	c.JSON(http.StatusOK, bt)
}

type UpdateConfigRequest struct {
	Config string `json:"config" binding:"required"`
}

func UpdateStrategyConfig(c *gin.Context) {
	id := c.Param("id")
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Permission check
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")
	var instance models.StrategyInstance
	if err := database.DB.Where("id = ?", id).First(&instance).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Strategy not found"})
		return
	}
	if instance.OwnerID != userID.(uint) && userRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(req.Config), &config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON config"})
		return
	}

	if err := stratMgr.UpdateStrategyConfig(id, config); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Update DB
	instance.Config = req.Config
	database.DB.Save(&instance)

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func GetStrategyLogs(c *gin.Context) {
	id := c.Param("id")
	var logs []models.StrategyLog
	database.DB.Where("strategy_id = ?", id).Order("created_at desc").Limit(100).Find(&logs)
	c.JSON(http.StatusOK, logs)
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

func validateStrategyCode(code string) []string {
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
	templateType := strings.ToLower(strings.TrimSpace(req.TemplateType))
	if templateType == "" {
		templateType = "strategy"
	}
	if templateType != "strategy" && templateType != "selector" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模板类型必须是 strategy 或 selector"})
		return
	}

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

	// Save code to file
	filename := fmt.Sprintf("%s_%d.py", req.Name, userID)
	filename = strings.ReplaceAll(filename, " ", "_")
	filename = filepath.Base(filename)
	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	absStrategiesDir, err := filepath.Abs(strategiesDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to resolve strategies dir. err: %v", err)})
		return
	}
	if err := os.MkdirAll(absStrategiesDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create strategies dir. err: %v", err)})
		return
	}
	absPath := filepath.Join(absStrategiesDir, filename)

	if err := os.WriteFile(absPath, []byte(req.Code), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save code file. err: %v", err)})
		return
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
	templateType := strings.ToLower(strings.TrimSpace(req.TemplateType))
	if templateType == "" {
		templateType = "strategy"
	}
	if templateType != "strategy" && templateType != "selector" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模板类型必须是 strategy 或 selector"})
		return
	}

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

	filename := fmt.Sprintf("%s_%d.py", req.Name, template.AuthorID)
	filename = strings.ReplaceAll(filename, " ", "_")
	filename = filepath.Base(filename)
	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	absStrategiesDir, err := filepath.Abs(strategiesDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to resolve strategies dir. err: %v", err)})
		return
	}
	if err := os.MkdirAll(absStrategiesDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create strategies dir. err: %v", err)})
		return
	}
	absPath := filepath.Join(absStrategiesDir, filename)
	if err := os.WriteFile(absPath, []byte(req.Code), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save code file. err: %v", err)})
		return
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

func StopStrategy(c *gin.Context) {
	id := c.Param("id")
	force := c.Query("force") == "true"
	if err := stratMgr.StopStrategy(id, force); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

func DeleteStrategy(c *gin.Context) {
	id := c.Param("id")
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")

	var instance models.StrategyInstance
	if err := database.DB.Where("id = ?", id).First(&instance).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Strategy not found"})
		return
	}

	// Permission check: owner or admin
	if instance.OwnerID != userID.(uint) && userRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	if err := database.DB.Delete(&instance).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete from DB"})
		return
	}

	stratMgr.RemoveStrategy(id)
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
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
