package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// optimizer / cron-driven auto-optimization endpoints.
// Architecture: every 6h an external (Claude-side) routine fetches context via
// GET /api/admin/optimize/context, reasons about it, then POSTs a new template
// via /api/admin/optimize/apply which atomically creates a new StrategyTemplate
// row, rebinds the instance to it (clearing any StrategyVersionID), and restarts.

// === Endpoint 1: GET /api/admin/optimize/context?strategy_id=<id>&hours=24 ===

type optimizeContextSymbolSummary struct {
	Count    int     `json:"count"`
	Wins     int     `json:"wins"`
	PnL      float64 `json:"pnl"`
	Notional float64 `json:"notional"`
}

type optimizeTradesSummary struct {
	Count       int                                     `json:"count"`
	WinCount    int                                     `json:"win_count"`
	LossCount   int                                     `json:"loss_count"`
	WinRatePct  float64                                 `json:"win_rate_pct"`
	RealizedPnL float64                                 `json:"realized_pnl"`
	BySymbol    map[string]optimizeContextSymbolSummary `json:"by_symbol"`
	LongCount   int                                     `json:"long_count"`
	ShortCount  int                                     `json:"short_count"`
	LongPnL     float64                                 `json:"long_pnl"`
	ShortPnL    float64                                 `json:"short_pnl"`
	// DataSource 标记本字段实际是哪儿来的：
	//   "binance" — 币安实时拉的（最准）
	//   "db"      — 我们 DB 的 StrategyPosition 表（fallback，可能有遗漏）
	DataSource string `json:"data_source"`
}

