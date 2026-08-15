package exchange

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// 全市场标记价流（!markPrice@arr@1s）：一条共享连接携带所有 USDM symbol 的
// 标记价，不需要 per-symbol 订阅管理（对比 klineHub 的分片模型）。
// 它不做任何决策——ROI 守护(scanROILimits)仍是唯一决策路径；本流只负责把
// "该跑守护了"的时机从 5s 轮询提前到 ~1s 级（见 strategy_ws_guard.go）。

// MarkTick 是一次标记价更新（单 symbol）。
type MarkTick struct {
	Symbol string // 交易所原生大写无斜杠格式，如 BTCUSDT
	Price  float64
}

type markPriceEvent struct {
	Symbol string `json:"s"`
	Price  string `json:"p"`
}

// StartMarkPriceStream 启动全市场标记价订阅并在每个推送批次回调 cb。
// 断线按指数退避重连（1s→30s 封顶）；ctx 取消即退出。幂等：重复调用只生效一次。
func (b *BinanceExchange) StartMarkPriceStream(ctx context.Context, cb func([]MarkTick)) {
	if b == nil || b.Market() != "usdm" || cb == nil {
		return
	}
	if !markStreamOn.CompareAndSwap(false, true) {
		return
	}
	go b.runMarkPriceStream(ctx, cb)
}

// markStreamOn 防重复启动。进程级单例：全市场流只需要一条连接。
var markStreamOn atomic.Bool

func (b *BinanceExchange) runMarkPriceStream(ctx context.Context, cb func([]MarkTick)) {
	// 退出时复位启动闩,让进程内重启(ctx 换新)后 StartMarkPriceStream 能再次拉起,
	// 否则 WS 仓位守护会永久退化为 5s 轮询(CR P2)。
	defer markStreamOn.Store(false)
	// 币安 USDM 2026-04-23 行情 WS 迁移:普通行情流移到 /market 分支,老
	// wss://fstream.binance.com/ws/... 连得上但零数据(详见 binance_kline_hub.go)。
	// markprice@arr 同属 market 数据,必须走 /market/ws/,否则每 90s 读超时空转、
	// 一个 tick 都收不到(WS 仓位守护静默失效,退回 5s 轮询)。
	base := b.wsBaseURL
	if b.Market() == "usdm" {
		base += "/market"
	}
	url := base + "/ws/!markPrice@arr@1s"
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
		if err != nil {
			log.Printf("[markprice-hub] dial失败 err=%v backoff=%s", err, backoff)
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
		log.Printf("[markprice-hub] 已连接 %s", url)
		b.readMarkPriceLoop(ctx, conn, cb)
		_ = conn.Close()
	}
}

func (b *BinanceExchange) readMarkPriceLoop(ctx context.Context, conn *websocket.Conn, cb func([]MarkTick)) {
	// 币安服务端每 ~3min ping 一次；gorilla 默认 pong handler 会自动回，
	// 这里只需要读超时护栏（arr@1s 流静默 >90s 即视为死连接）。
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[markprice-hub] 读取中断 err=%v，重连", err)
			return
		}
		var evs []markPriceEvent
		if err := json.Unmarshal(raw, &evs); err != nil || len(evs) == 0 {
			continue
		}
		ticks := make([]MarkTick, 0, len(evs))
		for _, e := range evs {
			p, err := strconv.ParseFloat(e.Price, 64)
			if err != nil || p <= 0 || e.Symbol == "" {
				continue
			}
			ticks = append(ticks, MarkTick{Symbol: e.Symbol, Price: p})
		}
		if len(ticks) > 0 {
			cb(ticks)
		}
	}
}
