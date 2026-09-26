package sdk

import (
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
)

// EventType classifies a streaming chat event.
type EventType string

const (
	EventThinking         EventType = "thinking"
	EventToolCall         EventType = "tool_call"
	EventToolResult       EventType = "tool_result"
	EventContent          EventType = "content"
	EventDone             EventType = "done"
	EventError            EventType = "error"
	EventObservation      EventType = "observation"
	EventApprovalRequired EventType = "approval_required"
	EventReasoning        EventType = "reasoning"
	EventUnknown          EventType = "unknown"
)

// Event is the stable streaming unit returned by ChatStream helpers.
type Event struct {
	Type    EventType
	Content string
	TaskID  string
	Name    string
	Args    string
	Result  string
	Round   int
	Err     error

	// Optional structured payloads. Nil when the event type does not carry them.
	Approval    *ApprovalInfo
	Observation *ObservationInfo
	Usage       *TokenUsage
	CreatedAt   *time.Time
}

// ApprovalInfo describes a gated tool action that needs host confirmation.
//
// v0 does not yet expose a separate Approve/Reject API; hosts that need gated
// tools should set Config.AutoApprove or mark host tools with AutoApprove.
type ApprovalInfo struct {
	RequestID string
	Tool      string
	Action    string
	Reason    string
	FrameID   string
}

// ObservationInfo is safe computer-use frame metadata (no local file path).
type ObservationInfo struct {
	FrameID      string
	MimeType     string
	Width        int
	Height       int
	ScaleFactor  float64
	DisplayID    string
	ActiveWindow string
}

// TokenUsage is aggregate provider token accounting for a finished turn.
type TokenUsage struct {
	InputTokens       int
	OutputTokens      int
	TotalTokens       int
	CachedInputTokens int
	Model             string
}

// MemoryHit is a simplified recall result.
type MemoryHit struct {
	ID       string
	Content  string
	Category string
	Score    float64
}

func mapEventType(t agent.ChatEventType) EventType {
	switch t {
	case agent.ChatEventThinking:
		return EventThinking
	case agent.ChatEventToolCall:
		return EventToolCall
	case agent.ChatEventToolResult:
		return EventToolResult
	case agent.ChatEventContent:
		return EventContent
	case agent.ChatEventDone:
		return EventDone
	case agent.ChatEventError:
		return EventError
	case agent.ChatEventObservation:
		return EventObservation
	case agent.ChatEventApprovalRequired:
		return EventApprovalRequired
	case agent.ChatEventReasoningContent:
		return EventReasoning
	default:
		return EventUnknown
	}
}

func mapEvent(ev agent.ChatEvent) Event {
	out := Event{
		Type:    mapEventType(ev.Type),
		Content: ev.Content,
		TaskID:  ev.TaskID,
		Name:    ev.Name,
		Args:    ev.Args,
		Result:  ev.Result,
		Round:   ev.Round,
		Err:     ev.Err,
	}
	if ev.Approval != nil {
		out.Approval = &ApprovalInfo{
			RequestID: ev.Approval.RequestID,
			Tool:      ev.Approval.Tool,
			Action:    ev.Approval.Action,
			Reason:    ev.Approval.Reason,
			FrameID:   ev.Approval.FrameID,
		}
	}
	if ev.Observation != nil {
		out.Observation = &ObservationInfo{
			FrameID:      ev.Observation.FrameID,
			MimeType:     ev.Observation.MimeType,
			Width:        ev.Observation.Width,
			Height:       ev.Observation.Height,
			ScaleFactor:  ev.Observation.ScaleFactor,
			DisplayID:    ev.Observation.DisplayID,
			ActiveWindow: ev.Observation.ActiveWindow,
		}
	}
	if ev.Usage != nil {
		out.Usage = &TokenUsage{
			InputTokens:       ev.Usage.InputTokens,
			OutputTokens:      ev.Usage.OutputTokens,
			TotalTokens:       ev.Usage.TotalTokens,
			CachedInputTokens: ev.Usage.CachedInputTokens,
			Model:             ev.Usage.Model,
		}
	}
	if ev.CreatedAt != nil {
		ts := *ev.CreatedAt
		out.CreatedAt = &ts
	}
	return out
}

func mapEvents(in <-chan agent.ChatEvent) <-chan Event {
	out := make(chan Event, 64)
	go func() {
		defer close(out)
		if in == nil {
			return
		}
		for ev := range in {
			out <- mapEvent(ev)
		}
	}()
	return out
}
