package rebalance

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// This file reconstructs the actual incident shape end-to-end: a gate balance read
// that FAILS, driven through the real signed-HTTP fetcher, the real BalanceSet, the
// real planner and the real executor — with only the withdrawal function replaced by
// a spy, so nothing can ever leave the sandbox. Both exchange hosts point at local
// httptest servers; no live exchange is contacted and no funds can move.
//
// Measured on the pre-fix code (2026-09-10, same sandbox, same fake keys):
//   gate HTTP 500 → 收集到 1 条余额(gate 读失败,静默丢弃)
//                 → plan USDT binance→gate 1000.000000 executable=true
//                   reason="USDT 库存 0.000000 < 下限 500.000000,补至目标 1000.000000"
//                 → executor executed=true amount=1000.000000
//                 → 提现函数被调用 1 次, 金额 1000.000000
// That is the phantom withdrawal. The two tests below are the fix's contract.

// fakeExchanges points the two READ-ONLY balance hosts at local servers.
// gateStatus 500 = "read failed"; 200 with an empty array = "gate really holds nothing".
func fakeExchanges(t *testing.T, gateStatus int, gateBody string) {
	t.Helper()
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(gateStatus)
		_, _ = w.Write([]byte(gateBody))
	}))
	t.Cleanup(gate.Close)
	binance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"balances":[{"asset":"USDT","free":"50000","locked":"0"}]}`))
	}))
	t.Cleanup(binance.Close)

	oldG, oldB := gateSpotHost, binanceSpotHost
	gateSpotHost, binanceSpotHost = gate.URL, binance.URL
	t.Cleanup(func() { gateSpotHost, binanceSpotHost = oldG, oldB })

	// 假凭据。真凭据一律不进测试,签名算法照跑,请求只到本地 httptest。
	t.Setenv("MM_GATE_API_KEY", "sandbox-fake-key")
	t.Setenv("MM_GATE_API_SECRET", "sandbox-fake-secret")
	t.Setenv("BINANCE_API_KEY", "sandbox-fake-key")
	t.Setenv("BINANCE_API_SECRET", "sandbox-fake-secret")
}

// runPipeline is the production sequence the auto loop and the manual endpoint both
// run: fetch → plan → execute every executable plan. Returns the blocked reason, the
// plans, and how many times / for how much the withdrawal fn was called.
func runPipeline(t *testing.T) (blocked string, plans []Plan, calls int, amount float64) {
	t.Helper()
	wl := NewWhitelist([]AllowedAddress{{Exchange: "gate", Asset: "USDT", Network: "TRC20", Address: "TGateDeposit"}})
	bs := FetchAllSpotBalances()
	plans, blocked = newTestPlanner(wl).Plan(bs)
	w, c, amt := spyWithdraw()
	ex := NewExecutor(semiCfg(), w, priceUSDT)
	for _, p := range plans {
		if !p.Executable() {
			continue
		}
		ex.Execute(p, 0, time.Time{}, time.Now())
	}
	t.Logf("bsErr=%q blocked=%q plans=%d withdrawCalls=%d amount=%.6f", bs.Err(), blocked, len(plans), *c, *amt)
	return blocked, plans, *c, *amt
}

// 现场一:gate 读余额失败。改前会提走 1000 USDT;改后必须一分不动。
func TestGateReadFailureProducesNoTransfer(t *testing.T) {
	fakeExchanges(t, http.StatusInternalServerError, `{"label":"INTERNAL","message":"boom"}`)

	blocked, plans, calls, _ := runPipeline(t)
	if blocked == "" {
		t.Fatalf("gate 读失败时必须给出 blocked 理由,却是空的")
	}
	if len(plans) != 0 {
		t.Fatalf("gate 读失败时不许产出计划,产出了 %d 个: %+v", len(plans), plans)
	}
	if calls != 0 {
		t.Fatalf("gate 读失败时提现函数必须 0 次调用,实际 %d 次", calls)
	}
}

// 现场二:gate 读成功,账户里确实没有 USDT。同样"没有那条余额行",但这次是已知的 0,
// 补仓是对的、必须照常发生 —— 否则这个修复就是把功能一起挡死了。
func TestGateGenuinelyEmptyStillRefills(t *testing.T) {
	fakeExchanges(t, http.StatusOK, `[]`)

	blocked, plans, calls, amount := runPipeline(t)
	if blocked != "" {
		t.Fatalf("读成功就不该 blocked: %s", blocked)
	}
	if len(plans) != 1 || plans[0].Amount != 1000 {
		t.Fatalf("已知为 0 时应产出补至 1000 的计划,得到 %+v", plans)
	}
	if calls != 1 || amount != 1000 {
		t.Fatalf("已知为 0 时应真的搬 1000,得到 calls=%d amount=%v", calls, amount)
	}
}
