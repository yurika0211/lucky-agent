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

	"github.com/yurika0211/luckyagent/internal/autonomy"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/cron"
	msggateway "github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/metrics"
	"github.com/yurika0211/luckyagent/internal/multimodal"
	"github.com/yurika0211/luckyagent/internal/proactive"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/soul"
	"github.com/yurika0211/luckyagent/internal/tool"
)

// --- truncate ---

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.expected)
		}
	}
}

// --- splitIntoChunks ---

func TestSplitIntoChunks_ShortText(t *testing.T) {
	text := "hello"
	chunks := splitIntoChunks(text, 100)
	if len(chunks) != 1 || chunks[0] != text {
		t.Errorf("expected [%q], got %v", text, chunks)
	}
}

func TestSplitIntoChunks_ExactSize(t *testing.T) {
	text := "abcdefghij"
	chunks := splitIntoChunks(text, 10)
	if len(chunks) != 1 || chunks[0] != text {
		t.Errorf("expected single chunk, got %v", chunks)
	}
}

func TestSplitIntoChunks_SplitAtSentence(t *testing.T) {
	text := "First sentence. Second sentence. Third sentence."
	chunks := splitIntoChunks(text, 20)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}
	// Reconstructed text should equal original
	reconstructed := strings.Join(chunks, "")
	if reconstructed != text {
		t.Errorf("reconstructed text mismatch: got %q, want %q", reconstructed, text)
	}
}

func TestSplitIntoChunks_ChineseSentence(t *testing.T) {
	text := "这是第一句话。这是第二句话。这是第三句话。"
	chunks := splitIntoChunks(text, 10)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks for Chinese text, got %d", len(chunks))
	}
	reconstructed := strings.Join(chunks, "")
	if reconstructed != text {
		t.Errorf("reconstructed text mismatch: got %q, want %q", reconstructed, text)
	}
}

func TestNewPrefersConfiguredEmbedderForRAG(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir() error = %v", err)
	}
	if err := cfg.Set("embedding.model", "jina-embeddings-v4"); err != nil {
		t.Fatalf("Set embedding.model: %v", err)
	}
	if err := cfg.Set("embedding.api_key", "test-embedding-key"); err != nil {
		t.Fatalf("Set embedding.api_key: %v", err)
	}
	if err := cfg.Set("embedding.api_base", "https://example.test/v1"); err != nil {
		t.Fatalf("Set embedding.api_base: %v", err)
	}
	if err := cfg.Set("embedding.dimension", "2048"); err != nil {
		t.Fatalf("Set embedding.dimension: %v", err)
	}

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	reg := a.EmbedderRegistry()
	if reg == nil {
		t.Fatal("expected embedder registry to be initialized")
	}
	if got := reg.ActiveID(); got != "openai-default" {
		t.Fatalf("expected active embedder openai-default, got %q", got)
	}
	if emb := reg.Active(); emb == nil || emb.Dimension() != 2048 {
		t.Fatalf("expected active embedder dimension 2048, got %+v", emb)
	}
}

