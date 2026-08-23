package api

import (
	"net/http"

	"quanty_trade/internal/marketmaker"

	"github.com/gin-gonic/gin"
)

// GetMMObserve powers the dashboard 做市 card: how many pairs are being observed,
// and where the widest capturable (gross) edge currently is. GET /stats/mm-observe
func GetMMObserve(c *gin.Context) {
	rows, running := marketmaker.ObserveSnapshot()
	status := "disabled"
	if running {
		status = "observing"
	}
	resp := gin.H{
		"status": status,
		"pairs":  len(rows),
		"rows":   rows, // 已按净边(扣费后)降序
		"stats":  marketmaker.StatsSnapshot(),
		// observe 阶段不下单、无成交,已实现收益恒为 0。切实盘后由成交撮合 PnL 回填。
		"realized_pnl": 0.0,
	}
	if len(rows) > 0 {
		best := rows[0] // 净边最高的
		resp["best"] = gin.H{
			"exchange":        best.Exchange,
			"symbol":          best.Symbol,
			"edge_bps":        best.BestEdgeBps(),      // 毛边
			"net_edge_bps":    best.NetBestEdgeBps,     // 扣双腿手续费后的净边
			"fee_bps":         best.FeeBps,             // 单腿 maker 费
			"fee_live":        best.FeeLive,            // true=实时费率, false=默认假设
			"exec_spread_bps": best.ExecSpreadBps,
		}
	}
	c.JSON(http.StatusOK, resp)
}
