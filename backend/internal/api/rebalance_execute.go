package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"quanty_trade/internal/rebalance"

	"github.com/gin-gonic/gin"
)

// rebalancePriceUSD values assets for the USD caps. Stablecoins = 1; anything else
// returns 0, and the executor refuses to move an asset it can't value — so the
// single/daily USD caps can never be bypassed by an unpriced asset.
func rebalancePriceUSD(asset string) float64 {
	switch strings.ToUpper(asset) {
	case "USDT", "USDC", "BUSD", "FDUSD", "DAI", "TUSD":
		return 1
	}
	return 0
}

// spentTodayUSD sums SUBMITTED transfers since UTC midnight. Derived from the DB so
// the daily cap survives restarts.
func spentTodayUSD() float64 {
	start := time.Now().UTC().Truncate(24 * time.Hour)
	var total float64
	database.DB.Model(&models.RebalanceTransfer{}).
		Where("status = ? AND created_at >= ?", "submitted", start).
		Select("COALESCE(SUM(amount_usd),0)").Scan(&total)
	return total
}

// lastSubmittedAt is the most recent submitted transfer time for an asset (cooldown).
func lastSubmittedAt(asset string) time.Time {
	var row models.RebalanceTransfer
	if err := database.DB.Where("status = ? AND asset = ?", "submitted", strings.ToUpper(asset)).
		Order("created_at desc").First(&row).Error; err != nil {
		return time.Time{}
	}
	return row.CreatedAt
}

// freshPlanForAsset re-computes plans server-side from LIVE balances + the current
// whitelist, and returns the one for asset (nil if none). This is the trust anchor:
// the executed address + amount always come from here, never from the client.
func freshPlanForAsset(asset string) *rebalance.Plan {
	var balances []rebalance.Balance
	if g, err := rebalance.FetchGateSpotBalances(); err == nil {
		balances = append(balances, g...)
	}
	if b, err := rebalance.FetchBinanceSpotBalances(); err == nil {
		balances = append(balances, b...)
	}
	plans := rebalanceCfg.BuildPlanner(LoadRebalanceWhitelist()).Plan(balances)
	for i := range plans {
		if strings.EqualFold(plans[i].Asset, asset) {
			return &plans[i]
		}
	}
	return nil
}

// executeAndRecord runs one plan through the executor and writes the outcome to the
// audit log (every outcome, not just successes). Returns the result.
func executeAndRecord(plan rebalance.Plan, actorID uint) rebalance.TransferResult {
	res := rebalanceExecutor.Execute(plan, spentTodayUSD(), lastSubmittedAt(plan.Asset), time.Now())
	rec := models.RebalanceTransfer{
		Asset: plan.Asset, FromExchange: plan.FromExchange, ToExchange: plan.ToExchange,
		Network: plan.Network, Amount: res.Amount, ToAddress: plan.ToAddress,
		TxID: res.TxID, Mode: rebalanceCfg.Mode, CreatedBy: actorID, CreatedAt: time.Now(),
	}
	switch {
	case res.Executed:
		rec.Status = "submitted"
		rec.AmountUSD = res.Amount * rebalancePriceUSD(plan.Asset) // 仅提交成功计入当日额度
	case res.DryRun:
		rec.Status = "dryrun"
	case res.Error != "":
		rec.Status = "failed"
		rec.Detail = res.Error
	default:
		rec.Status = "skipped"
		rec.Detail = res.Skipped
	}
	database.DB.Create(&rec)
	return res
}

type executeReq struct {
	Asset string `json:"asset"`
}

// ExecuteRebalancePlan POST /rebalance/execute —— 对某资产执行一次搬运(semi/auto)。
func ExecuteRebalancePlan(c *gin.Context) {
	if rebalanceExecutor == nil || rebalanceCfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "再平衡未启动(需 conf/rebalance.yaml enable:true)"})
		return
	}
	var req executeReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Asset) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要 asset"})
		return
	}
	if rebalanceCfg.Mode == rebalance.ModeRecommend {
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前 mode=recommend,只建议不执行;要执行请把配置改为 semi/auto 并重启"})
		return
	}
	plan := freshPlanForAsset(req.Asset)
	if plan == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": req.Asset + " 当前无搬运需求(在带内 / 无余额数据)"})
		return
	}
	res := executeAndRecord(*plan, rebalanceActorID(c))
	c.JSON(http.StatusOK, gin.H{"result": res})
}

// ListRebalanceTransfers GET /rebalance/transfers —— 最近搬运记录(审计)。
func ListRebalanceTransfers(c *gin.Context) {
	var rows []models.RebalanceTransfer
	database.DB.Order("created_at desc").Limit(100).Find(&rows)
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// runAutoRebalance drives mode=auto: periodically re-plan on live balances and run
// every executable plan through the executor (which still enforces caps/cooldown/
// dry-run). Only started when mode==auto. dryRun=true here just logs dryrun rows.
func runAutoRebalance(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	logger.Infof("[rebalance] auto 模式已启用(dryRun=%v)", rebalanceCfg.DryRun)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var balances []rebalance.Balance
		if g, err := rebalance.FetchGateSpotBalances(); err == nil {
			balances = append(balances, g...)
		}
		if b, err := rebalance.FetchBinanceSpotBalances(); err == nil {
			balances = append(balances, b...)
		}
		for _, p := range rebalanceCfg.BuildPlanner(LoadRebalanceWhitelist()).Plan(balances) {
			if !p.Executable() {
				continue
			}
			res := executeAndRecord(p, 0)
			if res.Executed {
				logger.Infof("[rebalance] auto 提交 %s %s→%s %.6f tx=%s", p.Asset, p.FromExchange, p.ToExchange, res.Amount, res.TxID)
			}
		}
	}
}
