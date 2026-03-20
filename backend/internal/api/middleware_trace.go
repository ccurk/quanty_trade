package api

import (
	"strings"
	"time"

	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"

	"github.com/gin-gonic/gin"
)

const traceIDKey = "trace_id"

func TraceID(c *gin.Context) string {
	v, ok := c.Get(traceIDKey)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := strings.TrimSpace(c.GetHeader("X-Trace-Id"))
		if traceID == "" {
			traceID = strings.TrimSpace(c.GetHeader("X-Request-Id"))
		}
		if traceID == "" {
			traceID = models.GenerateUUID()
		}
		c.Set(traceIDKey, traceID)
		c.Writer.Header().Set("X-Trace-Id", traceID)

		start := time.Now()
		logger.WithTrace(traceID).Infof("HTTP start method=%s path=%s ip=%s", c.Request.Method, c.FullPath(), c.ClientIP())
		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		if status >= 500 {
			logger.WithTrace(traceID).Errorf("HTTP end status=%d ms=%d", status, latency.Milliseconds())
		} else {
			logger.WithTrace(traceID).Infof("HTTP end status=%d ms=%d", status, latency.Milliseconds())
		}
	}
}
