package maintain

import (
	"strings"
	"sync"
)

// Config controls the cadence of background memory maintenance.
// A zero value uses the default maintenance cadence.
type Config struct {
	DecayEvery     uint64
	SummarizeEvery uint64
	ExpireEvery    uint64
}

const (
	defaultMaintenanceDecayEvery     uint64 = 10
	defaultMaintenanceSummarizeEvery uint64 = 20
	defaultMaintenanceExpireEvery    uint64 = 50
)

// DefaultConfig returns the library's default maintenance cadence.
func DefaultConfig() Config {
	return Config{
		DecayEvery:     defaultMaintenanceDecayEvery,
		SummarizeEvery: defaultMaintenanceSummarizeEvery,
		ExpireEvery:    defaultMaintenanceExpireEvery,
	}
}

func (c Config) normalized() Config {
	defaults := DefaultConfig()
	if c.DecayEvery == 0 {
		c.DecayEvery = defaults.DecayEvery
	}
	if c.SummarizeEvery == 0 {
		c.SummarizeEvery = defaults.SummarizeEvery
	}
	if c.ExpireEvery == 0 {
		c.ExpireEvery = defaults.ExpireEvery
	}
	return c
}

// Event is the immutable result of recording one completed turn.
type Event struct {
	SessionID    string
	RuntimeCount uint64
	SessionCount uint64
	RunDecay     bool
	RunSummarize bool
	RunExpire    bool
}

// Coordinator owns turn counters and maintenance cadence for a memory runtime.
type Coordinator struct {
	mu            sync.Mutex
	config        Config
	runtimeCount  uint64
	sessionCounts map[string]uint64
}

// NewCoordinator creates a coordinator with the supplied cadence.
func NewCoordinator(config Config) *Coordinator {
	return &Coordinator{
		config:        config.normalized(),
		sessionCounts: make(map[string]uint64),
	}
}

// RecordTurn records one completed conversation turn and returns the
// maintenance actions that should be performed by the memory owner.
func (c *Coordinator) RecordTurn(sessionID string) Event {
	if c == nil {
		return Event{}
	}

	sessionID = strings.TrimSpace(sessionID)
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.config.DecayEvery == 0 || c.config.SummarizeEvery == 0 || c.config.ExpireEvery == 0 {
		c.config = c.config.normalized()
	}
	if c.sessionCounts == nil {
		c.sessionCounts = make(map[string]uint64)
	}

	c.runtimeCount++
	sessionCount := uint64(0)
	if sessionID != "" {
		c.sessionCounts[sessionID]++
		sessionCount = c.sessionCounts[sessionID]
	}

	return Event{
		SessionID:    sessionID,
		RuntimeCount: c.runtimeCount,
		SessionCount: sessionCount,
		RunDecay:     c.runtimeCount%c.config.DecayEvery == 0,
		RunSummarize: c.runtimeCount%c.config.SummarizeEvery == 0,
		RunExpire:    c.runtimeCount%c.config.ExpireEvery == 0,
	}
}

// RuntimeCount returns the number of completed turns recorded by the runtime.
func (c *Coordinator) RuntimeCount() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runtimeCount
}

// SessionCount returns the number of completed turns attributed to a session.
func (c *Coordinator) SessionCount(sessionID string) uint64 {
	if c == nil {
		return 0
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionCounts[sessionID]
}

// ForgetSession removes a session counter after its session is deleted.  It
// is optional because the runtime cadence does not depend on this map.
func (c *Coordinator) ForgetSession(sessionID string) {
	if c == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessionCounts, sessionID)
}
