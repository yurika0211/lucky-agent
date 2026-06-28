package agent

import (
	"os"
	"strings"
)

const toolIntentGatingEnv = "LH_TOOL_INTENT_GATING"

func (a *Agent) applyIntentToolGating(loopCfg *LoopConfig, routingText string) {
	if !toolIntentGatingEnabled() {
		return
	}
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

func toolIntentGatingEnabled() bool {
	value := strings.TrimSpace(os.Getenv(toolIntentGatingEnv))
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true
	default:
		return false
	}
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
	toolSystemIntent := hasToolSystemIntent(intentText)
	localIntent := hasLocalToolIntent(intentText) || toolSystemIntent
	editIntent := hasEditToolIntent(intentText)
	webIntent := hasWebToolIntent(intentText)
	timeIntent := hasTimeToolIntent(intentText)
	calcIntent := hasCalculationIntent(intentText)
	memoryIntent := hasMemoryIntent(intentText)
	memoryHygieneIntent := hasMemoryHygieneIntent(intentText)
	rememberIntent := hasRememberIntent(intentText)
	ragIndexIntent := hasRAGIndexIntent(intentText)
	skillIntent := hasSkillIntent(intentText)
	mediaIntent := hasMediaIntent(intentText) && !toolSystemIntent
	dbIntent := hasDatabaseIntent(intentText)
	delegateIntent := hasDelegateIntent(intentText)

	strongToolIntent := localIntent || editIntent || webIntent || timeIntent || calcIntent ||
		memoryIntent || memoryHygieneIntent || rememberIntent || ragIndexIntent || skillIntent || mediaIntent ||
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
	readOnlyMutationBlocked := hasReadOnlyMutationBlockIntent(intentText)
	needsTerminal := needsTerminalIntent(intentText)
	readOnlySourceIntent := hasReadOnlyExternalSourceIntent(intentText)

	if localIntent || editIntent {
		addIntentTools(allowed, "file_read", "file_list")
		if (!negatedExecution || needsTerminal) && !readOnlySourceIntent && !readOnlyMutationBlocked {
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
		if intentTextContainsAny(intentText, ".pdf", ".docx", ".pptx", "pdf", "docx", "pptx", "word", "powerpoint", "文档", "幻灯片") {
			addIntentTools(allowed, "document_read")
		}
		if toolSystemIntent {
			addIntentTools(allowed, "json_query", "yaml_query")
		}
		if editIntent && !readOnlySourceIntent && !readOnlyMutationBlocked {
			addIntentTools(allowed, "file_patch", "file_write", "file_mkdir", "file_move")
		}
		if intentTextContainsAny(intentText, "删除", "删", "delete", "remove", "rm ") && !negatedDelete && !negatedExecution {
			addIntentTools(allowed, "file_delete")
		}
	}

	if webIntent {
		if intentTextContainsAny(intentText, "opencli", "doctor", "browser", "external register", "plugin install", "plugin create") {
			addIntentTools(allowed, "opencli")
		}
		if intentTextContainsAny(intentText, "推特关注", "关注动态", "following timeline", "twitter timeline", "x timeline", "看我的推特", "看看我的推特", "我的推特关注动态") {
			addIntentTools(allowed, "opencli")
		}
		if intentTextContainsAny(intentText, "http://", "https://", "url", "网页", "页面", "fetch", "抓取") {
			addIntentTools(allowed, "opencli", "web_fetch")
		}
		if intentTextContainsAny(intentText, "搜索", "联网", "最新", "新闻", "search", "web_search") {
			addIntentTools(allowed, "web_search")
		}
		if !toolNameInSet(allowed, "opencli") && !toolNameInSet(allowed, "web_fetch") && !toolNameInSet(allowed, "web_search") {
			addIntentTools(allowed, "web_search", "web_fetch", "opencli")
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
	if memoryHygieneIntent {
		addIntentTools(allowed, "recall", "memory_hygiene")
	}
	if rememberIntent && !intentTextContainsAny(intentText, "不要写入记忆", "不要记住", "不要保存到记忆") {
		addIntentTools(allowed, "remember")
	}
	if ragIndexIntent && !intentTextContainsAny(intentText, "不要索引", "不要建立索引", "不要写入 rag", "不要写入rag") {
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
		addIntentTools(allowed, "db_schema")
		if !intentTextContainsAny(intentText, "只看 schema", "只看schema", "只检查数据库表结构", "不要查询用户数据") {
			addIntentTools(allowed, "sql_query")
		}
	}
	if delegateIntent {
		if intentTextContainsAny(intentText, "定时", "cron", "计划任务", "周期") {
			addIntentTools(allowed, "cron_list", "cron_status")
			if !intentTextContainsAny(intentText, "只查看", "只列出", "列出", "查看", "状态", "不要新增") {
				addIntentTools(allowed, "cron", "cron_add")
			}
			if !intentTextContainsAny(intentText, "不要暂停", "不要暂停或删除") {
				addIntentTools(allowed, "cron_pause")
			}
			if !intentTextContainsAny(intentText, "不要删除", "不要移除", "不要暂停或删除") {
				addIntentTools(allowed, "cron_remove")
			}
		}
		if intentTextContainsAny(intentText, "后台", "自主", "autonomy", "worker", "队列") {
			addIntentTools(allowed, "autonomy", "autonomy_status")
			if !intentTextContainsAny(intentText, "只查看", "只列出") {
				addIntentTools(allowed, "autonomy_queue_add", "autonomy_worker_spawn")
			}
		}
		if intentTextContainsAny(intentText, "心跳", "heartbeat") {
			addIntentTools(allowed, "heartbeat_status")
			if !intentTextContainsAny(intentText, "不要手动触发", "不要触发", "只检查") {
				addIntentTools(allowed, "heartbeat_trigger")
			}
		}
		if intentTextContainsAny(intentText, "子代理", "委派", "delegate") {
			addIntentTools(allowed, "task_status", "list_tasks")
			if !intentTextContainsAny(intentText, "不要新建", "不要委派", "只查看", "只列出", "工具", "实现", "代码", "源码", "在哪里", "在哪", "位置", "定义") {
				addIntentTools(allowed, "delegate_task")
			}
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

func hasToolSystemIntent(text string) bool {
	if intentTextContainsAny(text,
		"工具系统", "工具路由", "工具门控", "工具菜单", "工具列表", "工具清单",
		"工具可见", "可见工具", "隐藏工具", "禁用工具",
		"tool system", "tool routing", "tool gate", "tool gating", "tool menu",
		"visible tool", "visible tools", "disabled tool", "disabled tools",
	) {
		return true
	}
	return intentTextContainsAny(text, "只有", "只剩", "only") &&
		intentTextContainsAny(text, "图片分析工具", "图片工具", "image_analyze", "image analysis tool", "image tool") &&
		intentTextContainsAny(text, "lh", "luckyagent", "agent", "模型", "说", "显示", "报")
}

func hasLocalToolIntent(text string) bool {
	if intentTextContainsAny(text,
		"internal/", "cmd/", "docs/", "ui/", ".go", ".md", ".json", ".yaml", ".yml", ".toml",
		"readme", "system_prompt", "tool router", "代码", "源码", "仓库", "repo",
		"文件", "目录", "路径", "benchmark", "单测", "测试", "跑测试", "日志", ".log", "logs", "log file",
		"git status", "git diff", "脏文件", "暂存", "分支", "commit", "push",
		"luckyagent", "lucky agent", "记忆系统", "工具系统", "上下文打包器",
	) {
		return true
	}
	return intentTextContainsAny(text, "看看", "看一下", "检查", "确认", "参考") &&
		intentTextContainsAny(text, "系统", "工具", "agent", "benchmark", "代码", "文件", "目录", "项目", "仓库", "repo")
}

func hasEditToolIntent(text string) bool {
	return intentTextContainsAny(text,
		"修一下", "修复", "补一个", "补一下", "补全", "补齐", "完善", "优化",
		"调优", "调整", "改进", "升级", "重构", "落地", "接入", "加上", "加入",
		"增加", "新增", "实现", "改一下", "修改", "编辑", "patch", "apply patch",
		"写个", "写一个", "输出", "生成", "保存", "开个新包",
		"创建", "commit", "push",
	)
}

func hasWebToolIntent(text string) bool {
	return intentTextContainsAny(text,
		"http://", "https://", "url", "网页", "页面", "联网", "搜索", "web_search",
		"web_fetch", "最新", "新闻", "外部资料", "官方文档", "外部 issue", "issue 内容",
		"opencli", "doctor", "browser", "external register", "plugin install", "plugin create",
		"twitter", "x.com", "x ", "推特", "x推特", "关注动态", "following timeline",
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
	return intentTextContainsAny(text,
		"查记忆", "查一下记忆", "从记忆", "记忆里", "回忆一下", "检索记忆",
		"历史上下文", "之前说过", "之前聊过", "之前提到", "我说过",
		"recall memory", "memory recall", "rag_search", "用 rag 搜索", "用rag搜索",
	)
}

func hasMemoryHygieneIntent(text string) bool {
	return intentTextContainsAny(text,
		"清洗记忆", "清理记忆", "脏记忆", "脏数据", "记忆污染", "清洗脏记忆",
		"memory_hygiene", "memory hygiene", "dirty memory", "memory cleanup", "clean memory",
	)
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
	if intentTextContainsAny(text, "图片分析工具", "图片生成工具", "图像分析工具", "图像生成工具") {
		return false
	}
	return intentTextContainsAny(text,
		"图片", "图像", "截图", "看图", "生成图片", "画图", "配图",
		"语音", "朗读", "音频", "image", "vision", "tts", "text_to_speech",
	)
}

func hasDatabaseIntent(text string) bool {
	return intentTextContainsAny(text, "sql", "数据库", "db_schema", "schema", "表结构", "订单表")
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
	if hasReadOnlyExternalSourceIntent(text) {
		return false
	}
	return intentTextContainsAny(text,
		"git status", "git diff", "commit", "push", "跑测试", "测试", "启动", "重启",
		"运行测试", "go test", "npm", "make ",
	)
}

func hasReadOnlyExternalSourceIntent(text string) bool {
	return intentTextContainsAny(text, "只读取 issue", "只读取 issue 内容", "只读取内容", "只总结网页", "只总结", "只读外部")
}

func hasReadOnlyMutationBlockIntent(text string) bool {
	return intentTextContainsAny(text,
		"只读", "只查看", "只列出", "只读取", "只总结", "只检查",
		"不要修改文件", "不修改文件", "不要写文件", "不要写入文件",
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
