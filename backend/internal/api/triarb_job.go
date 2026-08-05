package api

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// ===========================================================================
// 三角套利检测器(Phase 1,只读,绝不下单)
//
// 目的:用币安实盘现货盘口,持续计算每个三角环(USDT→A→B→USDT)扣手续费后的净利,
// 出现 net>0 就记一条(多大),每分钟打一条"当前最优环净利"心跳。
// 用来验证:币安内三角套利到底有没有可抓的正机会、多频繁、多大。
//
// 快照实测显示净利恒为负(~-0.28%,毛差 1-2bp << 30bp 手续费),本检测器就是要
// 用连续数据坐实/推翻这个结论,再决定要不要做 Phase 2 执行。不接账户、不下单。
// ===========================================================================

// triFeePerLeg 单腿 taker 手续费(按 BNB 抵扣后 ~0.075%;无 BNB 抵扣是 0.1%)。
// 检测器同时会打印毛差,方便你两种费率下自己判断。
const triFeePerLeg = 0.00075

type triLeg struct {
	sym string // 现货 symbol,如 BTCUSDT / ETHBTC
	buy bool   // true=用 quote 买 base(吃 ask);false=卖 base 换 quote(吃 bid)
}

type triangleDef struct {
	name string
	legs [3]triLeg
}

// 只用最深、最可能出现瞬时错价的主流三角,双向都算。
var triArbTriangles = []triangleDef{
	{"USDT>BTC>ETH", [3]triLeg{{"BTCUSDT", true}, {"ETHBTC", true}, {"ETHUSDT", false}}},
	{"USDT>ETH>BTC", [3]triLeg{{"ETHUSDT", true}, {"ETHBTC", false}, {"BTCUSDT", false}}},
	{"USDT>BTC>BNB", [3]triLeg{{"BTCUSDT", true}, {"BNBBTC", true}, {"BNBUSDT", false}}},
	{"USDT>BNB>BTC", [3]triLeg{{"BNBUSDT", true}, {"BNBBTC", false}, {"BTCUSDT", false}}},
	{"USDT>BTC>SOL", [3]triLeg{{"BTCUSDT", true}, {"SOLBTC", true}, {"SOLUSDT", false}}},
	{"USDT>SOL>BTC", [3]triLeg{{"SOLUSDT", true}, {"SOLBTC", false}, {"BTCUSDT", false}}},
	{"USDT>ETH>BNB", [3]triLeg{{"ETHUSDT", true}, {"BNBETH", true}, {"BNBUSDT", false}}},
	{"USDT>BNB>ETH", [3]triLeg{{"BNBUSDT", true}, {"BNBETH", false}, {"ETHUSDT", false}}},
	{"USDT>BTC>XRP", [3]triLeg{{"BTCUSDT", true}, {"XRPBTC", true}, {"XRPUSDT", false}}},
	{"USDT>XRP>BTC", [3]triLeg{{"XRPUSDT", true}, {"XRPBTC", false}, {"BTCUSDT", false}}},
}

type triBook struct {
	mu  sync.RWMutex
	bid map[string]float64
	ask map[string]float64
}

func newTriBook() *triBook {
	return &triBook{bid: map[string]float64{}, ask: map[string]float64{}}
}

func (b *triBook) set(sym string, bid, ask float64) {
	b.mu.Lock()
	b.bid[sym] = bid
	b.ask[sym] = ask
	b.mu.Unlock()
}

func (b *triBook) get(sym string) (bid, ask float64) {
	b.mu.RLock()
	bid, ask = b.bid[sym], b.ask[sym]
	b.mu.RUnlock()
	return
}

// evalNet 返回该三角环扣 feePerLeg 后的净利%(以及是否所有腿都有报价)。
func (b *triBook) evalNet(t triangleDef, feePerLeg float64) (float64, bool) {
	r := 1.0
	for _, leg := range t.legs {
		bid, ask := b.get(leg.sym)
		if bid <= 0 || ask <= 0 {
			return 0, false
		}
		if leg.buy {
			r *= (1.0 / ask) * (1 - feePerLeg)
		} else {
			r *= bid * (1 - feePerLeg)
		}
	}
	return (r - 1) * 100, true
}

