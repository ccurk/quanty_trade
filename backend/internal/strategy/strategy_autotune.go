package strategy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/models"
)

type optimizationPositionView struct {
	Symbol           string    `json:"symbol"`
	Direction        string    `json:"direction"`
	Amount           float64   `json:"amount"`
	AvgPrice         float64   `json:"avg_price"`
	AvgClosePrice    float64   `json:"avg_close_price"`
	RealizedPnL      float64   `json:"realized_pnl"`
	RealizedNotional float64   `json:"realized_notional"`
	Status           string    `json:"status"`
	OpenTime         time.Time `json:"open_time"`
	CloseTime        time.Time `json:"close_time,omitempty"`
}

type optimizationOrderView struct {
	Symbol       string    `json:"symbol"`
	Side         string    `json:"side"`
	Purpose      string    `json:"purpose"`
	Status       string    `json:"status"`
	RequestedQty float64   `json:"requested_qty"`
	ExecutedQty  float64   `json:"executed_qty"`
	AvgPrice     float64   `json:"avg_price"`
	RequestedAt  time.Time `json:"requested_at"`
}

type optimizationMarketSummary struct {
	Symbol               string  `json:"symbol"`
	CandleCount          int     `json:"candle_count"`
	WindowMinutes        int     `json:"window_minutes"`
	FirstClose           float64 `json:"first_close"`
	LastClose            float64 `json:"last_close"`
	ReturnPct            float64 `json:"return_pct"`
	High                 float64 `json:"high"`
	Low                  float64 `json:"low"`
	RangePct             float64 `json:"range_pct"`
	AverageVolume        float64 `json:"average_volume"`
	MaxVolume            float64 `json:"max_volume"`
	VolatilityPct        float64 `json:"volatility_pct"`
	LastCandleTimestamp  string  `json:"last_candle_timestamp"`
	LastCandleClose      float64 `json:"last_candle_close"`
	LastCandleVolume     float64 `json:"last_candle_volume"`
	Last15mReturnPct     float64 `json:"last_15m_return_pct"`
	Last30mReturnPct     float64 `json:"last_30m_return_pct"`
	Last60mReturnPct     float64 `json:"last_60m_return_pct"`
	Last15mAverageVolume float64 `json:"last_15m_average_volume"`
	Last30mAverageVolume float64 `json:"last_30m_average_volume"`
	Last60mAverageVolume float64 `json:"last_60m_average_volume"`
}

type optimizationSymbolTradeSummary struct {
	Symbol                string  `json:"symbol"`
	Orders                int     `json:"orders"`
	FilledOrders          int     `json:"filled_orders"`
	ClosedPositions       int     `json:"closed_positions"`
	OpenPositions         int     `json:"open_positions"`
	Wins                  int     `json:"wins"`
	Losses                int     `json:"losses"`
	RealizedPnL           float64 `json:"realized_pnl"`
	RealizedNotional      float64 `json:"realized_notional"`
	RealizedReturnPct     float64 `json:"realized_return_pct"`
	AverageHoldingMinutes float64 `json:"average_holding_minutes"`
	WinRatePct            float64 `json:"win_rate_pct"`
}

type optimizationWindowSummary struct {
	PositionCount         int     `json:"position_count"`
	ClosedPositions       int     `json:"closed_positions"`
	OpenPositions         int     `json:"open_positions"`
	OrderCount            int     `json:"order_count"`
	FilledOrders          int     `json:"filled_orders"`
	RequestedOrders       int     `json:"requested_orders"`
	RejectedOrders        int     `json:"rejected_orders"`
	FailedOrders          int     `json:"failed_orders"`
	ActiveSymbols         int     `json:"active_symbols"`
	TotalRealizedPnL      float64 `json:"total_realized_pnl"`
	TotalRealizedNotional float64 `json:"total_realized_notional"`
	RealizedReturnPct     float64 `json:"realized_return_pct"`
	WinRatePct            float64 `json:"win_rate_pct"`
}

type optimizationContextPayload struct {
	StrategyID      string                           `json:"strategy_id"`
	StrategyName    string                           `json:"strategy_name"`
	WindowStart     time.Time                        `json:"window_start"`
	WindowEnd       time.Time                        `json:"window_end"`
	Config          map[string]interface{}           `json:"config"`
	Summary         optimizationWindowSummary        `json:"summary"`
	Symbols         []optimizationSymbolTradeSummary `json:"symbols"`
	RecentPositions []optimizationPositionView       `json:"recent_positions"`
	RecentOrders    []optimizationOrderView          `json:"recent_orders"`
	Markets         []optimizationMarketSummary      `json:"markets"`
}

