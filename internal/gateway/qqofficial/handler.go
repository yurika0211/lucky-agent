package qqofficial

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/gateway"
)

type commandHandler func(ctx context.Context, msg *gateway.Message) error

type Handler struct {
	adapter  *Adapter
	agent    *agent.Agent
	commands map[string]commandHandler
	mu       sync.RWMutex
	sessions map[string]string
}

func NewHandler(adapter *Adapter, agentRuntime *agent.Agent) *Handler {
	h := &Handler{
		adapter:  adapter,
		agent:    agentRuntime,
		sessions: make(map[string]string),
	}
	h.commands = h.buildCommandRegistry()
	return h
}

func (h *Handler) HandleMessage(ctx context.Context, msg *gateway.Message) error {
	if h == nil || h.adapter == nil || h.agent == nil || msg == nil {
		return fmt.Errorf("qqofficial: handler not initialized")
	}
	if msg.IsCommand {
		if handler, ok := h.commands[h.commandKey(msg.Command)]; ok {
			return handler(ctx, msg)
		}
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, "暂不支持这个命令。可用命令：/help /reset /model /session /history")
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return nil
	}
	return h.handlePlainChat(ctx, msg)
}

func (h *Handler) buildCommandRegistry() map[string]commandHandler {
	return map[string]commandHandler{
		"start":   h.handleStart,
		"help":    h.handleHelp,
		"chat":    h.handleChatCommand,
		"model":   h.handleModel,
		"reset":   h.handleReset,
		"history": h.handleHistory,
		"session": h.handleSession,
	}
}

func (h *Handler) commandKey(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	cmd = strings.TrimPrefix(cmd, "/")
	return strings.ToLower(cmd)
}

func (h *Handler) handleStart(ctx context.Context, msg *gateway.Message) error {
	return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, "已连接 LuckyHarness QQ 机器人。\n可直接发送消息开始对话，或使用 /help 查看命令。")
}

func (h *Handler) handleHelp(ctx context.Context, msg *gateway.Message) error {
	help := `可用命令：
/help - 查看帮助
/chat <消息> - 显式发起对话
/model [模型] - 查看或切换模型
/reset - 重置当前会话
/session - 查看当前会话
/history - 查看最近会话历史

也可以直接发送普通消息开始对话。`
	return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, help)
}

func (h *Handler) handleChatCommand(ctx context.Context, msg *gateway.Message) error {
	if strings.TrimSpace(msg.Args) == "" {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, "请在 /chat 后面带上要发送的内容，例如：/chat 你好")
	}
	msgCopy := *msg
	msgCopy.Text = msg.Args
	return h.handlePlainChat(ctx, &msgCopy)
}

func (h *Handler) handleModel(ctx context.Context, msg *gateway.Message) error {
	if strings.TrimSpace(msg.Args) == "" {
		cfg := h.agent.Config().Get()
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("当前模型：%s\nProvider：%s", cfg.Model, cfg.Provider))
	}
	if err := h.agent.SwitchModel(strings.TrimSpace(msg.Args)); err != nil {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("切换模型失败：%s", err.Error()))
	}
	return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("已切换到模型：%s", strings.TrimSpace(msg.Args)))
}

func (h *Handler) handleReset(ctx context.Context, msg *gateway.Message) error {
	newID := h.resetSession(msg.Chat.ID)
	shortID := newID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("会话已重置，新会话：%s", shortID))
}

func (h *Handler) handleSession(ctx context.Context, msg *gateway.Message) error {
	h.mu.RLock()
	sessionID, ok := h.sessions[msg.Chat.ID]
	h.mu.RUnlock()
	if !ok {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, "当前还没有活跃会话，先发一条消息试试。")
	}
	sessions := h.agent.Sessions()
	if sessions == nil {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, "会话管理器当前不可用。")
	}
	sess, ok := sessions.Get(sessionID)
	if !ok {
		shortID := sessionID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("会话 %s 未找到，可能已过期。", shortID))
	}
	shortID := sessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	info := fmt.Sprintf("当前会话：%s\n标题：%s\n消息数：%d", shortID, sess.Title, sess.MessageCount())
	return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, info)
}

func (h *Handler) handleHistory(ctx context.Context, msg *gateway.Message) error {
	sessionID := h.getSessionID(msg.Chat.ID)
	sessions := h.agent.Sessions()
	if sessions == nil {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, "当前无法读取会话历史。")
	}
	sess, ok := sessions.Get(sessionID)
	if !ok {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, "当前没有可用的会话历史。")
	}
	messages := sess.GetMessages()
	if len(messages) == 0 {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, "这个会话里还没有消息。")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("最近消息（共 %d 条）：\n", len(messages)))
	start := 0
	if len(messages) > 10 {
		start = len(messages) - 10
	}
	for i := start; i < len(messages); i++ {
		role := messages[i].Role
		content := strings.TrimSpace(messages[i].Content)
		if len(content) > 80 {
			content = content[:80] + "..."
		}
		if content == "" {
			content = "(空内容)"
		}
		sb.WriteString(fmt.Sprintf("[%s] %s\n", role, content))
	}
	return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, strings.TrimSpace(sb.String()))
}

func (h *Handler) handlePlainChat(ctx context.Context, msg *gateway.Message) error {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return nil
	}

	sessionID := h.getSessionID(msg.Chat.ID)
	response, err := h.agent.ChatWithSession(ctx, sessionID, text)
	if err != nil {
		if strings.Contains(err.Error(), "session not found") {
			sessionID = h.resetSession(msg.Chat.ID)
			response, err = h.agent.ChatWithSession(ctx, sessionID, text)
		}
	}
	if err != nil {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("❌ Error: %s", err.Error()))
	}
	response = strings.TrimSpace(response)
	if response == "" {
		response = "我这边暂时还没有整理出可发送的结果。"
	}
	return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, response)
}

func (h *Handler) getSessionID(chatID string) string {
	h.mu.RLock()
	if sid, ok := h.sessions[chatID]; ok && strings.TrimSpace(sid) != "" {
		h.mu.RUnlock()
		return sid
	}
	h.mu.RUnlock()

	if h.agent == nil || h.agent.Sessions() == nil {
		return "qqofficial:" + chatID
	}
	sess := h.agent.Sessions().New()
	h.mu.Lock()
	h.sessions[chatID] = sess.ID
	h.mu.Unlock()
	return sess.ID
}

func (h *Handler) resetSession(chatID string) string {
	if h.agent == nil || h.agent.Sessions() == nil {
		return "qqofficial:" + chatID
	}
	sess := h.agent.Sessions().New()
	h.mu.Lock()
	h.sessions[chatID] = sess.ID
	h.mu.Unlock()
	return sess.ID
}