// ---- 状态快照(给前端 UI 的只读接口)-------------------------------------

type triArbCycleState struct {
	Name     string  `json:"name"`
	NetPct   float64 `json:"net_pct"`
	GrossPct float64 `json:"gross_pct"`
	OK       bool    `json:"ok"`
}

type triArbOppState struct {
	Name   string    `json:"name"`
	NetPct float64   `json:"net_pct"`
	At     time.Time `json:"at"`
}

// TriArbStatus 是 /api/triarb/status 返回体。
type TriArbStatus struct {
	Connected    bool               `json:"connected"`
	UpdatedAt    time.Time          `json:"updated_at"`
	FeePerLegPct float64            `json:"fee_per_leg_pct"`
	BestNetPct   float64            `json:"best_net_pct"`
	OppCount     int64              `json:"opp_count"` // 启动以来累计 net>0 机会数
	Cycles       []triArbCycleState `json:"cycles"`    // 按净利降序
	RecentOpps   []triArbOppState   `json:"recent_opps"`
}

var (
	triArbMu   sync.RWMutex
	triArbSnap TriArbStatus
)

func triArbSetConnected(v bool) {
	triArbMu.Lock()
	triArbSnap.Connected = v
	triArbMu.Unlock()
}

func triArbAddOpp(name string, net float64) {
	triArbMu.Lock()
	triArbSnap.OppCount++
	triArbSnap.RecentOpps = append([]triArbOppState{{Name: name, NetPct: net, At: time.Now()}}, triArbSnap.RecentOpps...)
	if len(triArbSnap.RecentOpps) > 20 {
		triArbSnap.RecentOpps = triArbSnap.RecentOpps[:20]
	}
	triArbMu.Unlock()
}

func triArbUpdateCycles(book *triBook) {
	cycles := make([]triArbCycleState, 0, len(triArbTriangles))
	best := -1e18
	anyOK := false
	for _, t := range triArbTriangles {
		net, ok := book.evalNet(t, triFeePerLeg)
		gross, _ := book.evalNet(t, 0)
		cycles = append(cycles, triArbCycleState{Name: t.name, NetPct: net, GrossPct: gross, OK: ok})
		if ok {
			anyOK = true
			if net > best {
				best = net
			}
		}
	}
	sort.Slice(cycles, func(i, j int) bool { return cycles[i].NetPct > cycles[j].NetPct })
	triArbMu.Lock()
	triArbSnap.Cycles = cycles
	if anyOK {
		triArbSnap.BestNetPct = best
	}
	triArbSnap.FeePerLegPct = triFeePerLeg * 100
	triArbSnap.UpdatedAt = time.Now()
	triArbMu.Unlock()
}

// GetTriArbStatus 只读返回三角套利检测器当前状态。
func GetTriArbStatus(c *gin.Context) {
	triArbMu.RLock()
	s := triArbSnap
	triArbMu.RUnlock()
	c.JSON(http.StatusOK, s)
}

var triArbOnce sync.Once

// StartTriArbDetectorJob 启动三角套利检测器(进程内幂等)。只读现货公开行情。
func StartTriArbDetectorJob(ctx context.Context) {
	triArbOnce.Do(func() {
		go runTriArbLoop(ctx)
	})
}

// 需要订阅的 symbol 全集(去重)。
func triArbSymbols() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, t := range triArbTriangles {
		for _, leg := range t.legs {
			if _, ok := seen[leg.sym]; !ok {
				seen[leg.sym] = struct{}{}
				out = append(out, leg.sym)
			}
		}
	}
	return out
}

