package memory

import (
	"strings"
	"sync"
)

// MaintenanceConfig controls the cadence of background memory maintenance.
// A zero value uses the historical LuckyAgent defaults.
//
// The counters deliberately live in the memory package.  Maintenance is a
// property of the process-wide memory store, not of the agent that happens to
// execute a turn.  Session IDs are retained for attribution and future
// session-scoped policies, while the cadence below remains runtime-wide to
// preserve the existing behavior.
type MaintenanceConfig struct {
	DecayEvery     uint64
	SummarizeEvery uint64
	ExpireEvery    uint64
}

const (
	defaultMaintenanceDecayEvery     uint64 = 10
	defaultMaintenanceSummarizeEvery uint64 = 20
	defaultMaintenanceExpireEvery    uint64 = 50
)

// DefaultMaintenanceConfig returns the cadence used by the legacy agent
// implementation.
func DefaultMaintenanceConfig() MaintenanceConfig {
	return MaintenanceConfig{
		DecayEvery:     defaultMaintenanceDecayEvery,
		SummarizeEvery: defaultMaintenanceSummarizeEvery,
		ExpireEvery:    defaultMaintenanceExpireEvery,
	}
}

func (c MaintenanceConfig) normalized() MaintenanceConfig {
	defaults := DefaultMaintenanceConfig()
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

// MaintenanceEvent is the immutable result of recording one completed turn.
// Each event owns a unique runtime count, so concurrent callers cannot both
// observe the same maintenance boundary.
type MaintenanceEvent struct {
	SessionID    string
	RuntimeCount uint64
	SessionCount uint64
	RunDecay     bool
	RunSummarize bool
	RunExpire    bool
}

// MaintenanceCoordinator owns turn counters and maintenance cadence for a
// memory runtime.  It is safe for concurrent use.
//
// The runtime counter is the source of truth for maintenance scheduling.  A
// per-session counter is kept separately so callers can inspect attribution
// without making unrelated sessions share mutable state in Agent.
type MaintenanceCoordinator struct {
	mu            sync.Mutex
	config        MaintenanceConfig
	runtimeCount  uint64
	sessionCounts map[string]uint64
}

// NewMaintenanceCoordinator creates a coordinator with the supplied cadence.
func NewMaintenanceCoordinator(config MaintenanceConfig) *MaintenanceCoordinator {
	return &MaintenanceCoordinator{
		config:        config.normalized(),
		sessionCounts: make(map[string]uint64),
	}
}

// RecordTurn records one completed conversation turn and returns the
// maintenance actions that should be performed by the memory owner.
func (c *MaintenanceCoordinator) RecordTurn(sessionID string) MaintenanceEvent {
	if c == nil {
		return MaintenanceEvent{}
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

	return MaintenanceEvent{
		SessionID:    sessionID,
		RuntimeCount: c.runtimeCount,
		SessionCount: sessionCount,
		RunDecay:     c.runtimeCount%c.config.DecayEvery == 0,
		RunSummarize: c.runtimeCount%c.config.SummarizeEvery == 0,
		RunExpire:    c.runtimeCount%c.config.ExpireEvery == 0,
	}
}

// RuntimeCount returns the number of completed turns recorded by the runtime.
func (c *MaintenanceCoordinator) RuntimeCount() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runtimeCount
}

// SessionCount returns the number of completed turns attributed to a session.
func (c *MaintenanceCoordinator) SessionCount(sessionID string) uint64 {
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
func (c *MaintenanceCoordinator) ForgetSession(sessionID string) {
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

// RecordTurn records a completed turn against the store's memory runtime.
// Keeping this forwarding method on Store lets callers avoid owning a second
// coordinator in Agent while preserving the zero value of Store for tests.
func (s *Store) RecordTurn(sessionID string) MaintenanceEvent {
	if s == nil {
		return MaintenanceEvent{}
	}
	s.maintenanceOnce.Do(func() {
		s.maintenance = NewMaintenanceCoordinator(DefaultMaintenanceConfig())
	})
	return s.maintenance.RecordTurn(sessionID)
}

// MaintenanceCoordinator returns the store-owned coordinator for diagnostics
// and lifecycle integrations.  Most callers should use RecordTurn instead.
func (s *Store) MaintenanceCoordinator() *MaintenanceCoordinator {
	if s == nil {
		return nil
	}
	s.maintenanceOnce.Do(func() {
		s.maintenance = NewMaintenanceCoordinator(DefaultMaintenanceConfig())
	})
	return s.maintenance
}

// ForgetSession releases the coordinator's per-session attribution after a
// session is deleted. The process-wide cadence is intentionally preserved.
func (s *Store) ForgetSession(sessionID string) {
	if s == nil {
		return
	}
	s.MaintenanceCoordinator().ForgetSession(sessionID)
}
