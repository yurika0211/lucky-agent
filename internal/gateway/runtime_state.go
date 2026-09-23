package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const telegramRuntimeStateFile = "telegram_gateway_state.json"
const telegramGatewayLeaseFile = "telegram_gateway.lock"
const recentChatTargetFile = "recent_chat_target.json"
const timeoutEventFile = "timeout_last_error.json"
const timeoutHistoryFile = "timeout_events.jsonl"

// telegramGatewayLeaseMaxAge covers the short interval between reserving a
// gateway process and publishing its first runtime state. A running gateway
// refreshes the state every two seconds, so an older lease can safely be
// recovered after an abnormal exit.
const telegramGatewayLeaseMaxAge = 15 * time.Second

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

// RecentChatTarget persists the most recently active gateway destination for
// cron and autonomy notifications that run after a process restart.
type RecentChatTarget struct {
	Platform     string    `json:"platform"`
	ChatID       string    `json:"chat_id"`
	ReplyToMsgID string    `json:"reply_to_message_id,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TimeoutEvent is the latest user-visible timeout diagnostic. It contains
// configuration metadata only and never persists credentials or message text.
type TimeoutEvent struct {
	Layer             string    `json:"layer"`
	ConfigPath        string    `json:"config_path"`
	ConfiguredSeconds int       `json:"configured_seconds"`
	Error             string    `json:"error,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func TelegramRuntimeStatePath(homeDir string) string {
	return filepath.Join(homeDir, "runtime", telegramRuntimeStateFile)
}

func RecentChatTargetPath(homeDir string) string {
	return filepath.Join(homeDir, "runtime", recentChatTargetFile)
}

func WriteRecentChatTarget(homeDir string, target RecentChatTarget) error {
	path := RecentChatTargetPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create recent chat target directory: %w", err)
	}
	target.Platform = strings.TrimSpace(target.Platform)
	target.ChatID = strings.TrimSpace(target.ChatID)
	target.ReplyToMsgID = strings.TrimSpace(target.ReplyToMsgID)
	if target.Platform == "" || target.ChatID == "" {
		return fmt.Errorf("platform and chat id are required")
	}
	target.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(target, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recent chat target: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".recent-chat-target-*.tmp")
	if err != nil {
		return fmt.Errorf("create recent chat target temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure recent chat target temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write recent chat target: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close recent chat target temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename recent chat target: %w", err)
	}
	return nil
}

func ReadRecentChatTarget(homeDir string) (*RecentChatTarget, error) {
	data, err := os.ReadFile(RecentChatTargetPath(homeDir))
	if err != nil {
		return nil, err
	}
	var target RecentChatTarget
	if err := json.Unmarshal(data, &target); err != nil {
		return nil, fmt.Errorf("parse recent chat target: %w", err)
	}
	if strings.TrimSpace(target.Platform) == "" || strings.TrimSpace(target.ChatID) == "" {
		return nil, fmt.Errorf("recent chat target is incomplete")
	}
	return &target, nil
}

func TimeoutEventPath(homeDir string) string {
	return filepath.Join(homeDir, "runtime", timeoutEventFile)
}

func timeoutHistoryPath(homeDir string) string {
	return filepath.Join(homeDir, "runtime", timeoutHistoryFile)
}

func telegramGatewayLeasePath(homeDir string) string {
	return filepath.Join(homeDir, "runtime", telegramGatewayLeaseFile)
}

// AcquireTelegramGatewayLease prevents two local long-polling processes from
// using the same bot token. Telegram terminates one of concurrent getUpdates
// requests, which otherwise makes message delivery (and auto-reactions)
// intermittent.
//
// The returned release function is idempotent and must be deferred by the
// gateway command for the lifetime of the Telegram process.
func AcquireTelegramGatewayLease(homeDir string) (func(), error) {
	state, err := ReadSharedTelegramState(homeDir)
	if err == nil && state.IsFresh(telegramGatewayLeaseMaxAge) {
		return nil, fmt.Errorf("telegram gateway is already running (pid %d); stop that process before starting another", state.PID)
	}

	leasePath := telegramGatewayLeasePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(leasePath), 0o700); err != nil {
		return nil, fmt.Errorf("create telegram gateway runtime directory: %w", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		lease, err := os.OpenFile(leasePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, writeErr := fmt.Fprintf(lease, "%d\n", os.Getpid()); writeErr != nil {
				_ = lease.Close()
				_ = os.Remove(leasePath)
				return nil, fmt.Errorf("write telegram gateway lease: %w", writeErr)
			}
			if closeErr := lease.Close(); closeErr != nil {
				_ = os.Remove(leasePath)
				return nil, fmt.Errorf("close telegram gateway lease: %w", closeErr)
			}

			var released bool
			return func() {
				if released {
					return
				}
				released = true
				_ = os.Remove(leasePath)
			}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create telegram gateway lease: %w", err)
		}

		info, statErr := os.Stat(leasePath)
		if statErr == nil && time.Since(info.ModTime()) < telegramGatewayLeaseMaxAge {
			return nil, fmt.Errorf("telegram gateway is already starting or running; stop it before starting another")
		}
		if removeErr := os.Remove(leasePath); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, fmt.Errorf("remove stale telegram gateway lease: %w", removeErr)
		}
	}

	return nil, fmt.Errorf("acquire telegram gateway lease: concurrent startup detected")
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

func WriteTimeoutEvent(homeDir string, event TimeoutEvent) error {
	path := TimeoutEventPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create timeout runtime directory: %w", err)
	}
	event.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal timeout event: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write timeout event: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename timeout event: %w", err)
	}
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal timeout history event: %w", err)
	}
	f, err := os.OpenFile(timeoutHistoryPath(homeDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open timeout history: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("write timeout history: %w", err)
	}
	_ = f.Close()
	return nil
}

func ReadTimeoutEvent(homeDir string) (*TimeoutEvent, error) {
	data, err := os.ReadFile(TimeoutEventPath(homeDir))
	if err != nil {
		return nil, err
	}
	var event TimeoutEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("parse timeout event: %w", err)
	}
	return &event, nil
}

func ReadTimeoutEventsSince(homeDir string, since time.Time) ([]TimeoutEvent, error) {
	data, err := os.ReadFile(timeoutHistoryPath(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	events := make([]TimeoutEvent, 0)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event TimeoutEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if since.IsZero() || !event.UpdatedAt.Before(since) {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *SharedTelegramState) IsFresh(maxAge time.Duration) bool {
	if s == nil || s.UpdatedAt.IsZero() {
		return false
	}
	return time.Since(s.UpdatedAt) <= maxAge
}
