package exchange

// 2026-09-22 活体定因（新加的日志第一次出声就抓到原文）：
//   主路把 STOP_MARKET / TAKE_PROFIT_MARKET 发去 /fapi/v1/order，币安整类迁移到
//   algo 接口后必返回 -4120 ⇒ **42/42 条条件腿 100% 走 fallback**，主路是注定失败的
//   一次往返（每仓 2 次多余请求，而 24h 里已有 429×44）。
//   而旧 fallback 发的是 STOP / TAKE_PROFIT + price=trig×(1∓0.003) + quantity 的
//   【带价限价单】—— 它是 fill-or-naked：价格跳过去而没成交时，腿挂在半路、
//   仓位继续裸奔。止损的全部意义是【保证出场】，出场不确定的止损严格劣于有滑点的止损。
//
// 本文件钉住三点：
//   ① fallback 首选 = closePosition=true 的市价全平腿，且不再带 price/quantity/reduceOnly
//      （官方参数表：closePosition 与 quantity、reduceOnly 互斥）；
//   ② 止盈腿用 TAKE_PROFIT_MARKET、止损腿用 STOP_MARKET —— 类型映射两处同源；
//   ③ closePosition 被拒时必须有日志地退回带价限价单，绝不静默降级。

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// algoCall 记录一次 /fapi/v1/algoOrder 的请求参数（参数走 query string，不是 body）。
type algoCall struct {
	Q url.Values
}

// newTPStopHarness 起替身服务端，覆盖 PlaceUSDMTPStopOrders 需要的三个端点。
// algoRespond 决定 algoOrder 怎么答（用来分别测「成功」与「被拒降级」两条路）。
func newTPStopHarness(t *testing.T, algoRespond func(q url.Values, w http.ResponseWriter)) (*BinanceExchange, *[]algoCall) {
	t.Helper()

	var mu sync.Mutex
	var calls []algoCall

	b := newTestUSDMExchange(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/fapi/v1/positionSide/dual"):
			_, _ = io.WriteString(w, `{"dualSidePosition":false}`)
		case strings.HasSuffix(r.URL.Path, "/fapi/v2/positionRisk"):
			_, _ = io.WriteString(w, `[{"symbol":"BTCUSDT","positionAmt":"0.5","entryPrice":"100","markPrice":"100"}]`)
		case strings.HasSuffix(r.URL.Path, "/fapi/v1/algoOrder"):
			q := r.URL.Query()
			mu.Lock()
			calls = append(calls, algoCall{Q: q})
			mu.Unlock()
			algoRespond(q, w)
		case strings.HasSuffix(r.URL.Path, "/fapi/v1/order"):
			// 主路必被拒：币安已把 USDM 条件单整类迁到 algo 接口。
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":-4120,"msg":"Order type not supported for this endpoint. Please use the Algo Order API endpoints instead."}`)
		default:
			t.Errorf("未预期的请求: %s", r.URL.Path)
		}
	})

	// 复用替身的手填 filters，但补上 TickSize（原 harness 只填了 qty 侧）。
	b.info = binanceExchangeInfoCache{
		bySymbol: map[string]binanceSymbolFilters{
			"BTCUSDT": {Symbol: "BTCUSDT", TickSize: 0.001, StepSize: 0.001, MinQty: 0.001, MinNotional: 5},
		},
		expires: b.info.expires,
	}
	return b, &calls
}

const algoOK = `{"algoId":4000001917999999,"clientAlgoId":"c","symbol":"BTCUSDT","side":"SELL","orderType":"STOP_MARKET","triggerPrice":"97.000","price":""}`

