package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const telegramRuntimeStateFile = "telegram_gateway_state.json"

// SharedTelegramState is the cross-process runtime snapshot for the Telegram gateway.
type SharedTelegramState struct {
	Platform         string    `json:"platform"`
	PID              int       `json:"pid"`
	Registered       bool      `json:"registered"`
	Connected        bool      `json:"connected"`
	MessagesSent     int64     `json:"messages_sent"`
	MessagesReceived int64     `json:"messages_received"`
	Errors           int64     `json:"errors"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func TelegramRuntimeStatePath(homeDir string) string {
	return filepath.Join(homeDir, "runtime", telegramRuntimeStateFile)
}

func WriteSharedTelegramState(homeDir string, state SharedTelegramState) error {
	path := TelegramRuntimeStatePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}
	state.Platform = "telegram"
	state.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal telegram runtime state: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write telegram runtime temp state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename telegram runtime state: %w", err)
	}
	return nil
}

func ReadSharedTelegramState(homeDir string) (*SharedTelegramState, error) {
	path := TelegramRuntimeStatePath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state SharedTelegramState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse telegram runtime state: %w", err)
	}
	return &state, nil
}

func (s *SharedTelegramState) IsFresh(maxAge time.Duration) bool {
	if s == nil || s.UpdatedAt.IsZero() {
		return false
	}
	return time.Since(s.UpdatedAt) <= maxAge
}
