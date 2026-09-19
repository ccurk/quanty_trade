package strategy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"quanty_trade/internal/bus"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"quanty_trade/internal/ws"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// resolveStrategyPath converts a strategy Path stored in DB into an executable
// absolute file path.
//
// Rules:
// - If Path is absolute, use it as-is.
// - If STRATEGIES_DIR is set, treat Path as relative to that directory.
// - Reject paths that escape STRATEGIES_DIR (basic path traversal guard).
func resolveStrategyPath(p string) (string, error) {
	if filepath.IsAbs(p) {
		fi, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		if fi.IsDir() {
			return "", fmt.Errorf("strategy path is a directory, need .py file: %s", p)
		}
		return p, nil
	}
	base := conf.C().Paths.StrategiesDir
	if base == "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return "", err
		}
		if fi.IsDir() {
			return "", fmt.Errorf("strategy path is a directory, need .py file: %s", abs)
		}
		return abs, nil
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	joined := filepath.Clean(filepath.Join(absBase, p))
	if fi, err := os.Stat(joined); err == nil && fi.IsDir() {
		return "", fmt.Errorf("strategy path is a directory, need .py file: %s", joined)
	}
	rel, err := filepath.Rel(absBase, joined)
	if err != nil {
		return "", err
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
		return joined, nil
	}
	return "", fmt.Errorf("invalid strategy path: %s", p)
}

