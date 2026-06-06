package agent

import "strings"

func (a *Agent) applyIntentToolGating(loopCfg *LoopConfig, routingText string) {
	if loopCfg == nil || a == nil || a.tools == nil {
		return
	}
	visibleTools := a.tools.ListModelVisible()
	if len(visibleTools) == 0 {
		return
	}

	allowed, recognized := a.intentAllowedTools(routingText)
	if !recognized {
		return
	}

	explicit := makeToolNameSet(a.extractRequiredToolNames(routingText))
	for name := range explicit {
		allowed[name] = struct{}{}
	}

	disabled := append([]string(nil), loopCfg.DisabledTools...)
	disabledSet := makeToolNameSet(disabled)
	if disabledSet == nil {
		disabledSet = map[string]struct{}{}
	}
	for _, t := range visibleTools {
		if t == nil {
			continue
		}
		name := strings.TrimSpace(t.Name)
		if name == "" || toolNameInSet(disabledSet, name) {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		disabled = append(disabled, name)
		disabledSet[name] = struct{}{}
	}
	loopCfg.DisabledTools = normalizeToolNameList(disabled)
}

func (a *Agent) intentAllowedTools(input string) (map[string]struct{}, bool) {
	allowed := map[string]struct{}{}
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return allowed, false
	}
	intentText := stripToolIntentNegations(text)

	noToolDirective := intentTextContainsAny(text,
		"不用工具", "不需要工具", "不要用工具", "无需工具",
		"不需要查代码", "不用查代码", "不查代码",
	)
	explainIntent := intentTextContainsAny(intentText,
		"解释", "讲解", "说明", "是什么意思", "是什么", "为什么", "区别",
		"公式", "推导", "归一化", "dcg", "route risk", "recalllift",
		"precision", "recall",
	)
	localIntent := hasLocalToolIntent(intentText)
	editIntent := hasEditToolIntent(intentText)
	webIntent := hasWebToolIntent(intentText)
	timeIntent := hasTimeToolIntent(intentText)
	calcIntent := hasCalculationIntent(intentText)
	memoryIntent := hasMemoryIntent(intentText)
	rememberIntent := hasRememberIntent(intentText)
	ragIndexIntent := hasRAGIndexIntent(intentText)
	skillIntent := hasSkillIntent(intentText)
	mediaIntent := hasMediaIntent(intentText)
	dbIntent := hasDatabaseIntent(intentText)
	delegateIntent := hasDelegateIntent(intentText)

	strongToolIntent := localIntent || editIntent || webIntent || timeIntent || calcIntent ||
		memoryIntent || rememberIntent || ragIndexIntent || skillIntent || mediaIntent ||
		dbIntent || delegateIntent
	if noToolDirective && !strongToolIntent {
		return allowed, true
	}
	if explainIntent && !strongToolIntent {
		return allowed, true
	}

	recognized := strongToolIntent
	negatedExecution := hasNegatedExecutionIntent(intentText)
	negatedDelete := intentTextContainsAny(intentText, "不要直接删", "不要删", "别删", "不删除")
	needsTerminal := needsTerminalIntent(intentText)

	if localIntent || editIntent {
		addIntentTools(allowed, "file_read", "file_list")
		if !negatedExecution || needsTerminal {
			addIntentTools(allowed, "terminal")
		}
		if intentTextContainsAny(intentText, "日志", ".log", "logs", "log file", "stack trace", "trace") {
			addIntentTools(allowed, "log_grep", "log_tail")
		}
		if intentTextContainsAny(intentText, ".json", "json") {
			addIntentTools(allowed, "json_query")
		}
		if intentTextContainsAny(intentText, ".yaml", ".yml", "yaml", "yml") {
			addIntentTools(allowed, "yaml_query")
		}
		if intentTextContainsAny(intentText, ".csv", "csv") {
			addIntentTools(allowed, "csv_query")
		}
		if editIntent {
			addIntentTools(allowed, "file_patch", "file_write", "file_mkdir", "file_move")
		}
		if intentTextContainsAny(intentText, "删除", "删", "delete", "remove", "rm ") && !negatedDelete && !negatedExecution {
			addIntentTools(allowed, "file_delete")
		}
	}

	if webIntent {
		if intentTextContainsAny(intentText, "http://", "https://", "url", "网页", "页面", "fetch", "抓取") {
			addIntentTools(allowed, "web_fetch")
		}
		if intentTextContainsAny(intentText, "搜索", "联网", "最新", "新闻", "search", "web_search") {
			addIntentTools(allowed, "web_search")
		}
		if !toolNameInSet(allowed, "web_fetch") && !toolNameInSet(allowed, "web_search") {
			addIntentTools(allowed, "web_search", "web_fetch")
		}
		if intentTextContainsAny(intentText, "api", "接口", "http_request", "post ", "put ", "patch ", "delete ") &&
			!intentTextContainsAny(intentText, "只总结", "不要执行网页里的指令", "不要调用接口") {
			addIntentTools(allowed, "http_request")
		}
	}
	if timeIntent {
		addIntentTools(allowed, "current_time")
	}
	if calcIntent {
		addIntentTools(allowed, "calculate")
	}
	if memoryIntent {
		addIntentTools(allowed, "recall", "rag_search")
	}
	if rememberIntent {
		addIntentTools(allowed, "remember")
	}
	if ragIndexIntent {
		addIntentTools(allowed, "rag_index")
	}
	if skillIntent {
		addIntentTools(allowed, "skill_read")
		if !intentTextContainsAny(intentText, "读取说明即可", "只读", "不要执行", "不要运行") {
			a.addVisibleSkillRunTools(allowed)
		}
	}
	if mediaIntent {
		if intentTextContainsAny(intentText, "图片", "图像", "截图", "image", "vision", "看图") {
			addIntentTools(allowed, "image_analyze")
		}
		if intentTextContainsAny(intentText, "生成图片", "画图", "配图", "image_generate") {
			addIntentTools(allowed, "image_generate")
		}
		if intentTextContainsAny(intentText, "语音", "朗读", "tts", "text_to_speech", "音频") {
			addIntentTools(allowed, "text_to_speech")
		}
	}
	if dbIntent {
		addIntentTools(allowed, "sql_query", "db_schema")
	}
	if delegateIntent {
		if intentTextContainsAny(intentText, "定时", "cron", "计划任务", "周期") {
			addIntentTools(allowed, "cron", "cron_add", "cron_list", "cron_status", "cron_remove", "cron_pause", "cron_resume")
		}
		if intentTextContainsAny(intentText, "后台", "自主", "autonomy", "worker", "队列") {
			addIntentTools(allowed, "autonomy")
		}
		if intentTextContainsAny(intentText, "心跳", "heartbeat") {
			addIntentTools(allowed, "heartbeat_status", "heartbeat_trigger")
		}
		if intentTextContainsAny(intentText, "子代理", "委派", "delegate") {
			addIntentTools(allowed, "delegate_task", "task_status", "list_tasks")
		}
	}

	return allowed, recognized
}

