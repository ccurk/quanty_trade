package rebalance

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// withdraw.go is the ONLY code that moves funds off an exchange. It is invoked
// solely by the Executor, solely for whitelisted destinations, solely when not
// dry-run. Keys, permissions, IP-binding and the exchange-side address whitelist
// are the operator's responsibility — this just submits the (already-guarded) call.
//
// Signing mirrors balance_source.go exactly: gate HMAC-SHA512, binance HMAC-SHA256.

var withdrawHTTP = &http.Client{Timeout: 20 * time.Second}

// Withdraw dispatches to the right exchange. fromExchange is the exchange we pull
// funds OUT of (the source of the withdrawal).
func Withdraw(fromExchange, asset, network, address, memo string, amount float64) (string, error) {
	switch {
	case eqFold(fromExchange, "gate"):
		return WithdrawGate(asset, network, address, memo, amount)
	case eqFold(fromExchange, "binance"):
		return WithdrawBinance(asset, network, address, memo, amount)
	default:
		return "", fmt.Errorf("不支持从 %s 提现(仅 gate/binance)", fromExchange)
	}
}

func amtStr(a float64) string { return strconv.FormatFloat(a, 'f', -1, 64) }

// WithdrawGate: POST /api/v4/withdrawals (HMAC-SHA512 signed JSON body).
func WithdrawGate(asset, network, address, memo string, amount float64) (string, error) {
	key, secret := os.Getenv("MM_GATE_API_KEY"), os.Getenv("MM_GATE_API_SECRET")
	if key == "" || secret == "" {
		return "", fmt.Errorf("gate 密钥未配置")
	}
	const host = "https://api.gateio.ws"
	const path = "/api/v4/withdrawals"
	body := map[string]string{"currency": asset, "amount": amtStr(amount), "address": address, "chain": network}
	if memo != "" {
		body["memo"] = memo
	}
	raw, _ := json.Marshal(body)
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	bodyHash := sha512.Sum512(raw)
	payload := "POST\n" + path + "\n\n" + hex.EncodeToString(bodyHash[:]) + "\n" + ts
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte(payload))
	sign := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, host+path, bytes.NewReader(raw))
	req.Header.Set("KEY", key)
	req.Header.Set("Timestamp", ts)
	req.Header.Set("SIGN", sign)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := withdrawHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gate withdraw HTTP %d: %s", resp.StatusCode, truncErr(rb))
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rb, &out)
	if out.ID == "" {
		return string(rb), nil // 没有标准 id 字段就回原始响应,便于人工核对
	}
	return out.ID, nil
}

// WithdrawBinance: POST /sapi/v1/capital/withdraw/apply (HMAC-SHA256 signed query).
func WithdrawBinance(asset, network, address, memo string, amount float64) (string, error) {
	key, secret := os.Getenv("BINANCE_API_KEY"), os.Getenv("BINANCE_API_SECRET")
	if key == "" || secret == "" {
		return "", fmt.Errorf("binance 密钥未配置")
	}
	const host = "https://api.binance.com"
	const path = "/sapi/v1/capital/withdraw/apply"
	q := url.Values{}
	q.Set("coin", asset)
	q.Set("network", network)
	q.Set("address", address)
	q.Set("amount", amtStr(amount))
	if memo != "" {
		q.Set("addressTag", memo)
	}
	q.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	q.Set("recvWindow", "5000")
	query := q.Encode()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(query))
	sig := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, host+path+"?"+query+"&signature="+sig, nil)
	req.Header.Set("X-MBX-APIKEY", key)

	resp, err := withdrawHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("binance withdraw HTTP %d: %s", resp.StatusCode, truncErr(rb))
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rb, &out)
	if out.ID == "" {
		return string(rb), nil
	}
	return out.ID, nil
}
