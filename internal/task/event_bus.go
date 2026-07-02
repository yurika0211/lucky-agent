package task

import "time"

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

func (b *EventBus) Created(record Record) error {
	return b.Emit(Event{
		Type:     EventCreated,
		TaskID:   record.ID,
		ParentID: record.ParentID,
		Status:   record.Status,
		Mode:     record.Mode,
		Message:  record.Description,
	})
}

func (b *EventBus) Started(record Record) error {
	return b.Emit(Event{
		Type:     EventStarted,
		TaskID:   record.ID,
		ParentID: record.ParentID,
		Status:   StatusRunning,
		Mode:     record.Mode,
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
	})
}
