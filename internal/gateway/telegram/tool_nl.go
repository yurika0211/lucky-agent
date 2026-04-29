package telegram

import (
	"encoding/json"
	"fmt"
	"strings"
)

func humanizeThinkingProgress(content string) string {
	content = clipOneLine(content, 180)
	if content == "" {
		return ""
	}
	return ensureSentenceSuffix("我先陪你把这条思路慢慢捋顺喔：" + content)
}

func humanizeToolCallProgress(step int, name, args string) string {
	if step <= 0 {
		step = 1
	}
	n := strings.TrimSpace(name)
	if n == "" {
		n = "unknown_tool"
	}
	lowerName := strings.ToLower(n)
	parsed := parseToolCallArgs(args)

	if strings.Contains(lowerName, "skill") {
		if skillName := pickArg(parsed, "skill", "skill_name", "name", "id"); skillName != "" {
			return fmt.Sprintf("我先借一下技能「%s」的处理套路呀，帮你把这一步走稳一点。", clipOneLine(skillName, 60))
		}
		return "我先调一下现成的技能链路呀，把这类问题的处理框架先搭稳。"
	}

	switch lowerName {
	case "web_search":
		if q := pickArg(parsed, "query", "q", "keyword"); q != "" {
			return fmt.Sprintf("我先替你去网上找一轮资料呀，重点盯着 %s。", quoteAndTrim(q, 90))
		}
		return "我先去外面帮你扫一圈资料呀，看看有没有更靠谱的线索。"
	case "web_fetch":
		if u := pickArg(parsed, "url", "uri", "link"); u != "" {
			return fmt.Sprintf("我把这页原文打开看看呀，先替你核对细节：%s。", clipOneLine(u, 100))
		}
		return "我先把网页正文捞出来呀，看看里面到底写了什么。"
	case "shell":
		if cmd := pickArg(parsed, "cmd", "command", "script"); cmd != "" {
			return fmt.Sprintf("我先在终端里替你查一下现状呀，跑这条命令看看：%s。", clipOneLine(cmd, 100))
		}
		return "我先去终端里探一下路呀，确认当前环境到底是什么状态。"
	case "file_read":
		if p := pickArg(parsed, "path", "file", "filepath", "filename"); p != "" {
			return fmt.Sprintf("我先陪你翻一下文件 %s 呀，看看里面现在是什么情况。", clipOneLine(p, 100))
		}
		return "我先把相关文件读一遍呀，免得后面判断跑偏。"
	case "file_write":
		if p := pickArg(parsed, "path", "file", "filepath", "filename"); p != "" {
			return fmt.Sprintf("我先把修改落到 %s 里呀，这样你后面接着看会更安心。", clipOneLine(p, 100))
		}
		return "我先把这次改动真正写进去呀，后面再陪你一起收口。"
	case "recall":
		if q := pickArg(parsed, "query", "q", "keyword"); q != "" {
			return fmt.Sprintf("我先回头翻一下之前的上下文呀，看看 %s 有没有旧线索。", quoteAndTrim(q, 90))
		}
		return "我先把之前的上下文翻出来呀，避免漏掉已经说过的关键信息。"
	case "remember":
		return "这个点值得记一下呀，我先替你收进记忆里，后面就不用重复解释了。"
	case "current_time":
		return "我先确认一下现在的时间和时区呀，免得时间线搞错。"
	}

	if keyHint := pickArg(parsed,
		"query", "url", "path", "cmd", "command",
		"title", "task_id", "name"); keyHint != "" {
		return fmt.Sprintf("我先借工具 %s 处理一下 %s 呀，把这块缺口替你补上。", n, clipOneLine(keyHint, 100))
	}
	return fmt.Sprintf("我先调一下 %s 呀，把这一小段信息替你补全。", n)
}

func humanizeToolResultProgress(step int, name, result string) string {
	if step <= 0 {
		step = 1
	}
	n := strings.TrimSpace(name)
	lowerName := strings.ToLower(n)

	switch lowerName {
	case "web_search":
		return "这一轮搜索结果我已经拿到了呀，我先帮你把有用的和噪音分开。"
	case "web_fetch":
		return "原文我已经读过了呀，接着帮你拎里面真正关键的部分。"
	case "shell":
		return "命令已经跑完啦，我正顺着输出继续帮你判断。"
	case "file_read":
		return "文件我已经看过了呀，接着帮你抓重点。"
	case "file_write":
		return "改动已经写进去了呀，我再顺手帮你做一轮确认。"
	case "recall":
		return "前面的上下文我已经翻过了呀，现在继续帮你往下收拢。"
	case "remember":
		return "这条信息我已经记住啦，后面我们可以直接接着用。"
	case "current_time":
		return "时间线已经核对好了呀，这块不会再跑偏。"
	}

	if strings.Contains(lowerName, "skill") {
		return "技能链路已经跑完啦，我现在把结果慢慢往结论里收。"
	}

	if summary := humanizeToolResult(name, result); summary != "" {
		summary = strings.TrimPrefix(summary, "我")
		summary = strings.TrimSpace(summary)
		return ensureSentenceSuffix("好呀，我已经把这一步带回来了，" + summary)
	}
	return "这一步已经有结果啦，我继续帮你往下整理。"
}

