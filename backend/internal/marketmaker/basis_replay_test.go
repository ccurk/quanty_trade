package marketmaker

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 离线回放:不需要任何成交,就能判断基差修正到底修对了没有。
//
// 为什么需要它:台账 #7 的修复(basis.go)默认关闭,而定档要的 markout A/B 必须有成交。
// 于是"代码写了但没人知道对不对"卡在那里。但有一件事不需要成交也能证明 ——
// 修正项作用在【报价中心】上,而报价中心与执行所中价的偏离(残差)每一秒都被观测记录着。
// 拿历史观测重放一遍,就能看出残差是不是真的回到了零附近。
//
// 跑法(数据不在仓库里,从服务器拉,只读):
//
//	ssh mycloud 'docker exec quanty-backend sh -c "grep -a mm-observe /app/logs/server.log" | gzip -c' \
//	  > /tmp/mm_observe.log.gz
//	MM_OBSERVE_LOG=/tmp/mm_observe.log.gz go test ./internal/marketmaker/ -run TestBasisReplay -v
//
// 没设 MM_OBSERVE_LOG 就跳过 —— 常规 go test 不依赖外部数据,不会因为拿不到日志变红。
//
// 三条对照,缺一不可:
//
//	raw     |b|              不修正(线上此刻的行为)
//	ewma    |b − b̂_t|        本次修复,b̂ 严格只用【当刻之前】的样本(与 engine.go:147-149 同序)
//	static  |b − median(b)|  只做一次固定水平位移的【非因果上界】
//
// static 那一列是这个测试的关键,不是凑数的:
//   - EWMA ≈ static  → 修正只吃掉了"水平位移",没碰行情 = 安全,正是想要的。
//   - EWMA ≪ static  → 修正跟上了窗口内的来回摆动,那不是水位差,那是跨所价格发现本身。
//     对零均值品种出现这种情况就是【误伤】,要报出来,不能当成"效果好"。
//
// 只看"残差变小了"会把误伤当成疗效 —— 半衰期越短残差越小,极限是拿上一条样本当预测,
// 那等于放弃参考所。所以必须有一条"纯水平位移"的参照线。
type replaySample struct {
	ts      time.Time
	basis   float64 // b = (execMid−refMid)/refMid×1e4
	spread  float64 // s:执行所自身价差(bps)
	refMid  float64
	execMid float64
}

// parseObserveLine 认两种落盘格式:
//   - 单行 JSON(795a348 起,带两所原始盘口)
//   - 旧的 key=value 文本(线上此刻跑的 d933d91 就是这个)
//
// 必须两种都认:现在服务器上 94 万条全是旧格式,而部署之后新数据是 JSON 格式。
// 只认一种的话,这个回放要么现在跑不了,要么部署当天就作废。
func parseObserveLine(line string) (symbol string, s replaySample, ok bool) {
	_, rest, found := strings.Cut(line, "[mm-observe] ")
	if !found {
		return "", s, false
	}
	if strings.HasPrefix(rest, "{") {
		var r ObserveRow
		if json.Unmarshal([]byte(rest), &r) != nil || r.RefMid <= 0 || r.ExecMid <= 0 {
			return "", s, false
		}
		return r.Symbol, replaySample{
			ts: r.Ts, basis: r.MidDiffBps, spread: r.ExecSpreadBps,
			refMid: r.RefMid, execMid: r.ExecMid,
		}, true
	}

	// 旧格式:SYMBOL@exch ref=feed refMid=.. execMid=.. midDiff=..bps execSpread=..bps ..
	f := strings.Fields(rest)
	if len(f) < 6 {
		return "", s, false
	}
	sym, _, _ := strings.Cut(f[0], "@")
	refMid, err1 := strconv.ParseFloat(strings.TrimPrefix(f[2], "refMid="), 64)
	execMid, err2 := strconv.ParseFloat(strings.TrimPrefix(f[3], "execMid="), 64)
	spread, err3 := strconv.ParseFloat(
		strings.TrimSuffix(strings.TrimPrefix(f[5], "execSpread="), "bps"), 64)
	if err1 != nil || err2 != nil || err3 != nil || refMid <= 0 || execMid <= 0 {
		return "", s, false
	}
	// 时间戳在 "[mm-observe] " 之前:2026/09/05 17:56:35.861316 [INFO] ...
	tf := strings.Fields(line)
	if len(tf) < 2 {
		return "", s, false
	}
	ts, err := time.Parse("2006/01/02 15:04:05.000000", tf[0]+" "+tf[1])
	if err != nil {
		return "", s, false
	}
	// b 从两个中价现算,不取日志里的 midDiff —— 旧格式的 midDiff 只印到 0.1bps,
	// SOL 的 |b| 中位数本身就是 1bps 量级,拿印出来的值算等于自带 10% 量化误差。
	// 中价有 8 位小数,现算是精确的,而且与 observeRow 里的式子逐字一致。
	return sym, replaySample{
		ts: ts, basis: (execMid - refMid) / refMid * 10000, spread: spread,
		refMid: refMid, execMid: execMid,
	}, true
}

