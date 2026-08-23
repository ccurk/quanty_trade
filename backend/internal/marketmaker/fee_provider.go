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
)

// fee_provider.go supplies the maker fee (bps) used to turn the observed GROSS edge
// into a NET edge. For gate it fetches the account's REAL per-pair maker fee live
// (GET /api/v4/spot/fee — uses the SPOT permission the key already has; /wallet/fee
// needs a separate wallet permission) so a zero-fee / promo pair shows up as 0
// automatically; results are cached for feeTTL. Exchanges we hold no key for fall
// back to a clearly labeled default (live=false) so the UI shows it's an assumption.

var feeHTTP = &http.Client{Timeout: 10 * time.Second}

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
