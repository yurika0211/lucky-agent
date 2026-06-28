package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/provider"
)

// ============================================================
// SS-1: Session 包测试补全 — ShellContext 相关测试
// ============================================================

// --- GetCwd/SetCwd ---
func TestSessionGetCwd(t *testing.T) {
	s := NewSession("test-cwd", t.TempDir())
	if s.GetCwd() != "" {
		t.Errorf("expected empty cwd initially, got %s", s.GetCwd())
	}
}

func TestSessionSetCwd(t *testing.T) {
	s := NewSession("test-setcwd", t.TempDir())
	s.SetCwd("/home/user/project")
	if s.GetCwd() != "/home/user/project" {
		t.Errorf("expected /home/user/project, got %s", s.GetCwd())
	}
}

func TestSessionSetCwdUpdatesTimestamp(t *testing.T) {
	s := NewSession("test-ts", t.TempDir())
	before := s.UpdatedAt
	s.SetCwd("/new/path")
	if s.UpdatedAt.Before(before) {
		t.Error("expected UpdatedAt to be updated")
	}
}

// --- GetEnv/SetEnv/UnsetEnv ---
func TestSessionGetEnv(t *testing.T) {
	s := NewSession("test-env", t.TempDir())
	env := s.GetEnv()
	if len(env) != 0 {
		t.Errorf("expected empty env initially, got %d entries", len(env))
	}
}

func TestSessionSetEnv(t *testing.T) {
	s := NewSession("test-setenv", t.TempDir())
	s.SetEnv("API_KEY", "secret123")
	env := s.GetEnv()
	if env["API_KEY"] != "secret123" {
		t.Errorf("expected secret123, got %s", env["API_KEY"])
	}
}

func TestSessionSetEnvUpdatesTimestamp(t *testing.T) {
	s := NewSession("test-envts", t.TempDir())
	before := s.UpdatedAt
	s.SetEnv("KEY", "value")
	if s.UpdatedAt.Before(before) {
		t.Error("expected UpdatedAt to be updated")
	}
}

func TestSessionUnsetEnv(t *testing.T) {
	s := NewSession("test-unsetenv", t.TempDir())
	s.SetEnv("KEY1", "value1")
	s.SetEnv("KEY2", "value2")
	s.UnsetEnv("KEY1")
	env := s.GetEnv()
	if _, ok := env["KEY1"]; ok {
		t.Error("KEY1 should be unset")
	}
	if env["KEY2"] != "value2" {
		t.Errorf("KEY2 should still exist: %s", env["KEY2"])
	}
}

func TestSessionUnsetEnvUpdatesTimestamp(t *testing.T) {
	s := NewSession("test-unsetts", t.TempDir())
	s.SetEnv("KEY", "value")
	before := s.UpdatedAt
	s.UnsetEnv("KEY")
	if s.UpdatedAt.Before(before) {
		t.Error("expected UpdatedAt to be updated")
	}
}

func TestSessionEnvIsolation(t *testing.T) {
	s1 := NewSession("test-iso1", t.TempDir())
	s2 := NewSession("test-iso2", t.TempDir())
	s1.SetEnv("KEY", "value1")
	s2.SetEnv("KEY", "value2")
	if s1.GetEnv()["KEY"] != "value1" {
		t.Error("s1 env should be isolated")
	}
	if s2.GetEnv()["KEY"] != "value2" {
		t.Error("s2 env should be isolated")
	}
}

// ============================================================
// SS-2: GetMessages 懒加载测试
// ============================================================

func TestGetMessagesLazyLoading(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("test-lazy", dir)
	s.AddMessage("user", "hello")
	s.Save()

	// 创建新 session 但不加载消息
	s2 := NewSession("test-lazy", dir)
	s2.messageCount = 1
	s2.messagesLoaded = false

	// 首次调用 GetMessages 应该加载消息
	msgs := s2.GetMessages()
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
	if !s2.messagesLoaded {
		t.Error("messagesLoaded should be true after loading")
	}
}

