package sdk

import (
	"context"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
)

// Agent is an in-process LuckyAgent runtime handle.
//
// It is safe to keep one Agent per application process and open many sessions
// on it. Call Close when the host is shutting down.
type Agent struct {
	inner *agent.Agent
	home  string
}

// New boots an embedded LuckyAgent runtime.
//
// It initializes an isolated HomeDir tree, applies provider settings, and
// returns a process-local Agent. No HTTP server is started.
func New(cfg Config) (*Agent, error) {
	home, err := cfg.resolvedHomeDir()
	if err != nil {
		return nil, err
	}

	mgr, err := config.NewManagerWithDir(home)
	if err != nil {
		return nil, fmt.Errorf("sdk: config manager: %w", err)
	}

	if err := mgr.InitHome(); err != nil {
		return nil, fmt.Errorf("sdk: init home: %w", err)
	}

	if cfg.LoadExisting {
		// Load is best-effort: missing file keeps defaults.
		_ = mgr.Load()
	}

	if err := cfg.applyTo(mgr); err != nil {
		return nil, fmt.Errorf("sdk: apply config: %w", err)
	}

	inner, err := agent.New(mgr)
	if err != nil {
		return nil, fmt.Errorf("sdk: new agent: %w", err)
	}

	a := &Agent{inner: inner, home: home}
	if err := a.applyDisabledTools(cfg.DisableTools); err != nil {
		_ = a.Close()
		return nil, err
	}
	return a, nil
}

// HomeDir returns the runtime data root used by this Agent.
func (a *Agent) HomeDir() string {
	if a == nil {
		return ""
	}
	return a.home
}

// Close releases runtime resources (memory, rag, autonomy, computer, ...).
func (a *Agent) Close() error {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.Close()
}

// Chat starts a fresh session and returns the final assistant text.
//
// Cancellation: cancel ctx to stop the in-flight turn. There is no separate
// Stop() API in v0; host apps should cancel the context they passed in.
func (a *Agent) Chat(ctx context.Context, message string) (string, error) {
	if err := a.require(); err != nil {
		return "", err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", fmt.Errorf("sdk: empty message")
	}
	return a.inner.Chat(ctx, message)
}

// ChatSession continues an existing session and returns the final assistant text.
func (a *Agent) ChatSession(ctx context.Context, sessionID, message string) (string, error) {
	if err := a.require(); err != nil {
		return "", err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("sdk: empty session id")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", fmt.Errorf("sdk: empty message")
	}
	return a.inner.ChatWithSession(ctx, sessionID, message)
}

// ChatStream starts a fresh session and streams chat events.
// The returned session id can be reused with ChatSession / ChatSessionStream.
func (a *Agent) ChatStream(ctx context.Context, message string) (sessionID string, events <-chan Event, err error) {
	if err := a.require(); err != nil {
		return "", nil, err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", nil, fmt.Errorf("sdk: empty message")
	}

	sess := a.inner.Sessions().New()
	if sess == nil {
		return "", nil, fmt.Errorf("sdk: create session failed")
	}
	ch, err := a.inner.ChatWithSessionStream(ctx, sess.ID, message)
	if err != nil {
		return "", nil, err
	}
	return sess.ID, mapEvents(ch), nil
}

// ChatSessionStream continues an existing session and streams chat events.
func (a *Agent) ChatSessionStream(ctx context.Context, sessionID, message string) (<-chan Event, error) {
	if err := a.require(); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("sdk: empty session id")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("sdk: empty message")
	}
	ch, err := a.inner.ChatWithSessionStream(ctx, sessionID, message)
	if err != nil {
		return nil, err
	}
	return mapEvents(ch), nil
}

// NewSession creates an empty session and returns its id.
func (a *Agent) NewSession() (string, error) {
	if err := a.require(); err != nil {
		return "", err
	}
	sess := a.inner.Sessions().New()
	if sess == nil {
		return "", fmt.Errorf("sdk: create session failed")
	}
	return sess.ID, nil
}

