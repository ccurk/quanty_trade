package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ===========================================================================
// klineHub —— kline 合并订阅多路复用器
//
// 背景:旧实现是"一 symbol 一条 WS 连接"。策略同时盯几十个 symbol 时会开几十条
// 连接,网络一抖就是几十条连接同时挂 + 几十次重连握手,还容易撞币安"每 IP 新建
// 连接频率"上限。
//
// 币安 USDM WS 支持一条连接订阅多个流(官方文档:单连接最多 1024 个流,入站消息
// <=10 条/秒,连接 24h 过期)。本 hub 把 N 个 symbol 按上限分片,摊到少量共享连接:
//   - 每条连接(shard)最多 wsMaxStreamsPerShard 个流,远小于 1024,把单连接故障域
//     控制在小范围;
//   - 新 symbol 分给"当前最少且未满"的分片(平均分配),都满了才新开一条;
//   - 连接生命周期/重连/软ban 退避逻辑沿用旧实现,只是从"每 symbol"提升到"每分片";
//   - 增量订阅/退订走 SUBSCRIBE/UNSUBSCRIBE,限速 <=10 条/秒;subs map 始终是权威源,
//     任何重连都按 map 全量重订,ctrl 指令丢了也不会真漏。
//
// 对外仍复用握手节流 wsDialTokenWait() 控制新建连接频率。
// ===========================================================================

const (
	// wsMaxStreamsPerShard 每条 WS 连接最多订阅多少个 kline 流。
	// 币安上限 1024;取远小于上限的值以缩小单连接故障域,并满足"平均分配 + 不超限"。
	wsMaxStreamsPerShard = 50
	// wsShardCtrlMinInterval 单连接上两条 SUBSCRIBE/UNSUBSCRIBE 的最小间隔,
	// 保证不超过币安"10 条入站消息/秒"(110ms => 最多约 9 条/秒)。
	wsShardCtrlMinInterval = 110 * time.Millisecond
)

type klineCallback func(Candle)
type klineStatus func(event string, detail string, err error)

type klineSub struct {
	callback klineCallback
	onStatus klineStatus
}

type klineHub struct {
	b      *BinanceExchange
	mu     sync.Mutex
	shards []*klineShard
	nextID int
}

func newKlineHub(b *BinanceExchange) *klineHub {
	h := &klineHub{b: b}
	go h.heartbeatLoop()
	return h
}

// heartbeatLoop 每分钟打一条每条连接的心跳,直接把"这条连接挂了多少流、多久没收到
// K 线"打进日志,补上"SUBSCRIBE 补订不落日志"的观测盲点。
// 判活:streams>0 但超过 120s 没收到 K 线 = STALE(连上了却没数据,像端点/软ban)。
func (h *klineHub) heartbeatLoop() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		h.mu.Lock()
		shards := make([]*klineShard, len(h.shards))
		copy(shards, h.shards)
		h.mu.Unlock()
		for _, s := range shards {
			n := s.symbolCount()
			last := atomic.LoadInt64(&s.lastKlineUnix)
			age := "never"
			status := "healthy"
			if last > 0 {
				secs := time.Now().Unix() - last
				age = fmt.Sprintf("%ds", secs)
				if n > 0 && secs > 120 {
					status = "STALE-no-data"
				}
			} else if n > 0 {
				status = "STALE-no-data"
			}
			log.Printf("[BINANCE WS] shard=%d heartbeat=%s streams=%d last_kline=%s", s.id, status, n, age)
		}
	}
}

// subscribe 把一个 symbol 的 1m kline 订阅挂到某条共享连接上,返回退订函数。
func (h *klineHub) subscribe(symbol string, cb klineCallback, onStatus klineStatus) (func(), error) {
	sym := strings.ToLower(binanceSymbol(symbol))
	sub := &klineSub{callback: cb, onStatus: onStatus}

	h.mu.Lock()
	shard := h.pickShardLocked(sym)
	shard.addSub(sym, sub)
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			empty := shard.removeSub(sym, sub)
			if empty {
				h.removeShardLocked(shard)
			}
			h.mu.Unlock()
		})
	}, nil
}

