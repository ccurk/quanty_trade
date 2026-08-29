package api

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
	"quanty_trade/internal/strategy"

	"github.com/gin-gonic/gin"
)

type strategyPositionMeta struct {
	StrategyID   string
	StrategyName string
	OpenTime     time.Time
	AvgPrice     float64
	TakeProfit   float64
	StopLoss     float64
	// RequestedAt 是该 symbol 最近一笔策略订单的下单时刻,收养归属用它判断
	// "订单元数据是否新鲜到可以作为开仓归属的 ground truth"。
	RequestedAt time.Time
}

func loadRecentStrategyOrderMeta(ownerID uint) map[string]strategyPositionMeta {
	bySymbol := map[string]strategyPositionMeta{}
	var rows []models.StrategyOrder
	_ = database.DB.Where("owner_id = ?", ownerID).Order("requested_at desc").Limit(500).Find(&rows).Error
	for _, o := range rows {
		key := strings.ToUpper(o.Symbol)
		if key == "" {
			continue
		}
		if _, ok := bySymbol[key]; ok {
			continue
		}
		if strings.TrimSpace(o.StrategyID) == "" || strings.TrimSpace(o.StrategyName) == "" {
			continue
		}
		bySymbol[key] = strategyPositionMeta{
			StrategyID:   o.StrategyID,
			StrategyName: o.StrategyName,
			RequestedAt:  o.RequestedAt,
		}
	}
	return bySymbol
}

func loadUserStrategyInstances(ownerID uint) []models.StrategyInstance {
	var instRows []models.StrategyInstance
	_ = database.DB.Where("owner_id = ?", ownerID).Order("updated_at desc").Find(&instRows).Error
	return instRows
}

// findStrategyInstanceForSymbol 把 symbol 按静态配置(symbol/symbols)解析到策略实例。
// #72 两趟解析: running 实例优先,停机实例只作兜底(停/删策略的在持仓位仍需可解析,
// 但不得抢走 running 载具的归属 —— 08-24/08-26 VELVET 双案:退役停机壳的
// config.symbols 命中率先于 running 载具,行级 sid 被污染,逐笔归因随之串账)。
// 两趟都尊重 symbol_blacklist:组内币池互斥用 blacklist 表达,归属解析同样要认。
func findStrategyInstanceForSymbol(instRows []models.StrategyInstance, sym string) *models.StrategyInstance {
	want := exchange.NormalizeSymbol(sym)
	var fallback *models.StrategyInstance
	for i := range instRows {
		var cfg map[string]interface{}
		if err := json.Unmarshal([]byte(instRows[i].Config), &cfg); err != nil {
			continue
		}
		if !configMatchesSymbol(cfg, want) || configBlacklistsSymbol(cfg, want) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(instRows[i].Status), "running") {
			return &instRows[i]
		}
		if fallback == nil {
			fallback = &instRows[i]
		}
	}
	return fallback
}

func findStrategyInstanceByID(instRows []models.StrategyInstance, id string) *models.StrategyInstance {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	for i := range instRows {
		if instRows[i].ID == id {
			return &instRows[i]
		}
	}
	return nil
}

func configMatchesSymbol(cfg map[string]interface{}, want string) bool {
	if v, ok := cfg["symbol"].(string); ok && exchange.NormalizeSymbol(v) == want {
		return true
	}
	if raw, ok := cfg["symbols"]; ok {
		switch xs := raw.(type) {
		case []interface{}:
			for _, it := range xs {
				if s, ok := it.(string); ok && exchange.NormalizeSymbol(s) == want {
					return true
				}
			}
		case []string:
			for _, s := range xs {
				if exchange.NormalizeSymbol(s) == want {
					return true
				}
			}
		}
	}
	return false
}

// configBlacklistsSymbol 与引擎侧 isBlacklistedSymbol 同语义(数组或逗号分隔字符串),
// 但工作在 DB 行的 JSON 配置上(API 层无内存实例可用)。
func configBlacklistsSymbol(cfg map[string]interface{}, want string) bool {
	raw, ok := cfg["symbol_blacklist"]
	if !ok {
		return false
	}
	switch xs := raw.(type) {
	case []interface{}:
		for _, it := range xs {
			if s, ok := it.(string); ok && exchange.NormalizeSymbol(s) == want {
				return true
			}
		}
	case []string:
		for _, s := range xs {
			if exchange.NormalizeSymbol(s) == want {
				return true
			}
		}
	case string:
		for _, s := range strings.Split(xs, ",") {
			if exchange.NormalizeSymbol(strings.TrimSpace(s)) == want {
				return true
			}
		}
	}
	return false
}

