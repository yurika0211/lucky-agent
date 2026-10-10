package sdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SetSessionWorkingDir sets the absolute working directory used by terminal
// and other shell-aware tools for a session. The directory must already exist.
// The setting is persisted with the session and is also used when rebuilding
// the session system context on the next turn.
func (a *Agent) SetSessionWorkingDir(sessionID, dir string) error {
	if err := a.require(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("sdk: empty session id")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("sdk: empty working directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("sdk: resolve working directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("sdk: stat working directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("sdk: working directory is not a directory: %s", abs)
	}
	sess, ok := a.inner.Sessions().Get(sessionID)
	if !ok || sess == nil {
		return fmt.Errorf("sdk: session %q not found", sessionID)
	}
	sess.SetCwd(abs)
	if err := sess.Save(); err != nil {
		return fmt.Errorf("sdk: save session working directory: %w", err)
	}
	return nil
}

// SessionWorkingDir returns the persisted working directory for a session.
func (a *Agent) SessionWorkingDir(sessionID string) (string, error) {
	if err := a.require(); err != nil {
		return "", err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("sdk: empty session id")
	}
	sess, ok := a.inner.Sessions().Get(sessionID)
	if !ok || sess == nil {
		return "", fmt.Errorf("sdk: session %q not found", sessionID)
	}
	return sess.GetCwd(), nil
}
