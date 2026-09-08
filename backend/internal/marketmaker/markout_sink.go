package marketmaker

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"quanty_trade/internal/logger"
)

// markout_sink.go 是 markout 的【持久化出口】。
//
// 为什么必须有:markout.go 算出来的东西全部只活在 MarkoutTracker.done 里 ——
// 一个 5000 条的内存滚动窗口,进程重启即失,且只有拿着 token 打
// GET /stats/mm-observe 才看得到。结果是 2026-09-09 之前每一次要用 markout 做判断,
// 都得拿 server.log 离线重算一遍(见 state/strategy/quote-anchor-decision-2026-09-09.md
// §6:"本报告里所有 markout 都是我用 markout.go 的公式在离线数据上重算的,
// 不是它跑出来的")。算了无数遍,一条也没记下来。
//
// 三条设计约束,顺序就是优先级:
//
//  1. 【绝不拖累下单】。写入口 emitMarkoutRecord 挂在 engine.go 的报价循环上
//     (循环 → markoutTracker.Observe → resolveLocked → emit),而且此刻还持着
//     tracker 的锁。所以出口只能是一次【非阻塞】的 channel 投递:满了就丢并计数,
//     绝不 select 等待、绝不在这里碰 DB。真正的写库在另一个 goroutine 里。
//     度量挂掉的代价必须是"少一条度量",不能是"报价慢一拍"。
//
//  2. 【append-only】。一条成交产出一条不可变的记录,写完不再改。没有"当前值"
//     这种会被覆盖的列 —— 那正是台账 #42 的病根(余额读了几万次、一次没落库,
//     且就算落也只落"现在多少")。
//
//  3. 【逐条可独立复核】。记录里带的是【原始量】(成交价、每个 horizon 实际用到的
//     中价和它的时刻),bps 只是它们的函数。第二个人可以只用这一行重算 bps 对答案,
//     不需要跑这个程序、不需要凭据。Verify() 就是这份复核的可执行形式。

// MarkoutPoint 是一个 horizon 上的一次采样 —— markout 的全部原始输入。
type MarkoutPoint struct {
	Horizon string `json:"horizon"` // "1s" | "5s" | "30s"
	// Mid 是这个 horizon 实际用到的【执行所】中价(必须与 FillPx 同所,见 markoutBps 注释)。
	Mid float64 `json:"mid"`
	// SampleTs 是那个中价样本自己的时刻。resolveLocked 取的是"target 之后的第一个
	// 样本",所以它总是 ≥ FillTs+Horizon;差多少 = LagMs,是这条记录的质量标签:
	// lag 大就说明行情稀疏,该 horizon 的 markout 其实测的是更长的时间尺度。
	SampleTs time.Time `json:"sample_ts"`
	LagMs    int64     `json:"lag_ms"`
	// Bps 是落盘时算好的 markout,= markoutBps(Side, FillPx, Mid)。存它是为了让
	// SQL 侧不用重算就能聚合;它与上面三个原始量的一致性由 Verify() 守住。
	// Stale=true 时它仍然自洽(所以 Verify 照常能查),但【不该被当作这个 horizon
	// 的 markout 用】—— 落库时那一列写 NULL,见 Stale。
	Bps float64 `json:"bps"`
	// Stale=true:样本来得太晚(LagMs > maxSampleLag(horizon)),Bps 实际测的是一个
	// 更长的时间尺度。这类点【保留证据、不产出数】:落库时 mid/sample_ts/lag 照写、
	// markout_bps_* 写 NULL、stale_* 写 1。
	//
	// 为什么不静默丢:丢了就没人知道丢了多少,而"这个品种 30s 样本少"和
	// "这个品种 30s 大半被断流吃掉了"是两个相反的结论。
	// 为什么不混着写:AVG(markout_bps_5s) 是所有人都会打的第一条查询,
	// 它必须默认就是对的 —— NULL 天然被 AVG 跳过,而计数仍可由 SUM(stale_5s) 得到。
	Stale bool `json:"stale"`
}

