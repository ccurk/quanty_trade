package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
	"quanty_trade/internal/ws"

	"github.com/gorilla/websocket"
)

type binanceUserStream struct {
	// ownerID is the user whose Binance credentials are used.
	ownerID uint
	// hub broadcasts exchange events to connected frontend clients.
	hub *ws.Hub
	// stop/done manage goroutine lifecycle.
	stop chan struct{}
	done chan struct{}
}

// EnsureUserDataStream starts (once per ownerID) Binance User Data Stream
// to receive account/order execution events (executionReport).
//
// Typical usage:
//   - Called when a strategy instance starts, so the UI can receive order updates
//     and the backend can persist execution events.
func (b *BinanceExchange) EnsureUserDataStream(ownerID uint, hub *ws.Hub) error {
	if b.market == "usdm" {
		return nil
	}
	if ownerID == 0 || hub == nil {
		return nil
	}

	b.streamMu.Lock()
	if s, ok := b.streamsByID[ownerID]; ok && s != nil {
		b.streamMu.Unlock()
		return nil
	}
	s := &binanceUserStream{
		ownerID: ownerID,
		hub:     hub,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	b.streamsByID[ownerID] = s
	b.streamMu.Unlock()

	go b.runUserStream(s)
	return nil
}

func (b *BinanceExchange) runUserStream(s *binanceUserStream) {
	// runUserStream maintains a long-lived websocket connection to Binance user data stream.
	// It handles:
	// - listenKey creation
	// - websocket connect/reconnect with exponential backoff
	// - listenKey keepalive
	defer close(s.done)

	backoff := 2 * time.Second
	for {
		select {
		case <-s.stop:
			return
		default:
		}

		cred, err := b.getCred(s.ownerID)
		if err != nil {
			time.Sleep(backoff)
			backoff = minDuration(backoff*2, 60*time.Second)
			continue
		}

		listenKey, err := b.createListenKey(cred)
		if err != nil {
			time.Sleep(backoff)
			backoff = minDuration(backoff*2, 60*time.Second)
			continue
		}

		wsURL := b.wsBaseURL + "/ws/" + listenKey
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			_ = b.closeListenKey(cred, listenKey)
			time.Sleep(backoff)
			backoff = minDuration(backoff*2, 60*time.Second)
			continue
		}

		backoff = 2 * time.Second
		keepaliveStop := make(chan struct{})
		go b.keepaliveListenKey(cred, listenKey, keepaliveStop)

		b.readUserStream(conn, s)

		close(keepaliveStop)
		_ = conn.Close()
		_ = b.closeListenKey(cred, listenKey)
	}
}

