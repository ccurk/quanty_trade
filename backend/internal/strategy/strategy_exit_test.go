package strategy

import "testing"

func TestComputeBreakevenSL(t *testing.T) {
	const fb = breakevenFeeBuffer
	cases := []struct {
		name, side                   string
		entry, cur, curSL, atr, trig float64
		wantMove                     bool
	}{
		{"long 盈利够→推保本", "buy", 100, 105, 96, 2, 1.0, true},
		{"long 盈利不够→不动", "buy", 100, 101, 96, 2, 1.0, false},
		{"long 已在保本上方→不动", "buy", 100, 105, 100.5, 2, 1.0, false},
		{"long 关闭(trig=0)", "buy", 100, 110, 96, 2, 0, false},
		{"short 盈利够→推保本", "sell", 100, 95, 104, 2, 1.0, true},
		{"short 盈利不够→不动", "sell", 100, 99, 104, 2, 1.0, false},
		{"atr=0→不动", "buy", 100, 105, 96, 0, 1.0, false},
	}
	for _, c := range cases {
		sl, move := computeBreakevenSL(c.side, c.entry, c.cur, c.curSL, c.atr, c.trig, fb)
		if move != c.wantMove {
			t.Errorf("%s: move=%v want=%v (sl=%.4f)", c.name, move, c.wantMove, sl)
			continue
		}
		if move {
			if c.side == "buy" && !(sl > c.entry*0.999 && sl > c.curSL && sl < c.cur) {
				t.Errorf("%s: long newSL=%.4f unreasonable", c.name, sl)
			}
			if c.side == "sell" && !(sl < c.entry*1.001 && sl < c.curSL && sl > c.cur) {
				t.Errorf("%s: short newSL=%.4f unreasonable", c.name, sl)
			}
		}
	}
}

func TestComputeTrailSL(t *testing.T) {
	cases := []struct {
		name, side                      string
		entry, cur, curSL, atr, act, cb float64
		wantMove                        bool
	}{
		{"long 已激活+创新高→抬止损", "buy", 100, 110, 100.1, 2, 2.0, 1.5, true},
		{"long 未激活(盈利不够)→不动", "buy", 100, 103, 96, 2, 2.0, 1.5, false},
		{"long 激活但回撤(候选低于现SL)→不动", "buy", 100, 110, 109, 2, 2.0, 1.5, false},
		{"long callback=0→关闭", "buy", 100, 110, 100, 2, 2.0, 0, false},
		{"short 已激活→压低止损", "sell", 100, 90, 99.9, 2, 2.0, 1.5, true},
		{"short 未激活→不动", "sell", 100, 97, 104, 2, 2.0, 1.5, false},
	}
	for _, c := range cases {
		sl, mv := computeTrailSL(c.side, c.entry, c.cur, c.curSL, c.atr, c.act, c.cb)
		if mv != c.wantMove {
			t.Errorf("%s: move=%v want=%v sl=%.4f", c.name, mv, c.wantMove, sl)
			continue
		}
		if mv {
			if c.side == "buy" && !(sl > c.curSL && sl < c.cur) {
				t.Errorf("%s long sl=%.4f unreasonable", c.name, sl)
			}
			if c.side == "sell" && !(sl < c.curSL && sl > c.cur) {
				t.Errorf("%s short sl=%.4f unreasonable", c.name, sl)
			}
		}
	}
}