// MarkoutRecord 是一笔成交的 markout 全画像,一次成交一条,不可变。
type MarkoutRecord struct {
	Exchange string  `json:"exchange"`
	Symbol   string  `json:"symbol"`
	FillID   string  `json:"fill_id"`
	Side     string  `json:"side"`
	FillPx   float64 `json:"fill_px"`
	Amount   float64 `json:"amount"`
	FeeBps   float64 `json:"fee_bps"`
	// FillTs 是交易所给的成交时刻(create_time),不是我们轮询到它的时刻。
	// 引擎每 10s 才拉一次成交,用轮询时刻会把 1s horizon 直接测成噪声。
	FillTs time.Time `json:"fill_ts"`
	// MidAtFill 是成交时刻的执行所中价(取 FillTs 之前最后一个样本),0 = 无样本覆盖。
	// 它把这笔的经济性拆成两段:(MidAtFill − FillPx) 是【拿到的边】,
	// 后面 Points 里的 markout 是【成交之后的漂移】。只看后者会把
	// "报价挂得好但随后被市场跑赢"和"报价本身就吃亏"混成一个数。
	MidAtFill float64 `json:"mid_at_fill"`
	// MidAtFillTs 是那个样本自己的时刻;它必然 ≤ FillTs,差值即样本有多陈旧。
	MidAtFillTs    time.Time `json:"mid_at_fill_ts"`
	MidAtFillLagMs int64     `json:"mid_at_fill_lag_ms"` // FillTs − MidAtFillTs,≥0
	// Points 按 MarkoutHorizons 的顺序;Complete=false 时可能缺项。
	Points []MarkoutPoint `json:"points"`
	// Complete=true 表示三个 horizon 【全都产出了可用的 markout】。
	// false 有两种成因,落库后可以分开数:
	//   * 某个 horizon 从头到尾没有样本(行情断流不回来)→ mid_*=0;
	//   * 有样本但太晚(lag > maxSampleLag)          → mid_*>0 且 stale_*=1。
	// 两者结论相反(前者是没数据,后者是数据不能用这个名字),所以不能混成一个。
	// 保留而不是丢弃:缺口本身是数据,统计时可以剔,但必须看得见。
	Complete bool `json:"complete"`
	// ResolvedAt 是最后一个 horizon 结算(或超时判定)的时刻,用于排查落库延迟。
	ResolvedAt time.Time `json:"resolved_at"`
}

// MarkoutBps 是 markoutBps 的导出形式 —— 给【离线复算】用。
//
// 台账里反复出现的漂移风险是:线上算一份、离线脚本按注释里的公式再写一份,
// 两份一旦不同没人会发现(报告里的数就是这么来的)。导出之后,任何 Go 侧的离线
// 工具都能调到【同一个函数】而不是同一段描述。非 Go 的复核路径见
// scripts/markout_persistence.sql 里的 SQL 复核查询 —— 那一份直接跑在落好的库上,
// 不经过任何一份重写的公式。
func MarkoutBps(side string, fillPx, midAfter float64) float64 {
	return markoutBps(side, fillPx, midAfter)
}

// Verify 用记录自带的原始量重算每个 horizon 的 bps,与落盘值比对。
//
// 这是"离线复算和线上同源"的可执行形式:复核方不需要相信 Bps 那一列,
// 拿 Side/FillPx/Mid 重算即可;算不出同一个数,就是这行记录本身坏了。
func (r MarkoutRecord) Verify(tolBps float64) error {
	for _, p := range r.Points {
		want := MarkoutBps(r.Side, r.FillPx, p.Mid)
		if d := want - p.Bps; d > tolBps || d < -tolBps {
			return fmt.Errorf("markout %s/%s fill=%s horizon=%s: 落盘 %.6f ≠ 重算 %.6f",
				r.Exchange, r.Symbol, r.FillID, p.Horizon, p.Bps, want)
		}
	}
	return nil
}

// MarkoutSink 是落库出口。实现在 markoutdb 包(它才依赖 GORM/models);
// 本包对 DB 一无所知,这样 markout 的测量逻辑永远不会被数据库层拖住。
//
// 实现方必须假定自己会失败、会慢:调用发生在独立 goroutine 里,返回错误只会
// 被计数和告警,不会向上冒泡到交易路径。
type MarkoutSink interface {
	WriteMarkout(MarkoutRecord) error
}

// defaultMarkoutBuffer 是投递缓冲。按实测成交频率(2026-09-08 三天窗口,
// 悲观口径 701 笔 / 乐观口径 12,703 笔 ≈ 234~4,234 笔/天)算,1024 条相当于
// 数小时的量 —— 够 DB 抖动几分钟,又不至于在内存里囤太久。
const defaultMarkoutBuffer = 1024

var (
	markoutSinkMu sync.RWMutex
	markoutSinkCh chan MarkoutRecord
	markoutStop   chan struct{}

	markoutEnqueued atomic.Int64
	markoutDropped  atomic.Int64
	markoutWritten  atomic.Int64
	markoutFailed   atomic.Int64
)

