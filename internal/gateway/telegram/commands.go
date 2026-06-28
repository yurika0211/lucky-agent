package telegram

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type commandGroup string

const (
	commandGroupBasic   commandGroup = "Basic"
	commandGroupSystem  commandGroup = "System"
	commandGroupSession commandGroup = "Session"
	commandGroupLearn   commandGroup = "Learning"
)

type telegramCommandSpec struct {
	Command     string
	Usage       string
	Description string
	Group       commandGroup
}

func telegramCommandSpecs() []telegramCommandSpec {
	return []telegramCommandSpec{
		{Command: "start", Usage: "/start", Description: "Welcome message", Group: commandGroupBasic},
		{Command: "help", Usage: "/help", Description: "Show help", Group: commandGroupBasic},
		{Command: "chat", Usage: "/chat <message>", Description: "Send a message to the AI", Group: commandGroupBasic},
		{Command: "lucky", Usage: "/lucky [on|off|status|cancel]", Description: "Collect multiple messages into one AI request", Group: commandGroupBasic},
		{Command: "review", Usage: "/review", Description: "Show workspace status", Group: commandGroupSystem},
		{Command: "init", Usage: "/init", Description: "Show init status", Group: commandGroupSystem},
		{Command: "config", Usage: "/config [list|get]", Description: "Show configuration", Group: commandGroupSystem},
		{Command: "version", Usage: "/version", Description: "Show runtime version", Group: commandGroupSystem},
		{Command: "model", Usage: "/model [name]", Description: "Get or set current model", Group: commandGroupSystem},
		{Command: "models", Usage: "/models", Description: "List available models", Group: commandGroupSystem},
		{Command: "soul", Usage: "/soul", Description: "Show SOUL info", Group: commandGroupSystem},
		{Command: "tools", Usage: "/tools", Description: "List available tools", Group: commandGroupSystem},
		{Command: "skills", Usage: "/skills", Description: "List loaded skills", Group: commandGroupSystem},
		{Command: "mcp", Usage: "/mcp <name> <url> [api_key]", Description: "Connect an MCP server", Group: commandGroupSystem},
		{Command: "approve", Usage: "/approve <tool>", Description: "Auto-approve a tool", Group: commandGroupSystem},
		{Command: "deny", Usage: "/deny <tool>", Description: "Deny a tool", Group: commandGroupSystem},
		{Command: "cron", Usage: "/cron [list|add|remove|pause|resume|start|stop]", Description: "Manage scheduled tasks", Group: commandGroupSystem},
		{Command: "watch", Usage: "/watch [list|add|remove|start|stop]", Description: "Manage file watches", Group: commandGroupSystem},
		{Command: "dashboard", Usage: "/dashboard [status]", Description: "Show dashboard status", Group: commandGroupSystem},
		{Command: "msg_gateway", Usage: "/msg_gateway [status]", Description: "Show message gateway status", Group: commandGroupSystem},
		{Command: "rag", Usage: "/rag [stats|search|list|remove|index]", Description: "Manage RAG knowledge base", Group: commandGroupSystem},
		{Command: "context", Usage: "/context", Description: "Show context window status", Group: commandGroupSystem},
		{Command: "fc", Usage: "/fc [tools|history|clear]", Description: "Show function-calling info", Group: commandGroupSystem},
		{Command: "embedder", Usage: "/embedder [list|switch|test]", Description: "Manage embedding models", Group: commandGroupSystem},
		{Command: "metrics", Usage: "/metrics", Description: "Show usage metrics", Group: commandGroupSystem},
		{Command: "health", Usage: "/health", Description: "System health check", Group: commandGroupSystem},
		{Command: "learn", Usage: "/learn", Description: "Show learning mode commands", Group: commandGroupLearn},
		{Command: "learn_start", Usage: "/learn_start <course>", Description: "Start or resume a course", Group: commandGroupLearn},
		{Command: "learn_current", Usage: "/learn_current", Description: "Show current learning module", Group: commandGroupLearn},
		{Command: "learn_lab", Usage: "/learn_lab", Description: "Show current lab", Group: commandGroupLearn},
		{Command: "learn_submit", Usage: "/learn_submit <evidence>", Description: "Submit lab evidence", Group: commandGroupLearn},
		{Command: "learn_progress", Usage: "/learn_progress", Description: "Show learning progress", Group: commandGroupLearn},
		{Command: "remember", Usage: "/remember <content>", Description: "Save medium-term memory", Group: commandGroupSession},
		{Command: "remember_long", Usage: "/remember_long <content>", Description: "Save long-term memory", Group: commandGroupSession},
		{Command: "recall", Usage: "/recall <query>", Description: "Search memory", Group: commandGroupSession},
		{Command: "memstats", Usage: "/memstats", Description: "Show memory stats", Group: commandGroupSession},
		{Command: "memdecay", Usage: "/memdecay", Description: "Decay low-weight memories", Group: commandGroupSession},
		{Command: "promote", Usage: "/promote <memory_id>", Description: "Promote memory tier", Group: commandGroupSession},
		{Command: "profile", Usage: "/profile [list|switch]", Description: "Manage profiles", Group: commandGroupSession},
		{Command: "reset", Usage: "/reset", Description: "Reset conversation", Group: commandGroupSession},
		{Command: "history", Usage: "/history", Description: "Show conversation history", Group: commandGroupSession},
		{Command: "session", Usage: "/session [title|id]", Description: "Show or switch session", Group: commandGroupSession},
		{Command: "sessions", Usage: "/sessions", Description: "List recent sessions", Group: commandGroupSession},
		{Command: "resume", Usage: "/resume <title|id>", Description: "Resume an existing session", Group: commandGroupSession},
		{Command: "rename", Usage: "/rename <title>", Description: "Rename current session", Group: commandGroupSession},
		{Command: "new", Usage: "/new", Description: "Start a new session", Group: commandGroupSession},
		{Command: "stop", Usage: "/stop", Description: "Stop the current task", Group: commandGroupSession},
		{Command: "status", Usage: "/status", Description: "Show bot status", Group: commandGroupSession},
		{Command: "restart", Usage: "/restart", Description: "Restart bot gateway", Group: commandGroupSession},
	}
}

