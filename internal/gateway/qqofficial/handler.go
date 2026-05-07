package qqofficial

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/gateway"
)

type Handler struct {
	adapter *Adapter
	agent   *agent.Agent
	mu      sync.RWMutex
	sessions map[string]string
}

func NewHandler(adapter *Adapter, agentRuntime *agent.Agent) *Handler {
	return &Handler{
		adapter: adapter,
		agent:   agentRuntime,
		sessions: make(map[string]string),
	}
}

func (h *Handler) HandleMessage(ctx context.Context, msg *gateway.Message) error {
	if h == nil || h.adapter == nil || h.agent == nil || msg == nil {
		return fmt.Errorf("qqofficial: handler not initialized")
	}
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
