package marketmaker

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// 一组手算得出的样本:所有断言里的期望值都能用笔算出来,不是拿实现跑一遍再抄回来。
//
//	gate  永续: bid 100.00 (2000 张)  ask 100.10 (1000 张)  quanto_multiplier 0.01
//	binance 永续: bid  99.90 (5 个)    ask 100.00 (4 个)
//	execMid = 100.05   refMid = 99.95
func fixtureGate() gateFuturesTicker {
	return gateFuturesTicker{
		Contract: "AAA_USDT", HighestBid: "100.00", HighestSize: "2000",
		LowestAsk: "100.10", LowestSize: "1000", QuantoMultiplier: "0.01",
		IndexPrice: "100.02", FundingRate: "0.000025", Volume24hQuote: "20000000",
	}
}

func fixtureRef() map[string]BookTicker {
	return map[string]BookTicker{
		"AAAUSDT": {Symbol: "AAAUSDT", BidPx: 99.90, BidQty: 5, AskPx: 100.00, AskQty: 4},
	}
}

func TestFuturesScanRowMath(t *testing.T) {
	rows := buildFuturesScanRows([]gateFuturesTicker{fixtureGate()}, fixtureRef(), time.Unix(0, 0).UTC())
	if len(rows) != 1 {
		t.Fatalf("应得 1 行,实得 %d", len(rows))
	}
	r := rows[0]

	if r.Exchange != "gate-futures" || r.Feed != "binance-futures" || r.RefSymbol != "AAAUSDT" {
		t.Fatalf("身份字段不对: %+v", r)
	}
	approx(t, r.ExecMid, 100.05, 1e-9, "exec 中价")
	approx(t, r.RefMid, 99.95, 1e-9, "ref 中价")
	// 基差 = (100.05−99.95)/99.95×1e4 = 10.0050025 bps,有符号(gate 贵 → 正)
	approx(t, r.BasisBps, 0.10/99.95*10000, 1e-9, "基差(有符号)")
	approx(t, r.ExecSpreadBps, 0.10/100.05*10000, 1e-9, "gate 自身价差")
	approx(t, r.RefSpreadBps, 0.10/99.95*10000, 1e-9, "binance 自身价差")

	// 张 → 基础币 → 美元:2000 张 × 0.01 × 100.00 = $2000
	approx(t, r.ExecBidUSD, 2000, 1e-9, "gate 买一美元深度")
	approx(t, r.ExecAskUSD, 1001, 1e-9, "gate 卖一美元深度") // 1000×0.01×100.10
	approx(t, r.RefBidUSD, 499.5, 1e-9, "binance 买一美元深度")
	approx(t, r.RefAskUSD, 400, 1e-9, "binance 卖一美元深度")
	approx(t, r.QuantoMultiplier, 0.01, 1e-12, "换算因子原样落盘")

	// 两个跨所方向(毛值):
	// 在 gate 买(100.10)、binance 卖(99.90) → (99.90−100.10)/99.95×1e4 = −20.010005
	// 在 binance 买(100.00)、gate 卖(100.00) → 0
	approx(t, r.XBuyExecBps, -0.20/99.95*10000, 1e-9, "买gate卖binance 毛价差")
	approx(t, r.XSellExecBps, 0, 1e-9, "买binance卖gate 毛价差")

	approx(t, r.FundingRateBps, 0.25, 1e-9, "gate 资金费 0.000025 → 0.25bps")
	approx(t, r.QuoteVol24h, 20000000, 1e-9, "24h 成交额")
	if r.Suspect != "" {
		t.Fatalf("这组样本既不宽价差也不偏离大,不该打标,实得 %q", r.Suspect)
	}
}

