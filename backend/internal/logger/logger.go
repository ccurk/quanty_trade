package logger

import (
	"log"
	"os"
	"strings"
	"sync/atomic"
)

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
	log.Printf("[ERROR] "+format, args...)
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
	log.Print(append([]interface{}{"[ERROR] "}, args...)...)
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
	if t.traceID == "" {
		log.Printf("[ERROR] "+format, args...)
		return
	}
	log.Printf("[ERROR] [trace=%s] "+format, append([]interface{}{t.traceID}, args...)...)
}
