package bus

import (
	"context"
	"sync"
	"time"

	"quanty_trade/internal/conf"
)

// 这份进程级状态存在的唯一理由(台账 #115):Redis 连不上 / 口令不对的时候,
// 进程照样起、网页照样回 200,于是"总线是死的"这件事【在进程外面完全看不见】——
// 只能靠人去 grep 容器日志。把最后一次连接结果记下来,健康端点才有东西可查。
//
// 这里刻意【不】自己建连接:探活只用已经建好的那一条,或者交出最近一次失败原因。
// 健康检查自己去 dial 会掩盖真正的问题(它连得上不代表业务那条连得上)。
var (
	healthMu  sync.RWMutex
	healthBus *RedisBus // 最近一次成功建立的连接;nil = 从没连上过
	healthErr error     // 最近一次连接/探活的失败原因;nil = 最近一次是成功
	healthAt  time.Time
)

// recordBusResult 由 NewRedisBusFromConfig 在每次尝试后调用。
// 失败【不】清掉 healthBus:已经建好的连接要留着给后续探活用,
// 否则一次瞬时失败就让健康检查永远退化成"读缓存的旧错误"。
func recordBusResult(b *RedisBus, err error) {
	healthMu.Lock()
	if err == nil && b != nil {
		healthBus = b
	}
	healthErr = err
	healthAt = time.Now()
	healthMu.Unlock()
}

// Health 报告 Redis 总线当前是否可用,给免认证健康端点用。
//
// 返回 detail 只含 go-redis 的错误文本(NOAUTH / WRONGPASS / dial 失败等),
// 【不含口令】——口令从不出现在这些错误里,也不要在这里拼进去。
//
// 三种结果:
//   - Redis 在配置里就是关的      → (true, "disabled")  它没坏,是没开
//   - 从没连上过                  → (false, 最近一次失败原因)
//   - 连上过                      → 现场 ping 一次(2s 上限),用真实结果回答
func Health(ctx context.Context) (bool, string) {
	if !conf.C().Redis.Enabled {
		return true, "disabled"
	}
	healthMu.RLock()
	b, lastErr := healthBus, healthErr
	healthMu.RUnlock()

	if b == nil || b.client == nil {
		if lastErr != nil {
			return false, lastErr.Error()
		}
		return false, "redis bus not initialized"
	}

	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := b.client.Ping(pctx).Err(); err != nil {
		recordBusResult(nil, err)
		return false, err.Error()
	}
	recordBusResult(b, nil)
	return true, "ok"
}