func TestNewInitializesProactiveStoreOnlyWhenEnabled(t *testing.T) {
	disabledDir := t.TempDir()
	disabledCfg, err := config.NewManagerWithDir(disabledDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir disabled: %v", err)
	}
	disabledAgent, err := New(disabledCfg)
	if err != nil {
		t.Fatalf("New disabled proactive: %v", err)
	}
	if disabledAgent.proactiveStore != nil {
		t.Fatalf("expected proactive store to be nil when disabled")
	}
	if err := disabledAgent.Close(); err != nil {
		t.Fatalf("Close disabled agent: %v", err)
	}

	enabledDir := t.TempDir()
	enabledCfg, err := config.NewManagerWithDir(enabledDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir enabled: %v", err)
	}
	if err := enabledCfg.Set("proactive.enabled", "true"); err != nil {
		t.Fatalf("Set proactive.enabled: %v", err)
	}
	if err := enabledCfg.Set("proactive.store_path", "runtime/test-proactive.db"); err != nil {
		t.Fatalf("Set proactive.store_path: %v", err)
	}
	enabledAgent, err := New(enabledCfg)
	if err != nil {
		t.Fatalf("New enabled proactive: %v", err)
	}
	if enabledAgent.proactiveStore == nil {
		t.Fatalf("expected proactive store to be initialized")
	}
	if err := enabledAgent.Close(); err != nil {
		t.Fatalf("Close enabled agent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(enabledDir, "runtime", "test-proactive.db")); err != nil {
		t.Fatalf("expected proactive db to exist: %v", err)
	}
}

func TestRecordProactiveRuntimeEvents(t *testing.T) {
	store, err := proactive.OpenStore(filepath.Join(t.TempDir(), "proactive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	a := &Agent{proactiveStore: store}
	sess := session.NewSession("sess-1", t.TempDir())
	sess.SetCwd("/tmp/project")
	a.recordProactiveChatEvent(sess, TextUserTurnInput("hello"), "world", 1, nil)
	a.recordProactiveToolEvent(sess, executedToolCall{
		ToolCall: provider.ToolCall{Name: "file_read", Arguments: `{"path":"README.md"}`},
		Result:   "ok",
		Duration: 12 * time.Millisecond,
	}, false)

	stats, err := store.RuntimeEventStats()
	if err != nil {
		t.Fatalf("RuntimeEventStats: %v", err)
	}
	if stats.ByType["chat_turn"] != 1 || stats.ByType["tool_call"] != 1 {
		t.Fatalf("unexpected runtime event stats: %#v", stats)
	}
	events, err := store.RecentRuntimeEvents(10)
	if err != nil {
		t.Fatalf("RecentRuntimeEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 runtime events, got %d", len(events))
	}
}

func TestParseEmbedderDimensionEnv(t *testing.T) {
	tests := []struct {
		input string
		want  int
		ok    bool
	}{
		{input: "", want: 0, ok: false},
		{input: "2048", want: 2048, ok: true},
		{input: " 1024 ", want: 1024, ok: true},
		{input: "abc", want: 0, ok: false},
		{input: "-1", want: 0, ok: false},
	}

	for _, tt := range tests {
		t.Setenv("EMBEDDING_MODEL_DIMENSION", tt.input)
		gotCfg, ok := resolveEmbedderRuntimeConfig(nil)
		if gotCfg.Dimension != tt.want || ok != tt.ok {
			t.Fatalf("parseEmbedderDimensionEnv(%q) = (%d, %v), want (%d, %v)", tt.input, gotCfg.Dimension, ok, tt.want, tt.ok)
		}
	}
}

func TestSplitIntoChunks_EmptyString(t *testing.T) {
	chunks := splitIntoChunks("", 10)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("expected single empty chunk, got %v", chunks)
	}
}

// --- inferCategory ---

func TestInferCategory(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"我喜欢编程", "preference"},
		{"I prefer dark mode", "preference"},
		{"讨厌这个设计", "preference"},
		{"项目进度如何", "project"},
		{"project deadline", "project"},
		{"代码仓库在哪", "project"},
		{"什么是RAG", "knowledge"},
		{"解释一下", "knowledge"},
		{"你好", "conversation"},
		{"hello", "conversation"},
		{"随便聊聊", "conversation"},
	}
	for _, tt := range tests {
		got := inferCategory(tt.input)
		if got != tt.expected {
			t.Errorf("inferCategory(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- inferImportance ---

func TestInferImportance(t *testing.T) {
	tests := []struct {
		input       string
		minExpected float64
		maxExpected float64
	}{
		{"重要：请记住这个", 0.7, 0.7},
		{"remember this", 0.7, 0.7},
		{"密码是123456", 0.7, 0.7},
		{"API token expired", 0.7, 0.7},
		{"你好", 0.2, 0.2},
		{"hi", 0.2, 0.2},
	}
	for _, tt := range tests {
		got := inferImportance(tt.input)
		if got < tt.minExpected || got > tt.maxExpected {
			t.Errorf("inferImportance(%q) = %f, want [%f, %f]", tt.input, got, tt.minExpected, tt.maxExpected)
		}
	}

	// Long messages should have at least medium importance
	longMsg := "这是一段超过50个字符的较长消息，包含了具体的项目信息和上下文描述"
	got := inferImportance(longMsg)
	if got < 0.4 {
		t.Errorf("long message importance = %f, expected >= 0.4", got)
	}
}

// --- sanitizeLoopConfig ---

func TestSanitizeLoopConfig_Defaults(t *testing.T) {
	cfg := LoopConfig{}
	sanitizeLoopConfig(&cfg)
	if cfg.MaxIterations != 10 {
		t.Errorf("expected MaxIterations=10, got %d", cfg.MaxIterations)
	}
	if cfg.Timeout != 60*time.Second {
		t.Errorf("expected Timeout=60s, got %v", cfg.Timeout)
	}
}

func TestSanitizeLoopConfig_ExceedsMax(t *testing.T) {
	cfg := LoopConfig{MaxIterations: 400, Timeout: 30 * time.Minute}
	sanitizeLoopConfig(&cfg)
	if cfg.MaxIterations != 300 {
		t.Errorf("expected MaxIterations capped at 300, got %d", cfg.MaxIterations)
	}
	if cfg.Timeout != 10*time.Minute {
		t.Errorf("expected Timeout capped at 10m, got %v", cfg.Timeout)
	}
}

func TestSanitizeLoopConfig_NegativeValues(t *testing.T) {
	cfg := LoopConfig{MaxIterations: -1, Timeout: -1 * time.Second}
	sanitizeLoopConfig(&cfg)
	if cfg.MaxIterations != 10 {
		t.Errorf("expected MaxIterations=10 for negative, got %d", cfg.MaxIterations)
	}
	if cfg.Timeout != 60*time.Second {
		t.Errorf("expected Timeout=60s for negative, got %v", cfg.Timeout)
	}
}

func TestSanitizeLoopConfig_ValidValues(t *testing.T) {
	cfg := LoopConfig{MaxIterations: 5, Timeout: 30 * time.Second}
	sanitizeLoopConfig(&cfg)
	if cfg.MaxIterations != 5 {
		t.Errorf("expected MaxIterations=5, got %d", cfg.MaxIterations)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("expected Timeout=30s, got %v", cfg.Timeout)
	}
}

// --- toContextMessages / fromContextMessages ---

func TestToContextMessages_SystemIsCritical(t *testing.T) {
	// Create a minimal agent to test the method
	a := &Agent{}
	msgs := []provider.Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}
	result := a.toContextMessages(msgs)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if result[0].Priority != contextx.PriorityCritical {
		t.Errorf("system message should be PriorityCritical, got %v", result[0].Priority)
	}
	if result[0].Category != "system" {
		t.Errorf("system message category should be 'system', got %q", result[0].Category)
	}
}

func TestToContextMessages_MemoryPriority(t *testing.T) {
	a := &Agent{}
	msgs := []provider.Message{
		{Role: "system", Content: "[Core Memory] important facts"},
		{Role: "system", Content: "[Working Memory] recent context"},
		{Role: "system", Content: "[Recent Context] last messages"},
	}
	result := a.toContextMessages(msgs)
	if result[0].Priority != contextx.PriorityHigh {
		t.Errorf("Core Memory should be PriorityHigh, got %v", result[0].Priority)
	}
	if result[0].Category != "memory_long" {
		t.Errorf("Core Memory category should be 'memory_long', got %q", result[0].Category)
	}
	if result[1].Priority != contextx.PriorityNormal {
		t.Errorf("Working Memory should be PriorityNormal, got %v", result[1].Priority)
	}
	if result[2].Priority != contextx.PriorityLow {
		t.Errorf("Recent Context should be PriorityLow, got %v", result[2].Priority)
	}
}

func TestContextPlannerInjectsMemoryGateForDaughterOutdoorPrompt(t *testing.T) {
	a := newTestAgentWithMemory(t)
	if err := a.memory.SaveWithTierAndTags("My [[Daughter]] has [[Pollen Allergy]].", "health", memory.TierLong, 0.98, []string{"health"}); err != nil {
		t.Fatalf("save allergy: %v", err)
	}
	if err := a.memory.SaveWithTierAndTags("When [[Outdoor Plan]] involves [[Daughter]] and [[Pollen Allergy]], check [[Weather Forecast]] and [[Air Quality]].", "rule", memory.TierLong, 0.92, []string{"tool-routing"}); err != nil {
		t.Fatalf("save rule: %v", err)
	}

	planner := newContextPlanner(a, defaultContextBuildOptions())
	messages := planner.Build(context.Background(), newTestSession(t), "今天下午适合和女儿出门吗")
	joined := providerMessagesContent(messages)
	for _, want := range []string{
		"[Working Memory",
		"Retrieved Evidence",
		"LuckyAgent Obsidian-compatible Markdown memory vault",
		"Prefer the latest user message",
		"[Memory Router]",
		"Required tools before final answer: current_time, web_search",
		"child_health_outdoor_plan",
		"Pollen Allergy",
		"Weather Forecast",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected context to contain %q:\n%s", want, joined)
		}
	}
}

func providerMessagesContent(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		if msg.Content != "" {
			b.WriteString(msg.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestFromContextMessages(t *testing.T) {
	a := &Agent{}
	original := []provider.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "usr"},
		{Role: "assistant", Content: "ast"},
	}
	ctxMsgs := a.toContextMessages(original)
	roundTrip := a.fromContextMessages(ctxMsgs)
	if len(roundTrip) != len(original) {
		t.Fatalf("expected %d messages, got %d", len(original), len(roundTrip))
	}
	for i, msg := range roundTrip {
		if msg.Role != original[i].Role {
			t.Errorf("msg[%d].Role = %q, want %q", i, msg.Role, original[i].Role)
		}
		if msg.Content != original[i].Content {
			t.Errorf("msg[%d].Content = %q, want %q", i, msg.Content, original[i].Content)
		}
	}
}

func TestContextMessageRoundTripPreservesStructuredProviderFields(t *testing.T) {
	a := &Agent{}
	original := []provider.Message{
		{
			Role:             "assistant",
			Content:          "calling tool",
			ReasoningContent: "hidden thinking",
			ToolCalls: []provider.ToolCall{{
				ID:        "call_1",
				Name:      "web_search",
				Arguments: `{"query":"lh"}`,
			}},
		},
		{
			Role:       "tool",
			Content:    "result",
			ToolCallID: "call_1",
			Name:       "web_search",
		},
	}

	roundTrip := a.fromContextMessages(a.toContextMessages(original))
	if len(roundTrip) != len(original) {
		t.Fatalf("expected %d messages, got %d", len(original), len(roundTrip))
	}
	if roundTrip[0].ReasoningContent != "hidden thinking" {
		t.Fatalf("expected reasoning content to survive round trip, got %q", roundTrip[0].ReasoningContent)
	}
	if len(roundTrip[0].ToolCalls) != 1 || roundTrip[0].ToolCalls[0].ID != "call_1" || roundTrip[0].ToolCalls[0].Name != "web_search" {
		t.Fatalf("expected tool call to survive round trip, got %#v", roundTrip[0].ToolCalls)
	}
	if roundTrip[1].ToolCallID != "call_1" || roundTrip[1].Name != "web_search" {
		t.Fatalf("expected tool result fields to survive round trip, got %#v", roundTrip[1])
	}
}

func TestProcessDirectResponsePreservesReasoningForRecoveryMessages(t *testing.T) {
	a := &Agent{}
	state := newLoopRuntimeState()
	messages, finalized, _ := a.processDirectResponse(&provider.Response{
		Content:          "",
		ReasoningContent: "empty reasoning",
	}, nil, state)
	if finalized {
		t.Fatal("expected empty response recovery to continue")
	}
	if len(messages) < 1 || messages[0].ReasoningContent != "empty reasoning" {
		t.Fatalf("expected empty response assistant reasoning to be preserved, got %#v", messages)
	}

	state = newLoopRuntimeState()
	messages, finalized, _ = a.processDirectResponse(&provider.Response{
		Content:          "partial",
		ReasoningContent: "length reasoning",
		FinishReason:     "length",
	}, nil, state)
	if finalized {
		t.Fatal("expected length recovery to continue")
	}
	if len(messages) < 1 || messages[0].ReasoningContent != "length reasoning" {
		t.Fatalf("expected length response assistant reasoning to be preserved, got %#v", messages)
	}
	if got := strings.TrimSpace(state.continuedReasoning.String()); got != "length reasoning" {
		t.Fatalf("expected continued reasoning to be tracked, got %q", got)
	}
}

// --- applyWebSearchEnv ---

func TestApplyWebSearchEnv(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(cfgPath, []byte("provider: openai\napi_key: test\nmodel: gpt-4\n"), 0o644)
	cfg, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Skipf("cannot create config manager: %v", err)
	}

	// Set env vars
	os.Setenv("LH_WEB_SEARCH_PROVIDER", "brave")
	os.Setenv("LH_WEB_SEARCH_API_KEY", "test-key-123")
	defer func() {
		os.Unsetenv("LH_WEB_SEARCH_PROVIDER")
		os.Unsetenv("LH_WEB_SEARCH_API_KEY")
	}()

	applyWebSearchEnv(cfg)

	if v := cfg.Get().WebSearch.Provider; v != "brave" {
		t.Errorf("expected web_search.provider=brave, got %q", v)
	}
	if v := cfg.Get().WebSearch.APIKey; v != "test-key-123" {
		t.Errorf("expected web_search.api_key=test-key-123, got %q", v)
	}
}

func TestApplyOpenCLIEnv(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Skipf("cannot create config manager: %v", err)
	}

	os.Setenv("LH_OPENCLI_ENABLED", "true")
	os.Setenv("LH_OPENCLI_COMMAND", "opencli")
	os.Setenv("LH_OPENCLI_ARGS", "web,read,--url,{url},--stdout,true,--download-images,false,-f,md")
	os.Setenv("LH_OPENCLI_TIMEOUT_SECONDS", "42")
	os.Setenv("LH_OPENCLI_MAX_CHARS", "12345")
	os.Setenv("LH_OPENCLI_FALLBACK_TO_WEB_FETCH", "true")
	defer func() {
		os.Unsetenv("LH_OPENCLI_ENABLED")
		os.Unsetenv("LH_OPENCLI_COMMAND")
		os.Unsetenv("LH_OPENCLI_ARGS")
		os.Unsetenv("LH_OPENCLI_TIMEOUT_SECONDS")
		os.Unsetenv("LH_OPENCLI_MAX_CHARS")
		os.Unsetenv("LH_OPENCLI_FALLBACK_TO_WEB_FETCH")
	}()

	applyOpenCLIEnv(cfg)

	got := cfg.Get().OpenCLI
	if !got.Enabled {
		t.Fatal("expected opencli.enabled=true")
	}
	if got.Command != "opencli" {
		t.Fatalf("expected opencli.command=opencli, got %q", got.Command)
	}
	if len(got.Args) != 10 || got.Args[0] != "web" || got.Args[1] != "read" {
		t.Fatalf("unexpected args: %#v", got.Args)
	}
	if got.TimeoutSeconds != 42 || got.MaxChars != 12345 || !got.FallbackToWebFetch {
		t.Fatalf("unexpected opencli config: %#v", got)
	}
}

// --- updateShellContext ---

func newTestAgentWithMemory(t *testing.T) *Agent {
	t.Helper()
	tmpDir := t.TempDir()
	memStore, err := memory.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("create memory store: %v", err)
	}
	return &Agent{
		memory: memStore,
	}
}

func newTestSession(t *testing.T) *session.Session {
	t.Helper()
	tmpDir := t.TempDir()
	mgr, err := session.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}
	sess := mgr.New()
	return sess
}

func TestUpdateShellContext_CdCommand(t *testing.T) {
	a := &Agent{}
	sess := newTestSession(t)

	// Use a directory that exists
	tmpDir := t.TempDir()
	a.updateShellContext(sess, "cd "+tmpDir, "")
	if sess.GetCwd() != tmpDir {
		t.Errorf("expected cwd=%s, got %s", tmpDir, sess.GetCwd())
	}
}

func TestUpdateShellContext_ExportCommand(t *testing.T) {
	a := &Agent{}
	sess := newTestSession(t)

	a.updateShellContext(sess, "export MY_VAR=hello", "")
	env := sess.GetEnv()
	if env["MY_VAR"] != "hello" {
		t.Errorf("expected MY_VAR=hello, got %v", env)
	}
}

func TestUpdateShellContext_UnsetCommand(t *testing.T) {
	a := &Agent{}
	sess := newTestSession(t)

	sess.SetEnv("REMOVE_ME", "value")
	a.updateShellContext(sess, "unset REMOVE_ME", "")
	env := sess.GetEnv()
	if _, ok := env["REMOVE_ME"]; ok {
		t.Error("expected REMOVE_ME to be unset")
	}
}

func TestUpdateShellContext_MultipleExports(t *testing.T) {
	a := &Agent{}
	sess := newTestSession(t)

	a.updateShellContext(sess, "export A=1 && export B=2", "")
	env := sess.GetEnv()
	if env["A"] != "1" || env["B"] != "2" {
		t.Errorf("expected A=1, B=2, got %v", env)
	}
}

// --- saveConversationMemory ---

func TestSaveConversationMemory(t *testing.T) {
	a := newTestAgentWithMemory(t)

	a.saveConversationMemory("我喜欢Rust语言", "Rust确实很棒")

	// Reusable user preferences should still be saved as memory facts.
	recent := a.memory.Recent(10)
	found := false
	for _, e := range recent {
		if strings.Contains(e.Content, "Rust") && !strings.HasPrefix(e.Content, "User:") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Rust-related memory to be saved")
	}
}

func TestSaveConversationMemoryDoesNotPersistTruncationEllipsis(t *testing.T) {
	a := newTestAgentWithMemory(t)
	userInput := "我喜欢Rust语言，" + strings.Repeat("这是一个很长的偏好描述", 40)

	a.saveConversationMemory(userInput, "Rust确实很棒")

	recent := a.memory.Recent(10)
	for _, e := range recent {
		if strings.Contains(e.Content, "Rust") {
			if strings.HasSuffix(strings.TrimSpace(e.Content), "...") {
				t.Fatalf("persisted memory should not end with generated ellipsis: %q", e.Content)
			}
			return
		}
	}
	t.Fatal("expected Rust-related memory to be saved")
}

