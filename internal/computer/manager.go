package computer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrStaleFrame     = errors.New("computer: stale observation")
	ErrStepLimit      = errors.New("computer: session step limit reached")
	ErrInvalidSession = errors.New("computer: session id is required")
)

type ManagerConfig struct {
	StorageDir          string
	FrameTTL            time.Duration
	KeepFrames          int
	Settle              time.Duration
	MaxSteps            int
	MaxObservationBytes int
	MaxScreenshotWidth  int
}

func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		FrameTTL: 10 * time.Minute, KeepFrames: 2, Settle: 350 * time.Millisecond,
		MaxSteps: 20, MaxObservationBytes: 10 << 20, MaxScreenshotWidth: 0,
	}
}

type ManagerOption func(*ManagerConfig)

func WithStorageDir(dir string) ManagerOption { return func(c *ManagerConfig) { c.StorageDir = dir } }
func WithFrameTTL(ttl time.Duration) ManagerOption {
	return func(c *ManagerConfig) { c.FrameTTL = ttl }
}
func WithKeepFrames(n int) ManagerOption            { return func(c *ManagerConfig) { c.KeepFrames = n } }
func WithSettleDelay(d time.Duration) ManagerOption { return func(c *ManagerConfig) { c.Settle = d } }
func WithMaxSteps(n int) ManagerOption              { return func(c *ManagerConfig) { c.MaxSteps = n } }

type sessionState struct {
	mu            sync.Mutex
	latestFrameID string
	latest        Observation
	sequence      uint64
	steps         int
	closed        bool
}

// Manager serializes desktop control globally and statefully tracks each session.
type Manager struct {
	backend   Backend
	store     *FrameStore
	config    ManagerConfig
	desktopMu sync.Mutex
	mu        sync.Mutex
	sessions  map[string]*sessionState
	closed    map[string]bool
}

func NewManager(backend Backend, options ...ManagerOption) (*Manager, error) {
	if backend == nil {
		return nil, errors.New("computer: backend is required")
	}
	cfg := DefaultManagerConfig()
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	if cfg.StorageDir == "" {
		cfg.StorageDir = filepath.Join(os.TempDir(), "luckyagent-computer")
	}
	store, err := NewFrameStore(cfg.StorageDir, cfg.KeepFrames, cfg.FrameTTL)
	if err != nil {
		return nil, err
	}
	store.maxBytes = cfg.MaxObservationBytes
	return &Manager{backend: backend, store: store, config: cfg, sessions: make(map[string]*sessionState), closed: make(map[string]bool)}, nil
}

func NewManagerWithConfig(backend Backend, cfg ManagerConfig) (*Manager, error) {
	return NewManager(backend, func(c *ManagerConfig) { *c = cfg })
}

func (m *Manager) FrameStore() *FrameStore { return m.store }

