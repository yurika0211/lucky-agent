package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

func recoveryManager(t *testing.T, dir string) *Manager {
	t.Helper()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSegmentInterruptedAppendPreservesHistory(t *testing.T) {
	for _, tail := range []string{`{"role":"assistant","content":"unfinished`, `{"role":`} {
		t.Run(tail, func(t *testing.T) {
			dir := t.TempDir()
			mgr := recoveryManager(t, dir)
			sess := mgr.New()
			sess.AddMessage("user", "first")
			sess.AddMessage("assistant", "second")
			if err := sess.Save(); err != nil {
				t.Fatal(err)
			}
			path := sess.messagesJSONLPath()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(before, tail...), 0600); err != nil {
				t.Fatal(err)
			}
			cold, ok := recoveryManager(t, dir).Get(sess.ID)
			if !ok {
				t.Fatal("cold session not found")
			}
			page, total, _, err := cold.GetMessagesPage(10, 0)
			if err != nil || total != 2 || len(page) != 2 || page[0].Content != "first" {
				t.Fatalf("page=%v total=%d err=%v", page, total, err)
			}
			unchanged, _ := os.ReadFile(path)
			if string(unchanged) != string(before)+tail {
				t.Fatal("read-only paging changed the damaged file")
			}
			if err := cold.AddMessageWithError("user", "third"); err != nil {
				t.Fatal(err)
			}
			if err := cold.Save(); err != nil {
				t.Fatal(err)
			}
			reloaded, _ := recoveryManager(t, dir).Get(sess.ID)
			messages, err := reloaded.GetMessagesWithError()
			if err != nil || len(messages) != 3 || messages[2].Content != "third" {
				t.Fatalf("reloaded=%v err=%v", messages, err)
			}
		})
	}
}

func TestSegmentCommittedCorruptionIsNotEmptyHistory(t *testing.T) {
	for _, corrupt := range []string{"{\"role\":\n", "{invalid", "{invalid}\n{\"role\":\"user\",\"content\":\"later\"}\n"} {
		t.Run(corrupt, func(t *testing.T) {
			dir := t.TempDir()
			sess := recoveryManager(t, dir).New()
			sess.AddMessage("user", "preserve")
			if err := sess.Save(); err != nil {
				t.Fatal(err)
			}
			path := sess.messagesJSONLPath()
			data, _ := os.ReadFile(path)
			data = append(data, corrupt...)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cold, _ := recoveryManager(t, dir).Get(sess.ID)
			if _, err := cold.GetMessagesWithError(); err == nil {
				t.Fatal("corruption hidden by history reader")
			}
			if err := cold.AddMessageWithError("user", "must not append"); err == nil {
				t.Fatal("corruption hidden by append")
			}
			if err := cold.Save(); err == nil {
				t.Fatal("corruption hidden by save")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(data) {
				t.Fatal("failed operation altered corrupt history")
			}
		})
	}
}

func TestSegmentAppendSeparatesCompleteRecordWithoutNewline(t *testing.T) {
	s := recoveryManager(t, t.TempDir()).New()
	if err := s.ensureSegmentDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.messagesJSONLPath(), []byte(`{"role":"user","content":"first"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.appendMessagesJSONL([]provider.Message{{Role: "assistant", Content: "second"}}); err != nil {
		t.Fatal(err)
	}
	got, err := readAllMessagesJSONL(s.messagesJSONLPath())
	if err != nil || len(got) != 2 || got[0].Content != "first" || got[1].Content != "second" {
		t.Fatalf("messages=%v err=%v", got, err)
	}
}

func TestSegmentColdPagesReuseIndexAndNoticeFileChanges(t *testing.T) {
	dir := t.TempDir()
	s := recoveryManager(t, dir).New()
	for i := 0; i < 20000; i++ {
		s.AddMessage("user", fmt.Sprintf("message-%05d", i))
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	cold, _ := recoveryManager(t, dir).Get(s.ID)
	for _, offset := range []int{0, 600, 12000, 19990, 21000} {
		page, total, more, err := cold.GetMessagesPage(10, offset)
		if err != nil || total != 20000 {
			t.Fatalf("offset=%d total=%d err=%v", offset, total, err)
		}
		start := pageStart(total, 10, offset)
		if more != (start > 0) {
			t.Fatalf("offset=%d more=%v", offset, more)
		}
		for i, msg := range page {
			if want := fmt.Sprintf("message-%05d", start+i); msg.Content != want {
				t.Fatalf("offset=%d item=%d got=%s want=%s", offset, i, msg.Content, want)
			}
		}
		if cold.messagesLoaded || len(cold.Messages) != 0 {
			t.Fatal("cold paging materialized the full transcript")
		}
	}
	index := cold.pageIndex
	if _, _, _, err := cold.GetMessagesPage(10, 3000); err != nil || cold.pageIndex != index {
		t.Fatalf("unchanged transcript did not reuse its offsets: %v", err)
	}
	line, _ := json.Marshal(provider.Message{Role: "assistant", Content: "new external record"})
	f, err := os.OpenFile(filepath.Join(dir, s.ID, messagesJSONLName), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(append(line, '\n'))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	page, total, _, err := cold.GetMessagesPage(1, 0)
	if err != nil || total != 20001 || len(page) != 1 || page[0].Content != "new external record" {
		t.Fatalf("external append page=%v total=%d err=%v", page, total, err)
	}
	// Same-sized edits still invalidate the cached index through mtime.
	data, _ := os.ReadFile(s.messagesJSONLPath())
	copy(data, strings.Repeat("!", 5))
	if err := os.WriteFile(s.messagesJSONLPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(s.messagesJSONLPath(), later, later); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := cold.GetMessagesPage(1, 0); err == nil {
		t.Fatal("cached page concealed externally corrupted history")
	}
}