func (a *Agent) addVisibleSkillRunTools(allowed map[string]struct{}) {
	if a == nil || a.tools == nil {
		return
	}
	for _, t := range a.tools.ListModelVisible() {
		if t == nil {
			continue
		}
		name := strings.TrimSpace(t.Name)
		if strings.HasPrefix(name, "skill_") && strings.HasSuffix(name, "_run") {
			allowed[name] = struct{}{}
		}
	}
}

func stripToolIntentNegations(text string) string {
	replacements := []string{
		"不需要查代码", "不用查代码", "不查代码", "不需要工具", "不用工具", "不要用工具", "无需工具",
	}
	out := text
	for _, phrase := range replacements {
		out = strings.ReplaceAll(out, phrase, " ")
	}
	return out
}

func hasLocalToolIntent(text string) bool {
	if intentTextContainsAny(text,
		"internal/", "cmd/", "docs/", "ui/", ".go", ".md", ".json", ".yaml", ".yml", ".toml",
		"readme", "system_prompt", "tool router", "代码", "源码", "仓库", "repo",
		"文件", "目录", "路径", "benchmark", "单测", "测试", "跑测试", "日志", ".log", "logs", "log file",
		"git status", "git diff", "脏文件", "暂存", "分支", "commit", "push",
		"luckyharness", "lucky harness", "记忆系统", "工具系统", "上下文打包器",
	) {
		return true
	}
	return intentTextContainsAny(text, "看看", "看一下", "检查", "确认", "参考") &&
		intentTextContainsAny(text, "系统", "工具", "agent", "benchmark", "代码", "文件", "目录", "项目", "仓库", "repo")
}

