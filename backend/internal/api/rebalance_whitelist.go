package api

import (
	"net/http"
	"strings"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
	"quanty_trade/internal/rebalance"

	"github.com/gin-gonic/gin"
)

// ---- 跨所再平衡提现白名单(UI 可配)-------------------------------------
// 这是 App 侧(第二道)守卫:再平衡只会把钱提到这里 Enabled 的地址。
// 第一道、也是硬性的守卫,是交易所自身"仅允许提现到白名单地址"的设置 —— 本表不替代它。
// 写操作限管理员(AdminOnly),并记录 CreatedBy,作为动钱的审计线索。

// ListRebalanceWhitelist GET /rebalance/whitelist —— 列出全部白名单地址。
func ListRebalanceWhitelist(c *gin.Context) {
	var rows []models.RebalanceWhitelist
	if err := database.DB.Order("exchange, asset, network").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

type rebalanceWhitelistReq struct {
	Exchange string `json:"exchange"`
	Asset    string `json:"asset"`
	Network  string `json:"network"`
	Address  string `json:"address"`
	Memo     string `json:"memo"`
	Label    string `json:"label"`
}

// CreateRebalanceWhitelist POST /rebalance/whitelist —— 新增一个提现目标地址。
func CreateRebalanceWhitelist(c *gin.Context) {
	var req rebalanceWhitelistReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误: " + err.Error()})
		return
	}
	ex := strings.ToLower(strings.TrimSpace(req.Exchange))
	asset := strings.ToUpper(strings.TrimSpace(req.Asset))
	network := strings.ToUpper(strings.TrimSpace(req.Network))
	addr := strings.TrimSpace(req.Address)
	if ex == "" || asset == "" || network == "" || addr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "exchange / asset / network / address 均必填(network=链,错链会永久丢钱)"})
		return
	}
	row := models.RebalanceWhitelist{
		Exchange: ex, Asset: asset, Network: network, Address: addr,
		Memo: strings.TrimSpace(req.Memo), Label: strings.TrimSpace(req.Label),
		Enabled: true, CreatedBy: rebalanceActorID(c),
	}
	if err := database.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "创建失败(可能同 所/币/链/地址 已存在): " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"item": row})
}

type rebalanceToggleReq struct {
	Enabled *bool `json:"enabled"`
}

// ToggleRebalanceWhitelist PATCH /rebalance/whitelist/:id —— 启用/停用一条。
func ToggleRebalanceWhitelist(c *gin.Context) {
	id := c.Param("id")
	var req rebalanceToggleReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要布尔字段 enabled"})
		return
	}
	res := database.DB.Model(&models.RebalanceWhitelist{}).Where("id = ?", id).Update("enabled", *req.Enabled)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteRebalanceWhitelist DELETE /rebalance/whitelist/:id —— 软删除一条。
func DeleteRebalanceWhitelist(c *gin.Context) {
	id := c.Param("id")
	if err := database.DB.Delete(&models.RebalanceWhitelist{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// LoadRebalanceWhitelist builds the planner-facing whitelist from the Enabled rows.
// Used by the rebalancer to resolve (and thus permit) a withdrawal destination.
func LoadRebalanceWhitelist() *rebalance.Whitelist {
	var rows []models.RebalanceWhitelist
	database.DB.Where("enabled = ?", true).Find(&rows)
	addrs := make([]rebalance.AllowedAddress, 0, len(rows))
	for _, r := range rows {
		addrs = append(addrs, rebalance.AllowedAddress{
			Exchange: r.Exchange, Asset: r.Asset, Network: r.Network,
			Address: r.Address, Memo: r.Memo, Label: r.Label,
		})
	}
	return rebalance.NewWhitelist(addrs)
}

func rebalanceActorID(c *gin.Context) uint {
	v, ok := c.Get("user_id")
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case uint:
		return t
	case int:
		return uint(t)
	case int64:
		return uint(t)
	case float64:
		return uint(t)
	}
	return 0
}
