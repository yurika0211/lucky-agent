package memory

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMidTermStoreSave(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	summary := &SessionSummary{
		SessionID:     "sess-001",
		UserID:        "user-1",
		CreatedAt:     time.Now(),
		Topics:        []string{"debugging", "performance"},
		KeyDecisions:  []string{"decided to use Redis for caching"},
		OpenQuestions: []string{"how to handle connection pooling?"},
		CodeContext:   "internal/cache/redis.go",
		RawSummary:    "User discussed Redis caching strategy",
	}

	if err := store.SaveSessionSummary(summary); err != nil {
		t.Fatalf("SaveSessionSummary: %v", err)
	}

	if store.Count() != 1 {
		t.Errorf("expected 1 summary, got %d", store.Count())
	}
}

func TestSaveSessionSummarySanitizesGeneratedTruncationMarkers(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	summary := &SessionSummary{
		SessionID:     "sess-truncated",
		UserID:        "user-1",
		CreatedAt:     time.Now(),
		Topics:        []string{"debugging", "...[truncated]"},
		KeyDecisions:  []string{"Use graph memory", "... (truncated for context)"},
		OpenQuestions: []string{"Can ordinary ellipsis wait... stay?"},
		CodeContext:   "internal/memory\n[... output truncated ...]",
		RawSummary:    "Compressed context\n...[truncated]\nKeep wait... as prose.",
	}
	if err := store.SaveSessionSummary(summary); err != nil {
		t.Fatalf("SaveSessionSummary: %v", err)
	}

	got, ok := store.Get("sess-truncated")
	if !ok {
		t.Fatal("expected saved summary")
	}
	joined := strings.Join(append(append(append([]string{got.RawSummary, got.CodeContext}, got.Topics...), got.KeyDecisions...), got.OpenQuestions...), "\n")
	for _, dirty := range []string{"...[truncated]", "... (truncated for context)", "[... output truncated ...]"} {
		if strings.Contains(joined, dirty) {
			t.Fatalf("generated truncation marker %q leaked into summary: %q", dirty, joined)
		}
	}
	if !strings.Contains(joined, "wait...") {
		t.Fatalf("ordinary ellipsis should be preserved: %q", joined)
	}
}

func TestMidTermStoreGet(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	summary := &SessionSummary{
		SessionID:  "sess-001",
		UserID:     "user-1",
		CreatedAt:  time.Now(),
		Topics:     []string{"debugging"},
		RawSummary: "Debug session",
	}
	store.SaveSessionSummary(summary)

	got, ok := store.Get("sess-001")
	if !ok {
		t.Fatal("expected to find summary")
	}
	if got.SessionID != "sess-001" {
		t.Errorf("expected session ID sess-001, got %s", got.SessionID)
	}
	if got.RawSummary != "Debug session" {
		t.Errorf("expected 'Debug session', got '%s'", got.RawSummary)
	}

	// 不存在的
	_, ok = store.Get("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent session")
	}
}

func TestMidTermStoreSearch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	// 保存几个摘要
	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-001",
		UserID:     "user-1",
		CreatedAt:  time.Now(),
		Topics:     []string{"debugging"},
		RawSummary: "User was debugging a Go API server crash",
	})

	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-002",
		UserID:     "user-1",
		CreatedAt:  time.Now(),
		Topics:     []string{"deployment"},
		RawSummary: "Deployed the application to Kubernetes",
	})

	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-003",
		UserID:     "user-1",
		CreatedAt:  time.Now(),
		Topics:     []string{"debugging", "performance"},
		RawSummary: "Performance optimization for database queries",
	})

	// 搜索 debugging 相关
	results := store.SearchSummaries("debugging", 2)
	if len(results) == 0 {
		t.Error("expected search results for 'debugging'")
	}

	// 搜索 Go 相关
	results = store.SearchSummaries("Go", 3)
	if len(results) == 0 {
		t.Error("expected search results for 'Go'")
	}

	// 搜索不相关的内容
	results = store.SearchSummaries("cooking recipes", 3)
	if len(results) != 0 {
		t.Error("expected no results for unrelated query")
	}
}

func TestMidTermStoreSearchTimeDecay(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	// 旧摘要
	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-old",
		UserID:     "user-1",
		CreatedAt:  time.Now().Add(-180 * 24 * time.Hour), // 6 个月前
		Topics:     []string{"debugging"},
		RawSummary: "Debugging Go API server crash",
	})

	// 新摘要
	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-new",
		UserID:     "user-1",
		CreatedAt:  time.Now(),
		Topics:     []string{"debugging"},
		RawSummary: "Debugging Go API server crash",
	})

	// 搜索应该优先返回新的
	results := store.SearchSummaries("debugging Go", 2)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// 新的应该排在前面
	if results[0].SessionID != "sess-new" {
		t.Errorf("expected newer summary first, got %s", results[0].SessionID)
	}
}

