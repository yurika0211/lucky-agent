package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/tool"
)

func TestLoopStateString(t *testing.T) {
	tests := []struct {
		state    LoopState
		expected string
	}{
		{StateReason, "Reason"},
		{StateAct, "Act"},
		{StateObserve, "Observe"},
		{StateDone, "Done"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.expected {
			t.Errorf("LoopState(%d).String() = %q, want %q", tt.state, got, tt.expected)
		}
	}
}

func TestDefaultLoopConfig(t *testing.T) {
	cfg := DefaultLoopConfig()
	if cfg.MaxIterations != 10 {
		t.Errorf("expected 10 iterations, got %d", cfg.MaxIterations)
	}
	if cfg.AutoApprove != false {
		t.Error("expected AutoApprove false by default")
	}
	if cfg.RepeatToolCallLimit != 3 {
		t.Errorf("expected repeat limit 3, got %d", cfg.RepeatToolCallLimit)
	}
	if cfg.ToolOnlyIterationLimit != 3 {
		t.Errorf("expected tool-only iteration limit 3, got %d", cfg.ToolOnlyIterationLimit)
	}
	if cfg.DuplicateFetchLimit != 1 {
		t.Errorf("expected duplicate fetch limit 1, got %d", cfg.DuplicateFetchLimit)
	}
}

func TestFilterFunctionTools(t *testing.T) {
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "autonomy"}},
		{"type": "function", "function": map[string]any{"name": "terminal"}},
	}

	got := filterFunctionTools(tools, []string{"autonomy"})
	if len(got) != 1 {
		t.Fatalf("expected one tool after filtering, got %d", len(got))
	}
	if functionToolNameFromSchema(got[0]) != "terminal" {
		t.Fatalf("expected terminal tool to remain, got %#v", got[0])
	}
}

func TestNormalizeToolChoiceForToolsDropsUnavailableForcedTool(t *testing.T) {
	choice := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "skill_read",
		},
	}
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "web_search"}},
	}

	if got := normalizeToolChoiceForTools(choice, tools); got != "auto" {
		t.Fatalf("expected auto after forced tool is unavailable, got %#v", got)
	}
	if got := normalizeToolChoiceForTools(choice, nil); got != "none" {
		t.Fatalf("expected none without tools, got %#v", got)
	}
}

func TestApplySimpleTaskLoopTuningDefaults(t *testing.T) {
	loopCfg := LoopConfig{
		MaxIterations:          10,
		Timeout:                60 * time.Second,
		RepeatToolCallLimit:    3,
		ToolOnlyIterationLimit: 3,
	}

	applySimpleTaskLoopTuning(&loopCfg, "check workspace files", config.AgentLoopConfig{})

	if loopCfg.MaxIterations != 3 {
		t.Fatalf("expected max iterations to be reduced to 3, got %d", loopCfg.MaxIterations)
	}
	if loopCfg.Timeout != 25*time.Second {
		t.Fatalf("expected timeout to be reduced to 25s, got %s", loopCfg.Timeout)
	}
	if loopCfg.RepeatToolCallLimit != 2 {
		t.Fatalf("expected repeat tool call limit to be reduced to 2, got %d", loopCfg.RepeatToolCallLimit)
	}
	if loopCfg.ToolOnlyIterationLimit != 2 {
		t.Fatalf("expected tool-only iteration limit to be reduced to 2, got %d", loopCfg.ToolOnlyIterationLimit)
	}
}

func TestApplySimpleTaskLoopTuningConfigurable(t *testing.T) {
	loopCfg := LoopConfig{
		MaxIterations:          10,
		Timeout:                60 * time.Second,
		RepeatToolCallLimit:    8,
		ToolOnlyIterationLimit: 7,
	}
	agentCfg := config.AgentLoopConfig{
		SimpleLocalInspection: config.SimpleLocalInspectionConfig{
			MaxIterations:          5,
			TimeoutSeconds:         12,
			RepeatToolCallLimit:    4,
			ToolOnlyIterationLimit: 6,
		},
	}

	applySimpleTaskLoopTuning(&loopCfg, "check workspace files", agentCfg)

	if loopCfg.MaxIterations != 5 {
		t.Fatalf("expected max iterations 5, got %d", loopCfg.MaxIterations)
	}
	if loopCfg.Timeout != 12*time.Second {
		t.Fatalf("expected timeout 12s, got %s", loopCfg.Timeout)
	}
	if loopCfg.RepeatToolCallLimit != 4 {
		t.Fatalf("expected repeat tool call limit 4, got %d", loopCfg.RepeatToolCallLimit)
	}
	if loopCfg.ToolOnlyIterationLimit != 6 {
		t.Fatalf("expected tool-only iteration limit 6, got %d", loopCfg.ToolOnlyIterationLimit)
	}
}

