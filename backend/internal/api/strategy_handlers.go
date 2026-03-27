package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"

	"github.com/gin-gonic/gin"
)

type CreateStrategyRequest struct {
	Name       string `json:"name" binding:"required"`
	TemplateID uint   `json:"template_id" binding:"required"`
	Config     string `json:"config" binding:"required"`
}

type BacktestRequest struct {
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	InitialBalance float64   `json:"initial_balance"`
}

type UpdateConfigRequest struct {
	Config string `json:"config" binding:"required"`
}

func ListStrategies(c *gin.Context) {
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")
	isAdmin := userRole == "admin"
	c.JSON(http.StatusOK, stratMgr.ListStrategies(userID.(uint), isAdmin))
}

func CreateStrategy(c *gin.Context) {
	var req CreateStrategyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	configJSON, config, err := normalizeStrategyConfigJSON(req.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON config"})
		return
	}
	instance := models.StrategyInstance{
		ID:         models.GenerateUUID(),
		Name:       req.Name,
		TemplateID: req.TemplateID,
		OwnerID:    userID.(uint),
		Config:     configJSON,
		Status:     "stopped",
	}

	if err := database.DB.Create(&instance).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create strategy"})
		return
	}
	var template models.StrategyTemplate
	if err := database.DB.First(&template, instance.TemplateID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template not found"})
		return
	}
	if strings.TrimSpace(template.Path) == "" {
		template.Path = fmt.Sprintf("db://template/%s_%d", strings.ReplaceAll(template.Name, " ", "_"), template.AuthorID)
		_ = database.DB.Model(&models.StrategyTemplate{}).Where("id = ?", template.ID).
			Updates(map[string]interface{}{"path": template.Path, "updated_at": time.Now()}).Error
	}

	stratMgr.AddStrategy(instance.ID, instance.Name, template.Path, userID.(uint), config)

	c.JSON(http.StatusOK, instance)
}

func StartStrategy(c *gin.Context) {
	id := c.Param("id")
	if err := stratMgr.StartStrategy(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "queued", "runtime_status": "starting"})
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
		req.StartTime = time.Now().AddDate(0, 0, -7)
	}
	if req.EndTime.IsZero() {
		req.EndTime = time.Now()
	}
	if req.InitialBalance <= 0 {
		req.InitialBalance = 10000.0
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

func UpdateStrategyConfig(c *gin.Context) {
	id := c.Param("id")
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

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

	configJSON, config, err := normalizeStrategyConfigJSON(req.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON config"})
		return
	}

	if err := stratMgr.UpdateStrategyConfig(id, config); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	instance.Config = configJSON
	database.DB.Save(&instance)

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func GetStrategyLogs(c *gin.Context) {
	id := c.Param("id")
	var logs []models.StrategyLog
	database.DB.Where("strategy_id = ?", id).Order("created_at desc").Limit(100).Find(&logs)
	c.JSON(http.StatusOK, logs)
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
