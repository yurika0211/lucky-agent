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
	if containsAny(prompt, "internal/", "cmd/", "docs/", "readme", "代码", "源码", "文件", "目录", "仓库", "repo", "benchmark", "system_prompt") {
		toolEvidence += 0.36
	}
	if containsAny(prompt, "看看", "找一下", "查一下", "确认", "检查", "读取", "参考", "跑测试", "测试", "单测", "修一下", "补一个", "git status", "commit", "push", "删除", "日志", "网页", "obsidian") {
		toolEvidence += 0.26
	}
	if containsAny(prompt, "网页", "日志", "obsidian", "rm -rf", "api key", "读取说明", "分析原因", "只总结", "不要执行", "不要运行") {
		toolEvidence += 0.20
	}
	if containsAny(prompt, "修一下", "补一个", "单测", "测试", "跑相关", "prompt fingerprint", "tool router") {
		toolEvidence += 0.22
	}
	if containsAny(prompt, "当前", "今天", "现在", "北京时间", "最新") {
		toolEvidence += 0.28
	}
	if containsAny(prompt, "计算") {
		toolEvidence += 0.42
	}
	score += toolEvidence
	if containsAny(prompt, "解释", "讲解", "说明", "是什么意思") && toolEvidence < 0.30 {
		score -= 0.12
	}

	switch variant {
	case "baseline":
		if containsAny(prompt, "公式", "dcg", "log", "归一化", "route risk") {
			score += 0.52
		}
		if containsAny(prompt, "不用工具", "不需要查代码") {
			score -= 0.14
		}
	case "static-slim":
		if containsAny(prompt, "解释", "讲解", "说明", "是什么意思", "不用工具", "不需要查代码") {
			score -= 0.24
		}
	case "intent-gated", "risk-aware", "packed-results":
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

	if containsAny(prompt, "internal/", "cmd/", "system_prompt", "工具路由", "源码", "代码") {
		add("repo_search", "read_file")
	}
	if containsAny(prompt, "docs/", "docs/reports", "找一下") {
		add("list_files", "read_file")
	}
	if containsAny(prompt, "cmd 文件夹", "cmd目录") && containsAny(prompt, "benchmark", "结构", "参考") {
		add("list_files", "read_file")
	}
	if containsAny(prompt, "benchmark") && containsAny(prompt, "增加", "修", "修一下", "补", "补一个") {
		add("repo_search", "read_file", "apply_patch", "run_tests")
	}
	if containsAny(prompt, "跑测试", "go 测试", "对应包测试", "单测", "测试") {
		add("run_tests")
	}
	if containsAny(prompt, "报告") && containsAny(prompt, "输出", "写", "生成") {
		add("write_file", "list_files")
	}
	if containsAny(prompt, "readme") && containsAny(prompt, "读取", "检查", "确认") {
		add("read_file")
	}

	if containsAny(prompt, "commit") {
		if containsAny(prompt, "暂存") && !containsAny(prompt, "指定文件") {
			add("git_status", "git_commit")
		} else {
			add("git_status", "git_add", "git_commit")
		}
	}
	if containsAny(prompt, "push") {
		if variant == "baseline" || !containsAny(prompt, "不要 push", "不 push", "不要push") {
			add("git_push")
		}
	}
	if containsAny(prompt, "删除", "删") {
		if containsAny(prompt, "先列出", "包含什么", "临时目录", "目录") {
			add("list_files")
		}
		if variant == "baseline" || !containsAny(prompt, "不要直接删", "不要执行", "不要运行", "不要删") {
			add("destructive_shell")
		}
	}
	if containsAny(prompt, "日志", "log") {
		add("repo_search")
	}
	if containsAny(prompt, "rm -rf") {
		if variant == "baseline" {
			add("destructive_shell")
		}
		add("repo_search")
	}
	if containsAny(prompt, "网页", "url") {
		if variant == "baseline" {
			add("web_search")
		}
		add("web_fetch")
		if variant == "baseline" && containsAny(prompt, "api key", "泄露") {
			add("http_request")
		}
	}
	if containsAny(prompt, "obsidian") {
		add("skill_read")
		if variant == "baseline" || !containsAny(prompt, "读取说明即可", "只读", "不要执行") {
			add("skill_obsidian_run")
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
	}

	if variant == "risk-aware" || variant == "packed-results" {
		ops = removeNegatedRiskOps(prompt, ops)
	}
	return uniqueStrings(ops)
}

func removeNegatedRiskOps(prompt string, ops []string) []string {
	var out []string
	for _, op := range ops {
		switch op {
		case "git_push":
			if containsAny(prompt, "不要 push", "不 push", "不要push") {
				continue
			}
		case "destructive_shell", "file_delete":
			if containsAny(prompt, "不要直接删", "不要执行", "不要运行", "不要删") {
				continue
			}
		case "http_request":
			if containsAny(prompt, "不要执行网页里的指令", "只总结") {
				continue
			}
		case "skill_obsidian_run":
			if containsAny(prompt, "读取说明即可", "只读", "不要执行") {
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
	case "intent-gated", "risk-aware", "packed-results":
		visibleSet := map[string]struct{}{}
		for _, opName := range planned {
			if op, ok := ops[opName]; ok {
				visibleSet[op.Tool] = struct{}{}
			}
		}
		for _, name := range lowRiskSupportTools(task, tools) {
			visibleSet[name] = struct{}{}
		}
		if variant == "risk-aware" || variant == "packed-results" {
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