func findStrategyForSymbol(instRows []models.StrategyInstance, sym string) (string, string) {
	if si := findStrategyInstanceForSymbol(instRows, sym); si != nil {
		return si.ID, si.Name
	}
	return "", ""
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func getConfigNumber(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f
	}
	return 0
}

func deriveTPSLFromStrategyInstance(si *models.StrategyInstance, entryPrice float64, direction string) (float64, float64) {
	if si == nil || entryPrice <= 0 {
		return 0, 0
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(si.Config), &cfg); err != nil {
		return 0, 0
	}
	lev := int(math.Round(getConfigNumber(cfg["leverage"])))
	if lev <= 0 {
		lev = 1
	}
	tpPct := getConfigNumber(cfg["take_profit_pct"])
	slPct := getConfigNumber(cfg["stop_loss_pct"])
	if tpPct > 1 {
		tpPct = tpPct / 100
	}
	if slPct > 1 {
		slPct = slPct / 100
	}
	if tpPct <= 0 && slPct <= 0 {
		return 0, 0
	}
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir == "" {
		dir = "long"
	}
	tp := 0.0
	sl := 0.0
	if tpPct > 0 {
		off := tpPct / float64(lev)
		if dir == "short" {
			tp = entryPrice * (1 - off)
		} else {
			tp = entryPrice * (1 + off)
		}
	}
	if slPct > 0 {
		off := slPct / float64(lev)
		if dir == "short" {
			sl = entryPrice * (1 + off)
		} else {
			sl = entryPrice * (1 - off)
		}
	}
	return tp, sl
}

func loadOpenStrategyPositionMeta(ownerID uint) map[string]strategyPositionMeta {
	bySymbol := map[string]strategyPositionMeta{}
	var rows []models.StrategyPosition
	_ = database.DB.Where("owner_id = ? AND status = ?", ownerID, "open").Find(&rows).Error
	for _, p := range rows {
		bySymbol[strings.ToUpper(p.Symbol)] = strategyPositionMeta{
			StrategyID:   p.StrategyID,
			StrategyName: p.StrategyName,
			OpenTime:     p.OpenTime,
			AvgPrice:     p.AvgPrice,
			TakeProfit:   p.TakeProfit,
			StopLoss:     p.StopLoss,
		}
	}
	return bySymbol
}

