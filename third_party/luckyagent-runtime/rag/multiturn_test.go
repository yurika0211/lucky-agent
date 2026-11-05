package rag

import (
	"context"
	"testing"
)

// ===== v0.16.0: Multi-turn RAG tests =====

// --- ConversationContext tests ---

func TestNewConversationContext(t *testing.T) {
	cc := NewConversationContext(10)
	if cc == nil {
		t.Fatal("expected non-nil ConversationContext")
	}
	if cc.Len() != 0 {
		t.Errorf("expected 0 turns, got %d", cc.Len())
	}
}

func TestNewConversationContextDefaultLimit(t *testing.T) {
	cc := NewConversationContext(0)
	if cc.limit != 20 {
		t.Errorf("expected default limit 20, got %d", cc.limit)
	}
}

func TestConversationContextAddTurn(t *testing.T) {
	cc := NewConversationContext(10)
	cc.AddUserTurn("hello")
	cc.AddAssistantTurn("hi there")

	if cc.Len() != 2 {
		t.Errorf("expected 2 turns, got %d", cc.Len())
	}
}

func TestConversationContextEviction(t *testing.T) {
	cc := NewConversationContext(3)
	cc.AddUserTurn("a")
	cc.AddUserTurn("b")
	cc.AddUserTurn("c")
	cc.AddUserTurn("d") // should evict "a"

	if cc.Len() != 3 {
		t.Errorf("expected 3 turns after eviction, got %d", cc.Len())
	}

	turns := cc.Turns()
	if turns[0].Content != "b" {
		t.Errorf("expected first turn 'b', got %s", turns[0].Content)
	}
}

func TestConversationContextLastTurn(t *testing.T) {
	cc := NewConversationContext(10)
	if last := cc.LastTurn(); last != nil {
		t.Error("expected nil for empty context")
	}

	cc.AddUserTurn("hello")
	cc.AddAssistantTurn("hi")
	last := cc.LastTurn()
	if last == nil {
		t.Fatal("expected non-nil last turn")
	}
	if last.Content != "hi" {
		t.Errorf("expected 'hi', got %s", last.Content)
	}
}

func TestConversationContextLastUserTurn(t *testing.T) {
	cc := NewConversationContext(10)
	cc.AddUserTurn("hello")
	cc.AddAssistantTurn("hi")
	cc.AddAssistantTurn("how can I help?")

	last := cc.LastUserTurn()
	if last == nil {
		t.Fatal("expected non-nil last user turn")
	}
	if last.Content != "hello" {
		t.Errorf("expected 'hello', got %s", last.Content)
	}
}

func TestConversationContextRecentTurns(t *testing.T) {
	cc := NewConversationContext(10)
	cc.AddUserTurn("a")
	cc.AddUserTurn("b")
	cc.AddUserTurn("c")

	recent := cc.RecentTurns(2)
	if len(recent) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(recent))
	}
	if recent[0].Content != "b" {
		t.Errorf("expected 'b', got %s", recent[0].Content)
	}
}

func TestConversationContextRecentTurnsMoreThanAvailable(t *testing.T) {
	cc := NewConversationContext(10)
	cc.AddUserTurn("a")
	recent := cc.RecentTurns(5)
	if len(recent) != 1 {
		t.Errorf("expected 1 turn, got %d", len(recent))
	}
}

func TestConversationContextClear(t *testing.T) {
	cc := NewConversationContext(10)
	cc.AddUserTurn("hello")
	cc.Clear()
	if cc.Len() != 0 {
		t.Errorf("expected 0 turns after clear, got %d", cc.Len())
	}
}

