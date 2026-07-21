package api

// Bot/admin-friendly endpoints — 给量化机器人和管理员，让它们能完整管理 strategy。
// 设计原则：每个 endpoint 都通过 writeAudit 自动写一行 strategy_audit_logs。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"

	"github.com/gin-gonic/gin"
)

// ===========================================================================
// 1. Audit helper —— 所有写操作 endpoint 都用它
// ===========================================================================

type auditCtx struct {
	StrategyID string
	OwnerID    uint
	Actor      string
	ActorID    uint
	Action     string
	Endpoint   string
	Before     interface{}
	After      interface{}
	Summary    string
}

// writeAudit 把一次写操作写到 strategy_audit_logs。
// 失败不阻塞主流程，只 log 一行警告。
func writeAudit(c *gin.Context, ac auditCtx, success bool, status int, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[AUDIT] panic actor=%s action=%s err=%v", ac.Actor, ac.Action, r)
		}
	}()
	if ac.Actor == "" {
		if username, _ := c.Get("username"); username != nil {
			if s, ok := username.(string); ok {
				ac.Actor = s
			}
		}
	}
	if ac.ActorID == 0 {
		if uid, _ := c.Get("user_id"); uid != nil {
			if u, ok := uid.(uint); ok {
				ac.ActorID = u
			}
		}
	}
	beforeJSON := ""
	if ac.Before != nil {
		if b, err := json.Marshal(ac.Before); err == nil {
			beforeJSON = string(b)
		}
	}
	afterJSON := ""
	if ac.After != nil {
		if b, err := json.Marshal(ac.After); err == nil {
			afterJSON = string(b)
		}
	}
	row := models.StrategyAuditLog{
		StrategyID:   ac.StrategyID,
		OwnerID:      ac.OwnerID,
		Actor:        ac.Actor,
		ActorID:      ac.ActorID,
		Action:       ac.Action,
		Endpoint:     ac.Endpoint,
		BeforeJSON:   beforeJSON,
		AfterJSON:    afterJSON,
		Summary:      ac.Summary,
		Success:      success,
		HTTPStatus:   status,
		ErrorMessage: errMsg,
		CreatedAt:    time.Now(),
	}
	if err := database.DB.Create(&row).Error; err != nil {
		logger.Errorf("[AUDIT] insert failed: %v", err)
	}
}

// loadInstanceForBot 查 instance + 权限校验，复用给所有 bot endpoint。
// 失败时它**自己**写 c.JSON 并返回 false。
func loadInstanceForBot(c *gin.Context, id string) (*models.StrategyInstance, bool) {
	var inst models.StrategyInstance
	if err := database.DB.Where("id = ?", id).First(&inst).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "strategy not found"})
		return nil, false
	}
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")
	if inst.OwnerID != userID.(uint) && userRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return nil, false
	}
	return &inst, true
}

// parseInstanceConfig 拿 instance.Config 字符串 → map。
func parseInstanceConfig(inst *models.StrategyInstance) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	if strings.TrimSpace(inst.Config) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(inst.Config), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// saveMergedConfig 把改完的 config map 走和 PUT 同一条规整化路径回写。
func saveMergedConfig(inst *models.StrategyInstance, merged map[string]interface{}) error {
	b, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	configJSON, normCfg, err := normalizeStrategyConfigJSON(string(b))
	if err != nil {
		return fmt.Errorf("normalize failed: %w", err)
	}
	if err := stratMgr.UpdateStrategyConfig(inst.ID, normCfg); err != nil {
		return fmt.Errorf("strategy manager update failed: %w", err)
	}
	inst.Config = configJSON
	return database.DB.Save(inst).Error
}

// ===========================================================================
// 2. DELETE /api/strategies/:id/blacklist/:symbol
//    从 config.symbol_blacklist 删除单个 symbol，bot 用来"释放"误黑名单的币。
// ===========================================================================