// loadLatestDiskStrategySource 在 strategies/ 目录里找匹配 template 名/path 的
// .py 文件，返回 mtime 最新的那个。
// 返回值: (路径, 代码内容, ModTime, 找到?)
func loadLatestDiskStrategySource(inst *StrategyInstance, row *models.StrategyInstance) (string, string, time.Time, bool) {
	candidates := make([]string, 0, 2)
	names := make([]string, 0, 3)
	if row != nil {
		if p := strings.TrimSpace(row.Template.Path); p != "" && !strings.HasPrefix(strings.ToLower(p), "db://") {
			candidates = append(candidates, p)
		}
		if n := strings.TrimSpace(row.Template.Name); n != "" {
			names = append(names, n, strings.ReplaceAll(n, " ", "_")+".py")
		}
	}
	if inst != nil {
		if p := strings.TrimSpace(inst.Path); p != "" && !strings.HasPrefix(strings.ToLower(p), "db://") {
			candidates = append(candidates, p)
			names = append(names, filepath.Base(p))
		}
	}

	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	if absDir, err := filepath.Abs(strategiesDir); err == nil {
		entries, _ := os.ReadDir(absDir)
		for _, want := range names {
			want = strings.ToLower(strings.TrimSpace(filepath.Base(want)))
			if want == "" || want == "." {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := strings.ToLower(e.Name())
				if name == want || strings.HasPrefix(name, strings.TrimSuffix(want, ".py")) {
					candidates = append(candidates, filepath.Join(absDir, e.Name()))
				}
			}
		}
	}

	seen := map[string]struct{}{}
	bestPath := ""
	bestCode := ""
	bestAt := time.Time{}
	for _, p := range candidates {
		abs, err := resolveStrategyPath(p)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		if strings.Contains(filepath.ToSlash(abs), "/_runtime/") {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		code := strings.TrimSpace(string(b))
		if code == "" {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if bestCode == "" || info.ModTime().After(bestAt) {
			bestPath = abs
			bestCode = code
			bestAt = info.ModTime()
		}
	}
	if bestCode != "" {
		return bestPath, bestCode, bestAt, true
	}
	return "", "", time.Time{}, false
}

func resolveStrategySource(inst *StrategyInstance, row *models.StrategyInstance) (string, string, *uint, error) {
	if row == nil {
		return "", "", nil, fmt.Errorf("missing strategy row")
	}
	if row.StrategyVersionID != nil && row.StrategyVersion != nil {
		code := strings.TrimSpace(row.StrategyVersion.Code)
		path := strings.TrimSpace(row.StrategyVersion.Path)
		if code != "" {
			if path == "" {
				path = fmt.Sprintf("db://strategy_version/%d", row.StrategyVersion.ID)
			}
			return code, path, row.StrategyVersionID, nil
		}
	}

	dbCode := strings.TrimSpace(row.Template.Code)
	code := dbCode
	sourcePath := strings.TrimSpace(row.Template.Path)
	if sourcePath == "" && row.Template.ID > 0 {
		sourcePath = fmt.Sprintf("db://template/%d", row.Template.ID)
	}
	// 决定 DB code 还是磁盘 code 用：
	// 比较 template.UpdatedAt（DB 改动时间）vs 磁盘文件 ModTime，谁新用谁。
	// 这样修 disk 覆盖 DB 的旧坑：用户在 UI 粘新代码 → DB UpdatedAt 刷新 →
	// DB 比磁盘新 → 用 DB。反过来，直接编辑 disk 文件，disk mtime 刷新 → 用 disk。
	if diskPath, diskCode, diskMTime, ok := loadLatestDiskStrategySource(inst, row); ok {
		dbUpdatedAt := row.Template.UpdatedAt
		useDisk := false
		if dbCode == "" {
			// DB 没代码 → 只能用 disk
			useDisk = true
		} else if !diskMTime.IsZero() && diskMTime.After(dbUpdatedAt) {
			// disk 比 DB 新
			useDisk = true
		}
		if useDisk {
			sourcePath = diskPath
			code = diskCode
			// 用 disk 时把它反写回 DB template，保持 DB 是 source-of-truth
			if row.Template.ID > 0 && row.StrategyVersionID == nil && diskCode != dbCode {
				_ = database.DB.Model(&models.StrategyTemplate{}).Where("id = ?", row.Template.ID).
					Updates(map[string]interface{}{"code": diskCode, "path": diskPath, "updated_at": time.Now()}).Error
			}
		}
		// 否则保持用 DB code，不动磁盘。
	}
	if code == "" {
		return "", "", row.StrategyVersionID, fmt.Errorf("current strategy code is empty")
	}
	return code, sourcePath, row.StrategyVersionID, nil
}

func sanitizeStrategyRuntimeCode(code string) string {
	if strings.TrimSpace(code) == "" {
		return code
	}
	oldDX := "dx = np.where((plus_di + minus_di) == 0, 0, 100 * np.abs(plus_di - minus_di) / (plus_di + minus_di))"
	newDX := "di_sum = plus_di + minus_di\n    with np.errstate(divide=\"ignore\", invalid=\"ignore\"):\n        dx = np.divide(\n            100 * np.abs(plus_di - minus_di),\n            di_sum,\n            out=np.zeros_like(di_sum),\n            where=di_sum != 0,\n        )\n    dx = np.nan_to_num(dx, nan=0.0, posinf=0.0, neginf=0.0)"
	code = strings.ReplaceAll(code, oldDX, newDX)
	return code
}

func parseSymbolsValue(v interface{}) []string {
	out := make([]string, 0)
	switch t := v.(type) {
	case []string:
		for _, s := range t {
			if x := strings.TrimSpace(s); x != "" {
				out = append(out, x)
			}
		}
	case []interface{}:
		for _, it := range t {
			if s, ok := it.(string); ok {
				if x := strings.TrimSpace(s); x != "" {
					out = append(out, x)
				}
			}
		}
	case string:
		for _, p := range strings.Split(t, ",") {
			if x := strings.TrimSpace(p); x != "" {
				out = append(out, x)
			}
		}
	}
	seen := map[string]struct{}{}
	dedup := make([]string, 0, len(out))
	for _, s := range out {
		k := strings.ToUpper(strings.TrimSpace(s))
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		dedup = append(dedup, s)
	}
	if len(dedup) > 20 {
		dedup = dedup[:20]
	}
	return dedup
}

// feedHasSymbol reports whether sym is in the instance's LIVE feed set — the
// authoritative "what are we trading now" under dynamic rotation, which hot-adds
// symbols that are deliberately absent from the static config seed list.
func feedHasSymbol(inst *StrategyInstance, sym string) bool {
	if inst == nil {
		return false
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	for _, s := range inst.feedSymbols {
		if exchange.NormalizeSymbol(s) == sym {
			return true
		}
	}
	return false
}

func isAllowedSymbol(inst *StrategyInstance, symbol string) bool {
	if inst == nil {
		return false
	}
	sym := exchange.NormalizeSymbol(symbol)
	if sym == "" {
		return false
	}
	if xs := parseSymbolsValue(inst.Config()["symbols"]); len(xs) > 0 {
		for _, s := range xs {
			if exchange.NormalizeSymbol(s) == sym {
				return true
			}
		}
		// 动态币池：config.symbols 只是启动种子；轮换热加入的币在 feedSymbols
		// 里而不在静态配置里。此前这里直接 return false，导致轮换币的开仓信号
		// 被静默丢弃（"择优胜出"却永不下单）。
		return feedHasSymbol(inst, sym)
	}
	if raw, ok := inst.Config()["symbol"].(string); ok && strings.TrimSpace(raw) != "" {
		if exchange.NormalizeSymbol(raw) == sym {
			return true
		}
		return feedHasSymbol(inst, sym)
	}
	return true
}

// isBlacklistedSymbol 返回 true 表示该 symbol 在 inst.Config()["symbol_blacklist"]
// 黑名单里。黑名单独立于 symbols 白名单，用于"白名单内但历史亏钱"的标的。
// 配置形式：JSON 数组 ["SAHARAUSDT","XYZUSDT"] 或逗号分隔字符串。
func isBlacklistedSymbol(inst *StrategyInstance, symbol string) bool {
	if inst == nil {
		return false
	}
	xs := parseSymbolsValue(inst.Config()["symbol_blacklist"])
	if len(xs) == 0 {
		return false
	}
	sym := exchange.NormalizeSymbol(symbol)
	if sym == "" {
		return false
	}
	for _, s := range xs {
		if exchange.NormalizeSymbol(s) == sym {
			return true
		}
	}
	return false
}

// isAllowedSide 返回 true 表示该方向在 inst.Config()["allowed_sides"] 白名单里。
// 配置缺省（空数组或不存在）→ buy/sell 都放行（保持向后兼容）。
// 60 天分析显示 long 严重亏钱、short 净盈利，可用 ["sell"] 关掉所有 long。
func isAllowedSide(inst *StrategyInstance, side string) bool {
	if inst == nil {
		return false
	}
	raw, ok := inst.Config()["allowed_sides"]
	if !ok || raw == nil {
		return true
	}
	var sides []string
	switch v := raw.(type) {
	case []interface{}:
		for _, it := range v {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				sides = append(sides, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	case []string:
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				sides = append(sides, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	case string:
		for _, p := range strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == ' ' || r == ';' || r == '|'
		}) {
			if strings.TrimSpace(p) != "" {
				sides = append(sides, strings.ToLower(strings.TrimSpace(p)))
			}
		}
	}
	if len(sides) == 0 {
		return true
	}
	target := strings.ToLower(strings.TrimSpace(side))
	// 同义词归一：long/buy 和 short/sell 互相等价
	if target == "long" {
		target = "buy"
	} else if target == "short" {
		target = "sell"
	}
	for _, s := range sides {
		canonical := s
		if canonical == "long" {
			canonical = "buy"
		} else if canonical == "short" {
			canonical = "sell"
		}
		if canonical == target {
			return true
		}
	}
	return false
}

func getNumber(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f
		}
		return 0
	default:
		return 0
	}
}

func getString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func getBool(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "true" || s == "1" || s == "yes" || s == "y" || s == "on"
	default:
		return false
	}
}

func clampOrderAmount(inst *StrategyInstance, requested float64) float64 {
	if inst == nil {
		return 0
	}
	amt := requested
	if amt <= 0 {
		amt = getNumber(inst.Config()["trade_amount"])
	}
	if amt <= 0 {
		return 0
	}
	maxAmt := getNumber(inst.Config()["max_order_amount"])
	if maxAmt <= 0 {
		maxAmt = getNumber(inst.Config()["max_trade_amount"])
	}
	if maxAmt > 0 && amt > maxAmt {
		amt = maxAmt
	}
	minAmt := getNumber(inst.Config()["min_order_amount"])
	if minAmt > 0 && amt < minAmt {
		return 0
	}
	return amt
}

// StrategyStatus is the runtime state of a strategy process managed by Manager.
type StrategyStatus string

const (
	StatusStarting StrategyStatus = "starting"
	StatusRunning  StrategyStatus = "running"
	StatusStopped  StrategyStatus = "stopped"
	StatusError    StrategyStatus = "error"
)

type StrategyInstance struct {
	// ID is the StrategyInstance primary key (UUID string).
	ID string `json:"id"`
	// Name is the user-visible strategy name.
	Name string `json:"name"`
	// TemplateID is the backing template family id.
	TemplateID uint `json:"template_id"`
	// StrategyVersionID is the bound immutable version id, if any.
	StrategyVersionID *uint `json:"strategy_version_id,omitempty"`
	// Path points to the python file of this strategy.
	Path             string `json:"path"`
	RuntimePath      string `json:"runtime_path"`
	RuntimeGenerated bool   `json:"runtime_generated"`
	RuntimeKeep      bool   `json:"runtime_keep"`
	// config 是当前生效的用户参数快照，通过 Config() 读取。
	//
	// 为什么不是裸 `Config map[string]interface{}`（2026-09-16 改）：
	// 这个 map 会被自动化路径（claude_cron / DeepSeek 优化器 / bot endpoint）
	// 在策略**运行中**整体替换，而入场/出场/信号/下单各路径有 110+ 处无锁读。
	// 裸字段整体替换是真实的 data race，后果不是"读到旧值"而是 Go runtime
	// 直接 "concurrent map read and map write" fatal error 打挂整个后端进程。
	//
	// 用 atomic.Pointer 而不是加互斥锁：读方**无锁**，因此不存在锁序问题
	// （inst.mu / orderMu 已在多处被持有，再加一把锁极易造成死锁）。
	// 写方只换指针，读方拿到的永远是某个完整一致的快照。
	//
	// 这也是同日移除 "cannot update config while strategy is running" 那道
	// guard 的前提。留着 guard 的代价是实测出来的：2026-07-17 起 claude_cron
	// 的 1589 次改动全被弹掉（strategy_audit_logs: action=patch_config success=0）。
	//
	// 约定：调用方**只读**，不得修改 Config() 返回的 map。
	config atomic.Pointer[map[string]interface{}]
	// Status is the process state.
	Status StrategyStatus `json:"status"`
	// OwnerID is the user who owns this running instance.
	OwnerID uint `json:"owner_id"`
	// CreatedAt is when this in-memory instance was created.
	CreatedAt time.Time `json:"created_at"`

	// cmd/stdout are the managed python process and pipes.
	cmd    *exec.Cmd
	stdout io.ReadCloser

	// mu guards process state and pipes.
	mu sync.Mutex
	// orderMu guards inflight open order counters and concurrency checks.
	orderMu       sync.Mutex
	inflightOpen  int
	lastSkipLogAt map[string]time.Time
	invalidSymbol map[string]time.Time
	// leverageSet 记录本进程周期内已对齐到交易所的 per-symbol 杠杆。
	// 交易所侧杠杆是粘性的（手动改过/新币默认 20X 会一直生效），必须显式 SetLeverage 对齐。
	leverageSet map[string]int
	// hub is the websocket broadcaster for UI updates.
	hub *ws.Hub
	// exchange is the exchange implementation (mock/binance/etc.).
	exchange exchange.Exchange
	// signal batching for per-kline selection
	sigMu           sync.Mutex
	pendingSignals  map[string][]bus.SignalMessage
	signalBatchWait time.Duration

	mgr *Manager

	redisCancel context.CancelFunc
	bootID      string
	startedAt   time.Time
	lastHB      time.Time
	stopping    bool
	// killedByUs：我们主动 Kill 子进程（停止/重启）时置 true，由 exit handler 读后清零。
	// 用来区分"主动停"与"进程自己崩溃"，避免每次 routine 改配置重启都误报 signal:killed。
	killedByUs       bool
	restarting       bool
	resync           bool
	resyncLogBootID  string
	resyncNextAt     time.Time
	resyncBackoff    time.Duration
	stateReadySeen   bool
	heartbeatSeen    bool
	feedSymbols      []string
	candleStops      map[string]func()
	candlePubCount   map[string]int
	candleRxCount    map[string]int
	lastCandleClose  map[string]float64
	lastCandleAt     map[string]time.Time
	lastCandleSeenAt map[string]time.Time
	candleEvent      map[string]string
	candleEventInfo  map[string]string
	candleEventAt    map[string]time.Time
	tpslCancel       map[string]context.CancelFunc
	optimizeRunning  bool
	lastOptimizeAt   time.Time
}

// Config 返回当前生效的用户参数快照，可能为 nil（尚未 setConfig）。
//
// 读方无锁 —— 这是选 atomic.Pointer 而非互斥锁的全部理由：inst.mu / orderMu
// 已经在 110+ 处被持有，再插一把锁会引入难以穷尽的锁序问题。
// 返回的 map 视为只读，调用方不得修改。
func (inst *StrategyInstance) Config() map[string]interface{} {
	if inst == nil {
		return nil
	}
	if p := inst.config.Load(); p != nil {
		return *p
	}
	return nil
}

// setConfig 整体替换配置快照。写方只换指针，不与任何读方竞争。
func (inst *StrategyInstance) setConfig(cfg map[string]interface{}) {
	if inst == nil {
		return
	}
	inst.config.Store(&cfg)
}

// MarshalJSON 让非导出的原子快照仍以 "config" 出现在 API 响应里。
// 前端、ListStrategies 的调用方都按这个字段名取值，改成原子存储后
// 必须显式补回来，否则 /api/strategies 会静默少一个字段。
func (inst *StrategyInstance) MarshalJSON() ([]byte, error) {
	type alias StrategyInstance // 避免递归调用 MarshalJSON
	return json.Marshal(struct {
		*alias
		Config map[string]interface{} `json:"config"`
	}{(*alias)(inst), inst.Config()})
}

// Manager manages lifecycle of all strategy instances and coordinates exchange access.
type Manager struct {
	// instances keeps in-memory runtime state keyed by strategy instance id.
	instances map[string]*StrategyInstance
	mu        sync.RWMutex
	// hub broadcasts runtime events (logs/orders/candles/backtest updates) to frontend.
	hub *ws.Hub
	// exchange is the global exchange connector used by all strategies.
	exchange exchange.Exchange

	redisBus *bus.RedisBus

	sigMu           sync.Mutex
	pendingSignals  map[string][]bus.SignalMessage
	signalBatchWait time.Duration

	quickCloseMu sync.Mutex
	quickCloseAt map[string]time.Time

	// tpslLocks serializes TP/SL placement per (owner_id, symbol) so the
	// periodic guard tick and the entry-side placement path can't both
	// cancel-and-replace algo orders at the same time. See #10 audit.
	tpslMu    sync.Mutex
	tpslLocks map[string]*sync.Mutex

	notifier RuntimeNotifier

	orderCh chan orderReq
	startCh chan string
	stopCh  chan stopReq

	// wsGuard: 标记价流触发的守护加速状态（strategy_ws_guard.go）。
	wsGuard wsGuardState
	// pyramidAdds: 赢家金字塔加仓计数 uid|SYMBOL -> 已加次数（strategy_pyramid.go）。
	pyramidMu   sync.Mutex
	pyramidAdds map[string]int
}

func (m *Manager) ReleaseOpenSlot(strategyID string) {
	if m == nil || strings.TrimSpace(strategyID) == "" {
		return
	}
	m.mu.RLock()
	rb := m.redisBus
	m.mu.RUnlock()
	if rb == nil {
		return
	}
	_, _ = rb.ReleaseOpenSlot(context.Background(), strategyID)
}

func (m *Manager) StopPositionTPStopMonitor(strategyID string, symbol string) {
	if m == nil || strings.TrimSpace(strategyID) == "" || strings.TrimSpace(symbol) == "" {
		return
	}
	m.mu.RLock()
	inst := m.instances[strategyID]
	m.mu.RUnlock()
	if inst == nil {
		return
	}
	m.stopPositionTPStopMonitor(inst, symbol)
}

// staleGracePeriod 是"新仓宽限期":USDM 无实时成交流,平仓全靠 2s 轮询;刚开的仓可能
// 只是交易所持仓查询还没反映,宽限内不判 stale,避免 open→stale→收养 每 2s churn。
const staleGracePeriod = 30 * time.Second

// fetchStaleRealizedPnL 汇总该仓生命周期内币安 USDM 的真实 REALIZED_PNL —— 交易所侧
// (TP/SL/强平/手工)平仓的真实盈亏,否则会被按开仓价记成持平(0)。取不到返回 (0,false),
// 调用方保留旧的持平回退。注:共享账户下同 symbol 并发交易可能串账,属已知取舍。
func (m *Manager) fetchStaleRealizedPnL(ex exchange.Exchange, ownerID uint, symKey string, openTime, now time.Time) (float64, bool) {
	bex, ok := ex.(*exchange.BinanceExchange)
	if !ok || openTime.IsZero() {
		return 0, false
	}
	// 必须翻页。本窗口是 [开仓时间-1m, 现在]，持仓越久事件越多：账户实测 ~490 条/天，
	// 一笔拿过两天的仓就跨过 1000 条上限。单页会拿到**最早**那批（币安按时间升序返回），
	// 真正的平仓事件根本不在里面 —— 要么 found=false 让调用方按"持平"记 0（这笔亏损
	// 就凭空消失，正是 strategy_positions 里那批空壳行），要么匹配到同 symbol 的另一笔
	// 旧结算，金额还是错的。两种都不可接受。
	//
	// complete=false 时退回 (0,false) 而不是拿残缺集去凑：宁可让调用方保留原有回退，
	// 也不能编一个看似有据的错数。
	events, complete, err := bex.USDMIncomeHistoryAll(ownerID, openTime.Add(-time.Minute), now, 200*time.Millisecond)
	if err != nil || !complete {
		return 0, false
	}
	var sum float64
	var found bool
	for _, ev := range events {
		if ev.IncomeType == "REALIZED_PNL" && exchange.NormalizeSymbol(ev.Symbol) == symKey {
			sum += ev.Income
			found = true
		}
	}
	return sum, found
}

// mayAdoptUnclaimedPosition reports whether inst is live enough to be handed
// ownership of an exchange net position that no open DB row claims.
//
// Only running/starting instances qualify. A stopped/error instance — or one no
// longer in the in-memory registry (inst == nil, e.g. after RemoveStrategy) —
// must not adopt: latestOrderBySymbol is built from the owner's last 500 orders
// with no liveness filter, so a retired strategy otherwise keeps a permanent
// claim on every symbol it last traded. On a shared exchange account that turns
// somebody else's net position into a phantom row under the dead strategy
// (2026-09-08: qt-breakout-follow-v2 / qt-fade-short-v2 kept growing rows for
// 9-10 days after being stopped).
func mayAdoptUnclaimedPosition(inst *StrategyInstance) bool {
	if inst == nil {
		return false
	}
	return inst.Status == StatusRunning || inst.Status == StatusStarting
}

// adoptUnclaimedExchangePositions records exchange net positions that no open
// strategy_positions row claims, attributing each to the strategy that OPENED
// it — provided that strategy belongs to this owner and is still live.
//
// The opener, not the last actor: see the ruling in position_opener.go. Under a
// shared exchange account the last order on a symbol is routinely a CLOSE leg
// from a different strategy than the one that built the position, and crediting
// it makes the per-strategy PnL a function of execution order rather than of
// what the strategy did.
//
// Declining to adopt does not leave a naked position: TP/SL live as native
// exchange conditional orders rather than in the DB row, and the positions API
// renders exchange positions directly. The decline is logged so the orphan is
// visible instead of silent.
//
// loadEntryOrders is a lazy loader so the common tick — nothing unclaimed —
// costs no extra query on this 2s loop.
func adoptUnclaimedExchangePositions(
	ownerID uint,
	now time.Time,
	activePositions map[string]exchange.Position,
	loadEntryOrders func() map[string][]models.StrategyOrder,
	instLookup map[string]*StrategyInstance,
	countedSymbols map[string]struct{},
	countByStrategy map[string]int64,
) {
	var entryOrders map[string][]models.StrategyOrder
	for symKey, pos := range activePositions {
		if _, ok := countedSymbols[symKey]; ok {
			continue
		}
		if entryOrders == nil {
			entryOrders = loadEntryOrders()
		}
		ord, ok := PositionOpener(entryOrders[symKey], pos.Direction, pos.OpenTime)
		if !ok {
			logger.Warnf("[REDIS OPEN COUNT] skip adoption: opener unknown owner=%d symbol=%s direction=%s amount=%v", ownerID, pos.Symbol, pos.Direction, pos.Amount)
			continue
		}
		if ord.OwnerID != ownerID {
			// 开仓方挂在另一个 app owner 名下(共享交易所账户)。这一仓归它,不归本
			// owner 的任何策略 —— 等外层循环走到开仓方那个 owner 时再收养。不打日志:
			// 这是每 2s 都会命中的正常分支,不是异常。
			continue
		}
		if !mayAdoptUnclaimedPosition(instLookup[strings.TrimSpace(ord.StrategyID)]) {
			logger.Warnf("[REDIS OPEN COUNT] skip adoption: strategy not live owner=%d strategy=%s symbol=%s amount=%v", ownerID, ord.StrategyID, pos.Symbol, pos.Amount)
			continue
		}
		countByStrategy[ord.StrategyID]++
		countedSymbols[symKey] = struct{}{}
		_ = database.DB.Create(&models.StrategyPosition{
			StrategyID:   ord.StrategyID,
			StrategyName: ord.StrategyName,
			OwnerID:      ownerID,
			Exchange:     pos.ExchangeName,
			Symbol:       pos.Symbol,
			Direction:    pos.Direction, // 缺 Direction → 收养的空头平仓被当加仓、DB量涨、PnL 记0(CR P1)
			Amount:       pos.Amount,
			AvgPrice:     pos.Price,
			Status:       "open",
			OpenTime:     pos.OpenTime,
			// 收养行的 realized_pn_l 从来不是"这笔打平",而是"还没有平仓腿"。
			// 显式标 unknown,让它一出生就能被聚合排除掉(台账 #122 三态)。
			PnLSource: "unknown",
			UpdatedAt: now,
		}).Error
	}
}

func (m *Manager) SyncRedisOpenCountsFromExchange(ctx context.Context) {
	if m == nil {
		return
	}
	syncOnce := func() {
		m.mu.RLock()
		rb := m.redisBus
		ex := m.exchange
		snap := make([]*StrategyInstance, 0, len(m.instances))
		for _, inst := range m.instances {
			snap = append(snap, inst)
		}
		m.mu.RUnlock()
		if rb == nil || ex == nil {
			return
		}

		// 开仓腿是账户级的(共享账户下开仓方常常挂在别的 owner 名下),所以按 tick
		// 读一次、跨 owner 复用;没有无主净仓的 tick 一次都不读。
		var entryOrdersOnce map[string][]models.StrategyOrder
		loadEntryOrders := func() map[string][]models.StrategyOrder {
			if entryOrdersOnce == nil {
				entryOrdersOnce = LoadRecentEntryOrdersBySymbol()
			}
			return entryOrdersOnce
		}

		byOwner := map[uint][]*StrategyInstance{}
		for _, inst := range snap {
			if inst == nil || inst.OwnerID == 0 || strings.TrimSpace(inst.ID) == "" {
				continue
			}
			byOwner[inst.OwnerID] = append(byOwner[inst.OwnerID], inst)
		}

		for ownerID, insts := range byOwner {
			activePositions := map[string]exchange.Position{}
			if ps, err := ex.FetchPositions(ownerID, "active"); err == nil {
				for _, p := range ps {
					if math.Abs(p.Amount) <= 0 {
						continue
					}
					key := exchange.NormalizeSymbol(p.Symbol)
					if key == "" {
						continue
					}
					activePositions[key] = p
				}
			} else {
				logger.Errorf("[REDIS OPEN COUNT] fetch positions failed owner=%d err=%v", ownerID, err)
				continue
			}

			var openRows []models.StrategyPosition
			if err := database.DB.Where("owner_id = ? AND status = ?", ownerID, "open").Find(&openRows).Error; err != nil {
				logger.Errorf("[REDIS OPEN COUNT] load open rows failed owner=%d err=%v", ownerID, err)
				continue
			}

			var recentOrders []models.StrategyOrder
			_ = database.DB.Where("owner_id = ?", ownerID).Order("requested_at desc").Limit(500).Find(&recentOrders).Error
			latestOrderBySymbol := map[string]models.StrategyOrder{}
			for _, ord := range recentOrders {
				symKey := exchange.NormalizeSymbol(ord.Symbol)
				if symKey == "" {
					continue
				}
				if _, ok := latestOrderBySymbol[symKey]; ok {
					continue
				}
				if strings.TrimSpace(ord.StrategyID) == "" {
					continue
				}
				latestOrderBySymbol[symKey] = ord
			}

			countByStrategy := map[string]int64{}
			countedSymbols := map[string]struct{}{}
			now := time.Now()
			// instLookup 让 stale-position 自动关闭路径能拿到 hub / Name / exchange，
			// 用于即时广播 + Telegram 通知，缩小 "DB closed 但前端还看到 open" 的窗口。
			instLookup := map[string]*StrategyInstance{}
			for _, in := range insts {
				if in != nil {
					instLookup[in.ID] = in
				}
			}
			for _, row := range openRows {
				symKey := exchange.NormalizeSymbol(row.Symbol)
				if symKey == "" {
					continue
				}
				if _, ok := activePositions[symKey]; !ok {
					// 新仓宽限:刚开的仓可能只是交易所持仓查询还没反映,别误判平仓。
					if !row.OpenTime.IsZero() && now.Sub(row.OpenTime) < staleGracePeriod {
						if strings.TrimSpace(row.StrategyID) != "" {
							countByStrategy[row.StrategyID]++
							countedSymbols[symKey] = struct{}{}
						}
						continue
					}
					// 交易所侧平仓无实时成交价,过去拿开仓价当平仓价 → 记 0(持平)。这里拉真实
					// REALIZED_PNL 回填,让持仓行 + 量化卡的 realized PnL 反映真盈亏。
					realizedPnL, closeKnown := m.fetchStaleRealizedPnL(ex, ownerID, symKey, row.OpenTime, now)
					qtyClosed := math.Abs(row.Amount)
					impliedClose := row.AvgClosePrice
					if impliedClose <= 0 {
						impliedClose = row.AvgPrice
					}
					if closeKnown && qtyClosed > 0 && row.AvgPrice > 0 {
						if strings.EqualFold(row.Direction, "short") {
							impliedClose = row.AvgPrice - realizedPnL/qtyClosed
						} else {
							impliedClose = row.AvgPrice + realizedPnL/qtyClosed
						}
					}
					closeTime := row.CloseTime
					if closeTime.IsZero() {
						closeTime = now
					}
					updates := map[string]interface{}{
						"amount":     0,
						"status":     "closed",
						"updated_at": now,
					}
					if row.CloseTime.IsZero() {
						updates["close_time"] = now
					}
					if closeKnown {
						updates["realized_pn_l"] = realizedPnL
						updates["avg_close_price"] = impliedClose
						updates["closed_qty"] = qtyClosed
						updates["pnl_source"] = "exchange_income"
					} else {
						// 拉不到 REALIZED_PNL 就一个字都不写盈亏 —— 这是对的,不能编。
						// 但过去连"没查到"这件事也不写,行以 realized_pn_l=0 收尾,
						// 和"这笔真的打平"在库里长得一模一样(台账 #122)。标一下,
						// 让下游能把缺失值排除掉而不是当 0 平均进去。
						updates["pnl_source"] = "unknown"
					}
					_ = database.DB.Model(&models.StrategyPosition{}).Where("id = ?", row.ID).Updates(updates).Error
					if strings.TrimSpace(row.StrategyID) != "" {
						_, _ = rb.ReleaseOpenSlot(ctx, row.StrategyID)
					}
					logger.Infof("[REDIS OPEN COUNT] auto close stale open position owner=%d strategy=%s symbol=%s", ownerID, row.StrategyID, row.Symbol)
					// 交易所侧平仓（TP/SL 在币安成交、强平、手工平）不走 closeUSDMPosition，
					// 残留的另一腿联动委托需在此清理：主路径 closePosition=true 的单币安通常
					// 自动过期，但 -4120 回退路径的按数量 algo 单不会。只撤本仓位在
					// strategy_orders 登记过的联动 TP/SL，不碰用户手挂委托；撤已过期单仅
					// 无害报错（标记 cancel_failed）。
					if found, canceled, cErr := m.cancelLinkedTPSLOrders(ownerID, row.StrategyID, row.Symbol); found > 0 {
						logger.Infof("[REDIS OPEN COUNT] external close cleanup owner=%d symbol=%s linked_tpsl found=%d canceled=%d err=%v", ownerID, row.Symbol, found, canceled, cErr)
					}

					// 即时通知：交易所 TP/SL 触发 / 用户在交易所手工平仓时，
					// 这里是后端第一次"知道"的地方。早一秒把 closed 状态推
					// 给前端 + 触发器外部通知（Telegram 等）。
					inst := instLookup[strings.TrimSpace(row.StrategyID)]
					side := "sell"
					if strings.EqualFold(row.Direction, "short") {
						side = "buy"
					}
					exitPrice := impliedClose
					if exitPrice <= 0 {
						exitPrice = row.AvgPrice
					}
					closeRow := row
					closeRow.Amount = 0
					closeRow.Status = "closed"
					closeRow.CloseTime = closeTime
					closeRow.UpdatedAt = now
					closeRow.PnLSource = "unknown"
					if closeKnown {
						closeRow.RealizedPnL = realizedPnL
						closeRow.AvgClosePrice = impliedClose
						closeRow.ClosedQty = qtyClosed
						closeRow.PnLSource = "exchange_income"
					}
					if inst != nil && inst.hub != nil {
						inst.hub.BroadcastJSON(map[string]interface{}{
							"type":   "position",
							"reason": "external_close_detected",
							"data":   closeRow,
						})
					} else if m.hub != nil {
						m.hub.BroadcastJSON(map[string]interface{}{
							"type":   "position",
							"reason": "external_close_detected",
							"data":   closeRow,
						})
					}
					exchangeName := ""
					strategyName := row.StrategyName
					if inst != nil {
						strategyName = inst.Name
						if inst.exchange != nil {
							exchangeName = inst.exchange.GetName()
						}
					}
					if exchangeName == "" {
						exchangeName = row.Exchange
					}
					metrics := BuildTradeCloseMetricsFromPosition(&closeRow, closeRow.ClosedQty, exitPrice, closeTime)
					m.NotifyExternalTradeClosed(ownerID, row.StrategyID, strategyName, exchangeName, row.Symbol, side, row.Amount, exitPrice, "closed", "external_close_detected", metrics)
					continue
				}
				if strings.TrimSpace(row.StrategyID) != "" {
					countByStrategy[row.StrategyID]++
					countedSymbols[symKey] = struct{}{}
				}
			}

			adoptUnclaimedExchangePositions(ownerID, now, activePositions, loadEntryOrders, instLookup, countedSymbols, countByStrategy)

			pendingCutoff := now.Add(-2 * time.Minute)
			for symKey, ord := range latestOrderBySymbol {
				if _, ok := countedSymbols[symKey]; ok {
					continue
				}
				if strings.TrimSpace(ord.StrategyID) == "" {
					continue
				}
				st := strings.ToLower(strings.TrimSpace(ord.Status))
				if st != "requested" && st != "new" && st != "partially_filled" {
					continue
				}
				if ord.RequestedAt.Before(pendingCutoff) {
					continue
				}
				countByStrategy[ord.StrategyID]++
				countedSymbols[symKey] = struct{}{}
			}
			for _, inst := range insts {
				_ = rb.SetOpenCount(ctx, inst.ID, countByStrategy[inst.ID], 6*time.Hour)
			}
		}
	}

	syncOnce()
	// 2s 间隔：把交易所 TP/SL 成交的发现窗口从 10s 压到 ~2s。EnsureUserDataStream
	// 过去对 usdm 直接 return，期货完全没有成交回报流，这条同步链是发现服务器侧
	// 平仓的唯一渠道；现在 ORDER_TRADE_UPDATE 已接通，它退化为兜底（流断开、
	// 或成交对不上 client_order_id 时仍然靠它）。每个 owner 一次 FetchPositions
	// REST 调用，30 个用户 * 30 次/min = 900/min，远低于 2400/min 的 IP 上限。
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncOnce()
		}
	}
}

