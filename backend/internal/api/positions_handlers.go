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

	"github.com/gin-gonic/gin"
)

type strategyPositionMeta struct {
	StrategyID   string
	StrategyName string
	OpenTime     time.Time
	AvgPrice     float64
	TakeProfit   float64
	StopLoss     float64
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
		}
	}
	return bySymbol
}

func loadUserStrategyInstances(ownerID uint) []models.StrategyInstance {
	var instRows []models.StrategyInstance
	_ = database.DB.Where("owner_id = ?", ownerID).Order("updated_at desc").Find(&instRows).Error
	return instRows
}

func findStrategyInstanceForSymbol(instRows []models.StrategyInstance, sym string) *models.StrategyInstance {
	want := exchange.NormalizeSymbol(sym)
	for i := range instRows {
		var cfg map[string]interface{}
		if err := json.Unmarshal([]byte(instRows[i].Config), &cfg); err != nil {
			continue
		}
		if v, ok := cfg["symbol"].(string); ok && exchange.NormalizeSymbol(v) == want {
			return &instRows[i]
		}
		if raw, ok := cfg["symbols"]; ok {
			if xs, ok := raw.([]interface{}); ok {
				for _, it := range xs {
					if s, ok := it.(string); ok && exchange.NormalizeSymbol(s) == want {
						return &instRows[i]
					}
				}
			} else if xs, ok := raw.([]string); ok {
				for _, s := range xs {
					if exchange.NormalizeSymbol(s) == want {
						return &instRows[i]
					}
				}
			}
		}
	}
	return nil
}

func findStrategyForSymbol(instRows []models.StrategyInstance, sym string) (string, string) {
	if si := findStrategyInstanceForSymbol(instRows, sym); si != nil {
		return si.ID, si.Name
	}
	return "", ""
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
					if si != nil || orderMeta[key].StrategyID != "" {
						strategyID := ""
						strategyName := ""
						if si != nil {
							strategyID = si.ID
							strategyName = si.Name
						} else {
							strategyID = orderMeta[key].StrategyID
							strategyName = orderMeta[key].StrategyName
						}
						tp, sl := deriveTPSLFromStrategyInstance(si, p.Price, p.Direction)
						now := time.Now()
						pos := models.StrategyPosition{
							StrategyID:   strategyID,
							StrategyName: strategyName,
							OwnerID:      uid,
							Exchange:     bx.GetName(),
							Symbol:       p.Symbol,
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
	if err := query.Order("open_time desc").Find(&rows).Error; err != nil {
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
			StrategyID:         p.StrategyID,
			Symbol:             p.Symbol,
			Direction:          "long",
			Amount:             p.Amount,
			Price:              p.AvgPrice,
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

	sort.Slice(positions, func(i, j int) bool { return positions[i].OpenTime.After(positions[j].OpenTime) })
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
		prevRealizedPnL := 0.0
		prevRealizedNotional := 0.0
		if hasExisting {
			strategyID = existing.StrategyID
			strategyName = existing.StrategyName
			openTime = existing.OpenTime
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
			StrategyID:      strategyID,
			StrategyName:    strategyName,
			OwnerID:         uid,
			Exchange:        bx.GetName(),
			Symbol:          symbol,
			Side:            strings.ToLower(order.Side),
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
				amt, _, _, e := bx.USDMPositionAmt(ownerID, sym)
				if e == nil && amt == 0 {
					_, _ = bx.CancelUSDMAllSymbolOrdersDetailed(ownerID, sym)
					return
				}
				<-ticker.C
			}
		}(uid, symbol)
		stratMgr.NotifyExternalTradeClosed(uid, strategyID, strategyName, bx.GetName(), symbol, strings.ToLower(order.Side), qty, exitPrice, strings.ToLower(order.Status), "manual_close")

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
		StrategyID:    pos.StrategyID,
		StrategyName:  pos.StrategyName,
		OwnerID:       uid,
		Exchange:      pos.Exchange,
		Symbol:        pos.Symbol,
		Side:          "sell",
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
	stratMgr.NotifyExternalTradeClosed(uid, pos.StrategyID, pos.StrategyName, pos.Exchange, pos.Symbol, "sell", order.Amount, order.Price, strings.ToLower(order.Status), "manual_close")

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
