package api

import (
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

type dashboardBuildOptions struct {
	RangePreset     string
	StartRaw        string
	EndRaw          string
	IncludeCalendar bool
	CalendarDays    int
}

func buildDashboardResponse(uid uint, now time.Time, opt dashboardBuildOptions) (DashboardResponse, error) {
	loc := now.Location()
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	weekOffset := (int(now.Weekday()) + 6) % 7
	startWeek := startDay.AddDate(0, 0, -weekOffset)
	startMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

	unrealized, openCount, openSymbols, openNotional := loadOpenPositionMetrics(uid)
	period := func(start time.Time, end time.Time) PnLPeriodSummary {
		return buildPnLPeriod(uid, start, end, unrealized)
	}

	resp := DashboardResponse{
		UpdatedAt: now,
		PnL: PnLSummaryResponse{
			UpdatedAt:     now,
			UnrealizedPnL: unrealized,
			Day:           period(startDay, now),
			Week:          period(startWeek, now),
			Month:         period(startMonth, now),
		},
	}
	if opt.IncludeCalendar {
		days := opt.CalendarDays
		if days <= 0 {
			days = 60
		}
		resp.PnL.Calendar = loadDailyPnLCalendar(uid, days)
	}

	customStart, customEnd, label := resolveDashboardCustomRange(now, opt.RangePreset, opt.StartRaw, opt.EndRaw)
	if !customStart.IsZero() && !customEnd.IsZero() && customEnd.After(customStart) {
		p := period(customStart, customEnd)
		resp.PnL.Custom = &p
		resp.PnL.CustomLabel = label
	}

	ordersAgg := loadDashboardOrderAgg(uid)
	stratAgg := loadDashboardStrategyAgg(uid)

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

func loadOpenPositionMetrics(uid uint) (unrealized float64, openCount int, openSymbols int, openNotional float64) {
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
	return
}

func buildPnLPeriod(uid uint, start time.Time, end time.Time, unrealized float64) PnLPeriodSummary {
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

func resolveDashboardCustomRange(now time.Time, rangePreset string, startRaw string, endRaw string) (time.Time, time.Time, string) {
	rangePreset = strings.TrimSpace(rangePreset)
	startRaw = strings.TrimSpace(startRaw)
	endRaw = strings.TrimSpace(endRaw)
	customStart := time.Time{}
	customEnd := time.Time{}
	label := rangePreset
	if startRaw != "" {
		if t, err := time.Parse(time.RFC3339, startRaw); err == nil {
			customStart = t
		} else if t, err := time.Parse("2006-01-02T15:04", startRaw); err == nil {
			customStart = t
		}
	}
	if endRaw != "" {
		if t, err := time.Parse(time.RFC3339, endRaw); err == nil {
			customEnd = t
		} else if t, err := time.Parse("2006-01-02T15:04", endRaw); err == nil {
			customEnd = t
		}
	}
	if rangePreset != "" {
		d := time.Duration(0)
		switch strings.ToLower(rangePreset) {
		case "1m":
			d = time.Minute
			label = "近 1 分钟"
		case "5m":
			d = 5 * time.Minute
			label = "近 5 分钟"
		case "1h":
			d = time.Hour
			label = "近 1 小时"
		case "1d":
			d = 24 * time.Hour
			label = "近 1 天"
		case "1w":
			d = 7 * 24 * time.Hour
			label = "近 1 周"
		case "1mo":
			d = 30 * 24 * time.Hour
			label = "近 1 个月"
		}
		if d > 0 {
			customStart = now.Add(-d)
			customEnd = now
		}
	} else if !customStart.IsZero() || !customEnd.IsZero() {
		label = "自定义范围"
	}
	return customStart, customEnd, label
}

func loadDashboardOrderAgg(uid uint) struct {
	Total     int64
	Filled    int64
	Rejected  int64
	Failed    int64
	Requested int64
	New       int64
} {
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
	return ordersAgg
}

func loadDashboardStrategyAgg(uid uint) struct {
	Running int64
	Stopped int64
	Error   int64
	Total   int64
} {
	var stratAgg struct {
		Running int64
		Stopped int64
		Error   int64
		Total   int64
	}
	_ = database.DB.Model(&models.StrategyInstance{}).
		Select(`
			COUNT(*) AS total,
			SUM(CASE WHEN status IN ('running', 'starting') THEN 1 ELSE 0 END) AS running,
			SUM(CASE WHEN status = 'stopped' THEN 1 ELSE 0 END) AS stopped,
			SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) AS error
		`).
		Where("owner_id = ?", uid).
		Scan(&stratAgg).Error
	return stratAgg
}
