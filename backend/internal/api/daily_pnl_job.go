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

// BackfillDailyPnL is an admin-only handler that kicks off (in the background)
// a re-pull of the daily PnL aggregation from Binance for the past N days
// (default 400). Use after a first deploy or whenever you suspect the cache
// table drifted. Returns immediately; rebuildDailyPnL is single-flighted so
// repeated triggers won't stack Binance load.
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
	// 后台异步跑:带节流的全量回填可能耗时较久(每 symbol 间隔涓流),同步返回会挂住请求;
	// rebuildDailyPnL 内部有 single-flight 保护,重复触发不会叠加币安压力。
	go rebuildDailyPnL(days)

	c.JSON(http.StatusAccepted, gin.H{
		"status": "started",
		"days":   days,
		"source": "binance",
		"hint":   "已在后台按币安重建,过一会儿刷新「数据面板」查看",
	})
}

var dailyPnLOnce sync.Once

func StartDailyPnLJob(ctx context.Context) {
	dailyPnLOnce.Do(func() {
		go runDailyPnLLoop(ctx)
	})
}

func runDailyPnLLoop(ctx context.Context) {
	// 启动全量刷新前先等一会,避开进程启动时的下单/持仓/K线请求高峰,
	// 免得批量回填的突发请求把每分钟权重打满、触发 429 拖累持仓同步。
	if !sleepCtx(ctx, startupBackfillDelay) {
		return
	}
	// 全量刷新:按币安重建缓存表 400 天历史(income 分页后成本很低,且覆盖旧版本遗留的
	// 本地口径数据)。若遭遇限频,隔几分钟重试,最多 3 次。
	for attempt := 0; attempt < 3; attempt++ {
		if !rebuildDailyPnL(400) {
			break // 完成(或非限频原因结束)
		}
		logger.Errorf("daily pnl: startup backfill hit rate limit, retry in 3m (attempt %d)", attempt+1)
		if !sleepCtx(ctx, 3*time.Minute) {
			return
		}
	}
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

// sleepCtx 睡 d,期间若 ctx 取消则提前返回 false。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
	binancePageLimit = 1000
	binanceMaxPages  = 120 // 120*1000=12w 条,足够覆盖 400 天
	// 后台批量回填:慢速涓流,避免突发把每分钟权重打满触发 429
	//（一旦 429,币安客户端会设 2 分钟全局 ban 窗口,连累 2s 一次的持仓同步)。
	binanceBulkThrottle = 900 * time.Millisecond
	// 当天实时:调用量小,轻throttle 即可。
	binanceLiveThrottle = 150 * time.Millisecond
	// 「今天」实时值缓存 TTL:让 60s 一次的快照/其它调用复用,进一步压低币安调用频率。
	todayEntryTTL = 120 * time.Second
	// 启动后延迟再做全量回填,避开进程启动时的下单/持仓/K线请求高峰。
	startupBackfillDelay = 60 * time.Second
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

// isBinanceRateLimited 判断错误是否为币安限频(429 / -1003 / IP ban)。命中时应立刻
// 停手,把每分钟权重让给更关键的持仓同步等调用,而不是继续 hammer。
func isBinanceRateLimited(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "\"code\":429") ||
		strings.Contains(msg, "\"code\":-1003") ||
		strings.Contains(msg, "rate limited") ||
		strings.Contains(msg, "too many request") ||
		strings.Contains(msg, "ip banned")
}

// computeBinanceDailyBuckets 拉 [start,end] 区间的币安真实数据,按本地日聚合。
// throttle 是每次分页/每个 symbol 之间的间隔(批量回填用大值涓流,当天实时用小值)。
// 任一分页出错立即返回 error,调用方据此保留旧缓存 / 返回 0,不写脏数据。
func computeBinanceDailyBuckets(uid uint, start, end time.Time, throttle time.Duration) (map[string]*binanceDayAgg, error) {
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
		time.Sleep(throttle)
	}

	// 2) userTrades 逐 symbol 分页:只统计带 realizedPnl 的平仓成交,得每日平仓名义额 + 平仓单数。
	for sym := range symbolSet {
		scursor := start
		for page := 0; page < binanceMaxPages; page++ {
			fills, err := bx.USDMUserTrades(uid, sym, scursor, end, binancePageLimit)
			if err != nil {
				if isBinanceRateLimited(err) {
					return nil, err // 限频:立刻停手,让出权重
				}
				break // 其它错误:单 symbol 失败不阻塞其它 symbol
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
			time.Sleep(throttle)
		}
		time.Sleep(throttle)
	}
	return buckets, nil
}

