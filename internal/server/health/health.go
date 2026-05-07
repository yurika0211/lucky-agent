package health

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"time"
)

// Status 健康状态
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusDegraded  Status = "degraded"
	StatusUnhealthy Status = "unhealthy"
)

// CheckType 健康检查类型
type CheckType string

const (
	CheckLiveness  CheckType = "liveness"
	CheckReadiness CheckType = "readiness"
)

// CheckResult 单项检查结果
type CheckResult struct {
	Name      string        `json:"name"`
	Status    Status        `json:"status"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
}

// HealthCheck 健康检查器
type HealthCheck struct {
	mu      sync.RWMutex
	checks  map[string]CheckFunc
	version string
	started time.Time
}

// CheckFunc 健康检查函数
type CheckFunc func() CheckResult

// HealthReport 健康报告
type HealthReport struct {
	Status    Status                 `json:"status"`
	Version   string                 `json:"version"`
	Uptime    string                 `json:"uptime"`
	Timestamp time.Time              `json:"timestamp"`
	Checks    map[string]CheckResult `json:"checks,omitempty"`
	System    SystemInfo             `json:"system"`
}

// SystemInfo 系统信息
type SystemInfo struct {
	GoVersion    string  `json:"go_version"`
	NumGoroutine int     `json:"num_goroutine"`
	NumCPU       int     `json:"num_cpu"`
	HeapAllocMB  float64 `json:"heap_alloc_mb"`
	HeapSysMB    float64 `json:"heap_sys_mb"`
	StackInUseMB float64 `json:"stack_in_use_mb"`
}

// NewHealthCheck 创建健康检查器
func NewHealthCheck(version string) *HealthCheck {
	return &HealthCheck{
		checks:  make(map[string]CheckFunc),
		version: version,
		started: time.Now(),
	}
}

// RegisterCheck 注册健康检查
func (h *HealthCheck) RegisterCheck(name string, fn CheckFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = fn
}

// Liveness 存活检查（进程是否活着）
func (h *HealthCheck) Liveness() *HealthReport {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return &HealthReport{
		Status:    StatusHealthy,
		Version:   h.version,
		Uptime:    time.Since(h.started).String(),
		Timestamp: time.Now(),
		System:    getSystemInfo(),
	}
}

// Readiness 就绪检查（是否可以接受流量）
func (h *HealthCheck) Readiness() *HealthReport {
	h.mu.RLock()
	defer h.mu.RUnlock()

	report := &HealthReport{
		Version:   h.version,
		Uptime:    time.Since(h.started).String(),
		Timestamp: time.Now(),
		Checks:    make(map[string]CheckResult),
		System:    getSystemInfo(),
	}

	overallStatus := StatusHealthy

	for name, fn := range h.checks {
		result := fn()
		result.Timestamp = time.Now()
		report.Checks[name] = result

		if result.Status == StatusUnhealthy {
			overallStatus = StatusUnhealthy
		} else if result.Status == StatusDegraded && overallStatus != StatusUnhealthy {
			overallStatus = StatusDegraded
		}
	}

	report.Status = overallStatus
	return report
}

// Detail 详细检查（包含所有检查项的详细信息）
func (h *HealthCheck) Detail() *HealthReport {
	return h.Readiness()
}

// ToJSON 转为 JSON
func (r *HealthReport) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// getSystemInfo 获取系统信息
func getSystemInfo() SystemInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return SystemInfo{
		GoVersion:    runtime.Version(),
		NumGoroutine: runtime.NumGoroutine(),
		NumCPU:       runtime.NumCPU(),
		HeapAllocMB:  float64(m.HeapAlloc) / 1024 / 1024,
		HeapSysMB:    float64(m.HeapSys) / 1024 / 1024,
		StackInUseMB: float64(m.StackInuse) / 1024 / 1024,
	}
}

// FormatUptime 格式化运行时间
func FormatUptime(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm %ds", days, hours, minutes, seconds)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
