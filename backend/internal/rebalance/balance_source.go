package rebalance

import (
	"crypto/hmac"
	"crypto/sha256"
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
)

// balance_source.go fetches live SPOT balances via signed REST. For cross-exchange
// rebalancing (transfers take minutes–hours, cooldowns are in minutes) polling every
// ~15s is more than enough and far more robust than a private balance websocket —
// binance's listenKey balance stream is deprecated and gate's needs a maintained
// authed long connection. Keys are read from env: gate = MM_GATE_API_*, binance =
// BINANCE_API_*. Read-only calls; nothing here can move funds.

var balanceHTTP = &http.Client{Timeout: 10 * time.Second}

// FetchGateSpotBalances calls GET /api/v4/spot/accounts (HMAC-SHA512 signed).
func FetchGateSpotBalances() ([]Balance, error) {
	key, secret := os.Getenv("MM_GATE_API_KEY"), os.Getenv("MM_GATE_API_SECRET")
	if key == "" || secret == "" {
		return nil, fmt.Errorf("gate 密钥未配置(MM_GATE_API_KEY/SECRET)")
	}
	const host = "https://api.gateio.ws"
	const path = "/api/v4/spot/accounts"
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	// Gate v4 sign: HMAC-SHA512( METHOD\nPATH\nQUERY\nSHA512(body)\nTIMESTAMP )
	bodyHash := sha512.Sum512([]byte(""))
	payload := "GET\n" + path + "\n\n" + hex.EncodeToString(bodyHash[:]) + "\n" + ts
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte(payload))
	sign := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodGet, host+path, nil)
	req.Header.Set("KEY", key)
	req.Header.Set("Timestamp", ts)
	req.Header.Set("SIGN", sign)
	req.Header.Set("Accept", "application/json")

	resp, err := balanceHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gate accounts HTTP %d: %s", resp.StatusCode, truncErr(raw))
	}
	var rows []struct {
		Currency  string `json:"currency"`
		Available string `json:"available"`
		Locked    string `json:"locked"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("gate accounts 解析失败: %w", err)
	}
	out := make([]Balance, 0, len(rows))
	for _, r := range rows {
		free, locked := atof(r.Available), atof(r.Locked)
		if free == 0 && locked == 0 {
			continue
		}
		out = append(out, Balance{Exchange: "gate", Asset: strings.ToUpper(r.Currency), Free: free, Locked: locked})
	}
	return out, nil
}

// FetchBinanceSpotBalances calls GET /api/v3/account (HMAC-SHA256 signed).
func FetchBinanceSpotBalances() ([]Balance, error) {
	key, secret := os.Getenv("BINANCE_API_KEY"), os.Getenv("BINANCE_API_SECRET")
	if key == "" || secret == "" {
		return nil, fmt.Errorf("binance 密钥未配置(BINANCE_API_KEY/SECRET)")
	}
	const host = "https://api.binance.com"
	const path = "/api/v3/account"
	query := "timestamp=" + strconv.FormatInt(time.Now().UnixMilli(), 10) + "&recvWindow=5000"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(query))
	sig := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodGet, host+path+"?"+query+"&signature="+sig, nil)
	req.Header.Set("X-MBX-APIKEY", key)

	resp, err := balanceHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance account HTTP %d: %s", resp.StatusCode, truncErr(raw))
	}
	var acct struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(raw, &acct); err != nil {
		return nil, fmt.Errorf("binance account 解析失败: %w", err)
	}
	out := make([]Balance, 0, len(acct.Balances))
	for _, b := range acct.Balances {
		free, locked := atof(b.Free), atof(b.Locked)
		if free == 0 && locked == 0 {
			continue
		}
		out = append(out, Balance{Exchange: "binance", Asset: strings.ToUpper(b.Asset), Free: free, Locked: locked})
	}
	return out, nil
}

func atof(s string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return v }

func truncErr(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