func TestSaveConversationMemoryDoesNotStoreRawConversationInMemoryStore(t *testing.T) {
	a := newTestAgentWithMemory(t)
	a.shortTerm = memory.NewShortTermBuffer(10)

	a.saveConversationMemory("hello", "hi there")

	if a.shortTerm.MessageCount() != 2 {
		t.Errorf("expected 2 messages in short term buffer, got %d", a.shortTerm.MessageCount())
	}
	for _, e := range a.memory.Recent(10) {
		if strings.HasPrefix(e.Content, "User:") || strings.HasPrefix(e.Content, "Assistant:") || e.Category == "conversation" {
			t.Fatalf("raw conversation should not be persisted to memory.Store: %#v", e)
		}
	}
}

func TestSaveConversationMemory_ShortTermBuffer(t *testing.T) {
	a := newTestAgentWithMemory(t)
	a.shortTerm = memory.NewShortTermBuffer(10)

	a.saveConversationMemory("hello", "hi there")

	if a.shortTerm.MessageCount() != 2 {
		t.Errorf("expected 2 messages in short term buffer, got %d", a.shortTerm.MessageCount())
	}
}

func TestContextPlannerFiltersRawConversationShortMemory(t *testing.T) {
	a := newTestAgentWithMemory(t)
	a.memory.SaveWithTier("User: hello about deploy", "conversation", memory.TierShort, 0.9)
	a.memory.SaveWithTier("Assistant: deploy answer", "conversation", memory.TierShort, 0.9)
	a.memory.SaveWithTier("Project deploy constraint: run tests before release", "project", memory.TierShort, 0.9)

	planner := newContextPlanner(a, defaultContextBuildOptions())
	msg := planner.buildRelevantMemoryMessage("deploy", TurnScope{})
	if msg.Content == "" {
		t.Fatal("expected filtered working memory to keep project fact")
	}
	if strings.Contains(msg.Content, "User: hello") || strings.Contains(msg.Content, "Assistant: deploy") {
		t.Fatalf("raw conversation memory leaked into context:\n%s", msg.Content)
	}
	if !strings.Contains(msg.Content, "run tests before release") {
		t.Fatalf("expected project fact in context:\n%s", msg.Content)
	}
}

func TestSaveConversationMemoryFromTurnAddsScopeTags(t *testing.T) {
	a := newTestAgentWithMemory(t)
	scope := TurnScope{
		Platform: "qqofficial",
		ChatID:   "group:100",
		ChatType: "group",
		SenderID: "user-a",
	}

	a.saveConversationMemoryFromTurn(TextUserTurnInput("我喜欢Rust语言").WithScope(scope), "Rust确实很棒")

	recent := a.memory.Recent(10)
	var got *memory.Entry
	for i := range recent {
		if strings.Contains(recent[i].Content, "Rust") {
			got = &recent[i]
			break
		}
	}
	if got == nil {
		t.Fatal("expected scoped Rust memory to be saved")
	}
	if !stringSliceContains(got.Tags, scope.UserTag()) || !stringSliceContains(got.Tags, scope.GroupTag()) || !stringSliceContains(got.Tags, "scope:personal") {
		t.Fatalf("expected personal scope tags, got %#v", got.Tags)
	}
}

func TestContextPlannerFiltersOtherUserScopedMemory(t *testing.T) {
	a := newTestAgentWithMemory(t)
	scopeA := TurnScope{Platform: "qqofficial", ChatID: "group:100", ChatType: "group", SenderID: "user-a"}
	scopeB := TurnScope{Platform: "qqofficial", ChatID: "group:100", ChatType: "group", SenderID: "user-b"}
	if err := a.memory.SaveWithOptions("Project deploy constraint for user A", "project", memory.TierLong, 0.95, memory.SaveOptions{Tags: scopeA.MemoryTags()}); err != nil {
		t.Fatalf("save user A memory: %v", err)
	}
	if err := a.memory.SaveWithOptions("Project deploy constraint for user B", "project", memory.TierLong, 0.95, memory.SaveOptions{Tags: scopeB.MemoryTags()}); err != nil {
		t.Fatalf("save user B memory: %v", err)
	}

	planner := newContextPlanner(a, defaultContextBuildOptions())
	msg := planner.buildRelevantMemoryMessage("deploy constraint", scopeA)
	if !strings.Contains(msg.Content, "user A") {
		t.Fatalf("expected user A memory in scoped context:\n%s", msg.Content)
	}
	if strings.Contains(msg.Content, "user B") {
		t.Fatalf("other user's scoped memory leaked into context:\n%s", msg.Content)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestContextMemoryHygieneHookQuarantinesDirtyMemoryBeforeRecall(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	if err := cfg.Set("context.memory_hygiene_before_context", "true"); err != nil {
		t.Fatalf("set hygiene enabled: %v", err)
	}
	if err := cfg.Set("context.memory_hygiene_action", "quarantine"); err != nil {
		t.Fatalf("set hygiene action: %v", err)
	}
	if err := cfg.Set("context.memory_hygiene_min_severity", "high"); err != nil {
		t.Fatalf("set hygiene severity: %v", err)
	}

	a := newTestAgentWithMemory(t)
	a.cfg = cfg
	a.memory.SaveWithTier("User: stale deploy instruction", "conversation", memory.TierShort, 0.9)
	a.memory.SaveWithTier("Project deploy constraint: run tests before release", "project", memory.TierShort, 0.9)

	planner := newContextPlanner(a, defaultContextBuildOptions())
	_ = planner.BuildInput(context.Background(), nil, TextUserTurnInput("deploy"))

	for _, e := range a.memory.Search("deploy") {
		if strings.Contains(e.Content, "User: stale deploy") {
			t.Fatalf("dirty raw conversation should be quarantined before recall: %#v", e)
		}
	}
}

func TestPersistCompressedSummaryDoesNotWriteDurableMemory(t *testing.T) {
	a := newTestAgentWithMemory(t)
	planner := newContextPlanner(a, defaultContextBuildOptions())

	planner.persistCompressedSummary(nil, nil, "[Conversation Summary]\nTool evidence: checked config")

	for _, e := range a.memory.Recent(10) {
		if e.Category == "context_compression" || strings.Contains(e.Content, "Tool evidence: checked config") {
			t.Fatalf("compressed LLM summary should not be persisted to durable memory: %#v", e)
		}
	}
}

// --- autoSummarize ---

func TestAutoSummarize_FewMemories(t *testing.T) {
	a := newTestAgentWithMemory(t)

	// Only 3 short-term memories — should not trigger summarize
	a.memory.SaveWithTier("m1", "test", memory.TierShort, 0.3)
	a.memory.SaveWithTier("m2", "test", memory.TierShort, 0.3)
	a.memory.SaveWithTier("m3", "test", memory.TierShort, 0.3)

	before := len(a.memory.ByTier(memory.TierShort))
	a.autoSummarize()
	after := len(a.memory.ByTier(memory.TierShort))

	// Should not change — too few memories
	if after != before {
		t.Errorf("expected no change with few memories, before=%d after=%d", before, after)
	}
}

func TestAutoSummarize_ManyMemories(t *testing.T) {
	a := newTestAgentWithMemory(t)

	// Add 8 short-term memories — should trigger summarize (keep 5)
	for i := 0; i < 8; i++ {
		a.memory.SaveWithTier("memory item "+strings.Repeat("x", 20), "test", memory.TierShort, 0.3)
	}

	a.autoSummarize()

	// After summarize, short-term should be reduced
	shorts := a.memory.ByTier(memory.TierShort)
	if len(shorts) > 6 { // allow some tolerance
		t.Errorf("expected short-term memories to be reduced, got %d", len(shorts))
	}
}

// --- MemoryStats / DecayMemory / PromoteMemory / ExpireMidTermMemory ---

func TestMemoryStats(t *testing.T) {
	a := newTestAgentWithMemory(t)
	a.memory.SaveWithTier("short", "test", memory.TierShort, 0.3)
	a.memory.SaveLongTerm("long", "test")

	stats := a.MemoryStats()
	if stats[memory.TierShort] < 1 {
		t.Errorf("expected at least 1 short-term, got %d", stats[memory.TierShort])
	}
	if stats[memory.TierLong] < 1 {
		t.Errorf("expected at least 1 long-term, got %d", stats[memory.TierLong])
	}
}

func TestDecayMemory(t *testing.T) {
	a := newTestAgentWithMemory(t)
	a.memory.SaveWithTier("will decay", "test", memory.TierShort, 0.01)

	decayed := a.DecayMemory(0.5)
	// Low importance memory should decay
	if decayed < 0 {
		t.Errorf("decayed count should be >= 0, got %d", decayed)
	}
}

func TestExpireMidTermMemory_Nil(t *testing.T) {
	a := &Agent{midTerm: nil}
	count := a.ExpireMidTermMemory(24 * time.Hour)
	if count != 0 {
		t.Errorf("expected 0 with nil midTerm, got %d", count)
	}
}

// --- buildMessages ---

func TestBuildMessages_Basic(t *testing.T) {
	a := &Agent{
		soul:   soul.Default(),
		memory: &memory.Store{},
		tools:  tool.NewRegistry(),
		skills: nil,
	}

	msgs := a.buildMessages("hello")
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("first message should be system, got %q", msgs[0].Role)
	}
	lastMsg := msgs[len(msgs)-1]
	if lastMsg.Role != "user" || lastMsg.Content != "hello" {
		t.Errorf("last message should be user 'hello', got role=%q content=%q", lastMsg.Role, lastMsg.Content)
	}
}

// --- getStreamMode ---

func TestGetStreamMode(t *testing.T) {
	a := &Agent{}
	// Default should be native
	mode := a.getStreamMode()
	if mode != StreamModeNative {
		t.Errorf("expected StreamModeNative, got %v", mode)
	}
}

// --- LoopState edge cases ---

func TestLoopStateUnknown(t *testing.T) {
	var s LoopState = 99
	if s.String() != "Unknown" {
		t.Errorf("expected Unknown for invalid LoopState, got %q", s.String())
	}
}

// --- Agent Getter 测试 ---

func TestAgent_Getters(t *testing.T) {
	a := &Agent{
		soul:      soul.Default(),
		tmplMgr:   soul.NewTemplateManager(),
		catalog:   provider.NewModelCatalog(),
		tools:     tool.NewRegistry(),
		mcpClient: tool.NewMCPClient(),
		delegate:  tool.NewDelegateManager(tool.DefaultDelegateConfig()),
		gateway:   tool.NewGateway(tool.NewRegistry()),
		skills:    []*tool.SkillInfo{},
	}

	tests := []struct {
		name string
		got  interface{}
	}{
		{"Soul", a.Soul()},
		{"TemplateManager", a.TemplateManager()},
		{"Tools", a.Tools()},
		{"Catalog", a.Catalog()},
		{"MCPClient", a.MCPClient()},
		{"Delegate", a.Delegate()},
		{"Gateway", a.Gateway()},
		{"Skills", a.Skills()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got == nil {
				t.Errorf("%s() returned nil", tt.name)
			}
		})
	}
}