// NewSessionWithTitle creates a session with a display title.
func (a *Agent) NewSessionWithTitle(title string) (string, error) {
	if err := a.require(); err != nil {
		return "", err
	}
	sess := a.inner.Sessions().NewWithTitle(strings.TrimSpace(title))
	if sess == nil {
		return "", fmt.Errorf("sdk: create session failed")
	}
	return sess.ID, nil
}

// ListSessions returns lightweight session metadata, newest activity first when
// the session manager provides that ordering.
func (a *Agent) ListSessions() ([]SessionInfo, error) {
	if err := a.require(); err != nil {
		return nil, err
	}
	items := a.inner.Sessions().ListInfo()
	out := make([]SessionInfo, 0, len(items))
	for _, item := range items {
		out = append(out, SessionInfo{
			ID:           item.ID,
			Title:        item.Title,
			MessageCount: item.MessageCount,
			CreatedAt:    item.CreatedAt,
			UpdatedAt:    item.UpdatedAt,
		})
	}
	return out, nil
}

// GetSession returns one session snapshot including messages.
func (a *Agent) GetSession(sessionID string) (*Session, error) {
	if err := a.require(); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("sdk: empty session id")
	}
	sess, ok := a.inner.Sessions().Get(sessionID)
	if !ok || sess == nil {
		return nil, fmt.Errorf("sdk: session %q not found", sessionID)
	}
	msgs := sess.GetMessages()
	outMsgs := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		outMsgs = append(outMsgs, Message{
			Role:    m.Role,
			Content: m.Content,
			Name:    m.Name,
		})
	}
	return &Session{
		ID:           sess.ID,
		Title:        sess.Title,
		MessageCount: sess.MessageCount(),
		CreatedAt:    sess.CreatedAt,
		UpdatedAt:    sess.UpdatedAt,
		Messages:     outMsgs,
	}, nil
}

// DeleteSession removes a session and its on-disk transcript.
func (a *Agent) DeleteSession(sessionID string) error {
	if err := a.require(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("sdk: empty session id")
	}
	if err := a.inner.Sessions().Delete(sessionID); err != nil {
		return fmt.Errorf("sdk: delete session: %w", err)
	}
	return nil
}

// Remember stores a durable memory entry.
func (a *Agent) Remember(content, category string) error {
	if err := a.require(); err != nil {
		return err
	}
	return a.inner.Remember(content, category)
}

// RememberLongTerm stores a long-tier durable memory entry.
func (a *Agent) RememberLongTerm(content, category string) error {
	if err := a.require(); err != nil {
		return err
	}
	return a.inner.RememberLongTerm(content, category)
}

// Recall searches stored memories.
func (a *Agent) Recall(query string) []MemoryHit {
	if err := a.require(); err != nil {
		return nil
	}
	entries := a.inner.Recall(query)
	out := make([]MemoryHit, 0, len(entries))
	for _, e := range entries {
		out = append(out, MemoryHit{
			ID:       e.ID,
			Content:  e.Content,
			Category: e.Category,
			Score:    e.Importance,
		})
	}
	return out
}

// SwitchModel changes the active chat model for subsequent turns.
func (a *Agent) SwitchModel(modelID string) error {
	if err := a.require(); err != nil {
		return err
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("sdk: empty model id")
	}
	if err := a.inner.SwitchModel(modelID); err != nil {
		return fmt.Errorf("sdk: switch model: %w", err)
	}
	return nil
}

// Underlying exposes the internal agent for advanced in-repo callers.
// External applications should avoid this; it is not a stability surface.
func (a *Agent) Underlying() *agent.Agent {
	if a == nil {
		return nil
	}
	return a.inner
}

func (a *Agent) require() error {
	if a == nil || a.inner == nil {
		return fmt.Errorf("sdk: agent is nil")
	}
	return nil
}