// 两个方向的毛价差之和恒等于「两个所各自绝对价差之和」的负值 —— 也就是说
// 它们不可能同时为正。这条恒等式是每行记录的自检位:落盘的数一旦违反它,
// 就说明拿错了盘口的某一边(把 bid 当 ask 之类),而不是行情真有那么好。
func TestCrossVenueEdgesCannotBothBePositive(t *testing.T) {
	cases := []struct{ gBid, gAsk, rBid, rAsk float64 }{
		{100.00, 100.10, 99.90, 100.00}, // gate 贵
		{99.80, 99.90, 100.00, 100.10},  // gate 便宜
		{50.0, 50.5, 50.2, 50.3},        // 互相包含
		{1e-6, 1.1e-6, 1.05e-6, 1.06e-6},
	}
	for _, c := range cases {
		g := fixtureGate()
		g.HighestBid, g.LowestAsk = ftoa(c.gBid), ftoa(c.gAsk)
		ref := map[string]BookTicker{"AAAUSDT": {BidPx: c.rBid, BidQty: 5, AskPx: c.rAsk, AskQty: 4}}
		rows := buildFuturesScanRows([]gateFuturesTicker{g}, ref, time.Unix(0, 0))
		if len(rows) != 1 {
			t.Fatalf("%v: 应得 1 行", c)
		}
		r := rows[0]
		want := -((r.ExecAskPx - r.ExecBidPx) + (r.RefAskPx - r.RefBidPx)) / r.RefMid * 10000
		approx(t, r.XBuyExecBps+r.XSellExecBps, want, 1e-6, "两方向之和 = −(两所价差之和)")
		if r.XBuyExecBps > 0 && r.XSellExecBps > 0 {
			t.Fatalf("%v: 两个方向不可能同时为正,实得 %+v", c, r)
		}
	}
}

// 落盘的每一行都必须能被【不跑这个程序、不带凭据】的第三方重算一遍 ——
// 这是"可复核"的定义本身,也是台账 #7 迟迟关不掉的那个坑。
func TestFuturesScanLineIsOfflineVerifiable(t *testing.T) {
	rows := buildFuturesScanRows([]gateFuturesTicker{fixtureGate()}, fixtureRef(), time.Unix(1757400000, 0).UTC())
	var buf bytes.Buffer
	n, err := writeFuturesScanRows(&buf, rows)
	if err != nil || n != 1 {
		t.Fatalf("落盘失败: n=%d err=%v", n, err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("一行一条记录,实得 %d 行", len(lines))
	}

	var r FuturesScanRow
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatalf("落盘的必须是合法单行 JSON: %v\n原文: %s", err, lines[0])
	}
	if r.Ts.IsZero() {
		t.Fatal("时间戳必须落盘,否则拼不出时间序列 —— #14 要的就是持续性")
	}

	// 只用八个原始报价重算,必须与记录里的派生量一致。复核者不必信任任何派生字段。
	execM := (r.ExecBidPx + r.ExecAskPx) / 2
	refM := (r.RefBidPx + r.RefAskPx) / 2
	approx(t, r.ExecMid, execM, 1e-12, "exec 中价可重算")
	approx(t, r.RefMid, refM, 1e-12, "ref 中价可重算")
	approx(t, r.BasisBps, (execM-refM)/refM*10000, 1e-9, "基差可重算")
	approx(t, r.ExecSpreadBps, (r.ExecAskPx-r.ExecBidPx)/execM*10000, 1e-9, "gate 价差可重算")
	approx(t, r.RefSpreadBps, (r.RefAskPx-r.RefBidPx)/refM*10000, 1e-9, "binance 价差可重算")
	approx(t, r.XBuyExecBps, (r.RefBidPx-r.ExecAskPx)/refM*10000, 1e-9, "买gate卖bin 可重算")
	approx(t, r.XSellExecBps, (r.ExecBidPx-r.RefAskPx)/refM*10000, 1e-9, "买bin卖gate 可重算")
	// 深度同理:张数 × 换算因子 × 价格,不用相信 *_usd 字段
	approx(t, r.ExecBidUSD, r.ExecBidSzCont*r.QuantoMultiplier*r.ExecBidPx, 1e-9, "gate 买一深度可重算")
	approx(t, r.ExecAskUSD, r.ExecAskSzCont*r.QuantoMultiplier*r.ExecAskPx, 1e-9, "gate 卖一深度可重算")
	approx(t, r.RefBidUSD, r.RefBidQty*r.RefBidPx, 1e-9, "binance 买一深度可重算")
	approx(t, r.RefAskUSD, r.RefAskQty*r.RefAskPx, 1e-9, "binance 卖一深度可重算")
}

