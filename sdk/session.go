package sdk

import (
	"fmt"
	"strings"
	"time"
)

// SessionInfo is lightweight session metadata for listings.
type SessionInfo struct {
	ID           string
	Title        string
	MessageCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Message is a simplified chat transcript entry.
type Message struct {
	Role    string
	Content string
	Name    string
}

// Session is a full session snapshot for host UIs.
type Session struct {
	ID           string
	Title        string
	MessageCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Messages     []Message
}

// RenameSession updates a session display title and persists it.
func (a *Agent) RenameSession(sessionID, title string) error {
	if err := a.require(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("sdk: empty session id")
	}
	sess, ok := a.inner.Sessions().Get(sessionID)
	if !ok || sess == nil {
		return fmt.Errorf("sdk: session %q not found", sessionID)
	}
	sess.SetTitle(title)
	if err := sess.Save(); err != nil {
		return fmt.Errorf("sdk: save session: %w", err)
	}
	return nil
}
