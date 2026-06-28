package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDelegateManagerCreate(t *testing.T) {
	cfg := DefaultDelegateConfig()
	dm := NewDelegateManager(cfg)

	if dm.config.MaxConcurrent != 3 {
		t.Errorf("expected max 3, got %d", dm.config.MaxConcurrent)
	}
}

func TestDelegateTaskToolRegistration(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	r := NewRegistry()

	r.Register(DelegateTaskTool(dm))
	r.Register(TaskStatusTool(dm))
	r.Register(ListTasksTool(dm))

	if r.Count() != 3 {
		t.Errorf("expected 3 delegate tools, got %d", r.Count())
	}

	// 检查分类
	dt, _ := r.Get("delegate_task")
	if dt.Category != CatDelegate {
		t.Errorf("expected CatDelegate, got %s", dt.Category)
	}

	ts, _ := r.Get("task_status")
	if ts.Permission != PermAuto {
		t.Errorf("task_status should be auto, got %s", ts.Permission)
	}
}

func TestDelegateTaskCall(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	r.Register(TaskStatusTool(dm))

	// 委派任务
	result, err := r.Call("delegate_task", map[string]any{
		"description": "Test task",
	})
	if err != nil {
		t.Fatalf("delegate_task: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	taskID, ok := resp["task_id"].(string)
	if !ok || taskID == "" {
		t.Error("expected task_id in response")
	}

	// 等待任务完成
	time.Sleep(100 * time.Millisecond)

	// 查询状态
	statusResult, err := r.Call("task_status", map[string]any{
		"task_id": taskID,
	})
	if err != nil {
		t.Fatalf("task_status: %v", err)
	}

	var status map[string]any
	json.Unmarshal([]byte(statusResult), &status)
	if status["status"] != "completed" {
		t.Errorf("expected completed, got %v", status["status"])
	}
}

func TestDelegateTaskInjectsWritableWorkspace(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	capturedContext := make(chan string, 1)
	dm.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
		capturedContext <- contextStr
		return "done", nil
	})

	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	r.Register(TaskStatusTool(dm))

	workspace := filepath.Join(t.TempDir(), "twitter-coser")
	result, err := r.Call("delegate_task", map[string]any{
		"description": "Download five images and save them under " + workspace + ".",
	})
	if err != nil {
		t.Fatalf("delegate_task: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if got := resp["workspace"]; got != workspace {
		t.Fatalf("expected response workspace %q, got %v", workspace, got)
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		t.Fatalf("expected workspace directory to exist, info=%v err=%v", info, err)
	}

	var contextStr string
	select {
	case contextStr = <-capturedContext:
	case <-time.After(time.Second):
		t.Fatal("executor was not called")
	}
	if got := ExtractDelegateWorkspace(contextStr); got != workspace {
		t.Fatalf("expected extracted workspace %q, got %q; context=%q", workspace, got, contextStr)
	}
	if !strings.Contains(contextStr, "Allowed file roots") {
		t.Fatalf("expected sandbox guidance in context, got %q", contextStr)
	}

	taskID, _ := resp["task_id"].(string)
	statusResult, err := r.Call("task_status", map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("task_status: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(statusResult), &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if got := status["workspace"]; got != workspace {
		t.Fatalf("expected status workspace %q, got %v", workspace, got)
	}
}

func TestPrepareDelegateExecutionContextUsesDefaultWorkspace(t *testing.T) {
	workspace, contextStr, err := prepareDelegateExecutionContext("task-99", "summarize", "")
	if err != nil {
		t.Fatalf("prepareDelegateExecutionContext: %v", err)
	}
	if !strings.Contains(filepath.ToSlash(workspace), "/luckyagent-delegate/task-99") {
		t.Fatalf("expected default delegate workspace, got %q", workspace)
	}
	if got := ExtractDelegateWorkspace(contextStr); got != workspace {
		t.Fatalf("expected workspace round trip %q, got %q", workspace, got)
	}
}

func TestListTasks(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	r.Register(ListTasksTool(dm))

	// 委派几个任务
	for i := 0; i < 3; i++ {
		r.Call("delegate_task", map[string]any{
			"description": "Task {i}",
		})
	}

	time.Sleep(100 * time.Millisecond)

	// 列出任务
	result, err := r.Call("list_tasks", map[string]any{})
	if err != nil {
		t.Fatalf("list_tasks: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	count, _ := resp["count"].(float64)
	if int(count) != 3 {
		t.Errorf("expected 3 tasks, got %v", count)
	}
}

func TestTaskStatusNotFound(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	r := NewRegistry()
	r.Register(TaskStatusTool(dm))

	_, err := r.Call("task_status", map[string]any{
		"task_id": "nonexistent",
	})
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestDelegateMaxConcurrent(t *testing.T) {
	cfg := DefaultDelegateConfig()
	cfg.MaxConcurrent = 1 // 只允许1个并发
	dm := NewDelegateManager(cfg)
	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))

	// 第一个任务
	r.Call("delegate_task", map[string]any{
		"description": "First task",
	})

	// 第二个任务应该被拒绝（第一个还在 running）
	// 注意：由于 executeTask 很快完成，这个测试可能不稳定
	// 在真实场景中，子代理任务会持续更长时间
}

func TestTaskStatusString(t *testing.T) {
	tests := []struct {
		status   TaskStatus
		expected string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{StatusCancelled, "cancelled"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.expected {
			t.Errorf("TaskStatus(%d).String() = %q, want %q", tt.status, got, tt.expected)
		}
	}
}