// pickShardLocked 选一条连接(调用者需持 h.mu):
//  1. 已在跟这个 symbol 的分片 → 复用(同 symbol 多订阅者共享一个流,fan-out);
//  2. 否则挑"当前 symbol 数最少且未达上限"的分片(平均分配);
//  3. 都满了就新开一条。
func (h *klineHub) pickShardLocked(sym string) *klineShard {
	if s := chooseKlineShard(h.shards, sym); s != nil {
		return s
	}
	h.nextID++
	s := newKlineShard(h, h.nextID)
	h.shards = append(h.shards, s)
	go s.run()
	return s
}

// chooseKlineShard 从现有分片里选一个可复用的连接:优先已订这个 symbol 的分片(去重
// 共享),其次挑当前 symbol 数最少且未达上限的分片(平均分配);都不合适返回 nil
// (表示需要新开一条)。抽成纯函数便于单测分配策略。
func chooseKlineShard(shards []*klineShard, sym string) *klineShard {
	for _, s := range shards {
		if s.hasSymbol(sym) {
			return s
		}
	}
	var best *klineShard
	bestN := wsMaxStreamsPerShard // 只接受 symbol 数 < 上限的分片
	for _, s := range shards {
		if n := s.symbolCount(); n < bestN {
			best, bestN = s, n
		}
	}
	return best
}

func (h *klineHub) removeShardLocked(s *klineShard) {
	for i, cur := range h.shards {
		if cur == s {
			h.shards = append(h.shards[:i], h.shards[i+1:]...)
			break
		}
	}
	s.shutdown()
}

// ---------------------------------------------------------------------------

type wsCtrl struct {
	method string // SUBSCRIBE / UNSUBSCRIBE
	stream string // e.g. btcusdt@kline_1m
}

type klineShard struct {
	hub *klineHub
	id  int

	mu   sync.Mutex
	subs map[string][]*klineSub // symbol(lower) -> 订阅者们

	ctrl     chan wsCtrl // 连接存活期间的增量订阅指令
	stop     chan struct{}
	stopOnce sync.Once

	lastKlineUnix int64 // 最近一次分发 K 线的 Unix 秒(atomic),给心跳判活用
}

func newKlineShard(h *klineHub, id int) *klineShard {
	return &klineShard{
		hub:  h,
		id:   id,
		subs: map[string][]*klineSub{},
		ctrl: make(chan wsCtrl, 256),
		stop: make(chan struct{}),
	}
}

func (s *klineShard) hasSymbol(sym string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs[sym]) > 0
}

func (s *klineShard) symbolCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

func (s *klineShard) addSub(sym string, sub *klineSub) {
	s.mu.Lock()
	first := len(s.subs[sym]) == 0
	s.subs[sym] = append(s.subs[sym], sub)
	s.mu.Unlock()
	if first {
		s.enqueueCtrl(wsCtrl{method: "SUBSCRIBE", stream: sym + "@kline_1m"})
	}
}

// removeSub 移除一个订阅者;返回该分片是否已空(需回收整条连接)。
func (s *klineShard) removeSub(sym string, sub *klineSub) bool {
	s.mu.Lock()
	list := s.subs[sym]
	for i, x := range list {
		if x == sub {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	last := false
	if len(list) == 0 {
		delete(s.subs, sym)
		last = true
	} else {
		s.subs[sym] = list
	}
	empty := len(s.subs) == 0
	s.mu.Unlock()
	if last {
		s.enqueueCtrl(wsCtrl{method: "UNSUBSCRIBE", stream: sym + "@kline_1m"})
	}
	return empty
}

func (s *klineShard) enqueueCtrl(c wsCtrl) {
	select {
	case s.ctrl <- c:
	default:
		// 队列满就丢:重连时按 subs map 全量重订,不会真漏
	}
}

func (s *klineShard) currentStreams() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.subs))
	for sym := range s.subs {
		out = append(out, sym+"@kline_1m")
	}
	return out
}

func (s *klineShard) dispatch(sym string, c Candle) {
	s.mu.Lock()
	list := s.subs[sym]
	subs := make([]*klineSub, len(list))
	copy(subs, list)
	s.mu.Unlock()
	for _, sub := range subs {
		if sub.callback != nil {
			sub.callback(c)
		}
	}
}

