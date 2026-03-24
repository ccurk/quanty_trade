package api

import (
	"context"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
)

var dashboardSnapOnce sync.Once
var dashboardSnapMu sync.RWMutex
var dashboardSnaps map[uint]DashboardResponse

func StartDashboardSnapshotJob(ctx context.Context) {
	dashboardSnapOnce.Do(func() {
		dashboardSnaps = make(map[uint]DashboardResponse)
		go runDashboardSnapshotLoop(ctx)
	})
}

func getDashboardSnapshot(uid uint) (DashboardResponse, bool) {
	dashboardSnapMu.RLock()
	defer dashboardSnapMu.RUnlock()
	v, ok := dashboardSnaps[uid]
	return v, ok
}

func runDashboardSnapshotLoop(ctx context.Context) {
	refreshAll := func() {
		if database.DB == nil || stratMgr == nil {
			return
		}
		var users []models.User
		if err := database.DB.Select("id").Find(&users).Error; err != nil {
			logger.Errorf("dashboard snapshot: list users failed err=%v", err)
			return
		}
		now := time.Now()
		for _, u := range users {
			resp, err := buildDashboardSnapshot(u.ID, now)
			if err != nil {
				continue
			}
			dashboardSnapMu.Lock()
			dashboardSnaps[u.ID] = resp
			dashboardSnapMu.Unlock()
		}
	}

	refreshAll()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshAll()
		}
	}
}

func buildDashboardSnapshot(uid uint, now time.Time) (DashboardResponse, error) {
	loc := now.Location()
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	weekOffset := (int(now.Weekday()) + 6) % 7
	startWeek := startDay.AddDate(0, 0, -weekOffset)
	startMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

	unrealized := 0.0
	openCount := 0
	openSymbols := 0
	openNotional := 0.0
	if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		if ps, err := bx.FetchPositions(uid, "active"); err == nil {
			openCount = len(ps)
			syms := map[string]struct{}{}
			for _, p := range ps {
				syms[strings.ToUpper(p.Symbol)] = struct{}{}
				unrealized += p.UnrealizedPnL
				cp := p.CurrentPrice
				if cp <= 0 {
					cp = p.Price
				}
				if cp > 0 {
					openNotional += p.Amount * cp
				}
			}
			openSymbols = len(syms)
		}
	}

	period := func(start time.Time, end time.Time) PnLPeriodSummary {
		var row struct {
			GrossProfit      float64
			GrossLoss        float64
			RealizedPnL      float64
			RealizedNotional float64
		}
		_ = database.DB.Model(&models.StrategyPosition{}).
			Select(`
				COALESCE(SUM(CASE WHEN realized_pn_l > 0 THEN realized_pn_l ELSE 0 END), 0) AS gross_profit,
				COALESCE(SUM(CASE WHEN realized_pn_l < 0 THEN realized_pn_l ELSE 0 END), 0) AS gross_loss,
				COALESCE(SUM(realized_pn_l), 0) AS realized_pnl,
				COALESCE(SUM(realized_notional), 0) AS realized_notional
			`).
			Where("owner_id = ? AND status = ? AND close_time >= ? AND close_time <= ?", uid, "closed", start, end).
			Scan(&row).Error

		ret := 0.0
		if row.RealizedNotional > 0 {
			ret = (row.RealizedPnL / row.RealizedNotional) * 100
		}
		return PnLPeriodSummary{
			StartTime:         start,
			EndTime:           end,
			GrossProfit:       row.GrossProfit,
			GrossLoss:         row.GrossLoss,
			RealizedPnL:       row.RealizedPnL,
			RealizedNotional:  row.RealizedNotional,
			RealizedReturnPct: ret,
			UnrealizedPnL:     unrealized,
			TotalPnL:          row.RealizedPnL + unrealized,
		}
	}

	var ordersAgg struct {
		Total     int64
		Filled    int64
		Rejected  int64
		Failed    int64
		Requested int64
		New       int64
	}
	_ = database.DB.Model(&models.StrategyOrder{}).
		Select(`
			COUNT(*) AS total,
			SUM(CASE WHEN status = 'filled' THEN 1 ELSE 0 END) AS filled,
			SUM(CASE WHEN status = 'rejected' THEN 1 ELSE 0 END) AS rejected,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed,
			SUM(CASE WHEN status = 'requested' THEN 1 ELSE 0 END) AS requested,
			SUM(CASE WHEN status = 'new' THEN 1 ELSE 0 END) AS new
		`).
		Where("owner_id = ?", uid).
		Scan(&ordersAgg).Error

	var stratAgg struct {
		Running int64
		Stopped int64
		Error   int64
		Total   int64
	}
	_ = database.DB.Model(&models.StrategyInstance{}).
		Select(`
			COUNT(*) AS total,
			SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END) AS running,
			SUM(CASE WHEN status = 'stopped' THEN 1 ELSE 0 END) AS stopped,
			SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) AS error
		`).
		Where("owner_id = ?", uid).
		Scan(&stratAgg).Error

	var resp DashboardResponse
	resp.UpdatedAt = now
	resp.PnL = PnLSummaryResponse{
		UpdatedAt:     now,
		UnrealizedPnL: unrealized,
		Day:           period(startDay, now),
		Week:          period(startWeek, now),
		Month:         period(startMonth, now),
	}
	resp.Account.Exchange = stratMgr.GetExchange().GetName()
	if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok {
		resp.Account.Market = bx.Market()
	}
	resp.Account.UserID = uid
	resp.Positions.OpenCount = openCount
	resp.Positions.OpenSymbols = openSymbols
	resp.Positions.OpenNotional = openNotional
	resp.Positions.UnrealizedPnL = unrealized
	resp.Orders.Total = ordersAgg.Total
	resp.Orders.Filled = ordersAgg.Filled
	resp.Orders.Rejected = ordersAgg.Rejected
	resp.Orders.Failed = ordersAgg.Failed
	resp.Orders.Requested = ordersAgg.Requested
	resp.Orders.New = ordersAgg.New
	resp.Strategies.Total = stratAgg.Total
	resp.Strategies.Running = stratAgg.Running
	resp.Strategies.Stopped = stratAgg.Stopped
	resp.Strategies.Error = stratAgg.Error
	return resp, nil
}
