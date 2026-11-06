package agent

import (
	"context"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/turn"
)

func TestRequestProfileGateEndToEndSelectsLightweightChat(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	cfg.Foreground = false
	if err := a.cfg.Set("agent.foreground.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: "你好"}, nil
	}}
	result, err := a.RunLoopWithSession(context.Background(), sess, "你好", cfg)
	if err != nil {
		t.Fatalf("RunLoopWithSession() error = %v", err)
	}
	if result.Profile != RequestProfileChat || result.ProfileSource != "fallback_low_risk" {
		t.Fatalf("unexpected profile result: %+v", result)
	}
	if result.ProfileConfidence <= 0 {
		t.Fatalf("unexpected profile confidence or budget: %+v", result)
	}
}

func TestRequestProfileGateEndToEndSelectsCodeTask(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	cfg.Foreground = false
	if err := a.cfg.Set("agent.foreground.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: "已完成"}, nil
	}}
	result, err := a.RunLoopWithSession(context.Background(), sess, "运行测试并修复失败", cfg)
	if err != nil {
		t.Fatalf("RunLoopWithSession() error = %v", err)
	}
	if result.Profile != RequestProfileCodeTask || result.ProfileSource != "deterministic_code_intent" {
		t.Fatalf("unexpected profile result: %+v", result)
	}
}

func TestRequestProfileBudgets(t *testing.T) {
	tests := []struct {
		name     string
		decision RequestProfileDecision
		wantIter int
		wantTime time.Duration
	}{
		{"chat", RequestProfileDecision{Profile: RequestProfileChat, Source: "fallback_low_risk", Confidence: 0.55}, 6, 45 * time.Second},
		{"empty", RequestProfileDecision{Profile: RequestProfileChat, Source: "empty_input", Confidence: 1}, 6, 45 * time.Second},
		{"tool_task", RequestProfileDecision{Profile: RequestProfileToolTask, Source: "deterministic_tool_intent", Confidence: 0.8}, 10, 90 * time.Second},
		{"code_task", RequestProfileDecision{Profile: RequestProfileCodeTask, Source: "deterministic_code_intent", Confidence: 0.85}, 20, 3 * time.Minute},
		{"long_task", RequestProfileDecision{Profile: RequestProfileLongTask, Source: "explicit_execution", Confidence: 1}, maxAllowedIterations, 10 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := LoopConfig{MaxIterations: 300, Timeout: 30 * time.Minute}
			applyRequestProfileDecision(&cfg, tt.decision)
			if cfg.MaxIterations != tt.wantIter || cfg.Timeout != tt.wantTime {
				t.Fatalf("%s budget = %d %s, want %d %s", tt.name, cfg.MaxIterations, cfg.Timeout, tt.wantIter, tt.wantTime)
			}
		})
	}
}

func TestRequestProfileKeepsConfiguredBudget(t *testing.T) {
	cfg := LoopConfig{
		MaxIterations:           80,
		Timeout:                 5 * time.Minute,
		maxIterationsFromConfig: true,
		timeoutFromConfig:       true,
	}
	applyRequestProfileDecision(&cfg, RequestProfileDecision{Profile: RequestProfileChat, Source: "fallback_low_risk", Confidence: 0.55})
	if cfg.MaxIterations != 80 || cfg.Timeout != 5*time.Minute {
		t.Fatalf("configured chat budget = %d %s, want 80 5m", cfg.MaxIterations, cfg.Timeout)
	}

	over := LoopConfig{
		MaxIterations:           400,
		Timeout:                 30 * time.Minute,
		maxIterationsFromConfig: true,
		timeoutFromConfig:       true,
	}
	applyRequestProfileDecision(&over, RequestProfileDecision{Profile: RequestProfileChat, Source: "fallback_low_risk"})
	sanitizeLoopConfig(&over)
	if over.MaxIterations != maxAllowedIterations || over.Timeout != 10*time.Minute {
		t.Fatalf("hard cap = %d %s, want %d 10m", over.MaxIterations, over.Timeout, maxAllowedIterations)
	}
}

