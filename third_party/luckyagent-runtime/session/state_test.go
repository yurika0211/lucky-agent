package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/appservice"
	"github.com/yurika0211/luckyagent-runtime/provider"
)

func coldStateSession(t *testing.T, legacy bool) (*Manager, *Session) {
	t.Helper()
	dir := t.TempDir()
	s := NewSession("cold-session", dir)
	s.SetTitle("original title")
	s.SetCwd("/original")
	s.SetEnv("ORIGINAL", "old")
	s.SetAgentProfile("old role", "old prompt")
	s.SetPinned(true)
	s.SetProject("original project")
	s.AddMessage("user", "saved history")
	if legacy {
		if err := s.saveLegacyMarkdownWithProfile(dir, s.ID, s.Title, s.AgentRole, s.AgentSystemPrompt, s.Project, s.Pinned, s.CreatedAt, s.UpdatedAt, s.ShellContext, s.Messages); err != nil {
			t.Fatal(err)
		}
	} else if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := m.Get(s.ID)
	if !ok || loaded.messagesLoaded {
		t.Fatal("expected metadata-only session")
	}
	return m, loaded
}

func TestSaveAndAppendPreserveAllMessages(t *testing.T) {
	s := NewSession("concurrent", t.TempDir())
	const messages = 160
	var wg sync.WaitGroup
	errCh := make(chan error, messages+1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range messages {
			if err := s.AddMessageWithError("user", fmt.Sprintf("message-%d", i)); err != nil {
				errCh <- err
				return
			}
			runtime.Gosched()
		}
	}()
	go func() {
		defer wg.Done()
		for range 40 {
			if err := s.Save(); err != nil {
				errCh <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, ok := m.Get(s.ID)
	if !ok {
		t.Fatal("session missing after reload")
	}
	stored, err := reloaded.GetMessagesWithError()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != messages || s.MessageCount() != messages {
		t.Fatalf("message count: memory=%d disk=%d, want %d", s.MessageCount(), len(stored), messages)
	}
	for i, msg := range stored {
		if msg.Content != fmt.Sprintf("message-%d", i) {
			t.Fatalf("message %d = %q", i, msg.Content)
		}
	}
}

func TestColdMetadataPatchDoesNotLoadOrRewriteTranscript(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			m, s := coldStateSession(t, legacy)
			path := s.messagesJSONLPath()
			if legacy {
				path = filepath.Join(m.dir, s.ID+".md")
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			s.SetCwd("/new")
			s.SetEnv("NEW", "new")
			s.SetAgentProfile("new role", "new prompt")
			title, project, pinned := "new title", "new project", false
			if err := appservice.UpdateSessionMetadata(s, appservice.SessionMetadata{Title: &title, Project: &project, Pinned: &pinned}); err != nil {
				t.Fatal(err)
			}
			if s.messagesLoaded {
				t.Fatal("metadata update loaded the transcript")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("transcript changed during metadata update: %v", err)
			}
			if m.ListInfo()[0].Title != title || m.ListInfo()[0].Project != project || m.ListInfo()[0].Pinned {
				t.Fatalf("metadata update not visible: %+v", m.ListInfo())
			}
			messages, err := s.GetMessagesWithError()
			if err != nil || len(messages) != 1 {
				t.Fatalf("load history: messages=%d err=%v", len(messages), err)
			}
			if s.Title != title || s.GetCwd() != "/new" || s.GetEnv()["NEW"] != "new" {
				t.Fatalf("lazy load replaced updated metadata: title=%q cwd=%q env=%v", s.Title, s.GetCwd(), s.GetEnv())
			}
			if role, prompt := s.AgentProfile(); role != "new role" || prompt != "new prompt" {
				t.Fatalf("lazy load replaced profile: %q/%q", role, prompt)
			}
			if err := s.Save(); err != nil {
				t.Fatal(err)
			}
			reloaded, err := NewManager(m.dir)
			if err != nil {
				t.Fatal(err)
			}
			persisted, _ := reloaded.Get(s.ID)
			if persisted.Title != title || persisted.GetCwd() != "/new" {
				t.Fatalf("updated metadata not durable: %+v", reloaded.ListInfo())
			}
		})
	}
}

func TestColdRenameAndSettersSurviveFirstSave(t *testing.T) {
	_, s := coldStateSession(t, false)
	s.SetCwd("/new")
	s.SetEnv("NEW", "new")
	s.SetAgentProfile("new role", "new prompt")
	if err := appservice.RenameSession(s, "renamed"); err != nil {
		t.Fatal(err)
	}
	if s.Title != "renamed" || s.GetCwd() != "/new" || s.GetEnv()["NEW"] != "new" {
		t.Fatalf("first Save replaced setters: title=%q cwd=%q env=%v", s.Title, s.GetCwd(), s.GetEnv())
	}
	if role, prompt := s.AgentProfile(); role != "new role" || prompt != "new prompt" {
		t.Fatalf("first Save replaced profile: %q/%q", role, prompt)
	}
}

func TestDeletedHandleCannotRecreateSession(t *testing.T) {
	m, s := coldStateSession(t, false)
	if _, err := s.GetMessagesWithError(); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(s.ID); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"append": s.AddMessageWithError("assistant", "late reply"),
		"save":   s.Save(), "metadata": s.SaveMetadata(),
	} {
		if !errors.Is(err, ErrSessionDeleted) {
			t.Errorf("%s error = %v, want ErrSessionDeleted", name, err)
		}
	}
	if _, err := s.GetMessagesWithError(); !errors.Is(err, ErrSessionDeleted) {
		t.Errorf("read error = %v, want ErrSessionDeleted", err)
	}
	if _, _, _, err := s.GetMessagesPage(10, 0); !errors.Is(err, ErrSessionDeleted) {
		t.Errorf("page error = %v, want ErrSessionDeleted", err)
	}
	m.Upsert(s)
	if m.Count() != 0 {
		t.Fatal("Upsert registered a deleted handle")
	}
	reloaded, err := NewManager(m.dir)
	if err != nil || reloaded.Count() != 0 {
		t.Fatalf("deleted session returned on restart: manager=%v err=%v", reloaded, err)
	}
	fresh := m.Ensure(s.ID)
	if fresh == s {
		t.Fatal("Ensure reused deleted handle")
	}
	if err := fresh.AddMessageWithError("user", "new session"); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Save(); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); !errors.Is(err, ErrSessionDeleted) {
		t.Fatalf("old handle can overwrite replacement session: %v", err)
	}
}