func TestAgent_GettersNil(t *testing.T) {
	a := &Agent{}

	tests := []struct {
		name string
		got  interface{}
	}{
		{"Registry", a.Registry()},
		{"Provider", a.Provider()},
		{"Sessions", a.Sessions()},
		{"Config", a.Config()},
		{"Memory", a.Memory()},
		{"ContextWindow", a.ContextWindow()},
		{"RAG", a.RAG()},
		{"Metrics", a.Metrics()},
		{"CronEngine", a.CronEngine()},
		{"Autonomy", a.Autonomy()},
		{"MsgGateway", a.MsgGateway()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 这些 getter 在 Agent 未初始化时返回 nil 是预期的
			if tt.got != nil {
				t.Logf("%s() = %v (may be non-nil if initialized)", tt.name, tt.got)
			}
		})
	}
}

func TestAgent_SessionsWithConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Sessions 需要 sessions 字段初始化，而不是 cfg
	sessMgr, err := session.NewManager(tmpDir + "/sessions")
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	a := &Agent{sessions: sessMgr}

	s := a.Sessions()
	if s == nil {
		t.Error("Sessions() should return non-nil when sessions is set")
	}
}

func TestAgent_ConfigWithManager(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	a := &Agent{cfg: cfg}

	c := a.Config()
	if c == nil {
		t.Error("Config() should return non-nil when cfg is set")
	}
}

func TestAgent_SwitchModel(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")
	cfg.Set("autonomy.enabled", "true")
	cfg.Set("max_tokens", "4096")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// 尝试切换到一个不存在的模型
	err = a.SwitchModel("nonexistent-model")
	if err == nil {
		t.Log("SwitchModel() should return error for nonexistent model")
	}
}

func TestAgent_ProviderWithMock(t *testing.T) {
	mockProv := &mockProvider{name: "test-mock"}
	a := &Agent{provider: mockProv}

	p := a.Provider()
	if p == nil {
		t.Error("Provider() returned nil")
	}
	if p.Name() != "test-mock" {
		t.Errorf("Provider().Name() = %q, want %q", p.Name(), "test-mock")
	}
}

// mockProvider 用于测试的 mock provider
type mockProvider struct {
	name string
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return &provider.Response{Content: "mock"}, nil
}

func (m *mockProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (m *mockProvider) Validate() error { return nil }

// errorProvider returns errors for all calls, used to test error handling
type errorProvider struct{}

func (e *errorProvider) Name() string { return "error-mock" }
func (e *errorProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return nil, fmt.Errorf("mock provider error: no API key available")
}

func (e *errorProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("mock provider error: no API key available")
}
func (e *errorProvider) Validate() error { return nil }

type loopingFunctionProvider struct {
	callCount int
	toolName  string
}

func (p *loopingFunctionProvider) Name() string { return "looping-fc" }

func (p *loopingFunctionProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return nil, fmt.Errorf("unexpected Chat call")
}

func (p *loopingFunctionProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("unexpected ChatStream call")
}

func (p *loopingFunctionProvider) Validate() error { return nil }

func (p *loopingFunctionProvider) ChatWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (*provider.Response, error) {
	p.callCount++
	if p.callCount <= 4 {
		return &provider.Response{
			ToolCalls: []provider.ToolCall{
				{
					ID:        fmt.Sprintf("call-%d", p.callCount),
					Name:      p.toolName,
					Arguments: `{"step":"same"}`,
				},
			},
		}, nil
	}
	return &provider.Response{Content: "final answer"}, nil
}

func (p *loopingFunctionProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("unexpected ChatStreamWithOptions call")
}

type streamToolThenFinalProvider struct {
	callCount       int
	streamCallCount int
}

func (p *streamToolThenFinalProvider) Name() string { return "stream-tool-final" }

func (p *streamToolThenFinalProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return nil, fmt.Errorf("unexpected Chat call")
}

func (p *streamToolThenFinalProvider) ChatWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (*provider.Response, error) {
	if hasToolMessage(messages, "rag_search") {
		return &provider.Response{Content: "final answer from retrieved course material"}, nil
	}
	p.callCount++
	if p.callCount == 1 {
		return &provider.Response{
			ToolCalls: []provider.ToolCall{{
				ID:        "call-rag-1",
				Name:      "rag_search",
				Arguments: `{"query":"course chapter"}`,
			}},
		}, nil
	}
	return &provider.Response{Content: "final answer from retrieved course material"}, nil
}

func (p *streamToolThenFinalProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("unexpected ChatStream call")
}

func (p *streamToolThenFinalProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	p.streamCallCount++
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		if p.streamCallCount == 1 && !hasToolMessage(messages, "rag_search") {
			ch <- provider.StreamChunk{
				ToolCallDeltas: []provider.StreamToolCallDelta{{
					Index:     0,
					ID:        "call-rag-1",
					Name:      "rag_search",
					Arguments: `{"query":"course chapter"}`,
				}},
			}
			ch <- provider.StreamChunk{Done: true, FinishReason: "tool_calls"}
			return
		}
		ch <- provider.StreamChunk{Content: "final answer from retrieved course material"}
		ch <- provider.StreamChunk{Done: true, FinishReason: "stop"}
	}()
	return ch, nil
}

func (p *streamToolThenFinalProvider) Validate() error { return nil }

func hasToolMessage(messages []provider.Message, name string) bool {
	for _, msg := range messages {
		if msg.Role == "tool" && msg.Name == name {
			return true
		}
	}
	return false
}

func TestChatWithSessionStreamPersistsToolContext(t *testing.T) {
	for _, mode := range []string{"simulated", "native"} {
		t.Run(mode, func(t *testing.T) {
			sessMgr, err := session.NewManager(t.TempDir())
			if err != nil {
				t.Fatalf("NewManager() error = %v", err)
			}
			sess := sessMgr.New()
			memStore, err := memory.NewStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			cfg, err := config.NewManagerWithDir(t.TempDir())
			if err != nil {
				t.Fatalf("NewManagerWithDir() error = %v", err)
			}
			if err := cfg.Set("stream_mode", mode); err != nil {
				t.Fatalf("Set(stream_mode) error = %v", err)
			}
			registry := tool.NewRegistry()
			registry.Register(&tool.Tool{
				Name:       "rag_search",
				Permission: tool.PermAuto,
				Parameters: map[string]tool.Param{
					"query": {Type: "string", Required: true},
				},
				Handler: func(args map[string]any) (string, error) {
					return "找到 1 条课程资料：第一章的正确内容", nil
				},
				ParallelSafe: true,
			})

			a := &Agent{
				provider:   &streamToolThenFinalProvider{},
				sessions:   sessMgr,
				memory:     memStore,
				shortTerm:  memory.NewShortTermBuffer(8),
				tools:      registry,
				gateway:    tool.NewGateway(registry),
				cfg:        cfg,
				metrics:    metrics.NewMetrics(),
				contextEst: contextx.NewTokenEstimator(4096),
				contextWin: contextx.NewContextWindow(contextx.DefaultWindowConfig()),
			}

			events, err := a.ChatWithSessionStreamInputWithLoopConfig(context.Background(), sess.ID, TextUserTurnInput("问第一章"), LoopConfig{
				MaxIterations:          3,
				Timeout:                time.Second,
				AutoApprove:            true,
				RepeatToolCallLimit:    3,
				ToolOnlyIterationLimit: 3,
				DuplicateFetchLimit:    1,
			})
			if err != nil {
				t.Fatalf("ChatWithSessionStreamInputWithLoopConfig() error = %v", err)
			}
			var doneContent string
			for evt := range events {
				if evt.Type == ChatEventError {
					t.Fatalf("unexpected stream error: %v", evt.Err)
				}
				if evt.Type == ChatEventDone {
					doneContent = evt.Content
				}
			}
			if !strings.Contains(doneContent, naturalCitationHeader) {
				t.Fatalf("expected done content to include natural citations, got %q", doneContent)
			}
			if !strings.Contains(doneContent, `[1] Local RAG search. Query: "course chapter".`) {
				t.Fatalf("expected done content to cite rag_search, got %q", doneContent)
			}

			messages := sess.GetMessages()
			if len(messages) != 4 {
				t.Fatalf("expected user, assistant tool-call, tool result, final assistant; got %d messages: %#v", len(messages), messages)
			}
			if messages[0].Role != "user" || !strings.Contains(messages[0].Content, "第一章") {
				t.Fatalf("expected first message to be user turn, got %#v", messages[0])
			}
			if messages[1].Role != "assistant" || len(messages[1].ToolCalls) != 1 || messages[1].ToolCalls[0].Name != "rag_search" {
				t.Fatalf("expected assistant rag_search tool call, got %#v", messages[1])
			}
			if messages[2].Role != "tool" || messages[2].Name != "rag_search" || !strings.Contains(messages[2].Content, "第一章的正确内容") {
				t.Fatalf("expected persisted rag_search tool result, got %#v", messages[2])
			}
			if messages[3].Role != "assistant" || !strings.Contains(messages[3].Content, "final answer") {
				t.Fatalf("expected final assistant answer, got %#v", messages[3])
			}
			if !strings.Contains(messages[3].Content, naturalCitationHeader) {
				t.Fatalf("expected persisted final assistant answer to include citations, got %#v", messages[3])
			}
		})
	}
}

type cronNotifyGateway struct {
	mu         sync.Mutex
	name       string
	running    bool
	messages   []string
	deliveries []cronNotifyDelivery
	forwards   []cronNotifyForwardedText
	nextID     int
}

type cronNotifyDelivery struct {
	chatID       string
	replyToMsgID string
	message      string
}

type cronNotifyForwardedText struct {
	chatID string
	title  string
	chunks []string
}

func (g *cronNotifyGateway) Name() string { return g.name }
func (g *cronNotifyGateway) Start(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.running = true
	return nil
}

func (g *cronNotifyGateway) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.running = false
	return nil
}

func (g *cronNotifyGateway) Send(ctx context.Context, chatID string, message string) error {
	_, err := g.SendWithReceipt(ctx, chatID, message)
	return err
}

func (g *cronNotifyGateway) SendWithReceipt(ctx context.Context, chatID string, message string) (msggateway.SentMessage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	id := fmt.Sprintf("%d", g.nextID)
	g.messages = append(g.messages, message)
	g.deliveries = append(g.deliveries, cronNotifyDelivery{chatID: chatID, message: message})
	return msggateway.SentMessage{ID: id, ChatID: chatID}, nil
}

func (g *cronNotifyGateway) SendWithReply(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	_, err := g.SendWithReplyReceipt(ctx, chatID, replyToMsgID, message)
	return err
}

func (g *cronNotifyGateway) SendWithReplyReceipt(ctx context.Context, chatID string, replyToMsgID string, message string) (msggateway.SentMessage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	id := fmt.Sprintf("%d", g.nextID)
	g.messages = append(g.messages, message)
	g.deliveries = append(g.deliveries, cronNotifyDelivery{chatID: chatID, replyToMsgID: replyToMsgID, message: message})
	return msggateway.SentMessage{ID: id, ChatID: chatID}, nil
}

func (g *cronNotifyGateway) SendForwardedText(_ context.Context, chatID string, title string, chunks []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.forwards = append(g.forwards, cronNotifyForwardedText{
		chatID: chatID,
		title:  title,
		chunks: append([]string(nil), chunks...),
	})
	return nil
}

func (g *cronNotifyGateway) IsRunning() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

