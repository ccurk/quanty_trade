package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

// 盈亏日历完全以币安为数据源：
//   - 已实现盈亏 / 盈亏拆分 / 有成交的 symbol ← /fapi/v1/income (REALIZED_PNL)
//   - 平仓名义额(收益率分母) / 交易数(不同平仓单) ← /fapi/v1/userTrades
//
// 不再读取本地 StrategyPosition。因为币安接口单次上限 1000 条、userTrades 还要
// 逐 symbol 拉，无法在每次请求里全量实时拉 400 天，所以历史每天的值由后台任务
// 逐日拉取后**缓存**进 daily_pnls 表（启动全量刷新一次 + 每天 00:05 刷新最近几天），
// 「今天」的值则在读取时实时从币安计算。缓存表里的数据 100% 来自币安。

// BackfillDailyPnL is an admin-only handler that synchronously re-pulls the
// daily PnL aggregation from Binance for the past N days (default 400). Use
// after a first deploy or whenever you suspect the cache table drifted.
//
// Query: ?days=N (1..3650, default 400)
func BackfillDailyPnL(c *gin.Context) {
	if database.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db not ready"})
		return
	}
	if _, ok := binanceExchangeUSDM(); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前交易所非币安 USDM,盈亏日历无数据源"})
		return
	}
	days := 400
	if v := strings.TrimSpace(c.Query("days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 3650 {
			days = n
		}
	}
	startedAt := time.Now()
	rebuildDailyPnL(days)

	c.JSON(http.StatusOK, gin.H{
		"status":      "ok",
		"days":        days,
		"source":      "binance",
		"duration_ms": time.Since(startedAt).Milliseconds(),
		"hint":        "前端「数据面板」刷新一下,应该能看到日历填充",
	})
}

var dailyPnLOnce sync.Once

func StartDailyPnLJob(ctx context.Context) {
	dailyPnLOnce.Do(func() {
		go runDailyPnLLoop(ctx)
	})
}

func runDailyPnLLoop(ctx context.Context) {
	// 启动全量刷新：把缓存表 400 天的历史全部按币安重建（income 分页后成本很低，
	// 且覆盖任何旧版本遗留的本地口径数据）。
	rebuildDailyPnL(400)
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
		// 每天 00:05 刷新最近几天（覆盖刚过去的昨天完整值）。历史日一旦过完即不再变。
		rebuildDailyPnL(3)
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

// ===========================================================================
// 币安数据源
// ===========================================================================

type binanceDayAgg struct {
	GrossProfit      float64
	GrossLoss        float64
	RealizedPnL      float64
	RealizedNotional float64
	closeOrders      map[int64]struct{} // 当日不同平仓单 → 交易数
}

func (a *binanceDayAgg) trades() int { return len(a.closeOrders) }

const (
	binancePageLimit     = 1000
	binanceMaxPages      = 120 // 120*1000=12w 条,足够覆盖 400 天
	binanceFetchThrottle = 120 * time.Millisecond
)

func binanceExchangeUSDM() (*exchange.BinanceExchange, bool) {
	if stratMgr == nil || stratMgr.GetExchange() == nil {
		return nil, false
	}
	bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return nil, false
	}
	return bx, true
}

// computeBinanceDailyBuckets 拉 [start,end] 区间的币安真实数据,按本地日聚合。
// 任一分页出错立即返回 error,调用方据此保留旧缓存 / 返回 0,不写脏数据。
func computeBinanceDailyBuckets(uid uint, start, end time.Time) (map[string]*binanceDayAgg, error) {
	bx, ok := binanceExchangeUSDM()
	if !ok {
		return nil, fmt.Errorf("binance usdm not available")
	}
	loc := time.Now().Location()
	dayKey := func(ms int64) string { return time.UnixMilli(ms).In(loc).Format("2006-01-02") }

	buckets := map[string]*binanceDayAgg{}
	getBucket := func(k string) *binanceDayAgg {
		b := buckets[k]
		if b == nil {
			b = &binanceDayAgg{closeOrders: map[int64]struct{}{}}
			buckets[k] = b
		}
		return b
	}
	symbolSet := map[string]struct{}{}

	// 1) income 分页(按时间游标翻页):每日已实现盈亏 + 盈/亏拆分 + 有成交的 symbol。
	cursor := start
	for page := 0; page < binanceMaxPages; page++ {
		events, err := bx.USDMIncomeHistory(uid, cursor, end, binancePageLimit)
		if err != nil {
			return nil, fmt.Errorf("income: %w", err)
		}
		if len(events) == 0 {
			break
		}
		var lastTime int64
		for _, e := range events {
			if e.Time > lastTime {
				lastTime = e.Time
			}
			if e.IncomeType != "REALIZED_PNL" {
				continue
			}
			b := getBucket(dayKey(e.Time))
			b.RealizedPnL += e.Income
			if e.Income > 0 {
				b.GrossProfit += e.Income
			} else if e.Income < 0 {
				b.GrossLoss += e.Income
			}
			if e.Symbol != "" {
				symbolSet[e.Symbol] = struct{}{}
			}
		}
		if len(events) < binancePageLimit {
			break
		}
		next := time.UnixMilli(lastTime + 1)
		if !next.After(cursor) || !next.Before(end) {
			break
		}
		cursor = next
		time.Sleep(binanceFetchThrottle)
	}

	// 2) userTrades 逐 symbol 分页:只统计带 realizedPnl 的平仓成交,得每日平仓名义额 + 平仓单数。
	for sym := range symbolSet {
		scursor := start
		for page := 0; page < binanceMaxPages; page++ {
			fills, err := bx.USDMUserTrades(uid, sym, scursor, end, binancePageLimit)
			if err != nil {
				break // 单 symbol 失败不阻塞其它 symbol
			}
			if len(fills) == 0 {
				break
			}
			var lastTime int64
			for _, t := range fills {
				if t.Time > lastTime {
					lastTime = t.Time
				}
				if t.RealizedPnL == 0 {
					continue // 只有平仓成交才带已实现盈亏
				}
				b := getBucket(dayKey(t.Time))
				b.RealizedNotional += math.Abs(t.QuoteQty)
				b.closeOrders[t.OrderID] = struct{}{}
			}
			if len(fills) < binancePageLimit {
				break
			}
			next := time.UnixMilli(lastTime + 1)
			if !next.After(scursor) || !next.Before(end) {
				break
			}
			scursor = next
			time.Sleep(binanceFetchThrottle)
		}
		time.Sleep(binanceFetchThrottle)
	}
	return buckets, nil
}

