package exchange

// 台账 #174：生产库 1,418 笔开仓单永久停在 status=new/avg_price=0/executed_qty=下单量。
// 根因链（binance.go）：
//   ① waitUSDMOrderFinal 单次查询报错直接 return，err 被调用方静默丢弃、不打日志
//   ② PlaceOrder 用 origQty（下单量）兜底 executedQty，把"已下单"冒充成"已成交"
// 本文件钉住三点修复：查询报错会重试并留痕、未确认成交不再用 origQty 冒充、
// 正常一把成交的路径行为不变。

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestUSDMExchange 起一个 httptest 服务端当 Binance USDM REST 的替身，
// 并塞好走 PlaceOrder 需要的 filters 缓存（跳过 infoOnce 里真正打网络的那次
// refreshExchangeInfo）和一把免查库的 cred。
func newTestUSDMExchange(t *testing.T, handler http.HandlerFunc) *BinanceExchange {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	b := &BinanceExchange{
		name:       "Binance",
		market:     "usdm",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		baseURLSet: true,
		credsByID:  map[uint]binanceCred{1: {APIKey: "test-key", APISecret: "test-secret"}},
	}
	// 消耗掉 infoOnce，这样 getFilters -> ensureInfoCache 不会把下面手填的
	// bySymbol 覆盖掉（它只在第一次 Do 时真正执行）。
	b.infoOnce.Do(func() {})
	// loadRateLimitIfNeeded 为了限流权重会另打一次 /fapi/v1/exchangeInfo ——
	// 那是与 infoOnce 无关的第二套缓存。不喂它，mock 服务端会收到没预期的请求
	// 而 t.Fatalf。这里直接把限流缓存标成"刚拉过"。
	b.lastRateLimitLoad = time.Now()
	b.rateLimitWeight1m = 1200
	b.info = binanceExchangeInfoCache{
		bySymbol: map[string]binanceSymbolFilters{
			"BTCUSDT": {Symbol: "BTCUSDT", StepSize: 0.001, MinQty: 0.001, MinNotional: 5},
		},
		expires: time.Now().Add(time.Hour),
	}
	return b
}

// withFastPolling 把 waitUSDMOrderFinal 的总轮数上限调小、轮询间隔调到 1ms，
// 只影响测试跑起来的耗时，不改生产默认值（30 次/200ms，测试结束自动还原）。
func withFastPolling(t *testing.T, maxAttempts int) {
	t.Helper()
	prevMax, prevInterval := usdmOrderFinalMaxAttempts, usdmOrderFinalPollInterval
	usdmOrderFinalMaxAttempts = maxAttempts
	usdmOrderFinalPollInterval = time.Millisecond
	t.Cleanup(func() {
		usdmOrderFinalMaxAttempts = prevMax
		usdmOrderFinalPollInterval = prevInterval
	})
}

// captureLog 把标准库 log 包的输出（internal/logger 的 Warnf/Errorf 底层走的
// 就是它）接到 buffer 里，测试结束还原，用来断言"确实打了日志"。
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return &buf
}

const tickerPriceOKBody = `{"price":"30000.00"}`

// ① 查询报错：前两次 GET /fapi/v1/order 报 500，第三次才返回 FILLED。
// waitUSDMOrderFinal 必须在剩余轮询预算内重试并最终拿到真实成交，而不是第一次
// 报错就放弃；每次失败都要落一条能看出 symbol/client_order_id/order_id/错误
// 原文的日志。
func TestWaitUSDMOrderFinalRetriesTransientErrors(t *testing.T) {
	var calls int32
	b := newTestUSDMExchange(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/fapi/v1/order" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":-1021,"msg":"Timestamp for this request is outside of the recvWindow."}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"FILLED","origQty":"1.500","executedQty":"1.500","avgPrice":"30000.5","updateTime":1700000000000}`))
	})
	withFastPolling(t, 10)
	logBuf := captureLog(t)

	cred := binanceCred{APIKey: "k", APISecret: "s"}
	refreshed, err := b.waitUSDMOrderFinal(cred, "BTCUSDT", "cid-retry-1", "999001")
	if err != nil {
		t.Fatalf("expected eventual success, got err=%v", err)
	}
	if refreshed == nil || strings.ToLower(refreshed.Status) != "filled" {
		t.Fatalf("expected FILLED after retries, got %+v", refreshed)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected exactly 3 requests (2 failed + 1 success), got %d", got)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "cid-retry-1") || !strings.Contains(logged, "999001") {
		t.Fatalf("expected retry log to name client_order_id/order_id, got: %s", logged)
	}
	if !strings.Contains(logged, "Timestamp for this request") {
		t.Fatalf("expected retry log to include the raw Binance error text, got: %s", logged)
	}
	if strings.Count(logged, "[WARN]") != 2 {
		t.Fatalf("expected exactly 2 WARN log lines (one per failed attempt), got: %s", logged)
	}
}

// ① 查询报错的另一半：轮询预算内全部失败时，必须返回 error（而不是悄悄放弃
// 返回一个看起来正常的空结果），并且落一条 ERROR 级日志。
func TestWaitUSDMOrderFinalAllErrorsReturnErrorAndLogs(t *testing.T) {
	b := newTestUSDMExchange(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":-1021,"msg":"boom"}`))
	})
	withFastPolling(t, 3)
	logBuf := captureLog(t)

	cred := binanceCred{APIKey: "k", APISecret: "s"}
	refreshed, err := b.waitUSDMOrderFinal(cred, "BTCUSDT", "cid-allfail", "999002")
	if err == nil {
		t.Fatalf("expected error when every poll attempt fails, got refreshed=%+v", refreshed)
	}
	if refreshed != nil {
		t.Fatalf("expected nil result on total failure, got %+v", refreshed)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "[ERROR]") {
		t.Fatalf("expected an ERROR-level log line on total failure, got: %s", logged)
	}
	if !strings.Contains(logged, "cid-allfail") || !strings.Contains(logged, "999002") {
		t.Fatalf("expected ERROR log to name client_order_id/order_id, got: %s", logged)
	}
}

