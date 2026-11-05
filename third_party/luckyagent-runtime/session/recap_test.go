package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecapPersistsWithoutChangingConversation(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "segment", true: "legacy"}[legacy], func(t *testing.T) {
			m, s := coldStateSession(t, legacy)
			previous, messages, version, err := s.RecapSnapshot(10)
			if err != nil || previous != "" || len(messages) != 1 {
				t.Fatalf("snapshot: %q %v %v", previous, messages, err)
			}
			title := s.Title
			if ok, err := s.UpdateRecap("你问了 saved history。", version); err != nil || !ok {
				t.Fatalf("update: %v %v", ok, err)
			}
			if s.Title != title || !s.UpdatedAt.Equal(version) {
				t.Fatal("recap changed conversation metadata")
			}
			for _, rebuild := range []bool{false, true} {
				if rebuild {
					_ = m.index.Close()
					if err := os.Remove(filepath.Join(m.dir, sessionIndexName)); err != nil {
						t.Fatal(err)
					}
				}
				reopened, err := NewManager(m.dir)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.index.Close()
				cold, ok := reopened.Get(s.ID)
				if !ok {
					t.Fatal("session missing")
				}
				text, at := cold.Recap()
				if text != "你问了 saved history。" || at == nil || cold.messagesLoaded {
					t.Fatalf("cold recap: %q %v loaded=%v", text, at, cold.messagesLoaded)
				}
				info := reopened.ListInfo()[0]
				if info.Recap != text || info.RecapUpdatedAt == nil {
					t.Fatal("list lost recap")
				}
				at.Add(time.Hour) // returned timestamps are independent of session state
				if err := cold.Save(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRecapRejectsStaleDeletedAndFailedUpdates(t *testing.T) {
	m, s := coldStateSession(t, false)
	_, _, first, _ := s.RecapSnapshot(10)
	if ok, err := s.UpdateRecap("old preview", first); !ok || err != nil {
		t.Fatal(ok, err)
	}
	s.AddMessage("user", "new question")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	_, _, second, _ := s.RecapSnapshot(10)
	if ok, err := s.UpdateRecap("stale", first); ok || err != nil {
		t.Fatal(ok, err)
	}
	path := sessionMetadataPath(m.dir, s.ID)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateRecap("failed", second); ok || err == nil {
		t.Fatal("write should fail", ok, err)
	}
	if text, _ := s.Recap(); text != "old preview" {
		t.Fatal(text)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(s.ID); err != nil {
		t.Fatal(err)
	}
	replacement := m.Ensure(s.ID)
	if ok, err := s.UpdateRecap("deleted", second); ok || !errors.Is(err, ErrSessionDeleted) {
		t.Fatal(ok, err)
	}
	if text, _ := replacement.Recap(); text != "" {
		t.Fatal(text)
	}
}

func TestRecapOptionalAndIndexMigration(t *testing.T) {
	m, s := coldStateSession(t, false)
	data, err := json.Marshal(m.ListInfo()[0])
	if err != nil || bytes.Contains(data, []byte("recap")) {
		t.Fatalf("legacy JSON: %s %v", data, err)
	}
	for _, name := range []string{"recap", "recap_updated_at"} {
		if _, err := m.index.db.Exec("ALTER TABLE sessions DROP COLUMN " + name); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.index.init(); err != nil {
		t.Fatal(err)
	}
	rows, err := m.index.List()
	if err != nil || rows[s.ID].Meta.Recap != "" {
		t.Fatal(rows, err)
	}
	_, _, version, _ := s.RecapSnapshot(10)
	if ok, err := s.UpdateRecap("new", version); !ok || err != nil {
		t.Fatal(ok, err)
	}
	rows, err = m.index.List()
	if err != nil || rows[s.ID].Meta.Recap != "new" || rows[s.ID].Meta.RecapUpdatedAt == nil {
		t.Fatal(rows, err)
	}
}

func TestRecapSnapshotPreservesUserOutsideToolTail(t *testing.T) {
	s := NewSession("long-turn", t.TempDir())
	s.AddMessage("user", "修复代码")
	for range 80 {
		s.AddMessage("tool", "tool result")
	}
	s.AddMessage("assistant", "完成")
	_, messages, _, err := s.RecapSnapshot(64)
	if err != nil || len(messages) != 65 || messages[0].Content != "修复代码" || messages[64].Content != "完成" {
		t.Fatalf("snapshot: %d messages, %v", len(messages), err)
	}
}