// rebuildDailyPnL 拉取每个用户最近 days 天的币安数据并写入缓存表(覆盖历史 1..days 天;
// 当天由读取路径实时计算,这里不写)。任一用户拉取失败只记录日志、保留其旧缓存。
func rebuildDailyPnL(days int) {
	if database.DB == nil || days <= 0 {
		return
	}
	if _, ok := binanceExchangeUSDM(); !ok {
		return // 非币安 usdm,无数据源
	}
	var users []models.User
	if err := database.DB.Select("id").Find(&users).Error; err != nil {
		logger.Errorf("daily pnl: list users failed err=%v", err)
		return
	}
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := today.AddDate(0, 0, -days)
	for _, u := range users {
		buckets, err := computeBinanceDailyBuckets(u.ID, start, now)
		if err != nil {
			logger.Errorf("daily pnl: binance fetch failed uid=%d err=%v", u.ID, err)
			continue // 保留旧缓存,不写脏
		}
		for i := 1; i <= days; i++ {
			dayStart := today.AddDate(0, 0, -i)
			dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
			day := dayStart.Format("2006-01-02")
			if err := upsertDailyPnLRow(u.ID, day, dayStart, dayEnd, buckets[day]); err != nil {
				logger.Errorf("daily pnl: upsert failed uid=%d day=%s err=%v", u.ID, day, err)
			}
		}
	}
}

// upsertDailyPnLRow 原子 upsert(owner_id, day)。agg 为 nil 表示当天无成交,写 0 占位
// (这样也会覆盖旧版本遗留的本地口径行)。
func upsertDailyPnLRow(uid uint, day string, start, end time.Time, agg *binanceDayAgg) error {
	if database.DB == nil || uid == 0 {
		return fmt.Errorf("missing db/uid")
	}
	var (
		grossProfit, grossLoss, realized, notional float64
		trades                                     int
	)
	if agg != nil {
		grossProfit = agg.GrossProfit
		grossLoss = agg.GrossLoss
		realized = roundMoney2(agg.RealizedPnL)
		notional = agg.RealizedNotional
		trades = agg.trades()
	}
	now := time.Now()
	record := models.DailyPnL{
		OwnerID:          uid,
		Day:              day,
		StartTime:        start,
		EndTime:          end,
		GrossProfit:      grossProfit,
		GrossLoss:        grossLoss,
		RealizedPnL:      realized,
		RealizedNotional: notional,
		Trades:           trades,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return database.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "owner_id"}, {Name: "day"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"start_time":        start,
			"end_time":          end,
			"gross_profit":      grossProfit,
			"gross_loss":        grossLoss,
			"realized_pn_l":     realized,
			"realized_notional": notional,
			"trades":            trades,
			"updated_at":        now,
		}),
	}).Create(&record).Error
}

// ===========================================================================
// 读取路径:历史读缓存表,今天实时算币安
// ===========================================================================

func loadDailyPnLCalendar(uid uint, days int) []DailyPnLEntry {
	if database.DB == nil || uid == 0 || days <= 0 {
		return nil
	}
	var rows []models.DailyPnL
	_ = database.DB.Where("owner_id = ?", uid).Order("day desc").Limit(days).Find(&rows).Error
	loc := time.Now().Location()
	today := time.Now()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	todayEntry := buildTodayPnLEntry(uid, todayStart, today)
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

// monthlyFromDailyEntries 把已算好的每日数组聚合成月度汇总,避免重复拉一遍数据。
func monthlyFromDailyEntries(daysData []DailyPnLEntry) []MonthlyPnLEntry {
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

// buildTodayPnLEntry 实时从币安算「今天」的盈亏(读取路径用,不落库)。币安拉失败则返回 0。
func buildTodayPnLEntry(uid uint, start time.Time, end time.Time) DailyPnLEntry {
	day := start.Format("2006-01-02")
	buckets, err := computeBinanceDailyBuckets(uid, start, end)
	if err != nil {
		return DailyPnLEntry{Day: day}
	}
	agg := buckets[day]
	if agg == nil {
		return DailyPnLEntry{Day: day}
	}
	ret := 0.0
	if agg.RealizedNotional > 0 {
		ret = (agg.RealizedPnL / agg.RealizedNotional) * 100
	}
	return DailyPnLEntry{
		Day:               day,
		RealizedPnL:       roundMoney2(agg.RealizedPnL),
		RealizedNotional:  agg.RealizedNotional,
		RealizedReturnPct: ret,
		Trades:            agg.trades(),
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
