package exchange

import (
	"fmt"
	"sort"
	"testing"
)

// newTestShard 造一个不启动网络循环的分片,只用于测试分配/簿记逻辑。
func newTestShard(id int) *klineShard {
	return &klineShard{
		id:   id,
		subs: map[string][]*klineSub{},
		ctrl: make(chan wsCtrl, 256),
		stop: make(chan struct{}),
	}
}

func noopSub() *klineSub { return &klineSub{callback: func(Candle) {}} }

// 分配策略:cap 上限强制 + 平均分配 + 同 symbol 复用同一分片。
func TestChooseKlineShard(t *testing.T) {
	// 空集合 → 需要新开(返回 nil)
	if s := chooseKlineShard(nil, "btcusdt"); s != nil {
		t.Fatalf("empty shards should return nil, got shard %d", s.id)
	}

	// 已订该 symbol 的分片优先复用
	a := newTestShard(1)
	b := newTestShard(2)
	a.addSub("ethusdt", noopSub()) // a=1
	b.addSub("btcusdt", noopSub()) // b=1
	if got := chooseKlineShard([]*klineShard{a, b}, "btcusdt"); got != b {
		t.Fatalf("existing-symbol should reuse its shard b, got %v", got)
	}

	// 新 symbol 分给负载最少的分片(平均分配):a 有 1,b 有 2 → 选 a
	b.addSub("solusdt", noopSub()) // b=2
	if got := chooseKlineShard([]*klineShard{a, b}, "dogeusdt"); got != a {
		t.Fatalf("new symbol should go to least-loaded shard a, got %v", got)
	}

	// 所有分片都满 → 返回 nil(需要新开)
	full1 := newTestShard(3)
	full2 := newTestShard(4)
	for i := 0; i < wsMaxStreamsPerShard; i++ {
		full1.addSub(fmt.Sprintf("f1sym%d", i), noopSub())
		full2.addSub(fmt.Sprintf("f2sym%d", i), noopSub())
	}
	if got := chooseKlineShard([]*klineShard{full1, full2}, "newsym"); got != nil {
		t.Fatalf("all shards full should return nil, got shard %d (count=%d)", got.id, got.symbolCount())
	}
}

// 通过 hub.subscribe 走完整分配路径,断言:每分片不超上限、总数正确、且用的是最少
// 连接数(ceil(N/cap))。分配策略是"装箱到上限"以最小化连接数(B 的首要目标);
// symbol 逐个到达、且不做在线迁移,所以不追求严格均分——最后一条连接可能装得少。
// 注意:每个新分片会起 run() goroutine 尝试拨号;测试给一个立即失败的 wsBaseURL,
// run() 只会进退避循环,并在 stop() 后退出,不影响分配断言。
func TestHubPacksUnderCap(t *testing.T) {
	b := &BinanceExchange{wsBaseURL: "wss://127.0.0.1:0"} // 拨号必失败,run() 只退避
	h := newKlineHub(b)

	n := wsMaxStreamsPerShard*2 + 5 // 需要 3 个分片
	stops := make([]func(), 0, n)
	for i := 0; i < n; i++ {
		stop, err := h.subscribe(fmt.Sprintf("sym%dusdt", i), func(Candle) {}, nil)
		if err != nil {
			t.Fatalf("subscribe %d failed: %v", i, err)
		}
		stops = append(stops, stop)
	}
	defer func() {
		for _, s := range stops {
			s()
		}
	}()

	h.mu.Lock()
	shardCount := len(h.shards)
	counts := make([]int, 0, shardCount)
	total := 0
	for _, s := range h.shards {
		c := s.symbolCount()
		counts = append(counts, c)
		total += c
		if c > wsMaxStreamsPerShard {
			h.mu.Unlock()
			t.Fatalf("shard %d exceeds cap: %d > %d", s.id, c, wsMaxStreamsPerShard)
		}
	}
	h.mu.Unlock()

	if total != n {
		t.Fatalf("total symbols across shards = %d, want %d", total, n)
	}
	// 最少连接数
	wantShards := (n + wsMaxStreamsPerShard - 1) / wsMaxStreamsPerShard
	if shardCount != wantShards {
		t.Fatalf("shard count = %d, want %d (counts=%v)", shardCount, wantShards, counts)
	}
	sort.Ints(counts)
}

// 退订会腾出容量:装满两片后退订其中一片的若干 symbol,新 symbol 应回填到有空位的
// 那片,而不是无谓新开连接。
func TestHubReusesFreedCapacity(t *testing.T) {
	b := &BinanceExchange{wsBaseURL: "wss://127.0.0.1:0"}
	h := newKlineHub(b)

	stops := map[string]func(){}
	for i := 0; i < wsMaxStreamsPerShard; i++ { // 恰好装满第一片
		sym := fmt.Sprintf("a%dusdt", i)
		stop, _ := h.subscribe(sym, func(Candle) {}, nil)
		stops[sym] = stop
	}
	h.mu.Lock()
	if len(h.shards) != 1 {
		n := len(h.shards)
		h.mu.Unlock()
		t.Fatalf("expected 1 full shard, got %d", n)
	}
	h.mu.Unlock()

	// 退订 3 个 → 第一片腾出 3 个空位
	for i := 0; i < 3; i++ {
		sym := fmt.Sprintf("a%dusdt", i)
		stops[sym]()
	}
	// 再订 3 个新 symbol,应回填到第一片,不新开连接
	for i := 0; i < 3; i++ {
		stop, _ := h.subscribe(fmt.Sprintf("b%dusdt", i), func(Candle) {}, nil)
		defer stop()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.shards) != 1 {
		t.Fatalf("should still be 1 shard after refill, got %d", len(h.shards))
	}
	if c := h.shards[0].symbolCount(); c != wsMaxStreamsPerShard {
		t.Fatalf("shard symbolCount = %d, want %d", c, wsMaxStreamsPerShard)
	}
}

// 退订后计数正确;最后一个订阅者走后分片判定为空(触发回收)。
func TestShardRefcountAndEmpty(t *testing.T) {
	s := newTestShard(1)
	sub1, sub2 := noopSub(), noopSub()

	s.addSub("btcusdt", sub1)
	s.addSub("btcusdt", sub2) // 同 symbol 两个订阅者
	s.addSub("ethusdt", noopSub())
	if got := s.symbolCount(); got != 2 {
		t.Fatalf("symbolCount = %d, want 2", got)
	}

	// 移除 btc 的一个订阅者:symbol 仍在(refcount 2→1),分片非空
	if empty := s.removeSub("btcusdt", sub1); empty {
		t.Fatal("removing one of two subs should not empty shard")
	}
	if got := s.symbolCount(); got != 2 {
		t.Fatalf("after partial remove symbolCount = %d, want 2", got)
	}
	// 移除 btc 的最后一个订阅者:symbol 消失
	if empty := s.removeSub("btcusdt", sub2); empty {
		t.Fatal("still has ethusdt, should not be empty")
	}
	if s.hasSymbol("btcusdt") {
		t.Fatal("btcusdt should be gone")
	}
	if got := s.symbolCount(); got != 1 {
		t.Fatalf("symbolCount = %d, want 1", got)
	}
}

func TestNextBackoff(t *testing.T) {
	// 1s→2s→4s ... 封顶 30s
	got := nextBackoff(1e9, 30e9) // 1s, cap 30s
	if got != 2e9 {
		t.Fatalf("1s doubled = %v, want 2s", got)
	}
	if got := nextBackoff(20e9, 30e9); got != 30e9 {
		t.Fatalf("20s doubled capped = %v, want 30s", got)
	}
}
