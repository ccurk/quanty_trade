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

const usdTrade7DaysMs int64 = 7 * 24 * 3600 * 1000

// fakeUserTradesServer 忠实还原 /fapi/v1/userTrades 的两条硬限制：
//  1. startTime~endTime 跨度超过 7 天 → **整个请求失败**（Binance 文档原文）；
//  2. 单次最多 1000 条，按时间升序返回。
//
// 限制 1 是关键：调用方传 >7 天窗口时不是"少拿几条"，是**一条都拿不到**，
// 而 fetchBinanceContext 过去用 `continue` 把它吞了 —— 于是 pair_count=0
// 看起来和"真的没交易"完全一样（2026-09-16 实测 hours=336）。
func fakeUserTradesServer(t *testing.T, trades []USDMUserTrade) (*httptest.Server, *int64) {
	t.Helper()
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fapi/v1/userTrades" {
			// signedRequest 首个调用会顺带打一发 /fapi/v1/exchangeInfo，不是翻页。
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		atomic.AddInt64(&calls, 1)

		q := r.URL.Query()
		startMs, _ := strconv.ParseInt(q.Get("startTime"), 10, 64)
		endMs, _ := strconv.ParseInt(q.Get("endTime"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 || limit > 1000 {
			limit = 1000
		}

		if endMs > 0 && startMs > 0 && endMs-startMs > usdTrade7DaysMs {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":-1128,"msg":"More than 7 days between startTime and endTime"}`))
			return
		}

		page := make([]USDMUserTrade, 0, limit)
		for _, tr := range trades {
			if tr.Time < startMs {
				continue
			}
			if endMs > 0 && tr.Time > endMs {
				continue
			}
			if len(page) == limit {
				break
			}
			page = append(page, tr)
		}
		raw := make([]map[string]interface{}, 0, len(page))
		for _, tr := range page {
			raw = append(raw, map[string]interface{}{
				"symbol": tr.Symbol, "id": tr.ID, "orderId": tr.OrderID,
				"side": tr.Side, "positionSide": tr.PositionSide,
				"qty": fmt.Sprintf("%.8f", tr.Qty), "price": fmt.Sprintf("%.8f", tr.Price),
				"quoteQty":        fmt.Sprintf("%.8f", tr.QuoteQty),
				"realizedPnl":     fmt.Sprintf("%.8f", tr.RealizedPnL),
				"commission":      fmt.Sprintf("%.8f", tr.Commission),
				"commissionAsset": tr.CommissionAsset, "maker": tr.Maker,
				"time": tr.Time,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(raw)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func mkTrades(base int64, n int, stepMs int64) []USDMUserTrade {
	out := make([]USDMUserTrade, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, USDMUserTrade{
			Symbol: "ETHUSDT", ID: int64(i + 1), Side: "SELL",
			Qty: 1, Price: 100, RealizedPnL: float64(i), Commission: 0.01,
			Time: base + int64(i)*stepMs,
		})
	}
	return out
}

// 短窗口（<7 天）必须一次拉完，且 complete=true。
func TestUserTradesAllSingleWindow(t *testing.T) {
	const base = 1_700_000_000_000
	srv, calls := fakeUserTradesServer(t, mkTrades(base, 5, 1000))
	b := newTestBinance(t, srv)

	got, complete, err := b.USDMUserTradesAll(1, "ETHUSDT",
		time.UnixMilli(base), time.UnixMilli(base+3600_000), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !complete {
		t.Fatal("complete must be true for a sub-7-day window")
	}
	if len(got) != 5 {
		t.Fatalf("want 5 trades, got %d", len(got))
	}
	if n := atomic.LoadInt64(calls); n != 1 {
		t.Fatalf("want 1 request, got %d", n)
	}
}

// 核心回归：窗口 >7 天时必须自动分段，把交易全拉回来。
func TestUserTradesAllSplitsOverSevenDays(t *testing.T) {
	const base = 1_700_000_000_000
	// 14 天窗口，每天 1 笔 → 旧实现一条都拿不到。
	all := mkTrades(base, 14, 24*3600*1000)
	end := base + int64(14*24*3600*1000)
	srv, calls := fakeUserTradesServer(t, all)
	b := newTestBinance(t, srv)

	got, complete, err := b.USDMUserTradesAll(1, "ETHUSDT", time.UnixMilli(base), time.UnixMilli(end), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !complete {
		t.Fatal("complete must be true once every segment is exhausted")
	}
	if len(got) != len(all) {
		t.Fatalf("want %d trades across segments, got %d", len(all), len(got))
	}
	// 14 天 / 7 天 = 2 段，每段一次请求。
	if n := atomic.LoadInt64(calls); n != 2 {
		t.Fatalf("want 2 segmented requests, got %d", n)
	}

	// 反证：同一个窗口走旧的单次调用，被币安的 7 天限制直接拒绝。
	// 没有这一段，本测试只证明"新代码自洽"，证明不了它挡住的是一个真实缺陷。
	if _, err := b.USDMUserTrades(1, "ETHUSDT", time.UnixMilli(base), time.UnixMilli(end), 1000); err == nil {
		t.Fatal("single-call baseline over 14 days should be rejected by the exchange — this test no longer reproduces the bug")
	}
}

// 段内超过 1000 笔时必须翻页，且不能丢最新的。
func TestUserTradesAllPaginatesWithinSegment(t *testing.T) {
	const base = 1_700_000_000_000
	const total = 1007
	all := mkTrades(base, total, 1000)
	end := base + int64(total)*1000
	srv, calls := fakeUserTradesServer(t, all)
	b := newTestBinance(t, srv)

	got, complete, err := b.USDMUserTradesAll(1, "ETHUSDT", time.UnixMilli(base), time.UnixMilli(end), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !complete {
		t.Fatal("complete must be true once the tail page is reached")
	}
	if len(got) != total {
		t.Fatalf("want %d trades, got %d — newest were dropped", total, len(got))
	}
	if n := atomic.LoadInt64(calls); n != 2 {
		t.Fatalf("want 2 requests (full page + tail), got %d", n)
	}
	last := got[len(got)-1]
	if last.Time != base+int64(total-1)*1000 {
		t.Fatalf("newest trade missing: got time=%d", last.Time)
	}
}