func TestMidTermStoreExpire(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	// 旧摘要
	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-old",
		UserID:     "user-1",
		CreatedAt:  time.Now().Add(-100 * 24 * time.Hour), // 100 天前
		RawSummary: "Old session",
	})

	// 新摘要
	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-new",
		UserID:     "user-1",
		CreatedAt:  time.Now(),
		RawSummary: "New session",
	})

	if store.Count() != 2 {
		t.Fatalf("expected 2 before expire, got %d", store.Count())
	}

	// 过期 90 天前的
	expired := store.ExpireOldSummaries(90 * 24 * time.Hour)
	if expired != 1 {
		t.Errorf("expected 1 expired, got %d", expired)
	}

	if store.Count() != 1 {
		t.Errorf("expected 1 after expire, got %d", store.Count())
	}

	// 剩余的应该是新的
	got, ok := store.Get("sess-new")
	if !ok {
		t.Fatal("expected new summary to survive")
	}
	if got.RawSummary != "New session" {
		t.Errorf("expected 'New session', got '%s'", got.RawSummary)
	}
}

func TestMidTermStorePersistence(t *testing.T) {
	dir := t.TempDir()

	// 创建并保存
	store1, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore1: %v", err)
	}

	store1.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-001",
		UserID:     "user-1",
		CreatedAt:  time.Now(),
		Topics:     []string{"debugging"},
		RawSummary: "Persistent session",
	})

	// 重新加载
	store2, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore2: %v", err)
	}

	if store2.Count() != 1 {
		t.Errorf("expected 1 after reload, got %d", store2.Count())
	}

	got, ok := store2.Get("sess-001")
	if !ok {
		t.Fatal("expected to find persisted summary")
	}
	if got.RawSummary != "Persistent session" {
		t.Errorf("expected 'Persistent session', got '%s'", got.RawSummary)
	}
}

func TestMidTermStoreMaxSummaries(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 3) // 最多 3 个
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	// 保存 5 个摘要
	for i := 0; i < 5; i++ {
		store.SaveSessionSummary(&SessionSummary{
			SessionID:  fmt.Sprintf("sess-%03d", i),
			UserID:     "user-1",
			CreatedAt:  time.Now().Add(time.Duration(i) * time.Minute),
			RawSummary: fmt.Sprintf("Session %d", i),
		})
	}

	// 应该只保留 3 个（最新的）
	if store.Count() != 3 {
		t.Errorf("expected 3 summaries (max), got %d", store.Count())
	}

	// 最旧的应该被驱逐
	_, ok := store.Get("sess-000")
	if ok {
		t.Error("expected oldest summary to be evicted")
	}
}

func TestMidTermStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-001",
		UserID:     "user-1",
		CreatedAt:  time.Now(),
		RawSummary: "To be deleted",
	})

	if err := store.Delete("sess-001"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if store.Count() != 0 {
		t.Errorf("expected 0 after delete, got %d", store.Count())
	}

	// 删除不存在的
	if err := store.Delete("nonexistent"); err == nil {
		t.Error("expected error for deleting nonexistent summary")
	}
}

func TestMidTermStoreMergeSummary(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	// 先保存
	store.SaveSessionSummary(&SessionSummary{
		SessionID:    "sess-001",
		UserID:       "user-1",
		CreatedAt:    time.Now(),
		Topics:       []string{"debugging"},
		KeyDecisions: []string{"use Redis"},
		RawSummary:   "First version",
	})

	// 再保存同 ID（合并）
	store.SaveSessionSummary(&SessionSummary{
		SessionID:    "sess-001",
		UserID:       "user-1",
		CreatedAt:    time.Now().Add(time.Minute),
		Topics:       []string{"performance"},
		KeyDecisions: []string{"add connection pooling"},
		RawSummary:   "Updated version",
	})

	if store.Count() != 1 {
		t.Errorf("expected 1 (merged), got %d", store.Count())
	}

	got, _ := store.Get("sess-001")
	if len(got.Topics) != 2 {
		t.Errorf("expected 2 topics after merge, got %d: %v", len(got.Topics), got.Topics)
	}
	if len(got.KeyDecisions) != 2 {
		t.Errorf("expected 2 decisions after merge, got %d: %v", len(got.KeyDecisions), got.KeyDecisions)
	}
	if got.RawSummary != "Updated version" {
		t.Errorf("expected updated raw_summary, got '%s'", got.RawSummary)
	}
}

