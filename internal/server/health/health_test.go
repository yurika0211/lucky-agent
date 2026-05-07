package health

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewHealthCheck(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	if hc == nil {
		t.Fatal("expected non-nil health check")
	}
	if hc.version != "0.17.0" {
		t.Errorf("expected version 0.17.0, got %s", hc.version)
	}
}

func TestLiveness(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	report := hc.Liveness()

	if report.Status != StatusHealthy {
		t.Errorf("expected healthy status, got %s", report.Status)
	}
	if report.Version != "0.17.0" {
		t.Errorf("expected version 0.17.0, got %s", report.Version)
	}
	if report.Uptime == "" {
		t.Error("expected non-empty uptime")
	}
	if report.System.GoVersion == "" {
		t.Error("expected non-empty go version")
	}
	if report.System.NumCPU <= 0 {
		t.Error("expected positive num cpu")
	}
}

func TestReadinessNoChecks(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	report := hc.Readiness()

	if report.Status != StatusHealthy {
		t.Errorf("expected healthy with no checks, got %s", report.Status)
	}
}

func TestReadinessWithHealthyCheck(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	hc.RegisterCheck("memory", func() CheckResult {
		return CheckResult{
			Name:   "memory",
			Status: StatusHealthy,
		}
	})

	report := hc.Readiness()
	if report.Status != StatusHealthy {
		t.Errorf("expected healthy, got %s", report.Status)
	}
	if len(report.Checks) != 1 {
		t.Errorf("expected 1 check, got %d", len(report.Checks))
	}
}

func TestReadinessWithUnhealthyCheck(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	hc.RegisterCheck("database", func() CheckResult {
		return CheckResult{
			Name:   "database",
			Status: StatusUnhealthy,
			Error:  "connection refused",
		}
	})

	report := hc.Readiness()
	if report.Status != StatusUnhealthy {
		t.Errorf("expected unhealthy, got %s", report.Status)
	}
	if report.Checks["database"].Status != StatusUnhealthy {
		t.Error("expected database check to be unhealthy")
	}
}

func TestReadinessWithDegradedCheck(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	hc.RegisterCheck("cache", func() CheckResult {
		return CheckResult{
			Name:   "cache",
			Status: StatusDegraded,
			Error:  "high latency",
		}
	})

	report := hc.Readiness()
	if report.Status != StatusDegraded {
		t.Errorf("expected degraded, got %s", report.Status)
	}
}

func TestReadinessMixedChecks(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	hc.RegisterCheck("memory", func() CheckResult {
		return CheckResult{Name: "memory", Status: StatusHealthy}
	})
	hc.RegisterCheck("cache", func() CheckResult {
		return CheckResult{Name: "cache", Status: StatusDegraded, Error: "slow"}
	})
	hc.RegisterCheck("database", func() CheckResult {
		return CheckResult{Name: "database", Status: StatusUnhealthy, Error: "down"}
	})

	report := hc.Readiness()
	// unhealthy 优先级最高
	if report.Status != StatusUnhealthy {
		t.Errorf("expected unhealthy (worst status), got %s", report.Status)
	}
}

func TestToJSON(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	report := hc.Liveness()
	data, err := report.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "healthy") {
		t.Error("expected healthy in JSON output")
	}
	if !strings.Contains(string(data), "0.17.0") {
		t.Error("expected version in JSON output")
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		d        time.Duration
		contains string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m 30s"},
		{3661 * time.Second, "1h 1m 1s"},
		{90061 * time.Second, "1d 1h 1m 1s"},
	}
	for _, tt := range tests {
		result := FormatUptime(tt.d)
		if !strings.Contains(result, tt.contains) {
			t.Errorf("FormatUptime(%v) = %q, want to contain %q", tt.d, result, tt.contains)
		}
	}
}

func TestSystemInfo(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	report := hc.Readiness()

	if report.System.NumGoroutine <= 0 {
		t.Error("expected positive goroutine count")
	}
	if report.System.HeapAllocMB <= 0 {
		t.Error("expected positive heap alloc")
	}
}

func TestRegisterCheckIdempotent(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	hc.RegisterCheck("test", func() CheckResult {
		return CheckResult{Name: "test", Status: StatusHealthy}
	})
	hc.RegisterCheck("test", func() CheckResult {
		return CheckResult{Name: "test", Status: StatusDegraded}
	})

	report := hc.Readiness()
	if len(report.Checks) != 1 {
		t.Errorf("expected 1 check (overwritten), got %d", len(report.Checks))
	}
	// 第二次注册覆盖第一次
	if report.Checks["test"].Status != StatusDegraded {
		t.Error("expected degraded (overwritten check)")
	}
}

func TestDetail(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	hc.RegisterCheck("memory", func() CheckResult {
		return CheckResult{Name: "memory", Status: StatusHealthy}
	})

	report := hc.Detail()
	if report.Status != StatusHealthy {
		t.Errorf("expected healthy, got %s", report.Status)
	}
	if len(report.Checks) != 1 {
		t.Errorf("expected 1 check, got %d", len(report.Checks))
	}
}

func TestJSONRoundTrip(t *testing.T) {
	hc := NewHealthCheck("0.17.0")
	hc.RegisterCheck("test", func() CheckResult {
		return CheckResult{Name: "test", Status: StatusHealthy, Duration: 10 * time.Millisecond}
	})

	report := hc.Readiness()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}

	var parsed HealthReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Version != "0.17.0" {
		t.Error("version mismatch after JSON round trip")
	}
}
