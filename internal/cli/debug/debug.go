package debug

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ShareInfo 调试信息
type ShareInfo struct {
	Timestamp   string                   `json:"timestamp"`
	Version     string                   `json:"version"`
	OS          string                   `json:"os"`
	Arch        string                   `json:"arch"`
	GoVersion   string                   `json:"go_version"`
	Profile     map[string]interface{}   `json:"profile,omitempty"`
	Config      map[string]interface{}   `json:"config,omitempty"`
	MemoryStats map[string]interface{}   `json:"memory_stats,omitempty"`
	Tools       []string                 `json:"tools,omitempty"`
	CronJobs    []map[string]interface{} `json:"cron_jobs,omitempty"`
	Env         map[string]string        `json:"env,omitempty"`
	Logs        []string                 `json:"logs,omitempty"`
}

// Collector 收集调试信息
type Collector struct {
	homeDir string
}

// New 创建调试信息收集器
func New(homeDir string) *Collector {
	return &Collector{homeDir: homeDir}
}

// Collect 收集所有调试信息
func (c *Collector) Collect(opts CollectOptions) (*ShareInfo, error) {
	info := &ShareInfo{
		Timestamp: time.Now().Format(time.RFC3339),
		Version:   "v0.9.0",
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
	}

	// 收集环境变量（脱敏）
	if opts.Env {
		info.Env = c.collectEnv()
	}

	// 收集配置
	if opts.Config {
		info.Config = c.collectConfig()
	}

	// 收集日志
	if opts.Logs {
		info.Logs = c.collectLogs(50)
	}

	return info, nil
}

// CollectOptions 收集选项
type CollectOptions struct {
	Env    bool
	Config bool
	Logs   bool
}

// DefaultCollectOptions 默认收集选项
func DefaultCollectOptions() CollectOptions {
	return CollectOptions{
		Env:    true,
		Config: true,
		Logs:   true,
	}
}

// collectEnv 收集环境变量（脱敏）
func (c *Collector) collectEnv() map[string]string {
	env := make(map[string]string)
	for _, pair := range os.Environ() {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := parts[1]

		// 脱敏敏感变量
		if isSensitive(key) {
			value = maskValue(value)
		}

		// 只收集 LH_ 前缀或常见相关变量
		if strings.HasPrefix(key, "LH_") || strings.HasPrefix(key, "OPENAI_") ||
			strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "HOME") ||
			strings.HasPrefix(key, "PATH") {
			env[key] = value
		}
	}
	return env
}

// collectConfig 收集配置信息
func (c *Collector) collectConfig() map[string]interface{} {
	// v0.55.1: 统一使用 config.json
	configPath := filepath.Join(c.homeDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return map[string]interface{}{"error": "config not found"}
	}

	// 简单返回行数和大小
	lines := strings.Count(string(data), "\n")
	return map[string]interface{}{
		"size":   len(data),
		"lines":  lines,
		"exists": true,
	}
}

// collectLogs 收集最近的日志
func (c *Collector) collectLogs(maxLines int) []string {
	logDir := filepath.Join(c.homeDir, "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return []string{"no logs directory"}
	}

	var logs []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}

		path := filepath.Join(logDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		lines := strings.Split(string(data), "\n")
		start := 0
		if len(lines) > maxLines {
			start = len(lines) - maxLines
		}

		for i := start; i < len(lines); i++ {
			if lines[i] != "" {
				logs = append(logs, entry.Name()+": "+lines[i])
			}
		}
	}

	if len(logs) == 0 {
		return []string{"no log entries found"}
	}

	return logs
}

// Export 导出调试信息为 JSON
func (c *Collector) Export(opts CollectOptions, outputPath string) (string, error) {
	info, err := c.Collect(opts)
	if err != nil {
		return "", fmt.Errorf("collect: %w", err)
	}

	if outputPath == "" {
		timestamp := time.Now().Format("2006-01-02_150405")
		outputPath = filepath.Join(c.homeDir, fmt.Sprintf("debug_%s.json", timestamp))
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0600); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	return outputPath, nil
}

// isSensitive 判断环境变量名是否敏感
func isSensitive(key string) bool {
	sensitive := []string{"KEY", "SECRET", "TOKEN", "PASSWORD", "CREDENTIAL"}
	upper := strings.ToUpper(key)
	for _, s := range sensitive {
		if strings.Contains(upper, s) {
			return true
		}
	}
	return false
}

// maskValue 脱敏值
func maskValue(value string) string {
	if len(value) <= 8 {
		return "***"
	}
	return value[:4] + "..." + value[len(value)-4:]
}