type optimizeAccountSnapshot struct {
	Exchange      string  `json:"exchange"`
	Market        string  `json:"market"`
	OpenPositions int     `json:"open_positions"`
	OpenNotional  float64 `json:"open_notional"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
}

// optimizeBinanceSymbolStats 单 symbol 在窗口内的累计
type optimizeBinanceSymbolStats struct {
	Symbol      string  `json:"symbol"`
	TradeCount  int     `json:"trade_count"` // 平仓配对数
	RealizedPnL float64 `json:"realized_pnl"`
	Commission  float64 `json:"commission"`
	Funding     float64 `json:"funding"`
	NetPnL      float64 `json:"net_pnl"`
}

// optimizeBinanceHoldBucket 持仓时长分布桶
type optimizeBinanceHoldBucket struct {
	Label    string  `json:"label"` // <1m / 1-5m / 5-15m / 15-60m / >60m
	Count    int     `json:"count"`
	WinCount int     `json:"win_count"`
	WinRate  float64 `json:"win_rate_pct"`
	PnL      float64 `json:"pnl"`
}

// optimizeBinanceContext 是从币安 API 拉的真实账户成交诊断。
// 远端 cron 拿这块数据就够做参数优化（不需要 IP 白名单）。
type optimizeBinanceContext struct {
	WindowHours      int                          `json:"window_hours"`
	Balance          float64                      `json:"balance_usdt"`
	AvailableBalance float64                      `json:"available_balance_usdt"`
	OpenPositions    []optimizeOpenPosition       `json:"open_positions"`
	IncomeTotals     map[string]float64           `json:"income_totals"` // type -> sum
	IncomeCounts     map[string]int               `json:"income_counts"` // type -> n
	BySymbol         []optimizeBinanceSymbolStats `json:"by_symbol"`     // realized pnl 排序
	PairedTrades     optimizePairedSummary        `json:"paired_trades"` // 开-平 配对统计
	HoldDistribution []optimizeBinanceHoldBucket  `json:"hold_distribution"`
	FetchedAt        time.Time                    `json:"fetched_at"`
	FetchError       string                       `json:"fetch_error,omitempty"`
}

type optimizeOpenPosition struct {
	Symbol           string  `json:"symbol"`
	Direction        string  `json:"direction"`
	Amount           float64 `json:"amount"`
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	Leverage         float64 `json:"leverage"`
	Notional         float64 `json:"notional"`
	Margin           float64 `json:"margin"`
	ROIPct           float64 `json:"roi_pct"`
	LiquidationPrice float64 `json:"liquidation_price"`
}

type optimizePairedSummary struct {
	PairCount       int     `json:"pair_count"`
	Wins            int     `json:"wins"`
	Losses          int     `json:"losses"`
	WinRatePct      float64 `json:"win_rate_pct"`
	AvgWin          float64 `json:"avg_win_usdt"`
	AvgLoss         float64 `json:"avg_loss_usdt"`
	RewardRiskRatio float64 `json:"reward_risk_ratio"`
	AvgHoldMinutes  float64 `json:"avg_hold_minutes"`
	GrossPnL        float64 `json:"gross_pnl_usdt"`
	TotalFees       float64 `json:"total_fees_usdt"`
	FeeDragPct      float64 `json:"fee_drag_pct"`
	NetPnL          float64 `json:"net_pnl_usdt"`
	LongCount       int     `json:"long_count"`
	LongWinRatePct  float64 `json:"long_win_rate_pct"`
	LongNetPnL      float64 `json:"long_net_pnl"`
	ShortCount      int     `json:"short_count"`
	ShortWinRatePct float64 `json:"short_win_rate_pct"`
	ShortNetPnL     float64 `json:"short_net_pnl"`
	BreakevenWinPct float64 `json:"breakeven_win_rate_pct"` // 净盈亏平衡需要的胜率
}

type optimizeContextResponse struct {
	StrategyID        string                  `json:"strategy_id"`
	StrategyName      string                  `json:"strategy_name"`
	OwnerID           uint                    `json:"owner_id"`
	TemplateID        uint                    `json:"current_template_id"`
	StrategyVersionID *uint                   `json:"current_version_id,omitempty"`
	Status            string                  `json:"status"`
	Config            map[string]interface{}  `json:"config"`
	CurrentCode       string                  `json:"current_code"`
	CurrentCodeHash   string                  `json:"current_code_hash"`
	Account           optimizeAccountSnapshot `json:"account"`
	TradesWindow      optimizeTradesSummary   `json:"trades_window"`
	DailyPnL7d        []DailyPnLEntry         `json:"daily_pnl_7d"`
	Binance           *optimizeBinanceContext `json:"binance,omitempty"`
	WindowHours       int                     `json:"window_hours"`
	GeneratedAt       time.Time               `json:"generated_at"`
}

// GetOptimizeContext returns the full context the cron-side LLM needs to
// reason about a candidate optimization.
func GetOptimizeContext(c *gin.Context) {
	strategyID := strings.TrimSpace(c.Query("strategy_id"))
	if strategyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing strategy_id"})
		return
	}
	hours := 24
	if v := strings.TrimSpace(c.Query("hours")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 720 {
			hours = n
		}
	}

	var row models.StrategyInstance
	if err := database.DB.Preload("Template").Preload("StrategyVersion").
		Where("id = ?", strategyID).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "strategy not found"})
		return
	}

	code, err := resolveCodeForOptimize(&row)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	codeHash := sha256.Sum256([]byte(code))

	var cfg map[string]interface{}
	if strings.TrimSpace(row.Config) != "" {
		_ = json.Unmarshal([]byte(row.Config), &cfg)
	}

	// Trades in window
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	var positions []models.StrategyPosition
	_ = database.DB.Where(
		"owner_id = ? AND strategy_id = ? AND status = ? AND close_time >= ?",
		row.OwnerID, strategyID, "closed", since,
	).Find(&positions).Error

	trades := optimizeTradesSummary{BySymbol: map[string]optimizeContextSymbolSummary{}, DataSource: "db"}
	for _, p := range positions {
		trades.Count++
		trades.RealizedPnL += p.RealizedPnL
		ss := trades.BySymbol[p.Symbol]
		ss.Count++
		ss.PnL += p.RealizedPnL
		ss.Notional += p.RealizedNotional
		if p.RealizedPnL > 0 {
			ss.Wins++
			trades.WinCount++
		} else if p.RealizedPnL < 0 {
			trades.LossCount++
		}
		trades.BySymbol[p.Symbol] = ss
		if strings.EqualFold(p.Direction, "long") {
			trades.LongCount++
			trades.LongPnL += p.RealizedPnL
		} else if strings.EqualFold(p.Direction, "short") {
			trades.ShortCount++
			trades.ShortPnL += p.RealizedPnL
		}
	}
	if trades.Count > 0 {
		trades.WinRatePct = float64(trades.WinCount) / float64(trades.Count) * 100
	}

	// Account snapshot
	acct := optimizeAccountSnapshot{}
	if stratMgr != nil && stratMgr.GetExchange() != nil {
		acct.Exchange = stratMgr.GetExchange().GetName()
		if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok {
			acct.Market = bx.Market()
			if ps, err := bx.FetchPositions(row.OwnerID, "active"); err == nil {
				acct.OpenPositions = len(ps)
				for _, p := range ps {
					acct.UnrealizedPnL += p.UnrealizedPnL
					cp := p.CurrentPrice
					if cp <= 0 {
						cp = p.Price
					}
					if cp > 0 {
						acct.OpenNotional += p.Amount * cp
					}
				}
			}
		}
	}

	resp := optimizeContextResponse{
		StrategyID:        strategyID,
		StrategyName:      row.Name,
		OwnerID:           row.OwnerID,
		TemplateID:        row.TemplateID,
		StrategyVersionID: row.StrategyVersionID,
		Status:            row.Status,
		Config:            cfg,
		CurrentCode:       code,
		CurrentCodeHash:   hex.EncodeToString(codeHash[:]),
		Account:           acct,
		TradesWindow:      trades,
		DailyPnL7d:        loadDailyPnLCalendar(row.OwnerID, 7),
		WindowHours:       hours,
		GeneratedAt:       time.Now(),
	}

	// 远端 cron 默认拉币安真实成交（这样它不需要 IP 白名单）。
	// 显式 ?binance=0 可关，省一次几秒级延迟（适合本地调试）。
	includeBinance := true
	if v := strings.TrimSpace(c.Query("binance")); v == "0" || strings.EqualFold(v, "false") {
		includeBinance = false
	}
	if includeBinance {
		resp.Binance = fetchBinanceContext(row.OwnerID, hours)

		// 关键升级：如果 binance 拉成功，就用 binance 数据**覆盖** trades_window 和 daily_pnl_7d。
		// 原因：DB 里的 StrategyPosition 表可能漏记（重启、TP/SL 由交易所触发但 strategy 没监听到、
		// 部分平仓累计逻辑等）。币安那边是 source of truth。
		if resp.Binance != nil && strings.TrimSpace(resp.Binance.FetchError) == "" {
			resp.TradesWindow = buildTradesWindowFromBinance(resp.Binance)
			if daily := fetchBinanceDailyPnL(row.OwnerID, 7); len(daily) > 0 {
				resp.DailyPnL7d = daily
			}
		}
	}

	c.JSON(http.StatusOK, resp)
}

// buildTradesWindowFromBinance 把币安端算好的 paired_trades + by_symbol 重新打包成
// 跟 DB 路径同结构的 optimizeTradesSummary，让 cron prompt 不必关心字段是哪儿来的。
func buildTradesWindowFromBinance(b *optimizeBinanceContext) optimizeTradesSummary {
	pt := b.PairedTrades
	out := optimizeTradesSummary{
		Count:       pt.PairCount,
		WinCount:    pt.Wins,
		LossCount:   pt.Losses,
		WinRatePct:  pt.WinRatePct,
		RealizedPnL: pt.GrossPnL,
		LongCount:   pt.LongCount,
		ShortCount:  pt.ShortCount,
		LongPnL:     pt.LongNetPnL,
		ShortPnL:    pt.ShortNetPnL,
		BySymbol:    map[string]optimizeContextSymbolSummary{},
		DataSource:  "binance",
	}
	for _, s := range b.BySymbol {
		// 简化映射：用 net_pnl 当 PnL，trade_count 当 Count；Wins/Notional 无法直接给（binance income
		// 不区分单笔），保留为 0 —— routine prompt 主要用 by_symbol[].net_pnl 排序，足够
		out.BySymbol[s.Symbol] = optimizeContextSymbolSummary{
			Count:    s.TradeCount,
			PnL:      s.NetPnL,
			Notional: 0,
			Wins:     0,
		}
	}
	return out
}

// fetchBinanceDailyPnL 从币安 income 接口按"日"聚合最近 N 天 PnL。
// 替代 loadDailyPnLCalendar（那个从 DB 的 daily_pnls 表读，依赖 StrategyPosition 是否完整）。
// 返回字段格式跟 DailyPnLEntry 兼容，前端/routine 无感知。
func fetchBinanceDailyPnL(ownerID uint, days int) []DailyPnLEntry {
	if days <= 0 {
		return nil
	}
	if stratMgr == nil || stratMgr.GetExchange() == nil {
		return nil
	}
	bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		return nil
	}

	// 拉 days+1 天的事件，最早一天可能被时区/边界裁掉，所以多拉 1 天兜底
	now := time.Now()
	since := now.AddDate(0, 0, -(days + 1))
	events, err := bx.USDMIncomeHistory(ownerID, since, now, 1000)
	if err != nil || len(events) == 0 {
		return nil
	}

	// 按本地 day 聚合
	loc := now.Location()
	type bucket struct {
		realized   float64
		commission float64
		funding    float64
		trades     int
	}
	byDay := map[string]*bucket{}
	for _, e := range events {
		t := time.UnixMilli(e.Time).In(loc)
		day := t.Format("2006-01-02")
		b := byDay[day]
		if b == nil {
			b = &bucket{}
			byDay[day] = b
		}
		switch e.IncomeType {
		case "REALIZED_PNL":
			b.realized += e.Income
			b.trades++
		case "COMMISSION":
			b.commission += e.Income
		case "FUNDING_FEE":
			b.funding += e.Income
		}
	}

	// 拼成连续 N 天的数组（即使某天无成交也要有占位行，cron 算 7d 累加才不会漏）
	out := make([]DailyPnLEntry, 0, days)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	for i := 0; i < days; i++ {
		d := today.AddDate(0, 0, -i)
		key := d.Format("2006-01-02")
		b := byDay[key]
		entry := DailyPnLEntry{
			Day:         key,
			Trades:      0,
			RealizedPnL: 0,
		}
		if b != nil {
			// 净 PnL = 实现盈亏 + 手续费（负值）+ 资金费率
			// 这样 cron 算 daily_7d_sum 自动是净值，跟前端日历语义一致
			entry.Trades = b.trades
			entry.RealizedPnL = roundMoney2(b.realized + b.commission + b.funding)
		}
		out = append(out, entry)
	}
	return out
}

// roundMoney2 给 PnL 字段限定 2 位小数，避免 float64 拖尾。
func roundMoney2(v float64) float64 {
	return math.Round(v*100) / 100
}

// fetchBinanceContext 调用币安私有接口，把账户成交配对、按 symbol 汇总、
// 持仓时长分布等都算好。任何子步骤失败都会返回带 FetchError 的部分结果，
// 不会让整个 optimize/context 接口失败。
func fetchBinanceContext(ownerID uint, hours int) *optimizeBinanceContext {
	out := &optimizeBinanceContext{
		WindowHours:  hours,
		IncomeTotals: map[string]float64{},
		IncomeCounts: map[string]int{},
		FetchedAt:    time.Now(),
	}
	if stratMgr == nil || stratMgr.GetExchange() == nil {
		out.FetchError = "no exchange manager"
		return out
	}
	bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange)
	if !ok || bx.Market() != "usdm" {
		out.FetchError = "not usdm binance"
		return out
	}

	// 1) 余额
	if bal, err := bx.USDMAvailableUSDT(ownerID); err == nil {
		out.AvailableBalance = bal
		out.Balance = bal // USDMAvailableUSDT 已经是可用，钱包 = 可用 + 冻结；这里只暴露可用就够 cron 分析了
	}

	// 2) 当前持仓
	if positions, err := bx.FetchPositions(ownerID, "active"); err == nil {
		for _, p := range positions {
			notional := p.Amount * p.CurrentPrice
			if notional == 0 && p.Price > 0 {
				notional = p.Amount * p.Price
			}
			// FetchPositions 不返回 leverage 单独字段，但 ROI 是按 margin 计算，这里粗估
			out.OpenPositions = append(out.OpenPositions, optimizeOpenPosition{
				Symbol:        p.Symbol,
				Direction:     p.Direction,
				Amount:        p.Amount,
				EntryPrice:    p.Price,
				MarkPrice:     p.CurrentPrice,
				UnrealizedPnL: p.UnrealizedPnL,
				Notional:      notional,
				ROIPct:        p.ReturnRate,
			})
		}
	}

	// 3) 收益事件（REALIZED_PNL / COMMISSION / FUNDING_FEE）
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	now := time.Now()
	events, err := bx.USDMIncomeHistory(ownerID, since, now, 1000)
	if err != nil {
		out.FetchError = "income: " + err.Error()
		return out
	}
	symbolSet := map[string]struct{}{}
	symbolStats := map[string]*optimizeBinanceSymbolStats{}
	for _, e := range events {
		out.IncomeTotals[e.IncomeType] += e.Income
		out.IncomeCounts[e.IncomeType]++
		if e.Symbol == "" {
			continue
		}
		symbolSet[e.Symbol] = struct{}{}
		st, ok := symbolStats[e.Symbol]
		if !ok {
			st = &optimizeBinanceSymbolStats{Symbol: e.Symbol}
			symbolStats[e.Symbol] = st
		}
		switch e.IncomeType {
		case "REALIZED_PNL":
			st.RealizedPnL += e.Income
			st.TradeCount++
		case "COMMISSION":
			st.Commission += e.Income
		case "FUNDING_FEE":
			st.Funding += e.Income
		}
	}
	for _, st := range symbolStats {
		st.NetPnL = st.RealizedPnL + st.Commission + st.Funding
		out.BySymbol = append(out.BySymbol, *st)
	}
	sort.Slice(out.BySymbol, func(i, j int) bool {
		return out.BySymbol[i].RealizedPnL < out.BySymbol[j].RealizedPnL
	})

	// 4) 拉 userTrades，用"已实现平仓事件"做权威聚合。
	//    ⚠️ 旧实现用朴素 open/close 状态机配对：一次开仓有多笔 fill、一次平仓也有多笔 fill，
	//       但状态机只把"1 个开 fill 配 1 个平 fill"，196 笔真实平仓事件被压成 ~13 个"配对"，
	//       丢掉的多是亏损 churn，导致 net_pnl 系统性偏乐观（曾报 +$0.86 但真账 -$8.31）；
	//       且平仓后剩余的反向 fill 被误当成"新开仓"，污染 long/short 归因。
	//    ✅ 新实现：每一笔 realizedPnl != 0 的成交 = 一次真实结算事件；方向 = 平仓 fill 方向的反面。
	//       口径与 income_totals 一致，routine 不必再绕开本字段手算。
	type fillRow struct {
		Time int64
		Side string // BUY / SELL
		Qty  float64
		PnL  float64 // realizedPnl（开/加仓 fill 为 0）
		Comm float64 // 正值手续费
	}
	allFills := map[string][]fillRow{}
	var totalCommission float64
	for sym := range symbolSet {
		trades, err := bx.USDMUserTrades(ownerID, sym, since, now, 1000)
		if err != nil {
			// 单 symbol 失败不影响整体（很可能是参数受限，例如 symbol 已下线）
			continue
		}
		for _, t := range trades {
			c := math.Abs(t.Commission)
			totalCommission += c
			allFills[sym] = append(allFills[sym], fillRow{
				Time: t.Time, Side: t.Side, Qty: t.Qty, PnL: t.RealizedPnL, Comm: c,
			})
		}
	}

	// 4a) 权威 headline 指标：遍历所有"已实现平仓事件"（realizedPnl != 0）。
	var realizedCount, wins, losses int
	var grossRealized, sumWin, sumLoss float64
	var longCnt, longWin, shortCnt, shortWin int
	var longRealized, shortRealized float64
	var longComm, shortComm float64
	for _, fills := range allFills {
		for _, f := range fills {
			// 手续费按方向归集（每笔 fill 自带精确 Comm，无需比例分摊）：
			// 平仓 fill（PnL!=0）：SELL 平多 → long；BUY 平空 → short。
			// 开/加仓 fill（PnL==0）：方向与 fill 同向：BUY 开多 → long；SELL 开空 → short。
			// （单向持仓模式下平空的 BUY 必带 realizedPnl，所以 PnL==0 的 BUY 一定是开多。）
			if f.PnL == 0 {
				if strings.EqualFold(f.Side, "BUY") {
					longComm += f.Comm
				} else if strings.EqualFold(f.Side, "SELL") {
					shortComm += f.Comm
				}
				continue // 开仓 / 加仓 fill 不产生已实现盈亏
			}
			realizedCount++
			grossRealized += f.PnL
			if f.PnL > 0 {
				wins++
				sumWin += f.PnL
			} else {
				losses++
				sumLoss += f.PnL
			}
			// 方向 = 平仓 fill 方向的反面：SELL 平多 → long；BUY 平空 → short
			if strings.EqualFold(f.Side, "SELL") {
				longCnt++
				longRealized += f.PnL
				longComm += f.Comm
				if f.PnL > 0 {
					longWin++
				}
			} else if strings.EqualFold(f.Side, "BUY") {
				shortCnt++
				shortRealized += f.PnL
				shortComm += f.Comm
				if f.PnL > 0 {
					shortWin++
				}
			}
		}
	}

	funding := out.IncomeTotals["FUNDING_FEE"]
	totalFees := -totalCommission + funding // 手续费为负，资金费率可正可负

	p := &out.PairedTrades
	p.PairCount = realizedCount
	p.Wins = wins
	p.Losses = losses
	if realizedCount > 0 {
		p.WinRatePct = float64(wins) / float64(realizedCount) * 100
	}
	if wins > 0 {
		p.AvgWin = sumWin / float64(wins)
	}
	if losses > 0 {
		p.AvgLoss = sumLoss / float64(losses)
	}
	if p.AvgLoss != 0 {
		p.RewardRiskRatio = math.Abs(p.AvgWin / p.AvgLoss)
	}
	p.GrossPnL = grossRealized
	p.TotalFees = totalFees
	p.NetPnL = grossRealized + totalFees // 权威净值，与 income_totals 口径一致
	if grossRealized != 0 {
		p.FeeDragPct = math.Abs(totalFees) / math.Abs(grossRealized) * 100
	}
	if p.RewardRiskRatio > 0 {
		p.BreakevenWinPct = 1.0 / (p.RewardRiskRatio + 1) * 100
	}
	// long/short 净额 = 该方向毛已实现盈亏 − 该方向实际手续费（开+平 fill 逐笔归集，
	// 非比例分摊）。资金费只有账户级总数、无方向归属，不摊入，因此
	// long_net + short_net + funding = net_pnl。
	p.LongCount = longCnt
	p.LongNetPnL = longRealized - longComm
	if longCnt > 0 {
		p.LongWinRatePct = float64(longWin) / float64(longCnt) * 100
	}
	p.ShortCount = shortCnt
	p.ShortNetPnL = shortRealized - shortComm
	if shortCnt > 0 {
		p.ShortWinRatePct = float64(shortWin) / float64(shortCnt) * 100
	}

	// 5) 持仓时长分布：按 symbol 跟踪净持仓 qty。position 从 0 离开记开仓时刻，
	//    回到 0 或反向穿越 0 记一段完整持仓，用该平仓 fill 的 realizedPnl 判定输赢。
	buckets := []struct {
		Label string
		Lo    float64
		Hi    float64
	}{
		{"<1m", 0, 1}, {"1-5m", 1, 5}, {"5-15m", 5, 15},
		{"15-60m", 15, 60}, {">60m", 60, math.MaxFloat64},
	}
	bucketCount := make([]int, len(buckets))
	bucketWin := make([]int, len(buckets))
	bucketPnL := make([]float64, len(buckets))
	var sumDur float64
	var durCount int
	for _, fills := range allFills {
		sort.Slice(fills, func(i, j int) bool { return fills[i].Time < fills[j].Time })
		var posQty float64
		var openTime int64
		for _, f := range fills {
			signed := f.Qty
			if strings.EqualFold(f.Side, "SELL") {
				signed = -f.Qty
			}
			prev := posQty
			if math.Abs(prev) < 1e-12 && signed != 0 {
				openTime = f.Time
			}
			posQty += signed
			// 持仓减少到 0 或反向穿越 0 → 记一段完整持仓
			crossedZero := math.Abs(prev) >= 1e-12 && (math.Abs(posQty) < 1e-12 || (prev > 0) != (posQty > 0))
			if crossedZero && f.PnL != 0 {
				durMin := float64(f.Time-openTime) / 60000.0
				if durMin < 0 {
					durMin = 0
				}
				sumDur += durMin
				durCount++
				for i, b := range buckets {
					if durMin >= b.Lo && durMin < b.Hi {
						bucketCount[i]++
						bucketPnL[i] += f.PnL
						if f.PnL > 0 {
							bucketWin[i]++
						}
						break
					}
				}
				// 若反向开了新仓，新仓开仓时刻 = 此刻
				if math.Abs(posQty) > 1e-12 {
					openTime = f.Time
				}
			}
		}
	}
	if durCount > 0 {
		p.AvgHoldMinutes = sumDur / float64(durCount)
	}
	for i, b := range buckets {
		rate := 0.0
		if bucketCount[i] > 0 {
			rate = float64(bucketWin[i]) / float64(bucketCount[i]) * 100
		}
		out.HoldDistribution = append(out.HoldDistribution, optimizeBinanceHoldBucket{
			Label: b.Label, Count: bucketCount[i], WinCount: bucketWin[i],
			WinRate: rate, PnL: bucketPnL[i],
		})
	}

	return out
}

// === Endpoint 2: POST /api/admin/optimize/apply ===

type applyOptimizationRequest struct {
	StrategyID   string `json:"strategy_id" binding:"required"`
	Code         string `json:"code" binding:"required"`
	Name         string `json:"name"` // optional, auto-generated
	Description  string `json:"description"`
	BaselineHash string `json:"baseline_hash"` // optional, race detection
}

// Required symbols the new code must contain (sanity check 1).
var optimizerMustHaveSymbols = []string{
	"on_market_message",
	"_emit_signal",
	"_append_bar",
	"self.pub.publish",
}

// Key numeric parameters whose magnitude may not drift > 2x per iteration.
var optimizerKeyParams = []string{
	"TP_RATIO", "SL_RATIO",
	"ATR_TP_MULT", "ATR_SL_MULT",
	"MIN_CONFIDENCE",
	"WARMUP_BARS",
	"VOLUME_RATIO_MIN",
	"MAX_BARS",
}

// ApplyOptimization handles cron-driven template swap. Three internal
// guards must pass before the new template is created and bound.
func ApplyOptimization(c *gin.Context) {
	var req applyOptimizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Guard 1: must-have symbols
	for _, sym := range optimizerMustHaveSymbols {
		if !strings.Contains(req.Code, sym) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("rejected: missing required symbol %q", sym),
				"guard": "must_have_symbols",
			})
			return
		}
	}

	// Guard 2: py_compile
	if err := pyCompileCheck(req.Code); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("rejected: py_compile failed: %v", err),
			"guard": "py_compile",
		})
		return
	}

	// Load current
	var row models.StrategyInstance
	if err := database.DB.Preload("Template").Preload("StrategyVersion").
		Where("id = ?", req.StrategyID).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "strategy not found"})
		return
	}
	oldCode, _ := resolveCodeForOptimize(&row)
	oldHashRaw := sha256.Sum256([]byte(oldCode))
	newHashRaw := sha256.Sum256([]byte(req.Code))
	oldHash := hex.EncodeToString(oldHashRaw[:])
	newHash := hex.EncodeToString(newHashRaw[:])
	if oldHash == newHash {
		c.JSON(http.StatusOK, gin.H{
			"status": "no_change",
			"hint":   "new code identical to current; nothing to do",
		})
		return
	}
	if req.BaselineHash != "" && req.BaselineHash != oldHash {
		c.JSON(http.StatusConflict, gin.H{
			"error": "baseline_hash mismatch — strategy changed since context fetched",
			"guard": "baseline_race",
		})
		return
	}

	// Guard 3: parameter drift
	if drift := paramDriftCheck(oldCode, req.Code); drift != "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "rejected: parameter drift too large — " + drift,
			"guard": "param_drift",
		})
		return
	}

	// Auto-generate template name if missing
	if strings.TrimSpace(req.Name) == "" {
		prefix := fmt.Sprintf("%s_auto_v", row.Name)
		nextN := nextAutoVersionNumber(prefix)
		req.Name = fmt.Sprintf("%s%d_%s", prefix, nextN, time.Now().Format("20060102-1504"))
	}

	// Transactional apply
	var newTemplateID uint
	wasVersionBound := row.StrategyVersionID != nil
	previousTemplateID := row.TemplateID
	dbErr := database.DB.Transaction(func(tx *gorm.DB) error {
		nt := models.StrategyTemplate{
			Name:         req.Name,
			Description:  req.Description,
			Code:         req.Code,
			Path:         fmt.Sprintf("db://template/%s", req.Name),
			TemplateType: "strategy",
			AuthorID:     row.OwnerID,
			IsDraft:      false,
			IsEnabled:    true,
		}
		if err := tx.Create(&nt).Error; err != nil {
			return err
		}
		newTemplateID = nt.ID
		if err := tx.Model(&models.StrategyInstance{}).
			Where("id = ?", req.StrategyID).
			Updates(map[string]interface{}{
				"template_id":         newTemplateID,
				"strategy_version_id": nil,
				"updated_at":          time.Now(),
			}).Error; err != nil {
			return err
		}
		summary := strings.TrimSpace(req.Description)
		if summary == "" {
			summary = "cron auto-optimize"
		}
		_ = tx.Create(&models.StrategyOptimizationRun{
			StrategyID:         req.StrategyID,
			OwnerID:            row.OwnerID,
			Status:             "applied",
			Trigger:            "cron_auto",
			Model:              "claude-cron",
			BaseCodeHash:       oldHash,
			CandidateCodeHash:  newHash,
			Applied:            true,
			PreviousTemplateID: previousTemplateID, // 让 rollback 能精准找回这次之前的 template
			NewTemplateID:      newTemplateID,
			Summary:            summary,
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		}).Error
		return nil
	})
	if dbErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": dbErr.Error()})
		return
	}

	// Restart strategy if currently active
	needsRestart := row.Status == "running" || row.Status == "starting"
	if needsRestart && stratMgr != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("[OPTIMIZE] restart panic strategy=%s panic=%v", req.StrategyID, r)
				}
			}()
			if err := stratMgr.StopStrategy(req.StrategyID, true); err != nil {
				logger.Errorf("[OPTIMIZE] stop failed strategy=%s err=%v", req.StrategyID, err)
			}
			time.Sleep(2 * time.Second)
			if err := stratMgr.StartStrategy(req.StrategyID); err != nil {
				logger.Errorf("[OPTIMIZE] start failed strategy=%s err=%v", req.StrategyID, err)
			}
		}()
	}

	c.JSON(http.StatusOK, gin.H{
		"status":               "applied",
		"new_template_id":      newTemplateID,
		"previous_template_id": previousTemplateID,
		"new_template_name":    req.Name,
		"version_unbound":      wasVersionBound,
		"needs_restart":        needsRestart,
		"restart":              "scheduled async",
		"old_code_hash":        oldHash,
		"new_code_hash":        newHash,
	})
}

// === Helpers ===

// resolveCodeForOptimize picks the code currently in effect for the instance.
// Mirrors strategy/manager.go:resolveStrategySource but stays inside the api
// package to avoid widening that internal helper's visibility.
func resolveCodeForOptimize(row *models.StrategyInstance) (string, error) {
	if row == nil {
		return "", fmt.Errorf("nil strategy row")
	}
	if row.StrategyVersionID != nil && row.StrategyVersion.ID > 0 {
		if c := strings.TrimSpace(row.StrategyVersion.Code); c != "" {
			return c, nil
		}
	}
	if c := strings.TrimSpace(row.Template.Code); c != "" {
		return c, nil
	}
	return "", fmt.Errorf("no code available for strategy %s", row.ID)
}

// pyCompileCheck writes code to a temp file and runs `python3 -m py_compile`.
// Returns nil on syntax-valid Python, error otherwise.
func pyCompileCheck(code string) error {
	f, err := os.CreateTemp("", "claude_optim_*.py")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	if _, err := f.WriteString(code); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	cmd := exec.Command("python3", "-m", "py_compile", tmpPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// paramDriftCheck compares values of optimizerKeyParams between old/new code.
// Returns non-empty string describing a violation if any param changed by
// more than 2x in either direction; empty string means all clear.
func paramDriftCheck(oldCode, newCode string) string {
	for _, name := range optimizerKeyParams {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=\s*([+\-0-9eE\.]+)`)
		oldM := re.FindStringSubmatch(oldCode)
		newM := re.FindStringSubmatch(newCode)
		if len(oldM) < 2 || len(newM) < 2 {
			continue
		}
		oldVal, err1 := strconv.ParseFloat(oldM[1], 64)
		newVal, err2 := strconv.ParseFloat(newM[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if oldVal == 0 {
			continue
		}
		ratio := newVal / oldVal
		if ratio < 0.5 || ratio > 2.0 {
			return fmt.Sprintf("%s: %g → %g (ratio %.2fx)", name, oldVal, newVal, ratio)
		}
	}
	return ""
}

// nextAutoVersionNumber scans for templates named "<prefix>N_*" and returns N+1.
func nextAutoVersionNumber(prefix string) int {
	var rows []models.StrategyTemplate
	_ = database.DB.Where("name LIKE ?", prefix+"%").
		Order("created_at desc").
		Limit(10).
		Find(&rows).Error
	maxN := 0
	for _, r := range rows {
		rest := strings.TrimPrefix(r.Name, prefix)
		parts := strings.SplitN(rest, "_", 2)
		if len(parts) == 0 {
			continue
		}
		n, err := strconv.Atoi(parts[0])
		if err == nil && n > maxN {
			maxN = n
		}
	}
	return maxN + 1
}
