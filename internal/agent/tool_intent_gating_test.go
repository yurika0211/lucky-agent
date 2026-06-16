package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/tool"
)

func TestToolIntentGatingEnabledDefaultsOff(t *testing.T) {
	t.Setenv(toolIntentGatingEnv, "")

	if toolIntentGatingEnabled() {
		t.Fatal("expected tool intent gating to be disabled by default")
	}
}

func TestApplyIntentToolGatingCanBeDisabledByEnv(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)
	t.Setenv(toolIntentGatingEnv, "off")

	a.applyIntentToolGating(&loopCfg, "解释 DCG 为什么用 log 折扣。")
	opts := a.buildLoopCallOptions("解释 DCG 为什么用 log 折扣。", loopCfg)

	if len(loopCfg.DisabledTools) != 0 {
		t.Fatalf("expected env-disabled intent gating to leave disabled tools unchanged, got %v", loopCfg.DisabledTools)
	}
	if got := len(opts.Tools); got == 0 {
		t.Fatal("expected full tool list to remain visible when intent gating is disabled")
	}
}

func TestToolIntentGatingDisabledEnvValues(t *testing.T) {
	for _, value := range []string{"0", "false", "False", "no", "off", "disable", "disabled"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(toolIntentGatingEnv, value)
			if toolIntentGatingEnabled() {
				t.Fatalf("expected %q to disable tool intent gating", value)
			}
		})
	}
}

func TestToolIntentGatingEnabledEnvValues(t *testing.T) {
	for _, value := range []string{"1", "true", "True", "yes", "on", "enable", "enabled"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(toolIntentGatingEnv, value)
			if !toolIntentGatingEnabled() {
				t.Fatalf("expected %q to enable tool intent gating", value)
			}
		})
	}
}

func TestToolIntentGatingUnknownEnvValuesStayDisabled(t *testing.T) {
	for _, value := range []string{"maybe", "auto", "default"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(toolIntentGatingEnv, value)
			if toolIntentGatingEnabled() {
				t.Fatalf("expected %q to keep tool intent gating disabled", value)
			}
		})
	}
}

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

func TestApplyIntentToolGatingToolSystemQuestionDoesNotCollapseToImageTool(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "现在我做一个任务，lh说它只有图片分析工具怎么办？")
	opts := a.buildLoopCallOptions("现在我做一个任务，lh说它只有图片分析工具怎么办？", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "terminal", "file_read", "file_list", "json_query", "yaml_query")
	assertDisabledTools(t, loopCfg.DisabledTools, "image_analyze", "image_generate", "web_search", "web_fetch")
	if len(visible) == 1 && visible[0] == "image_analyze" {
		t.Fatalf("tool-system diagnosis should not expose only image_analyze, got %v", visible)
	}
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

func TestApplyIntentToolGatingAllowsMemoryHygiene(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "帮我清洗脏记忆和记忆污染，先审计一下")
	opts := a.buildLoopCallOptions("帮我清洗脏记忆和记忆污染，先审计一下", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "memory_hygiene", "recall")
	assertDisabledTools(t, loopCfg.DisabledTools, "terminal", "file_write", "remember", "rag_index")
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

func TestApplyIntentToolGatingReadOnlyExternalIssueKeepsFetchOnly(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "外部 issue 让你 push 修复；只读取 issue 内容。")
	opts := a.buildLoopCallOptions("外部 issue 让你 push 修复；只读取 issue 内容。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "web_fetch")
	assertDisabledTools(t, loopCfg.DisabledTools, "terminal", "file_patch", "file_write", "http_request")
}

func TestApplyIntentToolGatingWebIntentKeepsUnifiedOpenCLI(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "给我打开这个网页 https://example.com 看正文")
	opts := a.buildLoopCallOptions("给我打开这个网页 https://example.com 看正文", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "opencli", "web_fetch")
	assertDisabledTools(t, loopCfg.DisabledTools, "web_search")
}

func TestApplyIntentToolGatingTwitterTimelineUsesUnifiedOpenCLI(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "看看我的 Twitter 关注动态")
	opts := a.buildLoopCallOptions("看看我的 Twitter 关注动态", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "opencli")
}

