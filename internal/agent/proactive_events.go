package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/logger"
	"github.com/yurika0211/luckyagent/internal/proactive"
	"github.com/yurika0211/luckyagent/internal/session"
)

func (a *Agent) recordProactiveRuntimeEvent(event proactive.RuntimeEvent) {
	if a == nil || a.proactiveStore == nil {
		return
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if err := a.proactiveStore.RecordRuntimeEvent(event); err != nil {
		logger.Warn("proactive runtime event record failed", "type", event.Type, "name", event.Name, "error", err)
	}
}

func (a *Agent) recordProactiveChatEvent(sess *session.Session, input UserTurnInput, response string, toolCalls int, err error) {
	sessionID, cwd := proactiveSessionFields(sess)
	metadata := map[string]string{
		"input_chars":    fmt.Sprintf("%d", len(input.RoutingText)),
		"response_chars": fmt.Sprintf("%d", len(response)),
		"tool_calls":     fmt.Sprintf("%d", toolCalls),
	}
	if cwd != "" {
		metadata["cwd"] = cwd
	}
	if err != nil {
		metadata["error"] = truncate(err.Error(), 180)
	}
	a.recordProactiveRuntimeEvent(proactive.RuntimeEvent{
		Source:    "agent",
		SessionID: sessionID,
		Type:      "chat_turn",
		Name:      "chat",
		Value:     float64(toolCalls),
		Metadata:  metadata,
	})
}

func (a *Agent) recordProactiveToolEvent(sess *session.Session, result executedToolCall, blocked bool) {
	sessionID, cwd := proactiveSessionFields(sess)
	eventType := "tool_call"
	success := !strings.HasPrefix(strings.TrimSpace(result.Result), "Error:")
	if blocked {
		eventType = "tool_blocked"
		success = false
	}
	metadata := map[string]string{
		"success":      fmt.Sprintf("%t", success),
		"duration_ms":  fmt.Sprintf("%d", result.Duration.Milliseconds()),
		"result_chars": fmt.Sprintf("%d", len(result.Result)),
		"arguments":    truncate(result.ToolCall.Arguments, 180),
	}
	if cwd != "" {
		metadata["cwd"] = cwd
	}
	a.recordProactiveRuntimeEvent(proactive.RuntimeEvent{
		Source:    "agent",
		SessionID: sessionID,
		Type:      eventType,
		Name:      result.ToolCall.Name,
		Value:     float64(result.Duration.Milliseconds()),
		Metadata:  metadata,
	})
}

func proactiveSessionFields(sess *session.Session) (sessionID string, cwd string) {
	if sess == nil {
		return "", ""
	}
	return sess.ID, sess.GetCwd()
}
