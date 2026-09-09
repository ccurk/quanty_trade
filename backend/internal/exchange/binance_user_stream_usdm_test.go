package exchange

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
	"quanty_trade/internal/ws"

	"github.com/gorilla/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// exchange_order_events 建表于 2026-03-26,至今 auto_increment=1 —— 一行都没插进去过。
// 不是"最近坏了",是从来没接上:conf_pro.yaml:45 market="usdm",而
// EnsureUserDataStream 对 usdm 直接 return nil,goroutine 根本没起。
//
// 而且就算把那道 return 去掉也连不上:listenKey 走的是现货的
// /api/v3/userDataStream(合约是 /fapi/v1/listenKey),事件分支只认现货的
// executionReport(合约发的是 ORDER_TRADE_UPDATE)。下面两个用例分别钉住这两层。

func newStreamTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.ExchangeOrderEvent{}, &models.StrategyOrder{}, &models.StrategyPosition{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prev })
	return db
}

// 一个真实的 USDM ORDER_TRADE_UPDATE 帧。内层 o 对象取自 Binance 开发者社区里
// 贴出的原始回报(APEUSDT 平仓,rp=0.132),只补了 "c"(clientOrderId)用来走
// 平台订单关联 —— 原帖把它截掉了。
//
// 注意 rp 是**每笔成交**的已实现盈亏,不是整单累计:同一 orderId 的多笔部分成交
// 要各自累加才是整单 PnL。所以 rp 落在逐事件的 exchange_order_events 行上,
// 而不是拿去当持仓的 realized_pn_l。
const usdmOrderTradeUpdateFrame = `{
  "e":"ORDER_TRADE_UPDATE","E":1662895612061,"T":1662895612061,
  "o":{"s":"APEUSDT","c":"qt-test-client-1","S":"SELL","o":"LIMIT","x":"TRADE","X":"FILLED",
       "q":"36","p":"5.5600","ap":"5.5600","l":"6","z":"36","L":"5.5600",
       "n":"0.00667200","N":"USDT","T":1662895612061,"t":268356372,
       "b":"0","a":"0","m":true,"R":true,"ps":"BOTH","rp":"0.13200000",
       "wt":"CONTRACT_PRICE","ot":"LIMIT","cp":false,"pP":false,"si":0,"ss":0,"sp":"0"}
}`

// 用一个进程内的 websocket 服务端喂一帧,再让 readUserStream 按它自己的分支跑。
// 不连外网、不用实盘 key。服务端发完就关,ReadMessage 报错让 readUserStream 返回,
// 所以 DB 写入在函数返回时已经完成,不需要 sleep。
func serveOneFrame(t *testing.T, payload string) *websocket.Conn {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = c.WriteMessage(websocket.TextMessage, []byte(payload))
		_ = c.Close()
	}))
	t.Cleanup(srv.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial test ws: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// ① 这条流对 usdm 根本没起过。EnsureUserDataStream 成功启动的可观测证据是
// streamsByID 里出现了这个 owner 的条目。
func TestEnsureUserDataStreamStartsForUSDM(t *testing.T) {
	b := &BinanceExchange{
		name:        "Binance",
		market:      "usdm",
		baseURL:     "https://fapi.binance.com",
		wsBaseURL:   "wss://127.0.0.1:0", // 拨号必失败,后台 goroutine 只退避
		httpClient:  &http.Client{Timeout: time.Second},
		streamsByID: make(map[uint]*binanceUserStream),
		credsByID:   make(map[uint]binanceCred),
	}

	if err := b.EnsureUserDataStream(7, ws.NewHub()); err != nil {
		t.Fatalf("EnsureUserDataStream: %v", err)
	}

	b.streamMu.Lock()
	s := b.streamsByID[7]
	b.streamMu.Unlock()
	if s == nil {
		t.Fatal("usdm 下 user data stream 没有启动:streamsByID 里没有 owner 7 的条目 " +
			"(binance_user_stream.go 的 market==\"usdm\" 提前 return)")
	}
	close(s.stop) // 让退避中的 goroutine 收敛,别泄漏到别的用例
}

// ② 即使连上了,合约的 ORDER_TRADE_UPDATE 也没有被识别:分支只认 executionReport,
// 其余一律落到 default 分支只做前端广播,一行都不落库。
func TestUSDMOrderTradeUpdatePersistsExchangeOrderEvent(t *testing.T) {
	db := newStreamTestDB(t)

	b := &BinanceExchange{
		name:       "Binance",
		market:     "usdm",
		httpClient: &http.Client{Timeout: time.Second},
	}
	s := &binanceUserStream{ownerID: 7, hub: ws.NewHub(), stop: make(chan struct{}), done: make(chan struct{})}

	b.readUserStream(serveOneFrame(t, usdmOrderTradeUpdateFrame), s)

	var rows []models.ExchangeOrderEvent
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ORDER_TRADE_UPDATE 没有落库:exchange_order_events 有 %d 行,want 1", len(rows))
	}
	got := rows[0]
	if got.Symbol != "APEUSDT" {
		t.Errorf("symbol = %q, want \"APEUSDT\"", got.Symbol)
	}
	if got.ClientOrderID != "qt-test-client-1" {
		t.Errorf("client_order_id = %q, want \"qt-test-client-1\"", got.ClientOrderID)
	}
	if got.Side != "sell" {
		t.Errorf("side = %q, want \"sell\"", got.Side)
	}
	if got.Status != "filled" {
		t.Errorf("status = %q, want \"filled\"", got.Status)
	}
	if got.ExecutedQty != 36 {
		t.Errorf("executed_qty = %v, want 36 (z)", got.ExecutedQty)
	}
	if got.LastQty != 6 {
		t.Errorf("last_qty = %v, want 6 (l)", got.LastQty)
	}
	if got.LastPrice != 5.56 {
		t.Errorf("last_price = %v, want 5.56 (L)", got.LastPrice)
	}
	if !strings.Contains(got.Raw, "\"rp\"") {
		t.Errorf("raw 没有保留原始 payload(找不到 rp 字段): %s", got.Raw)
	}
	if got.RealizedProfit != 0.132 {
		t.Errorf("realized_profit = %v, want 0.132 (rp) —— 这是本轮修的重点:"+
			"平仓的已实现盈亏交易所直接给了,过去一个字段都没解析", got.RealizedProfit)
	}
}

