package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"quanty_trade/internal/conf"

	"github.com/redis/go-redis/v9"
)

type RedisBus struct {
	client *redis.Client
	prefix string
}

var acquireOpenSlotScript = redis.NewScript(`
local cur = tonumber(redis.call("GET", KEYS[1]) or "0")
local maxv = tonumber(ARGV[1])
if cur >= maxv then
  return -1
end
cur = redis.call("INCR", KEYS[1])
redis.call("EXPIRE", KEYS[1], tonumber(ARGV[2]))
return cur
`)

var releaseOpenSlotScript = redis.NewScript(`
local cur = tonumber(redis.call("GET", KEYS[1]) or "0")
if cur <= 0 then
  redis.call("SET", KEYS[1], "0")
  return 0
end
cur = redis.call("DECR", KEYS[1])
if cur < 0 then
  redis.call("SET", KEYS[1], "0")
  return 0
end
return cur
`)

type CandleMessage struct {
	Type       string                 `json:"type,omitempty"`
	StrategyID string                 `json:"strategy_id"`
	Symbol     string                 `json:"symbol"`
	Timestamp  time.Time              `json:"timestamp"`
	Open       float64                `json:"open"`
	High       float64                `json:"high"`
	Low        float64                `json:"low"`
	Close      float64                `json:"close"`
	Volume     float64                `json:"volume"`
	Extra      map[string]interface{} `json:"extra,omitempty"`
}

type HistoryMessage struct {
	Type       string          `json:"type"`
	StrategyID string          `json:"strategy_id"`
	Symbol     string          `json:"symbol"`
	Candles    []CandleMessage `json:"candles"`
}

