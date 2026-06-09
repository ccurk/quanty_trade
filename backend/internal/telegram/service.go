package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"quanty_trade/internal/strategy"
)

type Service struct {
	token       string
	baseURL     string
	httpClient  *http.Client
	pollTimeout int
	manager     *strategy.Manager
}

var (
	currentServiceMu sync.RWMutex
	currentService   *Service
)

type updatesResponse struct {
	OK          bool             `json:"ok"`
	Result      []telegramUpdate `json:"result"`
	Description string           `json:"description"`
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int64         `json:"message_id"`
	Text      string        `json:"text"`
	Chat      telegramChat  `json:"chat"`
	From      *telegramUser `json:"from"`
}

type telegramChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

func Start(ctx context.Context, mgr *strategy.Manager) *Service {
	cfg := conf.C().Telegram
	token := strings.TrimSpace(cfg.BotToken)
	logger.Infof("telegram bootstrap enabled=%t token_present=%t token_len=%d poll_timeout=%d", cfg.Enabled, token != "", len(token), cfg.PollTimeoutSeconds)
	if !cfg.Enabled {
		setCurrentService(nil)
		logger.Infof("telegram disabled")
		return nil
	}
	if token == "" {
		setCurrentService(nil)
		logger.Errorf("telegram enabled but bot token is empty")
		return nil
	}
	pollTimeout := cfg.PollTimeoutSeconds
	if pollTimeout <= 0 {
		pollTimeout = 30
	}
	svc := &Service{
		token:       token,
		baseURL:     "https://api.telegram.org/bot" + token,
		httpClient:  &http.Client{Timeout: time.Duration(pollTimeout+15) * time.Second},
		pollTimeout: pollTimeout,
		manager:     mgr,
	}
	if mgr != nil {
		mgr.SetNotifier(svc)
	}
	setCurrentService(svc)
	logger.Infof("telegram service started poll_timeout=%d token_len=%d", pollTimeout, len(token))
	go svc.run(ctx)
	return svc
}

func NotifySystemEvent(title string, lines ...string) {
	getCurrentService().notifySystemEvent(title, lines...)
}

func NotifyBusinessEvent(category string, title string, lines ...string) {
	getCurrentService().notifyBusinessEvent(category, title, lines...)
}

func (s *Service) NotifyTradeOpened(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, takeProfit float64, stopLoss float64, status string) {
	notional := qty * price
	lines := []string{
		cardKV("策略", displayValue(strategyName, strategyID)),
		cardKV("策略ID", displayValue(strategyID, "-")),
		cardKV("交易所", displayValue(exchangeName, "-")),
		cardKV("交易对", displayValue(symbol, "-")),
		cardKV("方向", formatTradeSide(side)),
		cardKV("状态", formatTradeStatus(status)),
		"──── 交易信息 ────",
		cardKV("数量", formatFloat(qty)),
		cardKV("成交价", formatFloat(price)),
		cardKV("名义价值", formatFloat(notional)),
		cardKV("止盈", formatTargetPrice(takeProfit, price)),
		cardKV("止损", formatTargetPrice(stopLoss, price)),
	}
	s.broadcast(buildTelegramCard("📈", "开仓成交", lines...))
}