type optimizationPreparedInput struct {
	row        models.StrategyInstance
	code       string
	sourcePath string
	payload    optimizationContextPayload
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Temperature float64             `json:"temperature"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (m *Manager) runAutoOptimizeWorker() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.RLock()
		instances := make([]*StrategyInstance, 0, len(m.instances))
		for _, inst := range m.instances {
			instances = append(instances, inst)
		}
		m.mu.RUnlock()
		now := time.Now()
		for _, inst := range instances {
			if inst == nil || !getBool(inst.Config["auto_optimize_enabled"]) {
				continue
			}
			interval := time.Duration(getNumber(inst.Config["auto_optimize_interval_minutes"])) * time.Minute
			if interval <= 0 {
				interval = 180 * time.Minute
			}
			inst.mu.Lock()
			if inst.optimizeRunning || inst.stopping || inst.restarting {
				inst.mu.Unlock()
				continue
			}
			if !inst.lastOptimizeAt.IsZero() && now.Sub(inst.lastOptimizeAt) < interval {
				inst.mu.Unlock()
				continue
			}
			inst.optimizeRunning = true
			inst.lastOptimizeAt = now
			inst.mu.Unlock()
			go m.optimizeStrategyInstance(inst, "schedule")
		}
	}
}

func (m *Manager) optimizeStrategyInstance(inst *StrategyInstance, trigger string) {
	now := time.Now()
	defer func() {
		inst.mu.Lock()
		inst.optimizeRunning = false
		inst.mu.Unlock()
	}()

	lookback := time.Duration(getNumber(inst.Config["auto_optimize_lookback_minutes"])) * time.Minute
	if lookback <= 0 {
		lookback = 180 * time.Minute
	}
	windowStart := now.Add(-lookback)
	model := firstNonEmpty(
		strings.TrimSpace(getString(inst.Config["auto_optimize_model"])),
		strings.TrimSpace(conf.C().AI.Optimizer.Model),
		strings.TrimSpace(os.Getenv("AI_OPTIMIZER_MODEL")),
	)

	run := models.StrategyOptimizationRun{
		StrategyID:  inst.ID,
		OwnerID:     inst.OwnerID,
		Status:      "running",
		Trigger:     trigger,
		Model:       model,
		WindowStart: windowStart,
		WindowEnd:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if database.DB != nil {
		_ = database.DB.Create(&run).Error
	}

	emitStrategyLog(inst, "info", fmt.Sprintf("AI优化开始 trigger=%s lookback=%s", trigger, lookback))

	input, err := m.prepareOptimizationInput(inst, windowStart, now)
	if err != nil {
		m.finishOptimizationRun(inst, &run, "failed", false, "", "", err)
		return
	}
	run.BaseCodeHash = hashText(input.code)

	candidateCode, summary, err := m.requestOptimizedStrategyCode(inst, input)
	if err != nil {
		m.finishOptimizationRun(inst, &run, "failed", false, "", "", err)
		return
	}
	candidateCode = strings.TrimSpace(candidateCode)
	if candidateCode == "" {
		m.finishOptimizationRun(inst, &run, "failed", false, "", "", fmt.Errorf("AI 返回空策略代码"))
		return
	}
	run.CandidateCodeHash = hashText(candidateCode)

	if strings.TrimSpace(candidateCode) == strings.TrimSpace(input.code) {
		m.finishOptimizationRun(inst, &run, "skipped", false, summary, "", fmt.Errorf("AI 未生成新的策略变更"))
		return
	}
	if err := validatePythonStrategy(candidateCode); err != nil {
		m.finishOptimizationRun(inst, &run, "failed", false, summary, "", fmt.Errorf("候选策略语法校验失败: %w", err))
		return
	}

	if getBool(inst.Config["auto_optimize_dry_run"]) || !getBool(inst.Config["auto_optimize_apply"]) {
		m.finishOptimizationRun(inst, &run, "completed", false, summary, "dry-run", nil)
		return
	}

	wasRunning := false
	inst.mu.Lock()
	wasRunning = inst.Status == StatusRunning || inst.Status == StatusStarting
	inst.mu.Unlock()

	appliedPath, err := m.applyOptimizedCode(inst, input, candidateCode, &run, summary)
	if err != nil {
		m.finishOptimizationRun(inst, &run, "failed", false, summary, "", err)
		return
	}
	if wasRunning {
		if err := m.StopStrategy(inst.ID, true); err != nil {
			m.finishOptimizationRun(inst, &run, "failed", false, summary, appliedPath, fmt.Errorf("已写入新策略，但停止旧实例失败: %w", err))
			return
		}
		time.Sleep(2 * time.Second)
		if err := m.StartStrategy(inst.ID); err != nil {
			m.finishOptimizationRun(inst, &run, "failed", true, summary, appliedPath, fmt.Errorf("新策略已写入，但重启失败: %w", err))
			return
		}
	}

	m.finishOptimizationRun(inst, &run, "completed", true, summary, appliedPath, nil)
}

func (m *Manager) prepareOptimizationInput(inst *StrategyInstance, start, end time.Time) (*optimizationPreparedInput, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	var row models.StrategyInstance
	if err := database.DB.Preload("Template").Preload("StrategyVersion").Where("id = ?", inst.ID).First(&row).Error; err != nil {
		return nil, err
	}

	code, sourcePath, versionID, err := resolveStrategySource(inst, &row)
	if err != nil {
		return nil, err
	}
	inst.TemplateID = row.TemplateID
	inst.StrategyVersionID = versionID

	var posRows []models.StrategyPosition
	_ = database.DB.
		Where("owner_id = ? AND strategy_id = ? AND (updated_at >= ? OR open_time >= ? OR close_time >= ?)", inst.OwnerID, inst.ID, start, start, start).
		Order("updated_at desc").
		Limit(100).
		Find(&posRows).Error

	var ordRows []models.StrategyOrder
	_ = database.DB.
		Where("owner_id = ? AND strategy_id = ? AND requested_at >= ?", inst.OwnerID, inst.ID, start).
		Order("requested_at desc").
		Limit(200).
		Find(&ordRows).Error

	positions := make([]optimizationPositionView, 0, len(posRows))
	orders := make([]optimizationOrderView, 0, len(ordRows))
	symbolScore := map[string]int{}
	symbolSummaryMap := map[string]*optimizationSymbolTradeSummary{}
	windowSummary := optimizationWindowSummary{}
	maxPositions := int(getNumber(inst.Config["auto_optimize_max_positions"]))
	if maxPositions <= 0 {
		maxPositions = 24
	}
	maxOrders := int(getNumber(inst.Config["auto_optimize_max_orders"]))
	if maxOrders <= 0 {
		maxOrders = 48
	}
	for _, p := range posRows {
		if len(positions) < maxPositions {
			positions = append(positions, optimizationPositionView{
				Symbol:           p.Symbol,
				Direction:        p.Direction,
				Amount:           p.Amount,
				AvgPrice:         p.AvgPrice,
				AvgClosePrice:    p.AvgClosePrice,
				RealizedPnL:      p.RealizedPnL,
				RealizedNotional: p.RealizedNotional,
				Status:           p.Status,
				OpenTime:         p.OpenTime,
				CloseTime:        p.CloseTime,
			})
		}
		symbolScore[p.Symbol] += 3
		windowSummary.PositionCount++
		ss := ensureOptimizationSymbolSummary(symbolSummaryMap, p.Symbol)
		if p.Status == "closed" {
			windowSummary.ClosedPositions++
			ss.ClosedPositions++
			ss.RealizedPnL += p.RealizedPnL
			ss.RealizedNotional += p.RealizedNotional
			if p.RealizedPnL > 0 {
				ss.Wins++
			} else if p.RealizedPnL < 0 {
				ss.Losses++
			}
			if !p.CloseTime.IsZero() && !p.OpenTime.IsZero() && p.CloseTime.After(p.OpenTime) {
				ss.AverageHoldingMinutes += p.CloseTime.Sub(p.OpenTime).Minutes()
			}
			windowSummary.TotalRealizedPnL += p.RealizedPnL
			windowSummary.TotalRealizedNotional += p.RealizedNotional
		} else {
			windowSummary.OpenPositions++
			ss.OpenPositions++
		}
	}
	for _, o := range ordRows {
		if len(orders) < maxOrders {
			orders = append(orders, optimizationOrderView{
				Symbol:       o.Symbol,
				Side:         o.Side,
				Purpose:      o.Purpose,
				Status:       o.Status,
				RequestedQty: o.RequestedQty,
				ExecutedQty:  o.ExecutedQty,
				AvgPrice:     o.AvgPrice,
				RequestedAt:  o.RequestedAt,
			})
		}
		symbolScore[o.Symbol]++
		windowSummary.OrderCount++
		ss := ensureOptimizationSymbolSummary(symbolSummaryMap, o.Symbol)
		ss.Orders++
		switch o.Status {
		case "filled":
			windowSummary.FilledOrders++
			ss.FilledOrders++
		case "requested":
			windowSummary.RequestedOrders++
		case "rejected":
			windowSummary.RejectedOrders++
		case "failed":
			windowSummary.FailedOrders++
		}
	}
	for _, s := range parseSymbolsValue(inst.Config["symbols"]) {
		symbolScore[s] += 1
	}
	if raw := strings.TrimSpace(getString(inst.Config["symbol"])); raw != "" {
		symbolScore[raw] += 1
	}

	type pair struct {
		symbol string
		score  int
	}
	symbols := make([]pair, 0, len(symbolScore))
	for sym, score := range symbolScore {
		if strings.TrimSpace(sym) == "" {
			continue
		}
		symbols = append(symbols, pair{symbol: sym, score: score})
	}
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].score == symbols[j].score {
			return exchange.NormalizeSymbol(symbols[i].symbol) < exchange.NormalizeSymbol(symbols[j].symbol)
		}
		return symbols[i].score > symbols[j].score
	})

	maxSymbols := int(getNumber(inst.Config["auto_optimize_max_symbols"]))
	if maxSymbols <= 0 {
		maxSymbols = 6
	}
	if len(symbols) > maxSymbols {
		symbols = symbols[:maxSymbols]
	}

	markets := make([]optimizationMarketSummary, 0, len(symbols))
	for _, item := range symbols {
		candles, err := inst.exchange.FetchHistoricalCandles(item.symbol, "1m", start, end)
		if err != nil || len(candles) == 0 {
			continue
		}
		markets = append(markets, summarizeOptimizationCandles(item.symbol, candles, start, end))
	}
	if len(positions) == 0 && len(orders) == 0 && len(markets) == 0 {
		return nil, fmt.Errorf("过去窗口内没有可用于优化的交易或行情数据")
	}
	symbolsSummary := flattenOptimizationSymbolSummary(symbolSummaryMap)
	windowSummary.ActiveSymbols = len(symbolsSummary)
	if windowSummary.TotalRealizedNotional > 0 {
		windowSummary.RealizedReturnPct = (windowSummary.TotalRealizedPnL / windowSummary.TotalRealizedNotional) * 100
	}
	winTotal := 0
	lossTotal := 0
	for i := range symbolsSummary {
		if symbolsSummary[i].RealizedNotional > 0 {
			symbolsSummary[i].RealizedReturnPct = (symbolsSummary[i].RealizedPnL / symbolsSummary[i].RealizedNotional) * 100
		}
		decisions := symbolsSummary[i].Wins + symbolsSummary[i].Losses
		if decisions > 0 {
			symbolsSummary[i].WinRatePct = (float64(symbolsSummary[i].Wins) / float64(decisions)) * 100
		}
		if symbolsSummary[i].ClosedPositions > 0 && symbolsSummary[i].AverageHoldingMinutes > 0 {
			symbolsSummary[i].AverageHoldingMinutes = symbolsSummary[i].AverageHoldingMinutes / float64(symbolsSummary[i].ClosedPositions)
		}
		winTotal += symbolsSummary[i].Wins
		lossTotal += symbolsSummary[i].Losses
	}
	if winTotal+lossTotal > 0 {
		windowSummary.WinRatePct = (float64(winTotal) / float64(winTotal+lossTotal)) * 100
	}

	payload := optimizationContextPayload{
		StrategyID:      inst.ID,
		StrategyName:    inst.Name,
		WindowStart:     start,
		WindowEnd:       end,
		Config:          inst.Config,
		Summary:         windowSummary,
		Symbols:         symbolsSummary,
		RecentPositions: positions,
		RecentOrders:    orders,
		Markets:         markets,
	}
	return &optimizationPreparedInput{
		row:        row,
		code:       code,
		sourcePath: sourcePath,
		payload:    payload,
	}, nil
}

func (m *Manager) requestOptimizedStrategyCode(inst *StrategyInstance, input *optimizationPreparedInput) (string, string, error) {
	if input == nil {
		return "", "", fmt.Errorf("missing optimization input")
	}
	provider := strings.ToLower(strings.TrimSpace(getString(inst.Config["auto_optimize_provider"])))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(conf.C().AI.Optimizer.Provider))
	}
	defaultAPIURL := "https://api.openai.com/v1/chat/completions"
	switch provider {
	case "openrouter":
		defaultAPIURL = "https://openrouter.ai/api/v1/chat/completions"
	}
	apiURL := firstNonEmpty(
		strings.TrimSpace(getString(inst.Config["auto_optimize_api_url"])),
		strings.TrimSpace(conf.C().AI.Optimizer.APIURL),
		strings.TrimSpace(os.Getenv("AI_OPTIMIZER_API_URL")),
		defaultAPIURL,
	)
	apiKeyCandidates := []string{
		strings.TrimSpace(getString(inst.Config["auto_optimize_api_key"])),
		strings.TrimSpace(conf.C().AI.Optimizer.APIKey),
		strings.TrimSpace(os.Getenv("AI_OPTIMIZER_API_KEY")),
	}
	if provider == "openrouter" {
		apiKeyCandidates = append(apiKeyCandidates, strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")))
	}
	apiKeyCandidates = append(apiKeyCandidates, strings.TrimSpace(os.Getenv("OPENAI_API_KEY")))
	apiKey := firstNonEmpty(apiKeyCandidates...)
	model := firstNonEmpty(
		strings.TrimSpace(getString(inst.Config["auto_optimize_model"])),
		strings.TrimSpace(conf.C().AI.Optimizer.Model),
		strings.TrimSpace(os.Getenv("AI_OPTIMIZER_MODEL")),
	)
	if apiKey == "" {
		return "", "", fmt.Errorf("未配置 AI 优化器 API Key")
	}
	if model == "" {
		return "", "", fmt.Errorf("未配置 AI 优化模型名称")
	}

	ctxJSON, err := json.MarshalIndent(input.payload, "", "  ")
	if err != nil {
		return "", "", err
	}
	systemPrompt := firstNonEmpty(
		strings.TrimSpace(getString(inst.Config["auto_optimize_system_prompt"])),
		strings.TrimSpace(conf.C().AI.Optimizer.SystemPrompt),
		strings.TrimSpace(os.Getenv("AI_OPTIMIZER_SYSTEM_PROMPT")),
		"你是资深量化交易策略工程师。请基于最近3小时的持仓、订单和合约行情摘要，优化现有Python策略代码。必须保留现有项目的 websocket/redis 运行协议、run() 入口、信号输出格式和风险控制接口。优先调整参数、过滤逻辑、仓位控制和评分机制，避免破坏项目集成。优先做最小必要修改，不要重写框架。只返回完整 Python 代码，不要解释。",
	)
	userPrompt := fmt.Sprintf("当前策略代码如下：\n```python\n%s\n```\n\n最近3小时的策略上下文如下（已做压缩摘要，优先使用 summary、symbols、markets，不要假设缺失的逐笔明细）：\n```json\n%s\n```\n\n请输出优化后的完整策略 Python 源码。要求：\n1. 保持与当前项目兼容，仍通过 websocket 行情驱动并输出相同信号结构。\n2. 如果没有足够理由，不要删除现有日志、风控和 run() 主循环。\n3. 优先最小修改而不是重写整个框架。\n4. 尽量通过参数、阈值、评分权重、过滤条件和仓位控制优化表现。\n5. 不要输出 markdown 解释，不要输出测试代码，只输出策略源码。", input.code, string(ctxJSON))

	reqBody := openAIChatRequest{
		Model: model,
		Messages: []openAIChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.2,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if provider == "openrouter" || strings.Contains(strings.ToLower(apiURL), "openrouter.ai") {
		httpReferer := firstNonEmpty(
			strings.TrimSpace(getString(inst.Config["auto_optimize_http_referer"])),
			strings.TrimSpace(conf.C().AI.Optimizer.HTTPReferer),
			strings.TrimSpace(os.Getenv("OPENROUTER_HTTP_REFERER")),
			strings.TrimSpace(os.Getenv("AI_OPTIMIZER_HTTP_REFERER")),
		)
		xTitle := firstNonEmpty(
			strings.TrimSpace(getString(inst.Config["auto_optimize_app_name"])),
			strings.TrimSpace(conf.C().AI.Optimizer.AppName),
			strings.TrimSpace(os.Getenv("OPENROUTER_APP_NAME")),
			strings.TrimSpace(os.Getenv("AI_OPTIMIZER_APP_NAME")),
			"QuantyTrade",
		)
		if httpReferer != "" {
			req.Header.Set("HTTP-Referer", httpReferer)
		}
		if xTitle != "" {
			req.Header.Set("X-Title", xTitle)
		}
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("AI 接口返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}
	var parsed openAIChatResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return "", "", err
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return "", "", errors.New(strings.TrimSpace(parsed.Error.Message))
	}
	if len(parsed.Choices) == 0 {
		return "", "", fmt.Errorf("AI 接口未返回 choices")
	}
	raw := strings.TrimSpace(parsed.Choices[0].Message.Content)
	code := extractPythonCode(raw)
	summary := fmt.Sprintf(
		"provider=%s model=%s payload_symbols=%d recent_positions=%d recent_orders=%d market_summaries=%d realized_pnl=%.4f win_rate=%.2f%%",
		firstNonEmpty(provider, "openai_compatible"),
		model,
		len(input.payload.Symbols),
		len(input.payload.RecentPositions),
		len(input.payload.RecentOrders),
		len(input.payload.Markets),
		input.payload.Summary.TotalRealizedPnL,
		input.payload.Summary.WinRatePct,
	)
	return code, summary, nil
}

func (m *Manager) applyOptimizedCode(inst *StrategyInstance, input *optimizationPreparedInput, code string, run *models.StrategyOptimizationRun, summary string) (string, error) {
	if input == nil {
		return "", fmt.Errorf("missing prepared optimization input")
	}
	if database.DB == nil {
		return "", fmt.Errorf("database is not initialized")
	}
	now := time.Now()
	tpl := input.row.Template
	newVersion, appliedPath, err := m.recordStrategyVersionPublish(inst, input, code, "", run, summary)
	if err != nil {
		return "", fmt.Errorf("策略版本记录失败: %w", err)
	}
	if newVersion == nil || newVersion.ID == 0 {
		return "", fmt.Errorf("策略版本发布失败: 未生成新版本记录")
	}
	if err := database.DB.Model(&models.StrategyInstance{}).Where("id = ?", inst.ID).Updates(map[string]interface{}{
		"strategy_version_id": newVersion.ID,
		"updated_at":          now,
	}).Error; err != nil {
		return "", fmt.Errorf("策略实例绑定新版本失败: %w", err)
	}
	inst.TemplateID = tpl.ID
	inst.StrategyVersionID = &newVersion.ID
	if strings.TrimSpace(appliedPath) == "" {
		appliedPath = fmt.Sprintf("db://strategy_version/%d", newVersion.ID)
	}
	inst.Path = appliedPath
	emitStrategyLog(inst, "info", fmt.Sprintf("AI优化已发布新版本 template_id=%d version_id=%d path=%s", tpl.ID, newVersion.ID, appliedPath))
	return appliedPath, nil
}

func (m *Manager) finishOptimizationRun(inst *StrategyInstance, run *models.StrategyOptimizationRun, status string, applied bool, summary string, path string, err error) {
	finalSummary := strings.TrimSpace(summary)
	if strings.TrimSpace(path) != "" {
		if finalSummary != "" {
			finalSummary += " "
		}
		finalSummary += "path=" + strings.TrimSpace(path)
	}
	if run != nil {
		run.Status = status
		run.Applied = applied
		run.Summary = finalSummary
		run.UpdatedAt = time.Now()
		if err != nil {
			run.ErrorMessage = err.Error()
		}
		if database.DB != nil && run.ID != 0 {
			_ = database.DB.Save(run).Error
		}
	}
	if err != nil {
		if status == "skipped" {
			emitStrategyLog(inst, "info", fmt.Sprintf("AI优化跳过 status=%s reason=%v", status, err))
			return
		}
		emitStrategyLog(inst, "error", fmt.Sprintf("AI优化失败 status=%s err=%v", status, err))
		return
	}
	if applied {
		emitStrategyLog(inst, "info", fmt.Sprintf("AI优化完成 status=%s summary=%s", status, finalSummary))
	} else {
		emitStrategyLog(inst, "info", fmt.Sprintf("AI优化完成 status=%s summary=%s", status, finalSummary))
	}
}

func validatePythonStrategy(code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("empty code")
	}
	if !strings.Contains(code, "def run(") {
		return fmt.Errorf("missing run() entry")
	}
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("ai_optimize_%d.py", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, []byte(code), 0o644); err != nil {
		return err
	}
	defer os.Remove(tmpFile)
	cmd := exec.Command("python3", "-m", "py_compile", tmpFile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

func extractPythonCode(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "```") {
		start := strings.Index(raw, "\n")
		end := strings.LastIndex(raw, "```")
		if start >= 0 && end > start {
			return strings.TrimSpace(raw[start:end])
		}
	}
	if strings.Contains(raw, "```python") {
		start := strings.Index(raw, "```python")
		rest := raw[start+len("```python"):]
		if idx := strings.Index(rest, "```"); idx >= 0 {
			return strings.TrimSpace(rest[:idx])
		}
	}
	return raw
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func canWriteStrategyPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(strings.ToLower(path), "db://") {
		return false
	}
	if strings.Contains(filepath.ToSlash(path), "/_runtime/") {
		return false
	}
	abs, err := resolveStrategyPath(path)
	if err == nil {
		path = abs
	}
	strategiesDir := conf.C().Paths.StrategiesDir
	if strategiesDir == "" {
		strategiesDir = conf.Path("strategies")
	}
	absDir, err := filepath.Abs(strategiesDir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func displayPath(primary string, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func ensureOptimizationSymbolSummary(m map[string]*optimizationSymbolTradeSummary, symbol string) *optimizationSymbolTradeSummary {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		symbol = "UNKNOWN"
	}
	if v, ok := m[symbol]; ok && v != nil {
		return v
	}
	v := &optimizationSymbolTradeSummary{Symbol: symbol}
	m[symbol] = v
	return v
}

func flattenOptimizationSymbolSummary(m map[string]*optimizationSymbolTradeSummary) []optimizationSymbolTradeSummary {
	out := make([]optimizationSymbolTradeSummary, 0, len(m))
	for _, item := range m {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RealizedPnL == out[j].RealizedPnL {
			return out[i].Symbol < out[j].Symbol
		}
		return out[i].RealizedPnL > out[j].RealizedPnL
	})
	return out
}

