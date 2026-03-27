package strategy

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"quanty_trade/internal/bus"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
)

type strategyStartPlan struct {
	redisBus    *bus.RedisBus
	runCfg      map[string]interface{}
	feedSymbols []string
	logTrace    bool
}

type strategyProcess struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func validateFeedSymbolsForExchange(inst *StrategyInstance, feedSymbols []string, logTrace bool) ([]string, error) {
	if inst == nil || len(feedSymbols) == 0 {
		return feedSymbols, nil
	}
	bx, ok := inst.exchange.(*exchange.BinanceExchange)
	if !ok {
		return feedSymbols, nil
	}
	valid := make([]string, 0, len(feedSymbols))
	for _, sym := range feedSymbols {
		sym = strings.TrimSpace(sym)
		if sym == "" {
			continue
		}
		if err := bx.SupportsSymbol(sym); err != nil {
			emitStrategyLog(inst, "error", fmt.Sprintf("Symbol filtered out symbol=%s reason=%v", sym, err))
			continue
		}
		valid = append(valid, sym)
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("no tradable symbols available in current market")
	}
	if logTrace && len(valid) != len(feedSymbols) {
		emitStrategyLog(inst, "info", fmt.Sprintf("Symbol validation ok kept=%d dropped=%d market=%s", len(valid), len(feedSymbols)-len(valid), bx.Market()))
	}
	return valid, nil
}

func (m *Manager) getStartableStrategy(id string) (*StrategyInstance, error) {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("strategy %s not found", id)
	}
	inst.mu.Lock()
	if inst.Status == StatusRunning {
		inst.mu.Unlock()
		return nil, nil
	}
	logger.Infof("[STRATEGY START] id=%s owner=%d name=%s path=%s", inst.ID, inst.OwnerID, inst.Name, inst.Path)
	inst.mu.Unlock()
	return inst, nil
}

func (m *Manager) ensureStartRedisBus() (*bus.RedisBus, error) {
	m.mu.RLock()
	rb := m.redisBus
	m.mu.RUnlock()
	if rb != nil {
		return rb, nil
	}
	rb, err := bus.NewRedisBusFromConfig()
	if err != nil {
		return nil, fmt.Errorf("redis bus not available: %v (请确保已启动 Redis，并配置 REDIS_ENABLED=true / REDIS_ADDR)", err)
	}
	m.SetRedisBus(rb)
	return rb, nil
}

func (m *Manager) buildStrategyStartPlan(inst *StrategyInstance) (*strategyStartPlan, error) {
	if inst == nil {
		return nil, nil
	}
	rb, err := m.ensureStartRedisBus()
	if err != nil {
		return nil, err
	}
	inst.mu.Lock()
	if inst.Status == StatusRunning {
		inst.mu.Unlock()
		return nil, nil
	}
	inst.mu.Unlock()

	runCfg := make(map[string]interface{}, len(inst.Config)+8)
	for k, v := range inst.Config {
		runCfg[k] = v
	}
	debugOn := getBool(inst.Config["debug"])
	logTrace := getBool(inst.Config["log_trace"]) || debugOn
	if logTrace {
		runCfg["log_trace"] = true
		if _, ok := runCfg["log_every"]; !ok {
			runCfg["log_every"] = 1
		}
		if _, ok := runCfg["log_idle_sec"]; !ok {
			runCfg["log_idle_sec"] = 5
		}
		if _, ok := runCfg["log_rx"]; !ok {
			runCfg["log_rx"] = true
		}
		if _, ok := runCfg["log_decisions"]; !ok {
			runCfg["log_decisions"] = true
		}
	}
	runCfg["strategy_id"] = inst.ID
	runCfg["owner_id"] = inst.OwnerID
	rc := conf.C().Redis
	runCfg["redis_addr"] = rc.Addr
	runCfg["redis_password"] = rc.Password
	runCfg["redis_db"] = rc.DB
	runCfg["redis_prefix"] = rc.Prefix
	runCfg["use_redis"] = true
	runCfg["healthcheck"] = map[string]interface{}{
		"enabled":         true,
		"interval_sec":    5,
		"timeout_sec":     20,
		"ready_grace_sec": 30,
	}

	feedSymbols, err := m.resolveFeedSymbols(inst, logTrace)
	if err != nil {
		return nil, err
	}
	if len(feedSymbols) > 0 {
		runCfg["symbols"] = feedSymbols
		if _, ok := runCfg["symbol"].(string); !ok || strings.TrimSpace(fmt.Sprintf("%v", runCfg["symbol"])) == "" {
			runCfg["symbol"] = feedSymbols[0]
		}
	}
	return &strategyStartPlan{
		redisBus:    rb,
		runCfg:      runCfg,
		feedSymbols: feedSymbols,
		logTrace:    logTrace,
	}, nil
}