func loadObserve(t *testing.T, path string) map[string][]replaySample {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打不开 %s: %v", path, err)
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatalf("gzip %s: %v", path, err)
		}
		defer gz.Close()
		r = gz
	}

	out := map[string][]replaySample{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	var total, bad int
	for sc.Scan() {
		total++
		sym, s, ok := parseObserveLine(sc.Text())
		if !ok {
			bad++
			continue
		}
		out[sym] = append(out[sym], s)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	t.Logf("读入 %d 行,解析成功 %d 行,跳过 %d 行", total, total-bad, bad)
	for sym := range out {
		sort.Slice(out[sym], func(i, j int) bool { return out[sym][i].ts.Before(out[sym][j].ts) })
	}
	return out
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	i := int(p * float64(len(sorted)))
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

// stats 汇总一组残差:分位数 + |残差| 超过半价差的占比(#7 的判据本身)。
type stats struct {
	p10, p50, p90 float64
	overHalfFrac  float64
	medAbs        float64
}

func summarize(resid, spread []float64) stats {
	signed := append([]float64(nil), resid...)
	sort.Float64s(signed)
	abs := make([]float64, len(resid))
	var over int
	for i, v := range resid {
		abs[i] = math.Abs(v)
		if abs[i] > spread[i]/2 {
			over++
		}
	}
	sort.Float64s(abs)
	return stats{
		p10: pct(signed, 0.10), p50: pct(signed, 0.50), p90: pct(signed, 0.90),
		medAbs:       pct(abs, 0.50),
		overHalfFrac: float64(over) / float64(len(resid)),
	}
}

// replayEWMA 用生产同一个估计量重放一遍,返回逐条残差。
// 取值/更新的先后顺序与 engine.go:147-149 完全一致(先取 correctionBps 再 update),
// 所以 b̂ 严格只含【当刻之前】的信息 —— 否则就是拿未来的数偷看,回放结果没有意义。
func replayEWMA(samples []replaySample, halfLifeS, capBps float64) (resid, corr []float64) {
	est := newBasisEWMA(halfLifeS, capBps)
	resid = make([]float64, len(samples))
	corr = make([]float64, len(samples))
	for i, s := range samples {
		c := est.correctionBps()
		corr[i] = c
		resid[i] = s.basis - c
		est.update(s.basis, s.ts)
	}
	return resid, corr
}

func TestBasisReplayOnObserveLog(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("MM_OBSERVE_LOG"))
	if path == "" {
		t.Skip("未设 MM_OBSERVE_LOG,跳过离线回放(常规 go test 不依赖外部数据)")
	}
	bySym := loadObserve(t, path)
	if len(bySym) == 0 {
		t.Fatal("没解析出任何观测记录")
	}

	// 带上 1s/3s 是为了回答一个具体问题:这条指标到底有没有极小值。
	// 有极小值 → 它能定档;单调递减到最短 → 它只能证明"该修",定不了档。
	halfLives := []float64{1, 3, 10, 30, 60, 300, 1800}
	syms := make([]string, 0, len(bySym))
	for s := range bySym {
		syms = append(syms, s)
	}
	sort.Strings(syms)

	for _, sym := range syms {
		ss := bySym[sym]
		spread := make([]float64, len(ss))
		raw := make([]float64, len(ss))
		for i, s := range ss {
			spread[i] = s.spread
			raw[i] = s.basis
		}
		rawStat := summarize(raw, spread)

		// static:用【整段窗口的中位数】做一次固定水平位移。非因果(偷看了全窗口),
		// 所以它是"纯水平位移"能达到的上界。EWMA 比它好得多 = EWMA 在跟行情不是跟水位。
		sortedRaw := append([]float64(nil), raw...)
		sort.Float64s(sortedRaw)
		med := pct(sortedRaw, 0.50)
		staticResid := make([]float64, len(raw))
		for i, v := range raw {
			staticResid[i] = v - med
		}
		staticStat := summarize(staticResid, spread)

		t.Logf("=== %s  n=%d  窗口 %s → %s (UTC)  中位价差 s=%.2fbps",
			sym, len(ss), ss[0].ts.UTC().Format("2006-01-02 15:04:05"),
			ss[len(ss)-1].ts.UTC().Format("2006-01-02 15:04:05"), pct(sortedSpread(spread), 0.50))
		t.Logf("  %-22s %9s %9s %9s %9s %9s", "方案", "p10", "中位", "p90", "中位|·|", "|·|>s/2")
		t.Logf("  %-22s %9.2f %9.2f %9.2f %9.2f %8.1f%%", "raw(不修正,线上)",
			rawStat.p10, rawStat.p50, rawStat.p90, rawStat.medAbs, rawStat.overHalfFrac*100)
		t.Logf("  %-22s %9.2f %9.2f %9.2f %9.2f %8.1f%%", "static(全窗中位位移)",
			staticStat.p10, staticStat.p50, staticStat.p90, staticStat.medAbs, staticStat.overHalfFrac*100)

		for _, hl := range halfLives {
			resid, corr := replayEWMA(ss, hl, 0)
			st := summarize(resid, spread)
			// 自检:残差的绝对值必须与生产风控闸门 liveDivergenceBps 算的是同一个量。
			// 对不上就说明回放和线上算的不是一回事,后面所有数字都不用看了。
			//
			// 这里【不能】要求逐位相等,原因是实测出来的,不是猜的:arm64 上
			// `go build -gcflags=-S` 显示 basis.go:125 那行被编译成 FNMSUBD ——
			// 编译器把 `商*10000 - corrBps` 融成了一条 FMA(Go 规范允许)。
			// 回放这边是先把 b 存成 float64 再减,少了一次融合,两者差不到 1 个 ULP。
			// 相对容差 1e-12 足以放过这个,又远小于任何"算错了量"的差(那是量级差)。
			for i := range ss {
				want := liveDivergenceBps(ss[i].refMid, ss[i].execMid, corr[i])
				got := math.Abs(resid[i])
				if math.Abs(got-want) > 1e-12*math.Max(1, math.Abs(want)) {
					t.Fatalf("%s#%d 残差与 liveDivergenceBps 不一致:%v vs %v", sym, i, got, want)
				}
			}
			t.Logf("  %-22s %9.2f %9.2f %9.2f %9.2f %8.1f%%",
				"ewma 半衰期 "+strconv.FormatFloat(hl, 'f', -1, 64)+"s",
				st.p10, st.p50, st.p90, st.medAbs, st.overHalfFrac*100)
		}
	}
}

func sortedSpread(s []float64) []float64 {
	c := append([]float64(nil), s...)
	sort.Float64s(c)
	return c
}

// TestBasisLeadLagOnObserveLog 回答"修正到底该不该做"这个问题本身,同样不需要成交。
//
// 上面那张表只能说明残差变小了,不能说明变小是好事:半衰期越短残差越小,
// 极限是拿上一条样本当预测,那等于放弃参考所。所以还得问一句 ——
// 基差 b 出现之后,是【执行所回来】还是【参考所追过去】?
//
//	β_exec = cov(Δexec, b)/var(b)  执行所在未来 h 秒内朝参考所收敛的比例。
//	                               负得多 = 执行所自己会回来 = b 是水位差/噪声,修掉它安全。
//	β_ref  = cov(Δref,  b)/var(b)  参考所在未来 h 秒内朝执行所收敛的比例。
//	                               正得多 = 参考所在追执行所 = 执行所领先 = b 是【信息】,
//	                               修掉它等于把跨所价格发现扔了,这才是"误伤"。
//
// 两个 Δ 都以【当刻参考所中价】为基换算成 bps,这样它们和 b 同量纲、可以直接相减对账:
// β_exec − β_ref 就是 b 在 h 秒内被抹平的总比例。
func TestBasisLeadLagOnObserveLog(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("MM_OBSERVE_LOG"))
	if path == "" {
		t.Skip("未设 MM_OBSERVE_LOG,跳过 lead-lag 诊断")
	}
	bySym := loadObserve(t, path)
	syms := make([]string, 0, len(bySym))
	for s := range bySym {
		syms = append(syms, s)
	}
	sort.Strings(syms)

	horizons := []time.Duration{5 * time.Second, 60 * time.Second}
	t.Logf("%-14s %6s %10s %10s %10s %10s", "symbol", "h", "n", "β_exec", "β_ref", "b 抹平比例")
	for _, sym := range syms {
		ss := bySym[sym]
		for _, h := range horizons {
			var n int
			var sb, sbb, se, sr, sbe, sbr float64
			for _, s := range ss {
				// 找 h 秒之后的那条。跨重启/断流的空档要跳过,否则 Δ 里混进
				// 的是几十分钟的行情,不是 h 秒的反应。
				j := sort.Search(len(ss), func(k int) bool { return !ss[k].ts.Before(s.ts.Add(h)) })
				if j >= len(ss) || ss[j].ts.Sub(s.ts) > 3*h {
					continue
				}
				dExec := (ss[j].execMid - s.execMid) / s.refMid * 10000
				dRef := (ss[j].refMid - s.refMid) / s.refMid * 10000
				n++
				sb += s.basis
				sbb += s.basis * s.basis
				se += dExec
				sr += dRef
				sbe += s.basis * dExec
				sbr += s.basis * dRef
			}
			if n < 100 {
				t.Logf("%-14s %6s %10d  样本不足,跳过", sym, h, n)
				continue
			}
			fn := float64(n)
			mb := sb / fn
			varB := sbb/fn - mb*mb
			if varB <= 0 {
				continue
			}
			betaExec := (sbe/fn - mb*(se/fn)) / varB
			betaRef := (sbr/fn - mb*(sr/fn)) / varB
			t.Logf("%-14s %6s %10d %10.3f %10.3f %10.3f", sym, h, n, betaExec, betaRef, betaExec-betaRef)
		}
	}
}