// ② 未成交：初始下单响应和每次轮询都停在 NEW（从未到终态），executed_qty
// 不能被 origQty（下单量）冒充——这正是台账 #174 的故障现象。返回的 Order
// 必须诚实地是 status != filled 且 Amount == 0，让上层能判定"这单没成交"。
func TestPlaceOrderUSDMUnfilledDoesNotFakeExecutedQty(t *testing.T) {
	var pollCalls int32
	b := newTestUSDMExchange(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/fapi/v1/ticker/price":
			_, _ = w.Write([]byte(tickerPriceOKBody))
		case r.Method == http.MethodPost && r.URL.Path == "/fapi/v1/order":
			_, _ = w.Write([]byte(`{"orderId":42001,"clientOrderId":"cid-unfilled","symbol":"BTCUSDT","side":"BUY","price":"0","origQty":"1.500","executedQty":"0","avgPrice":"0","status":"NEW","updateTime":1700000001000}`))
		case r.Method == http.MethodGet && r.URL.Path == "/fapi/v1/order":
			atomic.AddInt32(&pollCalls, 1)
			_, _ = w.Write([]byte(`{"status":"NEW","origQty":"1.500","executedQty":"0","avgPrice":"0","updateTime":1700000001000}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	withFastPolling(t, 2)

	order, err := b.PlaceOrder(1, "cid-unfilled-req", "BTCUSDT", "buy", 1.5, 0)
	if err != nil {
		t.Fatalf("PlaceOrder returned error: %v", err)
	}
	if order == nil {
		t.Fatalf("expected a non-nil order even when unfilled")
	}
	if strings.ToLower(order.Status) == "filled" {
		t.Fatalf("status must not read as filled when the order never confirmed a fill, got %q", order.Status)
	}
	// 这是本次修复钉住的那一行：修复前 Amount 会等于请求量 1.5（origQty 冒充），
	// 修复后必须是 0（真实 executedQty），不能相等。
	if order.Amount == 1.5 {
		t.Fatalf("Amount must NOT be faked with the requested qty (1.5) when unfilled, got %v", order.Amount)
	}
	if order.Amount != 0 {
		t.Fatalf("expected Amount == 0 (true executedQty) when unfilled, got %v", order.Amount)
	}
	if order.Price != 0 {
		t.Fatalf("expected Price == 0 when avgPrice never confirmed positive, got %v", order.Price)
	}
	if atomic.LoadInt32(&pollCalls) == 0 {
		t.Fatalf("expected PlaceOrder to have polled waitUSDMOrderFinal at least once")
	}
}

// ③ 正常一把成交：初始下单响应已经是 FILLED/avgPrice>0/executedQty>0，走的是
// 快路径，不应触发任何轮询，且返回值必须是响应里的真实成交价/量——修复前后
// 这条路径的行为不能变。
func TestPlaceOrderUSDMFilledImmediatelyUnchanged(t *testing.T) {
	pollCalled := false
	b := newTestUSDMExchange(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/fapi/v1/ticker/price":
			_, _ = w.Write([]byte(tickerPriceOKBody))
		case r.Method == http.MethodPost && r.URL.Path == "/fapi/v1/order":
			_, _ = w.Write([]byte(`{"orderId":42002,"clientOrderId":"cid-filled","symbol":"BTCUSDT","side":"BUY","price":"0","origQty":"2.000","executedQty":"2.000","avgPrice":"100.5","status":"FILLED","updateTime":1700000002000}`))
		case r.Method == http.MethodGet && r.URL.Path == "/fapi/v1/order":
			pollCalled = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	withFastPolling(t, 2)

	order, err := b.PlaceOrder(1, "cid-filled-req", "BTCUSDT", "buy", 2, 0)
	if err != nil {
		t.Fatalf("PlaceOrder returned error: %v", err)
	}
	if strings.ToLower(order.Status) != "filled" {
		t.Fatalf("expected status filled, got %q", order.Status)
	}
	if order.Amount != 2 {
		t.Fatalf("expected Amount == 2 (real executedQty), got %v", order.Amount)
	}
	if order.Price != 100.5 {
		t.Fatalf("expected Price == 100.5 (real avgPrice), got %v", order.Price)
	}
	if pollCalled {
		t.Fatalf("an already-filled order must not trigger waitUSDMOrderFinal polling")
	}
}
