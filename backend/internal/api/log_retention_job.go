package api

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/logger"
)

// 日志表保留清扫。
//
// strategy_logs 在 debug 档以 ~15 行/秒（≈130 万行/天）增长，api_logs 每请求一行，
// 两表都没有任何清理路径。表一大，GetStrategyLogs 的 ORDER BY created_at DESC
// 查询在缺复合索引时退化到几十秒（2026-08-02 实测 33s，前端/巡检以 25s 超时表现
// 为 HTTP 000），磁盘也被持续吃掉。这里每小时删一次超过保留期的旧行。
//
// 删除用 id IN (子查询 LIMIT) 的小批次，避免单条大 DELETE 的长事务与长锁；
// MySQL 不允许 DELETE 直接子查询同一张表，需要包一层派生表（tmp），该写法在
// SQLite 下同样合法。子查询的 WHERE created_at < ? 由 created_at 索引服务。

const (
	logRetentionDefaultDays = 7
	logRetentionBatchSize   = 20000
	// 单轮至多 logRetentionMaxBatches×logRetentionBatchSize=100 万行；
	// 首次部署的历史积压靠逐小时多轮慢慢清空，不在一轮里硬拉。
	logRetentionMaxBatches = 50
)

var logRetentionOnce sync.Once

// StartLogRetentionJob 启动日志保留清扫循环（进程内幂等）。
func StartLogRetentionJob(ctx context.Context) {
	logRetentionOnce.Do(func() {
		go runLogRetentionLoop(ctx)
	})
}

// 保留天数默认 7，可用环境变量 LOG_RETENTION_DAYS 覆盖（最小 1）。
func logRetentionDays() int {
	if v := os.Getenv("LOG_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	return logRetentionDefaultDays
}

func runLogRetentionLoop(ctx context.Context) {
	// 避开进程启动高峰，也确保 AutoMigrate 新建的索引已就位。
	if !sleepCtx(ctx, 2*time.Minute) {
		return
	}
	for {
		sweepExpiredLogs(ctx)
		if !sleepCtx(ctx, time.Hour) {
			return
		}
	}
}

func sweepExpiredLogs(ctx context.Context) {
	if database.DB == nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(logRetentionDays()) * 24 * time.Hour)
	for _, table := range []string{"strategy_logs", "api_logs"} {
		var total int64
		for batch := 0; batch < logRetentionMaxBatches; batch++ {
			if ctx.Err() != nil {
				return
			}
			res := database.DB.Exec(
				"DELETE FROM "+table+" WHERE id IN (SELECT id FROM (SELECT id FROM "+table+" WHERE created_at < ? ORDER BY id LIMIT ?) tmp)",
				cutoff, logRetentionBatchSize,
			)
			if res.Error != nil {
				logger.Errorf("log retention: %s delete failed err=%v", table, res.Error)
				break
			}
			total += res.RowsAffected
			if res.RowsAffected < int64(logRetentionBatchSize) {
				break
			}
			// 批间让路，避免持续占写锁/IO。
			if !sleepCtx(ctx, 200*time.Millisecond) {
				return
			}
		}
		if total > 0 {
			logger.Infof("log retention: %s pruned %d rows older than %s (retention %dd)",
				table, total, cutoff.UTC().Format(time.RFC3339), logRetentionDays())
		}
	}
}