type StateMessage struct {
	Type       string    `json:"type"`
	StrategyID string    `json:"strategy_id"`
	BootID     string    `json:"boot_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type SignalMessage struct {
	StrategyID  string    `json:"strategy_id"`
	OwnerID     uint      `json:"owner_id"`
	Symbol      string    `json:"symbol"`
	Action      string    `json:"action"`
	Side        string    `json:"side"`
	Amount      float64   `json:"amount"`
	TakeProfit  float64   `json:"take_profit"`
	StopLoss    float64   `json:"stop_loss"`
	// Price 是【信号生成那一刻策略用的评估价】(v40 里是当根 K 线收盘价 P)。
	// 策略的 tp/sl 是相对 P 算出来的绝对价,锚在 P 上、不随成交漂 ⇒ 市价单成交价一偏离
	// P,两段距离就同向缩水(实测四笔真实开仓:成交价高出评估价 +0.0978%/+0.2210%/
	// +0.0930%/−0.0107%,设计 1.25:1 落到实盘变成 0.44~1.29:1)。Go 用它把 tp/sl 的
	// 【相对距离】原样搬到实际成交价上,见 strategy.reanchorTPSLToFill。
	// 0 = 该策略没发这个字段,此时不重锚(与引入本字段之前的行为完全一致)。
	Price       float64   `json:"price"`
	AtrAbs      float64   `json:"atr_abs"`    // absolute ATR at signal time, for exit engineering (breakeven/trailing); 0 if strategy omits it
	Confidence  float64   `json:"confidence"` // strategy-reported signal confidence in [0,1]; 0 if strategy omits it
	SignalID    string    `json:"signal_id"`
	GeneratedAt time.Time `json:"generated_at"`
}

// NewRedisBusFromConfig 建立总线连接。每一次尝试的结果(成败)都会登记到 health.go
// 的进程级状态里,健康端点靠它对外说实话——调用方吞掉 error 也不会让失败消失。
func NewRedisBusFromConfig() (*RedisBus, error) {
	c := conf.C().Redis
	if !c.Enabled {
		return nil, fmt.Errorf("redis disabled")
	}
	if c.Addr == "" {
		err := fmt.Errorf("redis addr is empty")
		recordBusResult(nil, err)
		return nil, err
	}
	prefix := c.Prefix
	if prefix == "" {
		prefix = "qt"
	}
	client := redis.NewClient(&redis.Options{
		Addr:     c.Addr,
		Password: c.Password,
		DB:       c.DB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		recordBusResult(nil, err)
		return nil, err
	}
	b := &RedisBus{client: client, prefix: prefix}
	recordBusResult(b, nil)
	return b, nil
}

func (b *RedisBus) CandleChannel(strategyID string) string {
	return fmt.Sprintf("%s:candle:%s", b.prefix, strategyID)
}

func (b *RedisBus) SignalChannel(strategyID string) string {
	return fmt.Sprintf("%s:signal:%s", b.prefix, strategyID)
}

func (b *RedisBus) StateChannel(strategyID string) string {
	return fmt.Sprintf("%s:state:%s", b.prefix, strategyID)
}

func (b *RedisBus) OpenCountKey(strategyID string) string {
	return fmt.Sprintf("%s:open_count:%s", b.prefix, strategyID)
}

func (b *RedisBus) SetOpenCount(ctx context.Context, strategyID string, n int64, ttl time.Duration) error {
	if b == nil || b.client == nil || strings.TrimSpace(strategyID) == "" {
		return nil
	}
	sec := int64(ttl.Seconds())
	if sec <= 0 {
		sec = 6 * 60 * 60
	}
	key := b.OpenCountKey(strategyID)
	pipe := b.client.Pipeline()
	pipe.Set(ctx, key, n, time.Duration(sec)*time.Second)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *RedisBus) AcquireOpenSlot(ctx context.Context, strategyID string, max int, ttl time.Duration) (bool, int64, error) {
	if b == nil || b.client == nil || strings.TrimSpace(strategyID) == "" {
		return true, 0, nil
	}
	if max <= 0 {
		return true, 0, nil
	}
	sec := int64(ttl.Seconds())
	if sec <= 0 {
		sec = 6 * 60 * 60
	}
	key := b.OpenCountKey(strategyID)
	res, err := acquireOpenSlotScript.Run(ctx, b.client, []string{key}, max, sec).Int64()
	if err != nil {
		return false, 0, err
	}
	if res == -1 {
		return false, 0, nil
	}
	return true, res, nil
}

func (b *RedisBus) ReleaseOpenSlot(ctx context.Context, strategyID string) (int64, error) {
	if b == nil || b.client == nil || strings.TrimSpace(strategyID) == "" {
		return 0, nil
	}
	key := b.OpenCountKey(strategyID)
	return releaseOpenSlotScript.Run(ctx, b.client, []string{key}).Int64()
}

func (b *RedisBus) GetOpenCount(ctx context.Context, strategyID string) (int64, error) {
	if b == nil || b.client == nil || strings.TrimSpace(strategyID) == "" {
		return 0, nil
	}
	key := b.OpenCountKey(strategyID)
	n, err := b.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, nil
	}
	return n, nil
}

func (b *RedisBus) PublishCandle(ctx context.Context, msg CandleMessage) error {
	ch := b.CandleChannel(msg.StrategyID)
	if msg.Type == "" {
		msg.Type = "candle"
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, ch, raw).Err()
}

func (b *RedisBus) PublishHistory(ctx context.Context, strategyID string, symbol string, candles []CandleMessage) error {
	ch := b.CandleChannel(strategyID)
	msg := HistoryMessage{
		Type:       "history",
		StrategyID: strategyID,
		Symbol:     symbol,
		Candles:    candles,
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, ch, raw).Err()
}

func (b *RedisBus) PublishState(ctx context.Context, msg StateMessage) error {
	if msg.Type == "" {
		msg.Type = "ready"
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	ch := b.StateChannel(msg.StrategyID)
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, ch, raw).Err()
}

func (b *RedisBus) SubscribeState(ctx context.Context, strategyID string, handler func(StateMessage)) error {
	pubsub := b.client.Subscribe(ctx, b.StateChannel(strategyID))
	_, err := pubsub.Receive(ctx)
	if err != nil {
		return err
	}
	ch := pubsub.Channel()
	go func() {
		defer pubsub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				var s StateMessage
				if err := json.Unmarshal([]byte(m.Payload), &s); err != nil {
					continue
				}
				handler(s)
			}
		}
	}()
	return nil
}

func (b *RedisBus) SubscribeSignals(ctx context.Context, strategyID string, handler func(SignalMessage)) error {
	pubsub := b.client.Subscribe(ctx, b.SignalChannel(strategyID))
	_, err := pubsub.Receive(ctx)
	if err != nil {
		return err
	}
	ch := pubsub.Channel()
	go func() {
		defer pubsub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				var s SignalMessage
				if err := json.Unmarshal([]byte(m.Payload), &s); err != nil {
					continue
				}
				handler(s)
			}
		}
	}()
	return nil
}
