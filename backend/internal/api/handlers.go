package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"quanty_trade/internal/auth"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
	"quanty_trade/internal/secure"
	"quanty_trade/internal/strategy"

	"github.com/gin-gonic/gin"
)

var stratMgr *strategy.Manager

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
			CreatedAt:  time.Now(),
		}

		// Save to DB asynchronously to avoid blocking the request
		go func(entry models.APILog) {
			database.DB.Create(&entry)
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
	status := c.DefaultQuery("status", "active") // active or closed
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")

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
	if err := query.Order("open_time desc").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	positions := make([]exchange.Position, 0, len(rows))
	for _, p := range rows {
		pos := exchange.Position{
			Symbol:       p.Symbol,
			Amount:       p.Amount,
			Price:        p.AvgPrice,
			StrategyName: p.StrategyName,
			ExchangeName: p.Exchange,
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
	c.JSON(http.StatusOK, positions)
}

func ClosePosition(c *gin.Context) {
	symbol := c.Query("symbol")
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Symbol is required"})
		return
	}

	userID, _ := c.Get("user_id")
	var pos models.StrategyPosition
	if err := database.DB.Where("owner_id = ? AND symbol = ? AND status = ?", userID.(uint), symbol, "open").
		Order("open_time desc").
		First(&pos).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Open position not found"})
		return
	}

	clientOrderID := fmt.Sprintf("qt_close_%d_%d", userID.(uint), time.Now().UnixNano())
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
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Code        string `json:"code" binding:"required"`
	IsDraft     bool   `json:"is_draft"`
}

func SaveTemplate(c *gin.Context) {
	var req SaveTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")

	// Find if existing template with same name by same author
	var template models.StrategyTemplate
	err := database.DB.Where("name = ? AND author_id = ?", req.Name, userID.(uint)).First(&template).Error

	// Save code to file
	filename := fmt.Sprintf("%s_%d.py", req.Name, userID.(uint))
	filename = strings.ReplaceAll(filename, " ", "_")
	filename = filepath.Base(filename)
	strategiesDir := os.Getenv("STRATEGIES_DIR")
	if strategiesDir == "" {
		strategiesDir = filepath.Join("..", "strategies")
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
		template.Description = req.Description
		template.Code = req.Code
		template.IsDraft = req.IsDraft
		template.Path = absPath
		database.DB.Save(&template)
	} else {
		// Create new
		template = models.StrategyTemplate{
			Name:        req.Name,
			Description: req.Description,
			Path:        absPath,
			AuthorID:    userID.(uint),
			IsPublic:    false,
			IsDraft:     req.IsDraft,
			Code:        req.Code,
		}
		if err := database.DB.Create(&template).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save template"})
			return
		}
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

	c.JSON(http.StatusOK, gin.H{"valid": true})
}

func StopStrategy(c *gin.Context) {
	id := c.Param("id")
	if err := stratMgr.StopStrategy(id); err != nil {
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
	database.DB.Save(&template)

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