func summarizeOptimizationCandles(symbol string, candles []exchange.Candle, start, end time.Time) optimizationMarketSummary {
	out := optimizationMarketSummary{
		Symbol:        symbol,
		CandleCount:   len(candles),
		WindowMinutes: int(end.Sub(start).Minutes()),
	}
	if len(candles) == 0 {
		return out
	}
	first := candles[0]
	last := candles[len(candles)-1]
	out.FirstClose = first.Close
	out.LastClose = last.Close
	out.High = first.High
	out.Low = first.Low
	out.MaxVolume = first.Volume
	out.LastCandleTimestamp = last.Timestamp.Format(time.RFC3339)
	out.LastCandleClose = last.Close
	out.LastCandleVolume = last.Volume

	totalVolume := 0.0
	returns := make([]float64, 0, len(candles)-1)
	for i := range candles {
		c := candles[i]
		if c.High > out.High {
			out.High = c.High
		}
		if c.Low < out.Low {
			out.Low = c.Low
		}
		if c.Volume > out.MaxVolume {
			out.MaxVolume = c.Volume
		}
		totalVolume += c.Volume
		if i > 0 {
			prev := candles[i-1].Close
			if prev > 0 {
				returns = append(returns, ((c.Close-prev)/prev)*100)
			}
		}
	}
	if len(candles) > 0 {
		out.AverageVolume = totalVolume / float64(len(candles))
	}
	if out.FirstClose > 0 {
		out.ReturnPct = ((out.LastClose - out.FirstClose) / out.FirstClose) * 100
		if out.Low > 0 {
			out.RangePct = ((out.High - out.Low) / out.Low) * 100
		}
	}
	out.VolatilityPct = stddevFloat64(returns)
	out.Last15mReturnPct = trailingReturnPct(candles, 15)
	out.Last30mReturnPct = trailingReturnPct(candles, 30)
	out.Last60mReturnPct = trailingReturnPct(candles, 60)
	out.Last15mAverageVolume = trailingAverageVolume(candles, 15)
	out.Last30mAverageVolume = trailingAverageVolume(candles, 30)
	out.Last60mAverageVolume = trailingAverageVolume(candles, 60)
	return out
}