func RemoveSymbolFromBlacklist(c *gin.Context) {
	id := c.Param("id")
	symbol := strings.TrimSpace(c.Param("symbol"))
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol required"})
		return
	}

	inst, ok := loadInstanceForBot(c, id)
	if !ok {
		return
	}

	cfg, err := parseInstanceConfig(inst)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config parse: " + err.Error()})
		return
	}

	rawList, _ := cfg["symbol_blacklist"].([]interface{})
	oldList := make([]string, 0, len(rawList))
	for _, v := range rawList {
		if s, ok := v.(string); ok {
			oldList = append(oldList, s)
		}
	}

	newList := make([]interface{}, 0, len(oldList))
	removed := false
	for _, s := range oldList {
		if strings.EqualFold(strings.TrimSpace(s), symbol) {
			removed = true
			continue
		}
		newList = append(newList, s)
	}

	if !removed {
		c.JSON(http.StatusOK, gin.H{
			"status":    "no_change",
			"symbol":    symbol,
			"blacklist": oldList,
		})
		return
	}

	cfg["symbol_blacklist"] = newList
	if err := saveMergedConfig(inst, cfg); err != nil {
		status := configUpdateErrStatus(err)
		writeAudit(c, auditCtx{
			StrategyID: inst.ID, OwnerID: inst.OwnerID,
			Action:   "blacklist_remove",
			Endpoint: "DELETE /strategies/:id/blacklist/:symbol",
			Summary:  fmt.Sprintf("attempted remove %s, failed", symbol),
		}, false, status, err.Error())
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	writeAudit(c, auditCtx{
		StrategyID: inst.ID, OwnerID: inst.OwnerID,
		Action:   "blacklist_remove",
		Endpoint: "DELETE /strategies/:id/blacklist/:symbol",
		Before:   map[string]interface{}{"symbol_blacklist": oldList},
		After:    map[string]interface{}{"symbol_blacklist": newList},
		Summary:  fmt.Sprintf("removed %s from blacklist", symbol),
	}, true, http.StatusOK, "")

	c.JSON(http.StatusOK, gin.H{
		"status":           "removed",
		"symbol":           symbol,
		"blacklist_before": oldList,
		"blacklist_after":  newList,
	})
}

// ===========================================================================
// 3. POST /api/strategies/:id/rollback
//    回滚到上一版 StrategyTemplate。
//    需要 instance 之前有过 ApplyOptimization（StrategyOptimizationRun 里存了
//    previous_template_id）。
// ===========================================================================

type rollbackRequest struct {
	// Steps 表示往前回滚几代（默认 1）。
	Steps int `json:"steps"`
	// TargetTemplateID 优先级最高：直接指定 rollback 到哪个 template_id。
	TargetTemplateID uint `json:"target_template_id"`
}

