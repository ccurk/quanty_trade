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
	"quanty_trade/internal/logger"
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

// OrderFill is one exchange-confirmed terminal fill, handed to the platform
// ledger for position accounting.
type OrderFill struct {
	OwnerID      uint
	StrategyID   string
	StrategyName string
	Exchange     string
	Symbol       string // display form, e.g. "APE/USDT"
	Side         string // buy/sell
	Purpose      string // open/close, taken from the platform order ledger
	ExecutedQty  float64
	AvgPrice     float64
	EventTime    time.Time
}

// OrderFillFunc applies a fill to the position ledger and returns the
// StrategyPosition row id it landed on (0 = not applied).
//
// This is a callback rather than a direct call because internal/strategy
// already imports internal/exchange (manager.go), so the reverse import would
// be a cycle. The point of routing out instead of accounting locally is that
// the ledger must have ONE implementation: the one in strategy that derives
// direction from the stored position (or from side on an opening fill) and
// refuses to invent a position for an orphan close. See 台账 #91.
type OrderFillFunc func(OrderFill) uint

// SetOrderFillHandler installs the ledger callback used by the user data
// stream. Set by the strategy manager before it starts a stream.
func (b *BinanceExchange) SetOrderFillHandler(fn OrderFillFunc) {
	b.streamMu.Lock()
	b.onOrderFill = fn
	b.streamMu.Unlock()
}

func (b *BinanceExchange) orderFillHandler() OrderFillFunc {
	b.streamMu.Lock()
	defer b.streamMu.Unlock()
	return b.onOrderFill
}

// EnsureUserDataStream starts (once per ownerID) Binance User Data Stream to
// receive order execution events: executionReport on spot, ORDER_TRADE_UPDATE
// on USDM futures.
//
// This used to `return nil` immediately when market == "usdm". Since
// conf_pro.yaml sets market: "usdm", that meant the stream never started at
// all: exchange_order_events was created 2026-03-26 and still had
// auto_increment=1 months later — not one row ever inserted. Server-side fills
// (exchange TP/SL) were therefore only discoverable by the 2s REST position
// poll in manager.go, which sees the position vanish but not the fill that
// closed it, so per-fill price/PnL/attribution were lost.
//
// Typical usage:
//   - Called when a strategy instance starts, so the UI can receive order updates
//     and the backend can persist execution events.
func (b *BinanceExchange) EnsureUserDataStream(ownerID uint, hub *ws.Hub) error {
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

// listenKeyURL returns the market-correct user data stream endpoint.
// USDM futures live at /fapi/v1/listenKey, NOT the spot /api/v3/userDataStream
// (which under baseURL https://fapi.binance.com is simply a 404). Docs:
// developers.binance.com USDS-M "Start/Keepalive/Close User Data Stream".
func (b *BinanceExchange) listenKeyURL(cred binanceCred) string {
	if b.market == "usdm" {
		return b.apiBaseURL(cred) + "/fapi/v1/listenKey"
	}
	return b.apiBaseURL(cred) + "/api/v3/userDataStream"
}

// listenKeyQuery is the query string for keepalive/close. Spot identifies the
// stream by an explicit listenKey parameter; USDM identifies it by the API key
// alone and takes no parameter.
func (b *BinanceExchange) listenKeyQuery(listenKey string) string {
	if b.market == "usdm" {
		return ""
	}
	q := url.Values{}
	q.Set("listenKey", listenKey)
	return "?" + q.Encode()
}

func (b *BinanceExchange) createListenKey(cred binanceCred) (string, error) {
	// createListenKey calls POST /fapi/v1/listenKey (usdm) or
	// POST /api/v3/userDataStream (spot).
	u := b.listenKeyURL(cred)
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
	// pingListenKey keeps the listenKey alive. Binance closes the stream after
	// 60 minutes without a keepalive; the 30-minute ticker above stays inside that.
	u := b.listenKeyURL(cred) + b.listenKeyQuery(listenKey)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, u, nil)
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
	u := b.listenKeyURL(cred) + b.listenKeyQuery(listenKey)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, u, nil)
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
		case "ORDER_TRADE_UPDATE":
			b.handleOrderTradeUpdate(s, raw)
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