// emitStatus 把该连接的生命周期事件 fan-out 给分片上所有 symbol 的订阅者,
// 保持与旧"每 symbol 一连接"一致的 onStatus 语义。
func (s *klineShard) emitStatus(event, detail string, err error) {
	s.mu.Lock()
	subs := make([]*klineSub, 0, len(s.subs))
	for _, list := range s.subs {
		subs = append(subs, list...)
	}
	s.mu.Unlock()
	for _, sub := range subs {
		if sub.onStatus != nil {
			sub.onStatus(event, detail, err)
		}
	}
}

func (s *klineShard) shutdown() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// run 是分片的连接主循环,结构沿用旧的单 symbol 版:
// normalBackoff 处理握手失败/被动断开(1s 起翻倍封顶 30s);
// silent 探测 90s 无数据的软 ban,按次数二次方退避到 5min;都等收到第一条数据才 reset。
func (s *klineShard) run() {
	const (
		handshakeTimeout = 10 * time.Second
		readDeadline     = 90 * time.Second
		normalMaxBackoff = 30 * time.Second
		silentBaseUnit   = 30 * time.Second
		silentMaxBackoff = 5 * time.Minute
	)
	normalBackoff := 1 * time.Second
	silentDisconnects := 0
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: handshakeTimeout,
		NetDialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	for {
		select {
		case <-s.stop:
			return
		default:
		}
		streams := s.currentStreams()
		if len(streams) == 0 {
			return // 分片已空(被回收)
		}
		// 连到 <base>/ws/<第一个流>:沿用被验证过的 raw-stream URL 形式,保证初始至少订上
		// 一个流;其余流连上后由 pumpCtrl 用 SUBSCRIBE 补订。消息与单流一样是 raw(未包装)。
		//
		// 币安 USDM 于 2026-04-23 迁移行情 WS:kline 等普通行情移到 /market 分支,老
		// wss://fstream.binance.com/ws/... 连得上但不再推送 market 数据(实测零数据)。
		// 故 usdm 必须走 /market/ws/;现货(stream.binance.com)不受此迁移影响。
		base := s.hub.b.wsBaseURL
		if s.hub.b.market == "usdm" {
			base += "/market"
		}
		wsURL := base + "/ws/" + streams[0]

		wsDialTokenWait()
		s.emitStatus("dialing", wsURL, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		conn, _, err := dialer.DialContext(ctx, wsURL, nil)
		cancel()
		if err != nil {
			log.Printf("[BINANCE WS] shard=%d connect failed streams=%d err=%v", s.id, len(streams), err)
			s.emitStatus("connect_failed", wsURL, err)
			sleepWithJitter(normalBackoff, s.stop)
			normalBackoff = nextBackoff(normalBackoff, normalMaxBackoff)
			continue
		}
		log.Printf("[BINANCE WS] shard=%d connected streams=%d", s.id, len(streams))
		// 不在此重置 normalBackoff;等收到第一条数据才算真正成功。
		s.emitStatus("connected", wsURL, nil)

		connClosed := make(chan struct{})
		go func(c *websocket.Conn) {
			select {
			case <-s.stop:
			case <-connClosed:
			}
			_ = c.Close()
		}(conn)

		ctrlDone := make(chan struct{})
		go s.pumpCtrl(conn, streams[0], connClosed, ctrlDone)

		gotAnyData := false
		reconnected := false
		for !reconnected {
			select {
			case <-s.stop:
				_ = conn.Close()
				close(connClosed)
				<-ctrlDone
				return
			default:
			}
			_ = conn.SetReadDeadline(time.Now().Add(readDeadline))
			_, msg, rerr := conn.ReadMessage()
			if rerr != nil {
				_ = conn.Close()
				close(connClosed)
				<-ctrlDone
				isTimeout := false
				if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
					isTimeout = true
				}
				if isTimeout && !gotAnyData {
					silentDisconnects++
					penalty := time.Duration(silentDisconnects*silentDisconnects) * silentBaseUnit
					if penalty > silentMaxBackoff {
						penalty = silentMaxBackoff
					}
					log.Printf("[BINANCE WS] shard=%d silent disconnect (likely soft-ban) consecutive=%d wait=%s", s.id, silentDisconnects, penalty)
					s.emitStatus("silent_disconnect", fmt.Sprintf("consecutive=%d wait=%s", silentDisconnects, penalty), nil)
					sleepWithJitter(penalty, s.stop)
				} else {
					log.Printf("[BINANCE WS] shard=%d disconnected err=%v (reconnect in %s)", s.id, rerr, normalBackoff)
					s.emitStatus("disconnected", wsURL, rerr)
					sleepWithJitter(normalBackoff, s.stop)
					normalBackoff = nextBackoff(normalBackoff, normalMaxBackoff)
				}
				reconnected = true
				break
			}
			if !gotAnyData {
				gotAnyData = true
				normalBackoff = 1 * time.Second
				silentDisconnects = 0
			}
			s.handleMessage(msg)
		}
	}
}

