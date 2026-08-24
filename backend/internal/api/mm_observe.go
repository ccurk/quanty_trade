package api

import (
	"net/http"

	"quanty_trade/internal/marketmaker"

	"github.com/gin-gonic/gin"
)

// GetMMObserve powers the dashboard 做市 card. It now reflects the FULL-MARKET
// universe scan (每轮全市场批量扫描并按净边排序), not a hardcoded pair list:
// pairs = total pairs matched against binance; rows = top N by net edge. GET /stats/mm-observe
func GetMMObserve(c *gin.Context) {
	rows, matched, running := marketmaker.UniverseSnapshot()
	status := "disabled"
	if running {
		status = "observing"
	}
	resp := gin.H{
		"status": status,
		"pairs":  matched,    // 全市场匹配到的对数(跨所,动态)
		"shown":  len(rows),  // 展示的 top-N
		"rows":   rows,       // 已按净边(扣费后)降序,跨所混排
		// 实盘做市的盯市 PnL(observe/未 live 时为 0;live 后由 gate 成交实时回填)。
		"realized_pnl": marketmaker.LiveMMPnL(),
	}
	if len(rows) > 0 {
		best := rows[0] // 净边最高的
		resp["best"] = gin.H{
			"exchange":     best.Exchange,
			"symbol":       best.Symbol,
			"edge_bps":     best.BestEdgeBps(),  // 毛边
			"net_edge_bps": best.NetBestEdgeBps, // 扣双腿手续费后的净边
			"fee_bps":      best.FeeBps,         // 单腿 maker 费
			"fee_live":     best.FeeLive,        // true=实时费率, false=默认假设
		}
	}
	c.JSON(http.StatusOK, resp)
}