func TestConversationContextSummary(t *testing.T) {
	cc := NewConversationContext(10)
	if summary := cc.Summary(); summary != "(empty conversation)" {
		t.Errorf("expected empty summary, got %s", summary)
	}

	cc.AddUserTurn("hello")
	cc.AddAssistantTurn("hi")
	summary := cc.Summary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestConversationContextTurnsCopy(t *testing.T) {
	cc := NewConversationContext(10)
	cc.AddUserTurn("hello")
	turns := cc.Turns()
	turns[0].Content = "modified"
	if cc.Turns()[0].Content == "modified" {
		t.Error("Turns() should return a copy")
	}
}

// --- QueryRewriter tests ---

func TestQueryRewriterNone(t *testing.T) {
	qr := NewQueryRewriter(RewriteNone)
	results, err := qr.Rewrite("hello world", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0] != "hello world" {
		t.Errorf("expected ['hello world'], got %v", results)
	}
}

func TestQueryRewriterExpandNoContext(t *testing.T) {
	qr := NewQueryRewriter(RewriteExpand)
	results, err := qr.Rewrite("hello world", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0] != "hello world" {
		t.Errorf("expected ['hello world'], got %v", results)
	}
}

func TestQueryRewriterExpandWithContext(t *testing.T) {
	qr := NewQueryRewriter(RewriteExpand)
	cc := NewConversationContext(10)
	cc.AddUserTurn("Tell me about machine learning algorithms")

	results, err := qr.Rewrite("neural networks", cc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// Should contain original query plus context terms
	if results[0] == "neural networks" {
		t.Error("expected expanded query, got original")
	}
}

func TestQueryRewriterDecompose(t *testing.T) {
	qr := NewQueryRewriter(RewriteDecompose)
	results, err := qr.Rewrite("cats and dogs", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 decomposed queries, got %d", len(results))
	}
}

func TestQueryRewriterDecomposeNoConjunction(t *testing.T) {
	qr := NewQueryRewriter(RewriteDecompose)
	results, err := qr.Rewrite("simple query", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 query (no decomposition), got %d", len(results))
	}
}

func TestQueryRewriterClarify(t *testing.T) {
	qr := NewQueryRewriter(RewriteClarify)
	cc := NewConversationContext(10)
	cc.AddUserTurn("Tell me about Go programming language")

	results, err := qr.Rewrite("what about it?", cc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// Should have replaced "it" with context term
	if results[0] == "what about it?" {
		t.Error("expected clarified query with pronoun replaced")
	}
}

func TestQueryRewriterClarifyNoContext(t *testing.T) {
	qr := NewQueryRewriter(RewriteClarify)
	results, err := qr.Rewrite("what about it?", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0] != "what about it?" {
		t.Errorf("expected original query without context, got %v", results)
	}
}

func TestQueryRewriterStrategy(t *testing.T) {
	qr := NewQueryRewriter(RewriteExpand)
	if qr.Strategy() != RewriteExpand {
		t.Errorf("expected RewriteExpand, got %s", qr.Strategy())
	}
	qr.SetStrategy(RewriteDecompose)
	if qr.Strategy() != RewriteDecompose {
		t.Errorf("expected RewriteDecompose, got %s", qr.Strategy())
	}
}

func TestQueryRewriterInvalidStrategy(t *testing.T) {
	qr := NewQueryRewriter(RewriteStrategy("unknown"))
	results, err := qr.Rewrite("test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0] != "test" {
		t.Errorf("expected original query for unknown strategy, got %v", results)
	}
}

// --- FollowUpDetector tests ---

func TestFollowUpDetectorNoContext(t *testing.T) {
	fd := NewFollowUpDetector(0.5)
	result := fd.Detect("hello", nil)
	if result.IsFollowUp {
		t.Error("expected not a follow-up with no context")
	}
}

func TestFollowUpDetectorEmptyContext(t *testing.T) {
	fd := NewFollowUpDetector(0.5)
	cc := NewConversationContext(10)
	result := fd.Detect("hello", cc)
	if result.IsFollowUp {
		t.Error("expected not a follow-up with empty context")
	}
}

func TestFollowUpDetectorClarify(t *testing.T) {
	fd := NewFollowUpDetector(0.5)
	cc := NewConversationContext(10)
	cc.AddAssistantTurn("Go is a programming language")

	result := fd.Detect("can you explain more?", cc)
	if !result.IsFollowUp {
		t.Error("expected follow-up for clarification")
	}
	if result.Type != FollowUpClarify {
		t.Errorf("expected FollowUpClarify, got %s", result.Type)
	}
}

func TestFollowUpDetectorDeepen(t *testing.T) {
	fd := NewFollowUpDetector(0.5)
	cc := NewConversationContext(10)
	cc.AddAssistantTurn("Go has goroutines for concurrency")

	result := fd.Detect("tell me more about goroutines", cc)
	if !result.IsFollowUp {
		t.Error("expected follow-up for deepening")
	}
	if result.Type != FollowUpDeepen {
		t.Errorf("expected FollowUpDeepen, got %s", result.Type)
	}
}

func TestFollowUpDetectorCompare(t *testing.T) {
	fd := NewFollowUpDetector(0.5)
	cc := NewConversationContext(10)
	cc.AddAssistantTurn("Go uses goroutines")

	result := fd.Detect("compare goroutines and threads", cc)
	if !result.IsFollowUp {
		t.Error("expected follow-up for comparison")
	}
	if result.Type != FollowUpCompare {
		t.Errorf("expected FollowUpCompare, got %s", result.Type)
	}
}

func TestFollowUpDetectorPronoun(t *testing.T) {
	fd := NewFollowUpDetector(0.5)
	cc := NewConversationContext(10)
	cc.AddAssistantTurn("Rust is a systems programming language")

	result := fd.Detect("what about it?", cc)
	if !result.IsFollowUp {
		t.Error("expected follow-up for pronoun reference")
	}
}

func TestFollowUpDetectorShortQuery(t *testing.T) {
	fd := NewFollowUpDetector(0.5)
	cc := NewConversationContext(10)
	cc.AddAssistantTurn("Go is a programming language")

	result := fd.Detect("why?", cc)
	if !result.IsFollowUp {
		t.Error("expected follow-up for short query")
	}
}

func TestFollowUpDetectorNewTopic(t *testing.T) {
	fd := NewFollowUpDetector(0.5)
	cc := NewConversationContext(10)
	cc.AddAssistantTurn("Go is a programming language")

	result := fd.Detect("What is the weather like today?", cc)
	if result.IsFollowUp {
		t.Error("expected not a follow-up for new topic")
	}
}

func TestFollowUpDetectorChineseClarify(t *testing.T) {
	fd := NewFollowUpDetector(0.5)
	cc := NewConversationContext(10)
	cc.AddAssistantTurn("Go 是一门编程语言")

	result := fd.Detect("解释一下", cc)
	if !result.IsFollowUp {
		t.Error("expected follow-up for Chinese clarification")
	}
}

func TestFollowUpDetectorThreshold(t *testing.T) {
	fd := NewFollowUpDetector(0.7)
	if fd.Threshold() != 0.7 {
		t.Errorf("expected threshold 0.7, got %f", fd.Threshold())
	}
}

func TestFollowUpDetectorDefaultThreshold(t *testing.T) {
	fd := NewFollowUpDetector(0)
	if fd.Threshold() != 0.5 {
		t.Errorf("expected default threshold 0.5, got %f", fd.Threshold())
	}
}

// --- FeedbackStore tests ---

func TestNewFeedbackStore(t *testing.T) {
	fs := NewFeedbackStore(100)
	if fs == nil {
		t.Fatal("expected non-nil FeedbackStore")
	}
	if fs.Len() != 0 {
		t.Errorf("expected 0 entries, got %d", fs.Len())
	}
}

func TestNewFeedbackStoreDefaultLimit(t *testing.T) {
	fs := NewFeedbackStore(0)
	if fs.limit != 1000 {
		t.Errorf("expected default limit 1000, got %d", fs.limit)
	}
}

func TestFeedbackStoreRecord(t *testing.T) {
	fs := NewFeedbackStore(100)
	fs.Record("test query", []RetrievalResult{
		{ChunkID: "c1", Score: 0.9},
	}, FeedbackPositive, "good results")

	if fs.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", fs.Len())
	}
}

func TestFeedbackStoreStats(t *testing.T) {
	fs := NewFeedbackStore(100)
	fs.Record("q1", nil, FeedbackPositive, "")
	fs.Record("q2", nil, FeedbackNegative, "")
	fs.Record("q3", nil, FeedbackPositive, "")

	stats := fs.Stats()
	if stats.TotalQueries != 3 {
		t.Errorf("expected 3 total, got %d", stats.TotalQueries)
	}
	if stats.PositiveCount != 2 {
		t.Errorf("expected 2 positive, got %d", stats.PositiveCount)
	}
	if stats.NegativeCount != 1 {
		t.Errorf("expected 1 negative, got %d", stats.NegativeCount)
	}
}

func TestFeedbackStorePositiveRate(t *testing.T) {
	fs := NewFeedbackStore(100)
	fs.Record("q1", nil, FeedbackPositive, "")
	fs.Record("q2", nil, FeedbackPositive, "")
	fs.Record("q3", nil, FeedbackNegative, "")

	rate := fs.PositiveRate()
	if rate != 2.0/3.0 {
		t.Errorf("expected positive rate %.2f, got %.2f", 2.0/3.0, rate)
	}
}

func TestFeedbackStorePositiveRateEmpty(t *testing.T) {
	fs := NewFeedbackStore(100)
	if rate := fs.PositiveRate(); rate != 0 {
		t.Errorf("expected 0 rate for empty store, got %f", rate)
	}
}

func TestFeedbackStoreEviction(t *testing.T) {
	fs := NewFeedbackStore(3)
	fs.Record("q1", nil, FeedbackPositive, "")
	fs.Record("q2", nil, FeedbackPositive, "")
	fs.Record("q3", nil, FeedbackPositive, "")
	fs.Record("q4", nil, FeedbackPositive, "")

	if fs.Len() != 3 {
		t.Errorf("expected 3 entries after eviction, got %d", fs.Len())
	}
}

func TestFeedbackStoreRecentFeedback(t *testing.T) {
	fs := NewFeedbackStore(100)
	fs.Record("q1", nil, FeedbackPositive, "")
	fs.Record("q2", nil, FeedbackNegative, "")
	fs.Record("q3", nil, FeedbackPartial, "")

	recent := fs.RecentFeedback(2)
	if len(recent) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(recent))
	}
	if recent[0].Query != "q2" {
		t.Errorf("expected 'q2', got %s", recent[0].Query)
	}
}

