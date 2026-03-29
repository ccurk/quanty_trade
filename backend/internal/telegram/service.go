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
	if !cfg.Enabled {
		logger.Infof("telegram disabled")
		return nil
	}
	if token == "" {
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
	logger.Infof("telegram service started poll_timeout=%d", pollTimeout)
	go svc.run(ctx)
	return svc
}

func (s *Service) NotifyTradeOpened(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, takeProfit float64, stopLoss float64, status string) {
	text := strings.Join([]string{
		"📈 开仓通知",
		"策略：" + displayValue(strategyName, strategyID),
		"策略ID：" + displayValue(strategyID, "-"),
		"交易所：" + displayValue(exchangeName, "-"),
		"交易对：" + displayValue(symbol, "-"),
		"方向：" + displayValue(side, "-"),
		"数量：" + formatFloat(qty),
		"成交价：" + formatFloat(price),
		"止盈：" + formatFloat(takeProfit),
		"止损：" + formatFloat(stopLoss),
		"状态：" + displayValue(status, "-"),
		"时间：" + time.Now().Format("2006-01-02 15:04:05"),
	}, "\n")
	s.broadcast(text)
}

func (s *Service) NotifyTradeClosed(ownerID uint, strategyID string, strategyName string, exchangeName string, symbol string, side string, qty float64, price float64, status string, reason string) {
	text := strings.Join([]string{
		"📉 平仓通知",
		"策略：" + displayValue(strategyName, strategyID),
		"策略ID：" + displayValue(strategyID, "-"),
		"交易所：" + displayValue(exchangeName, "-"),
		"交易对：" + displayValue(symbol, "-"),
		"方向：" + displayValue(side, "-"),
		"数量：" + formatFloat(qty),
		"成交价：" + formatFloat(price),
		"状态：" + displayValue(status, "-"),
		"原因：" + displayValue(reason, "close"),
		"时间：" + time.Now().Format("2006-01-02 15:04:05"),
	}, "\n")
	s.broadcast(text)
}

func (s *Service) NotifyStrategyStatus(ownerID uint, strategyID string, strategyName string, status string) {
	status = strings.TrimSpace(status)
	if status != "running" && status != "stopped" && status != "error" {
		return
	}
	text := strings.Join([]string{
		"🤖 策略状态通知",
		"策略：" + displayValue(strategyName, strategyID),
		"策略ID：" + displayValue(strategyID, "-"),
		"状态：" + displayValue(status, "-"),
		"时间：" + time.Now().Format("2006-01-02 15:04:05"),
	}, "\n")
	s.broadcast(text)
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
		return
	}
	_ = database.DB.Create(&models.TelegramSubscriber{
		ChatID:    msg.Chat.ID,
		Username:  username,
		FirstName: firstName,
		Enabled:   true,
	}).Error
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
	return nil
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