func emitStrategyLog(inst *StrategyInstance, level string, msg string) {
	if inst == nil || strings.TrimSpace(msg) == "" {
		return
	}
	level = strings.ToLower(strings.TrimSpace(level))
	logPrefix := fmt.Sprintf("[STRATEGY LOG] id=%s name=%s owner=%d level=%s msg=%s", inst.ID, inst.Name, inst.OwnerID, firstNonEmpty(level, "info"), msg)
	switch level {
	case "error":
		logger.Errorf("%s", logPrefix)
	case "warn", "warning":
		logger.Warnf("%s", logPrefix)
	case "debug":
		logger.Debugf("%s", logPrefix)
	default:
		logger.Infof("%s", logPrefix)
	}
	if database.DB != nil {
		_ = database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      level,
			Message:    msg,
			CreatedAt:  time.Now(),
		}).Error
	}
	if inst.hub != nil {
		inst.hub.BroadcastJSON(map[string]interface{}{
			"type": "log",
			"data": msg,
			"id":   inst.ID,
		})
	}
}

// BacktestResult is returned by synchronous backtests and stored in Backtest.Result.
type BacktestResult struct {
	TotalTrades    int           `json:"total_trades"`
	TotalProfit    float64       `json:"total_profit"`
	TotalFees      float64       `json:"total_fees"`
	ReturnRate     float64       `json:"return_rate"`
	InitialBalance float64       `json:"initial_balance"`
	FinalBalance   float64       `json:"final_balance"`
	EquityCurve    []EquityPoint `json:"equity_curve"`
}

type EquityPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Equity    float64   `json:"equity"`
}

// NewManager constructs a Manager.
//
// Typical usage (see cmd/main.go):
// - Create Hub and run it in a goroutine
// - Create Exchange implementation (Mock/Binance)
// - NewManager(hub, exchange)
func NewManager(hub *ws.Hub, ex exchange.Exchange) *Manager {
	return &Manager{
		instances:       make(map[string]*StrategyInstance),
		hub:             hub,
		exchange:        ex,
		pendingSignals:  make(map[string][]bus.SignalMessage),
		signalBatchWait: 500 * time.Millisecond,
		quickCloseAt:    make(map[string]time.Time),
		tpslLocks:       make(map[string]*sync.Mutex),
		orderCh:         make(chan orderReq, 256),
		startCh:         make(chan string, 128),
		stopCh:          make(chan stopReq, 128),
		pyramidAdds:     make(map[string]int),
	}
}

// lockTPSL acquires a per-(uid, symbol) mutex so that the periodic guard
// tick and the entry placement path serialize their cancel + place ops
// against each other. Returns the unlock function (suitable for defer).
func (m *Manager) lockTPSL(uid uint, symbol string) func() {
	if m == nil {
		return func() {}
	}
	key := fmt.Sprintf("%d|%s", uid, strings.ToUpper(strings.TrimSpace(symbol)))
	m.tpslMu.Lock()
	if m.tpslLocks == nil {
		m.tpslLocks = map[string]*sync.Mutex{}
	}
	mu, ok := m.tpslLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		m.tpslLocks[key] = mu
	}
	m.tpslMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

