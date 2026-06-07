package main

import (
	"sort"
	"strings"
)

type strategyResult struct {
	NeedToolProb float64
	VisibleTools []string
	Calls        []toolCall
	Packed       bool
}

func runStrategy(cfg benchConfig, task benchTask, tools map[string]toolSpec, ops map[string]operationSpec) strategyResult {
	variant := normalizeVariant(cfg.Variant)
	prob := estimateNeedToolProbability(variant, task)
	planned := planOperations(variant, task)
	visible := visibleToolsForVariant(variant, task, planned, tools, ops)
	planned = filterCallsByVisible(planned, visible, ops)
	if prob < cfg.NeedThreshold {
		planned = nil
	}
	return strategyResult{
		NeedToolProb: prob,
		VisibleTools: visible,
		Calls:        callsFromOperations(planned, ops),
		Packed:       variant == "packed-results" || variant == "a4",
	}
}

func normalizeVariant(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = strings.ReplaceAll(v, "_", "-")
	switch v {
	case "", "manual", "baseline", "a0":
		return "baseline"
	case "static", "static-slim", "a1":
		return "static-slim"
	case "intent", "intent-gated", "a2":
		return "intent-gated"
	case "guarded", "guarded-intent", "execution-guard", "a2-guard":
		return "guarded-intent"
	case "risk", "risk-aware", "a3":
		return "risk-aware"
	case "packed", "packed-results", "a4":
		return "packed-results"
	default:
		return v
	}
}

func estimateNeedToolProbability(variant string, task benchTask) float64 {
	prompt := strings.ToLower(task.Prompt)
	score := 0.22
	toolEvidence := 0.0
	if containsAny(prompt, "internal/", "cmd/", "docs/", "readme", "config.yaml", ".json", ".jsonl", ".yaml", ".csv", "jsonl", "markdown", "url", "代码", "源码", "文件", "目录", "仓库", "repo", "benchmark", "system_prompt") {
		toolEvidence += 0.36
	}
	if containsAny(prompt, "看看", "找一下", "查一下", "查询", "打开", "确认", "检查", "读取", "参考", "搜索", "列出", "状态", "跑测试", "测试", "单测", "修一下", "补一个", "新增", "写入", "重命名", "git status", "commit", "push", "删除", "日志", "网页", "url", "issue", "官方说明", "obsidian", "记住", "记忆", "rag", "截图", "图片", "二维码", "示意图", "语音", "数据库", "订单表", "sql", "schema", "表结构", "csv", "jsonl", "cron", "定时", "提醒任务", "autonomy", "heartbeat", "子代理", "委派") {
		toolEvidence += 0.26
	}
	if containsAny(prompt, "网页", "日志", "obsidian", "rm -rf", "api key", "读取说明", "分析原因", "只总结", "不要执行", "不要运行", "不要新增", "不要暂停", "不要删除", "不要写入", "不要导出", "不要索引", "只看", "只查看", "只读取", "只识别", "只统计") {
		toolEvidence += 0.20
	}
	if containsAny(prompt, "修一下", "补一个", "单测", "测试", "跑相关", "prompt fingerprint", "tool router", "修改", "创建", "生成", "保存", "重命名", "写入", "建立", "委派") {
		toolEvidence += 0.22
	}
	if containsAny(prompt, "当前", "今天", "现在", "北京时间", "最新") {
		toolEvidence += 0.28
	}
	if containsAny(prompt, "计算") {
		toolEvidence += 0.42
	}
	if containsAny(prompt,
		"rag 搜索", "用 rag", "url", "外部 issue", "issue 内容", "截图", "二维码",
		"csv", "jsonl", "schema", "表结构", "sql", "订单表", "cron", "提醒任务",
		"heartbeat", "autonomy", "子代理", "委派",
	) {
		toolEvidence += 0.22
	}
	score += toolEvidence
	if containsAny(prompt, "解释", "讲解", "说明", "是什么意思") && toolEvidence < 0.30 {
		score -= 0.12
	}

	switch variant {
	case "baseline":
		if containsAny(prompt, "公式", "dcg", "log", "归一化", "route risk", "rag", "图 rag", "recalllift", "概率", "协方差") {
			score += 0.52
		}
		if containsAny(prompt, "不用工具", "不需要查代码") {
			score -= 0.14
		}
	case "static-slim":
		if containsAny(prompt, "解释", "讲解", "说明", "是什么意思", "不用工具", "不需要查代码") {
			score -= 0.24
		}
	case "intent-gated", "guarded-intent", "risk-aware", "packed-results":
		if containsAny(prompt, "解释", "讲解", "说明", "是什么意思", "不用工具", "不需要查代码") && toolEvidence < 0.30 {
			score -= 0.34
		}
		if containsAny(prompt, "不用工具") {
			score -= 0.20
		}
	}
	return clamp(score, 0.02, 0.98)
}