func TestContinuationInheritsLastProfile(t *testing.T) {
	cfg := LoopConfig{inheritProfile: RequestProfileToolTask}
	got := classifyRequestProfile("继续", cfg)
	if got.Profile != RequestProfileToolTask || got.Source != "session_continue" {
		t.Fatalf("继续 = %+v", got)
	}
	for _, input := range []string{"接着", "接着做", "continue", "go on", "keep going", "Keep Doing!", "继续。"} {
		got := classifyRequestProfile(input, cfg)
		if got.Profile != RequestProfileToolTask || got.Source != "session_continue" {
			t.Fatalf("%q = %+v", input, got)
		}
	}
	code := classifyRequestProfile("继续修编译错误", cfg)
	if code.Profile != RequestProfileCodeTask || code.Source != "deterministic_code_intent" {
		t.Fatalf("继续修编译错误 = %+v", code)
	}
	chat := classifyRequestProfile("你好", cfg)
	if chat.Profile != RequestProfileChat || chat.Source != "fallback_low_risk" {
		t.Fatalf("你好 = %+v", chat)
	}
	missing := classifyRequestProfile("继续", LoopConfig{})
	if missing.Profile != RequestProfileChat || missing.Source != "fallback_low_risk" {
		t.Fatalf("继续 without last profile = %+v", missing)
	}
	longTask := classifyRequestProfile("继续", LoopConfig{inheritProfile: RequestProfileLongTask})
	if longTask.Profile != RequestProfileCodeTask || longTask.Source != "session_continue" {
		t.Fatalf("long_task continue = %+v", longTask)
	}
	explicit := classifyRequestProfile("继续", LoopConfig{inheritProfile: RequestProfileLongTask, Foreground: true})
	if explicit.Profile != RequestProfileLongTask || explicit.Source != "explicit_foreground" {
		t.Fatalf("foreground continue = %+v", explicit)
	}
}

func TestAgentRemembersSessionProfileForContinuation(t *testing.T) {
	a, _, _ := foregroundHarness(t)
	a.rememberRequestProfile("sess-1", RequestProfileCodeTask)
	cfg := LoopConfig{SessionID: "sess-1", inheritProfile: a.lastRequestProfile("sess-1")}
	got := classifyRequestProfile("继续", cfg)
	if got.Profile != RequestProfileCodeTask || got.Source != "session_continue" {
		t.Fatalf("remembered continue = %+v", got)
	}
	if a.lastRequestProfile("missing") != "" {
		t.Fatal("missing session should have no profile")
	}
}

func TestLongTaskRequiresExplicitExecutionOrForeground(t *testing.T) {
	for _, input := range []string{"", "你好", "请做一个持续很久的后台任务"} {
		decision := classifyRequestProfile(input, LoopConfig{})
		if decision.Profile == RequestProfileLongTask {
			t.Fatalf("input %q selected long_task without Execution or Foreground: %+v", input, decision)
		}
	}
	if got := classifyRequestProfile("你好", LoopConfig{Foreground: true}); got.Profile != RequestProfileLongTask || got.Source != "explicit_foreground" {
		t.Fatalf("foreground = %+v", got)
	}
	if got := classifyRequestProfile("你好", LoopConfig{Execution: markerDurable{}}); got.Profile != RequestProfileLongTask || got.Source != "explicit_execution" {
		t.Fatalf("execution = %+v", got)
	}
}

func TestModelLongTaskWithoutExplicitCallerBecomesCodeTask(t *testing.T) {
	a, _, cfg := foregroundHarness(t)
	cfg.Foreground = false
	cfg.Execution = nil
	loaded := a.cfg.Get()
	loaded.ModelRouter.Enable = true
	a.cfg.SetConfig(loaded)
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: `{"profile":"long_task"}`}, nil
	}}
	decision := a.classifyRequestProfileWithModel(context.Background(), "请持续后台处理", cfg, providerSnapshot{provider: a.provider})
	if decision.Profile == RequestProfileLongTask {
		t.Fatalf("model selected long_task without Execution or Foreground: %+v", decision)
	}
	if decision.Profile != RequestProfileCodeTask || decision.Source != "model_intent" {
		t.Fatalf("model long_task = %+v, want code_task from model_intent", decision)
	}
}

type markerDurable struct{}

func (markerDurable) Snapshot() (*turn.DurableTask, error) { return nil, nil }
func (markerDurable) BindSession(string) error             { return nil }
func (markerDurable) SaveCheckpoint([]byte) error          { return nil }
func (markerDurable) BeginOperation(op turn.DurableOperation) (turn.DurableOperation, error) {
	return op, nil
}
func (markerDurable) FinishOperation(string, string, bool, []byte) error { return nil }
func (markerDurable) Finish(*turn.DurableResult, bool) error             { return nil }
