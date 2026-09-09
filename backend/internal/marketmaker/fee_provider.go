package marketmaker

import (
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

// fee_provider.go supplies the maker fee (bps) used to turn the observed GROSS edge
// into a NET edge. For gate it fetches the account's REAL per-pair maker fee live
// (GET /api/v4/spot/fee — uses the SPOT permission the key already has; /wallet/fee
// needs a separate wallet permission) so a zero-fee / promo pair shows up as 0
// automatically; results are cached for feeTTL. Exchanges we hold no key for fall
// back to a clearly labeled default (live=false) so the UI shows it's an assumption.

var feeHTTP = &http.Client{Timeout: 10 * time.Second}

// noteLiveFee 把【交易所返回的账户级 maker 费】打进日志,每个 (交易所, 费率) 只打一次。
//
// 为什么值得单独加这一句:台账 #15 的整条结论都卡在"我们账号真实的 maker 档位是多少"
// 上 —— gate-futures-mm-assessment-2026-09-08.md §1.4 当时写的补齐方式是"所有者去
// Profile > My Fees 看一眼"或"再写一个带凭据的调用"。两条都不必要:这个数**每轮
// universe 扫描都已经被真实拉回来了**(MakerFeeBps / PrefetchGateMakerFees 各一处
// 写缓存),只是从来没有落到任何进程外能看见的地方 —— 它只进了 feeCache,再经
// /stats/mm-observe 这个**需要鉴权**的接口出去。于是一个已经在手里的数,被当成了
// 需要人去查的未知数。
//
// 去重是必需的:universe 扫描每 10 秒一轮、每轮几千对,不去重就是刷屏。
// 不打 symbol 是有意的 —— 要回答的是"账户在哪一档",不是每个对各自多少;
// per-pair 的促销费率仪表盘那一行本来就有。
var loggedFee = map[string]bool{}

// 返回值 = 这次真的打了日志。生产代码不看它,存在的理由只有一个:
// 让"去重生效"这条断言直接测【日志有没有发出去】,而不是绕道测那张 map 的长度 ——
// 后者在把去重整条删掉之后仍然会通过,等于没测。
func noteLiveFee(exchange string, bps float64) bool {
	k := fmt.Sprintf("%s|%.4f", strings.ToLower(exchange), bps)
	feeMu.Lock()
	seen := loggedFee[k]
	loggedFee[k] = true
	feeMu.Unlock()
	if seen {
		return false
	}
	logger.Infof("[mm-fee] %s 账户级 maker 费(交易所实测返回)= %.4f bps", exchange, bps)
	return true
}

const feeTTL = 5 * time.Minute

// defaultMakerBps is the fallback maker fee (bps) when a live rate can't be fetched.
// These are conservative placeholders, NOT authoritative — shown as "assumed".
var defaultMakerBps = map[string]float64{"gate": 10, "kucoin": 10, "coinsph": 25}

func defaultFeeBps(ex string) float64 {
	if v, ok := defaultMakerBps[strings.ToLower(ex)]; ok {
		return v
	}
	return 20
}

type feeEntry struct {
	bps  float64
	live bool
	at   time.Time
}

var (
	feeMu    sync.RWMutex
	feeCache = map[string]feeEntry{}
)

// MakerFeeBps returns the maker fee in bps for exchange|symbol, plus whether it was
// live-fetched (true) or a default assumption (false). Cached for feeTTL.
func MakerFeeBps(exchange, symbol string) (float64, bool) {
	k := strings.ToLower(exchange) + "|" + symbol
	feeMu.RLock()
	e, ok := feeCache[k]
	feeMu.RUnlock()
	if ok && time.Since(e.at) < feeTTL {
		return e.bps, e.live
	}
	if strings.EqualFold(exchange, "gate") {
		if bps, err := fetchGateMakerBps(symbol); err == nil {
			feeMu.Lock()
			feeCache[k] = feeEntry{bps: bps, live: true, at: time.Now()}
			feeMu.Unlock()
			_ = noteLiveFee(exchange, bps)
			return bps, true
		}
	}
	d := defaultFeeBps(exchange)
	feeMu.Lock()
	feeCache[k] = feeEntry{bps: d, live: false, at: time.Now()} // 缓存默认值,避免拉取失败时反复打接口
	feeMu.Unlock()
	return d, false
}

// fetchGateMakerBps calls GET /api/v4/spot/fee?currency_pair=<sym> (HMAC-SHA512).
func fetchGateMakerBps(symbol string) (float64, error) {
	key, secret := os.Getenv("MM_GATE_API_KEY"), os.Getenv("MM_GATE_API_SECRET")
	if key == "" || secret == "" {
		return 0, fmt.Errorf("gate key 未配置")
	}
	const host = "https://api.gateio.ws"
	const path = "/api/v4/spot/fee"
	query := "currency_pair=" + symbol
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	bh := sha512.Sum512([]byte(""))
	payload := "GET\n" + path + "\n" + query + "\n" + hex.EncodeToString(bh[:]) + "\n" + ts
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte(payload))
	sign := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodGet, host+path+"?"+query, nil)
	req.Header.Set("KEY", key)
	req.Header.Set("Timestamp", ts)
	req.Header.Set("SIGN", sign)
	req.Header.Set("Accept", "application/json")

	resp, err := feeHTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("gate fee HTTP %d", resp.StatusCode)
	}
	// maker_fee is a decimal-ratio string, e.g. "0.002" = 0.2% = 20bps.
	var out struct {
		MakerFee string `json:"maker_fee"`
	}
	if err := json.Unmarshal(rb, &out); err != nil || out.MakerFee == "" {
		return 0, fmt.Errorf("gate fee 解析失败")
	}
	f, err := strconv.ParseFloat(out.MakerFee, 64)
	if err != nil {
		return 0, err
	}
	return f * 10000, nil
}

