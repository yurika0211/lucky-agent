package qqofficial

import (
	"context"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/gateway"
)

type Handler struct {
	adapter *Adapter
	agent   *agent.Agent
}

func NewHandler(adapter *Adapter, agentRuntime *agent.Agent) *Handler {
	return &Handler{
		adapter: adapter,
		agent:   agentRuntime,
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

	sessionID := "qqofficial:" + msg.Chat.ID
	response, err := h.agent.ChatWithSession(ctx, sessionID, text)
	if err != nil {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("❌ Error: %s", err.Error()))
	}
	response = strings.TrimSpace(response)
	if response == "" {
		response = "我这边暂时还没有整理出可发送的结果。"
	}
	return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, response)
}