// SetMarkoutSink 装上落库出口并起写入 goroutine。buffer<=0 用默认值。
// 传 nil 卸载(测试用):卸载后 emit 退化成纯计数,不再有任何 IO。
//
// 幂等地替换:重复调用会停掉上一个 writer。
func SetMarkoutSink(s MarkoutSink, buffer int) {
	if buffer <= 0 {
		buffer = defaultMarkoutBuffer
	}
	markoutSinkMu.Lock()
	if markoutStop != nil {
		close(markoutStop)
		markoutStop = nil
	}
	if s == nil {
		markoutSinkCh = nil
		markoutSinkMu.Unlock()
		return
	}
	ch := make(chan MarkoutRecord, buffer)
	stop := make(chan struct{})
	markoutSinkCh, markoutStop = ch, stop
	markoutSinkMu.Unlock()

	go func() {
		for {
			select {
			case <-stop:
				return
			case r := <-ch:
				if err := s.WriteMarkout(r); err != nil {
					// 失败只计数+告警。这里绝不重试排队:重试会把慢的 DB 变成
					// 一个越积越长的队列,最终以"缓冲满 → 全丢"收场,还看不出原因。
					// 丢一条度量是可接受的;把交易路径的观测循环拖住不是。
					if n := markoutFailed.Add(1); n == 1 || n%100 == 0 {
						logger.Errorf("[mm-markout] 落库失败(累计 %d 条): %v", n, err)
					}
					continue
				}
				markoutWritten.Add(1)
			}
		}
	}()
}

// emitMarkoutRecord 把一笔结算完(或超时)的成交投递出去。
//
// 全程非阻塞:未装 sink 时 markoutSinkCh 为 nil,nil channel 的 send 永远不就绪,
// select 直接走 default。装了 sink 但缓冲满时同样走 default(丢弃并计数)。
// 因此本函数的耗时与 DB 状态完全无关 —— 这是"写入失败不影响下单"的实现点,
// 由 TestMarkoutSinkFailureDoesNotBlockTradingPath 锁死。
//
// 调用方持有 MarkoutTracker.mu;本函数不得做任何可能阻塞的事。
func emitMarkoutRecord(pf *pendingFill, now time.Time, complete bool) {
	rec := MarkoutRecord{
		Exchange: pf.row.Exchange, Symbol: pf.row.Symbol, FillID: pf.row.FillID,
		Side: pf.row.Side, FillPx: pf.row.FillPx, Amount: pf.row.Amount,
		FeeBps: pf.row.FeeBps, FillTs: pf.row.FillTs,
		Complete: complete, ResolvedAt: now,
	}
	if !pf.midAtFill.ts.IsZero() {
		rec.MidAtFill = pf.midAtFill.mid
		rec.MidAtFillTs = pf.midAtFill.ts
		rec.MidAtFillLagMs = pf.row.FillTs.Sub(pf.midAtFill.ts).Milliseconds()
	}
	for _, h := range MarkoutHorizons {
		s, ok := pf.at[h]
		if !ok {
			continue // 该 horizon 没采到(仅 Complete=false 时可能)
		}
		rec.Points = append(rec.Points, MarkoutPoint{
			Horizon:  horizonKey(h),
			Mid:      s.mid,
			SampleTs: s.ts,
			LagMs:    s.ts.Sub(pf.row.FillTs.Add(h)).Milliseconds(),
			// 直接由样本重算,而不是读 pf.row.ByHorizon:陈旧的 horizon 不进
			// ByHorizon(那是有意的),但它的点仍要自洽,否则 Verify 会把整行否掉、
			// 连证据一起丢。调的是同一个 markoutBps,不存在第二份公式。
			Bps:   markoutBps(pf.row.Side, pf.row.FillPx, s.mid),
			Stale: pf.stale[h],
		})
	}
	markoutEnqueued.Add(1)

	markoutSinkMu.RLock()
	ch := markoutSinkCh
	markoutSinkMu.RUnlock()
	select {
	case ch <- rec:
	default:
		// 未装 sink,或缓冲满。两者都只丢这一条。
		markoutDropped.Add(1)
	}
}

// MarkoutSinkStats 是落库链路的自检计数。
// dropped 长期增长 = 缓冲一直满 = 写入端跟不上;failed 增长 = 表不存在/DB 出错。
type MarkoutSinkStats struct {
	Enqueued int64 `json:"enqueued"`
	Written  int64 `json:"written"`
	Failed   int64 `json:"failed"`
	Dropped  int64 `json:"dropped"`
}

// MarkoutSinkCounters 读取自检计数。
func MarkoutSinkCounters() MarkoutSinkStats {
	return MarkoutSinkStats{
		Enqueued: markoutEnqueued.Load(),
		Written:  markoutWritten.Load(),
		Failed:   markoutFailed.Load(),
		Dropped:  markoutDropped.Load(),
	}
}
