package tool

import (
	"testing"
	"time"
)

func TestUsageTrackerRecord(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record("user1", "terminal", 100*time.Millisecond, true)
	tracker.Record("user1", "terminal", 200*time.Millisecond, true)
	tracker.Record("user1", "terminal", 50*time.Millisecond, false)

	stats := tracker.GetUsage("user1", "terminal")
	if stats.TotalCalls != 3 {
		t.Errorf("expected 3 calls, got %d", stats.TotalCalls)
	}
	if stats.SuccessCalls != 2 {
		t.Errorf("expected 2 success, got %d", stats.SuccessCalls)
	}
	if stats.FailedCalls != 1 {
		t.Errorf("expected 1 failed, got %d", stats.FailedCalls)
	}
}

func TestUsageTrackerQuota(t *testing.T) {
	tracker := NewUsageTracker()

	// 设置配额
	err := tracker.SetQuota("user1", "terminal", "daily", 3)
	if err != nil {
		t.Fatalf("set quota: %v", err)
	}

	// 配额内
	if !tracker.CheckQuota("user1", "terminal") {
		t.Error("expected quota check to pass")
	}

	// 增加使用
	tracker.IncrementUsage("user1", "terminal")
	tracker.IncrementUsage("user1", "terminal")
	tracker.IncrementUsage("user1", "terminal")

	// 超配额
	if tracker.CheckQuota("user1", "terminal") {
		t.Error("expected quota check to fail after 3 uses")
	}
}

func TestUsageTrackerNoQuota(t *testing.T) {
	tracker := NewUsageTracker()

	// 无配额限制
	if !tracker.CheckQuota("user1", "terminal") {
		t.Error("expected quota check to pass without quota")
	}
}

func TestUsageTrackerInvalidWindow(t *testing.T) {
	tracker := NewUsageTracker()

	err := tracker.SetQuota("user1", "terminal", "weekly", 10)
	if err == nil {
		t.Error("expected error for invalid window")
	}
}

func TestUsageTrackerRemoveQuota(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.SetQuota("user1", "terminal", "daily", 5)
	tracker.RemoveQuota("user1", "terminal")

	// 移除后应该无限制
	if !tracker.CheckQuota("user1", "terminal") {
		t.Error("expected quota check to pass after removal")
	}
}

func TestUsageTrackerGetAllUsage(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record("user1", "terminal", 100*time.Millisecond, true)
	tracker.Record("user1", "file_read", 50*time.Millisecond, true)
	tracker.Record("user1", "terminal", 200*time.Millisecond, true)

	allStats := tracker.GetAllUsage("user1")
	if len(allStats) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(allStats))
	}

	// terminal 应该排第一（2次调用）
	if allStats[0].ToolName != "terminal" {
		t.Errorf("expected terminal first, got %s", allStats[0].ToolName)
	}
}

func TestUsageTrackerListQuotas(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.SetQuota("user1", "terminal", "daily", 10)
	tracker.SetQuota("user1", "file_read", "hourly", 50)

	quotas := tracker.ListQuotas("user1")
	if len(quotas) != 2 {
		t.Fatalf("expected 2 quotas, got %d", len(quotas))
	}
}

func TestUsageTrackerRecordLimit(t *testing.T) {
	tracker := NewUsageTracker()

	// 记录超过 1000 条
	for i := 0; i < 1100; i++ {
		tracker.Record("user1", "terminal", 10*time.Millisecond, true)
	}

	// 应该只保留最近 1000 条
	stats := tracker.GetUsage("user1", "terminal")
	if stats.TotalCalls != 1000 {
		t.Errorf("expected 1000 records (trimmed), got %d", stats.TotalCalls)
	}
}

func TestUsageStatsFormat(t *testing.T) {
	stats := UsageStats{
		ToolName:     "terminal",
		TotalCalls:   10,
		SuccessCalls: 9,
		AvgDuration:  150 * time.Millisecond,
	}
	formatted := stats.Format()
	if formatted == "" {
		t.Error("expected non-empty format")
	}
}
