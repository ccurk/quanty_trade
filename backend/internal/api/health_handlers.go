package api

import (
	"context"
	"net/http"
	"time"

	"quanty_trade/internal/bus"
	"quanty_trade/internal/database"

	"github.com/gin-gonic/gin"
)

// DBHealth 是免认证的 DB 层活性探针（2026-08-27 全 DB 端点永挂事故的产物）。
// 事故形态是 DB 请求无限挂起：进程活着、静态路由和鉴权中间件正常，唯独触 DB 的
// 请求全部卡死——外部只能靠"超时不返回"间接推断。本端点用 2s context 把探测
// 变成确定信号：ctx 同时约束连接池等待与查询本身（database/sql 语义），因此
// 池被耗尽时它同样能在 2s 内返回 503，而不是加入排队。
// 200 {"db":"ok"} = DB 可用；503 {"db":"unavailable"} = DB 层挂起/不可达。
func DBHealth(c *gin.Context) {
	if database.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"db": "uninitialized"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	var one int
	if err := database.DB.WithContext(ctx).Raw("SELECT 1").Scan(&one).Error; err != nil || one != 1 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"db": "unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"db": "ok"})
}

// RedisHealth 是免认证的 Redis 总线活性探针（台账 #115）。
//
// 为什么需要它：Redis 是 Go 与 Python 策略之间的唯一总线，也是并发仓位锁的所在。
// 它认证失败/断开时，进程照样起、HTTP 照样 200、/api/health/db 也照样 200——
// 于是【从外面看一切健康，实际总线是死的、策略一个都起不来】。没有这个端点，
// 这件事只能靠人去 grep 容器日志才能发现。
//
// 200 {"redis":"ok"} = 总线可用；{"redis":"disabled"} = 配置里就没开（不是故障）；
// 503 {"redis":"unavailable","detail":...} = 连不上或认证失败。detail 是 go-redis
// 的原始错误（NOAUTH / WRONGPASS / dial 失败），不含口令。
func RedisHealth(c *gin.Context) {
	ok, detail := bus.Health(c.Request.Context())
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"redis": "unavailable", "detail": detail})
		return
	}
	c.JSON(http.StatusOK, gin.H{"redis": detail})
}

// Health 是聚合探针：DB 与 Redis 【全绿才 200】，任一不可用即 503。
//
// 这一条是给容器 HEALTHCHECK / 外部监控用的单一入口。分端点仍然保留，
// 因为出事时要一眼看出是哪一层塌了。
func Health(c *gin.Context) {
	body := gin.H{}
	healthy := true

	if database.DB == nil {
		body["db"] = "uninitialized"
		healthy = false
	} else {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		var one int
		err := database.DB.WithContext(ctx).Raw("SELECT 1").Scan(&one).Error
		cancel()
		if err != nil || one != 1 {
			body["db"] = "unavailable"
			healthy = false
		} else {
			body["db"] = "ok"
		}
	}

	redisOK, redisDetail := bus.Health(c.Request.Context())
	body["redis"] = redisDetail
	if !redisOK {
		healthy = false
	}

	if !healthy {
		body["status"] = "unhealthy"
		c.JSON(http.StatusServiceUnavailable, body)
		return
	}
	body["status"] = "ok"
	c.JSON(http.StatusOK, body)
}
