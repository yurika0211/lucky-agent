package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
)

func TestNewSession(t *testing.T) {
	s := NewSession("test-1", t.TempDir())
	if s.ID != "test-1" {
		t.Errorf("expected test-1, got %s", s.ID)
	}
	if len(s.Messages) != 0 {
		t.Errorf("expected empty messages, got %d", len(s.Messages))
	}
}

func TestAddMessage(t *testing.T) {
	s := NewSession("test-2", t.TempDir())
	s.AddMessage("user", "hello")
	s.AddMessage("assistant", "hi there")

	msgs := s.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}
}

func TestAutoTitle(t *testing.T) {
	s := NewSession("test-title", t.TempDir())
	if s.Title != "" {
		t.Error("expected empty title initially")
	}

	s.AddMessage("user", "this is a very long message that should be truncated to fifty characters or so")
	if s.Title != "this is a very long message that should be truncat..." {
		t.Errorf("expected truncated title, got: %s", s.Title)
	}
}

func TestAddToolMessage(t *testing.T) {
	s := NewSession("test-tool", t.TempDir())
	s.AddToolMessage("shell", "output: hello world")

	msgs := s.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "tool" {
		t.Errorf("expected tool role, got %s", msgs[0].Role)
	}
}

func TestAddProviderMessagePreservesToolCallFields(t *testing.T) {
	s := NewSession("test-provider-message", t.TempDir())
	s.AddProviderMessage(provider.Message{
		Role:             "tool",
		Content:          "42",
		ReasoningContent: "hidden reasoning",
		ToolCallID:       "call_abc123",
		Name:             "calculator",
	})

	msgs := s.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ToolCallID != "call_abc123" {
		t.Fatalf("expected tool_call_id call_abc123, got %q", msgs[0].ToolCallID)
	}
	if msgs[0].Name != "calculator" {
		t.Fatalf("expected name calculator, got %q", msgs[0].Name)
	}
	if msgs[0].ReasoningContent != "hidden reasoning" {
		t.Fatalf("expected reasoning content to be preserved, got %q", msgs[0].ReasoningContent)
	}
}

func TestLastMessage(t *testing.T) {
	s := NewSession("test-3", t.TempDir())
	if last := s.LastMessage(); last != nil {
		t.Error("expected nil for empty session")
	}

	s.AddMessage("user", "first")
	s.AddMessage("assistant", "second")

	last := s.LastMessage()
	if last.Content != "second" {
		t.Errorf("expected 'second', got %s", last.Content)
	}
}

func TestMessageCount(t *testing.T) {
	s := NewSession("test-count", t.TempDir())
	if s.MessageCount() != 0 {
		t.Errorf("expected 0 messages, got %d", s.MessageCount())
	}
	s.AddMessage("user", "hello")
	if s.MessageCount() != 1 {
		t.Errorf("expected 1 message, got %d", s.MessageCount())
	}
}

func TestSessionSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("test-save", dir)
	s.AddMessage("user", "test message")
	s.AddMessage("assistant", "response")

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load from disk
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	loaded, ok := m.Get("test-save")
	if !ok {
		t.Fatal("session not found after save/load")
	}
	if loaded.Title != "test message" {
		t.Errorf("expected title 'test message', got '%s'", loaded.Title)
	}
	if len(loaded.GetMessages()) != 2 {
		t.Errorf("expected 2 messages, got %d", len(loaded.GetMessages()))
	}
}

func TestAssistantMessageSanitizesToolProtocolLeakage(t *testing.T) {
	s := NewSession("test-sanitize", t.TempDir())
	s.AddMessage("assistant", "to=cron_list\n{\"name\":\"cron_list\",\"arguments\":{}}\n仍然没有已配置的定时任务。")

	msgs := s.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "仍然没有已配置的定时任务。" {
		t.Fatalf("unexpected sanitized content: %q", msgs[0].Content)
	}
}

func TestManagerNew(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	s := m.New()
	if s.ID == "" {
		t.Error("expected non-empty session ID")
	}

	s2, ok := m.Get(s.ID)
	if !ok {
		t.Error("session not found after creation")
	}
	if s2.ID != s.ID {
		t.Errorf("expected %s, got %s", s.ID, s2.ID)
	}
}

