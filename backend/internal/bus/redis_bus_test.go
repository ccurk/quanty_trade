package bus

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"quanty_trade/internal/conf"
)

func TestRedisBusPublishSubscribe(t *testing.T) {
	_ = os.Setenv("REDIS_ENABLED", "true")
	_ = os.Setenv("REDIS_ADDR", "127.0.0.1:6379")
	_ = os.Setenv("REDIS_PREFIX", "qt_test")
	conf.MustLoad()

	b, err := NewRedisBusFromConfig()
	if err != nil {
		t.Skip()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	got := make(chan SignalMessage, 1)
	if err := b.SubscribeSignals(ctx, "s1", func(s SignalMessage) { got <- s }); err != nil {
		t.Fatal(err)
	}

	msg := SignalMessage{
		StrategyID:  "s1",
		OwnerID:     1,
		Symbol:      "BTC/USDT",
		Action:      "open",
		Side:        "buy",
		Amount:      0.01,
		TakeProfit:  1,
		StopLoss:    1,
		SignalID:    "sig1",
		GeneratedAt: time.Now(),
	}
	raw, _ := json.Marshal(msg)
	if err := b.client.Publish(ctx, b.SignalChannel("s1"), raw).Err(); err != nil {
		t.Fatal(err)
	}

	select {
	case v := <-got:
		if v.SignalID != "sig1" {
			t.Fatalf("unexpected signal_id: %s", v.SignalID)
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}

