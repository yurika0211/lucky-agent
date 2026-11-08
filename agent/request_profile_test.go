package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
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
	if result.Profile != RequestProfileChat || result.ProfileSource != "explicit_chat" {
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
	a.provider = &foregroundProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if len(messages) > 0 && strings.Contains(messages[0].Content, "Classify the user's request") {
			return &provider.Response{Content: `{"profile":"code_task"}`}, nil
		}
		return &provider.Response{Content: "已完成"}, nil
	}}
	result, err := a.RunLoopWithSession(context.Background(), sess, "运行测试并修复失败", cfg)
	if err != nil {
		t.Fatalf("RunLoopWithSession() error = %v", err)
	}
	if result.Profile != RequestProfileCodeTask || result.ProfileSource != "model_intent" {
		t.Fatalf("unexpected profile result: %+v", result)
	}
}

func TestRequestProfileBudgets(t *testing.T) {
	defaults := config.DefaultConfig().Agent.RequestProfiles
	tests := []struct {
		name     string
		decision RequestProfileDecision
		wantIter int
		wantTime time.Duration
	}{
		{"chat", RequestProfileDecision{Profile: RequestProfileChat, Source: "fallback_low_risk", Confidence: 0.55}, defaults.Chat.MaxIterations, time.Duration(defaults.Chat.TimeoutSeconds) * time.Second},
		{"empty", RequestProfileDecision{Profile: RequestProfileChat, Source: "empty_input", Confidence: 1}, defaults.Chat.MaxIterations, time.Duration(defaults.Chat.TimeoutSeconds) * time.Second},
		{"tool_task", RequestProfileDecision{Profile: RequestProfileToolTask, Source: "deterministic_tool_intent", Confidence: 0.8}, defaults.ToolTask.MaxIterations, time.Duration(defaults.ToolTask.TimeoutSeconds) * time.Second},
		{"code_task", RequestProfileDecision{Profile: RequestProfileCodeTask, Source: "deterministic_code_intent", Confidence: 0.85}, defaults.CodeTask.MaxIterations, time.Duration(defaults.CodeTask.TimeoutSeconds) * time.Second},
		{"long_task", RequestProfileDecision{Profile: RequestProfileLongTask, Source: "explicit_execution", Confidence: 1}, maxAllowedIterations, 10 * time.Minute},
	}
	if defaults.Chat.MaxIterations != 6 || defaults.Chat.TimeoutSeconds != 45 ||
		defaults.ToolTask.MaxIterations != 10 || defaults.ToolTask.TimeoutSeconds != 90 ||
		defaults.CodeTask.MaxIterations != 20 || defaults.CodeTask.TimeoutSeconds != 180 {
		t.Fatalf("default request profiles drifted: %+v", defaults)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := LoopConfig{MaxIterations: 0, Timeout: 0}
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

func TestRequestProfileKeepsExplicitToolBudget(t *testing.T) {
	cfg := LoopConfig{MaxIterations: 4, Timeout: 10 * time.Second}
	applyRequestProfileDecision(&cfg, RequestProfileDecision{Profile: RequestProfileToolTask, Source: "deterministic_tool_intent", Confidence: 0.8})
	if cfg.MaxIterations != 4 || cfg.Timeout != 10*time.Second {
		t.Fatalf("explicit tool budget = %d %s, want 4 10s", cfg.MaxIterations, cfg.Timeout)
	}
}

func TestRequestProfileCustomChatBudget(t *testing.T) {
	cfg := LoopConfig{
		MaxIterations: 0,
		Timeout:       0,
		requestProfiles: config.RequestProfilesConfig{
			Chat: config.RequestProfileBudgetConfig{MaxIterations: 12, TimeoutSeconds: 70},
		},
	}
	applyRequestProfileDecision(&cfg, RequestProfileDecision{Profile: RequestProfileChat, Source: "explicit_chat", Confidence: 1})
	if cfg.MaxIterations != 12 || cfg.Timeout != 70*time.Second {
		t.Fatalf("custom chat budget = %d %s, want 12 70s", cfg.MaxIterations, cfg.Timeout)
	}
}

func TestRequestProfileClosingPromptCopied(t *testing.T) {
	profiles := config.RequestProfilesConfig{
		Chat:     config.RequestProfileBudgetConfig{MaxIterations: 6, TimeoutSeconds: 45, ClosingPrompt: "wrap chat"},
		ToolTask: config.RequestProfileBudgetConfig{MaxIterations: 10, TimeoutSeconds: 90, ClosingPrompt: "wrap tool"},
		CodeTask: config.RequestProfileBudgetConfig{MaxIterations: 20, TimeoutSeconds: 180, ClosingPrompt: "wrap code"},
	}
	cases := []struct {
		profile RequestProfile
		want    string
	}{
		{RequestProfileChat, "wrap chat"},
		{RequestProfileToolTask, "wrap tool"},
		{RequestProfileCodeTask, "wrap code"},
		{RequestProfileLongTask, ""},
	}
	for _, tt := range cases {
		cfg := LoopConfig{MaxIterations: 300, Timeout: 30 * time.Minute, requestProfiles: profiles}
		applyRequestProfileDecision(&cfg, RequestProfileDecision{Profile: tt.profile, Source: "test", Confidence: 1})
		if cfg.requestProfileClosingPrompt != tt.want {
			t.Fatalf("%s closing prompt = %q, want %q", tt.profile, cfg.requestProfileClosingPrompt, tt.want)
		}
	}
	unset := LoopConfig{MaxIterations: 300, Timeout: 30 * time.Minute}
	applyRequestProfileDecision(&unset, RequestProfileDecision{Profile: RequestProfileChat, Source: "explicit_chat", Confidence: 1})
	if unset.requestProfileClosingPrompt != "" {
		t.Fatalf("unset profiles injected closing prompt %q", unset.requestProfileClosingPrompt)
	}
}

func TestAppendRequestProfileClosingMessage(t *testing.T) {
	base := []provider.Message{{Role: "system", Content: "standing"}, {Role: "user", Content: "hi"}}
	chat := appendRequestProfileClosingMessage(base, LoopConfig{requestProfileClosingPrompt: "wrap chat"})
	if len(chat) != 2 || chat[0].Role != "system" || !strings.Contains(chat[0].Content, "standing") || !strings.Contains(chat[0].Content, "wrap chat") || chat[1].Role != "user" {
		t.Fatalf("chat append = %+v", chat)
	}
	if base[0].Content != "standing" || len(base) != 2 {
		t.Fatalf("standing prompt changed: %+v", base)
	}
	noSystem := []provider.Message{{Role: "user", Content: "hi"}}
	added := appendRequestProfileClosingMessage(noSystem, LoopConfig{requestProfileClosingPrompt: "wrap chat"})
	if len(added) != 2 || added[1].Role != "system" || added[1].Content != "wrap chat" {
		t.Fatalf("missing system prompt = %+v", added)
	}
	empty := appendRequestProfileClosingMessage(base, LoopConfig{})
	if len(empty) != 2 || &empty[0] != &base[0] {
		t.Fatalf("empty prompt changed messages: %+v", empty)
	}
	longTask := appendRequestProfileClosingMessage(base, LoopConfig{Profile: RequestProfileLongTask})
	if len(longTask) != 2 {
		t.Fatalf("long_task injected: %+v", longTask)
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
	if chat.Profile != RequestProfileChat || chat.Source != "explicit_chat" {
		t.Fatalf("你好 = %+v", chat)
	}
	missing := classifyRequestProfile("继续", LoopConfig{})
	if missing.Profile != RequestProfileToolTask || missing.Source != "fallback_needs_tools" {
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

func TestUnknownProfileIsNotCheapestChat(t *testing.T) {
	paragraph := strings.Repeat("调试这段输出为什么和预期不一致。", 26)
	if len([]rune(paragraph)) < 400 {
		t.Fatalf("fixture too short: %d", len([]rune(paragraph)))
	}
	unknown := classifyRequestProfile(paragraph, LoopConfig{})
	if unknown.Profile != RequestProfileToolTask || unknown.Source != "fallback_needs_tools" {
		t.Fatalf("unknown = %+v", unknown)
	}
	inherited := classifyRequestProfile(paragraph, LoopConfig{inheritProfile: RequestProfileCodeTask})
	if inherited.Profile != RequestProfileCodeTask || inherited.Source != "session_unknown" {
		t.Fatalf("unknown inherit = %+v", inherited)
	}
	longUnknown := classifyRequestProfile(paragraph, LoopConfig{inheritProfile: RequestProfileLongTask})
	if longUnknown.Profile != RequestProfileCodeTask || longUnknown.Source != "session_unknown" {
		t.Fatalf("unknown long_task = %+v", longUnknown)
	}
	cont := classifyRequestProfile("继续吧", LoopConfig{inheritProfile: RequestProfileToolTask})
	if cont.Profile != RequestProfileToolTask || cont.Source != "session_continue" {
		t.Fatalf("继续吧 = %+v", cont)
	}
	for _, input := range []string{"那就继续", "please continue", "go on then"} {
		got := classifyRequestProfile(input, LoopConfig{inheritProfile: RequestProfileToolTask})
		if got.Profile != RequestProfileToolTask || got.Source != "session_continue" {
			t.Fatalf("%q = %+v", input, got)
		}
	}
	code := classifyRequestProfile("继续修编译错误", LoopConfig{inheritProfile: RequestProfileToolTask})
	if code.Profile != RequestProfileCodeTask || code.Source != "deterministic_code_intent" {
		t.Fatalf("继续修编译错误 = %+v", code)
	}
	hello := classifyRequestProfile("你好", LoopConfig{})
	if hello.Profile != RequestProfileChat || hello.Source != "explicit_chat" {
		t.Fatalf("你好 = %+v", hello)
	}
	embedded := classifyRequestProfile("你好，"+paragraph, LoopConfig{})
	if embedded.Profile != RequestProfileToolTask || embedded.Source != "fallback_needs_tools" {
		t.Fatalf("embedded 你好 = %+v", embedded)
	}
}

func TestChatContextShrinksOnlyForExplicitChat(t *testing.T) {
	explicit := contextBuildOptionsForProfileSource(RequestProfileChat, "explicit_chat")
	if explicit.HistoryRecent != 4 || explicit.MaxContextTokens != 24000 {
		t.Fatalf("explicit chat context = %+v", explicit)
	}
	empty := contextBuildOptionsForProfileSource(RequestProfileChat, "empty_input")
	if empty.HistoryRecent != 4 || empty.MaxContextTokens != 24000 {
		t.Fatalf("empty chat context = %+v", empty)
	}
	stale := contextBuildOptionsForProfileSource(RequestProfileChat, "fallback_low_risk")
	tool := contextBuildOptionsForProfileSource(RequestProfileToolTask, "fallback_needs_tools")
	if stale.HistoryRecent != tool.HistoryRecent || stale.MaxContextTokens != tool.MaxContextTokens {
		t.Fatalf("stale chat context = %+v, tool = %+v", stale, tool)
	}
	if stale.HistoryRecent == 4 || stale.MaxContextTokens == 24000 {
		t.Fatalf("stale chat still cheapest: %+v", stale)
	}
}

func TestSimpleTaskTuningKeepsConfiguredBudget(t *testing.T) {
	cfg := LoopConfig{
		MaxIterations:           80,
		Timeout:                 5 * time.Minute,
		RepeatToolCallLimit:     8,
		ToolOnlyIterationLimit:  8,
		maxIterationsFromConfig: true,
		timeoutFromConfig:       true,
	}
	applySimpleTaskLoopTuning(&cfg, "列出当前目录文件", config.AgentLoopConfig{})
	if cfg.MaxIterations != 80 || cfg.Timeout != 5*time.Minute {
		t.Fatalf("configured simple task = %d %s", cfg.MaxIterations, cfg.Timeout)
	}
	if cfg.RepeatToolCallLimit != 2 || cfg.ToolOnlyIterationLimit != 2 {
		t.Fatalf("repeat limits = %d %d", cfg.RepeatToolCallLimit, cfg.ToolOnlyIterationLimit)
	}
	unset := LoopConfig{MaxIterations: 80, Timeout: 5 * time.Minute, RepeatToolCallLimit: 8, ToolOnlyIterationLimit: 8}
	applySimpleTaskLoopTuning(&unset, "列出当前目录文件", config.AgentLoopConfig{})
	if unset.MaxIterations != 3 || unset.Timeout != 25*time.Second {
		t.Fatalf("unset simple task = %d %s", unset.MaxIterations, unset.Timeout)
	}
}

func TestForgetShortTermSessionDropsRetainedSessionState(t *testing.T) {
	const n = 32
	a := &Agent{}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("sess-%02d", i)
		a.rememberRequestProfile(ids[i], RequestProfileCodeTask)
		a.recordAutoCompactFailure(ids[i])
		a.RecordExternalReplyAnchor("telegram", "chat-"+ids[i], "msg-"+ids[i], ids[i], "job-"+ids[i])
	}
	sizes := []struct {
		name string
		size func() int
	}{
		{name: "requestProfiles", size: a.requestProfileCount},
		{name: "autoCompactFailures", size: a.autoCompactFailureCountMapSize},
		{name: "externalReplyAnchors", size: a.externalReplyAnchorCount},
	}
	for _, row := range sizes {
		if got := row.size(); got != n {
			t.Fatalf("%s before delete = %d, want %d", row.name, got, n)
		}
	}

	for _, id := range ids {
		a.ForgetShortTermSession(id)
	}
	for _, row := range sizes {
		if got := row.size(); got != 0 {
			t.Fatalf("%s after delete = %d, want 0", row.name, got)
		}
	}
	for _, id := range ids {
		if a.lastRequestProfile(id) != "" || a.autoCompactFailureCount(id) != 0 {
			t.Fatalf("session %s still retained", id)
		}
		if _, ok := a.ResolveExternalReplyAnchor("telegram", "chat-"+id, "msg-"+id); ok {
			t.Fatalf("reply anchor %s still retained", id)
		}
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

func TestModelIntentIgnoresRouterEnableAndFallbacks(t *testing.T) {
	a, _, cfg := foregroundHarness(t)
	cfg.Foreground = false
	cfg.Execution = nil
	loaded := a.cfg.Get()
	if loaded.ModelRouter.Enable {
		t.Fatal("harness must leave model_router.enable false")
	}
	loaded.Fallbacks = []config.FallbackEntry{{Model: "a"}, {Model: "b"}}
	a.cfg.SetConfig(loaded)
	calls := 0
	a.provider = &foregroundProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		calls++
		if len(messages) == 0 || !strings.Contains(messages[0].Content, "Classify the user's request") {
			t.Fatal("classify call missing intent prompt")
		}
		joined := messages[len(messages)-1].Content
		if !strings.Contains(joined, "last_profile=code_task") {
			t.Fatalf("user content = %q", joined)
		}
		return &provider.Response{Content: `{"profile":"tool_task"}`}, nil
	}}
	cfg.inheritProfile = RequestProfileCodeTask
	decision := a.classifyRequestProfileWithModel(context.Background(), "这段日志和预期不一致", cfg, providerSnapshot{provider: a.provider})
	if decision.Profile != RequestProfileToolTask || decision.Source != "model_intent" || calls != 1 {
		t.Fatalf("tool_task = %+v calls=%d", decision, calls)
	}
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: `{"profile":"code_task"}`}, nil
	}}
	code := a.classifyRequestProfileWithModel(context.Background(), "随便一句", cfg, providerSnapshot{provider: a.provider})
	if code.Profile != RequestProfileCodeTask || code.Source != "model_intent" {
		t.Fatalf("code_task = %+v", code)
	}
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: `{"profile":"chat"}`}, nil
	}}
	chat := a.classifyRequestProfileWithModel(context.Background(), "随便一句", cfg, providerSnapshot{provider: a.provider})
	if chat.Profile != RequestProfileChat || chat.Source != "model_intent" {
		t.Fatalf("chat = %+v", chat)
	}
}