type orderReq struct {
	inst       *StrategyInstance
	symbol     string
	side       string
	amount     float64
	price      float64
	takeProfit float64
	stopLoss   float64
	signalID   string
	confidence float64
	// signalPrice = 信号生成时策略用的评估价,只用于在成交价上重锚 tp/sl
	// (reanchorTPSLToFill)。与上面的 price 是两回事:price 是下单参考价。
	signalPrice float64
}

type stopReq struct {
	id    string
	force bool
}

func (m *Manager) StartWorkers() {
	go m.runOrderWorker()
	go m.runStartWorker()
	go m.runStopWorker()
	go m.runAutoOptimizeWorker()
}

func (m *Manager) enqueueOrderForInstance(inst *StrategyInstance, symbol string, side string, amount float64, price float64, takeProfit float64, stopLoss float64, signalID string, confidence float64, signalPrice float64) {
	if inst == nil || symbol == "" || side == "" {
		return
	}
	select {
	case m.orderCh <- orderReq{inst: inst, symbol: symbol, side: side, amount: amount, price: price, takeProfit: takeProfit, stopLoss: stopLoss, signalID: signalID, confidence: confidence, signalPrice: signalPrice}:
	default:
		emitStrategyLog(inst, "error", "Order queue is full, dropping order request")
	}
}

func (m *Manager) SetRedisBus(b *bus.RedisBus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redisBus = b
}

// AddStrategy registers an in-memory strategy instance. It does not start the process.
//
// Typical usage:
// - When creating a StrategyInstance in DB (API CreateStrategy)
// - When syncing from DB on startup (SyncFromDB)
func (m *Manager) AddStrategy(id, name, path string, ownerID uint, templateID uint, strategyVersionID *uint, config map[string]interface{}) *StrategyInstance {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := &StrategyInstance{
		ID:                id,
		Name:              name,
		TemplateID:        templateID,
		StrategyVersionID: strategyVersionID,
		Path:              path,
		Status:            StatusStopped,
		OwnerID:           ownerID,
		CreatedAt:         time.Now(),
		hub:               m.hub,
		exchange:          m.exchange,
		mgr:               m,
	}
	inst.setConfig(config)
	m.instances[id] = inst
	return inst
}

func (m *Manager) prepareRuntimeStrategyFile(inst *StrategyInstance) (string, error) {
	if inst == nil {
		return "", fmt.Errorf("missing instance")
	}
	if database.DB == nil {
		return resolveStrategyPath(inst.Path)
	}

	var row models.StrategyInstance
	if err := database.DB.Preload("Template").Preload("StrategyVersion").Where("id = ?", inst.ID).First(&row).Error; err != nil {
		return resolveStrategyPath(inst.Path)
	}
	code, sourcePath, versionID, err := resolveStrategySource(inst, &row)
	if err != nil {
		return resolveStrategyPath(inst.Path)
	}
	inst.TemplateID = row.TemplateID
	inst.StrategyVersionID = versionID

	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	absDir, err := filepath.Abs(strategiesDir)
	if err != nil {
		return "", err
	}
	runtimeDir := filepath.Join(absDir, "_runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return "", err
	}

	if prev := strings.TrimSpace(inst.RuntimePath); prev != "" && prev != inst.Path && strings.Contains(filepath.ToSlash(prev), "/_runtime/") {
		_ = os.Remove(prev)
	}
	absPath := filepath.Join(runtimeDir, fmt.Sprintf("%s_%d.py", inst.ID, time.Now().UnixMilli()))
	code = sanitizeStrategyRuntimeCode(code)
	runtimeCode := "import os\nimport sys\nsys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), \"..\")))\n\n" + miniRedisRuntimeShim() + "\n" + code + "\n"
	if err := os.WriteFile(absPath, []byte(runtimeCode), 0o644); err != nil {
		return "", err
	}

	keep := getBool(inst.Config()["keep_runtime_file"]) || getBool(inst.Config()["debug"]) || getBool(inst.Config()["log_trace"])
	inst.RuntimePath = absPath
	inst.RuntimeGenerated = true
	inst.RuntimeKeep = keep
	if sourcePath == "" {
		sourcePath = "db_template_code"
	}
	emitStrategyLog(inst, "info", fmt.Sprintf("已生成运行脚本 source=%s runtime=%s", sourcePath, absPath))
	return absPath, nil
}

// backtestRedisRuntimeShim is the backtest-only stand-in for the live Redis
// client. Unlike miniRedisRuntimeShim (a real RESP socket client used in live
// runs), this version is backed by stdin/stdout so a backtest is a
// deterministic, dependency-free subprocess: candles arrive on stdin from the
// Go harness as {"type":"candle","data":{...}} lines, and trade signals the
// strategy publishes on its :signal: channel are translated to
// {"type":"order",...} lines on stdout, which runBacktestSimulation consumes.
func backtestRedisRuntimeShim() string {
	return `import sys
import json
import types


class MiniRedis:
    def __init__(self, host="127.0.0.1", port=6379, password="", db=0, timeout=30):
        self._sub = None

    def connect(self):
        return self

    def close(self):
        return None

    def subscribe(self, channel):
        self._sub = channel
        return None

    def psubscribe(self, pattern):
        self._sub = pattern
        return None

    def pubsub(self, *a, **k):
        return self

    def publish(self, channel, payload):
        if isinstance(channel, str) and ":signal:" in channel:
            try:
                msg = json.loads(payload) if isinstance(payload, (str, bytes, bytearray)) else payload
            except Exception:
                return 0
            sys.stdout.write(json.dumps({"type": "order", "data": msg}) + "\n")
            sys.stdout.flush()
        return 0

    def _next(self):
        line = sys.stdin.readline()
        if line == "":
            raise SystemExit(0)
        line = line.strip()
        if not line:
            return None
        try:
            obj = json.loads(line)
        except Exception:
            return None
        data = obj.get("data", obj) if isinstance(obj, dict) else obj
        return {"type": "message", "channel": self._sub, "data": json.dumps(data)}

    def read_pubsub_message(self):
        return self._next()

    def get_message(self, timeout=1.0):
        return self._next()

    def execute(self, *args):
        return None


_mod = types.ModuleType("mini_redis")
_mod.MiniRedis = MiniRedis
sys.modules.setdefault("mini_redis", _mod)
`
}

