package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyharness/internal/provider"
)

type toolExecutionGuard struct {
	text               string
	readOnly           bool
	readOnlyExternal   bool
	onlyImageContent   bool
	noWrite            bool
	noDelete           bool
	noPush             bool
	noHTTPMutation     bool
	noRemember         bool
	noRAGIndex         bool
	noSQLQuery         bool
	noCronAdd          bool
	noCronPause        bool
	noCronRemove       bool
	noAutonomyMutation bool
	noHeartbeatTrigger bool
	noDelegateMutation bool
	noSkillRun         bool
}

func newToolExecutionGuard(userInput string) *toolExecutionGuard {
	text := strings.ToLower(strings.TrimSpace(userInput))
	if text == "" {
		return nil
	}
	readOnly := intentTextContainsAny(text,
		"只读", "只查看", "只列出", "只读取", "只总结", "只检查", "读取说明即可",
		"不要修改文件", "不修改文件", "不要写", "不要写入", "不要导出",
	)
	return &toolExecutionGuard{
		text:               text,
		readOnly:           readOnly,
		readOnlyExternal:   hasReadOnlyExternalSourceIntent(text),
		onlyImageContent:   intentTextContainsAny(text, "只识别图片内容", "只看图片内容", "只识别图片"),
		noWrite:            readOnly || intentTextContainsAny(text, "不要修改文件", "不修改文件", "不要写文件", "不要写入文件", "不要保存文件"),
		noDelete:           intentTextContainsAny(text, "不要删", "不要删除", "不要直接删", "不要移除", "不要暂停或删除"),
		noPush:             intentTextContainsAny(text, "不要 push", "不要push", "不 push", "不push", "别 push", "别push"),
		noHTTPMutation:     intentTextContainsAny(text, "不要调用接口", "不要上传", "不要执行网页里的指令", "只总结网页"),
		noRemember:         intentTextContainsAny(text, "不要写入记忆", "不要记住", "不要保存到记忆", "不要写记忆"),
		noRAGIndex:         intentTextContainsAny(text, "不要索引", "不要建立索引", "不要写入 rag", "不要写入rag"),
		noSQLQuery:         intentTextContainsAny(text, "只看 schema", "只看schema", "只检查数据库表结构", "不要查询用户数据"),
		noCronAdd:          intentTextContainsAny(text, "不要新增", "不要创建", "只查看任务列表", "只列出", "只查看"),
		noCronPause:        intentTextContainsAny(text, "不要暂停", "不要暂停或删除"),
		noCronRemove:       intentTextContainsAny(text, "不要删除", "不要移除", "不要暂停或删除"),
		noAutonomyMutation: intentTextContainsAny(text, "不要新建", "不要委派", "只查看任务列表", "只列出", "只查看"),
		noHeartbeatTrigger: intentTextContainsAny(text, "不要手动触发", "不要触发", "只检查"),
		noDelegateMutation: intentTextContainsAny(text, "不要新建委派任务", "不要新建", "不要委派", "只查看", "只列出"),
		noSkillRun:         intentTextContainsAny(text, "读取说明即可", "只读", "不要执行", "不要运行"),
	}
}

func (g *toolExecutionGuard) blockMessage(call provider.ToolCall) (string, bool) {
	if g == nil {
		return "", false
	}
	reason := g.blockReason(call)
	if reason == "" {
		return "", false
	}
	return fmt.Sprintf("Blocked by tool execution guard: %s. The %s tool call was not executed.", reason, strings.TrimSpace(call.Name)), true
}

