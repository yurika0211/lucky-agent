package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	delegateruntime "github.com/yurika0211/luckyagent-orchestrator/delegate"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

// messageHoldProvider calls file_read and waits until the test releases the
// tool. The next model turn must already contain the parent note.
type messageHoldProvider struct {
	started chan struct{}
	release chan struct{}
	note    string
	reads   int
}

func (p *messageHoldProvider) Name() string    { return "message-hold" }
func (p *messageHoldProvider) Validate() error { return nil }
func (p *messageHoldProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return p.ChatWithOptions(ctx, messages, provider.CallOptions{})
}
func (p *messageHoldProvider) ChatWithOptions(_ context.Context, messages []provider.Message, _ provider.CallOptions) (*provider.Response, error) {
	var lastUser string
	sawTool := false
	for _, msg := range messages {
		if msg.Role == "tool" || len(msg.ToolCalls) > 0 {
			sawTool = true
		}
		if msg.Role == "user" {
			lastUser = msg.Content
		}
	}
	if strings.Contains(lastUser, p.note) {
		return &provider.Response{Content: "saw " + p.note}, nil
	}
	if sawTool {
		return &provider.Response{Content: "finished without the parent note"}, nil
	}
	p.reads++
	return &provider.Response{ToolCalls: []provider.ToolCall{{
		ID:        "read-1",
		Name:      "file_read",
		Arguments: `{"path":"notes.md"}`,
	}}}, nil
}
func (p *messageHoldProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return p.ChatStreamWithOptions(ctx, messages, provider.CallOptions{})
}
func (p *messageHoldProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	response, err := p.ChatWithOptions(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.StreamChunk, 2)
	if len(response.ToolCalls) > 0 {
		call := response.ToolCalls[0]
		ch <- provider.StreamChunk{ToolCallDeltas: []provider.StreamToolCallDelta{{Index: 0, ID: call.ID, Name: call.Name, Arguments: call.Arguments}}}
	} else {
		ch <- provider.StreamChunk{Content: response.Content}
	}
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func TestDelegateMessageReachesChildAfterCurrentTool(t *testing.T) {
	note := "TURN-LEFT-AFTER-THIS-READ"
	hold := &messageHoldProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
		note:    note,
	}
	var toolOnce sync.Once
	toolEntered := make(chan struct{})
	toolRelease := make(chan struct{})
	agent, manager := resumeTestAgent(t, hold, "file body")
	agent.tools.Register(&tool.Tool{
		Name: "file_read", Enabled: true, Permission: tool.PermAuto,
		Handler: func(map[string]any) (string, error) {
			toolOnce.Do(func() { close(toolEntered) })
			<-toolRelease
			return "file body", nil
		},
	})

	parent := "parent-session"
	startedRaw, err := manager.HandleDelegateWithOwner(map[string]any{
		"description": "read notes.md",
	}, delegateruntime.Owner{SessionID: parent})
	if err != nil {
		t.Fatal(err)
	}
	started := decodeStart(t, startedRaw)
	select {
	case <-toolEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("file_read did not start")
	}
	before := delegateTaskCount(t, manager, parent)
	sub, ok := agent.tools.Get("subagent")
	if !ok {
		t.Fatal("subagent tool missing")
	}
	queued, err := sub.ContextDetailedHandler(tool.ExecutionContext{SessionID: parent}, map[string]any{
		"action":  "message",
		"task_id": started.TaskID,
		"message": note,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(queued.Output, `"queued":true`) {
		t.Fatalf("queue response=%s", queued.Output)
	}
	if got := delegateTaskCount(t, manager, parent); got != before {
		t.Fatalf("message created a task: %d -> %d", before, got)
	}
	statusRaw, err := sub.ContextDetailedHandler(tool.ExecutionContext{SessionID: parent}, map[string]any{
		"action":         "status",
		"task_id":        started.TaskID,
		"include_result": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusRaw.Output, `"pending_messages":1`) || strings.Contains(statusRaw.Output, note) {
		t.Fatalf("status=%s", statusRaw.Output)
	}
	close(toolRelease)
	done := waitDelegateStatus(t, manager, started.TaskID, parent)
	if done.Status != "completed" || !strings.Contains(done.Result, note) {
		t.Fatalf("result=%s err=%s", done.Result, done.Error)
	}
	child := agent.Sessions().Ensure(started.ExecutionSessionID)
	if history := fmt.Sprint(child.GetMessages()); !strings.Contains(history, "Parent message:") || !strings.Contains(history, note) {
		t.Fatal("child session did not record the parent message")
	}
	if got := delegateTaskCount(t, manager, parent); got != before {
		t.Fatalf("finished message created a task: %d", got)
	}
}

func TestDelegateMessageFifthDoesNotDropTheFirstFour(t *testing.T) {
	release := make(chan struct{})
	_, manager := resumeTestAgent(t, &messageHoldProvider{note: "unused", started: make(chan struct{}), release: make(chan struct{})}, "unused")
	manager.SetAgentExecutor(func(context.Context, string, string) (string, error) {
		<-release
		return "done", nil
	})
	parent := "parent-session"
	raw, err := manager.HandleDelegateWithOwner(map[string]any{"description": "hold"}, delegateruntime.Owner{SessionID: parent})
	if err != nil {
		t.Fatal(err)
	}
	running := decodeStart(t, raw)
	pollDelegateStatus(t, manager, running.TaskID, parent, "running")
	agent := messageAgentFor(t, manager)
	sub, _ := agent.tools.Get("subagent")
	for i := 1; i <= 4; i++ {
		out, err := sub.ContextDetailedHandler(tool.ExecutionContext{SessionID: parent}, map[string]any{
			"action":  "message",
			"task_id": running.TaskID,
			"message": fmt.Sprintf("note-%d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Output, fmt.Sprintf(`"pending_messages":%d`, i)) {
			t.Fatalf("queued=%s", out.Output)
		}
	}
	if _, err := sub.ContextDetailedHandler(tool.ExecutionContext{SessionID: parent}, map[string]any{
		"action":  "message",
		"task_id": running.TaskID,
		"message": "note-5",
	}); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("fifth err=%v", err)
	}
	if got := agent.parentMessages().count(running.TaskID); got != 4 {
		t.Fatalf("queue=%d", got)
	}
	taken := agent.parentMessages().take(running.TaskID)
	if len(taken) != 4 || taken[0].body != "note-1" || taken[3].body != "note-4" {
		t.Fatalf("kept=%+v", taken)
	}
	close(release)
}

func TestDelegateMessageRejectsMissingAndForeignTasks(t *testing.T) {
	agent, manager := resumeTestAgent(t, &messageHoldProvider{note: "unused", started: make(chan struct{}), release: make(chan struct{})}, "unused")
	release := make(chan struct{})
	manager.SetAgentExecutor(func(context.Context, string, string) (string, error) {
		<-release
		return "done", nil
	})
	parent := "parent-session"
	raw, err := manager.HandleDelegateWithOwner(map[string]any{"description": "hold"}, delegateruntime.Owner{SessionID: parent})
	if err != nil {
		t.Fatal(err)
	}
	running := decodeStart(t, raw)
	pollDelegateStatus(t, manager, running.TaskID, parent, "running")
	sub, _ := agent.tools.Get("subagent")
	if _, err := sub.ContextDetailedHandler(tool.ExecutionContext{SessionID: parent}, map[string]any{
		"action":  "message",
		"task_id": "task-missing",
		"message": "hello",
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing err=%v", err)
	}
	if _, err := sub.ContextDetailedHandler(tool.ExecutionContext{SessionID: "other-session"}, map[string]any{
		"action":  "message",
		"task_id": running.TaskID,
		"message": "hello",
	}); err == nil || !strings.Contains(err.Error(), "current session") {
		t.Fatalf("foreign err=%v", err)
	}
	if got := agent.parentMessages().count(running.TaskID); got != 0 {
		t.Fatalf("rejected message was queued: %d", got)
	}
	close(release)
}

func TestParentMessagesInitIsRaceFreeAndKeepsQueuedNotes(t *testing.T) {
	agent := &Agent{}
	const tasks = 32
	var wg sync.WaitGroup
	errCh := make(chan error, tasks)
	for i := 0; i < tasks; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			taskID := fmt.Sprintf("task-%d", i)
			if err := agent.parentMessages().enqueue(taskID, "parent", fmt.Sprintf("note-%d", i)); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	box := agent.parentMessages()
	if box == nil {
		t.Fatal("queue was not published")
	}
	if again := agent.parentMessages(); again != box {
		t.Fatal("parentMessages published a second queue")
	}
	for i := 0; i < tasks; i++ {
		taskID := fmt.Sprintf("task-%d", i)
		taken := box.take(taskID)
		if len(taken) != 1 || taken[0].body != fmt.Sprintf("note-%d", i) {
			t.Fatalf("task %s kept %+v", taskID, taken)
		}
	}
}

func TestSubagentMessageRejectedInsideChild(t *testing.T) {
	agent, _ := resumeTestAgent(t, &messageHoldProvider{note: "unused", started: make(chan struct{}), release: make(chan struct{})}, "unused")
	sub, ok := agent.tools.Get("subagent")
	if !ok || sub.Parameters["message"].Type != "string" || !strings.Contains(sub.Parameters["action"].Description, "message") {
		t.Fatal("action=message is missing from the wrapped schema")
	}
	_, err := sub.ContextDetailedHandler(tool.ExecutionContext{
		Context:   context.Background(),
		SessionID: "child-session",
		Source:    "subagent",
	}, map[string]any{
		"action":                   "message",
		"task_id":                  "task-parent",
		"message":                  "from the child",
		"allow_recursive_delegate": true,
	})
	if err == nil || !strings.Contains(err.Error(), "parent session") {
		t.Fatalf("child err=%v", err)
	}
}

func TestDelegateMessageOnCompletedResumes(t *testing.T) {
	marker := "RESUME-FROM-MESSAGE"
	script := &resumeScriptProvider{marker: marker}
	agent, manager := resumeTestAgent(t, script, marker)
	parent := "parent-session"
	raw, err := manager.HandleDelegateWithOwner(map[string]any{"description": "read once"}, delegateruntime.Owner{SessionID: parent})
	if err != nil {
		t.Fatal(err)
	}
	started := decodeStart(t, raw)
	original := waitDelegateStatus(t, manager, started.TaskID, parent)
	if original.Status != "completed" {
		t.Fatal(original.Error)
	}
	before := delegateTaskCount(t, manager, parent)
	sub, _ := agent.tools.Get("subagent")
	out, err := sub.ContextDetailedHandler(tool.ExecutionContext{SessionID: parent}, map[string]any{
		"action":  "message",
		"task_id": started.TaskID,
		"message": "quote the marker",
	})
	if err != nil {
		t.Fatal(err)
	}
	resumed := decodeStart(t, out.Output)
	if resumed.TaskID == "" || resumed.TaskID == started.TaskID || resumed.ResumedFrom != started.TaskID {
		t.Fatalf("start=%+v", resumed)
	}
	if resumed.ExecutionSessionID != started.ExecutionSessionID {
		t.Fatalf("session %s want %s", resumed.ExecutionSessionID, started.ExecutionSessionID)
	}
	if got := delegateTaskCount(t, manager, parent); got != before+1 {
		t.Fatalf("tasks %d -> %d", before, got)
	}
	finished := waitDelegateStatus(t, manager, resumed.TaskID, parent)
	if finished.Status != "completed" || !strings.Contains(finished.Result, marker) {
		t.Fatalf("resume result=%s err=%s", finished.Result, finished.Error)
	}
	again := waitDelegateStatus(t, manager, started.TaskID, parent)
	if again.Status != original.Status || again.Result != original.Result {
		t.Fatal("original task changed")
	}
}

func messageAgentFor(t *testing.T, manager *tool.DelegateManager) *Agent {
	t.Helper()
	agent, live := resumeTestAgent(t, &messageHoldProvider{note: "unused", started: make(chan struct{}), release: make(chan struct{})}, "unused")
	_ = live
	agent.delegate = manager
	agent.bindOptionalToolServices()
	return agent
}
