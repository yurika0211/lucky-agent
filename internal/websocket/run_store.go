package websocket

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

const runStoreRetention = 7 * 24 * time.Hour

type persistedRun struct {
	ID          string               `json:"id"`
	SessionID   string               `json:"session_id"`
	ParentID    string               `json:"parent_id"`
	Message     string               `json:"message"`
	Stream      bool                 `json:"stream"`
	MaxIter     int                  `json:"max_iterations,omitempty"`
	Attachments []gateway.Attachment `json:"attachments,omitempty"`
	TaskID      string               `json:"task_id,omitempty"`
	State       string               `json:"state"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

type persistedSession struct {
	Version   int             `json:"version"`
	SessionID string          `json:"session_id"`
	Runs      []*persistedRun `json:"runs,omitempty"`
	Events    []*Message      `json:"events,omitempty"`
}

type runStore struct {
	root     string
	mu       sync.Mutex
	sessions map[string]*persistedSession
}

func newRunStore(root string) *runStore {
	return &runStore{root: strings.TrimSpace(root), sessions: make(map[string]*persistedSession)}
}

func (s *runStore) sessionLocked(sessionID string) *persistedSession {
	if session := s.sessions[sessionID]; session != nil {
		return session
	}
	session := &persistedSession{Version: 1, SessionID: sessionID}
	if s.root != "" {
		path := filepath.Join(s.root, filepath.Base(fmt.Sprintf("%x.json", stableSessionHash(sessionID))))
		if raw, err := os.ReadFile(path); err == nil {
			var loaded persistedSession
			if json.Unmarshal(raw, &loaded) == nil {
				if loaded.Version == 0 {
					loaded.Version = 1
				}
				if loaded.SessionID == "" {
					loaded.SessionID = sessionID
				}
				session = &loaded
			}
		}
	}
	s.sessions[sessionID] = session
	return session
}

func (s *runStore) saveLocked(session *persistedSession) error {
	if s.root == "" {
		return nil
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.root, filepath.Base(fmt.Sprintf("%x.json", stableSessionHash(session.SessionID))))
	tmp, err := os.CreateTemp(s.root, ".runs-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	encoder := json.NewEncoder(tmp)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(session); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (s *runStore) pruneLocked(session *persistedSession) {
	cutoff := time.Now().Add(-runStoreRetention)
	active := make(map[string]bool)
	keptRuns := session.Runs[:0]
	for _, run := range session.Runs {
		if run == nil {
			continue
		}
		if run.State == "queued" || run.State == "running" {
			active[run.ID] = true
			keptRuns = append(keptRuns, run)
			continue
		}
		if run.UpdatedAt.After(cutoff) {
			keptRuns = append(keptRuns, run)
		}
	}
	session.Runs = keptRuns
	keptEvents := session.Events[:0]
	for _, event := range session.Events {
		if event == nil || event.Timestamp.Before(cutoff) {
			if event == nil || !active[event.RunID] {
				continue
			}
		}
		keptEvents = append(keptEvents, event)
	}
	session.Events = keptEvents
}

func (s *runStore) upsertRun(run persistedRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessionLocked(run.SessionID)
	for i, existing := range session.Runs {
		if existing != nil && existing.ID == run.ID {
			session.Runs[i] = &run
			s.pruneLocked(session)
			return s.saveLocked(session)
		}
	}
	session.Runs = append(session.Runs, &run)
	s.pruneLocked(session)
	return s.saveLocked(session)
}

func (s *runStore) updateRun(sessionID, runID string, update func(*persistedRun)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessionLocked(sessionID)
	for _, run := range session.Runs {
		if run != nil && run.ID == runID {
			update(run)
			run.UpdatedAt = time.Now().UTC()
			s.pruneLocked(session)
			return s.saveLocked(session)
		}
	}
	return nil
}

func (s *runStore) appendEvent(sessionID, runID string, msg *Message) error {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessionLocked(sessionID)
	copyMsg := *msg
	copyMsg.Data = append(json.RawMessage(nil), msg.Data...)
	copyMsg.RunID = runID
	session.Events = append(session.Events, &copyMsg)
	s.pruneLocked(session)
	return s.saveLocked(session)
}

func (s *runStore) replay(sessionID, lastMessageID string) []*Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessionLocked(sessionID)
	start := 0
	if lastMessageID != "" {
		found := false
		for i, event := range session.Events {
			if event != nil && event.ID == lastMessageID {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			start = 0
		}
	}
	result := make([]*Message, 0, len(session.Events)-start)
	for _, event := range session.Events[start:] {
		if event == nil {
			continue
		}
		copyMsg := *event
		copyMsg.Data = append(json.RawMessage(nil), event.Data...)
		result = append(result, &copyMsg)
	}
	return result
}

func (s *runStore) replayForReconnect(sessionID, lastMessageID string) []*Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessionLocked(sessionID)
	start := 0
	if lastMessageID != "" {
		found := false
		for i, event := range session.Events {
			if event != nil && event.ID == lastMessageID {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return cloneEventsForRuns(session.Events, activeRunIDs(session))
		}
		return cloneEvents(session.Events[start:])
	}
	return cloneEventsForRuns(session.Events, activeRunIDs(session))
}

func activeRunIDs(session *persistedSession) map[string]bool {
	active := make(map[string]bool)
	for _, run := range session.Runs {
		if run != nil && (run.State == "queued" || run.State == "running") {
			active[run.ID] = true
		}
	}
	return active
}

func cloneEvents(events []*Message) []*Message {
	result := make([]*Message, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		copyMsg := *event
		copyMsg.Data = append(json.RawMessage(nil), event.Data...)
		result = append(result, &copyMsg)
	}
	return result
}

func cloneEventsForRuns(events []*Message, runIDs map[string]bool) []*Message {
	result := make([]*Message, 0, len(events))
	for _, event := range events {
		if event == nil || !runIDs[event.RunID] {
			continue
		}
		copyMsg := *event
		copyMsg.Data = append(json.RawMessage(nil), event.Data...)
		result = append(result, &copyMsg)
	}
	return result
}

func (s *runStore) activeRuns(sessionID string) []persistedRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessionLocked(sessionID)
	result := make([]persistedRun, 0)
	for _, run := range session.Runs {
		if run != nil && (run.State == "queued" || run.State == "running") {
			result = append(result, *run)
		}
	}
	return result
}

func (s *runStore) restoreable() []persistedRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root != "" {
		if paths, err := filepath.Glob(filepath.Join(s.root, "*.json")); err == nil {
			for _, path := range paths {
				raw, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				var loaded persistedSession
				if json.Unmarshal(raw, &loaded) == nil && loaded.SessionID != "" {
					s.sessions[loaded.SessionID] = &loaded
				}
			}
		}
	}
	var result []persistedRun
	for _, session := range s.sessions {
		for _, run := range session.Runs {
			if run != nil && (run.State == "queued" || run.State == "running") {
				result = append(result, *run)
			}
		}
	}
	return result
}

func stableSessionHash(sessionID string) uint64 {
	var hash uint64 = 1469598103934665603
	for i := 0; i < len(sessionID); i++ {
		hash ^= uint64(sessionID[i])
		hash *= 1099511628211
	}
	return hash
}