// usdmOrderUpdate is the subset of ORDER_TRADE_UPDATE's nested "o" object that
// this platform persists.
//
// Field letters, per Binance USDS-M user data stream docs (a real frame:
// {"e":"ORDER_TRADE_UPDATE","E":..,"T":..,"o":{"s":"APEUSDT","S":"SELL",
// "X":"FILLED","q":"36","ap":"5.5600","l":"6","z":"36","L":"5.5600",
// "n":"0.00667200","N":"USDT","rp":"0.13200000",...}}):
//
//	s  symbol            c  clientOrderId    S  side          o  order type
//	x  execution type    X  order status     q  orig qty      p  price
//	ap average price     l  last filled qty  z  cum filled qty
//	L  last filled price n  commission       N  commission asset
//	T  trade time        t  trade id         rp realized profit OF THIS FILL
//
// Two traps this layout sets:
//   - the inner object's own "o" key is the ORDER TYPE, while the outer "o" is
//     the object itself. Read them from the right map or you get "LIMIT" where
//     you wanted a struct.
//   - every numeric is a STRING. toFloat already handles that; do not switch to
//     a typed json.Unmarshal that assumes float64.
type usdmOrderUpdate struct {
	Symbol         string
	OrderID        string
	ClientOrderID  string
	Side           string
	OrderType      string
	Status         string
	Price          float64
	OrigQty        float64
	ExecutedQty    float64
	LastQty        float64
	LastPrice      float64
	AvgPrice       float64
	RealizedProfit float64
	EventTime      time.Time
}

func parseOrderTradeUpdate(raw map[string]interface{}) (usdmOrderUpdate, bool) {
	o, ok := raw["o"].(map[string]interface{})
	if !ok {
		return usdmOrderUpdate{}, false
	}
	// Prefer the order's own transaction time (o.T); fall back to the envelope
	// event time (E) when absent.
	ts := toInt64Default(o["T"])
	if ts == 0 {
		ts = toInt64Default(raw["E"])
	}
	return usdmOrderUpdate{
		Symbol:         toString(o["s"]),
		OrderID:        toString(o["i"]),
		ClientOrderID:  toString(o["c"]),
		Side:           strings.ToLower(toString(o["S"])),
		OrderType:      strings.ToLower(toString(o["o"])),
		Status:         strings.ToLower(toString(o["X"])),
		Price:          toFloat(o["p"]),
		OrigQty:        toFloat(o["q"]),
		ExecutedQty:    toFloat(o["z"]),
		LastQty:        toFloat(o["l"]),
		LastPrice:      toFloat(o["L"]),
		AvgPrice:       toFloat(o["ap"]),
		RealizedProfit: toFloat(o["rp"]),
		EventTime:      time.UnixMilli(ts),
	}, true
}

