package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRestoreBackupAfterRollSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("restore-after-roll", dir)
	for i := 1; i <= 3; i++ {
		s.AddMessage("user", fmt.Sprintf("question %d", i))
		s.AddMessage("assistant", fmt.Sprintf("answer %d", i))
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	want := s.GetMessages()
	backup, err := s.CreateBackup("manual")
	if err != nil {
		t.Fatal(err)
	}
	roll, err := s.Roll(1)
	if err != nil || !roll.Rolled {
		t.Fatalf("Roll = %+v, %v", roll, err)
	}
	if _, err := s.RestoreBackup(backup.ID); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, ok := m.Get(s.ID)
	if !ok {
		t.Fatal("restored session missing after restart")
	}
	if got := reloaded.GetMessages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored disk history differs: got=%+v want=%+v", got, want)
	}
}

func TestBackupRestoresBlobWithoutLiveDirectory(t *testing.T) {
	s := NewSession("standalone-backup", t.TempDir())
	body := strings.Repeat("complete body ", 6000)
	s.AddMessage("user", "question")
	s.AddMessage("tool", body)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	backup, err := s.CreateBackup("manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.sessionRootDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreBackup(backup.ID); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, ok := m.Get(s.ID)
	if !ok {
		t.Fatal("restored session missing")
	}
	messages := reloaded.GetMessages()
	if len(messages) != 2 {
		t.Fatalf("message count = %d", len(messages))
	}
	got, err := reloaded.ResolveMessageContent(messages[1])
	if err != nil || got != body {
		t.Fatalf("restored body differs: bytes=%d, err=%v", len(got), err)
	}
}

func TestGCProtectsBackupOnlyBlobs(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			s := NewSession("backup-gc", t.TempDir())
			s.AddMessage("user", "baseline")
			if err := s.Save(); err != nil {
				t.Fatal(err)
			}
			before, err := s.CreateBackup("manual")
			if err != nil {
				t.Fatal(err)
			}
			body := strings.Repeat("backup body ", 6000)
			s.AddMessage("tool", body)
			if err := s.Save(); err != nil {
				t.Fatal(err)
			}
			withBlob, err := s.CreateBackup("manual")
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				convertBackupToLegacy(t, withBlob.Path)
			}
			if _, err := s.RestoreBackup(before.ID); err != nil {
				t.Fatal(err)
			}
			gc, err := s.GCBlobs()
			if err != nil || gc.Removed != 0 {
				t.Fatalf("GC removed backup data: result=%+v err=%v", gc, err)
			}
			if _, err := s.RestoreBackup(withBlob.ID); err != nil {
				t.Fatal(err)
			}
			messages := s.GetMessages()
			got, err := s.ResolveMessageContent(messages[1])
			if err != nil || got != body {
				t.Fatalf("backup body unavailable after GC: bytes=%d err=%v", len(got), err)
			}
		})
	}
}

func TestLegacyBackupMissingBlobFailsBeforeChangingHistory(t *testing.T) {
	s := NewSession("legacy-backup-missing-blob", t.TempDir())
	s.AddMessage("user", "question")
	s.AddMessage("tool", strings.Repeat("legacy body ", 6000))
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	backup, err := s.CreateBackup("manual")
	if err != nil {
		t.Fatal(err)
	}
	convertBackupToLegacy(t, backup.Path)
	messages := s.GetMessages()
	if err := os.Remove(s.blobFilePath(messages[1].BlobHash)); err != nil {
		t.Fatal(err)
	}
	s.AddMessage("user", "newer turn must survive failed restore")
	want := s.GetMessages()
	if _, err := s.RestoreBackup(backup.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore should report missing legacy body: %v", err)
	}
	if got := s.GetMessages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed restore changed the transcript: got=%+v want=%+v", got, want)
	}
}

func TestGCStopsBeforeDeletionWhenReferenceSourceIsCorrupt(t *testing.T) {
	for _, source := range []string{"backup", "archive", "archive-tail"} {
		t.Run(source, func(t *testing.T) {
			s := NewSession("corrupt-references", t.TempDir())
			s.AddMessage("user", "baseline")
			if err := s.Save(); err != nil {
				t.Fatal(err)
			}
			orphan := s.blobFilePath(hashBytes([]byte("unreferenced body")))
			if err := os.WriteFile(orphan, []byte("unreferenced body"), 0600); err != nil {
				t.Fatal(err)
			}
			var path, corrupt string
			if source == "backup" {
				path = filepath.Join(s.dir, ".backups", s.ID, "corrupt.json")
				corrupt = `{}`
			} else if source == "archive" {
				path = filepath.Join(s.sessionRootDir(), "archive", "corrupt.jsonl")
				corrupt = "not-json\n{\"role\":\"user\",\"content\":\"after corruption\"}\n"
			} else {
				path = filepath.Join(s.sessionRootDir(), "archive", "interrupted.jsonl")
				corrupt = `{"role":"tool","blob_hash":"` + filepath.Base(orphan)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(corrupt), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := s.GCBlobs()
			if err == nil || result.Removed != 0 {
				t.Fatalf("GC must fail before deleting any body: result=%+v err=%v", result, err)
			}
			if _, err := os.Stat(orphan); err != nil {
				t.Fatalf("GC deleted body despite incomplete references: %v", err)
			}
		})
	}
}

func TestSessionMaintenanceRejectsDeletedSession(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := m.New()
	s.AddMessage("user", "before delete")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	backup, err := s.CreateBackup("manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(s.ID); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func() error{
		"externalize": s.ExternalizeLargeMessages,
		"migrate":     func() error { _, err := s.MigrateToSegment(); return err },
		"roll":        func() error { _, err := s.Roll(1); return err },
		"gc":          func() error { _, err := s.GCBlobs(); return err },
		"backup":      func() error { _, err := s.CreateBackup("manual"); return err },
		"restore":     func() error { _, err := s.RestoreBackup(backup.ID); return err },
	}
	for name, check := range checks {
		if err := check(); !errors.Is(err, ErrSessionDeleted) {
			t.Errorf("%s should reject deleted session: %v", name, err)
		}
	}
}

func convertBackupToLegacy(t *testing.T, path string) {
	t.Helper()
	backup, err := readSessionBackup(path)
	if err != nil {
		t.Fatal(err)
	}
	backup.FormatVersion = 1
	backup.Blobs = nil
	body, err := marshalSessionBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	backup.ContentHash = hashBytes(body)
	body, err = json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}
