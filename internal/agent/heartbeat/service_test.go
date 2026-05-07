package heartbeat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yurika0211/luckyharness/internal/provider"
)

type mockFCProvider struct {
	resp *provider.Response
	err  error
}

func (m *mockFCProvider) Name() string { return "mock" }
func (m *mockFCProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return m.resp, m.err
}
func (m *mockFCProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, nil
}
func (m *mockFCProvider) Validate() error { return nil }
func (m *mockFCProvider) ChatWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (*provider.Response, error) {
	return m.resp, m.err
}
func (m *mockFCProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

func TestTriggerNowRunsAndNotifies(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "HEARTBEAT.md"), []byte("check invoices"), 0o600); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"action": "run",
		"tasks":  "Check invoices and summarize issues.",
	})
	prov := &mockFCProvider{
		resp: &provider.Response{
			ToolCalls: []provider.ToolCall{{
				ID:        "call_1",
				Name:      "heartbeat",
				Arguments: string(args),
			}},
		},
	}

	var executed string
	var notified string
	svc := New(Config{
		Workspace: workspace,
		Provider:  prov,
		Enabled:   true,
		OnExecute: func(ctx context.Context, tasks string) (string, error) {
			executed = tasks
			return "done", nil
		},
		OnNotify: func(ctx context.Context, response string) error {
			notified = response
			return nil
		},
	})

	got, err := svc.TriggerNow(context.Background())
	if err != nil {
		t.Fatalf("TriggerNow() error = %v", err)
	}
	if got != "done" {
		t.Fatalf("TriggerNow() = %q, want done", got)
	}
	if executed != "Check invoices and summarize issues." {
		t.Fatalf("executed tasks = %q", executed)
	}
	if notified != "done" {
		t.Fatalf("notified = %q", notified)
	}
}

func TestTriggerNowSkipsOnMissingFile(t *testing.T) {
	svc := New(Config{
		Workspace: t.TempDir(),
		Provider:  &mockFCProvider{},
		Enabled:   true,
	})

	got, err := svc.TriggerNow(context.Background())
	if err != nil {
		t.Fatalf("TriggerNow() error = %v", err)
	}
	if got != "" {
		t.Fatalf("TriggerNow() = %q, want empty", got)
	}
}