// rebuildDailyPnL 拉取每个用户最近 days 天的币安数据并写入缓存表(覆盖历史 1..days 天;
// 当天由读取路径实时计算,这里不写)。返回值 rateLimited 表示中途遭遇币安限频而提前中止
// ——调用方可据此延后重试。任一用户普通拉取失败只记录日志、保留其旧缓存。
var dailyPnLRebuildMu sync.Mutex // single-flight:同一时刻只允许一个回填,避免叠加币安压力

func rebuildDailyPnL(days int) (rateLimited bool) {
	if database.DB == nil || days <= 0 {
		return false
	}
	if _, ok := binanceExchangeUSDM(); !ok {
		return false // 非币安 usdm,无数据源
	}
	if !dailyPnLRebuildMu.TryLock() {
		logger.Infof("daily pnl: rebuild already running, skip days=%d", days)
		return false
	}
	defer dailyPnLRebuildMu.Unlock()
	var users []models.User
	if err := database.DB.Select("id").Find(&users).Error; err != nil {
		logger.Errorf("daily pnl: list users failed err=%v", err)
		return false
	}
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := today.AddDate(0, 0, -days)
	for _, u := range users {
		buckets, err := computeBinanceDailyBuckets(u.ID, start, now, binanceBulkThrottle)
		if err != nil {
			if isBinanceRateLimited(err) {
				// 限频:立刻整体中止,把权重让给持仓同步,避免持续踩 429 触发全局 ban。
				logger.Errorf("daily pnl: binance rate limited, abort rebuild uid=%d err=%v", u.ID, err)
				return true
			}
			logger.Errorf("daily pnl: binance fetch failed uid=%d err=%v", u.ID, err)
			continue // 其它错误:保留旧缓存,不写脏
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
	return false
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

// 「今天」实时值缓存:loadDailyPnLCalendar 会被快照任务(60s)/optimize/按需构建等多处调用,
// 每次都实时打币安太费权重。用 TTL 缓存把频率压到 ~每 todayEntryTTL 一次。
type cachedTodayEntry struct {
	entry DailyPnLEntry
	at    time.Time
}

var (
	todayEntryMu    sync.RWMutex
	todayEntryCache = map[uint]cachedTodayEntry{}
)

// buildTodayPnLEntry 实时从币安算「今天」的盈亏(读取路径用,不落库),带 TTL 缓存。
// 币安拉失败时:有旧缓存则沿用旧值(哪怕过期),否则返回 0——避免瞬时限频把今天清零。
func buildTodayPnLEntry(uid uint, start time.Time, end time.Time) DailyPnLEntry {
	day := start.Format("2006-01-02")

	todayEntryMu.RLock()
	cached, ok := todayEntryCache[uid]
	todayEntryMu.RUnlock()
	if ok && cached.entry.Day == day && time.Since(cached.at) < todayEntryTTL {
		return cached.entry
	}

	buckets, err := computeBinanceDailyBuckets(uid, start, end, binanceLiveThrottle)
	if err != nil {
		if ok && cached.entry.Day == day {
			return cached.entry // 沿用旧值,别因瞬时限频清零
		}
		return DailyPnLEntry{Day: day}
	}

	entry := DailyPnLEntry{Day: day}
	if agg := buckets[day]; agg != nil {
		ret := 0.0
		if agg.RealizedNotional > 0 {
			ret = (agg.RealizedPnL / agg.RealizedNotional) * 100
		}
		entry.RealizedPnL = roundMoney2(agg.RealizedPnL)
		entry.RealizedNotional = agg.RealizedNotional
		entry.RealizedReturnPct = ret
		entry.Trades = agg.trades()
	}

	todayEntryMu.Lock()
	todayEntryCache[uid] = cachedTodayEntry{entry: entry, at: time.Now()}
	todayEntryMu.Unlock()
	return entry
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
