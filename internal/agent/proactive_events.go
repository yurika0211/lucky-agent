package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/logger"
	"github.com/yurika0211/luckyagent/internal/proactive"
	"github.com/yurika0211/luckyagent/internal/session"
)

type ProactiveStatus struct {
	Enabled               bool                        `json:"enabled"`
	DryRun                bool                        `json:"dry_run"`
	RuntimeStarted        bool                        `json:"runtime_started"`
	ConfidenceThreshold   float64                     `json:"confidence_threshold"`
	HorizonSeconds        int                         `json:"horizon_seconds"`
	ActionIntervalSeconds int                         `json:"action_interval_seconds"`
	MaxActions            int                         `json:"max_actions"`
	ActionCooldownSeconds int                         `json:"action_cooldown_seconds"`
	AllowedActions        []string                    `json:"allowed_actions"`
	KernelLearning        bool                        `json:"kernel_learning_enabled"`
	KernelLearningRate    float64                     `json:"kernel_learning_rate"`
	KernelMinSamples      int                         `json:"kernel_min_samples"`
	Stats                 proactive.Stats             `json:"stats"`
	RuntimeEventStats     proactive.RuntimeEventStats `json:"runtime_event_stats"`
	KernelStats           proactive.KernelStats       `json:"kernel_stats"`
	RecentEvents          []proactive.RuntimeEvent    `json:"recent_events,omitempty"`
	RecentExecutions      []proactive.ActionExecution `json:"recent_executions,omitempty"`
}

func (a *Agent) ProactiveStatus(limit int) (ProactiveStatus, error) {
	if limit <= 0 {
		limit = 20
	}
	if a == nil || a.cfg == nil {
		return ProactiveStatus{DryRun: true, RuntimeEventStats: proactive.RuntimeEventStats{ByType: map[string]int{}}}, nil
	}
	cfg := a.cfg.Get()
	status := ProactiveStatus{
		Enabled:               cfg.Proactive.Enabled,
		DryRun:                agentProactiveDryRunEnabled(cfg.Proactive.DryRun),
		ConfidenceThreshold:   cfg.Proactive.ConfidenceThreshold,
		HorizonSeconds:        cfg.Proactive.HorizonSeconds,
		ActionIntervalSeconds: cfg.Proactive.ActionIntervalSecs,
		MaxActions:            cfg.Proactive.MaxActions,
		ActionCooldownSeconds: cfg.Proactive.ActionCooldownSecs,
		AllowedActions:        append([]string(nil), cfg.Proactive.AllowedActions...),
		KernelLearning:        agentProactiveKernelLearningEnabled(cfg.Proactive.KernelLearning),
		KernelLearningRate:    cfg.Proactive.KernelLearningRate,
		KernelMinSamples:      cfg.Proactive.KernelMinSamples,
	}
	if a.proactiveRuntime != nil {
		status.RuntimeStarted = a.proactiveRuntime.Started()
	}
	if a.proactiveStore == nil {
		status.RuntimeEventStats = proactive.RuntimeEventStats{ByType: map[string]int{}}
		return status, nil
	}
	stats, err := a.proactiveStore.Stats()
	if err != nil {
		return ProactiveStatus{}, err
	}
	status.Stats = stats
	runtimeStats, err := a.proactiveStore.RuntimeEventStats()
	if err != nil {
		return ProactiveStatus{}, err
	}
	status.RuntimeEventStats = runtimeStats
	kernelStats, err := a.proactiveStore.KernelStats()
	if err != nil {
		return ProactiveStatus{}, err
	}
	status.KernelStats = kernelStats
	recentEvents, err := a.proactiveStore.RecentRuntimeEvents(limit)
	if err != nil {
		return ProactiveStatus{}, err
	}
	status.RecentEvents = recentEvents
	recentExecutions, err := a.proactiveStore.RecentActionExecutions(limit)
	if err != nil {
		return ProactiveStatus{}, err
	}
	status.RecentExecutions = recentExecutions
	return status, nil
}

func agentProactiveDryRunEnabled(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func agentProactiveKernelLearningEnabled(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

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
