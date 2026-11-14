package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/bootstrap"
	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

type subagentLimitProvider struct {
	path string
}

func (p *subagentLimitProvider) Name() string    { return "subagent-limit-test" }
func (p *subagentLimitProvider) Validate() error { return nil }
func (p *subagentLimitProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return p.ChatWithOptions(ctx, messages, provider.CallOptions{})
}
func (p *subagentLimitProvider) ChatWithOptions(_ context.Context, messages []provider.Message, opts provider.CallOptions) (*provider.Response, error) {
	if len(messages) > 0 && messages[0].Role == "system" && strings.Contains(messages[0].Content, "Classify the user's request") {
		return &provider.Response{Content: `{"profile":"tool_task"}`}, nil
	}
	if strings.Contains(fmt.Sprint(messages), "Review whether") {
		return &provider.Response{Content: verifiedAssessment}, nil
	}
	return &provider.Response{ToolCalls: []provider.ToolCall{{
		ID:        "call-read",
		Name:      "file_read",
		Arguments: fmt.Sprintf(`{"path":%q}`, p.path),
	}}}, nil
}
func (p *subagentLimitProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return p.ChatStreamWithOptions(ctx, messages, provider.CallOptions{})
}
func (p *subagentLimitProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	response, err := p.ChatWithOptions(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.StreamChunk, 3)
	if len(response.ToolCalls) > 0 {
		c := response.ToolCalls[0]
		ch <- provider.StreamChunk{ToolCallDeltas: []provider.StreamToolCallDelta{{Index: 0, ID: c.ID, Name: c.Name, Arguments: c.Arguments}}}
	} else {
		ch <- provider.StreamChunk{Content: response.Content}
	}
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func TestDelegatedChildLoopRewritesMaxIterations(t *testing.T) {
	root := t.TempDir()
	note := filepath.Join(root, "agent-loop.go")
	if err := os.WriteFile(note, []byte("package agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.NewManagerWithDir(filepath.Join(root, "home"))
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := session.NewManager(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:       "file_read",
		Enabled:    true,
		Permission: tool.PermAuto,
		Handler: func(args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	})
	p := &subagentLimitProvider{path: note}
	a, err := NewWithDependencies(Options{TakeOwnership: true}, &Dependencies{
		Config:   cfg,
		Sessions: sessions,
		Provider: bootstrap.ProviderRuntime{Provider: p},
		Support:  bootstrap.SupportRuntime{Tools: registry},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	loopCfg := DefaultLoopConfig()
	loopCfg.Source = "subagent"
	loopCfg.MaxIterations = 1
	loopCfg.Timeout = 10 * time.Second
	loopCfg.AutoApprove = true
	loopCfg.Ephemeral = true
	loopCfg.RepeatToolCallLimit = 10
	loopCfg.ToolOnlyIterationLimit = 10
	loopCfg.RestrictTools = true
	loopCfg.AllowedTools = []string{"file_read"}
	sess := sessions.New()
	result, err := a.runDelegatedChild(context.Background(), a.loopRunner(), sess, "Read "+note, loopCfg)
	if err != nil {
		t.Fatalf("child rewrite err = %v result=%+v", err, result)
	}
	if result == nil || !strings.Contains(result.Response, "Partial result") || !strings.Contains(result.Response, "Unfinished") {
		t.Fatalf("child result = %+v", result)
	}
	if !strings.Contains(result.Response, note) && !strings.Contains(result.Response, "file_read") {
		t.Fatalf("child Paths missing evidence: %s", result.Response)
	}

	parentCfg := loopCfg
	parentCfg.Source = "user"
	parentSess := sessions.New()
	parent, err := a.runDelegatedChild(context.Background(), a.loopRunner(), parentSess, "Read "+note, parentCfg)
	if err == nil || !strings.Contains(err.Error(), "max iterations") {
		t.Fatalf("parent rewrite err = %v result=%+v", err, parent)
	}
	if parent != nil && strings.Contains(parent.Response, "Partial result") {
		t.Fatalf("parent max-iterations was rewritten: %+v", parent)
	}
}