func TestExtractRequiredToolNames(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "file_read", Enabled: true})
	reg.Register(&tool.Tool{Name: "current_time", Enabled: true})
	reg.Register(&tool.Tool{Name: "terminal", Enabled: true})

	a := &Agent{tools: reg}
	got := a.extractRequiredToolNames("请必须调用 file_read 和 current_time，最后不要用 terminal")

	if len(got) != 3 {
		t.Fatalf("expected 3 required tools, got %d (%v)", len(got), got)
	}
	if got[0] != "file_read" || got[1] != "current_time" || got[2] != "terminal" {
		t.Fatalf("unexpected order: %v", got)
	}
}

func TestExtractRequiredToolNamesIgnoresDomainMentions(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "cron", Enabled: true})
	reg.Register(&tool.Tool{Name: "rag_search", Enabled: true})

	a := &Agent{tools: reg}
	if got := a.extractRequiredToolNames("列出 cron 任务，但不要暂停或删除任何任务。"); len(got) != 0 {
		t.Fatalf("expected cron domain mention not to force a tool, got %v", got)
	}
	if got := a.extractRequiredToolNames("必须调用 rag_search 搜索记忆"); len(got) != 1 || got[0] != "rag_search" {
		t.Fatalf("expected explicit rag_search mention, got %v", got)
	}
}

func TestShouldForceSearchSynthesis(t *testing.T) {
	if shouldForceSearchSynthesis(1, 2) {
		t.Fatal("should not force synthesis with insufficient evidence")
	}
	if shouldForceSearchSynthesis(2, 1) {
		t.Fatal("should not force synthesis with insufficient tool-only iterations")
	}
	if !shouldForceSearchSynthesis(2, 2) {
		t.Fatal("expected synthesis to be forced")
	}
}

func TestIsUsefulSearchEvidence(t *testing.T) {
	if !isUsefulSearchEvidence("web_search", "Results for: 四川大学 食堂\n\n1. Test") {
		t.Fatal("expected non-empty search results to count as evidence")
	}
	if isUsefulSearchEvidence("web_search", "No results found for '四川大学 食堂' (all search sources failed)") {
		t.Fatal("expected no-results output not to count as evidence")
	}
	if isUsefulSearchEvidence("terminal", "Results for: 四川大学 食堂") {
		t.Fatal("non-search tools should not count as search evidence")
	}
}

func TestCompactToolResultForContext(t *testing.T) {
	long := "Results for: test\n\n" + strings.Repeat("x", 5000) + "TAIL-EVIDENCE"
	got := compactToolResultForContext("web_search", long)
	if len(got) >= len(long) {
		t.Fatal("expected web_search result to be compacted")
	}
	if !strings.Contains(got, "middle omitted for context") {
		t.Fatal("expected middle omission marker")
	}
	if !strings.Contains(got, "Results for: test") {
		t.Fatal("expected head of result to be preserved")
	}
	if !strings.Contains(got, "TAIL-EVIDENCE") {
		t.Fatal("expected tail of result to be preserved")
	}
}

func TestNormalizedToolTarget(t *testing.T) {
	args := `{"url":"https://Example.com/path#frag","max_chars":5000}`
	got := normalizedToolTarget("web_fetch", args)
	if got != "https://example.com/path" {
		t.Fatalf("unexpected normalized target: %q", got)
	}
	if normalizedToolTarget("web_search", args) != "" {
		t.Fatal("non-web_fetch tool should not have target")
	}
}

