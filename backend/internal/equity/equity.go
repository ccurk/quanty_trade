// Package equity is the persistence outlet for balance observations.
//
// Why it exists (台账 #42): the system reads its own balances tens of thousands of
// times a day and has never once written one down.
//
//	internal/marketmaker/engine.go   ex.Balances()        ~4x/second (1 per pair per
//	                                                      refresh, refresh_ms=1000)
//	internal/exchange/binance.go     USDMAvailableUSDT()  every position size-up,
//	                                                      every /optimize/context
//
// Both return, the caller uses the number, and the number is gone. Nothing is
// missing except an INSERT. That is why every funding question so far has been
// answered by REVERSE-ENGINEERING equity out of fill notionals and config
// percentages (state/strategy/equity-blindspot-2026-09-09.md §3), which produces a
// LOWER BOUND resting on unverified assumptions — not a measurement.
//
// This package is deliberately a LEAF: it imports only internal/logger and the
// stdlib. That is what lets both internal/marketmaker (which imports nothing but
// logger, and must keep it that way) and internal/exchange emit into it without an
// import cycle, and it guarantees the emit path cannot reach a database.
//
// Three design constraints, in priority order:
//
//  1. NEVER slow down trading. Emit is called from inside the quoting loop.
//     It does a map lookup and one NON-BLOCKING channel send; buffer full means
//     drop-and-count, never wait. The DB write happens on another goroutine.
//     A broken metrics path must cost a metric, not a quote.
//
//  2. APPEND-ONLY, and no "total equity" column. A snapshot is an observation at a
//     point in time; it is never updated. Total equity is a QUERY-TIME expression
//     (SUM over venues), never a stored column — store it and one FX/haircut
//     revision invalidates every historical row, and you have built a second
//     source of truth. This is the same shape as models.ExchangeFill and
//     models.MarkoutFill.
//
//  3. EVERY ROW INDEPENDENTLY RE-CHECKABLE. A row carries venue, instant, asset,
//     free/total/unrealized, and the exact API endpoint it came from. VENUE IS
//     MANDATORY: the #18/#22 collision happened because two numbers from two
//     different venues ($57.60 Polymarket, $236 Binance) were treated as two
//     estimates of one quantity and triangulated, when they should have been
//     ADDED (equity-blindspot-2026-09-09.md §4). An unlabelled balance is not a
//     weak data point, it is a trap. Emit refuses a row with no venue.
package equity

import (
	"sync"
	"sync/atomic"
	"time"

	"quanty_trade/internal/logger"
)

// Venue labels. One venue = one place money actually sits. These strings land in
// the venue column verbatim and are the join key for any cross-venue sum, so they
// are constants rather than string literals at the call sites.
const (
	VenueBinanceUSDM = "binance_usdm" // Binance USD-M perpetuals wallet
	VenueBinanceSpot = "binance_spot"
	VenueGateSpot    = "gate_spot"
	VenuePolymarket  = "polymarket" // written by the orchestrator, not this backend
)

// Snapshot is one venue's holding of one asset at one instant. Immutable.
type Snapshot struct {
	Venue   string
	Asset   string
	TakenAt time.Time

	// Free is what can be spent right now (Binance availableBalance, Gate available).
	Free float64
	// Total is Free plus whatever is committed but still ours — margin posted,
	// open-order locks (Binance balance i.e. the wallet, Gate available+locked).
	// Total is stored NEXT TO Free rather than instead of it because the gap
	// between them IS the answer to "why can't the engine open a position when we
	// clearly have money".
	Total float64
	// Unrealized is open-position mark-to-market, and is meaningful only on a
	// derivatives venue. On a spot venue it is structurally 0 — the venue column
	// is what tells a reader that the 0 means "not applicable" rather than
	// "we failed to read it". Nothing here is ever a null-as-zero.
	Unrealized float64

	// Source is the exact endpoint this row was decoded from, e.g.
	// "GET /fapi/v2/balance". It is what makes a row checkable by a second person
	// months later: they can re-issue that call and compare, without reading this
	// program. Never a vague "api".
	Source string
}

// Sink is the storage outlet. Implemented in the equitydb subpackage, which is
// the only side that knows about GORM/models. Implementers must assume they will
// be slow and will fail: they run on a private goroutine, and a returned error is
// counted and logged, never propagated toward the trading path.
type Sink interface {
	WriteEquitySnapshot(Snapshot) error
}

// Downsampling. The read sites fire ~4x/second; writing all of them would be
// ~1.7M rows/day of overwhelmingly identical data (算式见
// scripts/equity_snapshots.sql §2). These three knobs cut that by ~200x while
// keeping the table honest, and each one is load-bearing:
//
//   - minWriteInterval caps the rate. Without it the quoting loop floods the DB.
//   - a value change beats the cap being unexpired only in the sense that we still
//     wait out minWriteInterval; between minWriteInterval and heartbeatInterval we
//     write ONLY if something actually moved, so a quiet account costs ~nothing.
//   - heartbeatInterval forces a row even when nothing moved. This is the one that
//     is easy to drop and must not be: without it, "the balance held steady" and
//     "we stopped observing" produce the same empty range, and a gap in an audit
//     table has to be distinguishable from a flat line.
const (
	minWriteInterval  = 60 * time.Second
	heartbeatInterval = 15 * time.Minute
	// relChangeEps ignores float round-trip noise while catching any real move.
	// Free/Total are parsed from decimal strings, so genuine changes are far
	// larger than this; Total is a sum of two of them, hence not exact equality.
	relChangeEps = 1e-9
)