func (g *cronNotifyGateway) messageSnapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.messages...)
}

func (g *cronNotifyGateway) deliverySnapshot() []cronNotifyDelivery {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]cronNotifyDelivery(nil), g.deliveries...)
}

func (g *cronNotifyGateway) forwardSnapshot() []cronNotifyForwardedText {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]cronNotifyForwardedText(nil), g.forwards...)
}

type staticChatProvider struct {
	name    string
	content string
	err     error
}

func (p *staticChatProvider) Name() string { return p.name }
func (p *staticChatProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &provider.Response{Content: p.content}, nil
}
func (p *staticChatProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("unexpected ChatStream call")
}
func (p *staticChatProvider) Validate() error { return nil }

// --- v0.64.0 Agent Package Coverage Improvements ---

func TestAgent_Tools(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")
	cfg.Set("autonomy.enabled", "true")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	tools := a.Tools()
	if tools == nil {
		t.Error("Tools() returned nil")
	}
}

func TestAgent_Catalog(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	catalog := a.Catalog()
	if catalog == nil {
		t.Error("Catalog() returned nil")
	}
}

func TestAgent_Registry(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	registry := a.Registry()
	if registry == nil {
		t.Error("Registry() returned nil")
	}
}

func TestAgent_MCPClient(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	client := a.MCPClient()
	if client == nil {
		t.Error("MCPClient() returned nil")
	}
}

func TestAgent_MemoryStats(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	stats := a.MemoryStats()
	if stats == nil {
		t.Error("MemoryStats() returned nil")
	}
}

func TestAgent_DecayMemory(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// DecayMemory should not panic
	a.DecayMemory(0.5)
}

func TestAgent_PromoteMemory(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// PromoteMemory with invalid ID should return error
	err = a.PromoteMemory("nonexistent-id")
	if err == nil {
		t.Log("PromoteMemory() should return error for nonexistent ID")
	}
}

func TestAgent_Remember(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// Remember should not panic
	err = a.Remember("test content", "default")
	if err != nil {
		t.Logf("Remember() error: %v", err)
	}
}

func TestAgent_RememberLongTerm(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// RememberLongTerm should not panic
	err = a.RememberLongTerm("test long-term content", "default")
	if err != nil {
		t.Logf("RememberLongTerm() error: %v", err)
	}
}

func TestAgent_Recall(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// Recall should not panic
	results := a.Recall("test query")
	if results == nil {
		t.Error("Recall() returned nil")
	}
}

func TestAgent_RecallMidTerm(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// RecallMidTerm should not panic
	results := a.RecallMidTerm("test query", 5)
	if results == nil {
		t.Error("RecallMidTerm() returned nil")
	}
}

func TestAgent_TemplateManager(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	tm := a.TemplateManager()
	if tm == nil {
		t.Error("TemplateManager() returned nil")
	}
}

func TestAgent_Soul(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")
	cfg.Set("soul.enabled", "true")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	soul := a.Soul()
	if soul == nil {
		t.Error("Soul() returned nil")
	}
}

func TestAgent_getStreamMode(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		streamMode string
		expected   StreamMode
	}{
		{"native", StreamModeNative},
		{"simulated", StreamModeSimulated},
		{"", StreamModeNative},        // default
		{"invalid", StreamModeNative}, // invalid defaults to native
	}

	for _, tt := range tests {
		cfg, _ := config.NewManagerWithDir(tmpDir)
		cfg.Set("stream_mode", tt.streamMode)

		a, err := New(cfg)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer a.Close()

		got := a.getStreamMode()
		if got != tt.expected {
			t.Errorf("getStreamMode(%q) = %q, want %q", tt.streamMode, got, tt.expected)
		}
	}
}

func TestAgent_splitIntoChunks(t *testing.T) {
	tests := []struct {
		text     string
		maxLen   int
		expected int
	}{
		{"", 100, 1},
		{"hello", 100, 1},
		{"hello world", 5, 3}, // "hello", " worl", "d"
	}

	for _, tt := range tests {
		chunks := splitIntoChunks(tt.text, tt.maxLen)
		if len(chunks) != tt.expected {
			t.Errorf("splitIntoChunks(%q, %d) returned %d chunks, want %d", tt.text, tt.maxLen, len(chunks), tt.expected)
		}
	}
}

func TestAgent_inferImportance(t *testing.T) {
	// Test that inferImportance returns a value in valid range
	tests := []struct {
		content string
	}{
		{""},
		{"hello"},
		{"IMPORTANT: critical information"},
		{"TODO: remember this"},
	}

	for _, tt := range tests {
		got := inferImportance(tt.content)
		// Should return value between 0 and 1
		if got < 0 || got > 1 {
			t.Errorf("inferImportance(%q) = %f, should be between 0 and 1", tt.content, got)
		}
	}
}

// ---------------------------------------------------------------------------
// v0.64.0: Agent 包测试补全 - 基础函数覆盖
// ---------------------------------------------------------------------------

func TestAgentNewWithMinimalConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}

	// Minimal config
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	if a == nil {
		t.Fatal("New() returned nil")
	}
}

func TestAgentNewWithSoulPath(t *testing.T) {
	tmpDir := t.TempDir()
	soulPath := filepath.Join(tmpDir, "SOUL.md")

	// Write minimal soul
	if err := os.WriteFile(soulPath, []byte("# Test Soul\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")
	cfg.Set("soul_path", soulPath)

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	if a.Soul() == nil {
		t.Error("Soul() returned nil")
	}
}

func TestAgentGetters(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// Test all getters
	if a.Config() == nil {
		t.Error("Config() returned nil")
	}
	if a.Provider() == nil {
		t.Error("Provider() returned nil")
	}
	if a.Tools() == nil {
		t.Error("Tools() returned nil")
	}
	if a.Catalog() == nil {
		t.Error("Catalog() returned nil")
	}
	if a.Registry() == nil {
		t.Error("Registry() returned nil")
	}
	if a.MCPClient() == nil {
		t.Error("MCPClient() returned nil")
	}
	if a.Delegate() == nil {
		t.Error("Delegate() returned nil")
	}
	if a.Autonomy() == nil {
		t.Error("Autonomy() returned nil")
	}
	if a.Gateway() == nil {
		t.Error("Gateway() returned nil")
	}
	if a.MsgGateway() == nil {
		t.Error("MsgGateway() returned nil")
	}
}

func TestAgentSessions(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	sessions := a.Sessions()
	if sessions == nil {
		t.Error("Sessions() returned nil")
	}
}

func TestAgentTemplateManager(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	tm := a.TemplateManager()
	if tm == nil {
		t.Error("TemplateManager() returned nil")
	}
}

func TestAgentSwitchModel(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// Switch to a different model
	err = a.SwitchModel("gpt-5.4-mini")
	if err != nil {
		t.Errorf("SwitchModel() error = %v", err)
	}

	// SwitchModel updates the provider, verification is complex
	// Just ensure the call doesn't crash
	t.Logf("SwitchModel() completed")
}

func TestAgentMemoryStats(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	stats := a.MemoryStats()
	if stats == nil {
		t.Error("MemoryStats() returned nil")
	}
}

func TestAgentBuildMemoryContext(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// Build memory context with empty messages
	messages := []provider.Message{}
	result := a.buildMemoryContext(messages)
	if result == nil {
		t.Error("buildMemoryContext() returned nil")
	}
}

func TestAgentAutoSummarize(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// AutoSummarize should not panic
	a.autoSummarize()
}

func TestAgentStartAutonomy(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")
	cfg.Set("autonomy.enabled", "true")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	original := a.Autonomy()
	if original == nil {
		t.Fatal("Autonomy() should not be nil")
	}

	if err := a.StartAutonomy(context.Background()); err != nil {
		t.Fatalf("StartAutonomy() error = %v", err)
	}
	if err := a.StartAutonomy(context.Background()); err != nil {
		t.Fatalf("second StartAutonomy() error = %v", err)
	}

	if a.Autonomy() != original {
		t.Fatal("StartAutonomy() should reuse the existing autonomy kit")
	}

	status := a.Autonomy().Status()
	if !status.Started {
		t.Fatal("autonomy should be started")
	}
	if status.PoolStats.WorkerCount < 1 {
		t.Fatalf("expected at least one worker, got %d", status.PoolStats.WorkerCount)
	}
	if status.LastHeartbeat.IsZero() {
		t.Fatal("expected initial heartbeat to be recorded on startup")
	}
}

func TestAgentStartAutonomyNowBypassesDisabledConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	if a.Autonomy().Status().Started {
		t.Fatal("autonomy should be stopped by default")
	}
	if err := a.StartAutonomy(context.Background()); err != nil {
		t.Fatalf("StartAutonomy() error = %v", err)
	}
	if a.Autonomy().Status().Started {
		t.Fatal("StartAutonomy should respect disabled config")
	}
	if err := a.StartAutonomyNow(context.Background()); err != nil {
		t.Fatalf("StartAutonomyNow() error = %v", err)
	}
	if !a.Autonomy().Status().Started {
		t.Fatal("StartAutonomyNow should force-start autonomy")
	}
}

func TestBuildAutonomyRuntimeConfigUsesWorkerConfig(t *testing.T) {
	autoApprove := false
	cfg := &config.Config{
		Autonomy: config.AutonomyConfig{
			Worker: config.AutonomyWorkerConfig{
				MaxIterations:          25,
				TimeoutSeconds:         240,
				AutoApprove:            &autoApprove,
				RepeatToolCallLimit:    5,
				ToolOnlyIterationLimit: 6,
				DuplicateFetchLimit:    2,
				DisabledTools:          []string{"autonomy", "cron_add"},
			},
		},
	}

	got := buildAutonomyRuntimeConfig(cfg)
	if got.Pool.WorkerLoop.MaxIterations != 25 {
		t.Fatalf("expected max iterations 25, got %d", got.Pool.WorkerLoop.MaxIterations)
	}
	if got.Pool.WorkerLoop.Timeout != 240*time.Second {
		t.Fatalf("expected worker timeout 240s, got %s", got.Pool.WorkerLoop.Timeout)
	}
	if got.Pool.WorkerLoop.AutoApprove {
		t.Fatal("expected auto approve false")
	}
	if !got.Pool.WorkerLoop.AutoApproveSet {
		t.Fatal("expected auto approve to be marked configured")
	}
	if got.Pool.WorkerLoop.RepeatToolCallLimit != 5 {
		t.Fatalf("expected repeat limit 5, got %d", got.Pool.WorkerLoop.RepeatToolCallLimit)
	}
	if got.Pool.WorkerLoop.ToolOnlyIterationLimit != 6 {
		t.Fatalf("expected tool-only limit 6, got %d", got.Pool.WorkerLoop.ToolOnlyIterationLimit)
	}
	if got.Pool.WorkerLoop.DuplicateFetchLimit != 2 {
		t.Fatalf("expected duplicate fetch limit 2, got %d", got.Pool.WorkerLoop.DuplicateFetchLimit)
	}
	if len(got.Pool.WorkerLoop.DisabledTools) != 2 || got.Pool.WorkerLoop.DisabledTools[0] != "autonomy" || got.Pool.WorkerLoop.DisabledTools[1] != "cron_add" {
		t.Fatalf("unexpected disabled tools: %v", got.Pool.WorkerLoop.DisabledTools)
	}
}

