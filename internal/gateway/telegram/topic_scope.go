package telegram

import (
	"strings"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

const telegramTopicScopeSeparator = "::thread::"

// telegramConversationScope scopes all mutable chat state to a forum topic.
// The empty ThreadID remains backward compatible with existing per-chat state.
func telegramConversationScope(msg *gateway.Message) string {
	if msg == nil {
		return ""
	}
	chatID := strings.TrimSpace(msg.Chat.ID)
	threadID := strings.TrimSpace(msg.ThreadID)
	if chatID == "" || threadID == "" {
		return chatID
	}
	return chatID + telegramTopicScopeSeparator + threadID
}