func RollbackStrategyTemplate(c *gin.Context) {
	id := c.Param("id")

	var req rollbackRequest
	_ = c.ShouldBindJSON(&req) // body 可空，全用默认值

	inst, ok := loadInstanceForBot(c, id)
	if !ok {
		return
	}

	targetID := req.TargetTemplateID
	currentTID := inst.TemplateID

	if targetID == 0 {
		steps := req.Steps
		if steps <= 0 {
			steps = 1
		}
		// 在 StrategyOptimizationRun 里找最近 N 次 applied 历史
		// 每次往前一步：current TID 对应的 PreviousTemplateID 就是上一代
		var runs []models.StrategyOptimizationRun
		if err := database.DB.Where("strategy_id = ? AND applied = ?", id, true).
			Order("created_at desc").
			Limit(steps + 5).
			Find(&runs).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// 从当前 templateID 向后回溯 steps 步
		cursor := currentTID
		for i := 0; i < steps; i++ {
			matched := false
			for _, r := range runs {
				if r.NewTemplateID == cursor && r.PreviousTemplateID != 0 {
					cursor = r.PreviousTemplateID
					matched = true
					break
				}
			}
			if !matched {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":      "no further history",
					"steps_done": i,
					"hint":       "instance has no recorded previous_template_id beyond this point",
				})
				return
			}
		}
		targetID = cursor
	}

	if targetID == currentTID {
		c.JSON(http.StatusOK, gin.H{"status": "no_change", "template_id": currentTID})
		return
	}

	// 确认目标 template 还存在
	var target models.StrategyTemplate
	if err := database.DB.Where("id = ?", targetID).First(&target).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("target template %d not found", targetID)})
		return
	}

	// 切 template_id + 解绑 version（让它跟随新 template）
	now := time.Now()
	if err := database.DB.Model(&models.StrategyInstance{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"template_id":         targetID,
			"strategy_version_id": nil,
			"updated_at":          now,
		}).Error; err != nil {
		writeAudit(c, auditCtx{
			StrategyID: inst.ID, OwnerID: inst.OwnerID,
			Action:   "rollback",
			Endpoint: "POST /strategies/:id/rollback",
			Before:   map[string]interface{}{"template_id": currentTID},
			After:    map[string]interface{}{"template_id": targetID},
			Summary:  "rollback DB update failed",
		}, false, http.StatusInternalServerError, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 记录一行 OptimizationRun 让下次还能再 rollback 回来
	_ = database.DB.Create(&models.StrategyOptimizationRun{
		StrategyID:         id,
		OwnerID:            inst.OwnerID,
		Status:             "applied",
		Trigger:            "rollback",
		Model:              "manual_rollback",
		Applied:            true,
		PreviousTemplateID: currentTID,
		NewTemplateID:      targetID,
		Summary:            fmt.Sprintf("rollback %d -> %d", currentTID, targetID),
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error

	// 如果 strategy 在跑则异步重启
	wasRunning := inst.Status == "running" || inst.Status == "starting"
	if wasRunning && stratMgr != nil {
		go func() {
			defer func() { _ = recover() }()
			_ = stratMgr.StopStrategy(id, true)
			time.Sleep(2 * time.Second)
			_ = stratMgr.StartStrategy(id)
		}()
	}

	writeAudit(c, auditCtx{
		StrategyID: inst.ID, OwnerID: inst.OwnerID,
		Action:   "rollback",
		Endpoint: "POST /strategies/:id/rollback",
		Before:   map[string]interface{}{"template_id": currentTID},
		After:    map[string]interface{}{"template_id": targetID},
		Summary:  fmt.Sprintf("rollback %d -> %d (restart=%v)", currentTID, targetID, wasRunning),
	}, true, http.StatusOK, "")

	c.JSON(http.StatusOK, gin.H{
		"status":               "rolled_back",
		"previous_template_id": currentTID,
		"new_template_id":      targetID,
		"target_template_name": target.Name,
		"restart":              wasRunning,
	})
}

// ===========================================================================
// 4. POST /api/strategies/:id/cancel-orders
//    取消该 strategy 在 binance 的所有未成交委托（含 TP/SL 算法单）。
//    给 routine 的"紧急刹车"用：账户异常时一次性撤干净。
//    注意：不平仓，只取消委托。
// ===========================================================================

func CancelStrategyOrders(c *gin.Context) {
	id := c.Param("id")
	inst, ok := loadInstanceForBot(c, id)
	if !ok {
		return
	}

	bx, ok2 := stratMgr.GetExchange().(*exchange.BinanceExchange)
	if !ok2 || bx.Market() != "usdm" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only USDM binance supported"})
		return
	}

	// 收集策略涉及的所有 symbol：当前持仓 + 已提交未平掉的 position rows
	symSet := map[string]struct{}{}
	var positions []models.StrategyPosition
	_ = database.DB.Where("owner_id = ? AND strategy_id = ?", inst.OwnerID, id).
		Find(&positions).Error
	for _, p := range positions {
		if strings.TrimSpace(p.Symbol) != "" {
			symSet[p.Symbol] = struct{}{}
		}
	}

	if len(symSet) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"status":          "no_change",
			"symbols_checked": 0,
			"hint":            "no positions to derive symbols from",
		})
		return
	}

	type symResult struct {
		Symbol         string `json:"symbol"`
		NormalCanceled int    `json:"normal_canceled"`
		AlgoCanceled   int    `json:"algo_canceled"`
		Error          string `json:"error,omitempty"`
	}
	results := []symResult{}
	totalCanceled := 0
	for sym := range symSet {
		r := symResult{Symbol: sym}
		if sum, err := bx.CancelUSDMAllSymbolOrdersDetailed(inst.OwnerID, sym); err != nil {
			r.Error = err.Error()
		} else {
			r.NormalCanceled = sum.NormalCanceled
			r.AlgoCanceled = sum.AlgoCanceled
			totalCanceled += sum.NormalCanceled + sum.AlgoCanceled
		}
		results = append(results, r)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Symbol < results[j].Symbol })

	writeAudit(c, auditCtx{
		StrategyID: inst.ID, OwnerID: inst.OwnerID,
		Action:   "cancel_orders",
		Endpoint: "POST /strategies/:id/cancel-orders",
		After:    results,
		Summary:  fmt.Sprintf("canceled %d orders across %d symbols", totalCanceled, len(symSet)),
	}, true, http.StatusOK, "")

	c.JSON(http.StatusOK, gin.H{
		"status":          "canceled",
		"total_canceled":  totalCanceled,
		"symbols_checked": len(symSet),
		"results":         results,
	})
}

