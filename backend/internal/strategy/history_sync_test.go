package strategy

import (
	"errors"
	"fmt"
	"testing"
)

// TestHistoryRateLimited 锁住"限频"的两种形态。
//
// 背景：2026-09-19 11:08:49 的 IP 封禁复盘。当时 historySyncLoop 对 300 个 symbol
// 零间隔紧循环拉历史，撞上限频后【没有收手】，把剩下每一次调用都变成一条本地拒绝，
// 10 秒内刷出 877 行。修法是识别到限频就 break。
//
// 这个测试要守住的是：**只认真币安那一种是不够的**。ban 窗口武装之后，后续请求
// 根本不出网 —— binance.go 的 signedRequest/publicRequest 在 `RateLimited()` 为真时
// 直接返回合成的 `{"code":429,"msg":"Rate limited"}`。只匹配 `-1003` 的话，
// 真实的封禁风暴一次都拦不住（实测那天 878 条里只有 1 条是真 -1003）。
func TestHistoryRateLimited(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"真币安 -1003 IP 级限流",
			errors.New(`binance api error: {"code":-1003,"msg":"Too many requests; current limit of IP(1.2.3.4) is 2400 requests per minute"}`), true},
		{"本地 ban 窗口合成的 429",
			errors.New(`binance api error: {"code":429,"msg":"Rate limited"}`), true},
		{"429 但报文是别家的写法", errors.New("HTTPError 429: 'Too Many Requests'"), true},
		{"被包了一层也要认",
			fmt.Errorf("FetchCandles failed for history: %w",
				errors.New(`binance api error: {"code":-1003,"msg":"Too many requests"}`)), true},
		{"普通业务错误不能误判", errors.New(`binance api error: {"code":-2019,"msg":"Margin is insufficient."}`), false},
		{"网络错误不能误判", errors.New("dial tcp: i/o timeout"), false},
		{"symbol 不存在不能误判", errors.New("symbol not found: FOO/USDT"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := historyRateLimited(c.err); got != c.want {
				t.Fatalf("historyRateLimited = %v, 期望 %v (err=%v)", got, c.want, c.err)
			}
		})
	}
}