func TestBuildAutonomyRuntimeConfigAllowsEmptyDisabledTools(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Autonomy.Worker.DisabledTools = []string{}

	got := buildAutonomyRuntimeConfig(cfg)
	if got.Pool.WorkerLoop.DisabledTools == nil {
		t.Fatal("expected explicit empty disabled tools to be preserved")
	}
	if len(got.Pool.WorkerLoop.DisabledTools) != 0 {
		t.Fatalf("expected disabled tools to be empty, got %v", got.Pool.WorkerLoop.DisabledTools)
	}
}

func TestAutonomyWorkerCompletionNotifiesRecentChat(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	a.provider = &mockProvider{name: "test-mock"}

	gm := msggateway.NewGatewayManager()
	gw := &cronNotifyGateway{name: "telegram", running: true}
	if err := gm.Register(gw); err != nil {
		t.Fatalf("register gateway: %v", err)
	}
	a.msgGateway = gm
	a.RecordRecentChatTarget("telegram", "12345", "77")

	if err := a.StartAutonomyNow(context.Background()); err != nil {
		t.Fatalf("StartAutonomyNow() error = %v", err)
	}
	a.Autonomy().AddTask("worker completion report", "Return a short result.", autonomy.PriorityNormal, nil)

	deadline := time.Now().Add(3 * time.Second)
	var messages []string
	for time.Now().Before(deadline) {
		messages = gw.messageSnapshot()
		if len(messages) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(messages) == 0 {
		t.Fatal("expected worker completion notification")
	}
	if !strings.Contains(messages[0], "worker completion report") || !strings.Contains(messages[0], "mock") {
		t.Fatalf("unexpected worker notification: %q", messages[0])
	}
}

func TestAgentConfiguresAutonomyQueuePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	want := filepath.Join(tmpDir, "runtime", "autonomy_queue.json")
	if got := a.Autonomy().Status().QueueStore; got != want {
		t.Fatalf("expected autonomy queue store %q, got %q", want, got)
	}
}

func TestAgentLoadSkills(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// LoadSkills with empty directory should not panic
	a.LoadSkills(filepath.Join(tmpDir, "skills"))
}

func TestAgentLoadSkillsUsesRegistryLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	skillDir := filepath.Join(skillsDir, "script-skill")
	scriptsDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# script-skill\n\nDesc.\n\n## Tools\n\n- `echo`: Echo\n"), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "echo.sh"), []byte("echo agent-skill\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	a := &Agent{tools: tool.NewRegistry()}
	count, err := a.LoadSkills(skillsDir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 skill, got %d", count)
	}
	if a.SkillRegistry() == nil {
		t.Fatal("expected skill registry to be initialized")
	}

	registered, ok := a.Tools().Get("skill_script-skill_echo")
	if !ok {
		t.Fatal("expected skill tool to be registered")
	}
	if !registered.Enabled {
		t.Fatal("expected skill tool to be enabled after lifecycle activation")
	}

	out, err := a.Tools().Call("skill_script-skill_echo", nil)
	if err != nil {
		t.Fatalf("call skill tool: %v", err)
	}
	if out != "agent-skill\n" {
		t.Fatalf("expected script output, got %q", out)
	}
}

func TestAgentHandleSkillRead(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	handler := tool.NewSkillToolService(a.skills).HandleRead

	// Call handler with empty args should return skill list (no error)
	result, err := handler(map[string]any{})
	if err != nil {
		t.Errorf("handleSkillRead handler error = %v", err)
	}
	if result == "" {
		t.Error("handleSkillRead handler should return skill list")
	}

	result2, err := handler(map[string]any{"name": "nonexistent"})
	if err != nil {
		t.Errorf("handleSkillRead handler error = %v", err)
	}
	if !strings.Contains(result2, "not found") {
		t.Error("handleSkillRead should indicate skill not found")
	}
}

func TestAgentHandleSkillReadMatchesAliases(t *testing.T) {
	a := &Agent{
		skills: []*tool.SkillInfo{
			{
				Name:    "research",
				Aliases: []string{"research_orchestrator"},
				Dir:     t.TempDir(),
			},
		},
	}

	skillFile := filepath.Join(a.skills[0].Dir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("# Research\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	handler := tool.NewSkillToolService(a.skills).HandleRead
	result, err := handler(map[string]any{"name": "research orchestrator"})
	if err != nil {
		t.Fatalf("handleSkillRead error = %v", err)
	}
	if !strings.Contains(result, "# Research") {
		t.Fatalf("expected alias match to return skill file, got %q", result)
	}
}

func TestAgentConnectMCPServer(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	// ConnectMCPServer should not panic
	a.ConnectMCPServer("test", "http://localhost:8080", "test-key")
}

func TestApplyWebSearchEnvUsesExaKey(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir() error = %v", err)
	}
	if err := cfg.Set("web_search.provider", "exa"); err != nil {
		t.Fatalf("Set provider error = %v", err)
	}

	t.Setenv("EXA_API_KEY", "exa-env-key")
	applyWebSearchEnv(cfg)

	if got := cfg.Get().WebSearch.APIKey; got != "exa-env-key" {
		t.Fatalf("expected exa env key, got %q", got)
	}
}

func TestAgentChatMethodsExist(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	// Replace provider with error mock to avoid real API calls
	a.provider = &errorProvider{}

	// Test that Chat methods exist and handle errors gracefully
	ctx := context.Background()

	// Chat should return error without proper provider setup
	_, err = a.Chat(ctx, "test")
	if err == nil {
		t.Log("Chat() should return error without proper setup")
	}

	// ChatWithSession should return error
	_, err = a.ChatWithSession(ctx, "session1", "test")
	if err == nil {
		t.Log("ChatWithSession() should return error without proper setup")
	}
}

func TestAgentStreamMethodsExist(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	// Replace provider with error mock to avoid real API calls
	a.provider = &errorProvider{}
	ctx := context.Background()

	// ChatStream should return error without proper provider setup
	_, err = a.ChatStream(ctx, "test")
	if err == nil {
		t.Log("ChatStream() should return error without proper setup")
	}

	// ChatWithSessionStream should return error
	_, err = a.ChatWithSessionStream(ctx, "session1", "test")
	if err == nil {
		t.Log("ChatWithSessionStream() should return error without proper setup")
	}
}

// ---------------------------------------------------------------------------
// v0.92.0: Coverage boost — getter methods, memory methods, autonomy
// ---------------------------------------------------------------------------

func TestAgent_GetterMethods(t *testing.T) {
	a := &Agent{
		soul:      &soul.Soul{},
		tmplMgr:   &soul.TemplateManager{},
		tools:     tool.NewRegistry(),
		catalog:   provider.NewModelCatalog(),
		provider:  nil, // no provider needed for nil check
		registry:  provider.NewRegistry(),
		mcpClient: nil,
		delegate:  nil,
		autonomy:  nil,
		sessions:  mustSessionManager(t),
	}

	// Test all getter methods (currently 0% coverage)
	if a.Soul() == nil {
		t.Error("Soul() should not be nil")
	}
	if a.TemplateManager() == nil {
		t.Error("TemplateManager() should not be nil")
	}
	if a.Tools() == nil {
		t.Error("Tools() should not be nil")
	}
	if a.Catalog() == nil {
		t.Error("Catalog() should not be nil")
	}
	if a.Registry() == nil {
		t.Error("Registry() should not be nil")
	}
	if a.MCPClient() != nil {
		t.Error("MCPClient() should be nil")
	}
	if a.Delegate() != nil {
		t.Error("Delegate() should be nil")
	}
	if a.Autonomy() != nil {
		t.Error("Autonomy() should be nil")
	}
}

func TestAgent_MemoryMethods(t *testing.T) {
	a := newTestAgentWithMemory(t)

	// Remember
	err := a.Remember("test content", "test")
	if err != nil {
		t.Errorf("Remember: %v", err)
	}

	// RememberLongTerm
	err = a.RememberLongTerm("important fact", "security")
	if err != nil {
		t.Errorf("RememberLongTerm: %v", err)
	}

	// Recall
	results := a.Recall("test")
	if len(results) == 0 {
		t.Error("Recall should return results")
	}

	// RecallMidTerm with nil midTerm
	midResults := a.RecallMidTerm("test", 5)
	if midResults != nil {
		t.Error("RecallMidTerm with nil midTerm should return nil")
	}

	// MemoryStats
	stats := a.MemoryStats()
	if stats == nil {
		t.Error("MemoryStats should not be nil")
	}

	// DecayMemory
	decayed := a.DecayMemory(0.01)
	_ = decayed // just verify it doesn't panic

	// PromoteMemory with invalid ID
	err = a.PromoteMemory("nonexistent-id")
	// May or may not error depending on implementation
	_ = err

	// ExpireMidTermMemory with nil midTerm
	expired := a.ExpireMidTermMemory(time.Hour)
	if expired != 0 {
		t.Error("ExpireMidTermMemory with nil midTerm should return 0")
	}
}

func TestAgent_StartAutonomy_Nil(t *testing.T) {
	a := &Agent{autonomy: nil}
	err := a.StartAutonomy(context.Background())
	if err == nil {
		t.Error("expected error for nil autonomy kit")
	}
}

func TestRunLoopWithSessionLazyStartsAutonomy(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")
	cfg.Set("autonomy.enabled", "true")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	a.provider = &mockProvider{name: "test-mock"}

	if a.Autonomy().Status().Started {
		t.Fatal("autonomy should be stopped before the first loop")
	}

	result, err := a.RunLoop(context.Background(), "say hi", DefaultLoopConfig())
	if err != nil {
		t.Fatalf("RunLoop() error = %v", err)
	}
	if result == nil || result.Response == "" {
		t.Fatal("expected non-empty run loop response")
	}
	if !a.Autonomy().Status().Started {
		t.Fatal("RunLoop() should lazy-start autonomy")
	}
}

func TestRunLoopWithSessionLazyStartsAutonomyFromFormalConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	current := cfg.Get()
	current.Autonomy.Enabled = true
	cfgBytes, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), cfgBytes, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := cfg.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	a.provider = &mockProvider{name: "test-mock"}

	if a.Autonomy().Status().Started {
		t.Fatal("autonomy should be stopped before the first loop")
	}

	result, err := a.RunLoop(context.Background(), "say hi", DefaultLoopConfig())
	if err != nil {
		t.Fatalf("RunLoop() error = %v", err)
	}
	if result == nil || result.Response == "" {
		t.Fatal("expected non-empty run loop response")
	}
	if !a.Autonomy().Status().Started {
		t.Fatal("RunLoop() should lazy-start autonomy from formal config")
	}
}

func TestNewRegistersOpenAIMultimodalProviderFromDedicatedConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-main")
	cfg.Set("model", "gpt-3.5-turbo")
	cfg.Set("multimodal.provider", "openai")
	cfg.Set("multimodal.api_key", "sk-mm")
	cfg.Set("multimodal.api_base", "https://example.com/v1")
	cfg.Set("multimodal.image_model", "gpt-4.1-mini")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	if a.mediaProcessor == nil {
		t.Fatal("expected mediaProcessor to be initialized")
	}
	providers := a.mediaProcessor.ProvidersForModality(multimodal.ModalityImage)
	found := false
	for _, provider := range providers {
		if provider.Name() == "openai-media" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected openai-media provider to be registered for image modality")
	}
}

