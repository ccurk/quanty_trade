package bus

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"quanty_trade/internal/conf"

	"github.com/redis/go-redis/v9"
)

// 注意:conf.Load() 是【进程内一次性】的(conf.go:256 `if loaded { return nil }`),
// 所以这些用例只设 REDIS_ENABLED,不碰 REDIS_ADDR —— 地址走各自 new 出来的 client,
// 免得污染同包其它用例。

// startFakeRedis 起一个假 Redis:对任何命令都回 reply。
// 台账 #115 的现场是"端口通、TCP 握手成功,唯独认证过不去",用假服务器精确复现,
// 全程不碰任何真实 Redis 实例。
func startFakeRedis(t *testing.T, reply string) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
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
					if _, err := c.Write([]byte(reply)); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// deadAddr 返回一个确定没人监听的地址。
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// loadConfWithRedisEnabled 保证 conf 里 redis.enabled=true。
// 走 env 而不是靠 conf_dev.yaml:测试的工作目录是包目录,repoRootFromWD() 在这里
// 找不到 conf/,Load() 会静默回落到 defaultConfig()(redis.enabled=false)。
// 同包的 redis_bus_test.go 也是同样设法,两者设的值一致,不会互相打架。
func loadConfWithRedisEnabled(t *testing.T) {
	t.Helper()
	t.Setenv("REDIS_ENABLED", "true")
	conf.MustLoad()
	if !conf.C().Redis.Enabled {
		t.Fatal("conf.Redis.Enabled 仍是 false —— conf 已被同包其它用例先加载成禁用态")
	}
}

func resetHealth(t *testing.T) {
	t.Helper()
	healthMu.Lock()
	healthBus, healthErr, healthAt = nil, nil, time.Time{}
	healthMu.Unlock()
}

// TestHealthReportsAuthFailure 是台账 #115 的回归用例。
//
// 改动之前:app/runtime.go 把 Redis 认证失败的 error 吞成一行日志之后,进程里
// 【没有任何地方还记得它发生过】,也没有任何端点能问出来 —— 容器 Up、网页 200、
// /api/health/db 200,而总线是死的。本用例断言的正是那条当时不存在的通路。
func TestHealthReportsAuthFailure(t *testing.T) {
	loadConfWithRedisEnabled(t)
	resetHealth(t)

	const wrongPass = "definitely-not-the-real-password"
	addr := startFakeRedis(t, "-WRONGPASS invalid username-password pair or user is disabled.\r\n")
	client := redis.NewClient(&redis.Options{Addr: addr, Password: wrongPass})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := client.Ping(ctx).Err()
	if err == nil {
		t.Fatal("fake redis accepted the ping; scene not reproduced")
	}
	if !strings.Contains(err.Error(), "WRONGPASS") {
		t.Fatalf("expected WRONGPASS, got: %v", err)
	}

	// 这一步就是 NewRedisBusFromConfig 在失败路径上做的事。
	recordBusResult(nil, err)

	ok, detail := Health(context.Background())
	if ok {
		t.Fatal("Health() reported healthy after an authentication failure")
	}
	if !strings.Contains(detail, "WRONGPASS") {
		t.Fatalf("Health() lost the reason, got: %q", detail)
	}
	// 口令绝不能跟着错误信息漏出去(台账 #107 的形状)。
	if strings.Contains(detail, wrongPass) {
		t.Fatal("Health() detail leaked the password")
	}
}

// TestHealthDoesNotTrustCachedSuccess 覆盖另一半:启动时连上了,之后 Redis 掉了。
//
// 这是健康检查最容易变成"帮凶"的地方 —— 缓存里写着"上次成功",于是它继续回 200。
// 用例刻意把状态设成"最近一次是成功"(healthErr=nil),但连接指向一个死端口:
// 只有【现场 ping】才能戳穿,读缓存的实现会在这里失败。
func TestHealthDoesNotTrustCachedSuccess(t *testing.T) {
	loadConfWithRedisEnabled(t)
	resetHealth(t)

	client := redis.NewClient(&redis.Options{Addr: deadAddr(t)})
	defer client.Close()
	recordBusResult(&RedisBus{client: client, prefix: "qt_test"}, nil)

	healthMu.RLock()
	cachedErr := healthErr
	healthMu.RUnlock()
	if cachedErr != nil {
		t.Fatalf("scene not set up: cached state should say 'last attempt succeeded', got %v", cachedErr)
	}

	ok, detail := Health(context.Background())
	if ok {
		t.Fatalf("Health() trusted the cached success and kept reporting healthy (detail=%q)", detail)
	}
}

// TestHealthNeverConnected:从没连上过,且没记过任何错误 → 必须是 false,
// 不能因为"没有坏消息"就当成健康。
func TestHealthNeverConnected(t *testing.T) {
	loadConfWithRedisEnabled(t)
	resetHealth(t)
	ok, detail := Health(context.Background())
	if ok {
		t.Fatal("Health() reported healthy with no connection ever established")
	}
	if detail != "redis bus not initialized" {
		t.Fatalf("unexpected detail: %q", detail)
	}
}
