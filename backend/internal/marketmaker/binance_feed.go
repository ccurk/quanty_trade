package marketmaker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"quanty_trade/internal/logger"
)

// BinanceFeed streams best bid/ask from Binance SPOT as the reference price.
// (Spot, not USDM — cross-exchange MM references the spot mid.)
func init() {
	RegisterFeed("binance", func() (FeedSource, error) {
		return &BinanceFeed{
			http:     &http.Client{Timeout: 8 * time.Second},
			wsBase:   "wss://stream.binance.com:9443",
			restBase: "https://api.binance.com",
		}, nil
	})
}

type BinanceFeed struct {
	http     *http.Client
	wsBase   string
	restBase string
}

func (f *BinanceFeed) Name() string { return "binance" }

func (f *BinanceFeed) FetchBookTicker(symbol string) (BookTicker, error) {
	u := f.restBase + "/api/v3/ticker/bookTicker?" + url.Values{"symbol": {normSym(symbol)}}.Encode()
	resp, err := f.http.Get(u)
	if err != nil {
		return BookTicker{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return BookTicker{}, fmt.Errorf("binance bookTicker -> %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var r struct {
		Symbol   string `json:"symbol"`
		BidPrice string `json:"bidPrice"`
		BidQty   string `json:"bidQty"`
		AskPrice string `json:"askPrice"`
		AskQty   string `json:"askQty"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return BookTicker{}, err
	}
	return BookTicker{Symbol: r.Symbol, BidPx: atof(r.BidPrice), BidQty: atof(r.BidQty), AskPx: atof(r.AskPrice), AskQty: atof(r.AskQty), Ts: time.Now()}, nil
}

func (f *BinanceFeed) SubscribeBookTicker(symbol string, cb func(BookTicker)) (func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	sym := strings.ToLower(normSym(symbol))
	go func() {
		backoff := time.Second
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			url := f.wsBase + "/ws/" + sym + "@bookTicker"
			conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
			if err != nil {
				logger.Warnf("[mm-feed] binance dial失败 sym=%s err=%v backoff=%s", sym, err, backoff)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if backoff *= 2; backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
				continue
			}
			backoff = time.Second
			f.readLoop(ctx, conn, cb)
			_ = conn.Close()
		}
	}()
	return cancel, nil
}

func (f *BinanceFeed) readLoop(ctx context.Context, conn *websocket.Conn, cb func(BookTicker)) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var e struct {
			S string `json:"s"`
			B string `json:"b"`
			BB string `json:"B"`
			A string `json:"a"`
			AA string `json:"A"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		bt := BookTicker{Symbol: e.S, BidPx: atof(e.B), BidQty: atof(e.BB), AskPx: atof(e.A), AskQty: atof(e.AA), Ts: time.Now()}
		if bt.BidPx > 0 && bt.AskPx > 0 {
			cb(bt)
		}
	}
}
