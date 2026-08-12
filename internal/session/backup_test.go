package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
)

func TestSessionBackupRoundTripRestoresExactMessages(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("backup-round-trip", dir)
	s.SetTitle("backup test")
	s.SetCwd("/tmp/project")
	s.SetEnv("MODE", "test")
	s.AddProviderMessage(providerMessage("user", "before compact"))
	s.AddProviderMessage(providerMessage("assistant", "with tool metadata"))
	before := s.GetMessages()

	info, err := s.CreateBackup("auto")
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if info.MessageCount != len(before) || info.ContentHash == "" {
		t.Fatalf("unexpected backup info: %+v", info)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	s.AddMessage("user", "after compact")
	if _, err := s.RestoreBackup(info.ID); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	got := s.GetMessages()
	if len(got) != len(before) || got[0].Content != before[0].Content || got[1].Content != before[1].Content {
		t.Fatalf("restore did not recover exact messages: got=%+v want=%+v", got, before)
	}
	if s.GetCwd() != "/tmp/project" || s.GetEnv()["MODE"] != "test" {
		t.Fatalf("restore did not recover shell context")
	}
}

func TestSessionBackupListIgnoresCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("backup-list", dir)
	s.AddMessage("user", "one")
	info, err := s.CreateBackup("manual")
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	corrupt := filepath.Join(filepath.Dir(info.Path), "corrupt.json")
	if err := os.WriteFile(corrupt, []byte(`{"format_version":1}`), 0600); err != nil {
		t.Fatalf("write corrupt backup: %v", err)
	}
	backups, err := s.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 1 || backups[0].ID != info.ID {
		t.Fatalf("unexpected backups: %+v", backups)
	}
}

// Keep the test independent from helper constructors that may change with the
// provider package; the backup path must preserve ordinary provider messages.
func providerMessage(role, content string) provider.Message {
	return provider.Message{Role: role, Content: content}
}
