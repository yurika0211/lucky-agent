package agent

import (
	"context"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/turn"
)

// ToTurnChatEvent converts the legacy in-process event into the neutral DTO
// used by gateway adapters. It is intentionally explicit so provider usage
// accounting and local computer paths do not leak through the boundary.
func ToTurnChatEvent(event ChatEvent) turn.ChatEvent {
	return turn.ChatEvent{
		Type:        turn.EventType(event.Type),
		Content:     event.Content,
		TaskID:      event.TaskID,
		Name:        event.Name,
		Args:        event.Args,
		Result:      event.Result,
		Duration:    event.Duration,
		Round:       event.Round,
		Observation: toTurnObservation(event.Observation),
		Approval:    toTurnApproval(event.Approval),
		Compact:     toTurnCompact(event.Compact),
		Usage:       provider.ToTurnTokenUsage(event.Usage),
		CreatedAt:   event.CreatedAt,
		Err:         event.Err,
	}
}

// FromTurnChatEvent restores the legacy in-process event shape for callers
// that have not migrated their rendering code yet. Keeping this conversion at
// the runtime boundary lets gateway interfaces use turn.ChatEvent without
// changing every existing stream consumer in one patch.
func FromTurnChatEvent(event turn.ChatEvent) ChatEvent {
	return ChatEvent{
		Type:        ChatEventType(event.Type),
		Content:     event.Content,
		TaskID:      event.TaskID,
		Name:        event.Name,
		Args:        event.Args,
		Result:      event.Result,
		Duration:    event.Duration,
		Round:       event.Round,
		Observation: fromTurnObservation(event.Observation),
		Approval:    fromTurnApproval(event.Approval),
		Compact:     fromTurnCompact(event.Compact),
		Usage:       fromTurnTokenUsage(event.Usage),
		CreatedAt:   event.CreatedAt,
		Err:         event.Err,
	}
}

// ToTurnChatEventStream adapts the legacy in-process event stream to the
// provider-independent turn event stream used by gateway boundaries.
//
// The adapter preserves event order and closes the returned channel when the
// source closes. Cancellation stops forwarding promptly so a gateway that
// abandons a response does not leave an adapter goroutine blocked on send.
func ToTurnChatEventStream(ctx context.Context, events <-chan ChatEvent) <-chan turn.ChatEvent {
	if events == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	out := make(chan turn.ChatEvent)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				converted := ToTurnChatEvent(event)
				select {
				case <-ctx.Done():
					return
				case out <- converted:
				}
			}
		}
	}()
	return out
}

// FromTurnChatEventStream adapts a neutral event stream for legacy consumers.
// It is the inverse of ToTurnChatEventStream and has the same cancellation and
// channel-closing guarantees.
func FromTurnChatEventStream(ctx context.Context, events <-chan turn.ChatEvent) <-chan ChatEvent {
	if events == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	out := make(chan ChatEvent)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				converted := FromTurnChatEvent(event)
				select {
				case <-ctx.Done():
					return
				case out <- converted:
				}
			}
		}
	}()
	return out
}

func toTurnObservation(value *ObservationEvent) *turn.ObservationEvent {
	if value == nil {
		return nil
	}
	return &turn.ObservationEvent{
		FrameID:      value.FrameID,
		FilePath:     value.FilePath,
		MimeType:     value.MimeType,
		Width:        value.Width,
		Height:       value.Height,
		ScaleFactor:  value.ScaleFactor,
		DisplayID:    value.DisplayID,
		ActiveWindow: value.ActiveWindow,
	}
}

func fromTurnObservation(value *turn.ObservationEvent) *ObservationEvent {
	if value == nil {
		return nil
	}
	return &ObservationEvent{
		FrameID:      value.FrameID,
		FilePath:     value.FilePath,
		MimeType:     value.MimeType,
		Width:        value.Width,
		Height:       value.Height,
		ScaleFactor:  value.ScaleFactor,
		DisplayID:    value.DisplayID,
		ActiveWindow: value.ActiveWindow,
	}
}

func toTurnApproval(value *ApprovalEvent) *turn.ApprovalEvent {
	if value == nil {
		return nil
	}
	fields := make(map[string]string, len(value.Fields))
	for key, item := range value.Fields {
		fields[key] = item
	}
	return &turn.ApprovalEvent{
		RequestID: value.RequestID,
		Tool:      value.Tool,
		Action:    value.Action,
		Reason:    value.Reason,
		FrameID:   value.FrameID,
		Kind:      value.Kind,
		Prompt:    value.Prompt,
		SessionID: value.SessionID,
		Secure:    value.Secure,
		Fields:    fields,
	}
}

func fromTurnApproval(value *turn.ApprovalEvent) *ApprovalEvent {
	if value == nil {
		return nil
	}
	fields := make(map[string]string, len(value.Fields))
	for key, item := range value.Fields {
		fields[key] = item
	}
	return &ApprovalEvent{
		RequestID: value.RequestID,
		Tool:      value.Tool,
		Action:    value.Action,
		Reason:    value.Reason,
		FrameID:   value.FrameID,
		Kind:      value.Kind,
		Prompt:    value.Prompt,
		SessionID: value.SessionID,
		Secure:    value.Secure,
		Fields:    fields,
	}
}

func toTurnCompact(value *CompactProgress) *turn.CompactProgress {
	if value == nil {
		return nil
	}
	return &turn.CompactProgress{
		Phase:               value.Phase,
		Trigger:             value.Trigger,
		Message:             value.Message,
		BoundaryID:          value.BoundaryID,
		SummarySource:       value.SummarySource,
		Chunk:               value.Chunk,
		Chunks:              value.Chunks,
		PreTokenEstimate:    value.PreTokenEstimate,
		PostTokenEstimate:   value.PostTokenEstimate,
		DroppedMessages:     value.DroppedMessages,
		RetainedMessages:    value.RetainedMessages,
		RestoredAttachments: value.RestoredAttachments,
		Model:               value.Model,
		Error:               value.Error,
	}
}

func fromTurnCompact(value *turn.CompactProgress) *CompactProgress {
	if value == nil {
		return nil
	}
	return &CompactProgress{
		Phase:               value.Phase,
		Trigger:             value.Trigger,
		Message:             value.Message,
		BoundaryID:          value.BoundaryID,
		SummarySource:       value.SummarySource,
		Chunk:               value.Chunk,
		Chunks:              value.Chunks,
		PreTokenEstimate:    value.PreTokenEstimate,
		PostTokenEstimate:   value.PostTokenEstimate,
		DroppedMessages:     value.DroppedMessages,
		RetainedMessages:    value.RetainedMessages,
		RestoredAttachments: value.RestoredAttachments,
		Model:               value.Model,
		Error:               value.Error,
	}
}

func fromTurnTokenUsage(value *turn.TokenUsage) *provider.TokenUsage {
	if value == nil {
		return nil
	}
	return &provider.TokenUsage{
		InputTokens:       value.InputTokens,
		OutputTokens:      value.OutputTokens,
		TotalTokens:       value.TotalTokens,
		CachedInputTokens: value.CachedInputTokens,
		Model:             value.Model,
	}
}
