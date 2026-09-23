package computer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yurika0211/luckyagent/internal/logger"
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
	ActionSettle        time.Duration
	MaxSteps            int
	MaxObservationBytes int
	MaxScreenshotWidth  int
	AllowedWindows      []string
	MaxBatchActions     int
	SettleMode          string
}

func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		FrameTTL: 10 * time.Minute, KeepFrames: 2, Settle: 350 * time.Millisecond, ActionSettle: 100 * time.Millisecond,
		MaxSteps: 20, MaxObservationBytes: 10 << 20, MaxScreenshotWidth: 0,
		MaxBatchActions: 5, SettleMode: "adaptive",
	}
}

type ManagerOption func(*ManagerConfig)

func WithStorageDir(dir string) ManagerOption { return func(c *ManagerConfig) { c.StorageDir = dir } }
func WithFrameTTL(ttl time.Duration) ManagerOption {
	return func(c *ManagerConfig) { c.FrameTTL = ttl }
}
func WithKeepFrames(n int) ManagerOption            { return func(c *ManagerConfig) { c.KeepFrames = n } }
func WithSettleDelay(d time.Duration) ManagerOption { return func(c *ManagerConfig) { c.Settle = d } }
func WithActionSettleDelay(d time.Duration) ManagerOption {
	return func(c *ManagerConfig) { c.ActionSettle = d }
}
func WithMaxSteps(n int) ManagerOption { return func(c *ManagerConfig) { c.MaxSteps = n } }
func WithAllowedWindows(names []string) ManagerOption {
	return func(c *ManagerConfig) { c.AllowedWindows = append([]string(nil), names...) }
}

type sessionState struct {
	mu            sync.Mutex
	latestFrameID string
	latest        Observation
	sequence      uint64
	steps         int
	closed        bool
	request       ObserveRequest
	revision      uint64
}

// Manager serializes desktop control globally and statefully tracks each session.
type Manager struct {
	backend   Backend
	store     *FrameStore
	config    ManagerConfig
	desktopMu chan struct{}
	revision  uint64 // guarded by desktopMu; invalidates frames after any session acts
	mu        sync.Mutex
	sessions  map[string]*sessionState
	closed    map[string]bool
	actionSeq uint64
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
	cfg.AllowedWindows = normalizeAllowedWindows(cfg.AllowedWindows)
	if cfg.MaxBatchActions <= 0 {
		cfg.MaxBatchActions = 5
	}
	if cfg.MaxBatchActions > 10 {
		return nil, errors.New("computer: max_batch_actions must be at most 10")
	}
	if cfg.SettleMode == "" {
		cfg.SettleMode = "adaptive"
	}
	if cfg.SettleMode != "fixed" && cfg.SettleMode != "adaptive" {
		return nil, errors.New("computer: settle_mode must be fixed or adaptive")
	}
	if cfg.ActionSettle < 0 {
		cfg.ActionSettle = 0
	}
	if cfg.Settle <= 0 {
		cfg.ActionSettle = 0
	}
	store, err := NewFrameStore(cfg.StorageDir, cfg.KeepFrames, cfg.FrameTTL)
	if err != nil {
		return nil, err
	}
	store.maxBytes = cfg.MaxObservationBytes
	return &Manager{backend: backend, store: store, config: cfg, desktopMu: make(chan struct{}, 1), sessions: make(map[string]*sessionState), closed: make(map[string]bool)}, nil
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
	if req.Format == "" {
		req.Format = "image"
	}
	if req.Format != "image" && req.Format != "tree" && req.Format != "both" {
		return Observation{}, errors.New("computer: format must be image, tree, or both")
	}
	if req.Target.Region != nil {
		region := *req.Target.Region
		req.Target.Region = &region
		if req.Format == "tree" {
			return Observation{}, errors.New("computer: region requires image or both format")
		}
	}
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
	if err := m.lockDesktop(ctx); err != nil {
		return Observation{}, err
	}
	defer m.unlockDesktop()
	return m.captureUnlocked(ctx, sessionID, s, req, false)
}

func (m *Manager) Step(ctx context.Context, sessionID string, action Action) (Observation, error) {
	return m.StepBatch(ctx, sessionID, []Action{action})
}

