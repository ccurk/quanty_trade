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
		"rows":   rows, // 已按可捕获毛边降序
	}
	if len(rows) > 0 {
		best := rows[0]
		resp["best"] = gin.H{
			"exchange":        best.Exchange,
			"symbol":          best.Symbol,
			"edge_bps":        best.BestEdgeBps(),
			"exec_spread_bps": best.ExecSpreadBps,
		}
	}
	c.JSON(http.StatusOK, resp)
}