func TestMidTermStoreListAll(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-001",
		UserID:     "user-1",
		CreatedAt:  time.Now().Add(-2 * time.Hour),
		RawSummary: "Older",
	})
	store.SaveSessionSummary(&SessionSummary{
		SessionID:  "sess-002",
		UserID:     "user-1",
		CreatedAt:  time.Now(),
		RawSummary: "Newer",
	})

	all := store.ListAll()
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}

	// 应该按时间降序（新的在前）
	if all[0].SessionID != "sess-002" {
		t.Errorf("expected newest first, got %s", all[0].SessionID)
	}
}

func TestMidTermStoreEmptySearch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	results := store.SearchSummaries("anything", 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty store, got %d", len(results))
	}
}

func TestMidTermStoreSaveValidation(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMidTermStore(dir, 100)
	if err != nil {
		t.Fatalf("NewMidTermStore: %v", err)
	}

	// 空 session ID 应该报错
	err = store.SaveSessionSummary(&SessionSummary{
		SessionID: "",
		UserID:    "user-1",
	})
	if err == nil {
		t.Error("expected error for empty session ID")
	}
}

// --- GenerateSessionSummary 测试 ---

func TestGenerateSessionSummary(t *testing.T) {
	messages := []ConversationTurn{
		{Role: "user", Content: "I decided to use Go for the backend"},
		{Role: "assistant", Content: "Good choice. Go is great for building APIs."},
		{Role: "user", Content: "How do I implement connection pooling?"},
		{Role: "assistant", Content: "You can use sql.DB which manages connection pooling internally."},
	}

	summary := GenerateSessionSummary("sess-001", "user-1", messages)

	if summary.SessionID != "sess-001" {
		t.Errorf("expected session ID sess-001, got %s", summary.SessionID)
	}
	if summary.UserID != "user-1" {
		t.Errorf("expected user ID user-1, got %s", summary.UserID)
	}
	if len(summary.Topics) == 0 {
		t.Error("expected some topics to be extracted")
	}
	if summary.RawSummary == "" {
		t.Error("expected non-empty raw summary")
	}
}

func TestGenerateSessionSummaryEmpty(t *testing.T) {
	summary := GenerateSessionSummary("sess-001", "user-1", nil)
	if summary.RawSummary != "" {
		t.Error("expected empty raw summary for nil messages")
	}
}

// --- 辅助函数测试 ---

func TestExtractTopics(t *testing.T) {
	messages := []ConversationTurn{
		{Role: "user", Content: "I need to debug this error in the API"},
		{Role: "assistant", Content: "Let's look at the error logs"},
	}

	topics := extractTopics(messages)
	if len(topics) == 0 {
		t.Error("expected some topics")
	}

	found := false
	for _, t := range topics {
		if t == "debugging" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'debugging' topic, got %v", topics)
	}
}

func TestExtractKeyDecisions(t *testing.T) {
	messages := []ConversationTurn{
		{Role: "user", Content: "We decided to use Redis for caching"},
		{Role: "assistant", Content: "Redis is a good choice for caching"},
	}

	decisions := extractKeyDecisions(messages)
	if len(decisions) == 0 {
		t.Error("expected some key decisions")
	}
}

func TestExtractOpenQuestions(t *testing.T) {
	messages := []ConversationTurn{
		{Role: "user", Content: "How do I implement the API?"},
		{Role: "assistant", Content: "You can use the standard library"},
	}

	questions := extractOpenQuestions(messages)
	if len(questions) == 0 {
		t.Error("expected some open questions")
	}
}

func TestExtractCodeContext(t *testing.T) {
	messages := []ConversationTurn{
		{Role: "user", Content: "Look at this func main() in cmd/server/main.go"},
		{Role: "assistant", Content: "I see the issue in the import statement"},
	}

	codeCtx := extractCodeContext(messages)
	if codeCtx == "" {
		t.Error("expected non-empty code context")
	}
}

func TestMergeStringSlices(t *testing.T) {
	a := []string{"debugging", "performance"}
	b := []string{"performance", "deployment"}

	result := mergeStringSlices(a, b)
	if len(result) != 3 {
		t.Errorf("expected 3 unique items, got %d: %v", len(result), result)
	}
}

// 需要导入 fmt
var _ = os.DevNull