// ③ 平仓成交的 direction 不许在这一层重新猜。
// 台账 #91:id=1897/1966 是买入平空,被按 side=buy 猜成 direction=long、
// realized_pn_l=0 的假仓。现在 WS 只负责把 side + 平台订单上的 purpose 交出去,
// 方向由持仓账本自己定;找不到持仓时账本拒绝建仓,而不是造一个多头。
func TestUSDMCloseFillIsRoutedToLedgerWithoutGuessingDirection(t *testing.T) {
	db := newStreamTestDB(t)
	if err := db.Create(&models.StrategyOrder{
		ClientOrderID: "qt-test-client-1",
		StrategyID:    "qt-breakout-follow",
		StrategyName:  "qt-breakout-follow",
		Symbol:        "APE/USDT",
		Purpose:       "close",
	}).Error; err != nil {
		t.Fatalf("seed strategy order: %v", err)
	}

	// baseURL 故意留空:displaySymbol 遇到无斜杠的 symbol 会走 getFilters →
	// refreshExchangeInfo,空 baseURL 让它立刻报 "unsupported protocol scheme"
	// 而不是发真请求,于是原样退回 "APEUSDT"。所以本用例不断言 f.Symbol ——
	// 它测的是路由和字段映射,不是 symbol 归一化。
	b := &BinanceExchange{
		name:       "Binance",
		market:     "usdm",
		httpClient: &http.Client{Timeout: time.Second},
	}
	var got []OrderFill
	b.SetOrderFillHandler(func(f OrderFill) uint {
		got = append(got, f)
		return 4242 // 假装账本把它记到了 4242 号持仓上
	})
	s := &binanceUserStream{ownerID: 7, hub: ws.NewHub(), stop: make(chan struct{}), done: make(chan struct{})}

	b.readUserStream(serveOneFrame(t, usdmOrderTradeUpdateFrame), s)

	if len(got) != 1 {
		t.Fatalf("成交没有交给账本:回调收到 %d 次,want 1", len(got))
	}
	f := got[0]
	if f.Purpose != "close" {
		t.Errorf("purpose = %q, want \"close\"(来自平台订单 StrategyOrder.Purpose,不是猜的)", f.Purpose)
	}
	if f.Side != "sell" {
		t.Errorf("side = %q, want \"sell\"", f.Side)
	}
	// 用累计量 z 和交易所自己的均价 ap,不是单笔的 l/L —— 部分成交时只在
	// 终态 FILLED 入账一次,拿单笔量会少记。
	if f.ExecutedQty != 36 {
		t.Errorf("executed_qty = %v, want 36 (z 累计量,不是 l=6)", f.ExecutedQty)
	}
	if f.AvgPrice != 5.56 {
		t.Errorf("avg_price = %v, want 5.56 (ap)", f.AvgPrice)
	}

	var ev models.ExchangeOrderEvent
	if err := db.First(&ev).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if ev.PositionID != 4242 {
		t.Errorf("position_id = %d, want 4242 —— 逐仓归属要在成交当时挂上", ev.PositionID)
	}
}