// StepBatch executes a short, predetermined macro under one desktop lease and
// returns one final observation. All policies are checked before the first input.
func (m *Manager) StepBatch(ctx context.Context, sessionID string, actions []Action) (Observation, error) {
	if len(actions) == 0 || len(actions) > m.config.MaxBatchActions {
		return Observation{}, fmt.Errorf("computer: batch requires 1..%d actions", m.config.MaxBatchActions)
	}
	s, err := m.getSession(sessionID)
	if err != nil {
		return Observation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Observation{}, fmt.Errorf("computer: session %q is closed", sessionID)
	}
	if s.latestFrameID == "" {
		return Observation{}, errors.New("computer: observe before acting")
	}
	if m.config.FrameTTL > 0 && time.Since(s.latest.CapturedAt) >= m.config.FrameTTL {
		return Observation{}, fmt.Errorf("%w: observation expired; observe again", ErrStaleFrame)
	}
	for i, action := range actions {
		if err := action.Validate(); err != nil {
			return Observation{}, err
		}
		if s.request.Format == "tree" && action.Kind != ActionInvoke && action.Kind != ActionSetText && action.Kind != ActionFocus {
			return Observation{}, errors.New("computer: tree-only observations support invoke, set_text, and focus; observe an image before keyboard or pointer actions")
		}
		if action.FrameID == "" || action.FrameID != s.latestFrameID {
			return Observation{}, fmt.Errorf("%w: expected %s, got %s; observe the current screen before acting", ErrStaleFrame, s.latestFrameID, action.FrameID)
		}
		if action.DisplayID != "" && action.DisplayID != s.latest.DisplayID {
			return Observation{}, errors.New("computer: action display differs from observed display")
		}
		if i > 0 && (pointerAction(action.Kind) || action.Kind == ActionInvoke || action.Kind == ActionFocus) {
			return Observation{}, errors.New("computer: only the first batch action may point, invoke, or focus; observe again before choosing another target")
		}
		if pointerAction(action.Kind) && (s.latest.Width <= 0 || s.latest.Height <= 0) {
			return Observation{}, errors.New("computer: pointer actions require an image observation")
		}
		if err := validateActionBounds(action, s.latest); err != nil {
			return Observation{}, err
		}
		if err := validateElement(action, s.latest.Accessibility); err != nil {
			return Observation{}, err
		}
	}
	if err := validateAllowedWindow(s.latest.ActiveWindow, m.config.AllowedWindows); err != nil {
		return Observation{}, err
	}
	if m.config.MaxSteps > 0 && len(actions) > m.config.MaxSteps-s.steps {
		return Observation{}, ErrStepLimit
	}
	if err := m.lockDesktop(ctx); err != nil {
		return Observation{}, err
	}
	defer m.unlockDesktop()
	if s.revision != m.revision {
		return Observation{}, fmt.Errorf("%w: another session changed the desktop; observe again", ErrStaleFrame)
	}
	requestID := m.nextActionRequestID()
	executions := make([]ActionExecution, 0, len(actions))
	for i, action := range actions {
		if err := ctx.Err(); err != nil {
			return Observation{}, &BatchError{Completed: i, Err: err}
		}
		if validator, ok := m.backend.(observationValidator); ok {
			if err := validator.ValidateObservation(ctx, s.latest, action); err != nil {
				return Observation{}, &BatchError{Completed: i, Err: err}
			}
		}
		// Even a failed backend call may have injected part of an action.
		action.RequestID = requestID
		startedAt := time.Now().UTC()
		execution := ActionExecution{
			RequestID:          requestID,
			Index:              i,
			Kind:               action.Kind,
			StartedAt:          startedAt,
			InputX:             action.X,
			InputY:             action.Y,
			InputEndX:          action.EndX,
			InputEndY:          action.EndY,
			ActiveWindowBefore: s.latest.ActiveWindow,
			CursorBeforeX:      s.latest.CursorX,
			CursorBeforeY:      s.latest.CursorY,
		}
		movedAction := desktopAction(action, s.latest)
		execution.BackendX = movedAction.X
		execution.BackendY = movedAction.Y
		execution.BackendEndX = movedAction.EndX
		execution.BackendEndY = movedAction.EndY
		s.latestFrameID = ""
		m.revision++
		s.steps++
		movedAction.DisplayID = s.latest.DisplayID
		if err := m.backend.Perform(ctx, movedAction); err != nil {
			execution.CompletedAt = time.Now().UTC()
			execution.DurationMS = execution.CompletedAt.Sub(startedAt).Milliseconds()
			execution.Error = err.Error()
			logger.Warn("computer action failed", "request_id", requestID, "action_index", i, "action", action.Kind, "duration_ms", execution.DurationMS, "error", err)
			return Observation{}, &BatchError{Completed: i, Err: fmt.Errorf("request_id=%s action_index=%d: %w", requestID, i, err)}
		}
		execution.CompletedAt = time.Now().UTC()
		execution.DurationMS = execution.CompletedAt.Sub(startedAt).Milliseconds()
		execution.Completed = true
		executions = append(executions, execution)
		logger.Info("computer action executed", "request_id", requestID, "action_index", i, "action", action.Kind, "input_x", action.X, "input_y", action.Y, "backend_x", movedAction.X, "backend_y", movedAction.Y, "duration_ms", execution.DurationMS)
		if i+1 < len(actions) && m.config.ActionSettle > 0 {
			if err := waitContext(ctx, m.config.ActionSettle); err != nil {
				return Observation{}, &BatchError{Completed: i + 1, Err: err}
			}
		}
	}
	obs, err := m.captureUnlocked(ctx, sessionID, s, s.request, true)
	if err != nil {
		return Observation{}, fmt.Errorf("computer: post-action observation request_id=%s: %w", requestID, err)
	}
	for i := range executions {
		executions[i].ActiveWindowAfter = obs.ActiveWindow
		executions[i].CursorAfterX = obs.CursorX
		executions[i].CursorAfterY = obs.CursorY
	}
	obs.ActionResults = executions
	return obs, nil
}

