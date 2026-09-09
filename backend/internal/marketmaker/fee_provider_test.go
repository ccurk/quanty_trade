package marketmaker

import "testing"

// noteLiveFee 唯一能造成伤害的方式是刷屏:universe 扫描每 10 秒一轮、每轮几千个对,
// 少了去重就是每天几千万行日志(台账 #37 刚为日志撑爆磁盘加过轮转)。
// 所以这里锁的就是去重本身:同一个 (交易所, 费率) 只放行一次,换了值要能再放行一次。
func TestNoteLiveFeeDedupes(t *testing.T) {
	feeMu.Lock()
	loggedFee = map[string]bool{}
	feeMu.Unlock()

	cases := []struct {
		ex      string
		bps     float64
		wantLog bool
		why     string
	}{
		{"gate", 20, true, "第一次见到 20bps,必须留痕"},
		{"gate", 20, false, "同一个值再来一次,不该再打"},
		{"gate", 20.00001, false, "四位小数内的抖动算同一档,不该再打"},
		{"gate", 0, true, "促销 0 费是另一个档,该记下来"},
		{"gate", 2, true, "换档到 2bps —— 这正是 #15 在等的那个信号,漏了等于白加"},
		{"GATE", 2, false, "交易所名大小写不同不算新档"},
		{"kucoin", 2, true, "另一个交易所的同一个数是另一条"},
	}
	logged := 0
	for _, c := range cases {
		if got := noteLiveFee(c.ex, c.bps); got != c.wantLog {
			t.Fatalf("noteLiveFee(%s, %v) = %v,应为 %v —— %s", c.ex, c.bps, got, c.wantLog, c.why)
		} else if got {
			logged++
		}
	}
	// 7 次调用只该产出 4 行。少了 = 漏记换档;多了 = universe 扫描每 10 秒几千对会刷屏
	// (台账 #37 刚为日志撑爆磁盘加过轮转)。
	if logged != 4 {
		t.Fatalf("7 次调用应只打 4 行日志,实际 %d", logged)
	}
}
