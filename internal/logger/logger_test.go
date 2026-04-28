package logger

import (
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Level != "info" {
		t.Errorf("expected level info, got %s", cfg.Level)
	}
	if cfg.Format != "text" {
		t.Errorf("expected format text, got %s", cfg.Format)
	}
	if cfg.Output != "stderr" {
		t.Errorf("expected output stderr, got %s", cfg.Output)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}
	for _, tt := range tests {
		got := parseLevel(tt.input)
		if got != tt.expected {
			t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestInitLoggerText(t *testing.T) {
	cfg := Config{
		Level:  "debug",
		Format: "text",
		Output: "stderr",
	}
	InitLogger(cfg)
	logger := GetLogger()
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestInitLoggerJSON(t *testing.T) {
	cfg := Config{
		Level:  "info",
		Format: "json",
		Output: "stderr",
	}
	InitLogger(cfg)
	logger := GetLogger()
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestInitLoggerFile(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "logger_test_*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	cfg := Config{
		Level:  "info",
		Format: "json",
		Output: tmpFile.Name(),
	}
	InitLogger(cfg)

	Info("test_log_message", "key", "value")

	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test_log_message") {
		t.Errorf("log file should contain test_log_message, got: %s", string(data))
	}
}

func TestWith(t *testing.T) {
	InitLogger(DefaultConfig())
	logger := With("component", "test")
	if logger == nil {
		t.Fatal("expected non-nil logger from With")
	}
}

func TestWithGroup(t *testing.T) {
	InitLogger(DefaultConfig())
	logger := WithGroup("server")
	if logger == nil {
		t.Fatal("expected non-nil logger from WithGroup")
	}
}

func TestConvenienceMethods(t *testing.T) {
	InitLogger(Config{
		Level:  "debug",
		Format: "text",
		Output: "stderr",
	})
	// 这些不应 panic
	Debug("debug message", "key", "value")
	Info("info message", "key", "value")
	Warn("warn message", "key", "value")
	Error("error message", "key", "value")
}

func TestInitLoggerInvalidFile(t *testing.T) {
	// 无效路径应回退到 stderr 而非崩溃
	cfg := Config{
		Level:  "info",
		Format: "text",
		Output: "/nonexistent/path/to/log.log",
	}
	InitLogger(cfg)
	// 不应 panic
	Info("fallback to stderr")
}

func TestInitLoggerStdout(t *testing.T) {
	cfg := Config{
		Level:  "info",
		Format: "json",
		Output: "stdout",
	}
	InitLogger(cfg)
	logger := GetLogger()
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}
