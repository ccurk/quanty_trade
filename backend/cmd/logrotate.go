package main

import (
	"io"

	"gopkg.in/natefinch/lumberjack.v2"
)

// 日志轮转上限。对齐 a6d84e6 给容器日志定的 docker
// --log-opt max-size=50m --log-opt max-file=3(3 个文件、合计 150MB):
// 这里同样是当前文件 50MB + 2 份历史备份 = 3 个文件。
//
// 背景(台账 #37):server.log 此前用 O_APPEND 打开后永不轮转,而 observe 每
// tick 写一行、795a348 又把该行加长约一倍。2026-09-08 实测服务器容器内
// /app/logs/server.log 已 195.4MiB、日增约 66MiB,并且它落在容器可写层里,
// a6d84e6 加的 docker log-opt 根本管不到它。2026-08-27 那次 DB 挂死事故的
// 根因正是 40G 磁盘被 256MB 级日志写满 → MySQL 写不进去。
const (
	logMaxSizeMB  = 50
	logMaxBackups = 2
)

// newRotatingWriter 返回按大小自动轮转的日志 writer。
//
// 备份做 gzip 压缩:这些是单行 JSON,压缩比很高,所以稳态磁盘占用远小于
// 150MB 上限。maxSizeMB 之所以是参数而不是直接读常量,是为了让测试能用很小
// 的阈值真实触发一次轮转(见 logrotate_test.go),生产调用一律传 logMaxSizeMB。
func newRotatingWriter(path string, maxSizeMB int) io.Writer {
	return &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxSizeMB,
		MaxBackups: logMaxBackups,
		Compress:   true,
	}
}
