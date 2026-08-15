package api

import (
	"net/http"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"

	"github.com/gin-gonic/gin"
)

// ModulePnL is one trading-method's realized PnL over three windows.
type ModulePnL struct {
	Current float64 `json:"current"` // 今日已实现
	Month   float64 `json:"month"`   // 本月已实现
	Total   float64 `json:"total"`   // 累计已实现
	// Status: "trading" | "detector-only" | "disabled" — honest state so the UI
	// can show WHY a module reads 0 (not-yet-trading vs genuinely flat).
	Status string `json:"status"`
}

// ModulesPnLResponse breaks realized PnL out by the three trading methods.
// Only quant trades today; triarb is Phase-1 read-only (no orders) and market
// making is not enabled — both report 0 with an honest status until they trade
// and record PnL of their own.
type ModulesPnLResponse struct {
	UpdatedAt    time.Time `json:"updated_at"`
	Quant        ModulePnL `json:"quant"`
	Triarb       ModulePnL `json:"triarb"`
	MarketMaking ModulePnL `json:"marketmaking"`
}

// GetModulesPnL powers the 3-module dashboard (量化 / 三角套利 / 做市).
// GET /stats/modules-pnl
func GetModulesPnL(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := userID.(uint)

	now := time.Now()
	loc := now.Location()
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	startMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

	sumRealized := func(start *time.Time) float64 {
		q := database.DB.Model(&models.StrategyPosition{}).
			Where("owner_id = ? AND status = ?", uid, "closed")
		if start != nil {
			q = q.Where("close_time >= ?", *start)
		}
		var r struct{ V float64 }
		_ = q.Select("COALESCE(SUM(realized_pn_l), 0) AS v").Scan(&r).Error
		return r.V
	}

	resp := ModulesPnLResponse{
		UpdatedAt: now,
		Quant: ModulePnL{
			Current: sumRealized(&startDay),
			Month:   sumRealized(&startMonth),
			Total:   sumRealized(nil),
			Status:  "trading",
		},
		// 三角套利:Phase-1 只读检测器,不下单 → 无已实现 PnL(接 Phase-2 执行后再接数据源)。
		Triarb: ModulePnL{Status: "detector-only"},
		// 做市:模块默认禁用,未成交 → 0(实盘记账接上后从做市成交汇总)。
		MarketMaking: ModulePnL{Status: "disabled"},
	}
	c.JSON(http.StatusOK, resp)
}