func TestGetMessagesAlreadyLoaded(t *testing.T) {
	s := NewSession("test-loaded", t.TempDir())
	s.AddMessage("user", "test")
	s.messagesLoaded = true

	msgs := s.GetMessages()
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

// ============================================================
// SS-3: Save/SaveAll 边界测试
// ============================================================

func TestSaveWithEmptyMessages(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("test-empty", dir)
	if err := s.Save(); err != nil {
		t.Fatalf("Save should not fail: %v", err)
	}
}

func TestSaveWithShellContext(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("test-shell", dir)
	s.SetCwd("/home/test")
	s.SetEnv("MY_VAR", "my_value")
	if err := s.Save(); err != nil {
		t.Fatalf("Save should not fail: %v", err)
	}

	// 使用 Manager 重新加载验证
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	s2, ok := m.Get("test-shell")
	if !ok {
		t.Fatal("session not found")
	}
	if s2.GetCwd() != "/home/test" {
		t.Errorf("expected /home/test, got %s", s2.GetCwd())
	}
	if s2.GetEnv()["MY_VAR"] != "my_value" {
		t.Errorf("expected my_value, got %s", s2.GetEnv()["MY_VAR"])
	}
}

func TestSaveAllWithEmptyManager(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	if err := m.SaveAll(); err != nil {
		t.Fatalf("SaveAll should not fail: %v", err)
	}
}

func TestSaveAllWithMultipleSessions(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	s1 := m.New()
	s1.AddMessage("user", "hello1")
	s2 := m.New()
	s2.AddMessage("user", "hello2")

	if err := m.SaveAll(); err != nil {
		t.Fatalf("SaveAll should not fail: %v", err)
	}

	// 重新加载验证
	m2, _ := NewManager(dir)
	if m2.Count() != 2 {
		t.Errorf("expected 2 sessions, got %d", m2.Count())
	}
}

// ============================================================
// SS-4: NewManager 边界测试
// ============================================================

func TestNewManagerWithNonExistentDir(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentDir := filepath.Join(tmpDir, "does_not_exist")
	m, err := NewManager(nonExistentDir)
	if err != nil {
		t.Fatalf("NewManager should create directory: %v", err)
	}
	if m == nil {
		t.Error("Manager should not be nil")
	}
}

func TestNewManagerLoadsExistingSessions(t *testing.T) {
	dir := t.TempDir()
	// 创建并保存一个 session
	m1, _ := NewManager(dir)
	s := m1.New()
	s.AddMessage("user", "test")
	s.Save()

	// 重新创建 Manager 应该加载已存在的 session
	m2, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	if m2.Count() != 1 {
		t.Errorf("expected 1 session, got %d", m2.Count())
	}
}

// ============================================================
// SS-5: Delete 边界测试
// ============================================================

func TestDeleteNonExistentSession(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	err := m.Delete("non-existent")
	if err == nil {
		t.Error("Delete should return error for non-existent session")
	}
}

func TestDeleteAndReload(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)
	s := m.New()
	s.AddMessage("user", "test")
	s.Save()

	if err := m.Delete(s.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// 重新加载验证删除
	m2, _ := NewManager(dir)
	if m2.Count() != 0 {
		t.Errorf("expected 0 sessions after delete, got %d", m2.Count())
	}
}

// ============================================================
// SS-6: Session 并发安全测试
// ============================================================

func TestSessionConcurrentAccess(t *testing.T) {
	s := NewSession("test-concurrent", t.TempDir())
	done := make(chan bool)

	// 并发写入
	go func() {
		for i := 0; i < 100; i++ {
			s.AddMessage("user", "message")
		}
		done <- true
	}()

	// 并发读取
	go func() {
		for i := 0; i < 100; i++ {
			s.GetMessages()
			s.MessageCount()
		}
		done <- true
	}()

	<-done
	<-done

	// 如果没有 panic，则测试通过
}

func TestEnvConcurrentAccess(t *testing.T) {
	s := NewSession("test-env-concurrent", t.TempDir())
	done := make(chan bool)

	// 并发写入环境变量
	go func() {
		for i := 0; i < 100; i++ {
			s.SetEnv("KEY"+string(rune(i)), "value")
		}
		done <- true
	}()

	// 并发读取环境变量
	go func() {
		for i := 0; i < 100; i++ {
			s.GetEnv()
		}
		done <- true
	}()

	<-done
	<-done
}

// ============================================================
// SS-7: Message 序列化测试
// ============================================================

func TestMessageSerialization(t *testing.T) {
	dir := t.TempDir()
	m1, _ := NewManager(dir)
	s := m1.New()
	s.AddMessage("user", "hello")
	s.AddMessage("assistant", "world")
	s.AddToolMessage("shell", "output")
	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// 使用 Manager 重新加载验证
	m2, _ := NewManager(dir)
	s2, ok := m2.Get(s.ID)
	if !ok {
		t.Fatal("session not found")
	}
	msgs := s2.GetMessages()
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages, got %d", len(msgs))
	}
}