// pumpCtrl 是连接的唯一写入者(应用层):连上后补订当前全量流(除 URL 里那个),
// 再把增量 SUBSCRIBE/UNSUBSCRIBE 限速发出。gorilla 的自动 pong 走 WriteControl,
// 与本 goroutine 的 WriteJSON 并发安全。
func (s *klineShard) pumpCtrl(conn *websocket.Conn, urlStream string, connClosed <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	// 以 subs map 为准补订(覆盖断线期间的全部增量);排除 URL 已自动订阅的那个流。
	var seq int64
	initial := make([]string, 0)
	for _, st := range s.currentStreams() {
		if st != urlStream {
			initial = append(initial, st)
		}
	}
	if len(initial) > 0 {
		seq++
		if err := writeWSControl(conn, "SUBSCRIBE", initial, seq); err != nil {
			_ = conn.Close()
			return
		}
	}

	last := time.Now()
	for {
		select {
		case <-s.stop:
			return
		case <-connClosed:
			return
		case c := <-s.ctrl:
			if d := time.Since(last); d < wsShardCtrlMinInterval {
				select {
				case <-time.After(wsShardCtrlMinInterval - d):
				case <-s.stop:
					return
				case <-connClosed:
					return
				}
			}
			seq++
			if err := writeWSControl(conn, c.method, []string{c.stream}, seq); err != nil {
				_ = conn.Close()
				return
			}
			last = time.Now()
		}
	}
}

func writeWSControl(conn *websocket.Conn, method string, streams []string, id int64) error {
	if len(streams) == 0 {
		return nil
	}
	req := map[string]interface{}{
		"method": method,
		"params": streams,
		"id":     id,
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteJSON(req)
}

// handleMessage 解析一条 raw kline 事件并路由到对应 symbol 的订阅者。
// 订阅 ack({"result":null,"id":N})没有 k/s 字段,会被安静忽略。
func (s *klineShard) handleMessage(msg []byte) {
	var payload struct {
		S string `json:"s"`
		K struct {
			T      int64           `json:"t"` // kline 开始时间(整分 :00)——策略时间窗校验依赖它
			TClose int64           `json:"T"` // kline 结束时间(:59.999)——必须显式声明,否则 Go 大小写回退会用 "T" 覆盖 "t"
			O      json.RawMessage `json:"o"`
			H      json.RawMessage `json:"h"`
			L      json.RawMessage `json:"l"`
			C      json.RawMessage `json:"c"`
			V      json.RawMessage `json:"v"` // 成交量
			VTaker json.RawMessage `json:"V"` // 主动买入量——同上,防止 "V" 覆盖 "v"
			X      bool            `json:"x"`
		} `json:"k"`
	}
	if err := json.Unmarshal(msg, &payload); err != nil {
		return
	}
	if payload.S == "" || !payload.K.X {
		return // 订阅 ack 或未收盘
	}
	atomic.StoreInt64(&s.lastKlineUnix, time.Now().Unix())
	open, _ := parseBinanceNum(payload.K.O)
	high, _ := parseBinanceNum(payload.K.H)
	low, _ := parseBinanceNum(payload.K.L)
	closeP, _ := parseBinanceNum(payload.K.C)
	vol, _ := parseBinanceNum(payload.K.V)
	s.dispatch(strings.ToLower(payload.S), Candle{
		Timestamp: time.UnixMilli(payload.K.T),
		Open:      open,
		High:      high,
		Low:       low,
		Close:     closeP,
		Volume:    vol,
	})
}

func nextBackoff(cur, max time.Duration) time.Duration {
	cur *= 2
	if cur > max {
		cur = max
	}
	return cur
}