func planOperations(variant string, task benchTask) []string {
	prompt := strings.ToLower(task.Prompt)
	var ops []string
	add := func(names ...string) {
		ops = append(ops, names...)
	}

	switch {
	case containsAny(prompt, "git status"):
		add("git_status")
	case containsAny(prompt, "计算"):
		add("calculate")
	case containsAny(prompt, "北京时间", "当前时间"):
		add("current_time")
	}

	if containsAny(prompt, "internal/", "cmd/", "internal/tool", "system_prompt", "工具路由", "源码", "代码") {
		add("repo_search", "read_file")
	}
	if containsAny(prompt, "docs/", "docs/reports", "找一下") {
		add("list_files")
		if containsAny(prompt, "找一下", "读取", "检查", "确认", "看看") && !containsAny(prompt, "创建", "写入", "生成", "输出") {
			add("read_file")
		}
	}
	if containsAny(prompt, "cmd 文件夹", "cmd目录") && containsAny(prompt, "benchmark", "结构", "参考") {
		add("list_files", "read_file")
	}
	if containsAny(prompt, "benchmark") && containsAny(prompt, "增加", "修", "修一下", "补", "补一个") {
		add("repo_search", "read_file", "apply_patch", "run_tests")
	}
	if containsAny(prompt, "修一下", "修复", "补一个", "补一下", "修改") &&
		containsAny(prompt, "tool router", "prompt fingerprint", "agent", "工具", "代码", "单测", "go 测试") {
		if !containsAny(prompt, "不要修改", "不修改", "只读", "只查看", "只检查") {
			add("repo_search", "read_file", "apply_patch")
		}
	}
	if containsAny(prompt, "跑测试", "go 测试", "对应包测试", "单测", "测试") {
		add("run_tests")
	}
	if containsAny(prompt, "报告", "汇总", "markdown") && containsAny(prompt, "输出", "写", "写入", "生成", "保存") {
		add("write_file")
		if containsAny(prompt, "确认文件存在", "确认文件", "docs/", "docs/reports") && containsAny(prompt, "确认", "创建", "目录") {
			add("list_files")
		}
	}
	if containsAny(prompt, "创建", "新建") && containsAny(prompt, "目录", "文件夹", "docs/reports") {
		add("make_dir")
	}
	if containsAny(prompt, "重命名", "移动") {
		add("list_files", "move_file")
	}
	if containsAny(prompt, "readme") && containsAny(prompt, "读取", "检查", "确认") {
		add("read_file")
	}
	if containsAny(prompt, ".json", "jsonl", "json") && containsAny(prompt, "查询", "summary", "输出") {
		add("json_query")
	}
	if containsAny(prompt, "config.yaml", ".yaml", ".yml", "yaml") {
		if containsAny(prompt, "修改", "写", "编辑") {
			add("read_file", "yaml_query", "apply_patch")
		} else {
			add("yaml_query", "read_file")
		}
	}
	if containsAny(prompt, "csv") {
		add("csv_query")
	}

	if containsAny(prompt, "commit") {
		if containsAny(prompt, "暂存") && !containsAny(prompt, "指定文件") {
			add("git_status", "git_commit")
		} else {
			add("git_status", "git_add", "git_commit")
		}
	}
	if containsAny(prompt, "push") {
		if variant == "baseline" || !containsAny(prompt, "不要 push", "不 push", "不要push", "只读取 issue", "只读取 issue 内容", "只读取内容") {
			add("git_push")
		}
	}
	if containsAny(prompt, "删除", "删") {
		if containsAny(prompt, "先列出", "包含什么", "临时目录", "目录") {
			add("list_files")
		}
		if variant == "baseline" || !containsAny(prompt, "不要直接删", "不要执行", "不要运行", "不要删", "不要删除", "不要暂停或删除") {
			add("destructive_shell")
		}
	}
	if containsAny(prompt, "日志", "log") {
		if variant == "baseline" {
			add("repo_search")
		} else {
			add("log_grep")
			if containsAny(prompt, "tail", "尾部", "最后", "实时", "持续") {
				add("log_tail")
			}
		}
	}
	if containsAny(prompt, "rm -rf") {
		if variant == "baseline" {
			add("destructive_shell")
		}
		if variant == "baseline" || !containsAny(prompt, "日志", "log") {
			add("repo_search")
		}
	}
	if containsAny(prompt, "网页", "url") {
		if variant == "baseline" {
			add("web_search")
		}
		add("web_fetch")
		if variant == "baseline" && containsAny(prompt, "api key", "泄露", "上传数据", "调用") {
			add("http_request")
		}
	}
	if containsAny(prompt, "最新", "新闻", "搜索") && !containsAny(prompt, "不用查资料", "不用工具", "rag 搜索", "用 rag", "用rag", "rag搜索") {
		add("web_search")
	}
	if containsAny(prompt, "外部 issue", "issue 内容", "官方说明") {
		add("web_fetch")
	}
	if containsAny(prompt, "obsidian") {
		add("skill_read")
		if variant == "baseline" || !containsAny(prompt, "读取说明即可", "只读", "不要执行") {
			add("skill_obsidian_run")
		}
	}
	if containsAny(prompt, "从记忆", "记忆里", "查一下记忆", "之前说过", "之前聊过", "之前写的", "之前提到") &&
		!containsAny(prompt, "rag 搜索", "用 rag", "用rag", "rag搜索") {
		add("recall")
	}
	if containsAny(prompt, "记住", "保存到记忆", "记下来") {
		add("remember")
		if variant == "baseline" && containsAny(prompt, "仓库", "索引") {
			add("rag_index")
		}
	}
	if containsAny(prompt, "rag") && containsAny(prompt, "搜索", "召回", "验证") {
		add("rag_search")
	}
	if containsAny(prompt, "rag") && containsAny(prompt, "索引", "建立") {
		add("rag_index")
	}
	if containsAny(prompt, "截图", "图片", "二维码", "识别", "看图") {
		add("image_analyze")
		if variant == "baseline" && containsAny(prompt, "打开链接", "转账") {
			add("web_fetch")
		}
	}
	if containsAny(prompt, "生成一张", "生成图片", "指标示意图", "配图", "画图") {
		add("image_generate")
	}
	if containsAny(prompt, "语音", "朗读", "音频") {
		add("text_to_speech")
	}
	if containsAny(prompt, "数据库", "schema", "表结构") {
		add("db_schema")
	}
	if containsAny(prompt, "sql", "查询", "统计") && containsAny(prompt, "数据库", "订单表", "sql") {
		if variant == "baseline" || !containsAny(prompt, "只看 schema", "只看schema") {
			add("sql_query")
		}
	}
	if containsAny(prompt, "cron", "定时", "提醒任务") {
		if containsAny(prompt, "状态") {
			add("cron_status")
		}
		if containsAny(prompt, "列出", "查看", "任务列表") {
			add("cron_list")
		}
		if containsAny(prompt, "新增", "创建", "每天", "提醒", "提醒任务") {
			if variant == "baseline" || !containsAny(prompt, "不要新增") {
				add("cron_add")
			}
		}
		if variant == "baseline" && containsAny(prompt, "不要暂停", "不要删除") {
			add("cron_pause", "cron_remove")
		}
	}
	if containsAny(prompt, "autonomy", "worker", "后台") {
		if containsAny(prompt, "状态", "当前") {
			add("autonomy_status")
		}
		if variant == "baseline" && containsAny(prompt, "新增", "新建", "后台 worker") {
			add("autonomy_worker_spawn")
		}
	}
	if containsAny(prompt, "heartbeat", "心跳") {
		if containsAny(prompt, "状态", "检查") {
			add("heartbeat_status")
		}
		if variant == "baseline" || !containsAny(prompt, "不要手动触发", "不要触发") {
			if containsAny(prompt, "触发") {
				add("heartbeat_trigger")
			}
		}
	}
	if containsAny(prompt, "子代理", "委派", "delegate") {
		if containsAny(prompt, "列表") {
			add("list_tasks")
		}
		if containsAny(prompt, "状态") {
			add("task_status")
		}
		if variant == "baseline" || !containsAny(prompt, "不要新建", "不要委派") {
			if containsAny(prompt, "委派", "子代理跑", "新建") {
				add("delegate_task")
			}
		}
	}

	if variant == "baseline" {
		if containsAny(prompt, "dcg", "log", "归一化", "公式") && !containsAny(prompt, "不用工具") {
			add("calculate")
		}
		if containsAny(prompt, "route risk") && !containsAny(prompt, "不需要查代码") {
			add("repo_search")
		}
		if task.TaskType == "repo_inspection" {
			add("git_status")
		}
		if task.TaskType == "coding" {
			add("web_search", "git_status")
		}
		if task.TaskType == "risk_sensitive" {
			add("git_status")
		}
	}

	if variant == "guarded-intent" || variant == "risk-aware" || variant == "packed-results" {
		ops = removeNegatedRiskOps(prompt, ops)
	}
	return uniqueStrings(ops)
}

