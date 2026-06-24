package logger

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// ErrorSink 在每条 ERROR 级日志触发时被调用，参数是格式化后的消息（不含 "[ERROR]" 前缀）。
// 约束：sink 必须极快且非阻塞——真正的工作（网络等）放到自己的 goroutine。
// sink 内【绝不能】再调用 logger.Error*，否则无限循环。sink 的 panic 会被 recover。
type ErrorSink func(msg string)

var (
	sinkMu     sync.RWMutex
	errorSinks []ErrorSink
)

// AddErrorSink 注册一个 ERROR 钩子（如 Lark 告警）。可多次调用注册多个。
func AddErrorSink(s ErrorSink) {
	if s == nil {
		return
	}
	sinkMu.Lock()
	errorSinks = append(errorSinks, s)
	sinkMu.Unlock()
}

func dispatchError(msg string) {
	sinkMu.RLock()
	sinks := errorSinks
	sinkMu.RUnlock()
	for _, s := range sinks {
		func(fn ErrorSink) {
			defer func() { _ = recover() }()
			fn(msg)
		}(s)
	}
}

type Level int32

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var currentLevel atomic.Int32

func init() {
	currentLevel.Store(int32(LevelInfo))
}

func SetLevel(level string) {
	l := strings.ToLower(strings.TrimSpace(level))
	switch l {
	case "debug":
		currentLevel.Store(int32(LevelDebug))
	case "info":
		currentLevel.Store(int32(LevelInfo))
	case "warn", "warning":
		currentLevel.Store(int32(LevelWarn))
	case "error":
		currentLevel.Store(int32(LevelError))
	}
}

func SetLevelFromEnv() {
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		SetLevel(v)
	}
}

func Debugf(format string, args ...interface{}) {
	if Level(currentLevel.Load()) > LevelDebug {
		return
	}
	log.Printf("[DEBUG] "+format, args...)
}

func Infof(format string, args ...interface{}) {
	if Level(currentLevel.Load()) > LevelInfo {
		return
	}
	log.Printf("[INFO] "+format, args...)
}

func Warnf(format string, args ...interface{}) {
	if Level(currentLevel.Load()) > LevelWarn {
		return
	}
	log.Printf("[WARN] "+format, args...)
}

func Errorf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Print("[ERROR] " + msg)
	dispatchError(msg)
}

func Debug(args ...interface{}) {
	if Level(currentLevel.Load()) > LevelDebug {
		return
	}
	log.Print(append([]interface{}{"[DEBUG] "}, args...)...)
}

func Info(args ...interface{}) {
	if Level(currentLevel.Load()) > LevelInfo {
		return
	}
	log.Print(append([]interface{}{"[INFO] "}, args...)...)
}

func Warn(args ...interface{}) {
	if Level(currentLevel.Load()) > LevelWarn {
		return
	}
	log.Print(append([]interface{}{"[WARN] "}, args...)...)
}

func Error(args ...interface{}) {
	msg := fmt.Sprint(args...)
	log.Print("[ERROR] " + msg)
	dispatchError(msg)
}

type TraceLogger struct {
	traceID string
}

func WithTrace(traceID string) TraceLogger {
	return TraceLogger{traceID: strings.TrimSpace(traceID)}
}

func (t TraceLogger) Debugf(format string, args ...interface{}) {
	if Level(currentLevel.Load()) > LevelDebug {
		return
	}
	if t.traceID == "" {
		log.Printf("[DEBUG] "+format, args...)
		return
	}
	log.Printf("[DEBUG] [trace=%s] "+format, append([]interface{}{t.traceID}, args...)...)
}

func (t TraceLogger) Infof(format string, args ...interface{}) {
	if Level(currentLevel.Load()) > LevelInfo {
		return
	}
	if t.traceID == "" {
		log.Printf("[INFO] "+format, args...)
		return
	}
	log.Printf("[INFO] [trace=%s] "+format, append([]interface{}{t.traceID}, args...)...)
}

func (t TraceLogger) Warnf(format string, args ...interface{}) {
	if Level(currentLevel.Load()) > LevelWarn {
		return
	}
	if t.traceID == "" {
		log.Printf("[WARN] "+format, args...)
		return
	}
	log.Printf("[WARN] [trace=%s] "+format, append([]interface{}{t.traceID}, args...)...)
}

func (t TraceLogger) Errorf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if t.traceID != "" {
		msg = "[trace=" + t.traceID + "] " + msg
	}
	log.Print("[ERROR] " + msg)
	dispatchError(msg)
}