type lastWrite struct {
	at   time.Time
	snap Snapshot
}

var (
	sinkMu sync.RWMutex
	sinkCh chan Snapshot
	stopCh chan struct{}

	lastMu   sync.Mutex
	lastSeen = map[string]lastWrite{} // "venue|asset" -> last row actually enqueued

	observed   atomic.Int64
	suppressed atomic.Int64
	enqueued   atomic.Int64
	dropped    atomic.Int64
	written    atomic.Int64
	failed     atomic.Int64
)

// SetSink attaches a storage outlet and starts its writer goroutine.
// buffer <= 0 uses the default. Passing nil detaches (used by tests): after that
// Emit still counts and still downsamples, but performs no IO whatsoever.
//
// Idempotent-by-replacement: calling it again stops the previous writer.
func SetSink(s Sink, buffer int) {
	if buffer <= 0 {
		buffer = 256
	}
	sinkMu.Lock()
	if stopCh != nil {
		close(stopCh)
		stopCh = nil
	}
	if s == nil {
		sinkCh = nil
		sinkMu.Unlock()
		return
	}
	ch := make(chan Snapshot, buffer)
	stop := make(chan struct{})
	sinkCh, stopCh = ch, stop
	sinkMu.Unlock()

	go func() {
		for {
			select {
			case <-stop:
				return
			case s2 := <-ch:
				err := sink(s).WriteEquitySnapshot(s2)
				// A write that was still in flight when this writer got replaced
				// or detached must not touch the counters: it no longer speaks for
				// the live pipeline, and its late increment would be attributed to
				// whatever sink is attached now.
				select {
				case <-stop:
					return
				default:
				}
				if err != nil {
					// Counted and logged, never retried. A retry queue in front of
					// a slow DB grows without bound and ends as "buffer full, drop
					// everything" with the cause hidden. Losing a snapshot is
					// acceptable; stalling the observer on the trading path is not.
					if n := failed.Add(1); n == 1 || n%100 == 0 {
						logger.Errorf("[equity] 落库失败(累计 %d 条): %v", n, err)
					}
					continue
				}
				written.Add(1)
			}
		}
	}()
}

// sink exists so the goroutine above reads the interface value it was started
// with, not a global that a later SetSink could swap underneath it.
func sink(s Sink) Sink { return s }

// Emit offers one observation to the sink. Safe to call from the quoting loop:
// it never blocks, never does IO, and never returns an error to the caller.
//
// A snapshot with no venue is DROPPED, not stored unlabelled — see the package
// comment on why an unlabelled balance is worse than a missing one.
func Emit(s Snapshot) {
	if s.Venue == "" || s.Asset == "" {
		return
	}
	if s.TakenAt.IsZero() {
		s.TakenAt = time.Now()
	}
	observed.Add(1)
	if !shouldWrite(s) {
		suppressed.Add(1)
		return
	}
	enqueued.Add(1)

	sinkMu.RLock()
	ch := sinkCh
	sinkMu.RUnlock()
	select {
	case ch <- s:
	default:
		// No sink attached (nil channel is never ready), or buffer full.
		// Either way this one row is dropped and counted.
		dropped.Add(1)
	}
}

// shouldWrite applies the downsampling rules and, when it says yes, records the
// decision. Holds a mutex only for a map lookup — no IO can ever occur under it.
func shouldWrite(s Snapshot) bool {
	k := s.Venue + "|" + s.Asset
	lastMu.Lock()
	defer lastMu.Unlock()

	prev, ok := lastSeen[k]
	if ok {
		age := s.TakenAt.Sub(prev.at)
		switch {
		case age < minWriteInterval:
			return false // rate cap: at most one row per key per minute
		case age >= heartbeatInterval:
			// forced row, so a flat balance is still provably observed
		case !moved(prev.snap, s):
			return false
		}
	}
	lastSeen[k] = lastWrite{at: s.TakenAt, snap: s}
	return true
}

func moved(a, b Snapshot) bool {
	return diff(a.Free, b.Free) || diff(a.Total, b.Total) || diff(a.Unrealized, b.Unrealized)
}

// diff reports whether two readings of the same quantity differ beyond float noise.
func diff(x, y float64) bool {
	d := x - y
	if d < 0 {
		d = -d
	}
	scale := x
	if scale < 0 {
		scale = -scale
	}
	if scale < 1 {
		scale = 1
	}
	return d > relChangeEps*scale
}

// Stats is the self-check for this pipeline.
//
// observed = how many times a balance was read at all; suppressed = how many the
// downsampler folded away (expected to be the overwhelming majority — that is the
// feature); written/failed/dropped = what the storage end did.
type Stats struct {
	Observed   int64 `json:"observed"`
	Suppressed int64 `json:"suppressed"`
	Enqueued   int64 `json:"enqueued"`
	Dropped    int64 `json:"dropped"`
	Written    int64 `json:"written"`
	Failed     int64 `json:"failed"`
}

// Counters reads the self-check counters.
func Counters() Stats {
	return Stats{
		Observed:   observed.Load(),
		Suppressed: suppressed.Load(),
		Enqueued:   enqueued.Load(),
		Dropped:    dropped.Load(),
		Written:    written.Load(),
		Failed:     failed.Load(),
	}
}

// ResetForTest clears the downsampler state and counters. Test-only.
func ResetForTest() {
	lastMu.Lock()
	lastSeen = map[string]lastWrite{}
	lastMu.Unlock()
	observed.Store(0)
	suppressed.Store(0)
	enqueued.Store(0)
	dropped.Store(0)
	written.Store(0)
	failed.Store(0)
}