func ListPositions(c *gin.Context) {
	status := c.DefaultQuery("status", "active")
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")
	pageRaw := strings.TrimSpace(c.Query("page"))
	pageSizeRaw := strings.TrimSpace(c.Query("page_size"))
	usePaging := pageRaw != "" || pageSizeRaw != ""
	page := 1
	pageSize := 20
	if pageRaw != "" {
		if v, err := strconv.Atoi(pageRaw); err == nil && v > 0 {
			page = v
		}
	}
	if pageSizeRaw != "" {
		if v, err := strconv.Atoi(pageSizeRaw); err == nil && v > 0 {
			pageSize = v
		}
	}
	if pageSize > 200 {
		pageSize = 200
	}

	uid := userID.(uint)
	if status == "active" {
		if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
			exPos, err := bx.FetchPositions(uid, "active")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}

			instRows := loadUserStrategyInstances(uid)
			bySymbol := loadOpenStrategyPositionMeta(uid)
			orderMeta := loadRecentStrategyOrderMeta(uid)
			out := make([]exchange.Position, 0, len(exPos))
			for _, p := range exPos {
				key := strings.ToUpper(p.Symbol)
				if info, ok := bySymbol[key]; ok {
					p.StrategyID = info.StrategyID
					p.StrategyName = info.StrategyName
					if strings.TrimSpace(p.StrategyName) == "" {
						if si := findStrategyInstanceForSymbol(instRows, p.Symbol); si != nil {
							p.StrategyID = si.ID
							p.StrategyName = si.Name
						} else if meta, ok := orderMeta[key]; ok {
							p.StrategyID = meta.StrategyID
							p.StrategyName = meta.StrategyName
						}
					}
					if !info.OpenTime.IsZero() {
						p.OpenTime = info.OpenTime
					}
					if p.Price == 0 && info.AvgPrice > 0 {
						p.Price = info.AvgPrice
					}
					p.TakeProfit = info.TakeProfit
					p.StopLoss = info.StopLoss
					if (p.TakeProfit <= 0 || p.StopLoss <= 0) && p.Price > 0 {
						if si := findStrategyInstanceForSymbol(instRows, p.Symbol); si != nil {
							tp, sl := deriveTPSLFromStrategyInstance(si, p.Price, p.Direction)
							if p.TakeProfit <= 0 && tp > 0 {
								p.TakeProfit = tp
							}
							if p.StopLoss <= 0 && sl > 0 {
								p.StopLoss = sl
							}
						}
					}
				} else {
					si := findStrategyInstanceForSymbol(instRows, p.Symbol)
					meta, hasMeta := orderMeta[key]
					// #72 归属优先级: 新鲜订单元数据 > 静态 symbols 扫描 > 陈旧订单兜底。
					// entry 行落库前的竞态窗内本收养先行时,该仓一定刚有一笔策略订单,
					// 它标注的 strategy_id 是开仓归属的 ground truth;静态扫描只该在
					// 没有新鲜订单可依时使用(手工仓/重启后遗留仓)。
					freshMeta := hasMeta && !meta.RequestedAt.IsZero() && time.Since(meta.RequestedAt) <= 15*time.Minute
					// 陈旧订单兜底只在该订单贴近本仓开仓时刻(≤15m)时可信——此时它
					// 确为本仓的入场单(重启后遗留仓救援)。时间远离 = 他仓的历史回声:
					// 本 owner 域内某载具早已平仓的旧订单,会把【另一 owner 域载具】
					// 刚开的新仓错认到本域旧载具名下(跨域轮询时 fresh/静态两层全空手,
					// 08-29 BTR 案: main 开仓被错标到已退役 fade-v2)。宁可不归属、
					// 不建行(仓位照常展示,sid 空),也不给错壳建幽灵行招来错配守护。
					staleMetaUsable := hasMeta && !freshMeta && !meta.RequestedAt.IsZero() && !p.OpenTime.IsZero() &&
						absDuration(p.OpenTime.Sub(meta.RequestedAt)) <= 15*time.Minute
					if si != nil || freshMeta || staleMetaUsable {
						strategyID := ""
						strategyName := ""
						if freshMeta {
							strategyID = meta.StrategyID
							strategyName = meta.StrategyName
						} else if si != nil {
							strategyID = si.ID
							strategyName = si.Name
						} else {
							strategyID = meta.StrategyID
							strategyName = meta.StrategyName
						}
						// TP/SL 派生要跟归属走同一个策略,归属与 si 不一致时按 ID 重解析。
						attributed := si
						if attributed == nil || attributed.ID != strategyID {
							attributed = findStrategyInstanceByID(instRows, strategyID)
						}
						tp, sl := deriveTPSLFromStrategyInstance(attributed, p.Price, p.Direction)
						now := time.Now()
						pos := models.StrategyPosition{
							StrategyID:   strategyID,
							StrategyName: strategyName,
							OwnerID:      uid,
							Exchange:     bx.GetName(),
							Symbol:       p.Symbol,
							Direction:    p.Direction,
							Amount:       p.Amount,
							AvgPrice:     p.Price,
							TakeProfit:   tp,
							StopLoss:     sl,
							Status:       "open",
							OpenTime:     p.OpenTime,
							UpdatedAt:    now,
						}
						_ = database.DB.Create(&pos).Error
						p.StrategyID = strategyID
						p.StrategyName = strategyName
						p.TakeProfit = tp
						p.StopLoss = sl
						bySymbol[key] = strategyPositionMeta{
							StrategyID:   strategyID,
							StrategyName: strategyName,
							OpenTime:     pos.OpenTime,
							AvgPrice:     pos.AvgPrice,
							TakeProfit:   pos.TakeProfit,
							StopLoss:     pos.StopLoss,
						}
					}
				}
				out = append(out, p)
			}
			sort.Slice(out, func(i, j int) bool { return out[i].OpenTime.After(out[j].OpenTime) })
			if !usePaging {
				c.JSON(http.StatusOK, out)
				return
			}
			total := len(out)
			start := (page - 1) * pageSize
			if start > total {
				start = total
			}
			end := start + pageSize
			if end > total {
				end = total
			}
			type resp struct {
				Items    []exchange.Position `json:"items"`
				Total    int                 `json:"total"`
				Page     int                 `json:"page"`
				PageSize int                 `json:"page_size"`
			}
			c.JSON(http.StatusOK, resp{Items: out[start:end], Total: total, Page: page, PageSize: pageSize})
			return
		}
	}

	// closed 历史仓位优先从币安拉（更准），失败再走 DB。
	// 用 ?source=db 强制走 DB（调试用）；默认 binance。
	if status == "closed" && c.Query("source") != "db" {
		// 默认拉 48h 历史；前端要更长可加 ?hours=168 之类
		hoursParam := 48
		if v := strings.TrimSpace(c.Query("hours")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 720 {
				hoursParam = n
			}
		}
		if positions, fetchErr := closedPositionsFromBinance(uid, hoursParam); fetchErr == "" {
			// 按 owner 过滤已经在 binance 里做了（key 是 ownerID）；admin 看其他人需 fallback DB
			if userRole != "admin" || c.Query("source") == "binance_only" {
				sort.Slice(positions, func(i, j int) bool { return positions[i].CloseTime.After(positions[j].CloseTime) })
				if !usePaging {
					c.JSON(http.StatusOK, positions)
					return
				}
				total := len(positions)
				start := (page - 1) * pageSize
				if start > total {
					start = total
				}
				end := start + pageSize
				if end > total {
					end = total
				}
				type resp struct {
					Items      []exchange.Position `json:"items"`
					Total      int                 `json:"total"`
					Page       int                 `json:"page"`
					PageSize   int                 `json:"page_size"`
					DataSource string              `json:"data_source"`
				}
				c.JSON(http.StatusOK, resp{
					Items: positions[start:end], Total: total, Page: page, PageSize: pageSize,
					DataSource: "binance",
				})
				return
			}
		}
		// fetchErr 非空 或 admin → fallback DB
	}

	query := database.DB.Model(&models.StrategyPosition{})
	if userRole != "admin" {
		query = query.Where("owner_id = ?", uid)
	}
	if status == "active" {
		query = query.Where("status = ?", "open")
	} else if status == "closed" {
		query = query.Where("status = ?", "closed")
	}

	var rows []models.StrategyPosition
	total := int64(0)
	if usePaging {
		_ = query.Count(&total).Error
		query = query.Offset((page - 1) * pageSize).Limit(pageSize)
	}
	orderClause := "open_time desc"
	if status == "closed" {
		orderClause = "close_time desc, open_time desc"
	}
	if err := query.Order(orderClause).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	positions := make([]exchange.Position, 0, len(rows))
	for _, p := range rows {
		realizedRet := 0.0
		if p.RealizedNotional > 0 {
			realizedRet = (p.RealizedPnL / p.RealizedNotional) * 100
		}
		pos := exchange.Position{
			StrategyID: p.StrategyID,
			Symbol:     p.Symbol,
			Direction:  p.Direction,
			Amount: func() float64 {
				if p.ClosedQty > 0 {
					return p.ClosedQty
				}
				return p.Amount
			}(),
			Price:              p.AvgPrice,
			TakeProfit:         p.TakeProfit,
			StopLoss:           p.StopLoss,
			ClosedQty:          p.ClosedQty,
			AvgClosePrice:      p.AvgClosePrice,
			RealizedPnL:        p.RealizedPnL,
			RealizedReturnRate: realizedRet,
			StrategyName:       p.StrategyName,
			ExchangeName:       p.Exchange,
			Status: func() string {
				if p.Status == "open" {
					return "active"
				}
				return "closed"
			}(),
			OwnerID:   p.OwnerID,
			OpenTime:  p.OpenTime,
			CloseTime: p.CloseTime,
		}
		positions = append(positions, pos)
	}

	sort.Slice(positions, func(i, j int) bool {
		if status == "closed" {
			return positions[i].CloseTime.After(positions[j].CloseTime)
		}
		return positions[i].OpenTime.After(positions[j].OpenTime)
	})
	if !usePaging {
		c.JSON(http.StatusOK, positions)
		return
	}
	type resp struct {
		Items    []exchange.Position `json:"items"`
		Total    int64               `json:"total"`
		Page     int                 `json:"page"`
		PageSize int                 `json:"page_size"`
	}
	c.JSON(http.StatusOK, resp{Items: positions, Total: total, Page: page, PageSize: pageSize})
}

