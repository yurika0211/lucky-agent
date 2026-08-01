package feishu

import (
	"sync"
	"time"
)

const (
	outboundMessageTTL = 24 * time.Hour
	eventDedupTTL      = 30 * time.Minute
	maxTrackedIDs      = 4096
)

// expiringIDSet is a small bounded set for retry deduplication and reply
// correlation. The bounded O(n) eviction path only runs when the cap is hit.
type expiringIDSet struct {
	mu      sync.Mutex
	entries map[string]time.Time
	ttl     time.Duration
	max     int
}

func newExpiringIDSet(ttl time.Duration, max int) *expiringIDSet {
	return &expiringIDSet{
		entries: make(map[string]time.Time),
		ttl:     ttl,
		max:     max,
	}
}

func (s *expiringIDSet) add(id string, now time.Time) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	s.makeRoomLocked(id)
	s.entries[id] = now.Add(s.ttl)
}

func (s *expiringIDSet) contains(id string, now time.Time) bool {
	if s == nil || id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt, ok := s.entries[id]
	if !ok {
		return false
	}
	if !now.Before(expiresAt) {
		delete(s.entries, id)
		return false
	}
	return true
}

// seenOrAdd returns true when id was already present and unexpired.
func (s *expiringIDSet) seenOrAdd(id string, now time.Time) bool {
	if s == nil || id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	if expiresAt, ok := s.entries[id]; ok && now.Before(expiresAt) {
		return true
	}
	s.makeRoomLocked(id)
	s.entries[id] = now.Add(s.ttl)
	return false
}

func (s *expiringIDSet) pruneLocked(now time.Time) {
	for id, expiresAt := range s.entries {
		if !now.Before(expiresAt) {
			delete(s.entries, id)
		}
	}
}

func (s *expiringIDSet) makeRoomLocked(incomingID string) {
	if s.max <= 0 {
		return
	}
	if _, exists := s.entries[incomingID]; exists {
		return
	}
	for len(s.entries) >= s.max {
		var oldestID string
		var oldestExpiry time.Time
		for id, expiresAt := range s.entries {
			if oldestID == "" || expiresAt.Before(oldestExpiry) {
				oldestID = id
				oldestExpiry = expiresAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(s.entries, oldestID)
	}
}