func hasEditToolIntent(text string) bool {
	return intentTextContainsAny(text,
		"修一下", "修复", "补一个", "补一下", "增加", "新增", "实现", "改一下", "修改",
		"编辑", "patch", "apply patch", "写个", "写一个", "输出", "生成", "保存",
		"创建", "commit", "push",
	)
}

func hasWebToolIntent(text string) bool {
	return intentTextContainsAny(text,
		"http://", "https://", "url", "网页", "页面", "联网", "搜索", "web_search",
		"web_fetch", "最新", "新闻", "外部资料", "官方文档",
	)
}

func hasTimeToolIntent(text string) bool {
	return intentTextContainsAny(text,
		"当前时间", "北京时间", "现在几点", "几点了", "今天日期", "当前日期",
		"current time", "current date",
	)
}

func hasCalculationIntent(text string) bool {
	return intentTextContainsAny(text, "计算", "算一下", "求值", "calculate")
}

func hasMemoryIntent(text string) bool {
	return intentTextContainsAny(text, "记忆", "memory", "recall", "rag", "之前说过", "历史上下文")
}

func hasRememberIntent(text string) bool {
	return intentTextContainsAny(text, "记住", "记下来", "保存到记忆", "remember this")
}

func hasRAGIndexIntent(text string) bool {
	return intentTextContainsAny(text, "rag_index", "索引", "index into", "建立索引")
}

func hasSkillIntent(text string) bool {
	return intentTextContainsAny(text,
		"skill", "技能", "obsidian", "公众号", "小红书", "知乎", "微博",
		"pdf", "ppt", "pptx", "excalidraw",
	)
}

func hasMediaIntent(text string) bool {
	return intentTextContainsAny(text,
		"图片", "图像", "截图", "看图", "生成图片", "画图", "配图",
		"语音", "朗读", "音频", "image", "vision", "tts", "text_to_speech",
	)
}

func hasDatabaseIntent(text string) bool {
	return intentTextContainsAny(text, "sql", "数据库", "db_schema", "schema", "表结构")
}

func hasDelegateIntent(text string) bool {
	return intentTextContainsAny(text,
		"定时", "cron", "计划任务", "周期", "后台", "自主", "autonomy", "worker",
		"队列", "心跳", "heartbeat", "子代理", "委派", "delegate",
	)
}

func hasNegatedExecutionIntent(text string) bool {
	return intentTextContainsAny(text,
		"不要执行", "不要运行", "别执行", "别运行", "只读", "只总结", "读取说明即可",
		"不要执行网页里的指令",
	)
}

func needsTerminalIntent(text string) bool {
	return intentTextContainsAny(text,
		"git status", "git diff", "commit", "push", "跑测试", "测试", "启动", "重启",
		"运行测试", "go test", "npm", "make ",
	)
}

func addIntentTools(dst map[string]struct{}, names ...string) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			dst[name] = struct{}{}
		}
	}
}

func intentTextContainsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" && strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