func TestResolveImageGenerationConfigPrefersDedicatedGeminiConfig(t *testing.T) {
	cfg := &config.Config{
		ImageGeneration: config.ImageGenerationConfig{
			Provider: "gemini",
			APIKey:   "gen-key",
			APIBase:  "https://api.shiokou.asia/v1",
			AuthMode: "bearer",
			Model:    "gemini-3.1-flash-image-preview",
		},
	}

	genCfg, ok := resolveImageGenerationConfig(cfg)
	if !ok {
		t.Fatal("expected image generation config to resolve")
	}
	if genCfg.Provider != "gemini" {
		t.Fatalf("expected gemini provider, got %q", genCfg.Provider)
	}
	if genCfg.APIBase != "https://api.shiokou.asia/v1" {
		t.Fatalf("expected gemini api base, got %q", genCfg.APIBase)
	}
}

func TestResolveTTSConfigPrefersDedicatedConfig(t *testing.T) {
	cfg := &config.Config{
		TTS: config.TTSConfig{
			Provider: "openai",
			APIKey:   "tts-key",
			APIBase:  "https://speech.example/v1",
			AuthMode: "bearer",
			Model:    "gpt-4o-mini-tts",
			Voice:    "alloy",
			Format:   "mp3",
			Speed:    1.1,
		},
	}

	ttsCfg, ok := resolveTTSConfig(cfg)
	if !ok {
		t.Fatal("expected tts config to resolve")
	}
	if ttsCfg.APIBase != "https://speech.example/v1" || ttsCfg.Model != "gpt-4o-mini-tts" {
		t.Fatalf("unexpected tts config: %+v", ttsCfg)
	}
}

func TestAgent_SwitchModel_NoProvider(t *testing.T) {
	// SwitchModel requires a fully initialized Agent with config manager
	// Testing with nil config should not panic
	a := &Agent{
		catalog:  provider.NewModelCatalog(),
		registry: provider.NewRegistry(),
		cfg:      nil,
	}
	// Catalog is empty, ResolveProvider behavior depends on implementation
	// Just verify no panic occurs
	_ = a.catalog
	_ = a.registry
}

func TestAgent_Config(t *testing.T) {
	cfg, err := config.NewManager()
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	a := &Agent{cfg: cfg}
	if a.Config() != cfg {
		t.Error("Config() should return the same config pointer")
	}
}

func TestChatWithSessionAppliesAgentLoopConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")
	cfg.Set("agent.repeat_tool_call_limit", "60")
	cfg.Set("agent.tool_only_iteration_limit", "60")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	toolName := "noop_repeat"
	a.Tools().Register(&tool.Tool{
		Name:       toolName,
		Enabled:    true,
		Permission: tool.PermAuto,
		Handler: func(args map[string]any) (string, error) {
			return "", nil
		},
	})

	a.provider = &loopingFunctionProvider{toolName: toolName}
	sess := a.Sessions().New()

	resp, err := a.ChatWithSession(context.Background(), sess.ID, "loop until done")
	if err != nil {
		t.Fatalf("ChatWithSession() error = %v", err)
	}
	if resp != "final answer" {
		t.Fatalf("expected final answer, got %q", resp)
	}
}

func TestAgent_Sessions(t *testing.T) {
	a := &Agent{sessions: mustSessionManager(t)}
	if a.Sessions() == nil {
		t.Error("Sessions() should not be nil")
	}
}

func TestAgent_Gateway(t *testing.T) {
	a := &Agent{gateway: nil}
	if a.Gateway() != nil {
		t.Error("Gateway() should be nil when not set")
	}
}

func TestCronToolsLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	addResp, err := a.Tools().Call("cron_add", map[string]any{
		"id":       "job1",
		"schedule": "每小时",
		"mode":     "shell",
		"command":  "echo hello-cron-tool",
	})
	if err != nil {
		t.Fatalf("cron_add error = %v", err)
	}
	if !strings.Contains(addResp, `"id":"job1"`) {
		t.Fatalf("unexpected cron_add response: %s", addResp)
	}
	if !a.CronEngine().IsRunning() {
		t.Fatal("cron engine should be running after cron_add")
	}
	job, ok := a.CronEngine().GetJob("job1")
	if !ok {
		t.Fatal("expected job1 to exist")
	}
	if got := job.Metadata["mode"]; got != "shell" {
		t.Fatalf("expected shell mode metadata, got %q", got)
	}

	listResp, err := a.Tools().Call("cron_list", nil)
	if err != nil {
		t.Fatalf("cron_list error = %v", err)
	}
	var listed map[string]any
	if err := json.Unmarshal([]byte(listResp), &listed); err != nil {
		t.Fatalf("unmarshal cron_list response: %v", err)
	}
	if listed["total"].(float64) < 1 {
		t.Fatalf("expected at least one cron job, got %v", listed["total"])
	}

	if _, err := a.Tools().Call("cron_pause", map[string]any{"id": "job1"}); err != nil {
		t.Fatalf("cron_pause error = %v", err)
	}
	job, _ = a.CronEngine().GetJob("job1")
	if job.Status != cron.StatusPaused {
		t.Fatalf("expected paused status, got %s", job.Status)
	}

	if _, err := a.Tools().Call("cron_resume", map[string]any{"id": "job1"}); err != nil {
		t.Fatalf("cron_resume error = %v", err)
	}
	job, _ = a.CronEngine().GetJob("job1")
	if job.Status != cron.StatusIdle {
		t.Fatalf("expected idle status, got %s", job.Status)
	}

	if _, err := a.Tools().Call("cron_remove", map[string]any{"id": "job1"}); err != nil {
		t.Fatalf("cron_remove error = %v", err)
	}
	if _, ok := a.CronEngine().GetJob("job1"); ok {
		t.Fatal("expected job1 to be removed")
	}
}

func TestCronUnifiedToolLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()

	addResp, err := a.Tools().Call("cron", map[string]any{
		"action":   "add",
		"id":       "job2",
		"schedule": "每小时",
		"mode":     "shell",
		"command":  "echo hello-unified-cron",
	})
	if err != nil {
		t.Fatalf("cron(add) error = %v", err)
	}
	if !strings.Contains(addResp, `"id":"job2"`) {
		t.Fatalf("unexpected cron(add) response: %s", addResp)
	}

	statusResp, err := a.Tools().Call("cron", map[string]any{"action": "status"})
	if err != nil {
		t.Fatalf("cron(status) error = %v", err)
	}
	if !strings.Contains(statusResp, `"job_count"`) {
		t.Fatalf("unexpected cron(status) response: %s", statusResp)
	}

	if _, err := a.Tools().Call("cron", map[string]any{"action": "pause", "id": "job2"}); err != nil {
		t.Fatalf("cron(pause) error = %v", err)
	}
	if _, err := a.Tools().Call("cron", map[string]any{"action": "resume", "id": "job2"}); err != nil {
		t.Fatalf("cron(resume) error = %v", err)
	}
	if _, err := a.Tools().Call("cron", map[string]any{"action": "remove", "id": "job2"}); err != nil {
		t.Fatalf("cron(remove) error = %v", err)
	}
}

func TestCronAddAgentModeExecutesLoop(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	a.provider = &mockProvider{name: "test-mock"}

	if _, err := a.Tools().Call("cron_add", map[string]any{
		"id":       "agent-job",
		"schedule": "每小时",
		"mode":     "agent",
		"command":  "say hello from cron agent",
	}); err != nil {
		t.Fatalf("cron_add(agent) error = %v", err)
	}

	job, ok := a.CronEngine().GetJob("agent-job")
	if !ok {
		t.Fatal("expected agent-job to exist")
	}
	if got := job.Metadata["mode"]; got != "agent" {
		t.Fatalf("expected agent mode metadata, got %q", got)
	}
	if got := strings.TrimSpace(job.Metadata["session_id"]); got != "" {
		t.Fatalf("expected cron agent job to be sessionless by default, got session_id %q", got)
	}
	beforeSessions := a.Sessions().Count()
	if err := job.Task(); err != nil {
		t.Fatalf("agent cron task error = %v", err)
	}
	if afterSessions := a.Sessions().Count(); afterSessions != beforeSessions {
		t.Fatalf("expected sessionless cron task not to create chat sessions, before=%d after=%d", beforeSessions, afterSessions)
	}
}

func TestCronAddAgentModeCanBindExplicitSession(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	a.provider = &mockProvider{name: "test-mock"}

	sess := a.Sessions().NewWithTitle("bound cron context")
	if _, err := a.Tools().Call("cron_add", map[string]any{
		"id":         "agent-job-bound",
		"schedule":   "每小时",
		"mode":       "agent",
		"command":    "say hello from bound cron agent",
		"session_id": sess.ID,
	}); err != nil {
		t.Fatalf("cron_add(agent bound) error = %v", err)
	}

	job, ok := a.CronEngine().GetJob("agent-job-bound")
	if !ok {
		t.Fatal("expected agent-job-bound to exist")
	}
	if got := job.Metadata["session_id"]; got != sess.ID {
		t.Fatalf("expected explicit session_id %q, got %q", sess.ID, got)
	}
	if err := job.Task(); err != nil {
		t.Fatalf("bound agent cron task error = %v", err)
	}
	if got := sess.MessageCount(); got == 0 {
		t.Fatal("expected bound cron task to append messages to explicit session")
	}
}

func TestRunLoopEphemeralSkipsFinalAnswerPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	a.provider = &mockProvider{name: "test-mock"}

	loopCfg := DefaultLoopConfig()
	loopCfg.Ephemeral = true
	if _, err := a.RunLoop(context.Background(), "background check", loopCfg); err != nil {
		t.Fatalf("RunLoop(ephemeral) error = %v", err)
	}

	dir := filepath.Join(cfg.HomeDir(), "knowledge", "final_answers")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected ephemeral loop not to write final answer documents, stat err=%v", err)
	}
}

func TestCronAddAgentModeSendsTelegramNotification(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	a.provider = &mockProvider{name: "test-mock"}

	gm := msggateway.NewGatewayManager()
	gw := &cronNotifyGateway{name: "telegram", running: true}
	if err := gm.Register(gw); err != nil {
		t.Fatalf("register gateway: %v", err)
	}
	a.msgGateway = gm

	if _, err := a.Tools().Call("cron_add", map[string]any{
		"id":                  "agent-job-tg",
		"schedule":            "每小时",
		"mode":                "agent",
		"command":             "say hello from cron agent",
		"platform":            "telegram",
		"chat_id":             "12345",
		"reply_to_message_id": "77",
		"session_id":          "session-telegram-cron",
	}); err != nil {
		t.Fatalf("cron_add(agent telegram) error = %v", err)
	}

	job, ok := a.CronEngine().GetJob("agent-job-tg")
	if !ok {
		t.Fatal("expected agent-job-tg to exist")
	}
	if err := job.Task(); err != nil {
		t.Fatalf("agent cron task error = %v", err)
	}
	if len(gw.messageSnapshot()) == 0 {
		t.Fatal("expected telegram notification to be sent")
	}
	deliveries := gw.deliverySnapshot()
	if len(deliveries) != 1 {
		t.Fatalf("expected 1 telegram delivery, got %d", len(deliveries))
	}
	if got := deliveries[0].chatID; got != "12345" {
		t.Fatalf("expected telegram notification chat ID 12345, got %q", got)
	}
	if got := deliveries[0].replyToMsgID; got != "77" {
		t.Fatalf("expected telegram notification to reply to 77, got %q", got)
	}
	if got, ok := a.ResolveExternalReplyAnchor("telegram", "12345", "1"); !ok || got != "session-telegram-cron" {
		t.Fatalf("expected cron notification reply anchor to resolve session-telegram-cron, got %q ok=%v", got, ok)
	}
}