func miniRedisRuntimeShim() string {
	return "import socket\nimport types\n\nclass MiniRedis:\n    def __init__(self, host=\"127.0.0.1\", port=6379, password=\"\", db=0, timeout=30):\n        self.host = host\n        self.port = int(port)\n        self.password = password or \"\"\n        self.db = int(db or 0)\n        self.timeout = timeout\n        self.sock = None\n        self.buf = b\"\"\n\n    def connect(self):\n        self.sock = socket.create_connection((self.host, self.port), timeout=self.timeout if self.timeout else None)\n        if self.timeout:\n            self.sock.settimeout(self.timeout)\n        if self.password:\n            try:\n                self.execute(\"AUTH\", self.password)\n            except RuntimeError as e:\n                msg = str(e)\n                if \"called without any password configured\" not in msg:\n                    raise\n        if self.db:\n            self.execute(\"SELECT\", str(self.db))\n        return self\n\n    def close(self):\n        try:\n            if self.sock:\n                self.sock.close()\n        finally:\n            self.sock = None\n            self.buf = b\"\"\n\n    def _encode(self, *parts):\n        out = [f\"*{len(parts)}\\r\\n\".encode(\"utf-8\")]\n        for p in parts:\n            if p is None:\n                p = \"\"\n            if not isinstance(p, (bytes, bytearray)):\n                p = str(p).encode(\"utf-8\")\n            out.append(f\"${len(p)}\\r\\n\".encode(\"utf-8\"))\n            out.append(p)\n            out.append(b\"\\r\\n\")\n        return b\"\".join(out)\n\n    def _read_exact(self, n):\n        while len(self.buf) < n:\n            chunk = self.sock.recv(4096)\n            if not chunk:\n                raise ConnectionError(\"redis connection closed\")\n            self.buf += chunk\n        out, self.buf = self.buf[:n], self.buf[n:]\n        return out\n\n    def _read_line(self):\n        while b\"\\r\\n\" not in self.buf:\n            chunk = self.sock.recv(4096)\n            if not chunk:\n                raise ConnectionError(\"redis connection closed\")\n            self.buf += chunk\n        i = self.buf.index(b\"\\r\\n\")\n        line, self.buf = self.buf[:i], self.buf[i + 2 :]\n        return line\n\n    def _read_resp(self):\n        prefix = self._read_exact(1)\n        if prefix == b\"+\":\n            return self._read_line().decode(\"utf-8\", errors=\"replace\")\n        if prefix == b\"-\":\n            raise RuntimeError(self._read_line().decode(\"utf-8\", errors=\"replace\"))\n        if prefix == b\":\":\n            return int(self._read_line())\n        if prefix == b\"$\":\n            n = int(self._read_line())\n            if n == -1:\n                return None\n            data = self._read_exact(n)\n            _ = self._read_exact(2)\n            return data.decode(\"utf-8\", errors=\"replace\")\n        if prefix == b\"*\":\n            n = int(self._read_line())\n            if n == -1:\n                return None\n            return [self._read_resp() for _ in range(n)]\n        raise RuntimeError(f\"unknown RESP prefix: {prefix!r}\")\n\n    def execute(self, *args):\n        if not self.sock:\n            self.connect()\n        self.sock.sendall(self._encode(*args))\n        return self._read_resp()\n\n    def publish(self, channel, payload):\n        return self.execute(\"PUBLISH\", channel, payload)\n\n    def subscribe(self, channel):\n        return self.execute(\"SUBSCRIBE\", channel)\n\n    def psubscribe(self, pattern):\n        return self.execute(\"PSUBSCRIBE\", pattern)\n\n    def pubsub(self, *args, **kwargs):\n        return self\n\n    def _set_timeout(self, timeout):\n        if self.sock:\n            self.sock.settimeout(timeout if timeout else self.timeout)\n\n    def get_message(self, timeout=1.0):\n        old_timeout = self.timeout\n        try:\n            self._set_timeout(timeout)\n            return self.read_pubsub_message()\n        finally:\n            self._set_timeout(old_timeout)\n\n    def read_pubsub_message(self):\n        try:\n            msg = self._read_resp()\n        except (TimeoutError, socket.timeout):\n            return None\n        if not isinstance(msg, list) or len(msg) < 3:\n            return None\n        kind = msg[0]\n        if kind == \"message\":\n            return {\"type\": \"message\", \"channel\": msg[1], \"data\": msg[2]}\n        if kind == \"pmessage\" and len(msg) >= 4:\n            return {\"type\": \"pmessage\", \"pattern\": msg[1], \"channel\": msg[2], \"data\": msg[3]}\n        return None\n\n_mod = types.ModuleType(\"mini_redis\")\n_mod.MiniRedis = MiniRedis\nsys.modules.setdefault(\"mini_redis\", _mod)\n"
}

func (m *Manager) prepareBacktestStrategyFile(inst *StrategyInstance, backtestID uint) (string, func(), error) {
	if inst == nil {
		return "", func() {}, fmt.Errorf("missing instance")
	}
	if database.DB == nil {
		absPath, err := resolveStrategyPath(inst.Path)
		return absPath, func() {}, err
	}

	var row models.StrategyInstance
	if err := database.DB.Preload("Template").Preload("StrategyVersion").Where("id = ?", inst.ID).First(&row).Error; err != nil {
		absPath, err2 := resolveStrategyPath(inst.Path)
		return absPath, func() {}, err2
	}
	code, _, versionID, err := resolveStrategySource(inst, &row)
	if err != nil {
		absPath, err2 := resolveStrategyPath(inst.Path)
		return absPath, func() {}, err2
	}
	inst.TemplateID = row.TemplateID
	inst.StrategyVersionID = versionID
	if code == "" {
		absPath, err := resolveStrategyPath(inst.Path)
		return absPath, func() {}, err
	}

	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	absDir, err := filepath.Abs(strategiesDir)
	if err != nil {
		return "", func() {}, err
	}
	runtimeDir := filepath.Join(absDir, "_runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return "", func() {}, err
	}

	tmp := filepath.Join(runtimeDir, fmt.Sprintf("backtest_%d_%s.py", backtestID, inst.ID))
	code = sanitizeStrategyRuntimeCode(code)
	runtimeCode := "import os\nimport sys\nsys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), \"..\")))\n\n" + backtestRedisRuntimeShim() + "\n" + code + "\n"
	if err := os.WriteFile(tmp, []byte(runtimeCode), 0o644); err != nil {
		return "", func() {}, err
	}
	return tmp, func() { _ = os.Remove(tmp) }, nil
}

