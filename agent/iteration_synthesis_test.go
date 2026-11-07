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

type iterationSynthesisProvider struct {
	calls       int
	opts        []provider.CallOptions
	ignoreLimit bool
	emptyFinal  bool
}

func (p *iterationSynthesisProvider) Name() string    { return "iteration-synthesis-test" }
func (p *iterationSynthesisProvider) Validate() error { return nil }
func (p *iterationSynthesisProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return p.ChatWithOptions(ctx, messages, provider.CallOptions{})
}
func (p *iterationSynthesisProvider) ChatWithOptions(_ context.Context, messages []provider.Message, opts provider.CallOptions) (*provider.Response, error) {
	if !strings.Contains(fmt.Sprint(messages), "Read evidence and answer") {
		return &provider.Response{Content: verifiedAssessment}, nil
	}
	p.calls++
	p.opts = append(p.opts, opts)
	if p.calls == 4 && p.emptyFinal {
		return &provider.Response{}, nil
	}
	if p.calls == 4 && len(opts.Tools) == 0 && !p.ignoreLimit {
		return &provider.Response{Content: "Partial answer from collected evidence"}, nil
	}
	return &provider.Response{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("call-%d", p.calls), Name: "read_evidence", Arguments: fmt.Sprintf(`{"step":%d}`, p.calls)}}}, nil
}
func (p *iterationSynthesisProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return p.ChatStreamWithOptions(ctx, messages, provider.CallOptions{})
}
func (p *iterationSynthesisProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	response, err := p.ChatWithOptions(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.StreamChunk, 3)
	if len(response.ToolCalls) > 0 {
		if p.ignoreLimit && p.calls == 4 {
			ch <- provider.StreamChunk{Content: "unverified claim"}
		}
		c := response.ToolCalls[0]
		ch <- provider.StreamChunk{ToolCallDeltas: []provider.StreamToolCallDelta{{Index: 0, ID: c.ID, Name: c.Name, Arguments: c.Arguments}}}
	} else {
		ch <- provider.StreamChunk{Content: response.Content}
	}
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func TestIterationLimitSynthesizesToolResults(t *testing.T) {
	for _, mode := range []string{"sync", "simulated", "native"} {
		for _, scenario := range []struct {
			name   string
			ignore bool
			empty  bool
			limit  int
		}{{"answer", false, false, 4}, {"ignored", true, false, 4}, {"empty", false, true, 4}, {"no-results", false, false, 1}} {
			t.Run(fmt.Sprintf("%s/%s", mode, scenario.name), func(t *testing.T) {
				root := t.TempDir()
				cfg, err := config.NewManagerWithDir(root)
				if err != nil {
					t.Fatal(err)
				}
				if mode != "sync" {
					if err := cfg.Set("stream_mode", mode); err != nil {
						t.Fatal(err)
					}
				}
				sessionDir := filepath.Join(root, "sessions")
				// The session manager has no exported index close operation.
				if err := os.MkdirAll(filepath.Join(sessionDir, "index.db"), 0700); err != nil {
					t.Fatal(err)
				}
				sessions, err := session.NewManager(sessionDir)
				if err != nil {
					t.Fatal(err)
				}
				registry := tool.NewRegistry()
				executed := 0
				registry.Register(&tool.Tool{Name: "read_evidence", Enabled: true, Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) { executed++; return "evidence", nil }})
				p := &iterationSynthesisProvider{ignoreLimit: scenario.ignore, emptyFinal: scenario.empty}
				a, err := NewWithDependencies(Options{TakeOwnership: true}, &Dependencies{Config: cfg, Sessions: sessions, Provider: bootstrap.ProviderRuntime{Provider: p}, Support: bootstrap.SupportRuntime{Tools: registry}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = a.Close() })
				sess := sessions.New()
				loopCfg := DefaultLoopConfig()
				loopCfg.MaxIterations, loopCfg.Timeout, loopCfg.AutoApprove = scenario.limit, 10*time.Second, true
				loopCfg.Ephemeral = true
				loopCfg.RepeatToolCallLimit, loopCfg.ToolOnlyIterationLimit = 10, 10
				var result, streamed string
				var runErr error
				if mode == "sync" {
					r, err := a.RunLoopWithSession(context.Background(), sess, "Read evidence and answer", loopCfg)
					runErr = err
					result = r.Response
				} else {
					events, err := a.ChatWithSessionStreamInputWithLoopConfig(context.Background(), sess.ID, TextUserTurnInput("Read evidence and answer"), loopCfg)
					if err != nil {
						t.Fatal(err)
					}
					for event := range events {
						if event.Type == ChatEventContent {
							streamed += event.Content
						}
						if event.Type == ChatEventError {
							runErr = event.Err
						}
						if event.Type == ChatEventDone {
							result = event.Content
						}
					}
				}
				if scenario.limit == 1 {
					if runErr == nil || !strings.Contains(runErr.Error(), "max iterations") || p.calls != 1 || executed != 1 {
						t.Fatalf("err=%v calls=%d executed=%d", runErr, p.calls, executed)
					}
					return
				}
				if runErr != nil {
					t.Fatal(runErr)
				}
				if scenario.ignore || scenario.empty {
					if !strings.Contains(result, "incomplete") || !strings.Contains(result, "evidence") {
						t.Fatalf("fallback result=%q", result)
					}
					if mode != "sync" && (strings.Contains(streamed, "unverified claim") || !strings.Contains(streamed, "incomplete")) {
						t.Fatalf("streamed=%q", streamed)
					}
				} else if result != "Partial answer from collected evidence" {
					t.Fatalf("result=%q", result)
				}
				if p.calls != 4 || executed != 3 {
					t.Fatalf("result=%q calls=%d executed=%d", result, p.calls, executed)
				}
				if len(p.opts[3].Tools) != 0 {
					t.Fatalf("final options: %#v", p.opts[3])
				}
			})
		}
	}
}
