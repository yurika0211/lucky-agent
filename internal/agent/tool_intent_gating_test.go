package agent

import (
	"testing"

	"github.com/yurika0211/luckyharness/internal/tool"
)

func TestApplyIntentToolGatingConceptHidesTools(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "解释 DCG 为什么用 log 折扣。")
	opts := a.buildLoopCallOptions("解释 DCG 为什么用 log 折扣。", loopCfg)

	if len(opts.Tools) != 0 {
		t.Fatalf("expected concept task to expose no tools, got %v", toolNamesFromSchemas(opts.Tools))
	}
	if opts.ToolChoice != "none" {
		t.Fatalf("expected tool choice none, got %#v", opts.ToolChoice)
	}
	assertDisabledTools(t, loopCfg.DisabledTools,
		"terminal", "file_write", "file_patch", "file_delete", "web_search",
		"web_fetch", "http_request", "sql_query", "rag_index", "image_generate",
		"text_to_speech", "remember",
	)
}

func TestApplyIntentToolGatingReadOnlyKeepsInspectionTools(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "看看 internal/agent 里工具路由入口在哪里。")
	opts := a.buildLoopCallOptions("看看 internal/agent 里工具路由入口在哪里。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "terminal", "file_read", "file_list")
	assertDisabledTools(t, loopCfg.DisabledTools,
		"file_write", "file_patch", "file_delete", "web_search", "web_fetch",
		"http_request", "remember", "rag_index", "image_generate", "text_to_speech",
	)
}

func TestApplyIntentToolGatingHonorsExplicitToolMention(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "不用工具解释一下，但必须调用 web_search。")
	opts := a.buildLoopCallOptions("不用工具解释一下，但必须调用 web_search。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "web_search")
	if containsString(loopCfg.DisabledTools, "web_search") {
		t.Fatalf("explicitly mentioned tool should remain enabled, disabled=%v", loopCfg.DisabledTools)
	}
	if len(visible) != 1 {
		t.Fatalf("expected only explicitly mentioned tool to remain visible, got %v", visible)
	}
}

func TestApplyIntentToolGatingCommitWithoutPushKeepsTerminalOnly(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "commit 当前暂存改动，但不要 push。")
	opts := a.buildLoopCallOptions("commit 当前暂存改动，但不要 push。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "terminal", "file_read", "file_list", "file_patch", "file_write")
	assertDisabledTools(t, loopCfg.DisabledTools, "web_search", "web_fetch", "http_request", "file_delete")
	if containsString(loopCfg.DisabledTools, "terminal") {
		t.Fatalf("terminal must remain available for git status/commit; disabled=%v", loopCfg.DisabledTools)
	}
}

func TestApplyIntentToolGatingDoesNotChangeUnknownIntent(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "继续")

	if len(loopCfg.DisabledTools) != 0 {
		t.Fatalf("unknown short intent should not auto-disable tools, got %v", loopCfg.DisabledTools)
	}
}

func TestApplyIntentToolGatingMetricExplanationDoesNotEnableMemory(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "讲解一下 Graph RecallLift 和图 RAG 是否需要向量召回。")
	opts := a.buildLoopCallOptions("讲解一下 Graph RecallLift 和图 RAG 是否需要向量召回。", loopCfg)

	if len(opts.Tools) != 0 {
		t.Fatalf("expected metric explanation to expose no tools, got %v", toolNamesFromSchemas(opts.Tools))
	}
	assertDisabledTools(t, loopCfg.DisabledTools, "recall", "rag_search", "web_search", "terminal")
}

func TestApplyIntentToolGatingOptimizationIntentKeepsEditTools(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "优化工具系统，完善 benchmark，并接入真实 agent。")
	opts := a.buildLoopCallOptions("优化工具系统，完善 benchmark，并接入真实 agent。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "terminal", "file_read", "file_list", "file_patch", "file_write")
	assertDisabledTools(t, loopCfg.DisabledTools, "web_search", "web_fetch", "http_request", "file_delete")
}

func agentWithIntentGateTools(t *testing.T) *Agent {
	t.Helper()
	reg := tool.NewRegistry()
	for _, name := range []string{
		"terminal", "file_read", "file_list", "file_write", "file_mkdir", "file_move",
		"file_patch", "file_delete", "web_search", "web_fetch", "http_request",
		"current_time", "calculate", "remember", "recall", "rag_search", "rag_index",
		"log_grep", "log_tail", "json_query", "yaml_query", "csv_query", "sql_query",
		"db_schema", "image_analyze", "image_generate", "text_to_speech", "skill_read",
		"skill_obsidian_run", "cron", "cron_add", "cron_list", "cron_status",
		"autonomy", "heartbeat_status", "heartbeat_trigger", "delegate_task",
		"task_status", "list_tasks",
	} {
		reg.Register(&tool.Tool{
			Name:        name,
			Description: name,
			Category:    tool.CatBuiltin,
			Parameters:  map[string]tool.Param{},
		})
	}
	return &Agent{tools: reg}
}

func toolNamesFromSchemas(tools []map[string]any) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if name := functionToolNameFromSchema(t); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func assertEnabledTools(t *testing.T, enabled []string, names ...string) {
	t.Helper()
	for _, name := range names {
		if !containsString(enabled, name) {
			t.Fatalf("expected %q to be visible in %v", name, enabled)
		}
	}
}

func assertDisabledTools(t *testing.T, disabled []string, names ...string) {
	t.Helper()
	for _, name := range names {
		if !containsString(disabled, name) {
			t.Fatalf("expected %q to be disabled in %v", name, disabled)
		}
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
