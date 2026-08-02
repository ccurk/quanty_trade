package strategy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// TestFakeRedisRoundTrip drives the fake server with a raw RESP client the way
// strategies' embedded MiniRedis does: AUTH → SUBSCRIBE (candle push must
// arrive as a pub/sub message frame) and PUBLISH on the signal channel (must
// surface on the orders channel).
func TestFakeRedisRoundTrip(t *testing.T) {
	s, err := newBacktestFakeRedis()
	if err != nil {
		t.Fatalf("start fake redis: %v", err)
	}
	defer s.Close()

	sub, err := net.Dial("tcp", s.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer sub.Close()
	subR := bufio.NewReader(sub)

	send := func(c net.Conn, parts ...string) {
		var b strings.Builder
		fmt.Fprintf(&b, "*%d\r\n", len(parts))
		for _, p := range parts {
			fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(p), p)
		}
		if _, err := c.Write([]byte(b.String())); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	readLine := func(r *bufio.Reader) string {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return strings.TrimRight(line, "\r\n")
	}

	send(sub, "AUTH", "whatever")
	if got := readLine(subR); got != "+OK" {
		t.Fatalf("AUTH reply = %q", got)
	}
	send(sub, "SUBSCRIBE", "qt:candle:sid-1")
	if got := readLine(subR); got != "*3" {
		t.Fatalf("SUBSCRIBE reply head = %q", got)
	}
	for i := 0; i < 4; i++ { // $9 subscribe $15 qt:candle:sid-1 — then :1
		readLine(subR)
	}
	if got := readLine(subR); got != ":1" {
		t.Fatalf("SUBSCRIBE count = %q", got)
	}

	if err := s.pushCandle([]byte(`{"type":"candle","symbol":"BTC/USDT","close":1}`)); err != nil {
		t.Fatalf("pushCandle: %v", err)
	}
	if got := readLine(subR); got != "*3" {
		t.Fatalf("push frame head = %q", got)
	}
	kind := ""
	for i := 0; i < 6; i++ {
		l := readLine(subR)
		if i == 1 {
			kind = l
		}
	}
	if kind != "message" {
		t.Fatalf("push frame kind = %q", kind)
	}

	pub, err := net.Dial("tcp", s.addr)
	if err != nil {
		t.Fatalf("dial pub: %v", err)
	}
	defer pub.Close()
	pubR := bufio.NewReader(pub)
	sig := `{"side":"buy","amount":0.5,"take_profit":110,"stop_loss":90,"confidence":0.8}`
	send(pub, "PUBLISH", "qt:signal:sid-1", sig)
	if got := readLine(pubR); got != ":1" {
		t.Fatalf("PUBLISH reply = %q", got)
	}

	select {
	case m := <-s.orders:
		raw, _ := json.Marshal(m)
		if m["side"] != "buy" {
			t.Fatalf("order side = %v (%s)", m["side"], raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("signal never reached orders channel")
	}

	// State-channel publishes must NOT become orders.
	send(pub, "PUBLISH", "qt:state:sid-1", `{"type":"heartbeat"}`)
	if got := readLine(pubR); got != ":1" {
		t.Fatalf("state PUBLISH reply = %q", got)
	}
	select {
	case m := <-s.orders:
		t.Fatalf("state publish leaked into orders: %v", m)
	case <-time.After(300 * time.Millisecond):
	}
}