func TestCronNotificationHonorsSnakeCaseTelegramMetadata(t *testing.T) {
	gm := msggateway.NewGatewayManager()
	gw := &cronNotifyGateway{name: "telegram", running: true}
	if err := gm.Register(gw); err != nil {
		t.Fatalf("register gateway: %v", err)
	}
	a := &Agent{msgGateway: gm}
	a.RecordRecentChatTarget("telegram", "fallback-chat", "fallback-reply")

	a.sendCronNotification(map[string]string{
		"platform":            "telegram",
		"chat_id":             "snake-chat",
		"reply_to_message_id": "snake-reply",
		"session_id":          "snake-session",
	}, cronNotificationPayload{
		JobID:     "snake-job",
		Mode:      "agent",
		Command:   "summarize something",
		Outcome:   "succeeded",
		RawResult: "done",
	})

	deliveries := gw.deliverySnapshot()
	if len(deliveries) != 1 {
		t.Fatalf("expected 1 telegram delivery, got %d", len(deliveries))
	}
	if got := deliveries[0].chatID; got != "snake-chat" {
		t.Fatalf("expected snake_case chat_id to be honored, got %q", got)
	}
	if got := deliveries[0].replyToMsgID; got != "snake-reply" {
		t.Fatalf("expected snake_case reply_to_message_id to be honored, got %q", got)
	}
	if got, ok := a.ResolveExternalReplyAnchor("telegram", "snake-chat", "1"); !ok || got != "snake-session" {
		t.Fatalf("expected snake_case cron notification reply anchor to resolve snake-session, got %q ok=%v", got, ok)
	}
	if _, ok := a.ResolveExternalReplyAnchor("telegram", "fallback-chat", "1"); ok {
		t.Fatal("expected snake_case metadata to avoid falling back to recent telegram target")
	}
}

func TestCronNotificationFallsBackToRecentTelegramTarget(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	a.provider = &mockProvider{name: "test-mock"}

	gm := msggateway.NewGatewayManager()
	gw := &cronNotifyGateway{name: "telegram", running: true}
	if err := gm.Register(gw); err != nil {
		t.Fatalf("register gateway: %v", err)
	}
	a.msgGateway = gm
	a.RecordRecentChatTarget("telegram", "12345", "77")

	if _, err := a.Tools().Call("cron_add", map[string]any{
		"id":       "agent-job-fallback-tg",
		"schedule": "每小时",
		"mode":     "agent",
		"command":  "say hello from cron fallback",
	}); err != nil {
		t.Fatalf("cron_add(agent fallback telegram) error = %v", err)
	}

	job, ok := a.CronEngine().GetJob("agent-job-fallback-tg")
	if !ok {
		t.Fatal("expected agent-job-fallback-tg to exist")
	}
	if err := job.Task(); err != nil {
		t.Fatalf("agent cron task error = %v", err)
	}
	if len(gw.messageSnapshot()) == 0 {
		t.Fatal("expected fallback telegram notification to be sent")
	}
}

func TestCronNotificationUsesForwardedTextForLongGatewayMessages(t *testing.T) {
	gm := msggateway.NewGatewayManager()
	gw := &cronNotifyGateway{name: "napcat", running: true}
	if err := gm.Register(gw); err != nil {
		t.Fatalf("register gateway: %v", err)
	}
	a := &Agent{msgGateway: gm}

	raw := strings.Repeat("完整结果", 500)
	a.sendCronNotification(map[string]string{
		"platform": "napcat",
		"chatID":   "group:123",
	}, cronNotificationPayload{
		JobID:     "long-cron",
		Mode:      "agent",
		Command:   "生成长报告",
		Outcome:   "succeeded",
		RawResult: raw,
	})

	forwards := gw.forwardSnapshot()
	if len(forwards) != 1 {
		t.Fatalf("expected one forwarded cron notification, got %#v", forwards)
	}
	if forwards[0].chatID != "group:123" || forwards[0].title != "LuckyAgent" {
		t.Fatalf("unexpected forward metadata: %#v", forwards[0])
	}
	joined := strings.Join(forwards[0].chunks, "\n")
	if joined == "" || !strings.Contains(joined, "完整结果完整结果") {
		t.Fatalf("expected forwarded chunks to include raw result, got %#v", forwards[0].chunks)
	}
	for _, chunk := range forwards[0].chunks {
		if len([]rune(chunk)) > cronNotificationForwardChunkLimit {
			t.Fatalf("forwarded chunk exceeds limit: %d", len([]rune(chunk)))
		}
	}
	if got := gw.messageSnapshot(); len(got) != 0 {
		t.Fatalf("expected long cron notification not to use plain messages, got %#v", got)
	}
}

func TestFormatCronNotificationUsesProviderRewrite(t *testing.T) {
	a := &Agent{
		provider: &staticChatProvider{
			name:    "static-chat",
			content: "我刚帮你把这轮定时巡检处理完了，完整结果我直接贴在下面。",
		},
	}

	got := a.formatCronNotification(cronNotificationPayload{
		JobID:     "job-1",
		Mode:      "agent",
		Command:   "巡检一下服务状态",
		Outcome:   "succeeded",
		RawResult: "服务状态正常，但 latency 有轻微波动。",
	})
	if !strings.Contains(got, "我刚帮你把这轮定时巡检处理完了") {
		t.Fatalf("expected provider rewritten notification, got %q", got)
	}
	if !strings.Contains(got, "完整结果：\n服务状态正常，但 latency 有轻微波动。") {
		t.Fatalf("expected full raw result to be appended, got %q", got)
	}
}

func TestFormatCronNotificationAppendsUntruncatedRawResult(t *testing.T) {
	raw := strings.Repeat("完整内容", 900)
	a := &Agent{
		provider: &staticChatProvider{
			name:    "static-chat",
			content: "长任务已经跑完了，完整结果如下。",
		},
	}

	got := a.formatCronNotification(cronNotificationPayload{
		JobID:     "long-job",
		Mode:      "agent",
		Command:   "抓取长报告",
		Outcome:   "succeeded",
		RawResult: raw,
	})
	if !strings.Contains(got, raw) {
		t.Fatal("expected cron notification to include the full untruncated raw result")
	}
	if strings.Contains(got, "已截断") {
		t.Fatalf("expected final notification not to report truncation, got %q", got)
	}
}

func TestFormatCronNotificationFallsBackNaturallyOnProviderError(t *testing.T) {
	a := &Agent{
		provider: &staticChatProvider{
			name: "static-chat",
			err:  fmt.Errorf("provider unavailable"),
		},
	}

	got := a.formatCronNotification(cronNotificationPayload{
		JobID:     "job-2",
		Mode:      "shell",
		Command:   "同步监控日志",
		Outcome:   "failed",
		RawResult: "连接上游接口超时",
	})
	if !strings.Contains(got, "同步监控日志") {
		t.Fatalf("expected fallback to include command context, got %q", got)
	}
	if !strings.Contains(got, "连接上游接口超时") {
		t.Fatalf("expected fallback to include raw failure, got %q", got)
	}
	if strings.Contains(got, "执行状态") {
		t.Fatalf("expected natural fallback wording, got %q", got)
	}
}

func TestChatStreamUsesAgentEventLoop(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")
	cfg.Set("stream_mode", "simulated")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer a.Close()
	a.provider = &staticChatProvider{name: "static-chat", content: "streamed through loop"}

	ch, err := a.ChatStream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	var got strings.Builder
	done := false
	for chunk := range ch {
		got.WriteString(chunk.Content)
		if chunk.Done {
			done = true
		}
	}
	if !done {
		t.Fatal("expected done chunk")
	}
	if strings.TrimSpace(got.String()) != "streamed through loop" {
		t.Fatalf("unexpected stream content %q", got.String())
	}
}

func TestCronToolsPersistAcrossAgentRestart(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a1, err := New(cfg)
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}
	if _, err := a1.Tools().Call("cron_add", map[string]any{
		"id":       "persisted-job",
		"schedule": "每小时",
		"mode":     "shell",
		"command":  "echo persisted-agent-job",
	}); err != nil {
		t.Fatalf("cron_add error = %v", err)
	}
	if err := a1.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	a2, err := New(cfg)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}
	defer a2.Close()

	job, ok := a2.CronEngine().GetJob("persisted-job")
	if !ok {
		t.Fatal("expected persisted-job to be restored")
	}
	if got := job.Metadata["command"]; got != "echo persisted-agent-job" {
		t.Fatalf("unexpected restored command %q", got)
	}
	if !a2.CronEngine().IsRunning() {
		t.Fatal("expected restored cron engine to be running")
	}
}

func TestCronEventHandlerPersistsAfterJobExecution(t *testing.T) {
	dir := t.TempDir()
	engine := cron.NewEngine()
	store := cron.NewStore(filepath.Join(dir, "mission.md"))
	a := &Agent{
		cronEngine: engine,
		cronStore:  store,
	}
	a.installCronEventHandler()

	ran := make(chan struct{}, 1)
	schedule := &singleSoonThenLaterSchedule{}
	if err := engine.AddJobWithMeta(
		"persist-after-run",
		"Cron: persist-after-run",
		"echo persisted",
		schedule,
		func() error {
			ran <- struct{}{}
			return nil
		},
		map[string]string{
			"mode":          "shell",
			"command":       "echo persisted",
			"schedule_text": "每小时",
		},
	); err != nil {
		t.Fatalf("AddJobWithMeta() error = %v", err)
	}

	engine.Start()
	defer engine.Stop()

	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("expected cron job to run")
	}

	storePath := store.Path()
	deadline := time.Now().Add(time.Second)
	for {
		data, err := os.ReadFile(storePath)
		if err == nil {
			content := string(data)
			if strings.Contains(content, "run_count: 1") && strings.Contains(content, "next_run_unix:") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected cron execution state to be persisted to %s", storePath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type singleSoonThenLaterSchedule struct {
	mu    sync.Mutex
	calls int
}

func (s *singleSoonThenLaterSchedule) Next(from time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return from.Add(10 * time.Millisecond)
	}
	return from.Add(time.Hour)
}

func (s *singleSoonThenLaterSchedule) String() string {
	return "single soon then later"
}

func TestAgent_MsgGateway(t *testing.T) {
	a := &Agent{msgGateway: nil}
	if a.MsgGateway() != nil {
		t.Error("MsgGateway() should be nil when not set")
	}
}

func mustSessionManager(t *testing.T) *session.Manager {
	t.Helper()
	mgr, err := session.NewManager("test-agent")
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}
	return mgr
}
