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
	"sync"
	"time"

	"quanty_trade/internal/logger"
)

// deadman.go implements the exchange-side dead-man's switch for gate. The engine
// refreshes a 30s auto-cancel countdown every 15s; if the engine (or the whole
// process) dies and stops refreshing, gate auto-cancels all the managed pairs'
// resting orders when the countdown lapses. This is the ONLY fail-safe that
// survives a process crash — an in-process watchdog dies with the process.

var deadmanHTTP = &http.Client{Timeout: 10 * time.Second}

// deadmanHost 是 gate REST 的地址。做成变量只为了让测试能把它指向一台【假交易所】
// (回 5xx / 断连 / 超时),生产上永远是这个默认值。
var deadmanHost = "https://api.gateio.ws"

// gateCountdownCancel arms (timeout>0) or clears (timeout=0) gate's auto-cancel
// countdown for currencyPair. POST /api/v4/spot/countdown_cancel_all (HMAC-SHA512).
func gateCountdownCancel(timeout int, currencyPair string) error {
	key, secret := os.Getenv("MM_GATE_API_KEY"), os.Getenv("MM_GATE_API_SECRET")
	if key == "" || secret == "" {
		return fmt.Errorf("gate key 未配置")
	}
	host := deadmanHost
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

const (
	// deadmanTimeout 是交易所侧倒计时的长度:最后一次 arm 成功之后,gate 最多再等
	// 这么久就会自动撤掉该 pair 的挂单。
	//
	// 它同时是【一次成功 arm 的覆盖时长】:超过它还没有新的成功,交易所那边的倒计时
	// 已经到点,覆盖就不存在了 —— 所以 covered() 用时间判,而不是用"上一发有没有报错"判。
	// 好处是一次偶发失败不会立刻掐掉报价(上一次成功仍在覆盖期内),而连续失败会
	// 自己过期,不需要另写一套"连续失败几次算失效"的逻辑。
	deadmanTimeout = 30 * time.Second
	// deadmanRefresh 是刷新间隔,取超时的一半:允许连丢一发刷新而不失去覆盖。
	deadmanRefresh = 15 * time.Second
)

// deadmanCovers 是"这个场馆有没有交易所侧死人开关"的唯一判据。
//
// 判的是【适配器自己报的名字】而不是配置里的 exec 名字:配置可以把 gate 那条腿
// 叫成任何名字(exec[].name 是自由字符串),按配置名判会让改个名字就静悄悄地
// 丢掉死人开关覆盖 —— 而报价那边正是拿这同一个判据决定要不要拒绝报价的。
func deadmanCovers(ex ExecExchange) bool {
	return ex != nil && strings.EqualFold(ex.Name(), "gate")
}

// deadmanArm 是「交易所侧倒计时到底 arm 上了没有」的凭证,按 symbol 记最近一次
// 【成功】的时刻。
//
// 【为什么闸判的是它】台账 #227 同一套理由:判据必须是数据,而且零值 = 未证实。
// 没有记录 = 没覆盖;记录过期 = 没覆盖;连 deadmanArm 本身是 nil(Engine 不经
// Start 构造)也 = 没覆盖。于是"arm 报错"、"忘了 arm"、"arm 的 goroutine 没起来"
// 三种情况自动同归一路 —— 都是"未证实",都不许报价。
type deadmanArm struct {
	mu sync.Mutex
	ok map[string]time.Time
}

func (d *deadmanArm) mark(symbol string, now time.Time) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ok == nil {
		d.ok = map[string]time.Time{}
	}
	d.ok[symbol] = now
}

// covered:这个 symbol 此刻有没有被交易所侧倒计时兜住。
func (d *deadmanArm) covered(symbol string, now time.Time) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.ok[symbol]
	return ok && now.Sub(t) < deadmanTimeout
}

// runDeadMansSwitch keeps gate's auto-cancel countdown alive for the managed gate
// pairs. On any death (crash/hang/shutdown) it stops refreshing → gate cancels
// within ~30s. Only gate pairs are covered (only gate supports this). Live only.
//
// 【arm 失败 = 该 pair 不报价】原来每个 pair 的 arm 失败只打一行 Warn,紧接着
// 无条件打 Info「死人开关启动」,然后引擎照常报价 —— 那等于:凭据一错,交易所侧
// 倒计时从未 arm 过,进程被 SIGKILL 后挂单原样留在盘口,而日志上看不出任何异常。
// 现在成功与否落进 e.dm(上面那份凭证),报价那边拿不到凭证就不报价
// (engine.go deadmanGate)。
func (e *Engine) runDeadMansSwitch(ctx context.Context) {
	var gatePairs []string
	for _, p := range e.cfg.Pairs {
		if deadmanCovers(e.execs[p.Exec]) {
			gatePairs = append(gatePairs, p.ExecSymbol)
		}
	}
	if len(gatePairs) == 0 {
		return
	}
	timeoutSec := int(deadmanTimeout / time.Second)
	refresh := func() (armed int) {
		now := time.Now()
		for _, pair := range gatePairs {
			if err := gateCountdownCancel(timeoutSec, pair); err != nil {
				logger.Errorf("[mm] 死人开关 arm 失败 %s: %v —— 交易所侧倒计时没上;"+
					"在它 arm 成功之前这个 pair 不会报价", pair, err)
				continue
			}
			e.dm.mark(pair, now)
			armed++
		}
		return armed
	}
	if armed := refresh(); armed == len(gatePairs) {
		logger.Infof("[mm] 死人开关启动(gate countdown_cancel_all · %d 对 · %s 刷新/%s 超时)",
			armed, deadmanRefresh, deadmanTimeout)
	} else {
		// 【不许再无条件宣布"启动"】只 arm 上一部分和一个都没 arm 上,都是这一条。
		logger.Errorf("[mm] 死人开关【没有全部启动】:%d/%d 对 arm 成功 —— "+
			"没 arm 上的 pair 在证实之前不报价;它是唯一能扛进程崩溃的兜底", armed, len(gatePairs))
	}
	t := time.NewTicker(deadmanRefresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// 停止刷新 → gate 倒计时到点自动撤单(与 Stop 主动撤单构成双保险)。
			return
		case <-t.C:
			refresh()
		}
	}
}