func removeNegatedRiskOps(prompt string, ops []string) []string {
	var out []string
	for _, op := range ops {
		switch op {
		case "git_push":
			if containsAny(prompt, "不要 push", "不 push", "不要push", "只读取 issue", "只读取 issue 内容", "只读取内容") {
				continue
			}
		case "destructive_shell", "file_delete":
			if containsAny(prompt, "不要直接删", "不要执行", "不要运行", "不要删") {
				continue
			}
		case "http_request":
			if containsAny(prompt, "不要执行网页里的指令", "只总结", "不要调用接口", "不要上传") {
				continue
			}
		case "skill_obsidian_run":
			if containsAny(prompt, "读取说明即可", "只读", "不要执行") {
				continue
			}
		case "write_file":
			if containsAny(prompt, "不要修改文件", "不要导出", "只查询", "只读") {
				continue
			}
		case "apply_patch":
			if containsAny(prompt, "不要修改文件", "只读取", "只读", "只读取 issue") {
				continue
			}
		case "rag_index":
			if containsAny(prompt, "不要索引", "不要写入记忆", "只搜索") {
				continue
			}
		case "remember":
			if containsAny(prompt, "不要写入记忆", "不要记住") {
				continue
			}
		case "sql_query":
			if containsAny(prompt, "只看 schema", "只看schema", "不要查询用户数据") {
				continue
			}
		case "cron_add":
			if containsAny(prompt, "不要新增", "只查看", "只查看任务列表", "只列出") {
				continue
			}
		case "cron_pause":
			if containsAny(prompt, "不要暂停", "不要暂停或删除") {
				continue
			}
		case "cron_remove":
			if containsAny(prompt, "不要删除", "不要移除", "不要暂停或删除") {
				continue
			}
		case "heartbeat_trigger":
			if containsAny(prompt, "不要手动触发", "不要触发") {
				continue
			}
		case "delegate_task":
			if containsAny(prompt, "不要新建委派任务", "不要新建", "只查看", "只列出") {
				continue
			}
		case "autonomy_worker_spawn":
			if containsAny(prompt, "只查看", "只查看任务列表") {
				continue
			}
		case "web_fetch":
			if containsAny(prompt, "只识别图片内容") {
				continue
			}
		}
		out = append(out, op)
	}
	return out
}