var gateFeePrefetchAt time.Time

// PrefetchGateMakerFees batch-loads gate's REAL per-pair maker fee into the cache
// via GET /api/v4/spot/batch_fee (≤50 pairs/call). This is what makes a zero-fee /
// promo pair surface its true 0 fee per pair (so its net edge = gross). Throttled to
// feeTTL so the whole-market scan doesn't refetch every cycle.
func PrefetchGateMakerFees(symbols []string) {
	feeMu.RLock()
	fresh := !gateFeePrefetchAt.IsZero() && time.Since(gateFeePrefetchAt) < feeTTL
	feeMu.RUnlock()
	if fresh || len(symbols) == 0 {
		return
	}
	key, secret := os.Getenv("MM_GATE_API_KEY"), os.Getenv("MM_GATE_API_SECRET")
	if key == "" || secret == "" {
		return
	}
	for i := 0; i < len(symbols); i += 50 {
		end := i + 50
		if end > len(symbols) {
			end = len(symbols)
		}
		fetchGateBatchFees(symbols[i:end], key, secret)
	}
	feeMu.Lock()
	gateFeePrefetchAt = time.Now()
	feeMu.Unlock()
}

func fetchGateBatchFees(pairs []string, key, secret string) {
	const host = "https://api.gateio.ws"
	const path = "/api/v4/spot/batch_fee"
	query := "currency_pairs=" + strings.Join(pairs, ",")
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	bh := sha512.Sum512([]byte(""))
	payload := "GET\n" + path + "\n" + query + "\n" + hex.EncodeToString(bh[:]) + "\n" + ts
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte(payload))
	sign := hex.EncodeToString(mac.Sum(nil))
	req, _ := http.NewRequest(http.MethodGet, host+path+"?"+query, nil)
	req.Header.Set("KEY", key)
	req.Header.Set("Timestamp", ts)
	req.Header.Set("SIGN", sign)
	req.Header.Set("Accept", "application/json")
	resp, err := feeHTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return
	}
	var m map[string]struct {
		MakerFee string `json:"maker_fee"`
	}
	if json.Unmarshal(rb, &m) != nil {
		return
	}
	now := time.Now()
	seen := map[float64]bool{}
	feeMu.Lock()
	for pair, v := range m {
		f, err := strconv.ParseFloat(v.MakerFee, 64)
		if err != nil {
			continue
		}
		feeCache["gate|"+pair] = feeEntry{bps: f * 10000, live: true, at: now}
		seen[f*10000] = true
	}
	feeMu.Unlock()
	for bps := range seen { // 批量路径同样要留痕,否则只有单对路径的 BTC_USDT 会被记下来
		_ = noteLiveFee("gate", bps)
	}
}

// CachedMakerFeeBps returns the cached maker fee (no HTTP). Fresh cache → (fee, live);
// otherwise the labeled default → (default, false). The universe scan uses this after
// PrefetchGateMakerFees has populated the cache.
func CachedMakerFeeBps(exchange, symbol string) (float64, bool) {
	k := strings.ToLower(exchange) + "|" + symbol
	feeMu.RLock()
	e, ok := feeCache[k]
	feeMu.RUnlock()
	if ok && time.Since(e.at) < feeTTL {
		return e.bps, e.live
	}
	return defaultFeeBps(exchange), false
}
