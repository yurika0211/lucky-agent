package sdk

import "github.com/yurika0211/luckyagent/internal/agent"

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

func mapEvents(in <-chan agent.ChatEvent) <-chan Event {
	out := make(chan Event, 64)
	go func() {
		defer close(out)
		if in == nil {
			return
		}
		for ev := range in {
			out <- Event{
				Type:    mapEventType(ev.Type),
				Content: ev.Content,
				TaskID:  ev.TaskID,
				Name:    ev.Name,
				Args:    ev.Args,
				Result:  ev.Result,
				Round:   ev.Round,
				Err:     ev.Err,
			}
		}
	}()
	return out
}