func (b *BinanceExchange) createListenKey(cred binanceCred) (string, error) {
	// createListenKey calls POST /api/v3/userDataStream.
	u := b.apiBaseURL(cred) + "/api/v3/userDataStream"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-MBX-APIKEY", cred.APIKey)
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("failed to create listenKey: status=%d", resp.StatusCode)
	}
	var parsed struct {
		ListenKey string `json:"listenKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if parsed.ListenKey == "" {
		return "", fmt.Errorf("empty listenKey")
	}
	return parsed.ListenKey, nil
}

func (b *BinanceExchange) keepaliveListenKey(cred binanceCred, listenKey string, stop chan struct{}) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_ = b.pingListenKey(cred, listenKey)
		}
	}
}

func (b *BinanceExchange) pingListenKey(cred binanceCred, listenKey string) error {
	// pingListenKey keeps the listenKey alive (Binance requires periodic keepalive).
	u := b.apiBaseURL(cred) + "/api/v3/userDataStream"
	q := url.Values{}
	q.Set("listenKey", listenKey)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, u+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", cred.APIKey)
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("failed to keepalive listenKey: status=%d", resp.StatusCode)
	}
	return nil
}

func (b *BinanceExchange) closeListenKey(cred binanceCred, listenKey string) error {
	// closeListenKey releases the listenKey on Binance side (best-effort).
	u := b.apiBaseURL(cred) + "/api/v3/userDataStream"
	q := url.Values{}
	q.Set("listenKey", listenKey)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, u+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", cred.APIKey)
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (b *BinanceExchange) readUserStream(conn *websocket.Conn, s *binanceUserStream) {
	// readUserStream reads websocket messages and dispatches supported event types.
	for {
		select {
		case <-s.stop:
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(70 * time.Minute))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var raw map[string]interface{}
		if err := json.Unmarshal(msg, &raw); err != nil {
			continue
		}
		ev, _ := raw["e"].(string)

		switch ev {
		case "executionReport":
			b.handleExecutionReport(s, raw)
		default:
			s.hub.BroadcastJSON(map[string]interface{}{
				"type":     "exchange_event",
				"exchange": b.name,
				"owner_id": s.ownerID,
				"event":    ev,
				"data":     raw,
			})
		}
	}
}

func (b *BinanceExchange) handleExecutionReport(s *binanceUserStream, raw map[string]interface{}) {
	// handleExecutionReport persists the raw exchange event, then updates
	// StrategyOrder/StrategyPosition ledgers if this event matches a platform order
	// (by clientOrderID).
	symbol, _ := raw["s"].(string)
	orderID := toString(raw["i"])
	clientOrderID, _ := raw["c"].(string)
	side, _ := raw["S"].(string)
	orderType, _ := raw["o"].(string)
	status, _ := raw["X"].(string)
	price := toFloat(raw["p"])
	origQty := toFloat(raw["q"])
	execQty := toFloat(raw["z"])
	lastQty := toFloat(raw["l"])
	lastPrice := toFloat(raw["L"])
	eventTime := time.UnixMilli(toInt64Default(raw["E"]))

	var stratOrder models.StrategyOrder
	stratOrderFound := false
	if database.DB != nil && clientOrderID != "" {
		if err := database.DB.Where("client_order_id = ?", clientOrderID).First(&stratOrder).Error; err == nil {
			stratOrderFound = true
		}
	}

	if database.DB != nil {
		database.DB.Create(&models.ExchangeOrderEvent{
			OwnerID:       s.ownerID,
			Exchange:      b.name,
			Symbol:        symbol,
			OrderID:       orderID,
			ClientOrderID: clientOrderID,
			Side:          strings.ToLower(side),
			OrderType:     strings.ToLower(orderType),
			Status:        strings.ToLower(status),
			Price:         price,
			OrigQty:       origQty,
			ExecutedQty:   execQty,
			LastQty:       lastQty,
			LastPrice:     lastPrice,
			EventTime:     eventTime,
			Raw:           string(mustJSON(raw)),
			CreatedAt:     time.Now(),
		})

		if stratOrderFound {
			statusLower := strings.ToLower(status)
			sideLower := strings.ToLower(side)
			orderTypeLower := strings.ToLower(orderType)

			avgPrice := stratOrder.AvgPrice
			if execQty > 0 {
				avgPrice = ((stratOrder.AvgPrice * stratOrder.ExecutedQty) + (lastPrice * lastQty)) / execQty
			}

			database.DB.Model(&models.StrategyOrder{}).Where("id = ?", stratOrder.ID).
				Updates(map[string]interface{}{
					"exchange_order_id": orderID,
					"status":            statusLower,
					"side":              sideLower,
					"order_type":        orderTypeLower,
					"executed_qty":      execQty,
					"avg_price":         avgPrice,
					"updated_at":        time.Now(),
				})

			if statusLower == "filled" {
				b.applyFillToPosition(s.hub, stratOrder.StrategyID, stratOrder.StrategyName, s.ownerID, b.name, symbol, sideLower, execQty, avgPrice, eventTime)
			} else if statusLower == "canceled" {
				// After cancellation, ensure no pre-position entry orders remain for this symbol
				_ = b.CancelPrePositionOpenOrders(s.ownerID, b.displaySymbol(symbol))
			}
		}
	}

	s.hub.BroadcastJSON(map[string]interface{}{
		"type":     "execution_report",
		"exchange": b.name,
		"owner_id": s.ownerID,
		"data": map[string]interface{}{
			"symbol":          symbol,
			"order_id":        orderID,
			"client_order_id": clientOrderID,
			"side":            strings.ToLower(side),
			"order_type":      strings.ToLower(orderType),
			"status":          strings.ToLower(status),
			"price":           price,
			"orig_qty":        origQty,
			"executed_qty":    execQty,
			"last_qty":        lastQty,
			"last_price":      lastPrice,
			"event_time":      eventTime,
		},
	})
}

func (b *BinanceExchange) applyFillToPosition(hub *ws.Hub, strategyID string, strategyName string, ownerID uint, exchangeName string, symbol string, side string, executedQty float64, avgPrice float64, eventTime time.Time) {
	// applyFillToPosition updates the strategy-scoped position ledger:
	// - buy fills open/increase a position with VWAP avgPrice
	// - sell fills decrease and eventually close the position
	if database.DB == nil || strategyID == "" || executedQty <= 0 {
		return
	}

	sym := b.displaySymbol(symbol)
	now := time.Now()

	var pos models.StrategyPosition
	err := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", ownerID, strategyID, sym, "open").First(&pos).Error
	if err != nil {
		if side != "buy" {
			return
		}
		pos = models.StrategyPosition{
			StrategyID:   strategyID,
			StrategyName: strategyName,
			OwnerID:      ownerID,
			Exchange:     exchangeName,
			Symbol:       sym,
			Amount:       executedQty,
			AvgPrice:     avgPrice,
			Status:       "open",
			OpenTime:     eventTime,
			UpdatedAt:    now,
		}
		database.DB.Create(&pos)
		if hub != nil {
			hub.BroadcastJSON(map[string]interface{}{
				"type": "position",
				"data": map[string]interface{}{
					"symbol":        pos.Symbol,
					"amount":        pos.Amount,
					"price":         pos.AvgPrice,
					"strategy_name": pos.StrategyName,
					"exchange_name": pos.Exchange,
					"status":        "active",
					"owner_id":      pos.OwnerID,
					"open_time":     pos.OpenTime,
				},
			})
		}
		return
	}

	if side == "buy" {
		newAmt := pos.Amount + executedQty
		newAvg := pos.AvgPrice
		if newAmt > 0 {
			newAvg = ((pos.AvgPrice * pos.Amount) + (avgPrice * executedQty)) / newAmt
		}
		database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).
			Updates(map[string]interface{}{"amount": newAmt, "avg_price": newAvg, "updated_at": now})
		if hub != nil {
			hub.BroadcastJSON(map[string]interface{}{
				"type": "position",
				"data": map[string]interface{}{
					"symbol":        pos.Symbol,
					"amount":        newAmt,
					"price":         newAvg,
					"strategy_name": pos.StrategyName,
					"exchange_name": pos.Exchange,
					"status":        "active",
					"owner_id":      pos.OwnerID,
					"open_time":     pos.OpenTime,
				},
			})
		}
		return
	}

	if side == "sell" {
		newAmt := pos.Amount - executedQty
		if newAmt <= 0 {
			database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).
				Updates(map[string]interface{}{
					"amount":     0,
					"status":     "closed",
					"close_time": eventTime,
					"updated_at": now,
				})
			if hub != nil {
				hub.BroadcastJSON(map[string]interface{}{
					"type": "position",
					"data": map[string]interface{}{
						"symbol":        pos.Symbol,
						"amount":        0,
						"price":         pos.AvgPrice,
						"strategy_name": pos.StrategyName,
						"exchange_name": pos.Exchange,
						"status":        "closed",
						"owner_id":      pos.OwnerID,
						"open_time":     pos.OpenTime,
						"close_time":    eventTime,
					},
				})
			}
		} else {
			database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).
				Updates(map[string]interface{}{"amount": newAmt, "updated_at": now})
			if hub != nil {
				hub.BroadcastJSON(map[string]interface{}{
					"type": "position",
					"data": map[string]interface{}{
						"symbol":        pos.Symbol,
						"amount":        newAmt,
						"price":         pos.AvgPrice,
						"strategy_name": pos.StrategyName,
						"exchange_name": pos.Exchange,
						"status":        "active",
						"owner_id":      pos.OwnerID,
						"open_time":     pos.OpenTime,
					},
				})
			}
		}
	}
}

func toString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

func toFloat(v interface{}) float64 {
	switch t := v.(type) {
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	case float64:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	default:
		return 0
	}
}

func toInt64Default(v interface{}) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case string:
		i, _ := strconv.ParseInt(t, 10, 64)
		return i
	case json.Number:
		i, _ := t.Int64()
		return i
	default:
		return 0
	}
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
