package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
	"quanty_trade/internal/selector"

	"github.com/gin-gonic/gin"
)

var selectorMgr *selector.Manager

func SetSelectorManager(m *selector.Manager) {
	selectorMgr = m
}

type CreateSelectorRequest struct {
	Name             string `json:"name" binding:"required"`
	ExecutorTemplate uint   `json:"executor_template_id" binding:"required"`
	Config           string `json:"config" binding:"required"`
}

type UpdateSelectorRequest struct {
	Name             string `json:"name" binding:"required"`
	ExecutorTemplate uint   `json:"executor_template_id" binding:"required"`
	Config           string `json:"config" binding:"required"`
}

func ListSelectors(c *gin.Context) {
	userIDValue, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	uid, ok := userIDValue.(uint)
	if !ok || uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	roleValue, _ := c.Get("role")
	role, _ := roleValue.(string)

	var selectors []models.StrategySelector
	q := database.DB.Model(&models.StrategySelector{})
	if role != "admin" {
		q = q.Where("owner_id = ?", uid)
	}
	q.Order("created_at desc").Find(&selectors)
	c.JSON(http.StatusOK, selectors)
}

func UpdateSelector(c *gin.Context) {
	id := c.Param("id")

	var req UpdateSelectorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称不能为空"})
		return
	}
	var tmp map[string]interface{}
	if err := json.Unmarshal([]byte(req.Config), &tmp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "配置必须是合法 JSON"})
		return
	}

	userIDValue, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	uid, ok := userIDValue.(uint)
	if !ok || uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	roleValue, _ := c.Get("role")
	role, _ := roleValue.(string)

	var sel models.StrategySelector
	q := database.DB.Model(&models.StrategySelector{})
	if role != "admin" {
		q = q.Where("owner_id = ?", uid)
	}
	if err := q.Where("id = ?", id).First(&sel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}

	if err := database.DB.Model(&models.StrategySelector{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":              req.Name,
		"executor_template": req.ExecutorTemplate,
		"config":            req.Config,
		"updated_at":        time.Now(),
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if sel.Status == "running" && selectorMgr != nil {
		_ = selectorMgr.Reconcile(id)
	}

	var updated models.StrategySelector
	_ = database.DB.Where("id = ?", id).First(&updated).Error
	c.JSON(http.StatusOK, updated)
}

func CreateSelector(c *gin.Context) {
	var req CreateSelectorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称不能为空"})
		return
	}
	var tmp map[string]interface{}
	if err := json.Unmarshal([]byte(req.Config), &tmp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "配置必须是合法 JSON"})
		return
	}
	userIDValue, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	uid, ok := userIDValue.(uint)
	if !ok || uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	id := models.GenerateUUID()
	now := time.Now()
	row := models.StrategySelector{
		ID:               id,
		Name:             req.Name,
		OwnerID:          uid,
		ExecutorTemplate: req.ExecutorTemplate,
		Config:           req.Config,
		Status:           "stopped",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := database.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, row)
}

func StartSelector(c *gin.Context) {
	id := c.Param("id")
	if err := database.DB.Model(&models.StrategySelector{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":     "running",
		"updated_at": time.Now(),
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if selectorMgr != nil {
		_ = selectorMgr.Reconcile(id)
	}
	c.JSON(http.StatusOK, gin.H{"status": "started"})
}

func StopSelector(c *gin.Context) {
	id := c.Param("id")
	if err := database.DB.Model(&models.StrategySelector{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":     "stopped",
		"updated_at": time.Now(),
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var children []models.StrategySelectorChild
	_ = database.DB.Where("selector_id = ?", id).Find(&children).Error
	for _, ch := range children {
		var openPos int64
		_ = database.DB.Model(&models.StrategyPosition{}).
			Where("strategy_id = ? AND status = ?", ch.StrategyID, "open").
			Count(&openPos).Error
		if openPos > 0 {
			continue
		}
		if stratMgr != nil {
			_ = stratMgr.StopStrategy(ch.StrategyID, false)
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

func ReconcileSelector(c *gin.Context) {
	id := c.Param("id")
	if selectorMgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "selector manager not available"})
		return
	}
	if err := selectorMgr.Reconcile(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func ListSelectorChildren(c *gin.Context) {
	id := c.Param("id")
	var children []models.StrategySelectorChild
	database.DB.Where("selector_id = ?", id).Order("created_at desc").Find(&children)
	c.JSON(http.StatusOK, children)
}