func (s *Service) NotifyTradeClosed(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, status string, reason string, metrics *strategy.TradeCloseMetrics) {
	notional := qty * price
	exitPrice := price
	if metrics != nil && metrics.ExitPrice > 0 {
		exitPrice = metrics.ExitPrice
	}
	// 胜负标签放标题，第一眼就能看到结果
	icon, title := "📉", "平仓成交"
	if metrics != nil {
		switch {
		case metrics.RealizedPnL > 0:
			icon, title = "🟢", "盈利平仓"
		case metrics.RealizedPnL < 0:
			icon, title = "🔴", "亏损平仓"
		case metrics.RealizedPnL == 0 && (metrics.EntryPrice > 0 || metrics.ExitPrice > 0):
			icon, title = "⚪", "持平平仓"
		}
	}
	lines := []string{
		cardKV("策略", displayValue(strategyName, strategyID)),
		cardKV("策略ID", displayValue(strategyID, "-")),
		cardKV("交易所", displayValue(exchangeName, "-")),
		cardKV("交易对", displayValue(symbol, "-")),
		cardKV("方向", formatTradeSide(side)),
		cardKV("状态", formatTradeStatus(status)),
		cardKV("原因", formatCloseReason(reason)),
		"──── 成交信息 ────",
		cardKV("数量", formatFloat(qty)),
		cardKV("平仓价", formatFloat(exitPrice)),
		cardKV("名义价值", formatFloat(notional)),
	}
	if metrics != nil {
		lines = append(lines,
			"──── 收益信息 ────",
			cardKV("开仓价", formatFloat(metrics.EntryPrice)),
			cardKV("平仓价", formatFloat(metrics.ExitPrice)),
			cardKV("已实现收益（毛）", formatSignedFloat(metrics.RealizedPnL)),
			cardKV("收益率（仓位）", formatPercent(metrics.RealizedReturnPct)),
		)
		if metrics.Leverage > 0 {
			lines = append(lines,
				cardKV("杠杆", fmt.Sprintf("%dx", int(metrics.Leverage))),
				cardKV("保证金", formatFloat(metrics.Margin)),
				cardKV("ROI（保证金）", formatPercent(metrics.MarginReturnPct)),
			)
		} else {
			lines = append(lines, cardKV("入场本金", formatFloat(metrics.RealizedNotional)))
		}
		if metrics.FeeEstimate > 0 {
			lines = append(lines,
				cardKV("手续费(预估)", "-"+formatFloat(metrics.FeeEstimate)),
				cardKV("净收益(预估)", formatSignedFloat(metrics.NetPnL)),
			)
		}
		lines = append(lines, cardKV("持仓时长", formatDuration(metrics.HoldingDuration)))
	}
	s.broadcast(buildTelegramCard(icon, title, lines...))
}

func (s *Service) NotifyStrategyStatus(ownerID uint, strategyID string, strategyName string, status string) {
	status = strings.TrimSpace(status)
	if status != "running" && status != "stopped" && status != "error" {
		return
	}
	lines := []string{
		cardKV("策略", displayValue(strategyName, strategyID)),
		cardKV("策略ID", displayValue(strategyID, "-")),
		cardKV("状态", formatStrategyStatus(status)),
	}
	s.broadcast(buildTelegramCard("🤖", "策略状态变更", lines...))
}

func (s *Service) NotifyAIOptimization(ownerID uint, strategyID string, strategyName string, status string, trigger string, summary string, detail string) {
	status = strings.TrimSpace(status)
	if status == "" {
		return
	}
	title := "AI优化通知"
	switch status {
	case "running":
		title = "AI优化开始"
	case "completed":
		title = "AI优化完成"
	case "skipped":
		title = "AI优化跳过"
	case "failed":
		title = "AI优化失败"
	}
	lines := []string{
		cardKV("策略", displayValue(strategyName, strategyID)),
		cardKV("策略ID", displayValue(strategyID, "-")),
		cardKV("状态", formatAIOptimizeStatus(status)),
		cardKV("触发方式", displayValue(trigger, "-")),
	}
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		lines = append(lines, "──── 优化摘要 ────", trimmed)
	}
	if trimmed := strings.TrimSpace(detail); trimmed != "" {
		lines = append(lines, "──── 详细信息 ────", trimmed)
	}
	s.broadcast(buildTelegramCard("🧠", title, lines...))
}

func (s *Service) notifySystemEvent(title string, lines ...string) {
	if s == nil {
		return
	}
	all := []string{"分类：系统", "标题：" + displayValue(strings.TrimSpace(title), "系统事件")}
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			all = append(all, trimmed)
		}
	}
	s.broadcast(buildTelegramCard("🟦", "系统通知", all...))
}

func (s *Service) notifyBusinessEvent(category string, title string, lines ...string) {
	if s == nil {
		return
	}
	all := []string{
		cardKV("分类", displayValue(strings.TrimSpace(category), "business")),
		cardKV("标题", displayValue(strings.TrimSpace(title), "业务事件")),
	}
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			all = append(all, trimmed)
		}
	}
	s.broadcast(buildTelegramCard("🟨", "业务通知", all...))
}

