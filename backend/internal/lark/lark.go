// Package lark 把后端 ERROR 级日志实时推送到飞书/Lark 自定义群机器人。
//
// 设计要点：
//   - 通过 logger.AddErrorSink 钩住每条 ERROR，非阻塞推入 channel（不卡日志热路径）。
//   - 后台 goroutine 消费：去重（同类错误窗口内只发一次）+ 限流（每分钟上限，超出聚合成一条）。
//   - 发送失败【只用标准 log】记录，绝不调用 logger.Error*（否则无限循环）。
//   - 群机器人若开了"签名校验"，配 secret 即自动带 HMAC-SHA256 签名。
package lark

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/logger"
)

// Config 来自 conf.LarkConfig。
type Config struct {
	Enabled            bool
	WebhookURL         string
	Secret             string // 可选：群机器人开启签名校验时填
	MinIntervalSeconds int    // 同类错误去重窗口（默认 5s）
	MaxPerMinute       int    // 每分钟最多发送条数，超出聚合（默认 12）
}

type Notifier struct {
	cfg        Config
	httpClient *http.Client
	ch         chan string
	host       string

	mu           sync.Mutex
	recent       map[string]time.Time // 去重：错误指纹 -> 上次发送时间
	windowStart  time.Time            // 限流窗口起点
	sentInWindow int                  // 本窗口已发送条数
	suppressed   int                  // 被限流/缓冲满抑制的条数（每分钟汇报一次）
}

var current *Notifier

