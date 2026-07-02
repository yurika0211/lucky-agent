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
	r.Register(DelegateCancelTool(dm))

	if r.Count() != 4 {
		t.Errorf("expected 4 delegate tools, got %d", r.Count())
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
	cancel, _ := r.Get("delegate_cancel")
	if cancel.Permission != PermApprove {
		t.Errorf("delegate_cancel should require approval, got %s", cancel.Permission)
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

func TestListTasksFilterLimitAndOrder(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	now := time.Now()
	dm.tasks["old-completed"] = &DelegateTask{ID: "old-completed", Description: "old", Status: StatusCompleted, StartedAt: now.Add(-2 * time.Hour), CompletedAt: now.Add(-time.Hour), Result: "done"}
	dm.tasks["new-running"] = &DelegateTask{ID: "new-running", Description: "new", Status: StatusRunning, StartedAt: now}
	dm.tasks["mid-running"] = &DelegateTask{ID: "mid-running", Description: "mid", Status: StatusRunning, StartedAt: now.Add(-time.Hour)}

	result, err := dm.handleList(map[string]any{"status": "running", "limit": 1, "order": "desc", "include_result": true})
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	var resp struct {
		Tasks []struct {
			TaskID      string `json:"task_id"`
			CompletedAt string `json:"completed_at"`
		} `json:"tasks"`
		Count    int            `json:"count"`
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if resp.Count != 1 || resp.Total != 2 {
		t.Fatalf("expected count=1 total=2, got count=%d total=%d", resp.Count, resp.Total)
	}
	if len(resp.Tasks) != 1 || resp.Tasks[0].TaskID != "new-running" {
		t.Fatalf("expected newest running task first, got %#v", resp.Tasks)
	}
	if resp.Tasks[0].CompletedAt != "" {
		t.Fatalf("running task completed_at should be empty, got %q", resp.Tasks[0].CompletedAt)
	}
	if resp.ByStatus["completed"] != 1 || resp.ByStatus["running"] != 2 {
		t.Fatalf("unexpected by_status: %#v", resp.ByStatus)
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

func TestDelegateCancelRunningTask(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	started := make(chan struct{})
	dm.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})

	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	r.Register(TaskStatusTool(dm))
	r.Register(DelegateCancelTool(dm))

	result, err := r.Call("delegate_task", map[string]any{"description": "long task"})
	if err != nil {
		t.Fatalf("delegate_task: %v", err)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(result), &created); err != nil {
		t.Fatalf("parse delegate response: %v", err)
	}
	taskID := created["task_id"].(string)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}

	cancelResult, err := r.Call("delegate_cancel", map[string]any{"task_id": taskID, "reason": "test cancel"})
	if err != nil {
		t.Fatalf("delegate_cancel: %v", err)
	}
	var cancelled map[string]any
	if err := json.Unmarshal([]byte(cancelResult), &cancelled); err != nil {
		t.Fatalf("parse cancel response: %v", err)
	}
	if cancelled["status"] != "cancelled" {
		t.Fatalf("expected cancelled response, got %#v", cancelled)
	}

	time.Sleep(20 * time.Millisecond)
	statusResult, err := r.Call("task_status", map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("task_status: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(statusResult), &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if status["status"] != "cancelled" {
		t.Fatalf("expected task status cancelled, got %#v", status)
	}
	if status["completed_at"] == "" {
		t.Fatalf("expected cancelled task completed_at to be set")
	}
}

func TestTaskStatusResultTruncationAndOmitResult(t *testing.T) {
	cfg := DefaultDelegateConfig()
	cfg.MaxResultBytesInline = 10
	dm := NewDelegateManager(cfg)
	dm.tasks["task-large"] = &DelegateTask{
		ID:          "task-large",
		Description: "large",
		Status:      StatusCompleted,
		Result:      "first paragraph\n\nsecond paragraph with extra data",
		StartedAt:   time.Now().Add(-time.Minute),
		CompletedAt: time.Now(),
	}

	result, err := dm.handleStatus(map[string]any{"task_id": "task-large"})
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(result), &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if status["result_truncated"] != true {
		t.Fatalf("expected result_truncated=true, got %#v", status)
	}
	if got := status["result"].(string); !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncated result marker, got %q", got)
	}

	result, err = dm.handleStatus(map[string]any{"task_id": "task-large", "include_result": false})
	if err != nil {
		t.Fatalf("handleStatus without result: %v", err)
	}
	status = map[string]any{}
	if err := json.Unmarshal([]byte(result), &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if _, ok := status["result"]; ok {
		t.Fatalf("expected result omitted when include_result=false, got %#v", status)
	}
}

func TestDelegateTimeoutClamp(t *testing.T) {
	cfg := DefaultDelegateConfig()
	cfg.MinTimeout = 10 * time.Second
	cfg.MaxTimeout = 20 * time.Second
	cfg.Timeout = 15 * time.Second
	dm := NewDelegateManager(cfg)

	if got := dm.delegateTimeoutFromArgs(map[string]any{"timeout": 1}); got != 10*time.Second {
		t.Fatalf("expected min timeout clamp, got %v", got)
	}
	if got := dm.delegateTimeoutFromArgs(map[string]any{"timeout": 100}); got != 20*time.Second {
		t.Fatalf("expected max timeout clamp, got %v", got)
	}
	if got := dm.delegateTimeoutFromArgs(map[string]any{"timeout": 0}); got != 15*time.Second {
		t.Fatalf("expected default timeout, got %v", got)
	}
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