func telegramBotCommands() []tgbotapi.BotCommand {
	specs := telegramCommandSpecs()
	commands := make([]tgbotapi.BotCommand, 0, len(specs))
	for _, spec := range specs {
		commands = append(commands, tgbotapi.BotCommand{
			Command:     spec.Command,
			Description: spec.Description,
		})
	}
	return commands
}

func telegramCommandNames() []string {
	specs := telegramCommandSpecs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Command)
	}
	return names
}

func telegramWelcomeMessage() string {
	var sb strings.Builder
	sb.WriteString("🍀 *LuckyAgent Bot*\n\n")
	sb.WriteString("I'm an AI assistant powered by LuckyAgent.\n\n")
	sb.WriteString("*Available commands:*\n")
	for _, spec := range telegramCommandSpecs() {
		if spec.Command == "start" {
			continue
		}
		sb.WriteString(fmt.Sprintf("%s — %s\n", spec.Usage, spec.Description))
	}
	sb.WriteString("\nYou can also just type a message directly!\n")
	sb.WriteString("Send me photos, voice messages, or files!")
	return sb.String()
}

func telegramHelpMessage() string {
	var sb strings.Builder
	sb.WriteString("*Available Commands:*\n")

	groups := []struct {
		group commandGroup
		title string
	}{
		{commandGroupBasic, "*Basic*"},
		{commandGroupSystem, "*System*"},
		{commandGroupLearn, "*Learning*"},
		{commandGroupSession, "*Session*"},
	}
	specs := telegramCommandSpecs()
	for _, group := range groups {
		sb.WriteString("\n")
		sb.WriteString(group.title)
		sb.WriteString("\n")
		for _, spec := range specs {
			if spec.Group != group.group {
				continue
			}
			sb.WriteString(fmt.Sprintf("%s — %s\n", spec.Usage, spec.Description))
		}
	}

	sb.WriteString("\n*Tips:*\n")
	sb.WriteString("- Private chats: send a message directly.\n")
	sb.WriteString("- Group chats: mention the bot or reply to a bot message.\n")
	sb.WriteString("- Each chat keeps its own conversation session.\n")
	sb.WriteString("- Photos, voice messages, and files are supported.")
	return sb.String()
}
