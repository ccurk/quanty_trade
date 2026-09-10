package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quanty_trade/internal/bus"
	"quanty_trade/internal/conf"

	"github.com/gin-gonic/gin"
)

// startWrongPassRedis 造出台账 #115 的现场:端口通、TCP 握手成功,唯独认证过不去。
// 全程在本机假服务器上,不碰任何真实 Redis。
func startWrongPassRedis(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 512)
				for {
					_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
					if _, err := c.Read(buf); err != nil {
						return
					}
					if _, err := c.Write([]byte("-WRONGPASS invalid username-password pair or user is disabled.\r\n")); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// TestRedisHealthReports503OnAuthFailure 是台账 #115 的端到端回归:
// Redis 认证失败之后,免认证健康端点必须【回 503】。
//
// 改动之前根本没有这个端点,后端唯一的探针是 /api/health/db —— 它只探 DB,
// Redis 死透了照样 200。于是"容器 Up、网页 200、总线是死的"能瞒过所有外部检查。
func TestRedisHealthReports503OnAuthFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	addr := startWrongPassRedis(t)
	t.Setenv("REDIS_ENABLED", "true")
	t.Setenv("REDIS_ADDR", addr)
	t.Setenv("REDIS_PASSWORD", "definitely-not-the-real-password")
	conf.MustLoad()
	if conf.C().Redis.Addr != addr {
		t.Skipf("conf 已被同包其它用例先加载(addr=%s),跳过", conf.C().Redis.Addr)
	}

	// 这正是 app.BuildStrategyManager 在启动期走的那一步。它返回 error,
	// 而改动之前 runtime.go 把这个 error 吞成一行日志就继续跑了。
	if _, err := bus.NewRedisBusFromConfig(); err == nil {
		t.Fatal("fake redis accepted the connection; scene not reproduced")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/health/redis", nil)
	RedisHealth(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while redis auth is failing, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if body["redis"] != "unavailable" {
		t.Fatalf("expected redis=unavailable, got %v", body["redis"])
	}
	detail, _ := body["detail"].(string)
	if !strings.Contains(detail, "WRONGPASS") {
		t.Fatalf("detail should name the reason, got %q", detail)
	}
	if strings.Contains(w.Body.String(), "definitely-not-the-real-password") {
		t.Fatal("health response leaked the redis password")
	}

	// 聚合端点同样必须 503 —— 容器 HEALTHCHECK 问的是这一个。
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	Health(c2)
	if w2.Code != http.StatusServiceUnavailable {
		t.Fatalf("aggregate /api/health should be 503, got %d body=%s", w2.Code, w2.Body.String())
	}
}
