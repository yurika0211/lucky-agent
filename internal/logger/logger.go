package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"
)

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
	global = slog.New(newTextHandler(os.Stderr, slog.LevelInfo))
}

type Config struct {
	Level  string `yaml:"level,omitempty"`
	Format string `yaml:"format,omitempty"`
	Output string `yaml:"output,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Level:  "info",
		Format: "text",
		Output: "stderr",
	}
}

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
		f, err := os.OpenFile(cfg.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
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
		handler = newTextHandler(writer, level)
	}

	global = slog.New(handler)
	slog.SetDefault(global)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func GetLogger() *slog.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

func With(args ...any) *slog.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global.With(args...)
}

func WithGroup(name string) *slog.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global.WithGroup(name)
}

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

func newTextHandler(w io.Writer, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level <= slog.LevelDebug,
	}
	if usePrettyText(w) {
		return &prettyTextHandler{
			writer: w,
			opts:   opts,
		}
	}
	return slog.NewTextHandler(w, opts)
}

type prettyTextHandler struct {
	writer io.Writer
	opts   *slog.HandlerOptions

	mu     sync.Mutex
	attrs  []slog.Attr
	groups []string
}

func (h *prettyTextHandler) Enabled(_ context.Context, level slog.Level) bool {
	if h.opts == nil || h.opts.Level.Level() == 0 {
		return true
	}
	return level >= h.opts.Level.Level()
}

func (h *prettyTextHandler) Handle(_ context.Context, record slog.Record) error {
	var b strings.Builder

	icon, color := levelDecoration(record.Level)
	if color != "" {
		b.WriteString(color)
	}
	b.WriteString(icon)
	b.WriteString(" ")
	b.WriteString(strings.ToUpper(record.Level.String()))
	if color != "" {
		b.WriteString(resetColor)
	}
	b.WriteString(" ")
	b.WriteString(record.Time.Format("15:04:05"))
	b.WriteString(" ")
	b.WriteString(record.Message)

	fields := make([]string, 0, len(h.attrs)+record.NumAttrs())
	for _, attr := range h.attrs {
		fields = append(fields, formatAttr(h.groups, attr)...)
	}
	record.Attrs(func(attr slog.Attr) bool {
		fields = append(fields, formatAttr(h.groups, attr)...)
		return true
	})
	if len(fields) > 0 {
		b.WriteString(" · ")
		b.WriteString(strings.Join(fields, " "))
	}
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.writer, b.String())
	return err
}

func (h *prettyTextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	cp.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &cp
}

func (h *prettyTextHandler) WithGroup(name string) slog.Handler {
	cp := *h
	if name != "" {
		cp.groups = append(append([]string(nil), h.groups...), name)
	}
	return &cp
}

func formatAttr(groups []string, attr slog.Attr) []string {
	attr.Value = attr.Value.Resolve()

	if attr.Value.Kind() == slog.KindGroup {
		out := make([]string, 0, len(attr.Value.Group()))
		for _, child := range attr.Value.Group() {
			out = append(out, formatAttr(append(groups, attr.Key), child)...)
		}
		return out
	}

	key := attr.Key
	if len(groups) > 0 {
		prefix := strings.Join(groups, ".")
		if key != "" {
			key = prefix + "." + key
		} else {
			key = prefix
		}
	}
	if key == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s=%s", key, attrValueString(attr.Value))}
}

func attrValueString(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return strconv.Quote(v.String())
	case slog.KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case slog.KindInt64:
		return fmt.Sprintf("%d", v.Int64())
	case slog.KindUint64:
		return fmt.Sprintf("%d", v.Uint64())
	case slog.KindFloat64:
		return fmt.Sprintf("%g", v.Float64())
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format("2006-01-02 15:04:05")
	case slog.KindAny:
		return fmt.Sprint(v.Any())
	default:
		return v.String()
	}
}

func levelDecoration(level slog.Level) (string, string) {
	switch {
	case level >= slog.LevelError:
		return "✖", redColor
	case level >= slog.LevelWarn:
		return "⚠", yellowColor
	case level >= slog.LevelInfo:
		return "●", greenColor
	default:
		return "◌", cyanColor
	}
}

func usePrettyText(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

const (
	resetColor  = "\x1b[0m"
	redColor    = "\x1b[31m"
	yellowColor = "\x1b[33m"
	greenColor  = "\x1b[32m"
	cyanColor   = "\x1b[36m"
)