// purpose 取 "open" / "close",与写 strategy_orders 时的 Purpose 同义。它是调用方
// 已经知道、过去却没传进来的那一位信息:没有它,本函数在找不到 open 行时只能拿
// side 猜 —— buy 一律猜成开多,买入平空于是被写成 direction=long / realized_pn_l=0
// 的假仓(台账 #91)。
// applyOrderFillToPosition is the single implementation of position accounting.
// It returns the StrategyPosition row the fill landed on (0 = nothing written),
// so callers that keep their own event ledger — the exchange user data stream —
// can stamp position_id on the fill at the moment it happens instead of
// reconstructing attribution later by joining back through strategy_orders.
func applyOrderFillToPosition(hub *ws.Hub, ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, executedQty float64, avgPrice float64, takeProfit float64, stopLoss float64, eventTime time.Time, purpose string) uint {
	if database.DB == nil || executedQty <= 0 || strategyID == "" {
		return 0
	}

	side = strings.ToLower(strings.TrimSpace(side))
	now := time.Now()
	var pos models.StrategyPosition
	err := database.DB.Where("owner_id = ? AND strategy_id = ? AND symbol = ? AND status = ?", ownerID, strategyID, symbol, "open").First(&pos).Error
	if err != nil {
		if purpose == "close" {
			// 平仓成交却找不到持仓行 = 账本缺口(台账 #92),不是新开仓。过去这里
			// 按 side 猜方向建行:买入平空被写成 direction=long、realized_pn_l=0
			// 的假仓,既污染多空归属又把真盈亏吞成 0,而且从库里看不出来。宁可留
			// 一条可数的孤儿日志,也不写一个骗人的 0。
			logger.Errorf("[POSITION] 平仓成交找不到对应持仓,不建仓 owner=%d strategy=%s symbol=%s side=%s qty=%v price=%v", ownerID, strategyID, symbol, side, executedQty, avgPrice)
			return 0
		}
		// 开仓成交:side 直接决定方向。过去 sell 还额外要求带 tp/sl 才认空头,于是
		// resolveTPSLFromROI 在拿不到成交价(entryPrice<=0)时返回 0/0 的那些开空
		// 单一行都不落 —— 缺失的开仓腿又反过来喂大了上面那个分支。
		openDirection := ""
		switch side {
		case "buy":
			openDirection = "long"
		case "sell":
			openDirection = "short"
		}
		if openDirection == "" {
			return 0
		}
		pos = models.StrategyPosition{
			StrategyID:       strategyID,
			StrategyName:     strategyName,
			ParamVersionID:   currentParamVersionID(strategyID),
			OwnerID:          ownerID,
			Exchange:         exchangeName,
			Symbol:           symbol,
			Direction:        openDirection,
			Amount:           executedQty,
			AvgPrice:         avgPrice,
			TakeProfit:       takeProfit,
			StopLoss:         stopLoss,
			ClosedQty:        0,
			AvgClosePrice:    0,
			RealizedPnL:      0,
			RealizedNotional: 0,
			Status:           "open",
			OpenTime:         eventTime,
			UpdatedAt:        now,
		}
		database.DB.Create(&pos)
		if hub != nil {
			hub.BroadcastJSON(map[string]interface{}{"type": "position", "data": pos})
		}
		return pos.ID
	}

	direction := strings.ToLower(strings.TrimSpace(pos.Direction))
	if purpose == "close" {
		// 平仓单的 side 唯一确定持仓方向:买入只能平空,卖出只能平多。它比行上
		// 存的 direction 硬,所以这里【不只是补空值,还要覆盖矛盾值】:
		//
		// 行上的 direction 可能本来就是错的。共享账户下 owner 自己没下过开仓单、
		// 仓位行是收养/补录出来的,方向就靠补录那一刻的推断,推错了没人纠。存成
		// long 的空头再来一笔 buy 平仓,下面 isIncrease=(long && buy) 判真,这笔
		// 平仓被当成加仓:仓位量反涨、closed_qty 和 realized_pn_l 一个字不写,
		// 随后被 stale-close 置成 closed,库里留下一行"看起来打平"的空壳。
		//
		// 台账 #91 点名的两行就是这么来的(生产库实测):
		//   id=1897 STAR 存 long,平仓 buy 676@0.08014,建仓价 0.08075
		//           → 被当加仓后均价变成 (0.08075+0.08014)/2 = 0.080445,与库里
		//             那行分毫不差;真实盈亏 +0.20618 被吞成 0。
		//   id=1966 AKE  同一条路径,+1.083498 被吞成 0。
		// 这两笔加起来 +1.2897,足以把 qt-breakout-follow 从 −1.2208 翻成 +0.069。
		//
		// 覆盖时留一条 warn:方向存错是上游写入的病,这里只是不让它继续吃掉盈亏,
		// 病灶本身要靠日志被看见。
		inferred := ""
		switch side {
		case "buy":
			inferred = "short"
		case "sell":
			inferred = "long"
		}
		if inferred != "" && inferred != direction {
			if direction != "" {
				logger.Warnf("[POSITION] 平仓单方向与持仓行不符,以平仓单为准 position_id=%d owner=%d strategy=%s symbol=%s stored_direction=%s side=%s corrected=%s", pos.ID, ownerID, strategyID, symbol, direction, side, inferred)
			}
			direction = inferred
		}
	}
	if direction == "" {
		if pos.TakeProfit > 0 && pos.StopLoss > 0 {
			if pos.TakeProfit < pos.AvgPrice && pos.StopLoss > pos.AvgPrice {
				direction = "short"
			} else if pos.TakeProfit > pos.AvgPrice && pos.StopLoss < pos.AvgPrice {
				direction = "long"
			}
		}
		if direction == "" {
			direction = "long"
		}
	}

	isIncrease := (direction == "long" && side == "buy") || (direction == "short" && side == "sell")
	if isIncrease {
		newAmt := pos.Amount + executedQty
		newAvg := pos.AvgPrice
		if newAmt > 0 {
			newAvg = ((pos.AvgPrice * pos.Amount) + (avgPrice * executedQty)) / newAmt
		}
		upd := map[string]interface{}{
			"amount":     newAmt,
			"avg_price":  newAvg,
			"direction":  direction,
			"updated_at": now,
		}
		if takeProfit > 0 {
			upd["take_profit"] = takeProfit
		}
		if stopLoss > 0 {
			upd["stop_loss"] = stopLoss
		}
		database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).Updates(upd)
		if hub != nil {
			pos.Direction = direction
			pos.Amount = newAmt
			pos.AvgPrice = newAvg
			if takeProfit > 0 {
				pos.TakeProfit = takeProfit
			}
			if stopLoss > 0 {
				pos.StopLoss = stopLoss
			}
			hub.BroadcastJSON(map[string]interface{}{"type": "position", "data": pos})
		}
		return pos.ID
	}

	isReduce := (direction == "long" && side == "sell") || (direction == "short" && side == "buy")
	if !isReduce {
		return 0
	}
	newAmt := pos.Amount - executedQty
	realized := executedQty * (avgPrice - pos.AvgPrice)
	if direction == "short" {
		realized = executedQty * (pos.AvgPrice - avgPrice)
	}
	// Round at the persistence boundary to bound long-run float64 drift.
	// Per-fill accumulation across thousands of trades otherwise lets
	// IEEE-754 error creep into PnL and notional totals.
	newRealizedPnL := roundMoney8(pos.RealizedPnL + realized)
	newRealizedNotional := roundMoney8(pos.RealizedNotional + (executedQty * pos.AvgPrice))
	newClosedQty := pos.ClosedQty + executedQty
	newAvgClose := pos.AvgClosePrice
	if newClosedQty > 0 {
		newAvgClose = roundMoney8(((pos.AvgClosePrice * pos.ClosedQty) + (avgPrice * executedQty)) / newClosedQty)
	}
	if newAmt <= 0 {
		database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).
			Updates(map[string]interface{}{
				"amount":            0,
				"direction":         direction,
				"closed_qty":        newClosedQty,
				"avg_close_price":   newAvgClose,
				"realized_pn_l":     newRealizedPnL,
				"realized_notional": newRealizedNotional,
				"pnl_source":        "fill",
				"status":            "closed",
				"close_time":        eventTime,
				"updated_at":        now,
			})
		if hub != nil {
			pos.Direction = direction
			pos.Amount = 0
			pos.ClosedQty = newClosedQty
			pos.AvgClosePrice = newAvgClose
			pos.RealizedPnL = newRealizedPnL
			pos.RealizedNotional = newRealizedNotional
			pos.PnLSource = "fill"
			pos.Status = "closed"
			pos.CloseTime = eventTime
			hub.BroadcastJSON(map[string]interface{}{"type": "position", "data": pos})
		}
		return pos.ID
	}
	database.DB.Model(&models.StrategyPosition{}).Where("id = ?", pos.ID).
		Updates(map[string]interface{}{
			"amount":            newAmt,
			"direction":         direction,
			"closed_qty":        newClosedQty,
			"avg_close_price":   newAvgClose,
			"realized_pn_l":     newRealizedPnL,
			"realized_notional": newRealizedNotional,
			"pnl_source":        "fill",
			"updated_at":        now,
		})
	if hub != nil {
		pos.Direction = direction
		pos.Amount = newAmt
		pos.ClosedQty = newClosedQty
		pos.AvgClosePrice = newAvgClose
		pos.RealizedPnL = newRealizedPnL
		pos.RealizedNotional = newRealizedNotional
		pos.PnLSource = "fill"
		hub.BroadcastJSON(map[string]interface{}{"type": "position", "data": pos})
	}
	return pos.ID
}

func (inst *StrategyInstance) readStderr(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	traceLines := make([]string, 0, 64)
	inTrace := false
	flush := func() {
		if len(traceLines) == 0 {
			inTrace = false
			return
		}
		msg := strings.Join(traceLines, "\n")
		traceLines = traceLines[:0]
		inTrace = false
		logger.Errorf("[%s ERROR] %s", inst.Name, msg)
		_ = database.DB.Create(&models.StrategyLog{
			StrategyID: inst.ID,
			Level:      "error",
			Message:    msg,
			CreatedAt:  time.Now(),
		}).Error
		inst.hub.BroadcastJSON(map[string]interface{}{
			"type":        "error",
			"strategy_id": inst.ID,
			"owner_id":    inst.OwnerID,
			"error":       msg,
		})
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Traceback (most recent call last):") {
			flush()
			inTrace = true
			traceLines = append(traceLines, line)
			continue
		}
		if inTrace {
			traceLines = append(traceLines, line)
			if strings.HasPrefix(line, "During handling of the above exception") {
				continue
			}
			if strings.HasPrefix(line, "The above exception") {
				continue
			}
			if strings.HasPrefix(line, "  File ") || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") || strings.TrimSpace(line) == "" {
				continue
			}
			if strings.Contains(line, ":") {
				flush()
			}
			continue
		}
		traceLines = append(traceLines, line)
		flush()
	}
	flush()
}