func (s *Service) run(ctx context.Context) {
	offset := s.loadOffset()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, nextOffset, err := s.fetchUpdates(ctx, offset)
		if err != nil {
			logger.Errorf("telegram poll failed err=%v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		offset = nextOffset
		for _, upd := range updates {
			s.handleUpdate(upd)
		}
		if offset > 0 {
			s.saveOffset(offset - 1)
		}
	}
}

func (s *Service) fetchUpdates(ctx context.Context, offset int64) ([]telegramUpdate, int64, error) {
	values := url.Values{}
	values.Set("timeout", strconv.Itoa(s.pollTimeout))
	if offset > 0 {
		values.Set("offset", strconv.FormatInt(offset, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/getUpdates?"+values.Encode(), nil)
	if err != nil {
		return nil, offset, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, offset, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, offset, err
	}
	var parsed updatesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, offset, err
	}
	if !parsed.OK {
		return nil, offset, fmt.Errorf("telegram getUpdates failed: %s", parsed.Description)
	}
	nextOffset := offset
	for _, upd := range parsed.Result {
		if upd.UpdateID >= nextOffset {
			nextOffset = upd.UpdateID + 1
		}
	}
	if len(parsed.Result) > 0 {
		logger.Infof("telegram fetched updates count=%d next_offset=%d", len(parsed.Result), nextOffset)
	}
	return parsed.Result, nextOffset, nil
}

func (s *Service) handleUpdate(upd telegramUpdate) {
	msg := upd.Message
	if msg == nil {
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	logger.Infof("telegram handle update update_id=%d chat_id=%d text=%s", upd.UpdateID, msg.Chat.ID, previewTelegramText(text))
	s.upsertSubscriber(msg)
	reply := s.handleCommand(msg, text)
	if strings.TrimSpace(reply) == "" {
		return
	}
	if err := s.sendText(msg.Chat.ID, reply); err != nil {
		logger.Errorf("telegram send reply failed chat_id=%d err=%v", msg.Chat.ID, err)
	}
}

func (s *Service) handleCommand(msg *telegramMessage, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	cmd := strings.ToLower(strings.TrimSpace(fields[0]))
	switch cmd {
	case "/start":
		return s.helpMessage(msg.Chat.ID)
	case "/help":
		return s.helpMessage(msg.Chat.ID)
	case "/id":
		return fmt.Sprintf("当前 Chat ID：%d", msg.Chat.ID)
	case "/mute":
		s.setSubscriberEnabled(msg.Chat.ID, false)
		return "已关闭当前聊天的交易通知"
	case "/unmute":
		s.setSubscriberEnabled(msg.Chat.ID, true)
		return "已开启当前聊天的交易通知"
	case "/strategies":
		return s.listStrategies()
	case "/run", "/start_strategy":
		return s.startStrategyByTarget(strings.TrimSpace(strings.TrimPrefix(text, fields[0])))
	case "/stop", "/stop_strategy":
		return s.stopStrategyByTarget(strings.TrimSpace(strings.TrimPrefix(text, fields[0])), false)
	case "/force_stop":
		return s.stopStrategyByTarget(strings.TrimSpace(strings.TrimPrefix(text, fields[0])), true)
	}
	if strings.HasPrefix(text, "启动 ") {
		return s.startStrategyByTarget(strings.TrimSpace(strings.TrimPrefix(text, "启动 ")))
	}
	if strings.HasPrefix(text, "停止 ") {
		return s.stopStrategyByTarget(strings.TrimSpace(strings.TrimPrefix(text, "停止 ")), false)
	}
	if strings.HasPrefix(text, "强停 ") {
		return s.stopStrategyByTarget(strings.TrimSpace(strings.TrimPrefix(text, "强停 ")), true)
	}
	return s.helpMessage(msg.Chat.ID)
}

func (s *Service) startStrategyByTarget(target string) string {
	if strings.TrimSpace(target) == "" {
		return "用法：/run 策略ID或策略名"
	}
	inst, err := s.manager.FindStrategyForCommand(target)
	if err != nil {
		return err.Error()
	}
	if err := s.manager.StartStrategy(inst.ID); err != nil {
		return "启动失败：" + err.Error()
	}
	return fmt.Sprintf("已提交启动请求\n策略：%s\n策略ID：%s", displayValue(inst.Name, inst.ID), inst.ID)
}

func (s *Service) stopStrategyByTarget(target string, force bool) string {
	if strings.TrimSpace(target) == "" {
		if force {
			return "用法：/force_stop 策略ID或策略名"
		}
		return "用法：/stop 策略ID或策略名"
	}
	inst, err := s.manager.FindStrategyForCommand(target)
	if err != nil {
		return err.Error()
	}
	if err := s.manager.StopStrategy(inst.ID, force); err != nil {
		return "停止失败：" + err.Error()
	}
	if force {
		return fmt.Sprintf("已提交强制停止请求\n策略：%s\n策略ID：%s", displayValue(inst.Name, inst.ID), inst.ID)
	}
	return fmt.Sprintf("已提交停止请求\n策略：%s\n策略ID：%s", displayValue(inst.Name, inst.ID), inst.ID)
}

func (s *Service) listStrategies() string {
	if s.manager == nil {
		return "策略管理器未初始化"
	}
	items := s.manager.ListStrategies(0, true)
	if len(items) == 0 {
		return "当前没有可控制的策略"
	}
	lines := []string{"📋 策略列表"}
	for _, inst := range items {
		if inst == nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("• %s | %s | %s", displayValue(inst.Name, inst.ID), inst.ID, inst.Status))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) helpMessage(chatID int64) string {
	return strings.Join([]string{
		"已绑定 Telegram 机器人",
		fmt.Sprintf("当前 Chat ID：%d", chatID),
		"/strategies 查看策略列表",
		"/run 策略ID或策略名",
		"/stop 策略ID或策略名",
		"/force_stop 策略ID或策略名",
		"/mute 关闭通知",
		"/unmute 开启通知",
		"/id 查看当前 Chat ID",
	}, "\n")
}

func (s *Service) upsertSubscriber(msg *telegramMessage) {
	if database.DB == nil || msg == nil {
		return
	}
	username := msg.Chat.Username
	firstName := msg.Chat.FirstName
	if msg.From != nil {
		if strings.TrimSpace(username) == "" {
			username = msg.From.Username
		}
		if strings.TrimSpace(firstName) == "" {
			firstName = msg.From.FirstName
		}
	}
	var row models.TelegramSubscriber
	err := database.DB.Where("chat_id = ?", msg.Chat.ID).First(&row).Error
	if err == nil {
		_ = database.DB.Model(&row).Updates(map[string]interface{}{
			"username":   username,
			"first_name": firstName,
			"enabled":    true,
			"updated_at": time.Now(),
		}).Error
		logger.Infof("telegram subscriber updated chat_id=%d username=%s enabled=true", msg.Chat.ID, username)
		return
	}
	_ = database.DB.Create(&models.TelegramSubscriber{
		ChatID:    msg.Chat.ID,
		Username:  username,
		FirstName: firstName,
		Enabled:   true,
	}).Error
	logger.Infof("telegram subscriber created chat_id=%d username=%s enabled=true", msg.Chat.ID, username)
}

func (s *Service) setSubscriberEnabled(chatID int64, enabled bool) {
	if database.DB == nil {
		return
	}
	_ = database.DB.Model(&models.TelegramSubscriber{}).
		Where("chat_id = ?", chatID).
		Updates(map[string]interface{}{"enabled": enabled, "updated_at": time.Now()}).Error
}

func (s *Service) broadcast(text string) {
	if s == nil || database.DB == nil || strings.TrimSpace(text) == "" {
		return
	}
	var rows []models.TelegramSubscriber
	if err := database.DB.Where("enabled = ?", true).Find(&rows).Error; err != nil {
		logger.Errorf("telegram load subscribers failed err=%v", err)
		return
	}
	logger.Infof("telegram broadcast subscribers=%d text=%s", len(rows), previewTelegramText(text))
	for _, row := range rows {
		if row.ChatID == 0 {
			continue
		}
		if err := s.sendText(row.ChatID, text); err != nil {
			logger.Errorf("telegram push failed chat_id=%d err=%v", row.ChatID, err)
		}
	}
}

func (s *Service) sendText(chatID int64, text string) error {
	if s == nil || chatID == 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	form := url.Values{}
	form.Set("chat_id", strconv.FormatInt(chatID, 10))
	form.Set("text", text)
	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return err
	}
	if !parsed.OK {
		return fmt.Errorf("telegram sendMessage failed: %s", parsed.Description)
	}
	logger.Infof("telegram send success chat_id=%d text=%s", chatID, previewTelegramText(text))
	return nil
}

func previewTelegramText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "\n", " | ")
	if len(text) > 160 {
		return text[:160] + "..."
	}
	return text
}

func setCurrentService(svc *Service) {
	currentServiceMu.Lock()
	currentService = svc
	currentServiceMu.Unlock()
}

func getCurrentService() *Service {
	currentServiceMu.RLock()
	defer currentServiceMu.RUnlock()
	return currentService
}

func (s *Service) loadOffset() int64 {
	if database.DB == nil {
		return 0
	}
	var state models.TelegramBotState
	if err := database.DB.Order("id asc").First(&state).Error; err != nil {
		return 0
	}
	if state.LastUpdateID < 0 {
		return 0
	}
	return state.LastUpdateID + 1
}

func (s *Service) saveOffset(lastUpdateID int64) {
	if database.DB == nil {
		return
	}
	var state models.TelegramBotState
	err := database.DB.Order("id asc").First(&state).Error
	if err == nil {
		_ = database.DB.Model(&state).Updates(map[string]interface{}{
			"last_update_id": lastUpdateID,
			"updated_at":     time.Now(),
		}).Error
		return
	}
	_ = database.DB.Create(&models.TelegramBotState{
		LastUpdateID: lastUpdateID,
	}).Error
}

func displayValue(v string, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func formatSignedFloat(v float64) string {
	if v > 0 {
		return "+" + formatFloat(v)
	}
	return formatFloat(v)
}

func formatPercent(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.2f%%", v)
	}
	return fmt.Sprintf("%.2f%%", v)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	parts := make([]string, 0, 3)
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if s > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", s))
	}
	return strings.Join(parts, " ")
}

