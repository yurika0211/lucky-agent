package agent

import (
	"context"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/logger"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

const maxAutoCompactFailures = 3

func (a *Agent) maybeAutoCompactSession(ctx context.Context, sess *session.Session, routingText string, ephemeral bool) {
	if !a.shouldAutoCompactSession(sess, routingText, ephemeral) {
		return
	}
	result, err := a.CompactSession(ctx, sess, "auto")
	if err != nil {
		a.recordAutoCompactFailure(sess.ID)
		logger.Warn("auto compact failed", "session_id", sess.ID, "error", err)
		return
	}
	a.resetAutoCompactFailure(sess.ID)
	logger.Info("auto compact completed",
		"session_id", sess.ID,
		"boundary_id", result.BoundaryID,
		"dropped_messages", result.DroppedMessages,
		"pre_tokens", result.PreTokenEstimate,
		"post_tokens", result.PostTokenEstimate,
	)
}

func (a *Agent) shouldAutoCompactSession(sess *session.Session, routingText string, ephemeral bool) bool {
	if a == nil || sess == nil || ephemeral {
		return false
	}
	cfg := a.autoCompactConfig()
	if !cfg.AutoCompact {
		return false
	}
	if a.autoCompactFailureCount(sess.ID) >= maxAutoCompactFailures {
		return false
	}
	all := sess.GetMessages()
	raw, _, _, hadPrior := session.MessagesAfterLastCompactBoundary(all)
	minMessages := cfg.AutoCompactMinMessages
	if minMessages <= 0 {
		minMessages = 24
	}
	if len(raw) < minMessages {
		return false
	}
	cooldownTurns := cfg.AutoCompactCooldownTurns
	if cooldownTurns <= 0 {
		cooldownTurns = 8
	}
	if hadPrior && compactMessageTurns(raw) < cooldownTurns {
		return false
	}
	maxTokens := cfg.MaxContextTokens
	if maxTokens <= 0 {
		maxTokens = 8000
	}
	threshold := cfg.AutoCompactThreshold
	if threshold <= 0 {
		threshold = 0.82
	}
	checkMessages := append([]provider.Message(nil), raw...)
	if routingText != "" {
		checkMessages = append(checkMessages, provider.Message{Role: "user", Content: routingText})
	}
	estimated := estimateProviderMessages(a.contextEst, checkMessages)
	if estimated <= 0 {
		return false
	}
	return float64(estimated)/float64(maxTokens) >= threshold
}

func (a *Agent) autoCompactConfig() config.ContextConfig {
	if a != nil && a.cfg != nil {
		return a.cfg.Get().Context
	}
	return config.DefaultConfig().Context
}

func compactMessageTurns(messages []provider.Message) int {
	if len(messages) == 0 {
		return 0
	}
	return (len(messages) + 1) / 2
}

func (a *Agent) autoCompactFailureCount(sessionID string) int {
	a.autoCompactMu.Lock()
	defer a.autoCompactMu.Unlock()
	if a.autoCompactFailures == nil {
		return 0
	}
	return a.autoCompactFailures[sessionID]
}

func (a *Agent) recordAutoCompactFailure(sessionID string) {
	a.autoCompactMu.Lock()
	defer a.autoCompactMu.Unlock()
	if a.autoCompactFailures == nil {
		a.autoCompactFailures = make(map[string]int)
	}
	a.autoCompactFailures[sessionID]++
}

func (a *Agent) resetAutoCompactFailure(sessionID string) {
	a.autoCompactMu.Lock()
	defer a.autoCompactMu.Unlock()
	if a.autoCompactFailures == nil {
		return
	}
	delete(a.autoCompactFailures, sessionID)
}