func trailingReturnPct(candles []exchange.Candle, n int) float64 {
	if len(candles) < 2 {
		return 0
	}
	if n <= 1 {
		n = 2
	}
	if len(candles) < n {
		n = len(candles)
	}
	first := candles[len(candles)-n].Close
	last := candles[len(candles)-1].Close
	if first <= 0 {
		return 0
	}
	return ((last - first) / first) * 100
}

func trailingAverageVolume(candles []exchange.Candle, n int) float64 {
	if len(candles) == 0 {
		return 0
	}
	if n <= 0 || len(candles) < n {
		n = len(candles)
	}
	sum := 0.0
	for _, c := range candles[len(candles)-n:] {
		sum += c.Volume
	}
	return sum / float64(n)
}

func stddevFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))
	return varianceToStddev(variance)
}

func varianceToStddev(variance float64) float64 {
	if variance <= 0 {
		return 0
	}
	z := variance
	for i := 0; i < 8; i++ {
		z -= (z*z - variance) / (2 * z)
	}
	return z
}

func (m *Manager) recordStrategyVersionPublish(inst *StrategyInstance, input *optimizationPreparedInput, code string, appliedPath string, run *models.StrategyOptimizationRun, summary string) (*models.StrategyVersion, string, error) {
	if database.DB == nil || input == nil {
		return nil, "", nil
	}
	now := time.Now()
	tpl := input.row.Template
	baseVersion, err := upsertStrategyVersionRecord(inst.ID, tpl.ID, inst.OwnerID, input.code, displayPath(input.sourcePath, tpl.Path), "base", "baseline", "优化前基线版本", false, now)
	if err != nil {
		return nil, "", err
	}
	newVersion, err := upsertStrategyVersionRecord(inst.ID, tpl.ID, inst.OwnerID, code, appliedPath, "ai_optimize", safeRunTrigger(run), summary, true, now)
	if err != nil {
		return nil, "", err
	}
	if baseVersion != nil && newVersion != nil && baseVersion.ID == newVersion.ID {
		return newVersion, displayPath(appliedPath, newVersion.Path), nil
	}
	if newVersion != nil && newVersion.ID != 0 && strings.TrimSpace(newVersion.Path) == "" {
		resolvedPath := fmt.Sprintf("db://strategy_version/%d", newVersion.ID)
		if err := database.DB.Model(&models.StrategyVersion{}).Where("id = ?", newVersion.ID).
			Updates(map[string]interface{}{"path": resolvedPath, "updated_at": now}).Error; err != nil {
			return nil, "", err
		}
		newVersion.Path = resolvedPath
		newVersion.UpdatedAt = now
	}
	finalAppliedPath := displayPath(appliedPath, "")
	if strings.TrimSpace(finalAppliedPath) == "" && newVersion != nil {
		finalAppliedPath = displayPath(newVersion.Path, fmt.Sprintf("db://strategy_version/%d", newVersion.ID))
	}
	var runID *uint
	if run != nil && run.ID != 0 {
		runID = &run.ID
	}
	record := models.StrategyPublishRecord{
		StrategyID:  inst.ID,
		TemplateID:  tpl.ID,
		OwnerID:     inst.OwnerID,
		RunID:       runID,
		Status:      "applied",
		Trigger:     safeRunTrigger(run),
		AppliedPath: finalAppliedPath,
		Summary:     summary,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if baseVersion != nil && baseVersion.ID != 0 {
		record.FromVersionID = &baseVersion.ID
	}
	if newVersion != nil && newVersion.ID != 0 {
		record.ToVersionID = &newVersion.ID
	}
	if err := database.DB.Create(&record).Error; err != nil {
		return nil, "", err
	}
	return newVersion, finalAppliedPath, nil
}

func upsertStrategyVersionRecord(strategyID string, templateID uint, ownerID uint, code string, path string, source string, trigger string, summary string, isCurrent bool, now time.Time) (*models.StrategyVersion, error) {
	if database.DB == nil {
		return nil, nil
	}
	codeHash := hashText(code)
	versionHash := hashText(fmt.Sprintf("%s:%d:%s", strategyID, templateID, codeHash))
	if isCurrent {
		if err := database.DB.Model(&models.StrategyVersion{}).
			Where("strategy_id = ? AND owner_id = ?", strategyID, ownerID).
			Update("is_current", false).Error; err != nil {
			return nil, err
		}
	}
	var row models.StrategyVersion
	tx := database.DB.Where("strategy_id = ? AND template_id = ? AND code_hash = ?", strategyID, templateID, codeHash).Limit(1).Find(&row)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected > 0 && row.ID != 0 {
		updates := map[string]interface{}{
			"version_hash": versionHash,
			"code":         code,
			"path":         path,
			"source":       source,
			"trigger":      trigger,
			"summary":      summary,
			"code_size":    len(code),
			"is_current":   isCurrent,
			"updated_at":   now,
		}
		if err := database.DB.Model(&models.StrategyVersion{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			return nil, err
		}
		row.VersionHash = versionHash
		row.Code = code
		row.Path = path
		row.Source = source
		row.Trigger = trigger
		row.Summary = summary
		row.CodeSize = len(code)
		row.IsCurrent = isCurrent
		row.UpdatedAt = now
		return &row, nil
	}
	row = models.StrategyVersion{
		StrategyID:  strategyID,
		TemplateID:  templateID,
		OwnerID:     ownerID,
		VersionHash: versionHash,
		CodeHash:    codeHash,
		CodeSize:    len(code),
		Code:        code,
		Source:      source,
		Trigger:     trigger,
		Path:        path,
		Summary:     summary,
		IsCurrent:   isCurrent,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := database.DB.Create(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func safeRunTrigger(run *models.StrategyOptimizationRun) string {
	if run == nil || strings.TrimSpace(run.Trigger) == "" {
		return "manual"
	}
	return strings.TrimSpace(run.Trigger)
}