func (m *Manager) getSession(id string) (*sessionState, error) {
	if id == "" {
		return nil, ErrInvalidSession
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed[id] {
		return nil, fmt.Errorf("computer: session %q is closed", id)
	}
	s := m.sessions[id]
	if s == nil {
		s = &sessionState{}
		m.sessions[id] = s
	}
	return s, nil
}

func (m *Manager) Observe(ctx context.Context, sessionID string, req ObserveRequest) (Observation, error) {
	s, err := m.getSession(sessionID)
	if err != nil {
		return Observation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Observation{}, fmt.Errorf("computer: session %q is closed", sessionID)
	}
	if req.Wait > 0 {
		if err := waitContext(ctx, req.Wait); err != nil {
			return Observation{}, err
		}
	}
	return m.captureLocked(ctx, sessionID, s, req.Target)
}

func (m *Manager) Step(ctx context.Context, sessionID string, action Action) (Observation, error) {
	s, err := m.getSession(sessionID)
	if err != nil {
		return Observation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Observation{}, fmt.Errorf("computer: session %q is closed", sessionID)
	}
	if err := action.Validate(); err != nil {
		return Observation{}, err
	}
	if s.latestFrameID == "" {
		return Observation{}, errors.New("computer: observe before acting")
	}
	if action.FrameID == "" || action.FrameID != s.latestFrameID {
		return Observation{}, fmt.Errorf("%w: expected %s, got %s; observe the current screen before acting", ErrStaleFrame, s.latestFrameID, action.FrameID)
	}
	if err := validateActionBounds(action, s.latest); err != nil {
		return Observation{}, err
	}
	if m.config.MaxSteps > 0 && s.steps >= m.config.MaxSteps {
		return Observation{}, ErrStepLimit
	}
	m.desktopMu.Lock()
	defer m.desktopMu.Unlock()
	if err := m.backend.Perform(ctx, action); err != nil {
		return Observation{}, fmt.Errorf("computer: perform %s: %w", action.Kind, err)
	}
	s.steps++
	if m.config.Settle > 0 {
		if err := waitContext(ctx, m.config.Settle); err != nil {
			return Observation{}, err
		}
	}
	return m.captureUnlocked(ctx, sessionID, s, Target{DisplayID: action.DisplayID})
}

func (m *Manager) captureLocked(ctx context.Context, sessionID string, s *sessionState, target Target) (Observation, error) {
	m.desktopMu.Lock()
	defer m.desktopMu.Unlock()
	return m.captureUnlocked(ctx, sessionID, s, target)
}

func (m *Manager) captureUnlocked(ctx context.Context, sessionID string, s *sessionState, target Target) (Observation, error) {
	obs, err := m.backend.Capture(ctx, target)
	if err != nil {
		return Observation{}, fmt.Errorf("computer: capture: %w", err)
	}
	if m.config.MaxScreenshotWidth > 0 && obs.Width > m.config.MaxScreenshotWidth {
		if obs.CleanupFile && obs.FilePath != "" {
			_ = os.Remove(obs.FilePath)
		}
		return Observation{}, fmt.Errorf("computer: screenshot width %d exceeds configured maximum %d", obs.Width, m.config.MaxScreenshotWidth)
	}
	s.sequence++
	obs.FrameID = fmt.Sprintf("frame-%d", s.sequence)
	if obs.CapturedAt.IsZero() {
		obs.CapturedAt = time.Now().UTC()
	}
	if obs.ScaleFactor <= 0 {
		obs.ScaleFactor = 1
	}
	sourcePath := obs.FilePath
	cleanupSource := obs.CleanupFile
	obs, err = m.store.Save(sessionID, s.sequence, obs)
	if err != nil {
		if cleanupSource && sourcePath != "" {
			_ = os.Remove(sourcePath)
		}
		return Observation{}, err
	}
	if cleanupSource && sourcePath != "" && sourcePath != obs.FilePath {
		_ = os.Remove(sourcePath)
	}
	s.latestFrameID = obs.FrameID
	s.latest = obs
	return obs, nil
}

func validateActionBounds(action Action, obs Observation) error {
	if obs.Width <= 0 || obs.Height <= 0 {
		return nil
	}
	valid := func(x, y int) bool { return x >= 0 && y >= 0 && x < obs.Width && y < obs.Height }
	switch action.Kind {
	case ActionClick, ActionDoubleClick, ActionMove:
		if !valid(action.X, action.Y) {
			return fmt.Errorf("computer: %s coordinate (%d,%d) outside frame %dx%d", action.Kind, action.X, action.Y, obs.Width, obs.Height)
		}
	case ActionDrag:
		if !valid(action.X, action.Y) || !valid(action.EndX, action.EndY) {
			return fmt.Errorf("computer: drag coordinates outside frame %dx%d", obs.Width, obs.Height)
		}
	}
	return nil
}

func (m *Manager) CloseSession(sessionID string) error {
	if sessionID == "" {
		return ErrInvalidSession
	}
	m.mu.Lock()
	s := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.closed[sessionID] = true
	m.mu.Unlock()
	if s != nil {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	}
	return m.store.RemoveSession(sessionID)
}

func (m *Manager) Close() error {
	m.mu.Lock()
	sessions := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		sessions = append(sessions, id)
	}
	m.mu.Unlock()
	var first error
	for _, id := range sessions {
		if err := m.CloseSession(id); err != nil && first == nil {
			first = err
		}
	}
	if err := m.backend.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

func (m *Manager) Cleanup() error { return m.store.Cleanup() }

func waitContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