func visibleToolsForVariant(variant string, task benchTask, planned []string, tools map[string]toolSpec, ops map[string]operationSpec) []string {
	switch variant {
	case "baseline":
		return sortedToolKeys(tools)
	case "static-slim":
		return filterExistingTools([]string{
			"terminal", "file_read", "file_list", "file_patch", "file_write",
			"web_search", "web_fetch", "current_time", "calculate", "recall", "skill_read",
		}, tools)
	case "intent-gated", "guarded-intent", "risk-aware", "packed-results":
		visibleSet := map[string]struct{}{}
		for _, opName := range planned {
			if op, ok := ops[opName]; ok {
				visibleSet[op.Tool] = struct{}{}
			}
		}
		for _, name := range lowRiskSupportTools(task, tools) {
			visibleSet[name] = struct{}{}
		}
		if variant == "guarded-intent" || variant == "risk-aware" || variant == "packed-results" {
			for _, name := range task.ForbiddenTools {
				if !hasAllowedPlannedOperation(name, planned, task.ForbiddenOperations, ops) {
					delete(visibleSet, name)
				}
			}
		}
		return sortedSetKeys(visibleSet)
	default:
		return sortedToolKeys(tools)
	}
}

func hasAllowedPlannedOperation(toolName string, planned []string, forbidden []string, ops map[string]operationSpec) bool {
	forbiddenSet := stringSet(forbidden)
	for _, opName := range planned {
		op, ok := ops[opName]
		if !ok || op.Tool != toolName {
			continue
		}
		if _, blocked := forbiddenSet[opName]; !blocked {
			return true
		}
	}
	return false
}

