package exchange

// #89 DNS 韧性 (2026-09-12): 宿主 resolver(Tailscale MagicDNS)已两次 SERVFAIL
// (08-11 停机、09-09 起 ≥74h)令 REST/WS 对 binance 全灭。系统解析失败时降级到
// 公共 DNS 直连解析,拨号连 IP 但 TLS 仍用原主机名(SNI/证书校验不变)。
// 系统解析成功路径零改动;fallback 服务器可用 QT_FALLBACK_DNS 覆盖(逗号分隔 host:port)。

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var baseDialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
}

func fallbackDNSServers() []string {
	if v := strings.TrimSpace(os.Getenv("QT_FALLBACK_DNS")); v != "" {
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"1.1.1.1:53", "8.8.8.8:53"}
}

type fbCacheEntry struct {
	ips []net.IP
	exp time.Time
}

var (
	fbCacheMu sync.Mutex
	fbCache   = map[string]fbCacheEntry{}
)

// resolveFallback 绕过系统 resolver,经公共 DNS 解析 host;成功结果缓存 60s。
func resolveFallback(ctx context.Context, host string) ([]net.IP, error) {
	fbCacheMu.Lock()
	if e, ok := fbCache[host]; ok && time.Now().Before(e.exp) {
		ips := e.ips
		fbCacheMu.Unlock()
		return ips, nil
	}
	fbCacheMu.Unlock()

	var lastErr error
	for _, server := range fallbackDNSServers() {
		srv := server
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return baseDialer.DialContext(ctx, network, srv)
			},
		}
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		addrs, err := r.LookupIPAddr(rctx, host)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		ips := make([]net.IP, 0, len(addrs))
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
		if len(ips) > 0 {
			fbCacheMu.Lock()
			fbCache[host] = fbCacheEntry{ips: ips, exp: time.Now().Add(60 * time.Second)}
			fbCacheMu.Unlock()
			return ips, nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("fallback DNS: no address")
	}
	return nil, lastErr
}

func isDNSFailure(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

// resilientDialContext 先走系统解析正常拨号;仅当错误链含 DNS 解析失败时,
// 用 fallback 解析出的 IP 重拨(端口不变)。上层 TLS/SNI 用原主机名,不受影响。
func resilientDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := baseDialer.DialContext(ctx, network, addr)
	if err == nil || !isDNSFailure(err) {
		return conn, err
	}
	host, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil || net.ParseIP(host) != nil {
		return nil, err
	}
	ips, fbErr := resolveFallback(ctx, host)
	if fbErr != nil {
		return nil, err
	}
	lastErr := err
	for _, ip := range ips {
		c, dErr := baseDialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dErr == nil {
			return c, nil
		}
		lastErr = dErr
	}
	return nil, lastErr
}

// newResilientTransport 克隆默认 Transport(保留 proxy/TLS/连接池默认),仅换 DialContext。
func newResilientTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = resilientDialContext
	return t
}

// wsDialer 等价 websocket.DefaultDialer,仅名字解析走 resilientDialContext。
var wsDialer = &websocket.Dialer{
	Proxy:            http.ProxyFromEnvironment,
	HandshakeTimeout: 45 * time.Second,
	NetDialContext:   resilientDialContext,
}
