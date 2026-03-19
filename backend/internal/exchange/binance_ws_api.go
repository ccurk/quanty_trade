package exchange

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type BinanceDepthSnapshot struct {
	LastUpdateID int64      `json:"lastUpdateId"`
	Bids         [][]string `json:"bids"`
	Asks         [][]string `json:"asks"`
}

type BinanceRecentTrade struct {
	ID           int64  `json:"id"`
	Price        string `json:"price"`
	Qty          string `json:"qty"`
	QuoteQty     string `json:"quoteQty"`
	Time         int64  `json:"time"`
	IsBuyerMaker bool   `json:"isBuyerMaker"`
	IsBestMatch  bool   `json:"isBestMatch"`
}

func (b *BinanceExchange) wsAPIRequest(cred binanceCred, method string, params map[string]interface{}, out interface{}) error {
	u := b.wsAPIURL(cred)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	id := strconv.FormatInt(time.Now().UnixNano(), 10)
	req := map[string]interface{}{
		"id":     id,
		"method": method,
		"params": params,
	}
	if err := conn.WriteJSON(req); err != nil {
		return err
	}

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return err
	}

	var resp struct {
		ID     string          `json:"id"`
		Status int             `json:"status"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		return err
	}
	if resp.Status != 200 {
		if resp.Error != nil {
			return fmt.Errorf("binance wsapi error: %d %s", resp.Error.Code, resp.Error.Msg)
		}
		return fmt.Errorf("binance wsapi error: status=%d", resp.Status)
	}

	if out == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

func (b *BinanceExchange) FetchDepthSnapshotWS(symbol string, limit int) (*BinanceDepthSnapshot, error) {
	cred, err := b.getCred(0)
	if err != nil {
		cred = binanceCred{}
	}

	params := map[string]interface{}{
		"symbol": binanceSymbol(symbol),
	}
	if limit > 0 {
		params["limit"] = limit
	}

	var snap BinanceDepthSnapshot
	if err := b.wsAPIRequest(cred, "depth", params, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (b *BinanceExchange) FetchRecentTradesWS(symbol string, limit int) ([]BinanceRecentTrade, error) {
	cred, err := b.getCred(0)
	if err != nil {
		cred = binanceCred{}
	}

	params := map[string]interface{}{
		"symbol": binanceSymbol(symbol),
	}
	if limit > 0 {
		params["limit"] = limit
	}

	var trades []BinanceRecentTrade
	if err := b.wsAPIRequest(cred, "trades.recent", params, &trades); err != nil {
		return nil, err
	}
	return trades, nil
}

type BinanceDepthUpdate struct {
	Symbol string     `json:"symbol"`
	Bids   [][]string `json:"bids"`
	Asks   [][]string `json:"asks"`
	Time   time.Time  `json:"time"`
}

func (b *BinanceExchange) SubscribeDepth(symbol string, callback func(BinanceDepthUpdate)) error {
	sym := strings.ToLower(binanceSymbol(symbol))
	wsURL := b.wsBaseURL + "/ws/" + sym + "@depth"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return err
	}

	go func() {
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var payload struct {
				S string     `json:"s"`
				B [][]string `json:"b"`
				A [][]string `json:"a"`
				T int64      `json:"T"`
			}
			if err := json.Unmarshal(msg, &payload); err != nil {
				continue
			}
			callback(BinanceDepthUpdate{
				Symbol: payload.S,
				Bids:   payload.B,
				Asks:   payload.A,
				Time:   time.UnixMilli(payload.T),
			})
		}
	}()

	return nil
}
