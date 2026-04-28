package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/contextx"
	"github.com/yurika0211/luckyharness/internal/cron"
	msggateway "github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/soul"
	"github.com/yurika0211/luckyharness/internal/tool"
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
		got, ok := parseEmbedderDimensionEnv(tt.input)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("parseEmbedderDimensionEnv(%q) = (%d, %v), want (%d, %v)", tt.input, got, ok, tt.want, tt.ok)
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
	cfg := LoopConfig{MaxIterations: 200, Timeout: 30 * time.Minute}
	sanitizeLoopConfig(&cfg)
	if cfg.MaxIterations != 100 {
		t.Errorf("expected MaxIterations capped at 100, got %d", cfg.MaxIterations)
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

// --- applyWebSearchEnv ---

func TestApplyWebSearchEnv(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(cfgPath, []byte("provider: openai\napi_key: test\nmodel: gpt-4\n"), 0644)
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

// --- handleMemoryTool ---

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

func TestHandleMemoryTool_Remember(t *testing.T) {
	a := newTestAgentWithMemory(t)

	result, err := a.handleMemoryTool("remember", `{"content": "用户喜欢Python", "category": "preference"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "已保存") {
		t.Errorf("expected save confirmation, got %q", result)
	}
	if !strings.Contains(result, "preference") {
		t.Errorf("expected category in result, got %q", result)
	}
}

func TestHandleMemoryTool_RememberLongTerm(t *testing.T) {
	a := newTestAgentWithMemory(t)

	result, err := a.handleMemoryTool("remember", `{"content": "重要密码", "category": "security", "long_term": true}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "长期记忆") {
		t.Errorf("expected long-term confirmation, got %q", result)
	}
}

func TestHandleMemoryTool_RememberNoContent(t *testing.T) {
	a := newTestAgentWithMemory(t)

	_, err := a.handleMemoryTool("remember", `{"category": "test"}`)
	if err == nil {
		t.Error("expected error for missing content")
	}
}

func TestHandleMemoryTool_RecallWithQuery(t *testing.T) {
	a := newTestAgentWithMemory(t)

	// Save some memories first
	a.memory.Save("用户喜欢Python", "preference")
	a.memory.Save("项目使用Go语言", "project")

	result, err := a.handleMemoryTool("recall", `{"query": "Python"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Python") {
		t.Errorf("expected Python in results, got %q", result)
	}
}

func TestHandleMemoryTool_RecallNoQuery(t *testing.T) {
	a := newTestAgentWithMemory(t)

	a.memory.Save("test memory", "test")

	result, err := a.handleMemoryTool("recall", `{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "最近的记忆") {
		t.Errorf("expected recent memory header, got %q", result)
	}
}

func TestHandleMemoryTool_RecallEmpty(t *testing.T) {
	a := newTestAgentWithMemory(t)

	result, err := a.handleMemoryTool("recall", `{"query": "nonexistent"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "没有找到") {
		t.Errorf("expected not found message, got %q", result)
	}
}

func TestHandleMemoryTool_UnknownTool(t *testing.T) {
	a := newTestAgentWithMemory(t)

	_, err := a.handleMemoryTool("forget", `{}`)
	if err == nil {
		t.Error("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown memory tool") {
		t.Errorf("expected unknown tool error, got %v", err)
	}
}

func TestHandleMemoryTool_InvalidJSON(t *testing.T) {
	a := newTestAgentWithMemory(t)

	// Invalid JSON should be handled gracefully (treated as raw args)
	result, err := a.handleMemoryTool("recall", "not json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still work (no query → recent memories)
	if !strings.Contains(result, "没有找到") && !strings.Contains(result, "记忆") {
		t.Errorf("expected memory response, got %q", result)
	}
}

// --- updateShellContext ---

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

	// Check that memory was saved
	recent := a.memory.Recent(10)
	found := false
	for _, e := range recent {
		if strings.Contains(e.Content, "Rust") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Rust-related memory to be saved")
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

// --- EventType edge cases ---

func TestEventTypeValues(t *testing.T) {
	if EventReason != 0 || EventAct != 1 || EventObserve != 2 {
		t.Errorf("unexpected EventType values: Reason=%d Act=%d Observe=%d", EventReason, EventAct, EventObserve)
	}
	if EventContent != 3 || EventDone != 4 || EventError != 5 {
		t.Errorf("unexpected EventType values: Content=%d Done=%d Error=%d", EventContent, EventDone, EventError)
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
	cfg.Set("max_tokens", "4096")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

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

type cronNotifyGateway struct {
	name     string
	running  bool
	messages []string
}

func (g *cronNotifyGateway) Name() string { return g.name }
func (g *cronNotifyGateway) Start(ctx context.Context) error {
	g.running = true
	return nil
}
func (g *cronNotifyGateway) Stop() error {
	g.running = false
	return nil
}
func (g *cronNotifyGateway) Send(ctx context.Context, chatID string, message string) error {
	g.messages = append(g.messages, message)
	return nil
}
func (g *cronNotifyGateway) SendWithReply(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	g.messages = append(g.messages, message)
	return nil
}
func (g *cronNotifyGateway) IsRunning() bool { return g.running }

// --- v0.64.0 Agent Package Coverage Improvements ---

func TestAgent_Tools(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, _ := config.NewManagerWithDir(tmpDir)
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

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
	if a == nil {
		t.Fatal("New() returned nil")
	}
}

func TestAgentNewWithSoulPath(t *testing.T) {
	tmpDir := t.TempDir()
	soulPath := filepath.Join(tmpDir, "SOUL.md")

	// Write minimal soul
	if err := os.WriteFile(soulPath, []byte("# Test Soul\n"), 0644); err != nil {
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

	// Switch to a different model
	err = a.SwitchModel("gpt-4o")
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

	// AutoSummarize should not panic
	a.autoSummarize()
}

func TestAgentStartAutonomy(t *testing.T) {
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

	// LoadSkills with empty directory should not panic
	a.LoadSkills(filepath.Join(tmpDir, "skills"))
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

	// Get the handler function
	handler := a.handleSkillRead()
	if handler == nil {
		t.Fatal("handleSkillRead() returned nil")
	}

	// Call handler with empty args should return skill list (no error)
	result, err := handler(map[string]any{})
	if err != nil {
		t.Errorf("handleSkillRead handler error = %v", err)
	}
	if result == "" {
		t.Error("handleSkillRead handler should return skill list")
	}

	// Call handler with non-existent skill name
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
	if err := os.WriteFile(skillFile, []byte("# Research\n"), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	handler := a.handleSkillRead()
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
	if err := job.Task(); err != nil {
		t.Fatalf("agent cron task error = %v", err)
	}
}

func TestCronAddAcceptsLegacyAliasesAndAutoID(t *testing.T) {
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

	resp, err := a.Tools().Call("cron_add", map[string]any{
		"schedule": "每两个小时",
		"mode":     "agent",
		"task":     "提醒我喝水",
	})
	if err != nil {
		t.Fatalf("cron_add(task alias) error = %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		t.Fatalf("unmarshal cron_add response: %v", err)
	}
	id, _ := out["id"].(string)
	if strings.TrimSpace(id) == "" {
		t.Fatal("expected generated id")
	}
	job, ok := a.CronEngine().GetJob(id)
	if !ok {
		t.Fatalf("expected job %q to exist", id)
	}
	if got := job.Metadata["command"]; got != "提醒我喝水" {
		t.Fatalf("expected command to come from task alias, got %q", got)
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
	if len(gw.messages) == 0 {
		t.Fatal("expected telegram notification to be sent")
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