func TestFeedbackStoreClear(t *testing.T) {
	fs := NewFeedbackStore(100)
	fs.Record("q1", nil, FeedbackPositive, "")
	fs.Clear()
	if fs.Len() != 0 {
		t.Errorf("expected 0 after clear, got %d", fs.Len())
	}
	stats := fs.Stats()
	if stats.TotalQueries != 0 {
		t.Errorf("expected 0 total queries after clear, got %d", stats.TotalQueries)
	}
}

func TestFeedbackStoreShouldAdjustStrategyNotEnough(t *testing.T) {
	fs := NewFeedbackStore(100)
	fs.Record("q1", nil, FeedbackNegative, "")
	adjust, _ := fs.ShouldAdjustStrategy()
	if adjust {
		t.Error("should not adjust with < 5 queries")
	}
}

func TestFeedbackStoreShouldAdjustStrategyHighNegative(t *testing.T) {
	fs := NewFeedbackStore(100)
	for i := 0; i < 6; i++ {
		fs.Record("q", nil, FeedbackNegative, "")
	}
	adjust, strategy := fs.ShouldAdjustStrategy()
	if !adjust {
		t.Error("should adjust with high negative rate")
	}
	if strategy != RewriteDecompose {
		t.Errorf("expected RewriteDecompose, got %s", strategy)
	}
}