// 部分成交不入账:z/ap 是累计值,每个 PARTIALLY_FILLED 都记一次会把同一批量
// 重复计入持仓。
func TestUSDMPartialFillIsPersistedButNotAppliedToLedger(t *testing.T) {
	db := newStreamTestDB(t)
	if err := db.Create(&models.StrategyOrder{
		ClientOrderID: "qt-test-client-1",
		StrategyID:    "qt-breakout-follow",
		Purpose:       "close",
	}).Error; err != nil {
		t.Fatalf("seed strategy order: %v", err)
	}

	b := &BinanceExchange{name: "Binance", market: "usdm", httpClient: &http.Client{Timeout: time.Second}}
	applied := 0
	b.SetOrderFillHandler(func(OrderFill) uint { applied++; return 1 })
	s := &binanceUserStream{ownerID: 7, hub: ws.NewHub(), stop: make(chan struct{}), done: make(chan struct{})}

	partial := strings.Replace(usdmOrderTradeUpdateFrame, `"X":"FILLED"`, `"X":"PARTIALLY_FILLED"`, 1)
	b.readUserStream(serveOneFrame(t, partial), s)

	if applied != 0 {
		t.Errorf("PARTIALLY_FILLED 被入账了 %d 次,want 0(z/ap 是累计值,只在 FILLED 记一次)", applied)
	}
	var n int64
	if err := db.Model(&models.ExchangeOrderEvent{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("部分成交的事件行 = %d, want 1(审计行要留,rp 靠逐行累加才凑得出整单)", n)
	}
}

// 共享账户下的归属:所有 owner 指着同一个 Binance 账户,而合约的
// POST /fapi/v1/listenKey 对同一把 API key 返回同一个 listenKey —— 于是每个
// owner 的流都会收到所有人的成交。入账必须认"下单的那个 owner"
// (StrategyOrder.OwnerID),不能认"收到这条消息的那个流"(s.ownerID),
// 否则 owner A 的成交会被塞进 owner B 的账本,找不到持仓后当孤儿平仓丢掉。
func TestFillIsAttributedToOrderOwnerNotStreamOwner(t *testing.T) {
	db := newStreamTestDB(t)
	if err := db.Create(&models.StrategyOrder{
		ClientOrderID: "qt-test-client-1",
		StrategyID:    "qt-breakout-follow",
		OwnerID:       2, // 下单的是 owner 2
		Purpose:       "close",
	}).Error; err != nil {
		t.Fatalf("seed strategy order: %v", err)
	}

	b := &BinanceExchange{name: "Binance", market: "usdm", httpClient: &http.Client{Timeout: time.Second}}
	var got []OrderFill
	b.SetOrderFillHandler(func(f OrderFill) uint { got = append(got, f); return 1 })
	// 这条流属于 owner 1,但收到的是 owner 2 的成交
	s := &binanceUserStream{ownerID: 1, hub: ws.NewHub(), stop: make(chan struct{}), done: make(chan struct{})}

	b.readUserStream(serveOneFrame(t, usdmOrderTradeUpdateFrame), s)

	if len(got) != 1 {
		t.Fatalf("回调收到 %d 次,want 1", len(got))
	}
	if got[0].OwnerID != 2 {
		t.Errorf("owner_id = %d, want 2(下单人 StrategyOrder.OwnerID,不是收流的 owner 1)", got[0].OwnerID)
	}
}

// listenKey 端点必须按 market 走:合约是 /fapi/v1/listenKey,且 keepalive/close
// 不带 listenKey 查询参数(合约按 API key 认流)。拿现货那套打 fapi.binance.com
// 只会 404,连都连不上。
func TestListenKeyEndpointIsMarketAware(t *testing.T) {
	usdm := &BinanceExchange{market: "usdm", baseURL: "https://fapi.binance.com"}
	if got := usdm.listenKeyURL(binanceCred{}); got != "https://fapi.binance.com/fapi/v1/listenKey" {
		t.Errorf("usdm listenKey URL = %q, want .../fapi/v1/listenKey", got)
	}
	if got := usdm.listenKeyQuery("KEY123"); got != "" {
		t.Errorf("usdm listenKey query = %q, want \"\"(合约按 API key 认流)", got)
	}

	spot := &BinanceExchange{market: "spot", baseURL: "https://api.binance.com"}
	if got := spot.listenKeyURL(binanceCred{}); got != "https://api.binance.com/api/v3/userDataStream" {
		t.Errorf("spot listenKey URL = %q, want .../api/v3/userDataStream", got)
	}
	if got := spot.listenKeyQuery("KEY123"); got != "?listenKey=KEY123" {
		t.Errorf("spot listenKey query = %q, want \"?listenKey=KEY123\"", got)
	}
}