func (m *Manager) historySyncLoop(ctx context.Context, inst *StrategyInstance, redisBus *bus.RedisBus) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			inst.mu.Lock()
			need := inst.resync
			bootID := inst.bootID
			symbols := append([]string(nil), inst.feedSymbols...)
			nextAt := inst.resyncNextAt
			inst.mu.Unlock()

			if !need || strings.TrimSpace(bootID) == "" || len(symbols) == 0 {
				continue
			}
			if !nextAt.IsZero() && time.Now().Before(nextAt) {
				continue
			}

			if getBool(inst.Config()["log_redis"]) {
				inst.mu.Lock()
				if inst.resyncLogBootID != bootID {
					inst.resyncLogBootID = bootID
					inst.mu.Unlock()
					emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish history start ch=%s boot_id=%s symbols=%d", redisBus.CandleChannel(inst.ID), bootID, len(symbols)))
				} else {
					inst.mu.Unlock()
				}
			}

			ok := true
			historyBars := 200
			for _, sym := range symbols {
				candles, err := inst.exchange.FetchCandles(sym, "1m", historyBars)
				if err != nil || len(candles) == 0 {
					ok = false
					emitStrategyLog(inst, "error", fmt.Sprintf("FetchCandles failed for history symbol=%s err=%v", sym, err))
					continue
				}
				out := make([]bus.CandleMessage, 0, len(candles))
				for _, c := range candles {
					out = append(out, bus.CandleMessage{
						Type:       "candle",
						StrategyID: inst.ID,
						Symbol:     sym,
						Timestamp:  c.Timestamp,
						Open:       c.Open,
						High:       c.High,
						Low:        c.Low,
						Close:      c.Close,
						Volume:     c.Volume,
					})
				}
				if err := redisBus.PublishHistory(context.Background(), inst.ID, sym, out); err != nil {
					ok = false
					logger.Errorf("[REDIS PUBLISH ERROR] id=%s owner=%d symbol=%s type=history err=%v", inst.ID, inst.OwnerID, sym, err)
					emitStrategyLog(inst, "error", fmt.Sprintf("Redis publish history failed symbol=%s err=%v", sym, err))
				} else if getBool(inst.Config()["log_redis"]) {
					emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish history ok symbol=%s bars=%d", sym, len(out)))
				}
			}
			if ok {
				inst.mu.Lock()
				inst.resync = false
				inst.resyncLogBootID = ""
				inst.resyncNextAt = time.Time{}
				inst.resyncBackoff = 0
				inst.mu.Unlock()
				if getBool(inst.Config()["log_redis"]) {
					emitStrategyLog(inst, "info", fmt.Sprintf("Redis publish history done boot_id=%s", bootID))
				}
			} else {
				inst.mu.Lock()
				b := inst.resyncBackoff
				if b <= 0 {
					b = 2 * time.Second
				} else {
					b = b * 2
				}
				if b > 60*time.Second {
					b = 60 * time.Second
				}
				inst.resyncBackoff = b
				inst.resyncNextAt = time.Now().Add(b)
				inst.mu.Unlock()
			}
		}
	}
}

func (m *Manager) RemoveStrategy(id string) error {
	// Remove from in-memory registry first, then kill process if needed.
	m.mu.Lock()
	inst, ok := m.instances[id]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.instances, id)
	m.mu.Unlock()

	inst.mu.Lock()
	defer inst.mu.Unlock()
	// 删除必须斩草除根：只在 Status==Running 时 Kill 会让 starting/stopping/
	// restarting 等瞬态窗口下的删除留下无主进程继续交易（已删载具 23:11Z 仍开仓
	// 的幽灵实证）。注册表行已删，任何存活进程都无人能再管——无条件杀。
	inst.killedByUs = true
	inst.stopping = true // 压制 requestRestart/健康检查复活路径
	if inst.cmd != nil && inst.cmd.Process != nil {
		_ = inst.cmd.Process.Kill()
	}
	inst.Status = StatusStopped
	return nil
}

// UpdateStrategyConfig 整体替换策略的内存配置快照。
// Caller is responsible for persisting to DB (API handler does this).
//
// 2026-09-16 用户直令：移除原先 "running 时禁止改配置" 的硬拒。
//
// 那道 guard 的原始理由是 data race，方向对但手段错：真正的修法是给 Config
// 加原子快照（见 StrategyInstance.config 的注释），而不是禁止修改。留着它的
// 代价是实测出来的 —— 审计表 strategy_audit_logs 里 claude_cron 从
// 2026-07-17 到 2026-09-16 的 1589 次改动**全部**因
// "cannot update config while strategy is running" 被弹掉（成功仅 219 次），
// 也就是说自动调参链路两个月里 88% 的时间是死的。
//
// 生效范围（重要，容易误解）：走 Config() 快照的**后端内**参数（杠杆、仓位
// 比例、confidence 门、ATR 止盈止损系数、并发上限…）下一次开仓/平仓即生效，
// 不需要重启。但 Python 子进程是在 spawn 时从 argv 读配置的，它那一侧的参数
// （信号计算相关）要 stop+start 才换 —— 这一点没变，optimizer 的提示文案仍准确。
func (m *Manager) UpdateStrategyConfig(id string, config map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}

	inst.setConfig(config)
	return nil
}

func (inst *StrategyInstance) readStdout() {
	// trace 模式(log_level=debug / debug=true / log_trace=true)下,策略日志
	// **只广播、不落库**。
	//
	// 起因(2026-09-16):线上一个 log_level=debug 的实例每根 K 线都打日志 ——
	// 43 个 symbol × 每分钟回放 200 根历史 = 143 条/秒,实测 148 条/秒,
	// 合 12.8M 行/天 ≈ 5GB/天;而 db-log-retention.sh 每天只跑一次、单次上限
	// 8M 行(400×20000),追不上 → 磁盘净增约 1.9GB/天,7.3G 空闲约 4 天见底。
	//
	// 这一档的语义本来就是"我要盯实时流"(见 strategy_start.go applyLogLevelPreset
	// 对 debug 档的说明),逐根 K 线属于瞬时观测,没有事后追溯价值。让它走
	// BroadcastJSON(前端实时)和 logger(容器日志)即可,DB 留给决策/下单/成交/
	// 错误这些低频、需要回溯的日志。非 trace 档位完全不受影响。
	traceOn := getBool(inst.Config()["debug"]) || getBool(inst.Config()["log_trace"]) ||
		strings.ToLower(strings.TrimSpace(getString(inst.Config()["log_level"]))) == "debug"
	persist := !traceOn
	scanner := bufio.NewScanner(inst.stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			txt := strings.TrimSpace(string(line))
			if txt == "" {
				continue
			}
			if persist {
				database.DB.Create(&models.StrategyLog{
					StrategyID: inst.ID,
					Level:      "info",
					Message:    txt,
					CreatedAt:  time.Now(),
				})
			}
			inst.hub.BroadcastJSON(map[string]interface{}{"type": "log", "data": txt, "id": inst.ID})
			continue
		}
		if t, _ := msg["type"].(string); t != "log" {
			continue
		}
		logMsg, _ := msg["data"].(string)
		if strings.TrimSpace(logMsg) == "" {
			continue
		}
		if persist {
			database.DB.Create(&models.StrategyLog{
				StrategyID: inst.ID,
				Level:      "info",
				Message:    logMsg,
				CreatedAt:  time.Now(),
			})
		}
		inst.hub.BroadcastJSON(map[string]interface{}{"type": "log", "data": logMsg, "id": inst.ID})
	}

	if inst.mgr != nil {
		inst.mgr.setStrategyStatus(inst, StatusStopped)
	} else {
		inst.mu.Lock()
		inst.Status = StatusStopped
		inst.mu.Unlock()
	}
}

func (m *Manager) SyncFromDB(db *gorm.DB) error {
	// SyncFromDB loads all strategy instances from DB into memory so they can be
	// started/stopped without recreating them.
	//
	// This is called once on backend startup.
	var instances []models.StrategyInstance
	if err := db.Preload("Template").Preload("StrategyVersion").Find(&instances).Error; err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, inst := range instances {
		if _, ok := m.instances[inst.ID]; !ok {
			var config map[string]interface{}
			json.Unmarshal([]byte(inst.Config), &config)

			path := strings.TrimSpace(inst.Template.Path)
			versionID := inst.StrategyVersionID
			if code, sourcePath, resolvedVersionID, err := resolveStrategySource(nil, &inst); err == nil {
				if strings.TrimSpace(sourcePath) != "" {
					path = sourcePath
				} else if strings.TrimSpace(path) == "" && strings.TrimSpace(code) != "" {
					if resolvedVersionID != nil {
						path = fmt.Sprintf("db://strategy_version/%d", *resolvedVersionID)
					} else if inst.Template.ID != 0 {
						path = fmt.Sprintf("db://template/%d", inst.Template.ID)
					}
				}
				versionID = resolvedVersionID
			}
			if path == "" {
				path = firstNonEmpty(strings.TrimSpace(inst.Template.Path), fmt.Sprintf("db://template/%d", inst.Template.ID))
			}

			restored := &StrategyInstance{
				ID:                inst.ID,
				Name:              inst.Name,
				TemplateID:        inst.TemplateID,
				StrategyVersionID: versionID,
				Path:              path,
				Status:            StatusStopped,
				OwnerID:           inst.OwnerID,
				CreatedAt:         inst.CreatedAt,
				hub:               m.hub,
				exchange:          m.exchange,
				mgr:               m,
			}
			restored.setConfig(config)
			m.instances[inst.ID] = restored

		}
	}
	return nil
}

// ListStrategies returns all strategy instances visible to a user.
// Admins can see all instances; non-admins can only see their own.
func (m *Manager) ListStrategies(ownerID uint, isAdmin bool) []*StrategyInstance {

	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*StrategyInstance, 0)
	for _, inst := range m.instances {
		if isAdmin || inst.OwnerID == ownerID {
			list = append(list, inst)
		}
	}

	// Sort by CreatedAt Desc
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})

	return list
}

// GetExchange exposes the exchange connector for API handlers.
func (m *Manager) GetExchange() exchange.Exchange {
	return m.exchange
}

// Clear stops all running strategies and clears the in-memory registry.
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.instances {
		if inst.Status == StatusRunning {
			inst.killedByUs = true
			inst.cmd.Process.Kill()
		}
	}
	m.instances = make(map[string]*StrategyInstance)
}