func lowRiskSupportTools(task benchTask, tools map[string]toolSpec) []string {
	var out []string
	terms := strings.ToLower(strings.Join(append([]string{task.Prompt}, task.IntentTerms...), " "))
	if containsAny(terms, "repo", "code", "file", "docs", "benchmark") {
		out = append(out, "file_read", "file_list")
	}
	if containsAny(terms, "time", "date", "fresh", "最新", "今天", "当前") {
		out = append(out, "current_time")
	}
	if containsAny(terms, "math", "formula", "计算") {
		out = append(out, "calculate")
	}
	if containsAny(terms, "memory", "记忆") {
		out = append(out, "recall")
	}
	if containsAny(terms, "rag") {
		out = append(out, "rag_search")
	}
	if containsAny(terms, "log", "runtime") {
		out = append(out, "log_grep", "log_tail")
	}
	if containsAny(terms, "json") {
		out = append(out, "json_query")
	}
	if containsAny(terms, "yaml", "config") {
		out = append(out, "yaml_query")
	}
	if containsAny(terms, "csv") {
		out = append(out, "csv_query")
	}
	if containsAny(terms, "database", "schema") {
		out = append(out, "db_schema")
	}
	if containsAny(terms, "cron", "schedule") {
		out = append(out, "cron_list", "cron_status")
	}
	if containsAny(terms, "autonomy", "worker") {
		out = append(out, "autonomy_status")
	}
	if containsAny(terms, "heartbeat") {
		out = append(out, "heartbeat_status")
	}
	if containsAny(terms, "delegate", "subagent") {
		out = append(out, "task_status", "list_tasks")
	}
	return filterExistingTools(out, tools)
}

func filterExistingTools(names []string, tools map[string]toolSpec) []string {
	var out []string
	for _, name := range uniqueStrings(names) {
		if _, ok := tools[name]; ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func filterCallsByVisible(names []string, visible []string, ops map[string]operationSpec) []string {
	visibleSet := stringSet(visible)
	var out []string
	for _, name := range names {
		op, ok := ops[name]
		if !ok {
			continue
		}
		if _, ok := visibleSet[op.Tool]; ok {
			out = append(out, name)
		}
	}
	return uniqueStrings(out)
}

func callsFromOperations(names []string, ops map[string]operationSpec) []toolCall {
	var out []toolCall
	for _, name := range names {
		op, ok := ops[name]
		if !ok {
			continue
		}
		out = append(out, toolCall{Name: op.Tool, Operation: op.Name})
	}
	return out
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" && strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