func (m *Manager) nextActionRequestID() string {
	return fmt.Sprintf("act-%d", atomic.AddUint64(&m.actionSeq, 1))
}

type BatchError struct {
	Completed int
	Err       error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("computer: action sequence stopped after %d completed actions: %v; the failing action may be partial; observe before continuing, do not replay completed actions", e.Completed, e.Err)
}
func (e *BatchError) Unwrap() error { return e.Err }

func pointerAction(kind ActionKind) bool {
	return kind == ActionClick || kind == ActionDoubleClick || kind == ActionMove || kind == ActionDrag
}

func validateElement(action Action, tree *AccessibilityTree) error {
	if action.Kind != ActionInvoke && action.Kind != ActionSetText && action.Kind != ActionFocus {
		return nil
	}
	if tree != nil {
		for _, node := range tree.Nodes {
			if node.ID == action.ElementID {
				if !node.Enabled {
					return errors.New("computer: accessibility element is disabled")
				}
				if action.Kind == ActionSetText && !node.Editable {
					return errors.New("computer: accessibility element is not editable")
				}
				return nil
			}
		}
	}
	return errors.New("computer: element_id is not in the latest accessibility observation")
}

func (m *Manager) lockDesktop(ctx context.Context) error {
	select {
	case m.desktopMu <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.unlockDesktop()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) unlockDesktop() { <-m.desktopMu }

func (m *Manager) captureUnlocked(ctx context.Context, sessionID string, s *sessionState, req ObserveRequest, settle bool) (Observation, error) {
	var obs Observation
	var err error
	if req.Format != "tree" {
		obs, err = m.captureSettled(ctx, req.Target, settle)
	} else if settle && m.config.Settle > 0 {
		err = waitContext(ctx, m.config.Settle)
	}
	if err != nil {
		return Observation{}, fmt.Errorf("computer: capture: %w", err)
	}
	sourcePath, cleanupSource := obs.FilePath, obs.CleanupFile
	savedPath := ""
	defer func() {
		if cleanupSource && sourcePath != "" && sourcePath != savedPath {
			_ = os.Remove(sourcePath)
		}
	}()
	if req.Format == "tree" || req.Format == "both" {
		backend, ok := m.backend.(accessibilityBackend)
		if !ok {
			return Observation{}, errors.New("computer: accessibility is unavailable on this backend")
		}
		tree, treeErr := backend.Accessibility(ctx, req.Target)
		if treeErr != nil {
			return Observation{}, treeErr
		}
		obs.Accessibility = &tree
		if obs.ActiveWindow == "" {
			obs.ActiveWindow = tree.Window
		}
	}
	if obs.CaptureBounds.Width == 0 && obs.Width > 0 {
		obs.CaptureBounds = Rect{X: obs.OriginX, Y: obs.OriginY, Width: obs.Width, Height: obs.Height}
	}
	obs, err = cropScreenshot(ctx, obs, req.Target.Region)
	if err != nil {
		return Observation{}, err
	}
	obs, err = fitScreenshot(ctx, obs, m.config.MaxScreenshotWidth, m.config.MaxObservationBytes)
	if err != nil {
		return Observation{}, err
	}
	s.sequence++
	obs.FrameID = fmt.Sprintf("frame-%d", s.sequence)
	if obs.CapturedAt.IsZero() {
		obs.CapturedAt = time.Now().UTC()
	}
	if obs.ScaleFactor <= 0 {
		obs.ScaleFactor = 1
	}
	if req.Format != "tree" {
		obs, err = m.store.Save(sessionID, s.sequence, obs)
		if err != nil {
			return Observation{}, err
		}
	}
	savedPath = obs.FilePath
	s.latestFrameID = obs.FrameID
	s.latest = obs
	s.revision = m.revision
	s.request = req
	if req.Target.Window != "" && obs.WindowID != "" {
		s.request.Target.Window = obs.WindowID
	}
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

func normalizeAllowedWindows(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func validateAllowedWindow(activeWindow string, allowed []string) error {
	if len(allowed) == 0 {
		return nil
	}
	activeWindow = strings.TrimSpace(activeWindow)
	if activeWindow == "" {
		return errors.New("computer: active window is unavailable while allowed_windows is configured")
	}
	for _, candidate := range allowed {
		if strings.Contains(strings.ToLower(activeWindow), strings.ToLower(candidate)) {
			return nil
		}
	}
	return fmt.Errorf("computer: active window %q is not allowed", activeWindow)
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
