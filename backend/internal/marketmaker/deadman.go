package marketmaker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"quanty_trade/internal/logger"
)

// deadman.go implements the exchange-side dead-man's switch for gate. The engine
// refreshes a 30s auto-cancel countdown every 15s; if the engine (or the whole
// process) dies and stops refreshing, gate auto-cancels all the managed pairs'
// resting orders when the countdown lapses. This is the ONLY fail-safe that
// survives a process crash — an in-process watchdog dies with the process.

var deadmanHTTP = &http.Client{Timeout: 10 * time.Second}

// gateCountdownCancel arms (timeout>0) or clears (timeout=0) gate's auto-cancel
// countdown for currencyPair. POST /api/v4/spot/countdown_cancel_all (HMAC-SHA512).
func gateCountdownCancel(timeout int, currencyPair string) error {
	key, secret := os.Getenv("MM_GATE_API_KEY"), os.Getenv("MM_GATE_API_SECRET")
	if key == "" || secret == "" {
		return fmt.Errorf("gate key 未配置")
	}
	const host = "https://api.gateio.ws"
	const path = "/api/v4/spot/countdown_cancel_all"
	body, _ := json.Marshal(map[string]interface{}{"timeout": timeout, "currency_pair": currencyPair})
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	bh := sha512.Sum512(body)
	payload := "POST\n" + path + "\n\n" + hex.EncodeToString(bh[:]) + "\n" + ts
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte(payload))
	sign := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, host+path, bytes.NewReader(body))
	req.Header.Set("KEY", key)
	req.Header.Set("Timestamp", ts)
	req.Header.Set("SIGN", sign)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := deadmanHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("countdown_cancel_all HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return nil
}

// runDeadMansSwitch keeps gate's auto-cancel countdown alive for the managed gate
// pairs. On any death (crash/hang/shutdown) it stops refreshing → gate cancels
// within ~30s. Only gate pairs are covered (only gate supports this). Live only.
func (e *Engine) runDeadMansSwitch(ctx context.Context) {
	var gatePairs []string
	for _, p := range e.cfg.Pairs {
		if strings.EqualFold(p.Exec, "gate") {
			gatePairs = append(gatePairs, p.ExecSymbol)
		}
	}
	if len(gatePairs) == 0 {
		return
	}
	refresh := func(timeout int) {
		for _, pair := range gatePairs {
			if err := gateCountdownCancel(timeout, pair); err != nil {
				logger.Warnf("[mm] 死人开关刷新失败 %s: %v", pair, err)
			}
		}
	}
	refresh(30)
	logger.Infof("[mm] 死人开关启动(gate countdown_cancel_all · %d 对 · 15s 刷新/30s 超时)", len(gatePairs))
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// 停止刷新 → gate 倒计时到点自动撤单(与 Stop 主动撤单构成双保险)。
			return
		case <-t.C:
			refresh(30)
		}
	}
}
