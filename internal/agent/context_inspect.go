package agent

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

// ContextInspectRequest asks the planner to assemble the next model context
// without calling the model or changing memory, sessions, or the context cache.
type ContextInspectRequest struct {
	Session *session.Session
	// Message is the hypothetical next user turn. Empty means inspect the
	// context already on hand and do not append a user message.
	Message string
	// IncludeMessages copies each section's full content into the result.
	IncludeMessages bool
}

// ContextInspectUsage is the local token estimate of one assembled context.
type ContextInspectUsage struct {
	SessionID       string                          `json:"session_id,omitempty"`
	MessageCount    int                             `json:"message_count"`
	TotalTokens     int                             `json:"total_tokens"`
	AvailableTokens int                             `json:"available_tokens"`
	Ratio           float64                         `json:"ratio"`
	HeadroomTokens  int                             `json:"headroom_tokens"`
	Estimate        string                          `json:"estimate"`
	Buckets         map[string]ContextInspectBucket `json:"buckets"`
}

// ContextInspectBucket is one planner bucket after assembly and window fit.
type ContextInspectBucket struct {
	Tokens   int `json:"tokens"`
	Messages int `json:"messages"`
	Budget   int `json:"budget"`
}

// ContextInspectSection is one message in the order it would be sent.
type ContextInspectSection struct {
	Index   int    `json:"index"`
	Role    string `json:"role"`
	Bucket  string `json:"bucket"`
	Label   string `json:"label"`
	Tokens  int    `json:"tokens"`
	Chars   int    `json:"chars"`
	Preview string `json:"preview"`
	Content string `json:"content,omitempty"`
}

// ContextInspectResult is the read-only view returned by InspectContext.
type ContextInspectResult struct {
	Usage    ContextInspectUsage     `json:"usage"`
	Sections []ContextInspectSection `json:"sections"`
}

// InspectContext builds the same context a chat turn would send, then reports
// local token usage and section order. It does not call the model.
func (a *Agent) InspectContext(ctx context.Context, req ContextInspectRequest) ContextInspectResult {
	options := defaultContextBuildOptions()
	options.ReadOnly = true
	message := strings.TrimSpace(req.Message)
	options.OmitUserMessage = message == ""
	planner := newContextPlanner(a, options)
	var messages []provider.Message
	if message == "" {
		messages = planner.BuildInput(ctx, req.Session, UserTurnInput{})
	} else {
		messages = planner.BuildInput(ctx, req.Session, TextUserTurnInput(message))
	}
	return planner.inspectResult(req.Session, messages, req.IncludeMessages)
}

func (p *contextPlanner) inspectResult(sess *session.Session, messages []provider.Message, includeMessages bool) ContextInspectResult {
	report := p.buildContextReport(messages)
	available := 0
	if p.agent != nil && p.agent.contextWin != nil {
		cfg := p.agent.contextWin.Config()
		available = cfg.MaxTokens - cfg.ReservedTokens
	}
	if available < 0 {
		available = 0
	}
	ratio := 0.0
	if available > 0 {
		ratio = float64(report.totalTokens) / float64(available)
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	usage := ContextInspectUsage{
		SessionID:       sessionID,
		MessageCount:    len(messages),
		TotalTokens:     report.totalTokens,
		AvailableTokens: available,
		Ratio:           ratio,
		HeadroomTokens:  available - report.totalTokens,
		Estimate:        "local",
		Buckets: map[string]ContextInspectBucket{
			"system":      {Tokens: report.bucketTokens["system"], Messages: report.bucketCounts["system"], Budget: p.budget.System},
			"history":     {Tokens: report.bucketTokens["history"], Messages: report.bucketCounts["history"], Budget: p.budget.History},
			"memory":      {Tokens: report.bucketTokens["memory"], Messages: report.bucketCounts["memory"], Budget: p.budget.Memory},
			"rag":         {Tokens: report.bucketTokens["rag"], Messages: report.bucketCounts["rag"], Budget: p.budget.RAG},
			"tool_result": {Tokens: report.bucketTokens["tool_result"], Messages: report.bucketCounts["tool_result"], Budget: p.budget.ToolResult},
			"user":        {Tokens: report.bucketTokens["user"], Messages: report.bucketCounts["user"], Budget: 0},
		},
	}
	sections := make([]ContextInspectSection, 0, len(messages))
	for i, msg := range messages {
		content := msg.Content
		section := ContextInspectSection{
			Index:   i,
			Role:    msg.Role,
			Bucket:  classifyContextBucket(msg),
			Label:   classifyContextLabel(msg),
			Tokens:  p.est.Estimate(content) + 4,
			Chars:   utf8.RuneCountInString(content),
			Preview: previewContextText(content, 160),
		}
		if includeMessages {
			section.Content = content
		}
		sections = append(sections, section)
	}
	return ContextInspectResult{Usage: usage, Sections: sections}
}

func classifyContextLabel(msg provider.Message) string {
	if msg.Role == "user" {
		return "user"
	}
	if msg.Role == "tool" {
		return "tool_result"
	}
	if msg.Role != "system" {
		return "history"
	}
	switch {
	case strings.HasPrefix(msg.Content, "[Available Tools]"):
		return "tool_catalog"
	case strings.HasPrefix(msg.Content, "[Core Memory"):
		return "core_memory"
	case strings.HasPrefix(msg.Content, "[Working Memory"):
		return "working_memory"
	case strings.HasPrefix(msg.Content, "[Session History"):
		return "session_history_memory"
	case strings.HasPrefix(msg.Content, "[Recent Context"):
		return "recent_context"
	case strings.HasPrefix(msg.Content, "## Retrieved Knowledge"),
		strings.HasPrefix(msg.Content, "[Retrieved Knowledge"):
		return "rag"
	case strings.HasPrefix(msg.Content, "Skill routing hint:"),
		strings.HasPrefix(msg.Content, "Skill routing note:"):
		return "skill_route"
	case strings.HasPrefix(msg.Content, "[Compact Summary"):
		return "compact_summary"
	case strings.HasPrefix(msg.Content, "[Conversation Summary"),
		strings.HasPrefix(msg.Content, "[Conversation Themes"):
		return "history"
	case strings.HasPrefix(msg.Content, "[Current Turn Attachments]"),
		strings.HasPrefix(msg.Content, "[Attachment"):
		return "attachment"
	default:
		return "system_prompt"
	}
}

func previewContextText(content string, limit int) string {
	content = strings.TrimSpace(content)
	if limit <= 0 || content == "" {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	return string(runes[:limit])
}