func TestFeedbackStoreShouldAdjustStrategyHighPositive(t *testing.T) {
	fs := NewFeedbackStore(100)
	for i := 0; i < 8; i++ {
		fs.Record("q", nil, FeedbackPositive, "")
	}
	adjust, _ := fs.ShouldAdjustStrategy()
	if adjust {
		t.Error("should not adjust with high positive rate")
	}
}

func TestFeedbackStoreShouldAdjustStrategyHighPartial(t *testing.T) {
	fs := NewFeedbackStore(100)
	for i := 0; i < 4; i++ {
		fs.Record("q", nil, FeedbackPartial, "")
	}
	fs.Record("q", nil, FeedbackPositive, "")
	adjust, strategy := fs.ShouldAdjustStrategy()
	if !adjust {
		t.Error("should adjust with high partial rate")
	}
	if strategy != RewriteExpand {
		t.Errorf("expected RewriteExpand, got %s", strategy)
	}
}

// --- ContextAwareRetriever integration tests ---

func TestContextAwareRetrieverSearch(t *testing.T) {
	embedder := newMockEmbedder(64)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	// Index some content
	_, err := rag.IndexText("test", "Go Programming", "Go is a statically typed compiled language designed at Google")
	if err != nil {
		t.Fatalf("IndexText: %v", err)
	}

	rewriter := NewQueryRewriter(RewriteExpand)
	detector := NewFollowUpDetector(0.5)
	ctx := NewConversationContext(10)

	car := NewContextAwareRetriever(rag, rewriter, detector, ctx)

	result, err := car.Search(context.Background(), "Go language")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Query != "Go language" {
		t.Errorf("expected query 'Go language', got %s", result.Query)
	}
	if len(result.RewrittenQuery) < 1 {
		t.Error("expected at least 1 rewritten query")
	}
}