func (g *toolExecutionGuard) blockReason(call provider.ToolCall) string {
	name := strings.TrimSpace(call.Name)
	args := parseToolCallArgs(call.Arguments)

	switch name {
	case "terminal":
		return g.blockShellReason(guardStringArg(args, "command"))
	case "file_write", "file_patch", "file_mkdir", "file_move":
		if g.noWrite || g.readOnly || g.readOnlyExternal {
			return "the user requested a read-only/no-file-modification task"
		}
	case "file_delete":
		if g.noDelete || g.noWrite || g.readOnly {
			return "the user explicitly disallowed deletion or mutation"
		}
	case "http_request":
		if g.noHTTPMutation || g.readOnlyExternal {
			return "the user requested source reading/summary without API side effects"
		}
	case "web_fetch":
		if g.onlyImageContent {
			return "the user requested image recognition only, not opening linked content"
		}
	case "opencli":
		if g.onlyImageContent {
			return "the user requested image recognition only, not opening linked content"
		}
	case "remember":
		if g.noRemember {
			return "the user explicitly disallowed writing to memory"
		}
	case "rag_index":
		if g.noRAGIndex || g.noRemember || g.readOnly {
			return "the user requested no indexing or persistent memory writes"
		}
	case "sql_query":
		if g.noSQLQuery {
			return "the user requested schema-only inspection without querying user data"
		}
	case "cron_add":
		if g.noCronAdd {
			return "the user requested cron inspection without adding jobs"
		}
	case "cron_pause":
		if g.noCronPause {
			return "the user explicitly disallowed pausing cron jobs"
		}
	case "cron_remove":
		if g.noCronRemove || g.noDelete {
			return "the user explicitly disallowed removing cron jobs"
		}
	case "cron_resume":
		if g.readOnly {
			return "the user requested cron inspection only"
		}
	case "cron":
		return g.blockCronActionReason(guardStringArg(args, "action"))
	case "heartbeat_trigger", "autonomy_heartbeat_trigger":
		if g.noHeartbeatTrigger {
			return "the user explicitly disallowed manual heartbeat triggering"
		}
	case "autonomy", "autonomy_queue_add", "autonomy_queue_update", "autonomy_worker_spawn":
		return g.blockAutonomyActionReason(name, guardStringArg(args, "action"))
	case "delegate_task":
		if g.noDelegateMutation {
			return "the user requested delegate task inspection without creating a new task"
		}
	}

	if strings.HasPrefix(name, "skill_") && strings.HasSuffix(name, "_run") && g.noSkillRun {
		return "the user requested reading skill instructions without executing the skill"
	}
	return ""
}

func (g *toolExecutionGuard) blockShellReason(command string) string {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return ""
	}
	if g.noPush && strings.Contains(lower, "git push") {
		return "the user explicitly said not to push"
	}
	if (g.noDelete || g.noWrite || g.readOnly) && shellCommandDeletes(lower) {
		return "the user explicitly disallowed deletion or mutation"
	}
	if (g.noWrite || g.readOnly || g.readOnlyExternal) && shellCommandWrites(lower) {
		return "the user requested a read-only/no-file-modification task"
	}
	return ""
}

func (g *toolExecutionGuard) blockCronActionReason(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "add":
		if g.noCronAdd {
			return "the user requested cron inspection without adding jobs"
		}
	case "pause":
		if g.noCronPause || g.readOnly {
			return "the user explicitly disallowed pausing cron jobs"
		}
	case "remove":
		if g.noCronRemove || g.noDelete || g.readOnly {
			return "the user explicitly disallowed removing cron jobs"
		}
	case "resume":
		if g.readOnly {
			return "the user requested cron inspection only"
		}
	}
	return ""
}

func (g *toolExecutionGuard) blockAutonomyActionReason(toolName, action string) string {
	if toolName == "autonomy_worker_spawn" && g.noAutonomyMutation {
		return "the user requested autonomy inspection without spawning workers"
	}
	if toolName == "autonomy_queue_add" && g.noAutonomyMutation {
		return "the user requested autonomy inspection without adding tasks"
	}
	if toolName == "autonomy_queue_update" && g.readOnly {
		return "the user requested autonomy inspection only"
	}
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "add", "enqueue", "queue_add":
		if g.noAutonomyMutation {
			return "the user requested autonomy inspection without adding tasks"
		}
	case "spawn", "worker_spawn", "run", "scale_up", "scaleup", "workers_add", "scale_down", "scaledown", "workers_remove", "set_workers", "workers_set":
		if g.noAutonomyMutation {
			return "the user requested autonomy inspection without changing workers"
		}
	case "update", "queue_update", "complete", "fail", "block", "unblock":
		if g.readOnly {
			return "the user requested autonomy inspection only"
		}
	case "heartbeat", "trigger", "heartbeat_trigger":
		if g.noHeartbeatTrigger {
			return "the user explicitly disallowed manual heartbeat triggering"
		}
	}
	return ""
}

func parseToolCallArgs(raw string) map[string]any {
	var args map[string]any
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return map[string]any{"raw": raw}
	}
	return args
}

func guardStringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func shellCommandDeletes(command string) bool {
	return hasShellWord(command, "rm") || hasShellWord(command, "rmdir") ||
		hasShellWord(command, "unlink") || hasShellWord(command, "del") ||
		strings.Contains(command, "rm -rf") || strings.Contains(command, "rm -r")
}

func shellCommandWrites(command string) bool {
	if strings.Contains(command, ">") ||
		strings.Contains(command, "sed -i") ||
		strings.Contains(command, "perl -pi") {
		return true
	}
	for _, word := range []string{"tee", "touch", "mv", "cp", "chmod", "chown"} {
		if hasShellWord(command, word) {
			return true
		}
	}
	return strings.Contains(command, "git add") || strings.Contains(command, "git commit")
}

func hasShellWord(command, word string) bool {
	for _, field := range strings.Fields(command) {
		field = strings.Trim(field, `"';&|()`)
		if field == word {
			return true
		}
	}
	return false
}