func TestModelIntentFallsBackToKeywords(t *testing.T) {
	a, _, cfg := foregroundHarness(t)
	cfg.Foreground = false
	for _, content := range []string{"", "not json", "   "} {
		reply := content
		a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
			if reply == "" {
				return nil, errors.New("down")
			}
			return &provider.Response{Content: reply}, nil
		}}
		got := a.classifyRequestProfileWithModel(context.Background(), "运行测试并修复失败", cfg, providerSnapshot{provider: a.provider})
		if got.Profile != RequestProfileCodeTask || got.Source != "deterministic_code_intent" {
			t.Fatalf("content %q = %+v", reply, got)
		}
	}
}

func TestModelIntentShortCircuitsWithoutChat(t *testing.T) {
	a, _, cfg := foregroundHarness(t)
	calls := 0
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		calls++
		return &provider.Response{Content: `{"profile":"tool_task"}`}, nil
	}}
	snap := providerSnapshot{provider: a.provider}
	execCfg := cfg
	execCfg.Execution = markerDurable{}
	execCfg.Foreground = false
	got := a.classifyRequestProfileWithModel(context.Background(), "你好", execCfg, snap)
	if got.Profile != RequestProfileLongTask || got.Source != "explicit_execution" || calls != 0 {
		t.Fatalf("execution = %+v calls=%d", got, calls)
	}
	fg := cfg
	fg.Foreground = true
	got = a.classifyRequestProfileWithModel(context.Background(), "你好", fg, snap)
	if got.Profile != RequestProfileLongTask || got.Source != "explicit_foreground" || calls != 0 {
		t.Fatalf("foreground = %+v calls=%d", got, calls)
	}
	cont := cfg
	cont.Foreground = false
	cont.inheritProfile = RequestProfileCodeTask
	got = a.classifyRequestProfileWithModel(context.Background(), "继续", cont, snap)
	if got.Profile != RequestProfileCodeTask || got.Source != "session_continue" || calls != 0 {
		t.Fatalf("continue = %+v calls=%d", got, calls)
	}
	cfg.Foreground = false
	cfg.Execution = nil
	got = a.classifyRequestProfileWithModel(context.Background(), "你好", cfg, snap)
	if got.Profile != RequestProfileChat || got.Source != "explicit_chat" || calls != 0 {
		t.Fatalf("hello = %+v calls=%d", got, calls)
	}
}

func TestModelLongTaskWithoutExplicitCallerBecomesCodeTask(t *testing.T) {
	a, _, cfg := foregroundHarness(t)
	cfg.Foreground = false
	cfg.Execution = nil
	loaded := a.cfg.Get()
	if loaded.ModelRouter.Enable {
		t.Fatal("model_router.enable must stay false")
	}
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
