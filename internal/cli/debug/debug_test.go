package debug

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollect(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "logs"), 0700)
	os.WriteFile(filepath.Join(tmpDir, "logs", "test.log"), []byte("line1\nline2\nline3\n"), 0600)
	os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte("provider: openai\n"), 0600)

	collector := New(tmpDir)
	opts := DefaultCollectOptions()

	info, err := collector.Collect(opts)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if info.Version != "v0.9.0" {
		t.Errorf("expected version v0.9.0, got %s", info.Version)
	}
	if info.OS == "" {
		t.Error("OS should not be empty")
	}
	if info.Arch == "" {
		t.Error("Arch should not be empty")
	}
	if info.GoVersion == "" {
		t.Error("GoVersion should not be empty")
	}
}

func TestCollectEnv(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(tmpDir, 0700)

	os.Setenv("LH_TEST_VAR", "test_value")
	os.Setenv("OPENAI_API_KEY", "sk-1234567890abcdef")
	defer os.Unsetenv("LH_TEST_VAR")
	defer os.Unsetenv("OPENAI_API_KEY")

	collector := New(tmpDir)
	env := collector.collectEnv()

	if env["LH_TEST_VAR"] != "test_value" {
		t.Errorf("expected LH_TEST_VAR=test_value, got %s", env["LH_TEST_VAR"])
	}

	// API key 应该被脱敏
	if env["OPENAI_API_KEY"] == "sk-1234567890abcdef" {
		t.Error("OPENAI_API_KEY should be masked")
	}
}

func TestCollectConfig(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"provider": "openai", "model": "gpt-4o"}`+"\n"), 0600)

	collector := New(tmpDir)
	config := collector.collectConfig()

	if config["exists"] != true {
		t.Error("config should exist")
	}
}

func TestCollectLogs(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "logs"), 0700)
	os.WriteFile(filepath.Join(tmpDir, "logs", "app.log"), []byte("log line 1\nlog line 2\nlog line 3\n"), 0600)

	collector := New(tmpDir)
	logs := collector.collectLogs(10)

	if len(logs) == 0 {
		t.Error("should have log entries")
	}
}

func TestExport(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "logs"), 0700)

	collector := New(tmpDir)
	opts := DefaultCollectOptions()

	outputPath, err := collector.Export(opts, filepath.Join(tmpDir, "debug.json"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Error("debug file not created")
	}

	// 验证 JSON 格式
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read debug file: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse debug JSON: %v", err)
	}

	if result["version"] != "v0.9.0" {
		t.Errorf("unexpected version: %v", result["version"])
	}
}

func TestExportAutoName(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "logs"), 0700)

	collector := New(tmpDir)
	opts := DefaultCollectOptions()

	outputPath, err := collector.Export(opts, "")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if !strings.Contains(outputPath, "debug_") {
		t.Errorf("unexpected output path: %s", outputPath)
	}

	if !strings.HasSuffix(outputPath, ".json") {
		t.Errorf("expected .json suffix: %s", outputPath)
	}
}

func TestIsSensitive(t *testing.T) {
	tests := []struct {
		key      string
		expected bool
	}{
		{"API_KEY", true},
		{"MY_SECRET", true},
		{"ACCESS_TOKEN", true},
		{"PASSWORD", true},
		{"NORMAL_VAR", false},
		{"LH_DEBUG", false},
		{"HOME", false},
	}

	for _, tt := range tests {
		result := isSensitive(tt.key)
		if result != tt.expected {
			t.Errorf("isSensitive(%q) = %v, want %v", tt.key, result, tt.expected)
		}
	}
}

func TestMaskValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sk-1234567890abcdef", "sk-1...cdef"},
		{"short", "***"},
		{"12345678", "***"},
		{"123456789", "1234...6789"},
	}

	for _, tt := range tests {
		result := maskValue(tt.input)
		if result != tt.expected {
			t.Errorf("maskValue(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestCollectNoLogs(t *testing.T) {
	tmpDir := t.TempDir()
	// 不创建 logs 目录

	collector := New(tmpDir)
	logs := collector.collectLogs(10)

	if len(logs) == 0 {
		t.Error("should return at least one entry")
	}
	if logs[0] != "no logs directory" {
		t.Errorf("unexpected log entry: %s", logs[0])
	}
}
