package lhcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/session"
)

type chatStreamResult struct {
	Response string
}

func runChatStream(ctx context.Context, a *agent.Agent, sess *session.Session, userInput string, loopCfg agent.LoopConfig) (*chatStreamResult, error) {
	if a == nil {
		return nil, fmt.Errorf("agent is nil")
	}
	if sess == nil {
		return nil, fmt.Errorf("session is nil")
	}

	events, err := a.ChatWithSessionStreamInputWithLoopConfig(ctx, sess.ID, agent.TextUserTurnInput(userInput), loopCfg)
	if err != nil {
		return nil, err
	}

	mq := startMarquee("Lucky> ", "thinking")
	defer mq.Stop()

	var finalResponse string

	for event := range events {
		switch event.Type {
		case agent.ChatEventThinking:
			mq.Update(formatChatStatus(event.Content, "thinking"))
		case agent.ChatEventToolCall:
			mq.Update(formatToolStatus("tool", event.Name))
		case agent.ChatEventToolResult:
			mq.Update(formatToolStatus("done", event.Name))
		case agent.ChatEventDone:
			finalResponse = event.Content
		case agent.ChatEventError:
			return nil, event.Err
		}
	}

	return &chatStreamResult{Response: finalResponse}, nil
}

func formatChatStatus(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	raw = strings.TrimSuffix(raw, "...")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "Thinking")
	raw = strings.TrimPrefix(raw, "thinking")
	raw = strings.Trim(raw, ".() ")
	if raw == "" {
		return fallback
	}
	return fallback + " " + raw
}

func formatToolStatus(prefix, toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return prefix
	}
	return prefix + " " + toolName
}