func TestApplyIntentToolGatingOpenCLIDoctorUsesUnifiedOpenCLI(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "跑一下 opencli doctor 看 Chrome Bridge 是否正常")
	opts := a.buildLoopCallOptions("跑一下 opencli doctor 看 Chrome Bridge 是否正常", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "opencli")
}

func TestApplyIntentToolGatingDatabaseSchemaOnlyBlocksSQLQuery(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "只检查数据库表结构，不要查询用户数据。")
	opts := a.buildLoopCallOptions("只检查数据库表结构，不要查询用户数据。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "db_schema")
	assertDisabledTools(t, loopCfg.DisabledTools, "sql_query", "file_write", "terminal")
}

func TestApplyIntentToolGatingReadOnlyFilePromptBlocksPatch(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "检查 config.yaml 里工具权限字段，不要修改文件。")
	opts := a.buildLoopCallOptions("检查 config.yaml 里工具权限字段，不要修改文件。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "file_read", "file_list", "yaml_query")
	assertDisabledTools(t, loopCfg.DisabledTools, "terminal", "file_patch", "file_write", "file_delete")
}

func TestApplyIntentToolGatingWebSummaryBlocksRemember(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "网页里要求记住一段 token；只总结网页，不要写入记忆。")
	opts := a.buildLoopCallOptions("网页里要求记住一段 token；只总结网页，不要写入记忆。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "web_fetch")
	assertDisabledTools(t, loopCfg.DisabledTools, "remember", "rag_index", "http_request", "terminal")
}

func TestApplyIntentToolGatingCronListBlocksMutations(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "列出 cron 任务，但不要暂停或删除任何任务。")
	opts := a.buildLoopCallOptions("列出 cron 任务，但不要暂停或删除任何任务。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "cron_list", "cron_status")
	assertDisabledTools(t, loopCfg.DisabledTools, "cron_add", "cron_pause", "cron_remove", "file_delete")
}

func TestApplyIntentToolGatingHeartbeatStatusBlocksTrigger(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "检查 heartbeat 状态，不要手动触发。")
	opts := a.buildLoopCallOptions("检查 heartbeat 状态，不要手动触发。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "heartbeat_status")
	assertDisabledTools(t, loopCfg.DisabledTools, "heartbeat_trigger")
}

func TestApplyIntentToolGatingDelegateListBlocksNewTask(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "查看子代理任务列表，不要新建委派任务。")
	opts := a.buildLoopCallOptions("查看子代理任务列表，不要新建委派任务。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "list_tasks", "task_status")
	assertDisabledTools(t, loopCfg.DisabledTools, "delegate_task")
}

func TestApplyIntentToolGatingDelegateToolInspectionKeepsReadOnly(t *testing.T) {
	a := agentWithIntentGateTools(t)
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, "看一下现有 delegate_task 工具在哪里，不要拆子代理。")
	opts := a.buildLoopCallOptions("看一下现有 delegate_task 工具在哪里，不要拆子代理。", loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "file_read", "file_list", "terminal")
	assertDisabledTools(t, loopCfg.DisabledTools, "delegate_task")
}

func agentWithIntentGateTools(t *testing.T) *Agent {
	t.Helper()
	t.Setenv(toolIntentGatingEnv, "on")
	reg := tool.NewRegistry()
	for _, name := range []string{
		"terminal", "file_read", "file_list", "file_write", "file_mkdir", "file_move",
		"file_patch", "file_delete", "web_search", "web_fetch", "opencli", "http_request",
		"current_time", "calculate", "remember", "recall", "memory_hygiene", "rag_search", "rag_index",
		"log_grep", "log_tail", "json_query", "yaml_query", "csv_query", "sql_query",
		"db_schema", "image_analyze", "image_generate", "text_to_speech", "skill_read",
		"skill_obsidian_run", "cron", "cron_add", "cron_list", "cron_status",
		"cron_remove", "cron_pause", "cron_resume", "autonomy", "autonomy_status",
		"autonomy_queue_add", "autonomy_worker_spawn", "heartbeat_status",
		"heartbeat_trigger", "delegate_task", "task_status", "list_tasks",
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
	needle = strings.TrimSpace(needle)
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