func TestContextAwareRetrieverFollowUp(t *testing.T) {
	embedder := newMockEmbedder(64)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	_, err := rag.IndexText("test", "Go Programming", "Go is a statically typed compiled language designed at Google")
	if err != nil {
		t.Fatalf("IndexText: %v", err)
	}

	rewriter := NewQueryRewriter(RewriteExpand)
	detector := NewFollowUpDetector(0.5)
	ctx := NewConversationContext(10)
	ctx.AddAssistantTurn("Go is a programming language")

	car := NewContextAwareRetriever(rag, rewriter, detector, ctx)

	result, err := car.Search(context.Background(), "tell me more about it")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !result.IsFollowUp {
		t.Error("expected follow-up detection")
	}
}

func TestContextAwareRetrieverSearchWithResponse(t *testing.T) {
	embedder := newMockEmbedder(64)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	_, err := rag.IndexText("test", "Go Programming", "Go is a statically typed compiled language")
	if err != nil {
		t.Fatalf("IndexText: %v", err)
	}

	rewriter := NewQueryRewriter(RewriteNone)
	detector := NewFollowUpDetector(0.5)
	ctx := NewConversationContext(10)

	car := NewContextAwareRetriever(rag, rewriter, detector, ctx)

	_, err = car.SearchWithResponse(context.Background(), "Go language", "Go is great!")
	if err != nil {
		t.Fatalf("SearchWithResponse: %v", err)
	}

	// Check that assistant response was recorded
	if ctx.Len() != 2 { // user + assistant
		t.Errorf("expected 2 turns, got %d", ctx.Len())
	}
}

func TestContextAwareRetrieverConversationContext(t *testing.T) {
	embedder := newMockEmbedder(64)
	rag := NewRAGManager(embedder, DefaultRAGConfig())
	rewriter := NewQueryRewriter(RewriteNone)
	detector := NewFollowUpDetector(0.5)
	ctx := NewConversationContext(10)

	car := NewContextAwareRetriever(rag, rewriter, detector, ctx)
	if car.ConversationContext() != ctx {
		t.Error("expected same conversation context")
	}
}

// --- sortResultsByScore test ---

func TestSortResultsByScore(t *testing.T) {
	results := []RetrievalResult{
		{ChunkID: "a", Score: 0.5},
		{ChunkID: "b", Score: 0.9},
		{ChunkID: "c", Score: 0.7},
	}
	sortResultsByScore(results)
	if results[0].Score != 0.9 {
		t.Errorf("expected highest score first, got %f", results[0].Score)
	}
}

// --- isStopWord test ---

func TestIsStopWord(t *testing.T) {
	if !isStopWord("the") {
		t.Error("expected 'the' to be a stop word")
	}
	if isStopWord("golang") {
		t.Error("expected 'golang' to not be a stop word")
	}
	if !isStopWord("的") {
		t.Error("expected '的' to be a stop word")
	}
}
