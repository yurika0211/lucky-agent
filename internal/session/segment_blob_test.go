package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSegmentSaveAppendAndReload(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("seg-1", dir)
	if s.Format() != FormatSegmentV1 {
		t.Fatalf("new session format = %q", s.Format())
	}
	s.AddMessage("user", "hello")
	s.AddMessage("assistant", "world")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jsonl := filepath.Join(dir, "seg-1", messagesJSONLName)
	if _, err := os.Stat(jsonl); err != nil {
		t.Fatalf("expected messages.jsonl: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "seg-1.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy md should be removed after segment save, err=%v", err)
	}

	s.AddMessage("user", "again")
	if err := s.Save(); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	loaded, ok := m.Get("seg-1")
	if !ok {
		t.Fatal("session missing after reload")
	}
	if loaded.Format() != FormatSegmentV1 {
		t.Fatalf("loaded format = %q", loaded.Format())
	}
	msgs := loaded.GetMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[2].Content != "again" {
		t.Fatalf("unexpected last message: %q", msgs[2].Content)
	}
}

func TestBlobExternalizeOnAddAndResolve(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("blob-1", dir)
	big := strings.Repeat("x", DefaultBlobThreshold+100)
	s.AddToolMessageWithCallID("call-1", "file_read", big)
	msgs := s.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].BlobHash == "" || msgs[0].BlobBytes != len(big) {
		t.Fatalf("expected blob metadata, got hash=%q bytes=%d", msgs[0].BlobHash, msgs[0].BlobBytes)
	}
	if len(msgs[0].Content) >= len(big) {
		t.Fatalf("content should be preview, got len=%d", len(msgs[0].Content))
	}
	if !strings.Contains(msgs[0].Content, "content externalized") {
		t.Fatalf("preview missing marker: %q", msgs[0].Content[:min(80, len(msgs[0].Content))])
	}
	full, err := s.ResolveMessageContent(msgs[0])
	if err != nil {
		t.Fatalf("ResolveMessageContent: %v", err)
	}
	if full != big {
		t.Fatalf("resolved content mismatch: got %d want %d", len(full), len(big))
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	blobPath := s.blobFilePath(msgs[0].BlobHash)
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("blob file missing: %v", err)
	}

	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	loaded, _ := m.Get("blob-1")
	got := loaded.GetMessages()
	if len(got) != 1 || got[0].BlobHash == "" {
		t.Fatalf("reloaded blob message broken: %+v", got)
	}
	full2, err := loaded.ResolveMessageContent(got[0])
	if err != nil || full2 != big {
		t.Fatalf("reloaded resolve failed: err=%v len=%d", err, len(full2))
	}
}

func TestLegacyMarkdownMigratesOnSave(t *testing.T) {
	dir := t.TempDir()
	// Write a legacy file the way old code did.
	legacy := NewSession("legacy-1", dir)
	legacy.format = FormatLegacyMD
	legacy.AddMessage("user", "old")
	legacy.AddMessage("assistant", "reply")
	if err := legacy.saveLegacyMarkdown(dir, "legacy-1", "old", "", false, legacy.CreatedAt, legacy.UpdatedAt, ShellContext{}, legacy.GetMessages()); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	// meta for list
	legacy.format = FormatLegacyMD
	legacy.persistedCount = 2
	if err := legacy.Save(); err != nil {
		t.Fatalf("migrate Save: %v", err)
	}
	if legacy.Format() != FormatSegmentV1 {
		t.Fatalf("expected segment after save, got %s", legacy.Format())
	}
	if _, err := os.Stat(filepath.Join(dir, "legacy-1.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy md should be gone after migrate")
	}
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	loaded, ok := m.Get("legacy-1")
	if !ok {
		t.Fatal("migrated session not listed")
	}
	if len(loaded.GetMessages()) != 2 {
		t.Fatalf("expected 2 messages after migrate, got %d", len(loaded.GetMessages()))
	}
}

func TestGetMessagesPageSegmentWithoutFullLoad(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("page-seg", dir)
	for i := 0; i < 150; i++ {
		s.AddMessage("user", fmt.Sprintf("message-%d", i))
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	loaded, ok := m.Get("page-seg")
	if !ok {
		t.Fatal("missing session")
	}
	if loaded.messagesLoaded {
		t.Fatal("manager should not fully load messages")
	}
	first, total, more, err := loaded.GetMessagesPage(60, 0)
	if err != nil {
		t.Fatalf("GetMessagesPage: %v", err)
	}
	if total != 150 || !more || len(first) != 60 {
		t.Fatalf("page meta total=%d more=%v n=%d", total, more, len(first))
	}
	if first[0].Content != "message-90" || first[59].Content != "message-149" {
		t.Fatalf("unexpected page bounds %q .. %q", first[0].Content, first[59].Content)
	}
	if loaded.messagesLoaded {
		t.Fatal("paged read must not promote full session")
	}
}
