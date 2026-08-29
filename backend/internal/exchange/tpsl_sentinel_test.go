package exchange

import (
	"errors"
	"fmt"
	"testing"
)

// 2026-08-29 三条 TP/SL 告警的回归测试:①② 是竞态噪音须降级,③ 是资金安全事件须升级。
func TestIsBenignOrderMiss(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// ①: 补挂 TP/SL 时仓位已被平掉(DB 落后于交易所)
		{"无持仓", ErrNoOpenPosition, true},
		{"无持仓-包装", fmt.Errorf("place tpsl: %w", ErrNoOpenPosition), true},
		// ②: 撤单时订单已不存在(已成交或已撤)—— 币安 -2011
		{"币安-2011", errors.New(`binance api error: {"code":-2011,"msg":"Unknown order sent."}`), true},
		// ③: 止损被穿越绝不能当良性错误吞掉,否则仓位裸奔
		{"止损被穿越", ErrStopLossBreached, false},
		{"止损被穿越-包装", fmt.Errorf("place tpsl: %w", ErrStopLossBreached), false},
		// 真故障必须保持 error
		{"签名错误", errors.New(`binance api error: {"code":-1022,"msg":"Signature invalid"}`), false},
		{"网络错误", errors.New("dial tcp: i/o timeout"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := IsBenignOrderMiss(c.err); got != c.want {
			t.Errorf("%s: IsBenignOrderMiss=%v want %v (err=%v)", c.name, got, c.want, c.err)
		}
	}
}

// 止损哨兵必须能被 errors.Is 识别(监控循环靠它触发市价平仓;认不出=仓位裸奔)。
func TestStopLossSentinelIsMatchable(t *testing.T) {
	wrapped := fmt.Errorf("PlaceUSDMTPStopOrders: %w", ErrStopLossBreached)
	if !errors.Is(wrapped, ErrStopLossBreached) {
		t.Fatal("包装后的止损哨兵必须能被 errors.Is 匹配,否则平仓兜底不会触发")
	}
	if errors.Is(ErrNoOpenPosition, ErrStopLossBreached) {
		t.Fatal("两个哨兵不得互相匹配")
	}
}
