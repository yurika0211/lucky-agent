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
		{Command: "model", Usage: "/model [name]", Description: "Get or set current model", Group: commandGroupSystem},
		{Command: "soul", Usage: "/soul", Description: "Show SOUL info", Group: commandGroupSystem},
		{Command: "tools", Usage: "/tools", Description: "List available tools", Group: commandGroupSystem},
		{Command: "skills", Usage: "/skills", Description: "List loaded skills", Group: commandGroupSystem},
		{Command: "cron", Usage: "/cron [list|add|remove|pause|resume]", Description: "Manage scheduled tasks", Group: commandGroupSystem},
		{Command: "metrics", Usage: "/metrics", Description: "Show usage metrics", Group: commandGroupSystem},
		{Command: "health", Usage: "/health", Description: "System health check", Group: commandGroupSystem},
		{Command: "reset", Usage: "/reset", Description: "Reset conversation", Group: commandGroupSession},
		{Command: "history", Usage: "/history", Description: "Show conversation history", Group: commandGroupSession},
		{Command: "session", Usage: "/session [id]", Description: "Show or switch session", Group: commandGroupSession},
		{Command: "sessions", Usage: "/sessions", Description: "List recent sessions", Group: commandGroupSession},
		{Command: "resume", Usage: "/resume <id>", Description: "Resume an existing session", Group: commandGroupSession},
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
	sb.WriteString("🍀 *LuckyHarness Bot*\n\n")
	sb.WriteString("I'm an AI assistant powered by LuckyHarness.\n\n")
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