func TestManagerNewWithTitle(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	s := m.NewWithTitle("My Session")
	if s.Title != "My Session" {
		t.Errorf("expected 'My Session', got '%s'", s.Title)
	}
}

func TestManagerEnsure(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	s1 := m.Ensure("ws-session")
	if s1.ID != "ws-session" {
		t.Fatalf("expected ensured session ID ws-session, got %q", s1.ID)
	}

	s2 := m.Ensure("ws-session")
	if s1 != s2 {
		t.Fatal("expected Ensure to return the existing session instance")
	}
}

func TestManagerNewGeneratesUniqueIDs(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	first := m.New()
	second := m.New()
	if first.ID == second.ID {
		t.Fatalf("expected unique session IDs, got %q", first.ID)
	}
}

func TestLoadMarkdownSessionWithEmbeddedCodeFenceText(t *testing.T) {
	dir := t.TempDir()
	content := "# LuckyAgent Session\n\n" +
		"自动生成，请勿手动编辑 JSON 块。\n\n" +
		"```json\n" +
		"{\n" +
		"  \"id\": \"test-md-session\",\n" +
		"  \"title\": \"demo\",\n" +
		"  \"messages\": [\n" +
		"    {\n" +
		"      \"role\": \"assistant\",\n" +
		"      \"content\": \"Assistant: ```tool\\n{\\\"name\\\":\\\"cron_list\\\"}\\n```\"\n" +
		"    }\n" +
		"  ],\n" +
		"  \"created_at\": \"2026-04-30T00:00:00Z\",\n" +
		"  \"updated_at\": \"2026-04-30T00:00:00Z\",\n" +
		"  \"shell_context\": {\n" +
		"    \"cwd\": \"\",\n" +
		"    \"env\": null\n" +
		"  }\n" +
		"}\n" +
		"```\n"
	if err := os.WriteFile(filepath.Join(dir, "test-md-session.md"), []byte(content), 0600); err != nil {
		t.Fatalf("write session md: %v", err)
	}

	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	sess, ok := mgr.Get("test-md-session")
	if !ok {
		t.Fatal("expected session to load")
	}
	msgs := sess.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestManagerList(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	m.New()
	m.New()
	m.New()

	sessions := m.List()
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestManagerListInfo(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	s1 := m.New()
	s1.AddMessage("user", "hello")

	s2 := m.New()
	s2.AddMessage("user", "world")

	infos := m.ListInfo()
	if len(infos) != 2 {
		t.Fatalf("expected 2 infos, got %d", len(infos))
	}

	// Should be sorted by UpdatedAt (most recent first)
	if infos[0].MessageCount != 1 {
		t.Errorf("expected 1 message, got %d", infos[0].MessageCount)
	}
}

func TestManagerSearch(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	s1 := m.NewWithTitle("Go Programming")
	s1.AddMessage("user", "how to use goroutines")

	s2 := m.NewWithTitle("Python Tips")
	s2.AddMessage("user", "list comprehension tricks")

	// Search by title
	results := m.Search("go")
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'go', got %d", len(results))
	}

	// Search by content
	results = m.Search("comprehension")
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'comprehension', got %d", len(results))
	}

	// Case insensitive
	results = m.Search("PYTHON")
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'PYTHON', got %d", len(results))
	}

	// No results
	results = m.Search("rust")
	if len(results) != 0 {
		t.Errorf("expected 0 results for 'rust', got %d", len(results))
	}
}

func TestManagerDelete(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	s := m.New()
	s.AddMessage("user", "test")
	s.Save()

	if err := m.Delete(s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, ok := m.Get(s.ID); ok {
		t.Error("session should be deleted")
	}
}

func TestManagerSaveAll(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	s1 := m.New()
	s1.AddMessage("user", "hello")

	s2 := m.New()
	s2.AddMessage("user", "world")

	if err := m.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	// Reload
	m2, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager2: %v", err)
	}

	if m2.Count() != 2 {
		t.Errorf("expected 2 sessions after reload, got %d", m2.Count())
	}
}

func TestManagerCount(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if m.Count() != 0 {
		t.Errorf("expected 0, got %d", m.Count())
	}

	m.New()
	m.New()

	if m.Count() != 2 {
		t.Errorf("expected 2, got %d", m.Count())
	}
}
