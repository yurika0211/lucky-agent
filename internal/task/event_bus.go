package task

import (
	"strings"
	"time"
)

type EventBus struct {
	store Store
}

func NewEventBus(store Store) *EventBus {
	return &EventBus{store: store}
}

func (b *EventBus) Emit(event Event) error {
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	return b.store.AppendEvent(event)
}

func eventMetadata(record Record) map[string]string {
	meta := map[string]string{}
	for key, value := range record.Metadata {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		meta[key] = value
	}
	if description := strings.TrimSpace(record.Description); description != "" {
		meta["description"] = description
	}
	if title := strings.TrimSpace(record.Metadata["title"]); title != "" {
		meta["title"] = title
	}
	sessionID := strings.TrimSpace(record.Metadata["session_id"])
	if sessionID == "" {
		sessionID = strings.TrimSpace(record.Metadata["owner_session_id"])
	}
	if sessionID != "" {
		meta["session_id"] = sessionID
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func (b *EventBus) Created(record Record) error {
	return b.Emit(Event{
		Type:     EventCreated,
		TaskID:   record.ID,
		ParentID: record.ParentID,
		Status:   record.Status,
		Mode:     record.Mode,
		Message:  record.Description,
		Metadata: eventMetadata(record),
	})
}

func (b *EventBus) Started(record Record) error {
	return b.Emit(Event{
		Type:     EventStarted,
		TaskID:   record.ID,
		ParentID: record.ParentID,
		Status:   StatusRunning,
		Mode:     record.Mode,
		Metadata: eventMetadata(record),
	})
}

func (b *EventBus) Completed(record Record, message string) error {
	return b.Emit(Event{
		Type:     EventCompleted,
		TaskID:   record.ID,
		ParentID: record.ParentID,
		Status:   StatusCompleted,
		Mode:     record.Mode,
		Message:  message,
		Metadata: eventMetadata(record),
	})
}

func (b *EventBus) Failed(record Record, errText string) error {
	return b.Emit(Event{
		Type:     EventFailed,
		TaskID:   record.ID,
		ParentID: record.ParentID,
		Status:   StatusFailed,
		Mode:     record.Mode,
		Error:    errText,
		Metadata: eventMetadata(record),
	})
}