func TestDeleteKeepsLiveStateWhenDiskRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("requires Unix directory permissions without root bypass")
	}
	m, s := coldStateSession(t, false)
	root := filepath.Join(m.dir, s.ID)
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700) })
	forgotten := false
	service := appservice.NewSessions[*Session](m, func(string) { forgotten = true })
	if err := service.Delete(s.ID, ""); err == nil {
		t.Fatal("Delete succeeded despite an undeletable transcript")
	}
	if forgotten {
		t.Fatal("failed deletion forgot short-term state")
	}
	if live, ok := m.Get(s.ID); !ok || live != s {
		t.Fatal("failed deletion removed the live handle")
	}
	if _, err := os.Stat(s.messagesJSONLPath()); err != nil {
		t.Fatalf("failed deletion did not preserve transcript: %v", err)
	}
	if err := s.SaveMetadata(); err != nil {
		t.Fatalf("failed deletion marked the handle deleted: %v", err)
	}
}

func TestHistoryErrorsAreReportedWithoutMutation(t *testing.T) {
	_, s := coldStateSession(t, false)
	corrupt := []byte("{\"role\":\n")
	if err := os.WriteFile(s.messagesJSONLPath(), corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessagesWithError(); err == nil {
		t.Fatal("history corruption was reported as an empty conversation")
	}
	if err := s.AddMessageWithError("user", "must not replace history"); err == nil {
		t.Fatal("append succeeded after a failed history load")
	}
	if s.messagesLoaded {
		t.Fatal("failed load marked history loaded")
	}
	s.SetTitle("still editable")
	if err := s.SaveMetadata(); err != nil {
		t.Fatalf("metadata-only update tried to parse corrupt history: %v", err)
	}
	after, err := os.ReadFile(s.messagesJSONLPath())
	if err != nil || !bytes.Equal(corrupt, after) {
		t.Fatalf("failed load/metadata update changed history: %v", err)
	}
}

func TestMissingPersistedTranscriptIsNotAnEmptySession(t *testing.T) {
	_, s := coldStateSession(t, false)
	if err := os.Remove(s.messagesJSONLPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessagesWithError(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing known history error = %v, want wrapped not-exist", err)
	}
	if err := s.AddMessageWithError("user", "must not replace missing history"); err == nil {
		t.Fatal("append succeeded after losing the durable transcript")
	}
	if err := s.Save(); err == nil {
		t.Fatal("Save replaced missing persisted history with an empty body")
	}
	if s.messagesLoaded || s.MessageCount() != 1 {
		t.Fatalf("missing load changed session state: loaded=%v count=%d", s.messagesLoaded, s.MessageCount())
	}
}

func TestTruncatedCommittedMessageCannotBecomeAnUncommittedTail(t *testing.T) {
	m, s := coldStateSession(t, false)
	if err := s.AddMessageWithError("assistant", "already committed reply"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(s.messagesJSONLPath())
	if err != nil {
		t.Fatal(err)
	}
	firstEnd := bytes.IndexByte(full, '\n')
	truncated := append(append([]byte(nil), full[:firstEnd+1]...), []byte(`{"role":"assistant","content":"truncated`)...)
	if err := os.WriteFile(s.messagesJSONLPath(), truncated, 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewManager(m.dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := restarted.Get(s.ID)
	if _, _, _, err := loaded.GetMessagesPage(10, 0); err == nil {
		t.Fatal("page silently lost a message acknowledged in metadata")
	}
	if _, err := loaded.GetMessagesWithError(); err == nil {
		t.Fatal("full load silently lost a message acknowledged in metadata")
	}
	if err := loaded.Save(); err == nil {
		t.Fatal("Save rewrote history after losing an acknowledged message")
	}
	after, err := os.ReadFile(s.messagesJSONLPath())
	if err != nil || !bytes.Equal(truncated, after) {
		t.Fatalf("rejected recovery changed transcript: %v", err)
	}
	if loaded.messagesLoaded || loaded.MessageCount() != 2 {
		t.Fatalf("rejected recovery changed count: loaded=%v count=%d", loaded.messagesLoaded, loaded.MessageCount())
	}
}

func TestHistoryPageRefreshesColdCountWithoutLoadingTranscript(t *testing.T) {
	m, s := coldStateSession(t, false)
	// Simulate a completed JSONL append whose sidecar update was interrupted.
	line, err := json.Marshal(provider.Message{Role: "assistant", Content: "committed before metadata update"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(s.messagesJSONLPath(), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.Write(append(line, '\n'))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append fixture: write=%v close=%v", writeErr, closeErr)
	}
	_, total, _, err := s.GetMessagesPage(10, 0)
	if err != nil || total != 2 || s.MessageCount() != 2 || s.messagesLoaded {
		t.Fatalf("page did not refresh metadata-only count: total=%d count=%d loaded=%v err=%v", total, s.MessageCount(), s.messagesLoaded, err)
	}
	if err := s.SaveMetadata(); err != nil {
		t.Fatal(err)
	}
	meta, _ := readSessionMetadataLoose(m.dir, s.ID)
	if meta.MessageCount != 2 {
		t.Fatalf("metadata did not retain recovered durable count: %+v", meta)
	}
	if got, want := m.ListInfo()[0].ByteSize, segmentStorageBytes(s.sessionRootDir()); got != want {
		t.Fatalf("page recovery did not refresh cached size: got %d want %d", got, want)
	}
}

func TestSegmentLegacyFallbackPersistsBodyBeforeRemovingMarkdown(t *testing.T) {
	m, s := coldStateSession(t, true)
	meta := sessionMetadata{
		ID: s.ID, Title: s.Title, MessageCount: 1, Format: FormatSegmentV1,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionMetadataPath(m.dir, s.ID), data, 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewManager(m.dir)
	if err != nil {
		t.Fatal(err)
	}
	fallback, ok := restarted.Get(s.ID)
	if !ok {
		t.Fatal("fallback session not discovered")
	}
	if err := fallback.Save(); err != nil {
		t.Fatal(err)
	}
	stored, err := readAllMessagesJSONL(fallback.messagesJSONLPath())
	if err != nil || len(stored) != 1 || stored[0].Content != "saved history" {
		t.Fatalf("fallback body was not migrated: messages=%+v err=%v", stored, err)
	}
	if _, err := os.Stat(filepath.Join(m.dir, s.ID+".md")); !os.IsNotExist(err) {
		t.Fatalf("legacy body not removed after complete migration: %v", err)
	}
}

func TestAppendReportsBlobWriteFailure(t *testing.T) {
	s := NewSession("blob-failure", t.TempDir())
	if err := os.MkdirAll(s.sessionRootDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.blobsDir(), []byte("blocks directory creation"), 0600); err != nil {
		t.Fatal(err)
	}
	err := s.AddProviderMessageWithError(provider.Message{Role: "tool", Content: strings.Repeat("x", DefaultBlobThreshold+1)})
	if err == nil || s.MessageCount() != 0 {
		t.Fatalf("blob failure must reject append: count=%d err=%v", s.MessageCount(), err)
	}
}

func TestListInfoCachesStorageSizeAcrossMetadataUpdates(t *testing.T) {
	m, s := coldStateSession(t, false)
	before := m.ListInfo()[0].ByteSize
	if before <= 0 || before != s.ByteSize() {
		t.Fatalf("invalid initial cached size: list=%d session=%d", before, s.ByteSize())
	}
	s.SetTitle("renamed")
	if err := s.SaveMetadata(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if got := m.ListInfo()[0].ByteSize; got != before {
			t.Fatalf("metadata update changed transcript bytes: got %d want %d", got, before)
		}
	}
	if err := s.AddMessageWithError("tool", strings.Repeat("large output", DefaultBlobThreshold)); err != nil {
		t.Fatal(err)
	}
	if got := m.ListInfo()[0].ByteSize; got <= before {
		t.Fatalf("blob creation did not update cached size: got %d before %d", got, before)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if got, want := m.ListInfo()[0].ByteSize, segmentStorageBytes(s.sessionRootDir()); got != want {
		t.Fatalf("cached size after Save: got %d want %d", got, want)
	}
}

func TestSaveMetadataCountsOnlyDurableMessages(t *testing.T) {
	m, s := coldStateSession(t, false)
	if err := s.AddMessageWithError("assistant", "not saved yet"); err != nil {
		t.Fatal(err)
	}
	s.SetTitle("metadata update")
	if err := s.SaveMetadata(); err != nil {
		t.Fatal(err)
	}
	meta, ok := readSessionMetadataLoose(m.dir, s.ID)
	if !ok || meta.MessageCount != 1 {
		t.Fatalf("sidecar counts unpersisted messages: %+v, ok=%v", meta, ok)
	}
	if s.MessageCount() != 2 {
		t.Fatal("metadata update removed pending messages")
	}
}

func TestDeleteWaitsForInFlightSessionOperation(t *testing.T) {
	m, s := coldStateSession(t, false)
	s.saveMu.Lock()
	done := make(chan error, 1)
	go func() { done <- m.Delete(s.ID) }()
	select {
	case err := <-done:
		s.saveMu.Unlock()
		t.Fatalf("Delete did not wait for lifecycle lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	s.saveMu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