// ===========================================================================
// 5. PATCH /api/strategies/:id
//    改 instance 顶层字段（name / description-暂无字段所以仅 name / 等）。
//    严格白名单：只接受少数安全字段。owner_id/template_id/exchange_id 不允许。
// ===========================================================================

type patchTopRequest struct {
	Name *string `json:"name,omitempty"`
	// 注：StrategyInstance model 上没有独立 description 字段，
	// 描述存在 template 上；这里先只支持 name，后续要加再扩。
}

func PatchStrategyMeta(c *gin.Context) {
	id := c.Param("id")

	var req patchTopRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	inst, ok := loadInstanceForBot(c, id)
	if !ok {
		return
	}

	changes := map[string]interface{}{}
	before := map[string]interface{}{}

	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name cannot be empty"})
			return
		}
		if len(trimmed) > 128 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name too long (max 128)"})
			return
		}
		if trimmed != inst.Name {
			before["name"] = inst.Name
			changes["name"] = trimmed
		}
	}

	if len(changes) == 0 {
		c.JSON(http.StatusOK, gin.H{"status": "no_change"})
		return
	}

	changes["updated_at"] = time.Now()
	if err := database.DB.Model(&models.StrategyInstance{}).Where("id = ?", id).Updates(changes).Error; err != nil {
		writeAudit(c, auditCtx{
			StrategyID: inst.ID, OwnerID: inst.OwnerID,
			Action:   "patch_meta",
			Endpoint: "PATCH /strategies/:id",
			Before:   before, After: changes,
			Summary: "patch meta failed",
		}, false, http.StatusInternalServerError, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	writeAudit(c, auditCtx{
		StrategyID: inst.ID, OwnerID: inst.OwnerID,
		Action:   "patch_meta",
		Endpoint: "PATCH /strategies/:id",
		Before:   before, After: changes,
		Summary: "patched meta",
	}, true, http.StatusOK, "")

	c.JSON(http.StatusOK, gin.H{"status": "patched", "changed": changes})
}

// ===========================================================================
// 6. GET /api/admin/strategies/:id/audit
//    给前端/bot 看自己的 audit log。最近 N 条。
// ===========================================================================

func ListStrategyAudit(c *gin.Context) {
	id := c.Param("id")
	if _, ok := loadInstanceForBot(c, id); !ok {
		return
	}

	limit := 50
	if v := c.Query("limit"); v != "" {
		var n int
		_, _ = fmt.Sscanf(v, "%d", &n)
		if n > 0 && n <= 500 {
			limit = n
		}
	}

	var rows []models.StrategyAuditLog
	if err := database.DB.Where("strategy_id = ?", id).
		Order("created_at desc").
		Limit(limit).
		Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"audit_logs": rows, "count": len(rows)})
}

// ===========================================================================
// 7. POST /api/admin/strategies/:id/export
//    导出一份可移植的 JSON bundle：template code + config + 一些 metadata。
//    用于一键迁移到另一台服务器/账号。
//    注意：不包含 user credentials（exchange API key）。
// ===========================================================================

type strategyBundle struct {
	BundleVersion   int                    `json:"bundle_version"`
	ExportedAt      time.Time              `json:"exported_at"`
	StrategyName    string                 `json:"strategy_name"`
	TemplateName    string                 `json:"template_name"`
	TemplateCode    string                 `json:"template_code"`
	TemplateType    string                 `json:"template_type"`
	TemplateDesc    string                 `json:"template_description"`
	Config          map[string]interface{} `json:"config"`
	Status          string                 `json:"status_at_export"`
	StrategyVersion *uint                  `json:"strategy_version_id_at_export,omitempty"`
	// 注：不导出 owner_id / 不导出 created_at；导入端会创建新的。
}

