package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	delegateruntime "github.com/yurika0211/luckyagent-orchestrator/delegate"
	"github.com/yurika0211/luckyagent-runtime/bootstrap"
	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

// resumeScriptProvider reads once. A later turn that already contains the
// marker quotes it without another file_read.
type resumeScriptProvider struct {
	reads  int
	marker string
}

func (p *resumeScriptProvider) Name() string    { return "resume-script" }
func (p *resumeScriptProvider) Validate() error { return nil }
func (p *resumeScriptProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return p.ChatWithOptions(ctx, messages, provider.CallOptions{})
}
func (p *resumeScriptProvider) ChatWithOptions(_ context.Context, messages []provider.Message, _ provider.CallOptions) (*provider.Response, error) {
	joined := fmt.Sprint(messages)
	if strings.Contains(joined, p.marker) && strings.Contains(joined, "quote the marker") {
		return &provider.Response{Content: "quoted " + p.marker}, nil
	}
	if strings.Contains(joined, "quote the marker") {
		return &provider.Response{Content: "marker missing from context"}, nil
	}
	if strings.Contains(joined, p.marker) {
		// The final answer is long and omits the marker. Resume has to read
		// it from the child tool transcript, not from this inline result.
		return &provider.Response{Content: strings.Repeat("summary ", 800)}, nil
	}
	p.reads++
	return &provider.Response{ToolCalls: []provider.ToolCall{{
		ID:        fmt.Sprintf("read-%d", p.reads),
		Name:      "file_read",
		Arguments: `{"path":"notes/secret.md"}`,
	}}}, nil
}
func (p *resumeScriptProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return p.ChatStreamWithOptions(ctx, messages, provider.CallOptions{})
}
func (p *resumeScriptProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
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

func TestDelegateResumeReusesChildToolResult(t *testing.T) {
	marker := "ONLY-IN-TOOL-RESULT-7f3a"
	body := strings.Repeat("p", 4500) + "\n" + marker
	if len(body) < 4000 {
		t.Fatal("fixture is smaller than the inline result cap")
	}
	script := &resumeScriptProvider{marker: marker}
	agent, manager := resumeTestAgent(t, script, body)
	cfg := manager.Config()
	cfg.MaxResultBytesInline = 4000
	*manager = *tool.NewDelegateManager(cfg)
	agent.delegate = manager
	agent.bindOptionalExecutionServices()

	parent := "parent-session"
	startedRaw, err := manager.HandleDelegateWithOwner(map[string]any{
		"description": "read notes/secret.md",
	}, delegateruntime.Owner{SessionID: parent})
	if err != nil {
		t.Fatal(err)
	}
	started := decodeStart(t, startedRaw)
	original := waitDelegateStatus(t, manager, started.TaskID, parent)
	if original.Status != "completed" {
		t.Fatalf("original status=%s err=%s result=%s", original.Status, original.Error, original.Result)
	}
	if strings.Contains(original.Result, marker) {
		t.Fatal("final answer included the marker; resume must use the tool transcript")
	}
	if len(original.Result) < 4000 {
		t.Fatalf("final answer is %d bytes, shorter than the inline cap", len(original.Result))
	}
	statusRaw, err := manager.HandleStatusForSession(map[string]any{"task_id": started.TaskID, "include_result": true}, parent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(statusRaw, marker) {
		t.Fatal("inline status included the marker past the truncation cap")
	}
	if !strings.Contains(statusRaw, `"result_truncated":true`) {
		t.Fatalf("status=%s", statusRaw)
	}

	resumedRaw, err := manager.HandleDelegateWithOwner(map[string]any{
		"description": "quote the marker from the file you already read",
		"resume_from": started.TaskID,
	}, delegateruntime.Owner{SessionID: parent})
	if err != nil {
		t.Fatal(err)
	}
	resumed := decodeStart(t, resumedRaw)
	if resumed.TaskID == "" || resumed.TaskID == started.TaskID || resumed.ResumedFrom != started.TaskID {
		t.Fatalf("start=%+v", resumed)
	}
	if resumed.ExecutionSessionID != started.ExecutionSessionID {
		t.Fatalf("session %s want %s", resumed.ExecutionSessionID, started.ExecutionSessionID)
	}
	if resumed.Mode != started.Mode {
		t.Fatalf("resume replanned mode %s from %s", resumed.Mode, started.Mode)
	}
	finished := waitDelegateStatus(t, manager, resumed.TaskID, parent)
	if finished.Status != "completed" || !strings.Contains(finished.Result, marker) {
		t.Fatalf("resume result=%s err=%s", finished.Result, finished.Error)
	}
	if strings.Contains(finished.Result, "marker missing") {
		t.Fatal("resume could not see the tool result")
	}
	again := waitDelegateStatus(t, manager, started.TaskID, parent)
	if again.Status != original.Status || again.Result != original.Result {
		t.Fatal("original task status or result changed")
	}
	child := agent.Sessions().Ensure(started.ExecutionSessionID)
	if history := fmt.Sprint(child.GetMessages()); !strings.Contains(history, marker) {
		t.Fatal("child session dropped the tool result")
	}
}

func TestDelegateResumeRejectsRunningMissingAndForeignTasks(t *testing.T) {
	_, manager := resumeTestAgent(t, &resumeScriptProvider{marker: "unused"}, "unused")
	release := make(chan struct{})
	manager.SetAgentExecutor(func(context.Context, string, string) (string, error) {
		<-release
		return "done", nil
	})
	parent := "parent-session"
	raw, err := manager.HandleDelegateWithOwner(map[string]any{"description": "still going"}, delegateruntime.Owner{SessionID: parent})
	if err != nil {
		t.Fatal(err)
	}
	running := decodeStart(t, raw)
	runningStatus := pollDelegateStatus(t, manager, running.TaskID, parent, "running")
	if runningStatus.Status != "running" {
		t.Fatalf("status=%s", runningStatus.Status)
	}
	before := delegateTaskCount(t, manager, parent)
	for _, tc := range []struct {
		name  string
		id    string
		owner string
		want  string
	}{
		{"running", running.TaskID, parent, "running"},
		{"missing", "task-missing", parent, "not found"},
		{"foreign", running.TaskID, "other-session", "current session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := manager.HandleDelegateWithOwner(map[string]any{
				"description": "resume",
				"resume_from": tc.id,
			}, delegateruntime.Owner{SessionID: tc.owner})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if got := delegateTaskCount(t, manager, parent); got != before {
		t.Fatalf("rejected resume created a task: %d -> %d", before, got)
	}
	close(release)
	if done := waitDelegateStatus(t, manager, running.TaskID, parent); done.Status != "completed" {
		t.Fatal(done.Error)
	}
	if _, err := manager.HandleDelegateWithOwner(map[string]any{
		"description": "resume from another session",
		"resume_from": running.TaskID,
	}, delegateruntime.Owner{SessionID: "other-session"}); err == nil || !strings.Contains(err.Error(), "current session") {
		t.Fatalf("foreign err=%v", err)
	}
	if got := delegateTaskCount(t, manager, parent); got != before {
		t.Fatalf("foreign resume created a task: %d", got)
	}
}

func TestDelegateResumeKeepsOriginalDisabledTools(t *testing.T) {
	marker := "ROLE-MARKER-ONLY"
	script := &resumeScriptProvider{marker: marker}
	agent, manager := resumeTestAgent(t, script, marker)
	cfg := manager.Config()
	cfg.ChildDisabledTools = []string{"file_write"}
	cfg.ChildModel = "original-child-model"
	*manager = *tool.NewDelegateManager(cfg)
	agent.delegate = manager
	var (
		seenMu sync.Mutex
		seen   []LoopConfig
	)
	agent.delegateChildRun = func(_ context.Context, _ *session.Session, _ UserTurnInput, loopCfg LoopConfig) (*LoopResult, error) {
		seenMu.Lock()
		seen = append(seen, loopCfg)
		seenMu.Unlock()
		return &LoopResult{Response: "quoted " + marker}, nil
	}
	agent.bindOptionalExecutionServices()

	parent := "parent-session"
	raw, err := manager.HandleDelegateWithOwner(map[string]any{"description": "read once"}, delegateruntime.Owner{SessionID: parent})
	if err != nil {
		t.Fatal(err)
	}
	started := decodeStart(t, raw)
	if done := waitDelegateStatus(t, manager, started.TaskID, parent); done.Status != "completed" {
		t.Fatal(done.Error)
	}

	// The published manager has no config setter. A new manager would also
	// drop the finished task. The executor records the captured loop config,
	// which must stay on the original disable list and model.
	resumedRaw, err := manager.HandleDelegateWithOwner(map[string]any{
		"description": "quote the marker",
		"resume_from": started.TaskID,
		"mode":        "debate",
	}, delegateruntime.Owner{SessionID: parent})
	if err != nil {
		t.Fatal(err)
	}
	resumed := decodeStart(t, resumedRaw)
	if resumed.TaskID == started.TaskID || resumed.ResumedFrom != started.TaskID || resumed.ExecutionSessionID != started.ExecutionSessionID {
		t.Fatalf("start=%+v", resumed)
	}
	if resumed.Mode != "single" {
		t.Fatalf("resume replanned mode %s", resumed.Mode)
	}
	finished := waitDelegateStatus(t, manager, resumed.TaskID, parent)
	if finished.Status != "completed" || !strings.Contains(finished.Result, marker) {
		t.Fatalf("resume result=%s err=%s", finished.Result, finished.Error)
	}
	seenMu.Lock()
	got := append([]LoopConfig(nil), seen...)
	seenMu.Unlock()
	if len(got) != 2 || got[1].subagentModel != "original-child-model" {
		t.Fatalf("child models=%v", modelsOf(got))
	}
	if !toolNameInSet(makeToolNameSet(got[1].DisabledTools), "file_write") {
		t.Fatalf("resume dropped file_write disable: %v", got[1].DisabledTools)
	}
	if toolNameInSet(makeToolNameSet(got[1].DisabledTools), "terminal") {
		t.Fatalf("resume picked up the replacement disable list: %v", got[1].DisabledTools)
	}
}

func TestSubagentSchemaAdvertisesResumeFrom(t *testing.T) {
	agent, _ := resumeTestAgent(t, &resumeScriptProvider{marker: "unused"}, "unused")
	registered, ok := agent.tools.Get("subagent")
	if !ok || registered == nil || registered.Parameters["resume_from"].Type != "string" {
		t.Fatal("resume_from is missing from the wrapped subagent schema")
	}
	if registered.Parameters["isolation"].Type == "" || registered.Parameters["subagent_type"].Type == "" {
		t.Fatal("resume wrapping dropped isolation or subagent_type")
	}
	format := registered.ToOpenAIFormat()
	encoded, err := json.Marshal(format)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "resume_from") {
		t.Fatalf("openai schema omitted resume_from: %s", encoded)
	}
}

func resumeTestAgent(t *testing.T, p provider.Provider, readBody string) (*Agent, *tool.DelegateManager) {
	t.Helper()
	root := t.TempDir()
	cfg, err := config.NewManagerWithDir(root)
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(filepath.Join(sessionDir, "index.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	sessions, err := session.NewManager(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("provider", "openai"); err != nil || cfg.Set("model", "main-model") != nil {
		t.Fatal(err)
	}
	providerRegistry := provider.NewRegistry()
	providerRegistry.RegisterFactory("openai", func(c provider.Config) provider.Provider {
		return p
	})
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name: "file_read", Enabled: true, Permission: tool.PermAuto,
		Handler: func(map[string]any) (string, error) { return readBody, nil },
	})
	registry.Register(&tool.Tool{
		Name: "file_write", Enabled: true, Permission: tool.PermApprove,
		Handler: func(map[string]any) (string, error) { return "wrote", nil },
	})
	manager := tool.NewDelegateManager(tool.DefaultDelegateConfig())
	services := tool.NewServices(nil, nil, "", nil, nil, tool.ImageGenerationDefaults{}, nil, tool.TTSDefaults{}, nil, nil, manager)
	agent, err := NewWithDependencies(Options{TakeOwnership: true}, &Dependencies{
		Config:   cfg,
		Sessions: sessions,
		Provider: bootstrap.ProviderRuntime{Provider: p, Registry: providerRegistry},
		Support:  bootstrap.SupportRuntime{Tools: registry, DelegateMgr: manager, ToolServices: services},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.tools.Register(&tool.Tool{
		Name: "file_read", Enabled: true, Permission: tool.PermAuto,
		Handler: func(map[string]any) (string, error) { return readBody, nil },
	})
	t.Cleanup(func() { _ = agent.Close() })
	return agent, manager
}

type resumeStart struct {
	TaskID             string `json:"task_id"`
	ExecutionSessionID string `json:"execution_session_id"`
	Status             string `json:"status"`
	Mode               string `json:"mode"`
	ResumedFrom        string `json:"resumed_from"`
}

func decodeStart(t *testing.T, raw string) resumeStart {
	t.Helper()
	var started resumeStart
	if err := json.Unmarshal([]byte(raw), &started); err != nil {
		t.Fatal(err)
	}
	return started
}

func readDelegateStatus(t *testing.T, manager *tool.DelegateManager, id, sessionID string) resumeStatus {
	t.Helper()
	raw, err := manager.HandleStatusForSession(map[string]any{"task_id": id, "include_result": true}, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var last resumeStatus
	if err := json.Unmarshal([]byte(raw), &last); err != nil {
		t.Fatal(err)
	}
	return last
}

func pollDelegateStatus(t *testing.T, manager *tool.DelegateManager, id, sessionID, want string) resumeStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last resumeStatus
	for time.Now().Before(deadline) {
		last = readDelegateStatus(t, manager, id, sessionID)
		if last.Status == want {
			return last
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("status=%s want %s", last.Status, want)
	return last
}

type resumeStatus struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Result string `json:"result"`
	Error  string `json:"error"`
	Mode   string `json:"mode"`
}

func waitDelegateStatus(t *testing.T, manager *tool.DelegateManager, id, sessionID string) resumeStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last resumeStatus
	for time.Now().Before(deadline) {
		raw, err := manager.HandleStatusForSession(map[string]any{"task_id": id, "include_result": true}, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &last); err != nil {
			t.Fatal(err)
		}
		switch last.Status {
		case "completed", "failed", "cancelled":
			return last
		}
		time.Sleep(time.Millisecond)
	}
	if last.Status == "running" || last.Status == "pending" {
		return last
	}
	t.Fatalf("delegate did not finish: %+v", last)
	return resumeStatus{}
}

func delegateTaskCount(t *testing.T, manager *tool.DelegateManager, sessionID string) int {
	t.Helper()
	raw, err := manager.HandleListForSession(map[string]any{}, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Tasks []struct {
			TaskID string `json:"task_id"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		t.Fatal(err)
	}
	return len(listed.Tasks)
}

func modelsOf(configs []LoopConfig) []string {
	out := make([]string, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, cfg.subagentModel)
	}
	return out
}
