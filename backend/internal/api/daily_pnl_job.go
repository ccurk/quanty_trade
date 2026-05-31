package api

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
)

var dailyPnLOnce sync.Once

func StartDailyPnLJob(ctx context.Context) {
	dailyPnLOnce.Do(func() {
		go runDailyPnLLoop(ctx)
	})
}

func runDailyPnLLoop(ctx context.Context) {
	backfillDailyPnL(400)
	for {
		next := nextLocalTime(0, 5, 0)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		timer.Stop()
		runDailyPnLForYesterday()
	}
}

func nextLocalTime(h, m, s int) time.Time {
	now := time.Now()
	loc := now.Location()
	next := time.Date(now.Year(), now.Month(), now.Day(), h, m, s, 0, loc)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func runDailyPnLForYesterday() {
	if database.DB == nil {
		return
	}
	now := time.Now()
	loc := now.Location()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := end.Add(-24 * time.Hour)
	day := start.Format("2006-01-02")
	runDailyPnLForRange(day, start, end.Add(-time.Nanosecond))
}

func backfillDailyPnL(days int) {
	if database.DB == nil || days <= 0 {
		return
	}
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	for i := 1; i <= days; i++ {
		start := today.AddDate(0, 0, -i)
		end := start.Add(24 * time.Hour).Add(-time.Nanosecond)
		day := start.Format("2006-01-02")
		runDailyPnLForRange(day, start, end)
	}
}

func runDailyPnLForRange(day string, start time.Time, end time.Time) {
	var users []models.User
	if err := database.DB.Select("id").Find(&users).Error; err != nil {
		logger.Errorf("daily pnl: list users failed err=%v", err)
		return
	}
	for _, u := range users {
		if err := upsertDailyPnL(u.ID, day, start, end); err != nil {
			logger.Errorf("daily pnl: compute failed uid=%d day=%s err=%v", u.ID, day, err)
		}
	}
}

func upsertDailyPnL(uid uint, day string, start time.Time, end time.Time) error {
	if database.DB == nil || uid == 0 {
		return fmt.Errorf("missing db/uid")
	}

	var row struct {
		GrossProfit      float64
		GrossLoss        float64
		RealizedPnL      float64
		RealizedNotional float64
		Trades           int64
	}
	if err := database.DB.Model(&models.StrategyPosition{}).
		Select(`
			COALESCE(SUM(CASE WHEN realized_pn_l > 0 THEN realized_pn_l ELSE 0 END), 0) AS gross_profit,
			COALESCE(SUM(CASE WHEN realized_pn_l < 0 THEN realized_pn_l ELSE 0 END), 0) AS gross_loss,
			COALESCE(SUM(realized_pn_l), 0) AS realized_pnl,
			COALESCE(SUM(realized_notional), 0) AS realized_notional,
			COALESCE(COUNT(*), 0) AS trades
		`).
		Where("owner_id = ? AND status = ? AND close_time >= ? AND close_time <= ?", uid, "closed", start, end).
		Scan(&row).Error; err != nil {
		return err
	}

	now := time.Now()
	var existing models.DailyPnL
	findTx := database.DB.Where("owner_id = ? AND day = ?", uid, day).Limit(1).Find(&existing)
	if findTx.Error != nil {
		return findTx.Error
	}
	if findTx.RowsAffected > 0 && existing.ID > 0 {
		return database.DB.Model(&models.DailyPnL{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{
			"start_time":        start,
			"end_time":          end,
			"gross_profit":      row.GrossProfit,
			"gross_loss":        row.GrossLoss,
			"realized_pn_l":     row.RealizedPnL,
			"realized_notional": row.RealizedNotional,
			"trades":            int(row.Trades),
			"updated_at":        now,
		}).Error
	}

	return database.DB.Create(&models.DailyPnL{
		OwnerID:          uid,
		Day:              day,
		StartTime:        start,
		EndTime:          end,
		GrossProfit:      row.GrossProfit,
		GrossLoss:        row.GrossLoss,
		RealizedPnL:      row.RealizedPnL,
		RealizedNotional: row.RealizedNotional,
		Trades:           int(row.Trades),
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error
}

func loadDailyPnLCalendar(uid uint, days int) []DailyPnLEntry {
	if database.DB == nil || uid == 0 || days <= 0 {
		return nil
	}
	var rows []models.DailyPnL
	_ = database.DB.Where("owner_id = ?", uid).Order("day desc").Limit(days).Find(&rows).Error
	loc := time.Now().Location()
	today := time.Now()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	todayEntry := buildDailyPnLEntryForRange(uid, todayStart, today)
	entryMap := map[string]DailyPnLEntry{}
	for _, r := range rows {
		entryMap[r.Day] = dailyPnLEntryFromRow(r)
	}
	entryMap[todayStart.Format("2006-01-02")] = todayEntry
	out := make([]DailyPnLEntry, 0, days)
	for i := days - 1; i >= 0; i-- {
		dayTime := todayStart.AddDate(0, 0, -i)
		key := dayTime.Format("2006-01-02")
		if entry, ok := entryMap[key]; ok {
			out = append(out, entry)
			continue
		}
		out = append(out, DailyPnLEntry{
			Day:               key,
			RealizedPnL:       0,
			RealizedNotional:  0,
			RealizedReturnPct: 0,
			Trades:            0,
		})
	}
	return out
}

func loadMonthlyPnLCalendar(uid uint, days int) []MonthlyPnLEntry {
	daysData := loadDailyPnLCalendar(uid, days)
	if len(daysData) == 0 {
		return nil
	}
	monthMap := map[string]*MonthlyPnLEntry{}
	for _, day := range daysData {
		monthKey := ""
		if len(day.Day) >= 7 {
			monthKey = day.Day[:7]
		}
		if monthKey == "" {
			continue
		}
		entry, ok := monthMap[monthKey]
		if !ok {
			entry = &MonthlyPnLEntry{Month: monthKey}
			monthMap[monthKey] = entry
		}
		entry.RealizedPnL += day.RealizedPnL
		entry.RealizedNotional += day.RealizedNotional
		entry.Trades += day.Trades
		if day.RealizedPnL > 0 {
			entry.PositiveDays++
		} else if day.RealizedPnL < 0 {
			entry.NegativeDays++
		}
	}
	out := make([]MonthlyPnLEntry, 0, len(monthMap))
	for _, entry := range monthMap {
		if entry.RealizedNotional > 0 {
			entry.RealizedReturnPct = (entry.RealizedPnL / entry.RealizedNotional) * 100
		}
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Month < out[j].Month
	})
	return out
}

func buildDailyPnLEntryForRange(uid uint, start time.Time, end time.Time) DailyPnLEntry {
	var row struct {
		RealizedPnL      float64
		RealizedNotional float64
		Trades           int64
	}
	_ = database.DB.Model(&models.StrategyPosition{}).
		Select(`
			COALESCE(SUM(realized_pn_l), 0) AS realized_pnl,
			COALESCE(SUM(realized_notional), 0) AS realized_notional,
			COALESCE(COUNT(*), 0) AS trades
		`).
		Where("owner_id = ? AND status = ? AND close_time >= ? AND close_time <= ?", uid, "closed", start, end).
		Scan(&row).Error
	ret := 0.0
	if row.RealizedNotional > 0 {
		ret = (row.RealizedPnL / row.RealizedNotional) * 100
	}
	return DailyPnLEntry{
		Day:               start.Format("2006-01-02"),
		RealizedPnL:       row.RealizedPnL,
		RealizedNotional:  row.RealizedNotional,
		RealizedReturnPct: ret,
		Trades:            int(row.Trades),
	}
}

func dailyPnLEntryFromRow(r models.DailyPnL) DailyPnLEntry {
	ret := 0.0
	if r.RealizedNotional > 0 {
		ret = (r.RealizedPnL / r.RealizedNotional) * 100
	}
	return DailyPnLEntry{
		Day:               r.Day,
		RealizedPnL:       r.RealizedPnL,
		RealizedNotional:  r.RealizedNotional,
		RealizedReturnPct: ret,
		Trades:            r.Trades,
	}
}
