package exchange

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// fakeIncomeServer 忠实还原 /fapi/v1/income 的两个关键行为：
//  1. 只返回 Time >= startTime 的事件（升序）；
//  2. 单次最多 1000 条。
//
// 单页调用在事件数 >1000 的窗口里因此只能拿到**最早**那批 —— 这正是 2026-09-16
// 线上实测到的失真来源（30 天口径 +3.56U vs 7 天口径 −68.57U，子集比全集亏得多）。
func fakeIncomeServer(t *testing.T, events []USDMIncomeEvent) (*httptest.Server, *int64) {
	t.Helper()
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只数 income 请求：signedRequest 首个调用会顺带打一发
		// /fapi/v1/exchangeInfo（loadRateLimitIfNeeded），那不是翻页。
		if r.URL.Path != "/fapi/v1/income" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		atomic.AddInt64(&calls, 1)
		startMs, _ := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 1000 {
			limit = 1000
		}
		page := make([]USDMIncomeEvent, 0, limit)
		for _, e := range events {
			if e.Time < startMs {
				continue
			}
			if len(page) == limit {
				break
			}
			page = append(page, e)
		}
		raw := make([]map[string]interface{}, 0, len(page))
		for _, e := range page {
			raw = append(raw, map[string]interface{}{
				"symbol":     e.Symbol,
				"incomeType": e.IncomeType,
				"income":     fmt.Sprintf("%.8f", e.Income),
				"asset":      e.Asset,
				"time":       e.Time,
				"info":       e.Info,
				"tranId":     e.TranID,
				"tradeId":    e.TradeID,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(raw)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTestBinance(t *testing.T, srv *httptest.Server) *BinanceExchange {
	t.Helper()
	b := &BinanceExchange{
		market:         "usdm",
		baseURL:        srv.URL,
		baseURLSet:     true,
		httpClient:     srv.Client(),
		credsByID:      map[uint]binanceCred{},
		acctKeyByOwner: map[uint]string{},
	}
	b.credsByID[1] = binanceCred{APIKey: "k", APISecret: "s"}
	return b
}

// 事件少于 1000 条时必须一次拉完，且 complete=true。
func TestIncomeAllSinglePage(t *testing.T) {
	const base = 1_700_000_000_000
	events := make([]USDMIncomeEvent, 0, 7)
	for i := 0; i < 7; i++ {
		events = append(events, USDMIncomeEvent{
			Symbol: "BTCUSDT", IncomeType: "REALIZED_PNL", Income: 1.5,
			Time: base + int64(i)*1000,
		})
	}
	srv, calls := fakeIncomeServer(t, events)
	b := newTestBinance(t, srv)

	got, complete, err := b.USDMIncomeHistoryAll(1,
		time.UnixMilli(base), time.UnixMilli(base+60_000), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !complete {
		t.Fatal("complete must be true when the window fits in one page")
	}
	if len(got) != 7 {
		t.Fatalf("want 7 events, got %d", len(got))
	}
	if n := atomic.LoadInt64(calls); n != 1 {
		t.Fatalf("want exactly 1 request for a sub-limit window, got %d", n)
	}
}

// 核心回归：窗口内事件 >1000 时，必须翻页把**最新**的成交也拿到。
// 单页旧实现只会返回最早的 1000 条，把最近的整段丢掉。
func TestIncomeAllPaginatesAndKeepsNewest(t *testing.T) {
	const base = 1_700_000_000_000
	const total = 1007 // 比单页上限多 7 条 —— 多出来的正是"最新"那批
	events := make([]USDMIncomeEvent, 0, total)
	for i := 0; i < total; i++ {
		events = append(events, USDMIncomeEvent{
			Symbol: "ETHUSDT", IncomeType: "REALIZED_PNL",
			Income: float64(i), Time: base + int64(i)*1000,
		})
	}
	end := time.UnixMilli(base + int64(total)*1000)
	srv, calls := fakeIncomeServer(t, events)
	b := newTestBinance(t, srv)

	got, complete, err := b.USDMIncomeHistoryAll(1, time.UnixMilli(base), end, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !complete {
		t.Fatal("complete must be true once the last partial page is reached")
	}
	if len(got) != total {
		t.Fatalf("want %d events (all of them), got %d — newest events were dropped", total, len(got))
	}
	if n := atomic.LoadInt64(calls); n != 2 {
		t.Fatalf("want 2 requests (full page + tail page), got %d", n)
	}
	// 断言"最新的那条在里面"—— 这才是修复的实质，而不只是数量对得上。
	last := got[len(got)-1]
	if last.Time != base+int64(total-1)*1000 || last.Income != float64(total-1) {
		t.Fatalf("newest event missing: got time=%d income=%v", last.Time, last.Income)
	}

	// 反证：同一个窗口走旧的单页调用，只能拿到最早的 1000 条，最新那 7 条全丢。
	// 没有这一段，本测试只证明"新代码自洽"，证明不了它挡住的是一个真实缺陷。
	baseline, err := b.USDMIncomeHistory(1, time.UnixMilli(base), end, 1000)
	if err != nil {
		t.Fatalf("baseline call failed: %v", err)
	}
	if len(baseline) != 1000 {
		t.Fatalf("single-page baseline should be capped at 1000, got %d", len(baseline))
	}
	if baseline[len(baseline)-1].Time >= last.Time {
		t.Fatal("baseline reached the newest event — this test no longer reproduces the truncation bug")
	}
}

// 翻到页数上限仍未拉完时必须如实报 complete=false，绝不能假装拉全了。
func TestIncomeAllReportsIncompleteAtPageCap(t *testing.T) {
	const base = 1_700_000_000_000
	// 每页都从 cursor 起造满 1000 条、时间只前进 1ms/条 ⇒ 50 页也只覆盖 50 秒，
	// 相对 1 小时的窗口永远拉不完。用固定切片不行：第一页翻完就真空了，那时代码
	// 返回 complete=true 是**正确**的（窗口确实耗尽）。
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fapi/v1/income" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		atomic.AddInt64(&calls, 1)
		startMs, _ := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
		raw := make([]map[string]interface{}, 0, 1000)
		for i := 0; i < 1000; i++ {
			raw = append(raw, map[string]interface{}{
				"symbol": "BTCUSDT", "incomeType": "REALIZED_PNL",
				"income": "1.0", "asset": "USDT", "time": startMs + int64(i),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(raw)
	}))
	t.Cleanup(srv.Close)
	b := newTestBinance(t, srv)

	_, complete, err := b.USDMIncomeHistoryAll(1,
		time.UnixMilli(base), time.UnixMilli(base+3_600_000), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if complete {
		t.Fatal("complete must be false when the page cap is hit before the window is exhausted")
	}
	if n := atomic.LoadInt64(&calls); n != 50 {
		t.Fatalf("want the page cap (50) of requests, got %d", n)
	}
}