func wrapFinalConclusion(finalOutput string) string {
	finalOutput = strings.TrimSpace(finalOutput)
	if finalOutput == "" {
		return "我这轮暂时还没整理出能直接递给你的结论呀。"
	}
	return finalOutput
}

func humanizeToolCall(name, args string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		n = "unknown_tool"
	}

	parsed := parseToolCallArgs(args)

	switch n {
	case "web_search":
		if q := pickArg(parsed, "query", "q", "keyword"); q != "" {
			return fmt.Sprintf("正在联网搜索：%s", quoteAndTrim(q, 80))
		}
		return "正在联网搜索相关信息"

	case "web_fetch":
		if u := pickArg(parsed, "url", "uri", "link"); u != "" {
			return fmt.Sprintf("正在读取网页：%s", clipOneLine(u, 90))
		}
		return "正在读取网页内容"

	case "shell":
		if cmd := pickArg(parsed, "cmd", "command", "script"); cmd != "" {
			return fmt.Sprintf("正在执行命令：%s", clipOneLine(cmd, 90))
		}
		return "正在执行终端命令"

	case "file_read":
		if p := pickArg(parsed, "path", "file", "filepath", "filename"); p != "" {
			return fmt.Sprintf("正在读取文件：%s", clipOneLine(p, 90))
		}
		return "正在读取文件"

	case "file_write":
		if p := pickArg(parsed, "path", "file", "filepath", "filename"); p != "" {
			return fmt.Sprintf("正在写入文件：%s", clipOneLine(p, 90))
		}
		return "正在写入文件"

	case "current_time":
		return "正在查询当前时间"

	case "remember":
		if content := pickArg(parsed, "content", "text", "memory"); content != "" {
			return fmt.Sprintf("正在保存记忆：%s", clipOneLine(content, 80))
		}
		return "正在保存记忆"

	case "recall":
		if q := pickArg(parsed, "query", "q", "keyword"); q != "" {
			return fmt.Sprintf("正在检索记忆：%s", quoteAndTrim(q, 80))
		}
		return "正在检索记忆"
	}

	if keyHint := pickArg(parsed,
		"query", "url", "path", "cmd", "command",
		"title", "task_id", "name"); keyHint != "" {
		return fmt.Sprintf("正在调用工具 %s：%s", n, clipOneLine(keyHint, 90))
	}
	return fmt.Sprintf("正在调用工具 %s", n)
}

func humanizeToolResult(name, result string) string {
	n := strings.TrimSpace(name)
	text := clipOneLine(result, 160)
	if text == "" {
		return ""
	}

	switch n {
	case "web_search":
		if strings.Contains(text, "Results for:") {
			return "我先捞到了一批相关结果呀，里面已经能看到几个比较靠谱的入口。"
		}
		if strings.Contains(strings.ToLower(text), "no results found") {
			return "我先替你搜了一轮，不过这次还没捞到像样的结果呀。"
		}
		return "我先查到了一批和这个问题贴得比较近的资料呀。"

	case "web_fetch":
		if strings.Contains(strings.ToLower(text), "failed to fetch") {
			return "我试着把网页正文抓下来，不过这页没顺利拿到呀。"
		}
		return "我把网页正文过了一遍呀，里面有几处细节可以直接拿来用。"

	case "file_read":
		return "我已经翻到文件里的关键片段啦。"

	case "recall":
		if strings.Contains(text, "没有找到") {
			return "我回头翻了之前的记录，不过这件事暂时没有现成线索呀。"
		}
		return "我先把之前相关的上下文顺了一遍呀。"

	case "current_time":
		return "我顺手把时间信息也核对过了呀。"
	}

	return fmt.Sprintf("我已经把 %s 这一步的结果带回来啦。", n)
}

func parseToolCallArgs(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

func pickArg(args map[string]any, keys ...string) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range keys {
		v, ok := args[key]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case string:
			if s := strings.TrimSpace(x); s != "" {
				return s
			}
		case []any:
			if len(x) > 0 {
				s := strings.TrimSpace(fmt.Sprintf("%v", x[0]))
				if s != "" {
					return s
				}
			}
		default:
			s := strings.TrimSpace(fmt.Sprintf("%v", v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func clipOneLine(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" {
		return s
	}
	if max <= 0 {
		max = 80
	}
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func quoteAndTrim(s string, max int) string {
	s = clipOneLine(s, max)
	if s == "" {
		return s
	}
	return "「" + s + "」"
}

func ensureSentenceSuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "。") || strings.HasSuffix(s, "！") || strings.HasSuffix(s, "？") ||
		strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "。"
}
