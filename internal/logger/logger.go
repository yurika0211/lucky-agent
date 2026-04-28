package logger

import (
	"io"
	"log/slog"
	"os"
	"sync"
)

// Level 日志级别常量
type Level = slog.Level

const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

var (
	globalMu sync.RWMutex
	global   *slog.Logger
)

func init() {
	global = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// Config 日志配置
type Config struct {
	Level  string `yaml:"level,omitempty"`  // debug, info, warn, error
	Format string `yaml:"format,omitempty"` // json, text
	Output string `yaml:"output,omitempty"` // stdout, stderr, 或文件路径
}

// DefaultConfig 返回默认日志配置
func DefaultConfig() Config {
	return Config{
		Level:  "info",
		Format: "text",
		Output: "stderr",
	}
}

// InitLogger 根据配置初始化全局日志
func InitLogger(cfg Config) {
	globalMu.Lock()
	defer globalMu.Unlock()

	level := parseLevel(cfg.Level)
	handlerOpts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level <= slog.LevelDebug,
	}

	var writer io.Writer
	switch cfg.Output {
	case "stdout":
		writer = os.Stdout
	case "stderr", "":
		writer = os.Stderr
	default:
		// 文件路径
		f, err := os.OpenFile(cfg.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			slog.Warn("failed to open log file, falling back to stderr", "path", cfg.Output, "error", err)
			writer = os.Stderr
		} else {
			writer = f
		}
	}

	var handler slog.Handler
	switch cfg.Format {
	case "json":
		handler = slog.NewJSONHandler(writer, handlerOpts)
	default:
		handler = slog.NewTextHandler(writer, handlerOpts)
	}
	global = slog.New(handler)
	slog.SetDefault(global)
}

// parseLevel 解析日志级别字符串
func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// GetLogger 获取全局 logger
func GetLogger() *slog.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// With 返回带预置字段的 logger
func With(args ...any) *slog.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global.With(args...)
}

// WithGroup 返回带分组的 logger
func WithGroup(name string) *slog.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global.WithGroup(name)
}

// 便捷方法

func Debug(msg string, args ...any) {
	globalMu.RLock()
	defer globalMu.RUnlock()
	global.Debug(msg, args...)
}

func Info(msg string, args ...any) {
	globalMu.RLock()
	defer globalMu.RUnlock()
	global.Info(msg, args...)
}

func Warn(msg string, args ...any) {
	globalMu.RLock()
	defer globalMu.RUnlock()
	global.Warn(msg, args...)
}

func Error(msg string, args ...any) {
	globalMu.RLock()
	defer globalMu.RUnlock()
	global.Error(msg, args...)
}