func (m *Manager) resolveFeedSymbols(inst *StrategyInstance, logTrace bool) ([]string, error) {
	fixedSymbol := strings.TrimSpace(getString(inst.Config["symbol"]))
	feedSymbols := parseSymbolsValue(inst.Config["symbols"])
	if len(feedSymbols) == 0 && fixedSymbol != "" {
		feedSymbols = []string{fixedSymbol}
	}

	selectMode := strings.ToLower(strings.TrimSpace(getString(inst.Config["symbol_select_mode"])))
	autoSymbols := getBool(inst.Config["auto_symbols"])
	minPrice := getNumber(inst.Config["min_price"])
	maxPrice := getNumber(inst.Config["max_price"])
	minPrecision := int(getNumber(inst.Config["min_precision"]))
	minVolatility := getNumber(inst.Config["min_volatility"])
	limit := int(getNumber(inst.Config["select_limit"]))
	if limit <= 0 {
		limit = 20
	}
	useFilter := fixedSymbol == "" && (selectMode == "filter" || autoSymbols || minPrice > 0 || maxPrice > 0 || minPrecision > 0 || minVolatility > 0)
	if !useFilter {
		return validateFeedSymbolsForExchange(inst, feedSymbols, logTrace)
	}

	emitStrategyLog(inst, "info", fmt.Sprintf("Symbol select start mode=%s min_price=%v max_price=%v min_precision=%d min_volatility=%v limit=%d", selectMode, minPrice, maxPrice, minPrecision, minVolatility, limit))
	criteria := exchange.SymbolSelectCriteria{
		MinPrice:      minPrice,
		MaxPrice:      maxPrice,
		MinPrecision:  minPrecision,
		MinVolatility: minVolatility,
		Quote:         "USDT",
		Limit:         limit,
		OnlySymbols:   feedSymbols,
	}
	bx, ok := inst.exchange.(*exchange.BinanceExchange)
	if !ok {
		emitStrategyLog(inst, "error", "Symbol select requires Binance exchange")
		if fixedSymbol == "" && len(feedSymbols) == 0 {
			return nil, fmt.Errorf("symbol select requires binance and no symbols configured")
		}
		return feedSymbols, nil
	}
	res, err := bx.SelectSymbolsDetailed(criteria)
	if err != nil {
		emitStrategyLog(inst, "error", fmt.Sprintf("Symbol select failed err=%v", err))
		if fixedSymbol == "" && len(feedSymbols) == 0 {
			return nil, fmt.Errorf("symbol select failed and no symbols configured")
		}
		return feedSymbols, nil
	}
	if len(res.Selected) == 0 {
		emitStrategyLog(inst, "error", "Symbol select returned empty set")
		if fixedSymbol == "" && len(feedSymbols) == 0 {
			return nil, fmt.Errorf("symbol select returned empty set and no symbols configured")
		}
		return feedSymbols, nil
	}

	before := append([]string(nil), feedSymbols...)
	feedSymbols = res.Selected
	preview := strings.Join(feedSymbols, ",")
	if len(feedSymbols) > 10 {
		preview = strings.Join(feedSymbols[:10], ",") + ",..."
	}
	emitStrategyLog(inst, "info", fmt.Sprintf("Symbol select ok count=%d mode=%s symbols=%s", len(feedSymbols), selectMode, preview))
	if logTrace && len(before) > 0 && len(res.Rejected) > 0 {
		n := 0
		for _, s := range before {
			if reason, ok := res.Rejected[s]; ok {
				emitStrategyLog(inst, "info", fmt.Sprintf("Symbol filtered out symbol=%s reason=%s", s, reason))
				n++
				if n >= 20 {
					break
				}
			}
		}
	}
	return validateFeedSymbolsForExchange(inst, feedSymbols, logTrace)
}

func (m *Manager) startStrategyProcess(inst *StrategyInstance, plan *strategyStartPlan) (*strategyProcess, error) {
	if inst == nil || plan == nil {
		return nil, nil
	}
	configJSON, _ := json.Marshal(plan.runCfg)
	absPath, err := m.prepareRuntimeStrategyFile(inst)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("python3", absPath, string(configJSON))
	runDir := filepath.Dir(absPath)
	if inst.RuntimeGenerated {
		runDir = filepath.Dir(runDir)
	}
	cmd.Dir = runDir
	cmd.Env = append(cmd.Environ(), "STRATEGY_CONFIG_JSON="+string(configJSON))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &strategyProcess{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

func (m *Manager) activateStartedStrategy(inst *StrategyInstance, plan *strategyStartPlan, proc *strategyProcess) {
	if inst == nil || plan == nil || proc == nil {
		return
	}
	inst.mu.Lock()
	inst.cmd = proc.cmd
	inst.stdout = proc.stdout
	inst.feedSymbols = plan.feedSymbols
	inst.resync = true
	inst.bootID = ""
	inst.stateReadySeen = false
	inst.heartbeatSeen = false
	inst.startedAt = time.Now()
	inst.lastHB = time.Time{}
	inst.stopping = false
	inst.restarting = false
	inst.mu.Unlock()
	m.setStrategyStatus(inst, StatusStarting)

	pid := 0
	if proc.cmd.Process != nil {
		pid = proc.cmd.Process.Pid
	}
	candleCh := plan.redisBus.CandleChannel(inst.ID)
	signalCh := plan.redisBus.SignalChannel(inst.ID)
	stateCh := plan.redisBus.StateChannel(inst.ID)
	logger.Infof("[STRATEGY PROCESS] id=%s owner=%d pid=%d symbols=%d candle_ch=%s signal_ch=%s state_ch=%s", inst.ID, inst.OwnerID, pid, len(plan.feedSymbols), candleCh, signalCh, stateCh)
	emitStrategyLog(inst, "info", fmt.Sprintf("Process started pid=%d symbols=%d candle_ch=%s signal_ch=%s state_ch=%s", pid, len(plan.feedSymbols), candleCh, signalCh, stateCh))
}

func (m *Manager) syncStrategyDebugConfig(inst *StrategyInstance) {
	if inst == nil || database.DB == nil || !getBool(inst.Config["debug"]) {
		return
	}
	cfg := make(map[string]interface{}, len(inst.Config))
	for k, v := range inst.Config {
		cfg[k] = v
	}
	cfg["debug"] = false
	if b, err := json.Marshal(cfg); err == nil {
		_ = database.DB.Model(&models.StrategyInstance{}).Where("id = ?", inst.ID).
			Updates(map[string]interface{}{"config": string(b), "updated_at": time.Now()}).Error
		inst.Config["debug"] = false
	}
}
