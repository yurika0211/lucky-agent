package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/provider"
)

const sessionBackupFormatVersion = 1

// BackupInfo describes an immutable pre-compaction session backup.
type BackupInfo struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	Trigger      string    `json:"trigger,omitempty"`
	Path         string    `json:"path"`
	CreatedAt    time.Time `json:"created_at"`
	MessageCount int       `json:"message_count"`
	ContentHash  string    `json:"content_hash"`
}

type sessionBackup struct {
	FormatVersion    int                `json:"format_version"`
	ID               string             `json:"id"`
	SessionID        string             `json:"session_id"`
	Trigger          string             `json:"trigger,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
	SessionCreatedAt time.Time          `json:"session_created_at"`
	SessionUpdatedAt time.Time          `json:"session_updated_at"`
	Title            string             `json:"title"`
	Messages         []provider.Message `json:"messages"`
	ShellContext     ShellContext       `json:"shell_context"`
	ContentHash      string             `json:"content_hash"`
}

// CreateBackup writes an immutable, standalone copy of the complete session.
// The backup is stored outside the live session Markdown file and is written
// atomically, so a failed backup cannot be mistaken for a valid restore point.
func (s *Session) CreateBackup(trigger string) (*BackupInfo, error) {
	if s == nil {
		return nil, fmt.Errorf("create session backup: session is nil")
	}
	if strings.TrimSpace(trigger) == "" {
		trigger = "manual"
	}
	if err := s.loadMessages(); err != nil {
		return nil, fmt.Errorf("create session backup: load messages: %w", err)
	}

	s.mu.RLock()
	backup := sessionBackup{
		FormatVersion:    sessionBackupFormatVersion,
		ID:               fmt.Sprintf("backup-%d", time.Now().UnixNano()),
		SessionID:        s.ID,
		Trigger:          trigger,
		CreatedAt:        time.Now().UTC(),
		SessionCreatedAt: s.CreatedAt,
		SessionUpdatedAt: s.UpdatedAt,
		Title:            s.Title,
		Messages:         append([]provider.Message(nil), s.Messages...),
		ShellContext: ShellContext{
			Cwd: s.ShellContext.Cwd,
			Env: cloneEnv(s.ShellContext.Env),
		},
	}
	dir := filepath.Join(s.dir, ".backups", s.ID)
	s.mu.RUnlock()

	body, err := marshalSessionBackup(backup)
	if err != nil {
		return nil, fmt.Errorf("create session backup: marshal: %w", err)
	}
	backup.ContentHash = hashBytes(body)
	body, err = json.Marshal(backup)
	if err != nil {
		return nil, fmt.Errorf("create session backup: marshal signed backup: %w", err)
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create session backup directory: %w", err)
	}
	path := filepath.Join(dir, backup.ID+".json")
	if err := writeAtomicPrivateFile(path, body); err != nil {
		return nil, fmt.Errorf("write session backup: %w", err)
	}

	return &BackupInfo{
		ID:           backup.ID,
		SessionID:    backup.SessionID,
		Trigger:      backup.Trigger,
		Path:         path,
		CreatedAt:    backup.CreatedAt,
		MessageCount: len(backup.Messages),
		ContentHash:  backup.ContentHash,
	}, nil
}

// ListBackups returns valid backups newest first. Invalid/incomplete files are
// ignored so one interrupted write does not hide the remaining restore points.
func (s *Session) ListBackups() ([]BackupInfo, error) {
	if s == nil {
		return nil, fmt.Errorf("list session backups: session is nil")
	}
	dir := filepath.Join(s.dir, ".backups", s.ID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupInfo{}, nil
		}
		return nil, fmt.Errorf("list session backups: read directory: %w", err)
	}

	backups := make([]BackupInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		backup, err := readSessionBackup(path)
		if err != nil || backup.SessionID != s.ID {
			continue
		}
		backups = append(backups, backupInfoFrom(path, backup))
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})
	return backups, nil
}

// RestoreBackup replaces the in-memory session and persists it from a backup.
// The backup itself remains untouched for repeatable recovery.
func (s *Session) RestoreBackup(backupID string) (*BackupInfo, error) {
	if s == nil {
		return nil, fmt.Errorf("restore session backup: session is nil")
	}
	backupID = strings.TrimSuffix(strings.TrimSpace(backupID), ".json")
	if !validBackupID(backupID) {
		return nil, fmt.Errorf("restore session backup: invalid backup id")
	}
	path := filepath.Join(s.dir, ".backups", s.ID, backupID+".json")
	backup, err := readSessionBackup(path)
	if err != nil {
		return nil, fmt.Errorf("restore session backup: %w", err)
	}
	if backup.SessionID != s.ID {
		return nil, fmt.Errorf("restore session backup: backup belongs to session %s", backup.SessionID)
	}

	s.mu.Lock()
	s.Title = backup.Title
	s.Messages = append([]provider.Message(nil), backup.Messages...)
	s.CreatedAt = backup.SessionCreatedAt
	s.UpdatedAt = time.Now()
	s.ShellContext = ShellContext{Cwd: backup.ShellContext.Cwd, Env: cloneEnv(backup.ShellContext.Env)}
	s.messagesLoaded = true
	s.messageCount = len(s.Messages)
	s.mu.Unlock()

	if err := s.Save(); err != nil {
		return nil, fmt.Errorf("restore session backup: save session: %w", err)
	}
	return &BackupInfo{
		ID:           backup.ID,
		SessionID:    backup.SessionID,
		Trigger:      backup.Trigger,
		Path:         path,
		CreatedAt:    backup.CreatedAt,
		MessageCount: len(backup.Messages),
		ContentHash:  backup.ContentHash,
	}, nil
}

func marshalSessionBackup(backup sessionBackup) ([]byte, error) {
	backup.ContentHash = ""
	return json.Marshal(backup)
}

func readSessionBackup(path string) (sessionBackup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionBackup{}, err
	}
	var backup sessionBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return sessionBackup{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if backup.FormatVersion != sessionBackupFormatVersion {
		return sessionBackup{}, fmt.Errorf("unsupported backup format %d", backup.FormatVersion)
	}
	if strings.TrimSpace(backup.ID) == "" || !validBackupID(backup.ID) {
		return sessionBackup{}, fmt.Errorf("invalid backup id")
	}
	want := backup.ContentHash
	body, err := marshalSessionBackup(backup)
	if err != nil {
		return sessionBackup{}, err
	}
	if want == "" || want != hashBytes(body) {
		return sessionBackup{}, fmt.Errorf("backup content hash mismatch")
	}
	return backup, nil
}

func backupInfoFrom(path string, backup sessionBackup) BackupInfo {
	return BackupInfo{
		ID:           backup.ID,
		SessionID:    backup.SessionID,
		Trigger:      backup.Trigger,
		Path:         path,
		CreatedAt:    backup.CreatedAt,
		MessageCount: len(backup.Messages),
		ContentHash:  backup.ContentHash,
	}
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneEnv(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	clone := make(map[string]string, len(env))
	for key, value := range env {
		clone[key] = value
	}
	return clone
}

func validBackupID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\\`)
}

func writeAtomicPrivateFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".backup-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
