package onebot

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/utils"
)

// Handler processes OneBot (QQ) messages with per-chat session management.
type Handler struct {
	adapter *Adapter
	agent   *agent.Agent

	mu       sync.RWMutex
	sessions map[string]string // chatID → sessionID
}

// NewHandler creates a new OneBot message handler.
func NewHandler(adapter *Adapter, a *agent.Agent) *Handler {
	return &Handler{
		adapter:  adapter,
		agent:    a,
		sessions: make(map[string]string),
	}
}

// getSessionID returns the session ID for a chat, creating one if needed.
func (h *Handler) getSessionID(chatID string) string {
	h.mu.RLock()
	if sid, ok := h.sessions[chatID]; ok {
		h.mu.RUnlock()
		return sid
	}
	h.mu.RUnlock()

	sess := h.agent.Sessions().New()
	h.mu.Lock()
	h.sessions[chatID] = sess.ID
	h.mu.Unlock()
	return sess.ID
}

// resetSession creates a new session for the chat.
func (h *Handler) resetSession(chatID string) string {
	sess := h.agent.Sessions().New()
	h.mu.Lock()
	h.sessions[chatID] = sess.ID
	h.mu.Unlock()
	return sess.ID
}

// HandleMessage processes an incoming gateway message.
func (h *Handler) HandleMessage(ctx context.Context, msg *gateway.Message) error {
	h.agent.RecordRecentChatTarget("onebot", msg.Chat.ID, msg.ID)
	if msg.IsCommand {
		return h.handleCommand(ctx, msg)
	}

	text := msg.Text
	if strings.TrimSpace(text) == "" {
		return nil
	}

	sessionID := h.getSessionID(msg.Chat.ID)

	// Send typing indicator while processing
	if h.adapter.cfg.ShowTyping {
		go h.adapter.sendTyping(ctx, msg.Chat.ID)
	}

	// Chat with agent
	response, err := h.agent.ChatWithSession(ctx, sessionID, text)
	if err != nil {
		if strings.Contains(err.Error(), "session not found") {
			h.resetSession(msg.Chat.ID)
			sessionID = h.getSessionID(msg.Chat.ID)
			response, err = h.agent.ChatWithSession(ctx, sessionID, text)
		}
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Error: %s", utils.TruncateKeepLength(err.Error(), 200)))
		}
	}

	if response == "" {
		return nil
	}

	return h.adapter.Send(ctx, msg.Chat.ID, response)
}

// handleCommand dispatches bot commands.
func (h *Handler) handleCommand(ctx context.Context, msg *gateway.Message) error {
	switch msg.Command {
	case "start", "help":
		return h.adapter.Send(ctx, msg.Chat.ID,
			"🍀 LuckyHarness Bot\n\n"+
				"直接发消息跟我聊天就行！\n"+
				"命令: /reset /model /soul /tools /skills /health")
	case "reset":
		h.resetSession(msg.Chat.ID)
		return h.adapter.Send(ctx, msg.Chat.ID, "✅ 对话已重置")
	case "model":
		provider := h.agent.Provider()
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🤖 当前模型: %s", provider.Name()))
	case "soul":
		return h.adapter.Send(ctx, msg.Chat.ID, "🧠 Soul: Lucky v0.42.0")
	case "tools":
		registry := h.agent.Tools()
		toolList := registry.List()
		var names []string
		for _, t := range toolList {
			names = append(names, t.Name)
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🔧 工具: %s", strings.Join(names, ", ")))
	case "skills":
		skills := h.agent.Skills()
		var names []string
		for _, s := range skills {
			names = append(names, s.Name)
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🎯 技能 (%d): %s", len(skills), strings.Join(names, ", ")))
	case "health":
		return h.adapter.Send(ctx, msg.Chat.ID, "🏥 系统正常运行中 ✅")
	default:
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("未知命令: /%s\n输入 /help 查看帮助", msg.Command))
	}
}
