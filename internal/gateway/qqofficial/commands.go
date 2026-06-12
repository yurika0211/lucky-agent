package qqofficial

import (
	"fmt"
	"strings"
)

type qqCommandGroup string

const (
	qqCommandGroupBasic   qqCommandGroup = "Basic"
	qqCommandGroupSystem  qqCommandGroup = "System"
	qqCommandGroupSession qqCommandGroup = "Session"
	qqCommandGroupLearn   qqCommandGroup = "Learning"
)

type qqCommandSpec struct {
	Command     string
	Usage       string
	Description string
	Group       qqCommandGroup
}

func qqCommandSpecs() []qqCommandSpec {
	return []qqCommandSpec{
		{Command: "start", Usage: "/start", Description: "连接状态", Group: qqCommandGroupBasic},
		{Command: "help", Usage: "/help", Description: "查看帮助", Group: qqCommandGroupBasic},
		{Command: "chat", Usage: "/chat <消息>", Description: "显式发起对话", Group: qqCommandGroupBasic},
		{Command: "lucky", Usage: "/lucky on|off|status|cancel", Description: "收集多段消息后统一发送", Group: qqCommandGroupBasic},
		{Command: "review", Usage: "/review", Description: "查看工作区状态", Group: qqCommandGroupSystem},
		{Command: "init", Usage: "/init", Description: "查看初始化状态", Group: qqCommandGroupSystem},
		{Command: "config", Usage: "/config [list|get]", Description: "查看配置", Group: qqCommandGroupSystem},
		{Command: "version", Usage: "/version", Description: "查看运行版本", Group: qqCommandGroupSystem},
		{Command: "model", Usage: "/model [name]", Description: "查看或切换模型", Group: qqCommandGroupSystem},
		{Command: "models", Usage: "/models", Description: "列出可用模型", Group: qqCommandGroupSystem},
		{Command: "soul", Usage: "/soul", Description: "查看当前 SOUL", Group: qqCommandGroupSystem},
		{Command: "tools", Usage: "/tools", Description: "列出可用工具", Group: qqCommandGroupSystem},
		{Command: "skills", Usage: "/skills", Description: "列出已加载技能", Group: qqCommandGroupSystem},
		{Command: "mcp", Usage: "/mcp <name> <url> [api_key]", Description: "连接 MCP server", Group: qqCommandGroupSystem},
		{Command: "approve", Usage: "/approve <tool>", Description: "自动批准工具", Group: qqCommandGroupSystem},
		{Command: "deny", Usage: "/deny <tool>", Description: "禁用工具", Group: qqCommandGroupSystem},
		{Command: "cron", Usage: "/cron [list|add|remove|pause|resume|start|stop]", Description: "管理定时任务", Group: qqCommandGroupSystem},
		{Command: "watch", Usage: "/watch [list|add|remove|start|stop]", Description: "管理文件监控", Group: qqCommandGroupSystem},
		{Command: "dashboard", Usage: "/dashboard [status]", Description: "查看 Dashboard 状态", Group: qqCommandGroupSystem},
		{Command: "msg_gateway", Usage: "/msg_gateway [status]", Description: "查看消息网关状态", Group: qqCommandGroupSystem},
		{Command: "rag", Usage: "/rag [stats|search|list|remove|index]", Description: "管理 RAG 知识库", Group: qqCommandGroupSystem},
		{Command: "context", Usage: "/context", Description: "查看上下文窗口状态", Group: qqCommandGroupSystem},
		{Command: "fc", Usage: "/fc [tools|history|clear]", Description: "查看 function-calling 信息", Group: qqCommandGroupSystem},
		{Command: "embedder", Usage: "/embedder [list|switch|test]", Description: "管理 embedding 模型", Group: qqCommandGroupSystem},
		{Command: "metrics", Usage: "/metrics", Description: "查看运行指标", Group: qqCommandGroupSystem},
		{Command: "health", Usage: "/health", Description: "查看系统健康状态", Group: qqCommandGroupSystem},
		{Command: "learn", Usage: "/learn", Description: "查看学习模式命令", Group: qqCommandGroupLearn},
		{Command: "learn_start", Usage: "/learn_start <course>", Description: "开始或恢复课程", Group: qqCommandGroupLearn},
		{Command: "learn_current", Usage: "/learn_current", Description: "查看当前学习模块", Group: qqCommandGroupLearn},
		{Command: "learn_lab", Usage: "/learn_lab", Description: "查看当前 lab", Group: qqCommandGroupLearn},
		{Command: "learn_submit", Usage: "/learn_submit <evidence>", Description: "提交 lab 证据", Group: qqCommandGroupLearn},
		{Command: "learn_progress", Usage: "/learn_progress", Description: "查看学习进度", Group: qqCommandGroupLearn},
		{Command: "remember", Usage: "/remember <content>", Description: "保存中期记忆", Group: qqCommandGroupSession},
		{Command: "remember_long", Usage: "/remember_long <content>", Description: "保存长期记忆", Group: qqCommandGroupSession},
		{Command: "recall", Usage: "/recall <query>", Description: "检索记忆", Group: qqCommandGroupSession},
		{Command: "memstats", Usage: "/memstats", Description: "查看记忆统计", Group: qqCommandGroupSession},
		{Command: "memdecay", Usage: "/memdecay", Description: "衰减低权重记忆", Group: qqCommandGroupSession},
		{Command: "promote", Usage: "/promote <memory_id>", Description: "提升记忆层级", Group: qqCommandGroupSession},
		{Command: "profile", Usage: "/profile [list|switch]", Description: "管理 profile", Group: qqCommandGroupSession},
		{Command: "reset", Usage: "/reset", Description: "重置当前会话", Group: qqCommandGroupSession},
		{Command: "history", Usage: "/history", Description: "查看最近会话历史", Group: qqCommandGroupSession},
		{Command: "session", Usage: "/session [title|id]", Description: "查看或切换会话", Group: qqCommandGroupSession},
		{Command: "sessions", Usage: "/sessions", Description: "列出最近会话", Group: qqCommandGroupSession},
		{Command: "resume", Usage: "/resume <title|id>", Description: "恢复已有会话", Group: qqCommandGroupSession},
		{Command: "rename", Usage: "/rename <title>", Description: "重命名当前会话", Group: qqCommandGroupSession},
		{Command: "new", Usage: "/new", Description: "开启新会话", Group: qqCommandGroupSession},
		{Command: "stop", Usage: "/stop", Description: "停止当前任务", Group: qqCommandGroupSession},
		{Command: "status", Usage: "/status", Description: "查看 bot 状态", Group: qqCommandGroupSession},
		{Command: "restart", Usage: "/restart", Description: "重启当前网关", Group: qqCommandGroupSession},
	}
}

func qqCommandNames() []string {
	specs := qqCommandSpecs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Command)
	}
	return names
}

func qqHelpMessage() string {
	var sb strings.Builder
	sb.WriteString("可用命令：\n")
	for _, group := range []qqCommandGroup{qqCommandGroupBasic, qqCommandGroupSystem, qqCommandGroupLearn, qqCommandGroupSession} {
		sb.WriteString("\n")
		sb.WriteString(string(group))
		sb.WriteString(":\n")
		for _, spec := range qqCommandSpecs() {
			if spec.Group != group || spec.Command == "start" {
				continue
			}
			sb.WriteString(fmt.Sprintf("%s : %s\n", spec.Usage, spec.Description))
		}
	}
	sb.WriteString("\n也可以直接发送普通消息开始对话。")
	return strings.TrimSpace(sb.String())
}