func ListMarketSymbols(c *gin.Context) {
	quote := strings.TrimSpace(c.Query("quote"))
	minPrice, _ := strconv.ParseFloat(strings.TrimSpace(c.Query("min_price")), 64)
	maxPrice, _ := strconv.ParseFloat(strings.TrimSpace(c.Query("max_price")), 64)
	minVol, _ := strconv.ParseFloat(strings.TrimSpace(c.Query("min_quote_volume_24h")), 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(c.Query("limit")))
	excludeStable := c.DefaultQuery("exclude_stable", "true") != "false"
	baseAssetsRaw := strings.TrimSpace(c.Query("base_assets"))
	var baseAssets []string
	if baseAssetsRaw != "" {
		for _, p := range strings.Split(baseAssetsRaw, ",") {
			if s := strings.TrimSpace(p); s != "" {
				baseAssets = append(baseAssets, s)
			}
		}
	}

	ex, ok := stratMgr.GetExchange().(*exchange.BinanceExchange)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "exchange does not support symbol selection"})
		return
	}
	out, err := ex.FetchMarketSymbols(quote, minPrice, maxPrice, minVol, limit, excludeStable, baseAssets)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

func ClosePosition(c *gin.Context) {
	symbol := c.Query("symbol")
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Symbol is required"})
		return
	}

	userID, _ := c.Get("user_id")
	uid := userID.(uint)
	if bx, ok := stratMgr.GetExchange().(*exchange.BinanceExchange); ok && bx.Market() == "usdm" {
		var existing models.StrategyPosition
		hasExisting := database.DB.Where("owner_id = ? AND symbol = ? AND status = ?", uid, symbol, "open").
			Order("open_time desc").
			First(&existing).Error == nil

		instRows := loadUserStrategyInstances(uid)
		if hasExisting && existing.StrategyID != "" && existing.StrategyID != "manual" {
			stratMgr.StopPositionTPStopMonitor(existing.StrategyID, symbol)
		}
		_, _, _ = stratMgr.CancelLinkedTPSLOrders(uid, existing.StrategyID, symbol)
		_, _ = bx.CancelUSDMAllSymbolOrdersDetailed(uid, symbol)
		order, entryPrice, signedAmt, err := bx.ClosePositionOrder(symbol, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if order == nil {
			_, _ = bx.CancelUSDMAllSymbolOrdersDetailed(uid, symbol)
			if hasExisting && existing.StrategyID != "" && existing.StrategyID != "manual" {
				stratMgr.ReleaseOpenSlot(existing.StrategyID)
			}
			c.JSON(http.StatusOK, gin.H{"status": "success"})
			return
		}

		strategyID := ""
		strategyName := ""
		openTime := time.Now()
		direction := ""
		prevRealizedPnL := 0.0
		prevRealizedNotional := 0.0
		if hasExisting {
			strategyID = existing.StrategyID
			strategyName = existing.StrategyName
			openTime = existing.OpenTime
			direction = existing.Direction
			prevRealizedPnL = existing.RealizedPnL
			prevRealizedNotional = existing.RealizedNotional
			if entryPrice == 0 {
				entryPrice = existing.AvgPrice
			}
		} else {
			strategyID, strategyName = findStrategyForSymbol(instRows, symbol)
		}
		if strategyID == "" {
			strategyID = "manual"
			strategyName = "manual"
		}
		if direction == "" {
			if signedAmt < 0 {
				direction = "short"
			} else {
				direction = "long"
			}
		}

		qty := order.Amount
		exitPrice := order.Price
		realized := 0.0
		if signedAmt >= 0 {
			realized = qty * (exitPrice - entryPrice)
		} else {
			realized = qty * (entryPrice - exitPrice)
		}
		realizedNotional := qty * entryPrice

		now := time.Now()
		database.DB.Create(&models.StrategyOrder{
			PositionID:      existing.ID,
			StrategyID:      strategyID,
			StrategyName:    strategyName,
			OwnerID:         uid,
			Exchange:        bx.GetName(),
			Symbol:          symbol,
			Side:            strings.ToLower(order.Side),
			Purpose:         "close",
			OrderType:       "market",
			ClientOrderID:   order.ClientOrderID,
			ExchangeOrderID: order.ID,
			Status:          order.Status,
			RequestedQty:    qty,
			Price:           0,
			ExecutedQty:     qty,
			AvgPrice:        exitPrice,
			RequestedAt:     now,
			UpdatedAt:       now,
		})

		if hasExisting {
			newClosedQty := existing.ClosedQty + qty
			newAvgClose := existing.AvgClosePrice
			if newClosedQty > 0 {
				newAvgClose = ((existing.AvgClosePrice * existing.ClosedQty) + (exitPrice * qty)) / newClosedQty
			}
			database.DB.Model(&models.StrategyPosition{}).Where("id = ?", existing.ID).
				Updates(map[string]interface{}{
					"direction":         direction,
					"amount":            0,
					"avg_price":         entryPrice,
					"closed_qty":        newClosedQty,
					"avg_close_price":   newAvgClose,
					"realized_pn_l":     prevRealizedPnL + realized,
					"realized_notional": prevRealizedNotional + realizedNotional,
					"status":            "closed",
					"close_time":        order.Timestamp,
					"updated_at":        now,
				})
		} else {
			database.DB.Create(&models.StrategyPosition{
				StrategyID:       strategyID,
				StrategyName:     strategyName,
				OwnerID:          uid,
				Exchange:         bx.GetName(),
				Symbol:           symbol,
				Direction:        direction,
				Amount:           0,
				AvgPrice:         entryPrice,
				ClosedQty:        qty,
				AvgClosePrice:    exitPrice,
				RealizedPnL:      realized,
				RealizedNotional: realizedNotional,
				Status:           "closed",
				OpenTime:         openTime,
				CloseTime:        order.Timestamp,
				UpdatedAt:        now,
			})
		}

		if strategyID != "" && strategyID != "manual" {
			stratMgr.ReleaseOpenSlot(strategyID)
		}
		go func(ownerID uint, sym string) {
			_, _ = bx.CancelUSDMAllSymbolOrdersDetailed(ownerID, sym)
		}(uid, symbol)
		go func(ownerID uint, sym string) {
			deadline := time.Now().Add(45 * time.Second)
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for time.Now().Before(deadline) {
				amt, _, _, e := bx.USDMPositionAmtCached(ownerID, sym)
				if e == nil && amt == 0 {
					_, _ = bx.CancelUSDMAllSymbolOrdersDetailed(ownerID, sym)
					return
				}
				<-ticker.C
			}
		}(uid, symbol)
		metrics := strategy.BuildTradeCloseMetricsFromPosition(&existing, qty, exitPrice, order.Timestamp)
		stratMgr.NotifyExternalTradeClosed(uid, strategyID, strategyName, bx.GetName(), symbol, strings.ToLower(order.Side), qty, exitPrice, strings.ToLower(order.Status), "manual_close", metrics)

		c.JSON(http.StatusOK, gin.H{"status": "success"})
		return
	}

	var pos models.StrategyPosition
	if err := database.DB.Where("owner_id = ? AND symbol = ? AND status = ?", uid, symbol, "open").
		Order("open_time desc").
		First(&pos).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Open position not found"})
		return
	}

	clientOrderID := models.GenerateUUID()
	database.DB.Create(&models.StrategyOrder{
		PositionID:    pos.ID,
		StrategyID:    pos.StrategyID,
		StrategyName:  pos.StrategyName,
		OwnerID:       uid,
		Exchange:      pos.Exchange,
		Symbol:        pos.Symbol,
		Side:          "sell",
		Purpose:       "close",
		OrderType:     "market",
		ClientOrderID: clientOrderID,
		Status:        "requested",
		RequestedQty:  pos.Amount,
		Price:         0,
		RequestedAt:   time.Now(),
		UpdatedAt:     time.Now(),
	})

	order, err := stratMgr.GetExchange().PlaceOrder(uid, clientOrderID, pos.Symbol, "sell", pos.Amount, 0)
	if err != nil {
		database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
			Updates(map[string]interface{}{"status": "failed", "updated_at": time.Now()})
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	database.DB.Model(&models.StrategyOrder{}).Where("client_order_id = ?", clientOrderID).
		Updates(map[string]interface{}{
			"exchange_order_id": order.ID,
			"status":            order.Status,
			"updated_at":        time.Now(),
		})
	metrics := strategy.BuildTradeCloseMetricsFromPosition(&pos, order.Amount, order.Price, order.Timestamp)
	stratMgr.NotifyExternalTradeClosed(uid, pos.StrategyID, pos.StrategyName, pos.Exchange, pos.Symbol, "sell", order.Amount, order.Price, strings.ToLower(order.Status), "manual_close", metrics)

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
