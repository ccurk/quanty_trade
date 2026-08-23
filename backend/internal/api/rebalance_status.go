package api

import (
	"context"
	"net/http"
	"os"
	"time"

	"quanty_trade/internal/logger"
	"quanty_trade/internal/rebalance"

	"github.com/gin-gonic/gin"
)

var rebalanceMonitor *rebalance.Monitor

// StartRebalanceMonitor starts READ-ONLY balance monitoring + recommendations when
// conf/rebalance.yaml exists with enable:true. It polls both exchanges' spot
// balances and runs the planner; it never executes a transfer. Path override via
// $REBALANCE_CONFIG (defaults to conf/rebalance.yaml, mounted at /app/conf).
func StartRebalanceMonitor(ctx context.Context) {
	path := os.Getenv("REBALANCE_CONFIG")
	if path == "" {
		path = "conf/rebalance.yaml"
	}
	cfg, err := rebalance.LoadConfig(path)
	if err != nil {
		logger.Warnf("[rebalance] 配置未加载(%s): %v —— 监控不启动", path, err)
		return
	}
	if !cfg.Enable {
		logger.Infof("[rebalance] enable=false, 监控不启动")
		return
	}
	// 启动自检:立即各拉一次余额,把结果(仅数量,不含金额)打进日志,便于确认 key/签名正确。
	if g, err := rebalance.FetchGateSpotBalances(); err != nil {
		logger.Warnf("[rebalance] gate 余额自检失败: %v", err)
	} else {
		logger.Infof("[rebalance] gate 余额自检 OK, %d 个非零资产", len(g))
	}
	if b, err := rebalance.FetchBinanceSpotBalances(); err != nil {
		logger.Warnf("[rebalance] binance 余额自检失败: %v", err)
	} else {
		logger.Infof("[rebalance] binance 余额自检 OK, %d 个非零资产", len(b))
	}

	rebalanceMonitor = rebalance.NewMonitor(cfg, LoadRebalanceWhitelist, 15*time.Second)
	stop := make(chan struct{})
	go func() { <-ctx.Done(); close(stop) }()
	rebalanceMonitor.Start(stop)
	logger.Infof("[rebalance] 监控启动 exec=%s reservoir=%s mode=%s dryRun=%v assets=%d",
		cfg.ExecExchange, cfg.ReservoirExchange, cfg.Mode, cfg.DryRun, len(cfg.Assets))
}

// GetRebalanceStatus GET /rebalance/status —— 实时余额 + 失衡搬运建议(只读)。
func GetRebalanceStatus(c *gin.Context) {
	if rebalanceMonitor == nil {
		c.JSON(http.StatusOK, gin.H{
			"enabled": false,
			"message": "再平衡监控未启动:需在 conf/rebalance.yaml 设 enable:true 后重启后端",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": true, "snapshot": rebalanceMonitor.Snapshot()})
}