func buildTelegramCard(icon string, title string, lines ...string) string {
	out := []string{
		fmt.Sprintf("%s %s", strings.TrimSpace(icon), displayValue(strings.TrimSpace(title), "通知")),
		"━━━━━━━━━━━━",
	}
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	out = append(out, "━━━━━━━━━━━━")
	out = append(out, "时间："+time.Now().Format("2006-01-02 15:04:05"))
	return strings.Join(out, "\n")
}

func cardKV(key string, value string) string {
	return fmt.Sprintf("%s：%s", strings.TrimSpace(key), displayValue(strings.TrimSpace(value), "-"))
}

func formatTradeSide(side string) string {
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "buy", "long":
		return "做多 / Buy"
	case "sell", "short":
		return "做空 / Sell"
	default:
		return displayValue(side, "-")
	}
}

func formatTradeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "filled":
		return "已成交"
	case "requested":
		return "已提交"
	case "partially_filled":
		return "部分成交"
	case "failed":
		return "失败"
	case "rejected":
		return "已拒绝"
	case "canceled", "cancelled":
		return "已取消"
	default:
		return displayValue(status, "-")
	}
}

func formatStrategyStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "运行中"
	case "stopped":
		return "已停止"
	case "error":
		return "异常"
	default:
		return displayValue(status, "-")
	}
}

func formatAIOptimizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "执行中"
	case "completed":
		return "已完成"
	case "skipped":
		return "已跳过"
	case "failed":
		return "失败"
	default:
		return displayValue(status, "-")
	}
}

func formatCloseReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "strategy_close":
		return "策略主动平仓"
	case "take_profit", "tp":
		return "触发止盈"
	case "stop_loss", "sl":
		return "触发止损"
	case "roi_guard":
		return "ROI 风控平仓"
	default:
		return displayValue(reason, "close")
	}
}

func formatTargetPrice(target float64, entry float64) string {
	if target <= 0 {
		return "-"
	}
	if entry <= 0 {
		return formatFloat(target)
	}
	pct := ((target - entry) / entry) * 100
	if pct >= 0 {
		return fmt.Sprintf("%s (+%.2f%%)", formatFloat(target), pct)
	}
	return fmt.Sprintf("%s (%.2f%%)", formatFloat(target), pct)
}