// handleOrderTradeUpdate is the USDM counterpart of handleExecutionReport.
//
// Position accounting is NOT done here. It is routed to the strategy ledger via
// b.onOrderFill, which derives direction from the stored position and declines
// to fabricate one for an orphan close. Re-deriving direction from side at this
// layer is what wrote 台账 #91's phantom long rows (id=1897/1966: buy-to-close a
// short recorded as direction=long, realized_pn_l=0).
func (b *BinanceExchange) handleOrderTradeUpdate(s *binanceUserStream, raw map[string]interface{}) {
	u, ok := parseOrderTradeUpdate(raw)
	if !ok || database.DB == nil {
		return
	}

	// Audit row first and unconditionally: exchange_order_events is the only
	// place the raw payload (commission n/N, trade id t, realized profit rp)
	// ever lands, so it must not be contingent on the ledger step succeeding.
	ev := models.ExchangeOrderEvent{
		OwnerID:        s.ownerID,
		Exchange:       b.name,
		Symbol:         u.Symbol,
		OrderID:        u.OrderID,
		ClientOrderID:  u.ClientOrderID,
		Side:           u.Side,
		OrderType:      u.OrderType,
		Status:         u.Status,
		Price:          u.Price,
		OrigQty:        u.OrigQty,
		ExecutedQty:    u.ExecutedQty,
		LastQty:        u.LastQty,
		LastPrice:      u.LastPrice,
		RealizedProfit: u.RealizedProfit,
		EventTime:      u.EventTime,
		Raw:            string(mustJSON(raw)),
		CreatedAt:      time.Now(),
	}
	database.DB.Create(&ev)

	var stratOrder models.StrategyOrder
	stratOrderFound := false
	if u.ClientOrderID != "" {
		if err := database.DB.Where("client_order_id = ?", u.ClientOrderID).First(&stratOrder).Error; err == nil {
			stratOrderFound = true
		}
	}

	if stratOrderFound {
		// ap is the exchange's own average fill price across the whole order, so
		// unlike the spot path there is no running average to recompute here.
		avgPrice := u.AvgPrice
		if avgPrice <= 0 {
			avgPrice = u.LastPrice
		}
		database.DB.Model(&models.StrategyOrder{}).Where("id = ?", stratOrder.ID).
			Updates(map[string]interface{}{
				"exchange_order_id": u.OrderID,
				"status":            u.Status,
				"side":              u.Side,
				"order_type":        u.OrderType,
				"executed_qty":      u.ExecutedQty,
				"avg_price":         avgPrice,
				"updated_at":        time.Now(),
			})

		// Apply to the position ledger only on the terminal event: z and ap are
		// cumulative, so applying on each PARTIALLY_FILLED too would count the
		// same quantity repeatedly.
		if u.Status == "filled" && u.ExecutedQty > 0 {
			onFill := b.orderFillHandler()
			if onFill == nil {
				logger.Errorf("[USER STREAM] 成交无法入账:没有注册 ledger 回调 owner=%d symbol=%s client_order_id=%s",
					s.ownerID, u.Symbol, u.ClientOrderID)
			} else {
				// Attribute the fill to the owner who PLACED the order, not to
				// whichever stream delivered it. Every owner here points at the
				// same Binance account, and POST /fapi/v1/listenKey returns the
				// same key per API key, so all N owner streams receive all N
				// owners' fills. Routing by s.ownerID would send owner A's fill
				// into owner B's ledger, where it finds no matching position and
				// is discarded as an orphan close.
				fillOwner := stratOrder.OwnerID
				if fillOwner == 0 {
					fillOwner = s.ownerID
				}
				posID := onFill(OrderFill{
					OwnerID:      fillOwner,
					StrategyID:   stratOrder.StrategyID,
					StrategyName: stratOrder.StrategyName,
					Exchange:     b.name,
					Symbol:       b.displaySymbol(u.Symbol),
					Side:         u.Side,
					Purpose:      strings.ToLower(strings.TrimSpace(stratOrder.Purpose)),
					ExecutedQty:  u.ExecutedQty,
					AvgPrice:     avgPrice,
					EventTime:    u.EventTime,
				})
				if posID != 0 {
					database.DB.Model(&models.ExchangeOrderEvent{}).Where("id = ?", ev.ID).
						Update("position_id", posID)
				}
			}
		}
	}

	s.hub.BroadcastJSON(map[string]interface{}{
		"type":     "execution_report",
		"exchange": b.name,
		"owner_id": s.ownerID,
		"data": map[string]interface{}{
			"symbol":          u.Symbol,
			"order_id":        u.OrderID,
			"client_order_id": u.ClientOrderID,
			"side":            u.Side,
			"order_type":      u.OrderType,
			"status":          u.Status,
			"price":           u.Price,
			"orig_qty":        u.OrigQty,
			"executed_qty":    u.ExecutedQty,
			"last_qty":        u.LastQty,
			"last_price":      u.LastPrice,
			"realized_profit": u.RealizedProfit,
			"event_time":      u.EventTime,
		},
	})
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
				if onFill := b.orderFillHandler(); onFill != nil {
					fillOwner := stratOrder.OwnerID
					if fillOwner == 0 {
						fillOwner = s.ownerID
					}
					_ = onFill(OrderFill{
						OwnerID:      fillOwner,
						StrategyID:   stratOrder.StrategyID,
						StrategyName: stratOrder.StrategyName,
						Exchange:     b.name,
						Symbol:       b.displaySymbol(symbol),
						Side:         sideLower,
						Purpose:      strings.ToLower(strings.TrimSpace(stratOrder.Purpose)),
						ExecutedQty:  execQty,
						AvgPrice:     avgPrice,
						EventTime:    eventTime,
					})
				} else {
					logger.Errorf("[USER STREAM] 成交无法入账:没有注册 ledger 回调 owner=%d symbol=%s client_order_id=%s",
						s.ownerID, symbol, clientOrderID)
				}
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
