package api

import (
	"encoding/json"
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

	stratMgr.AddStrategy(instance.ID, instance.Name, template.Path, userID.(uint), template.ID, instance.StrategyVersionID, config)

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

// PatchStrategyConfig 只更新传入的字段，其余字段保持不变（JSON merge patch 语义）。
// 自动化优化路径用：cron / routine 每次只想动 1-2 个字段，但又不想拉全量再回写。
//
// Body 直接就是要 merge 的 object（不需要包 {"config": ...}）：
//   {"order_amount_pct": 0.3, "symbol_blacklist": ["ALLOUSDT", "MOVEUSDT"]}
//
// 显式传 null 会删除字段（标准 JSON merge patch 语义）。
func PatchStrategyConfig(c *gin.Context) {
	id := c.Param("id")

	// 解析 patch（任意 JSON object）
	var patch map[string]interface{}
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON patch: " + err.Error()})
		return
	}
	if len(patch) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty patch"})
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

	// 拉当前 config
	var current map[string]interface{}
	if strings.TrimSpace(instance.Config) != "" {
		if err := json.Unmarshal([]byte(instance.Config), &current); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "current config corrupted: " + err.Error()})
			return
		}
	}
	if current == nil {
		current = map[string]interface{}{}
	}

	// merge：patch 的字段覆盖 current，null 删字段
	changed := make(map[string]interface{}, len(patch))
	for k, v := range patch {
		if v == nil {
			if _, existed := current[k]; existed {
				delete(current, k)
				changed[k] = nil
			}
			continue
		}
		old, hadOld := current[k]
		current[k] = v
		if !hadOld || !jsonEqual(old, v) {
			changed[k] = v
		}
	}

	if len(changed) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"status":  "no_change",
			"changed": map[string]interface{}{},
		})
		return
	}

	// 走和 PUT 同一条规整化路径
	merged, err := json.Marshal(current)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	configJSON, normCfg, err := normalizeStrategyConfigJSON(string(merged))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "merged config invalid: " + err.Error()})
		return
	}

	if err := stratMgr.UpdateStrategyConfig(id, normCfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	instance.Config = configJSON
	if err := database.DB.Save(&instance).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "patched",
		"changed": changed,
	})
}

func jsonEqual(a, b interface{}) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
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

// UnbindStrategyVersion 把实例的 StrategyVersionID 置空，让它回到
// "跟随 Template.Code 最新版本"的状态。下次 stop+start 时 resolveStrategySource
// 会走 template.Code 分支。
//
// 不主动重启实例 —— 调用方决定何时重启（避免 HTTP 请求里隐含 stop/start）。
func UnbindStrategyVersion(c *gin.Context) {
	id := c.Param("id")
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

	var instance models.StrategyInstance
	if err := database.DB.Where("id = ?", id).First(&instance).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Strategy not found"})
		return
	}
	if instance.OwnerID != userID && userRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}
	if instance.StrategyVersionID == nil {
		c.JSON(http.StatusOK, gin.H{
			"status":     "already_unbound",
			"id":         instance.ID,
			"hint":       "实例当前未绑定任何版本，已经在跟随 Template.Code",
			"needs_restart": false,
		})
		return
	}
	prevVersionID := *instance.StrategyVersionID
	// 显式置 NULL —— GORM 对指针字段 Updates 传 nil 才会写入 NULL，
	// Save() 在 zero value 时不会更新指针。
	if err := database.DB.Model(&models.StrategyInstance{}).
		Where("id = ?", instance.ID).
		Update("strategy_version_id", nil).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	needsRestart := instance.Status == "running" || instance.Status == "starting"
	c.JSON(http.StatusOK, gin.H{
		"status":           "unbound",
		"id":               instance.ID,
		"previous_version_id": prevVersionID,
		"current_status":   instance.Status,
		"needs_restart":    needsRestart,
		"hint":             "已解除版本绑定。如实例在运行中，需 stop+start 才会用上最新模板代码。",
	})
}