// 脏盘口(单边/零价/零换算因子)一律不落行。理由和 observe_store 那条一样:
// 让 Inf/NaN 流进 json.Marshal,整条记录会无声消失,比少一条更糟。
func TestFuturesScanDropsUnusableRows(t *testing.T) {
	base := fixtureGate()
	oneSided := base
	oneSided.HighestBid = "0"
	noMult := base
	noMult.QuantoMultiplier = "0"
	thin := base
	thin.Volume24hQuote = "1000000" // < futScanMinQuoteVolUSD
	noRef := base
	noRef.Contract = "ZZZ_USDT"
	notUSDT := base
	notUSDT.Contract = "AAA_BTC"

	for _, c := range []struct {
		name string
		in   gateFuturesTicker
	}{
		{"gate 单边盘", oneSided},
		{"换算因子缺失", noMult},
		{"成交额低于门槛", thin},
		{"没有 binance 永续参考", noRef},
		{"非 USDT 合约", notUSDT},
	} {
		if got := buildFuturesScanRows([]gateFuturesTicker{c.in}, fixtureRef(), time.Unix(0, 0)); len(got) != 0 {
			t.Fatalf("%s 应被丢弃,实得 %d 行: %+v", c.name, len(got), got)
		}
	}

	// binance 侧单边同样要丢
	badRef := map[string]BookTicker{"AAAUSDT": {BidPx: 0, AskPx: 100}}
	if got := buildFuturesScanRows([]gateFuturesTicker{base}, badRef, time.Unix(0, 0)); len(got) != 0 {
		t.Fatalf("binance 单边盘应被丢弃,实得 %d 行", len(got))
	}

	// 而正常那条必须留下,否则上面全丢只是因为过滤写死了
	if got := buildFuturesScanRows([]gateFuturesTicker{base}, fixtureRef(), time.Unix(0, 0)); len(got) != 1 {
		t.Fatalf("正常样本必须保留,实得 %d 行", len(got))
	}
}

// 疑点只打标、不删行 —— 沿用 universe.go 的处理方式(机会不漏、陷阱看得见)。
func TestFuturesScanFlagsSuspectButKeepsRow(t *testing.T) {
	// gate 报 130/131(相对 binance 99.9/100 高约 30%),远超 suspectDivergenceBps
	far := fixtureGate()
	far.HighestBid, far.LowestAsk = "130.00", "130.01"
	rows := buildFuturesScanRows([]gateFuturesTicker{far}, fixtureRef(), time.Unix(0, 0))
	if len(rows) != 1 || rows[0].Suspect != "偏离大" {
		t.Fatalf("偏离大应打标且保留,实得 %+v", rows)
	}

	// gate 价差 100.00/101.00 ≈ 99.5bps,超过 suspectSpreadBps
	wide := fixtureGate()
	wide.LowestAsk = "101.00"
	rows = buildFuturesScanRows([]gateFuturesTicker{wide}, fixtureRef(), time.Unix(0, 0))
	if len(rows) != 1 || rows[0].Suspect != "宽价差" {
		t.Fatalf("宽价差应打标且保留,实得 %+v", rows)
	}
}

// 落盘不能吞错:任何一行 marshal 不掉都要把错误抛回去,并如实报告已写行数。
func TestFuturesScanWriteReportsError(t *testing.T) {
	rows := []FuturesScanRow{
		{Contract: "OK_USDT"},
		{Contract: "BAD_USDT", BasisBps: math.Inf(1)},
		{Contract: "NEVER_USDT"},
	}
	var buf bytes.Buffer
	n, err := writeFuturesScanRows(&buf, rows)
	if err == nil {
		t.Fatal("Inf 必须让落盘报错,不能静默写出半条记录")
	}
	if n != 1 {
		t.Fatalf("报错前应已写 1 行,实得 %d", n)
	}
}

func ftoa(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
