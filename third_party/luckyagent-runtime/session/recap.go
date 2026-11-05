package session

import (
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

// Recap returns the user-facing preview without loading the transcript.
func (s *Session) Recap() (string, *time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recap, cloneRecapTime(s.recapUpdatedAt)
}

func cloneRecapTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	copy := *t
	return &copy
}

// RecapSnapshot captures recent evidence and its version atomically. The caller
// must not mutate nested message data. Recaps are distinct from compact summaries.
func (s *Session) RecapSnapshot(limit int) (string, []provider.Message, time.Time, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.checkLiveLocked(); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := s.loadMessages(); err != nil {
		return "", nil, time.Time{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.recapUpdatedAt != nil && !s.recapUpdatedAt.Before(s.UpdatedAt) {
		return s.recap, nil, s.UpdatedAt, nil
	}
	start := 0
	if limit > 0 && len(s.Messages) > limit {
		start = len(s.Messages) - limit
	}
	messages := append([]provider.Message(nil), s.Messages[start:]...)
	// A long tool loop can push the user's request outside the bounded tail.
	// Retain that request so generation still knows the task and its language.
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role == "user" {
			if i < start {
				messages = append([]provider.Message{s.Messages[i]}, messages...)
			}
			break
		}
	}
	return s.recap, messages, s.UpdatedAt, nil
}

// UpdateRecap persists a generated preview only while its source is current.
// It never changes conversation ordering, title or transcript. Failed writes
// restore the previous preview; deletion and ID reuse cannot revive old handles.
func (s *Session) UpdateRecap(text string, sourceUpdatedAt time.Time) (bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, nil
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.checkLiveLocked(); err != nil {
		return false, err
	}
	s.mu.Lock()
	if !s.UpdatedAt.Equal(sourceUpdatedAt) || (s.recapUpdatedAt != nil && !s.recapUpdatedAt.Before(sourceUpdatedAt)) {
		s.mu.Unlock()
		return false, nil
	}
	previous, previousAt := s.recap, s.recapUpdatedAt
	now := time.Now().UTC()
	s.recap, s.recapUpdatedAt = text, &now
	s.mu.Unlock()
	if err := s.saveMetadataLocked(); err != nil {
		s.mu.Lock()
		s.recap, s.recapUpdatedAt = previous, previousAt
		s.mu.Unlock()
		return false, err
	}
	return true, nil
}