func TestExecuteToolMaybeDedupSkipsDuplicateFetch(t *testing.T) {
	a := &Agent{}
	repeats := map[string]int{"https://example.com/path": 2}
	last := map[string]string{"https://example.com/path": "Fetched content"}
	out, err := a.executeToolMaybeDedup("web_fetch", `{"url":"https://example.com/path"}`, true, nil, repeats, last, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Skipped duplicate web_fetch") {
		t.Fatalf("expected duplicate skip message, got %q", out)
	}
	if !strings.Contains(out, "Fetched content") {
		t.Fatalf("expected cached content to be reused, got %q", out)
	}
}

func TestToolCallSignatureCanonicalizesArguments(t *testing.T) {
	sig1 := toolCallSignature("web_fetch", `{"url":"https://example.com","max_chars":500}`)
	sig2 := toolCallSignature("web_fetch", `{"max_chars":500,"url":"https://example.com"}`)
	if sig1 != sig2 {
		t.Fatalf("expected canonical signatures to match, got %q vs %q", sig1, sig2)
	}
}

func TestBuildFinalAnswerRAGDocument(t *testing.T) {
	source, title, content, ok := buildFinalAnswerRAGDocument(
		"请总结一下 LuckyHarness 的 RAG 设计",
		"结论：当前已经有 SQLite 持久化，但资料沉淀链路还不统一。",
	)
	if !ok {
		t.Fatal("expected document to be built")
	}
	if !strings.HasPrefix(source, "conversation/final/") {
		t.Fatalf("expected conversation final source, got %q", source)
	}
	if title != "请总结一下 LuckyHarness 的 RAG 设计" {
		t.Fatalf("unexpected title: %q", title)
	}
	if !strings.Contains(content, "Final Answer:\n结论：当前已经有 SQLite 持久化") {
		t.Fatalf("expected final answer content, got %q", content)
	}
	if !strings.Contains(content, "User Request:\n请总结一下 LuckyHarness 的 RAG 设计") {
		t.Fatalf("expected user request context, got %q", content)
	}
}

func TestBuildFinalAnswerRAGDocumentSkipsEmptyAnswer(t *testing.T) {
	source, title, content, ok := buildFinalAnswerRAGDocument("hello", "   ")
	if ok {
		t.Fatalf("expected empty answer to be skipped, got source=%q title=%q content=%q", source, title, content)
	}
}

func TestSaveFinalAnswerDocument(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir() error = %v", err)
	}

	a := &Agent{cfg: cfg}
	err = a.saveFinalAnswerDocument(
		"session-123",
		"请总结一下当前项目的持久化设计",
		"结论：当前已经有 session、memory 和 RAG 持久化。",
	)
	if err != nil {
		t.Fatalf("saveFinalAnswerDocument() error = %v", err)
	}

	dir := filepath.Join(cfg.HomeDir(), "knowledge", "final_answers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 saved document, got %d", len(entries))
	}

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "# 请总结一下当前项目的持久化设计") {
		t.Fatalf("expected title in document, got %q", text)
	}
	if !strings.Contains(text, "## Final Answer") {
		t.Fatalf("expected final answer section, got %q", text)
	}
	if !strings.Contains(text, "## User Request") {
		t.Fatalf("expected user request section, got %q", text)
	}
	if !strings.Contains(text, "- Session ID: session-123") {
		t.Fatalf("expected session id in document, got %q", text)
	}
}

func TestUpdateShellContextUsesSessionCwdAndEnv(t *testing.T) {
	baseDir := t.TempDir()
	subDir := filepath.Join(baseDir, "child")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	sess := session.NewSession("test", t.TempDir())
	sess.SetCwd(baseDir)
	sess.SetEnv("OLD_KEY", "old")

	a := &Agent{}
	a.updateShellContext(sess, `cd child && export GREETING="hello world" && unset OLD_KEY`, "")

	if got := sess.GetCwd(); got != subDir {
		t.Fatalf("expected cwd %q, got %q", subDir, got)
	}
	env := sess.GetEnv()
	if env["GREETING"] != "hello world" {
		t.Fatalf("expected GREETING to be unquoted, got %q", env["GREETING"])
	}
	if _, ok := env["OLD_KEY"]; ok {
		t.Fatal("expected OLD_KEY to be removed")
	}
}