// Start 启动 Lark 告警。enabled=false 或 webhook 为空 → 返回 nil（安全 no-op）。
func Start(cfg Config) *Notifier {
	if !cfg.Enabled || strings.TrimSpace(cfg.WebhookURL) == "" {
		log.Printf("[lark] disabled (enabled=%v webhook_present=%v)", cfg.Enabled, strings.TrimSpace(cfg.WebhookURL) != "")
		return nil
	}
	if cfg.MinIntervalSeconds <= 0 {
		cfg.MinIntervalSeconds = 5
	}
	if cfg.MaxPerMinute <= 0 {
		cfg.MaxPerMinute = 12
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	n := &Notifier{
		cfg:         cfg,
		httpClient:  &http.Client{Timeout: 8 * time.Second},
		ch:          make(chan string, 256),
		host:        host,
		recent:      map[string]time.Time{},
		windowStart: time.Now(),
	}
	current = n
	go n.loop()

	// 注册 ERROR 钩子：每条 ERROR 非阻塞推入 channel。
	logger.AddErrorSink(func(msg string) {
		select {
		case n.ch <- msg:
		default:
			// 缓冲满 → 丢弃并计数（绝不阻塞日志热路径）
			n.mu.Lock()
			n.suppressed++
			n.mu.Unlock()
		}
	})
	log.Printf("[lark] error alerting enabled host=%s max/min=%d dedup=%ds", host, cfg.MaxPerMinute, cfg.MinIntervalSeconds)
	return n
}

func (n *Notifier) loop() {
	flush := time.NewTicker(60 * time.Second)
	defer flush.Stop()
	for {
		select {
		case msg := <-n.ch:
			n.handle(msg)
		case <-flush.C:
			n.reportSuppressed()
		}
	}
}

func (n *Notifier) handle(msg string) {
	key := dedupKey(msg)
	now := time.Now()

	n.mu.Lock()
	// 去重
	if t, ok := n.recent[key]; ok && now.Sub(t) < time.Duration(n.cfg.MinIntervalSeconds)*time.Second {
		n.mu.Unlock()
		return
	}
	n.recent[key] = now
	if len(n.recent) > 512 { // 清理过期去重记录
		for k, t := range n.recent {
			if now.Sub(t) > 5*time.Minute {
				delete(n.recent, k)
			}
		}
	}
	// 限流窗口
	if now.Sub(n.windowStart) >= time.Minute {
		n.windowStart = now
		n.sentInWindow = 0
	}
	if n.sentInWindow >= n.cfg.MaxPerMinute {
		n.suppressed++
		n.mu.Unlock()
		return
	}
	n.sentInWindow++
	n.mu.Unlock()

	n.send(n.formatError(msg))
}

func (n *Notifier) reportSuppressed() {
	n.mu.Lock()
	s := n.suppressed
	n.suppressed = 0
	n.mu.Unlock()
	if s > 0 {
		n.send(fmt.Sprintf("⚠️ QuantyTrade · 过去 1 分钟另有 %d 条 ERROR 因限流/去重被抑制（host=%s）", s, n.host))
	}
}

// dedupKey 把同类错误（仅尾部数字/时间不同）聚合到同一指纹：截断到前 160 字符。
func dedupKey(msg string) string {
	m := strings.TrimSpace(msg)
	if len(m) > 160 {
		m = m[:160]
	}
	return m
}

func (n *Notifier) formatError(msg string) string {
	ts := time.Now().Format("2006-01-02 15:04:05")
	body := strings.TrimSpace(msg)
	if len(body) > 1500 {
		body = body[:1500] + " …(truncated)"
	}
	return fmt.Sprintf("🚨 QuantyTrade ERROR\nhost: %s\ntime: %s\n%s", n.host, ts, body)
}

func (n *Notifier) send(text string) {
	b := buildPayload(n.cfg, text)
	req, err := http.NewRequest(http.MethodPost, n.cfg.WebhookURL, bytes.NewReader(b))
	if err != nil {
		log.Printf("[lark] build req err=%v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.httpClient.Do(req)
	if err != nil {
		// ⚠️ 只用标准 log，绝不 logger.Errorf（防无限循环）
		log.Printf("[lark] send err=%v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[lark] non-200 status=%d", resp.StatusCode)
	}
}

// buildPayload 构造 Lark 文本消息体（带可选签名）。
func buildPayload(cfg Config, text string) []byte {
	payload := map[string]interface{}{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	}
	if s := strings.TrimSpace(cfg.Secret); s != "" {
		tsSec := strconv.FormatInt(time.Now().Unix(), 10)
		if sign, err := genSign(tsSec, s); err == nil {
			payload["timestamp"] = tsSec
			payload["sign"] = sign
		}
	}
	b, _ := json.Marshal(payload)
	return b
}

// ─── 同步告警：可在异步 Start 之前使用（如启动期 DB 致命错误，那时 loop 还没起） ───

var (
	syncMu     sync.Mutex
	syncCfg    Config
	syncClient = &http.Client{Timeout: 6 * time.Second}
)

// Configure 存一份配置，使 AlertSync 在 Start 之前也能用（启动早期告警）。
// main 在 database.InitDB 之前调用它。
func Configure(cfg Config) {
	syncMu.Lock()
	syncCfg = cfg
	syncMu.Unlock()
}

// AlertSync 同步、阻塞、尽力发一条告警。未启用/无 webhook 时静默 no-op。
// 适合在进程即将退出（log.Fatal）前调用，确保消息真的发出去。
func AlertSync(text string) {
	syncMu.Lock()
	cfg := syncCfg
	syncMu.Unlock()
	if !cfg.Enabled || strings.TrimSpace(cfg.WebhookURL) == "" {
		return
	}
	b := buildPayload(cfg, text)
	req, err := http.NewRequest(http.MethodPost, cfg.WebhookURL, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := syncClient.Do(req)
	if err != nil {
		log.Printf("[lark] sync send err=%v", err)
		return
	}
	_ = resp.Body.Close()
}

// SendInfo 主动发一条非错误通知（如服务启动）。Lark 未启用时静默忽略。
func SendInfo(text string) {
	if current == nil {
		return
	}
	current.send("ℹ️ QuantyTrade · " + text)
}

// genSign 计算 Lark 自定义机器人签名：
// HMAC-SHA256(key = "{timestamp}\n{secret}", data = "")，再 base64。
func genSign(timestamp, secret string) (string, error) {
	strToSign := timestamp + "\n" + secret
	h := hmac.New(sha256.New, []byte(strToSign))
	if _, err := h.Write([]byte("")); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