func TestMessageOrderPreserved(t *testing.T) {
	dir := t.TempDir()
	m1, _ := NewManager(dir)
	s := m1.New()
	for i := 0; i < 10; i++ {
		s.AddMessage("user", "message-"+string(rune('A'+i)))
	}

	if err := s.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// 使用 Manager 重新加载验证顺序
	m2, _ := NewManager(dir)
	s2, ok := m2.Get(s.ID)
	if !ok {
		t.Fatal("session not found")
	}
	msgs := s2.GetMessages()
	if len(msgs) != 10 {
		t.Errorf("expected 10 messages, got %d", len(msgs))
	}
}

// ============================================================
// SS-8: Session 元数据测试
// ============================================================

func TestSessionTimestamps(t *testing.T) {
	s := NewSession("test-ts", t.TempDir())
	created := s.CreatedAt
	updated := s.UpdatedAt

	if created.After(updated) {
		t.Error("Created should not be after Updated")
	}

	// 添加消息后应该更新 UpdatedAt
	time.Sleep(10 * time.Millisecond) // 确保时间有差异
	s.AddMessage("user", "test")
	if s.UpdatedAt.Before(updated) {
		t.Error("UpdatedAt should be updated after adding message")
	}
}

func TestSessionTitleFromFirstMessage(t *testing.T) {
	s := NewSession("test-title", t.TempDir())
	s.AddMessage("user", "这是一个很长的消息用来测试标题截断功能是否正常")
	if s.Title == "" {
		t.Error("Title should be set from first message")
	}
}

// ============================================================
// SS-9: Manager 搜索功能增强测试
// ============================================================

func TestManagerSearchEmptyQuery(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	m.NewWithTitle("Test Session")
	results := m.Search("")
	if len(results) != 1 {
		t.Errorf("empty search should return all results, got %d", len(results))
	}
}

func TestManagerSearchMultipleMatches(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	m.NewWithTitle("Go Programming")
	m.NewWithTitle("Python Programming")
	m.NewWithTitle("Rust Programming")

	results := m.Search("Programming")
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

// ============================================================
// SS-10: 边界情况测试
// ============================================================

func TestSessionWithSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	m1, _ := NewManager(dir)
	s := m1.New()
	special := "特殊字符！@#$%^&*()_+-=[]{}|;':\",./<>?"
	s.AddMessage("user", special)
	if err := s.Save(); err != nil {
		t.Fatalf("Save should handle special characters: %v", err)
	}

	// 使用 Manager 重新加载验证
	m2, _ := NewManager(dir)
	s2, ok := m2.Get(s.ID)
	if !ok {
		t.Fatal("session not found")
	}
	msgs := s2.GetMessages()
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

func TestSessionWithUnicodeContent(t *testing.T) {
	dir := t.TempDir()
	m1, _ := NewManager(dir)
	s := m1.New()
	unicode := "こんにちは世界 🌍 你好世界"
	s.AddMessage("user", unicode)
	if err := s.Save(); err != nil {
		t.Fatalf("Save should handle unicode: %v", err)
	}

	// 使用 Manager 重新加载验证
	m2, _ := NewManager(dir)
	s2, ok := m2.Get(s.ID)
	if !ok {
		t.Fatal("session not found")
	}
	msgs := s2.GetMessages()
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

func TestManagerListInfoEmptyManager(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	infos := m.ListInfo()
	if len(infos) != 0 {
		t.Errorf("expected 0 infos, got %d", len(infos))
	}
}

func TestSessionGetMessagesReturnsCopy(t *testing.T) {
	s := NewSession("test-copy", t.TempDir())
	s.AddMessage("user", "original")

	msgs1 := s.GetMessages()
	msgs1 = append(msgs1, provider.Message{Role: "user", Content: "added"})

	msgs2 := s.GetMessages()
	if len(msgs2) != 1 {
		t.Errorf("GetMessages should return a copy, got %d messages", len(msgs2))
	}
}
