package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"quanty_trade/internal/conf"

	"github.com/redis/go-redis/v9"
)

type RedisBus struct {
	client *redis.Client
	prefix string
}

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
	SignalID    string    `json:"signal_id"`
	GeneratedAt time.Time `json:"generated_at"`
}

func NewRedisBusFromConfig() (*RedisBus, error) {
	c := conf.C().Redis
	if !c.Enabled {
		return nil, fmt.Errorf("redis disabled")
	}
	if c.Addr == "" {
		return nil, fmt.Errorf("redis addr is empty")
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
		return nil, err
	}
	return &RedisBus{client: client, prefix: prefix}, nil
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