func ExportStrategy(c *gin.Context) {
	id := c.Param("id")
	inst, ok := loadInstanceForBot(c, id)
	if !ok {
		return
	}

	var tpl models.StrategyTemplate
	if err := database.DB.Where("id = ?", inst.TemplateID).First(&tpl).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "template not found: " + err.Error()})
		return
	}

	cfg, _ := parseInstanceConfig(inst)
	bundle := strategyBundle{
		BundleVersion:   1,
		ExportedAt:      time.Now(),
		StrategyName:    inst.Name,
		TemplateName:    tpl.Name,
		TemplateCode:    tpl.Code,
		TemplateType:    tpl.TemplateType,
		TemplateDesc:    tpl.Description,
		Config:          cfg,
		Status:          inst.Status,
		StrategyVersion: inst.StrategyVersionID,
	}

	writeAudit(c, auditCtx{
		StrategyID: inst.ID, OwnerID: inst.OwnerID,
		Action:   "export",
		Endpoint: "POST /admin/strategies/:id/export",
		Summary:  fmt.Sprintf("exported strategy %s", inst.Name),
	}, true, http.StatusOK, "")

	c.JSON(http.StatusOK, bundle)
}

// ===========================================================================
// 8. POST /api/admin/strategies/import
//    导入 strategyBundle，创建新 StrategyTemplate + 新 StrategyInstance。
//    body: 直接放上面 export 的 JSON。
//    可选 query：?name=NewName → 覆盖 bundle 里的 strategy_name
// ===========================================================================

func ImportStrategy(c *gin.Context) {
	var bundle strategyBundle
	if err := c.ShouldBindJSON(&bundle); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid bundle: " + err.Error()})
		return
	}
	if bundle.BundleVersion != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unsupported bundle_version=%d (only v1)", bundle.BundleVersion)})
		return
	}
	if strings.TrimSpace(bundle.TemplateCode) == "" || strings.TrimSpace(bundle.TemplateName) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "template_code and template_name required"})
		return
	}

	// override name 如果传了
	if v := strings.TrimSpace(c.Query("name")); v != "" {
		bundle.StrategyName = v
	}
	if strings.TrimSpace(bundle.StrategyName) == "" {
		bundle.StrategyName = bundle.TemplateName + "_imported_" + time.Now().Format("0102-1504")
	}

	uidVal, _ := c.Get("user_id")
	uid, _ := uidVal.(uint)
	if uid == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "no user_id in context"})
		return
	}

	// 处理 template name 冲突：加 _imported_<ts> 后缀
	tplName := bundle.TemplateName
	var existing models.StrategyTemplate
	if database.DB.Where("name = ?", tplName).First(&existing).Error == nil {
		tplName = fmt.Sprintf("%s_imported_%s", tplName, time.Now().Format("20060102-150405"))
	}

	newTpl := models.StrategyTemplate{
		Name:         tplName,
		Description:  bundle.TemplateDesc,
		Code:         bundle.TemplateCode,
		Path:         fmt.Sprintf("db://template/%s", tplName),
		TemplateType: bundle.TemplateType,
		AuthorID:     uid,
		IsDraft:      false,
		IsEnabled:    true,
	}
	if newTpl.TemplateType == "" {
		newTpl.TemplateType = "strategy"
	}

	if err := database.DB.Create(&newTpl).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create template: " + err.Error()})
		return
	}

	// 规整化 config（走和 PUT 同一路径）
	cfgBytes, _ := json.Marshal(bundle.Config)
	configJSON, _, err := normalizeStrategyConfigJSON(string(cfgBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config normalize: " + err.Error()})
		return
	}

	// 创建 instance；用 models 包里现有的 UUID 生成器
	newID := models.GenerateUUID()
	newInst := models.StrategyInstance{
		ID:         newID,
		Name:       bundle.StrategyName,
		TemplateID: newTpl.ID,
		OwnerID:    uid,
		Config:     configJSON,
		Status:     "stopped",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := database.DB.Create(&newInst).Error; err != nil {
		// 创建 instance 失败就回滚 template（避免孤儿）
		_ = database.DB.Delete(&newTpl).Error
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create instance: " + err.Error()})
		return
	}

	writeAudit(c, auditCtx{
		StrategyID: newInst.ID, OwnerID: uid,
		Action:   "import",
		Endpoint: "POST /admin/strategies/import",
		After:    map[string]interface{}{"template_id": newTpl.ID, "instance_id": newID, "name": bundle.StrategyName},
		Summary:  fmt.Sprintf("imported strategy %s (from bundle %s)", bundle.StrategyName, bundle.TemplateName),
	}, true, http.StatusOK, "")

	c.JSON(http.StatusOK, gin.H{
		"status":         "imported",
		"strategy_id":    newID,
		"strategy_name":  bundle.StrategyName,
		"template_id":    newTpl.ID,
		"template_name":  newTpl.Name,
		"status_default": "stopped",
	})
}