// ①+② closePosition 首选：止盈/止损两腿都必须是 *_MARKET + closePosition，
// 且绝不能夹带 quantity / reduceOnly / price。
func TestAlgoFallbackPrefersClosePosition(t *testing.T) {
	b, calls := newTPStopHarness(t, func(_ url.Values, w http.ResponseWriter) {
		_, _ = io.WriteString(w, algoOK)
	})

	created, err := b.PlaceUSDMTPStopOrders(1, "base", "BTC/USDT", 105, 97)
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("应挂上 2 条腿，实际 %d 条", len(created))
	}
	if len(*calls) != 2 {
		t.Fatalf("应打 2 次 algoOrder，实际 %d 次", len(*calls))
	}

	wantType := []string{"TAKE_PROFIT_MARKET", "STOP_MARKET"}
	// formatByStep 会 trimZeros：整数价不带小数点后的 0。
	wantTrig := []string{"105", "97"}
	for i, c := range *calls {
		if got := c.Q.Get("type"); got != wantType[i] {
			t.Errorf("第 %d 条腿 type=%q，应为 %q", i+1, got, wantType[i])
		}
		if got := c.Q.Get("orderType"); got != wantType[i] {
			t.Errorf("第 %d 条腿 orderType=%q，应与 type 同源 %q", i+1, got, wantType[i])
		}
		if got := c.Q.Get("closePosition"); got != "true" {
			t.Errorf("第 %d 条腿 closePosition=%q，应为 \"true\"", i+1, got)
		}
		if got := c.Q.Get("triggerPrice"); got != wantTrig[i] {
			t.Errorf("第 %d 条腿 triggerPrice=%q，应为 %q", i+1, got, wantTrig[i])
		}
		if got := c.Q.Get("algoType"); got != "CONDITIONAL" {
			t.Errorf("第 %d 条腿 algoType=%q，应为 CONDITIONAL", i+1, got)
		}
		// 互斥项：带价限价单的三件套一个都不能出现，否则交易所会拒。
		for _, bad := range []string{"price", "quantity", "reduceOnly"} {
			if v := c.Q.Get(bad); v != "" {
				t.Errorf("第 %d 条腿带了 %s=%q —— closePosition 与它互斥", i+1, bad, v)
			}
		}
	}
}

// ③ closePosition 被拒时必须退回带价限价单（保住「至少有一条腿」），且不能静默。
func TestAlgoFallbackDegradesToBandedLimit(t *testing.T) {
	b, calls := newTPStopHarness(t, func(q url.Values, w http.ResponseWriter) {
		if q.Get("closePosition") == "true" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":-1102,"msg":"Mandatory parameter 'quantity' was not sent"}`)
			return
		}
		_, _ = io.WriteString(w, algoOK)
	})

	created, err := b.PlaceUSDMTPStopOrders(1, "base", "BTC/USDT", 105, 97)
	if err != nil {
		t.Fatalf("降级成功时不该报错: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("降级后应仍挂上 2 条腿，实际 %d 条", len(created))
	}
	// 每腿 2 次：先 closePosition 被拒，再带价限价。
	if len(*calls) != 4 {
		t.Fatalf("每腿应有 2 次尝试，共 4 次，实际 %d 次", len(*calls))
	}

	for _, i := range []int{1, 3} { // 第 2、4 次 = 降级后的带价限价单
		q := (*calls)[i].Q
		if q.Get("closePosition") != "" {
			t.Errorf("降级单不该带 closePosition")
		}
		if got := q.Get("quantity"); got != "0.5" {
			t.Errorf("降级单必须定量 quantity，实际 %q", got)
		}
		if got := q.Get("reduceOnly"); got != "true" {
			t.Errorf("降级单应带 reduceOnly=true，实际 %q", got)
		}
		if q.Get("price") == "" {
			t.Errorf("降级单必须有价带 price")
		}
	}

	// 带子公式仍必须是 trig×(1∓0.003)/(1∓0.001)，符号随平仓方向。
	if got := (*calls)[1].Q.Get("price"); got != "104.895" {
		t.Errorf("止盈带价应为 105×0.999=104.895，实际 %q", got)
	}
	if got := (*calls)[3].Q.Get("price"); got != "96.709" {
		t.Errorf("止损带价应为 97×0.997=96.709，实际 %q", got)
	}
}