func runTriArbLoop(ctx context.Context) {
	book := newTriBook()
	syms := triArbSymbols()
	streams := make([]string, len(syms))
	for i, s := range syms {
		streams[i] = strings.ToLower(s) + "@bookTicker"
	}
	// 现货 combined 流。现货未迁移,仍用 stream.binance.com:9443。
	wsURL := "wss://stream.binance.com:9443/stream?streams=" + strings.Join(streams, "/")

	go runTriArbHeartbeat(ctx, book)
	go runTriArbSnapshot(ctx, book)

	backoff := time.Second
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 10 * time.Second,
		NetDialContext:   (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	}
	lastOppAt := map[string]time.Time{} // 每个三角环最多 1s 记一次,防刷屏

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		dctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		conn, _, err := dialer.DialContext(dctx, wsURL, nil)
		cancel()
		if err != nil {
			log.Printf("[TRIARB] connect failed err=%v (retry in %s)", err, backoff)
			if !sleepCtx(ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		log.Printf("[TRIARB] connected, watching %d symbols across %d cycles", len(syms), len(triArbTriangles))
		triArbSetConnected(true)
		go func() { <-ctx.Done(); _ = conn.Close() }()
		backoff = time.Second

		for {
			_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			_, msg, rerr := conn.ReadMessage()
			if rerr != nil {
				_ = conn.Close()
				triArbSetConnected(false)
				log.Printf("[TRIARB] disconnected err=%v (reconnect in %s)", rerr, backoff)
				if !sleepCtx(ctx, backoff) {
					return
				}
				if backoff < 30*time.Second {
					backoff *= 2
				}
				break
			}
			var env struct {
				Data struct {
					S  string `json:"s"`
					B  string `json:"b"` // bidPrice
					BQ string `json:"B"` // bidQty —— 必须显式声明,否则 Go 大小写回退会用 "B"(量)覆盖 "b"(价)
					A  string `json:"a"` // askPrice
					AQ string `json:"A"` // askQty —— 同上
				} `json:"data"`
			}
			if json.Unmarshal(msg, &env) != nil || env.Data.S == "" {
				continue
			}
			_ = env.Data.BQ
			_ = env.Data.AQ
			bid, _ := strconv.ParseFloat(env.Data.B, 64)
			ask, _ := strconv.ParseFloat(env.Data.A, 64)
			if bid <= 0 || ask <= 0 {
				continue
			}
			book.set(env.Data.S, bid, ask)

			// 每次更新后重算所有环,发现净利>0 就记(每环限 1s 一条)。
			now := time.Now()
			for _, t := range triArbTriangles {
				net, ok := book.evalNet(t, triFeePerLeg)
				if !ok || net <= 0 {
					continue
				}
				if last, seen := lastOppAt[t.name]; seen && now.Sub(last) < time.Second {
					continue
				}
				lastOppAt[t.name] = now
				gross, _ := book.evalNet(t, 0)
				triArbAddOpp(t.name, net)
				log.Printf("[TRIARB] OPP %s net=%+.4f%% gross=%+.4f%% (fee=%.3f%%/leg)", t.name, net, gross, triFeePerLeg*100)
			}
		}
	}
}

// runTriArbSnapshot 每 2 秒把所有环的当前净利刷进状态快照,供前端 UI 轮询。
func runTriArbSnapshot(ctx context.Context, book *triBook) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			triArbUpdateCycles(book)
		}
	}
}

// runTriArbHeartbeat 每分钟打一条"当前最优环净利",让你直观看到离盈亏平衡有多远。
func runTriArbHeartbeat(ctx context.Context, book *triBook) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		bestNet := -1e9
		bestName := ""
		bestGross := 0.0
		any := false
		for _, tri := range triArbTriangles {
			net, ok := book.evalNet(tri, triFeePerLeg)
			if !ok {
				continue
			}
			any = true
			if net > bestNet {
				bestNet = net
				bestName = tri.name
				bestGross, _ = book.evalNet(tri, 0)
			}
		}
		if !any {
			log.Printf("[TRIARB] heartbeat: 尚无完整报价")
			continue
		}
		log.Printf("[TRIARB] heartbeat best=%s net=%+.4f%% gross=%+.4f%% (>0才有套利,费=%.3f%%/腿)",
			bestName, bestNet, bestGross, triFeePerLeg*100)
	}
}
