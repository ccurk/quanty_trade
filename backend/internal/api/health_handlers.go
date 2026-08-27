package api

import (
	"context"
	"net/http"
	"time"

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
