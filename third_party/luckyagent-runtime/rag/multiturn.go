package rag

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ===== v0.16.0: Multi-turn RAG =====

// ConversationTurn represents a single turn in a conversation.
type ConversationTurn struct {
	Role      string    // "user" or "assistant"
	Content   string    // the message content
	Timestamp time.Time // when the turn occurred
	Query     string    // original query (for user turns, may differ from Content after rewriting)
}

// ConversationContext tracks conversation history for retrieval optimization.
type ConversationContext struct {
	mu    sync.RWMutex
	turns []ConversationTurn
	limit int // max turns to keep
}

// NewConversationContext creates a new conversation context tracker.
func NewConversationContext(limit int) *ConversationContext {
	if limit <= 0 {
		limit = 20
	}
	return &ConversationContext{
		turns: make([]ConversationTurn, 0, limit),
		limit: limit,
	}
}

// AddTurn adds a conversation turn.
func (cc *ConversationContext) AddTurn(role, content string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.turns = append(cc.turns, ConversationTurn{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	})

	// Evict oldest turns if over limit
	if len(cc.turns) > cc.limit {
		cc.turns = cc.turns[len(cc.turns)-cc.limit:]
	}
}

// AddUserTurn adds a user turn.
func (cc *ConversationContext) AddUserTurn(content string) {
	cc.AddTurn("user", content)
}

// AddAssistantTurn adds an assistant turn.
func (cc *ConversationContext) AddAssistantTurn(content string) {
	cc.AddTurn("assistant", content)
}

// Turns returns a copy of the conversation turns.
func (cc *ConversationContext) Turns() []ConversationTurn {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	out := make([]ConversationTurn, len(cc.turns))
	copy(out, cc.turns)
	return out
}

// LastTurn returns the most recent turn, or nil if empty.
func (cc *ConversationContext) LastTurn() *ConversationTurn {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	if len(cc.turns) == 0 {
		return nil
	}
	t := cc.turns[len(cc.turns)-1]
	return &t
}

// LastUserTurn returns the most recent user turn, or nil.
func (cc *ConversationContext) LastUserTurn() *ConversationTurn {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	for i := len(cc.turns) - 1; i >= 0; i-- {
		if cc.turns[i].Role == "user" {
			t := cc.turns[i]
			return &t
		}
	}
	return nil
}

// RecentTurns returns the last n turns.
func (cc *ConversationContext) RecentTurns(n int) []ConversationTurn {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	if n >= len(cc.turns) {
		out := make([]ConversationTurn, len(cc.turns))
		copy(out, cc.turns)
		return out
	}
	out := make([]ConversationTurn, n)
	copy(out, cc.turns[len(cc.turns)-n:])
	return out
}

// Clear removes all turns.
func (cc *ConversationContext) Clear() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.turns = cc.turns[:0]
}

// Len returns the number of turns.
func (cc *ConversationContext) Len() int {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return len(cc.turns)
}

// Summary returns a brief summary of the conversation for context.
func (cc *ConversationContext) Summary() string {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	if len(cc.turns) == 0 {
		return "(empty conversation)"
	}

	var sb strings.Builder
	for i, t := range cc.turns {
		if i > 0 {
			sb.WriteString(" | ")
		}
		content := t.Content
		if len(content) > 80 {
			content = content[:77] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s: %s", t.Role, content))
	}
	return sb.String()
}

// --- MR-2: QueryRewriter ---

// RewriteStrategy defines how a query should be rewritten.
type RewriteStrategy string

const (
	RewriteNone      RewriteStrategy = "none"      // no rewriting
	RewriteExpand    RewriteStrategy = "expand"    // expand with context
	RewriteDecompose RewriteStrategy = "decompose" // break into sub-queries
	RewriteClarify   RewriteStrategy = "clarify"   // add clarification terms
)

// QueryRewriter rewrites user queries based on conversation context.
type QueryRewriter struct {
	strategy RewriteStrategy
}

// NewQueryRewriter creates a new query rewriter.
func NewQueryRewriter(strategy RewriteStrategy) *QueryRewriter {
	return &QueryRewriter{strategy: strategy}
}

// Rewrite rewrites a query using the conversation context.
func (qr *QueryRewriter) Rewrite(query string, ctx *ConversationContext) ([]string, error) {
	switch qr.strategy {
	case RewriteNone:
		return []string{query}, nil
	case RewriteExpand:
		return qr.expandQuery(query, ctx)
	case RewriteDecompose:
		return qr.decomposeQuery(query, ctx)
	case RewriteClarify:
		return qr.clarifyQuery(query, ctx)
	default:
		return []string{query}, nil
	}
}

// expandQuery expands the query with context from recent turns.
func (qr *QueryRewriter) expandQuery(query string, ctx *ConversationContext) ([]string, error) {
	if ctx == nil || ctx.Len() == 0 {
		return []string{query}, nil
	}

	// Extract key terms from recent user turns
	terms := qr.extractContextTerms(ctx)
	if len(terms) == 0 {
		return []string{query}, nil
	}

	// Combine original query with context terms
	expanded := query
	for _, term := range terms {
		if !strings.Contains(strings.ToLower(query), strings.ToLower(term)) {
			expanded += " " + term
		}
	}

	return []string{expanded}, nil
}

// decomposeQuery breaks a complex query into sub-queries.
func (qr *QueryRewriter) decomposeQuery(query string, ctx *ConversationContext) ([]string, error) {
	// Simple decomposition: split on conjunctions and question patterns
	queries := []string{query}

	// Split on common conjunctions
	conjunctions := []string{" and ", " or ", "，以及", "，或者", "，还有"}
	for _, conj := range conjunctions {
		parts := strings.Split(query, conj)
		if len(parts) > 1 {
			queries = parts
			break
		}
	}

	// If we have context, add a context-aware sub-query
	if ctx != nil && ctx.Len() > 0 {
		lastUser := ctx.LastUserTurn()
		if lastUser != nil && lastUser.Content != query {
			queries = append(queries, lastUser.Content+" "+query)
		}
	}

	return queries, nil
}

// clarifyQuery adds clarification terms based on conversation context.
func (qr *QueryRewriter) clarifyQuery(query string, ctx *ConversationContext) ([]string, error) {
	if ctx == nil || ctx.Len() == 0 {
		return []string{query}, nil
	}

	// Look for pronouns and references that need clarification
	replacements := map[string]string{
		"it": "", "they": "", "them": "", "this": "", "that": "",
		"这些": "", "那个": "", "这个": "", "它": "", "它们": "",
	}

	clarified := query
	lastUser := ctx.LastUserTurn()
	if lastUser != nil {
		for pronoun := range replacements {
			if strings.Contains(strings.ToLower(query), pronoun) {
				// Extract noun phrases from previous turn
				terms := qr.extractContextTerms(ctx)
				if len(terms) > 0 {
					clarified = strings.ReplaceAll(clarified, pronoun, terms[0])
				}
			}
		}
	}

	return []string{clarified}, nil
}

// extractContextTerms extracts key terms from conversation context.
func (qr *QueryRewriter) extractContextTerms(ctx *ConversationContext) []string {
	turns := ctx.RecentTurns(4) // last 4 turns
	var terms []string

	for _, t := range turns {
		words := strings.Fields(t.Content)
		for _, w := range words {
			w = strings.Trim(w, ".,!?;:，。！？；：")
			if len(w) > 2 && len(w) < 30 {
				// Skip common stop words
				if !isStopWord(w) {
					terms = append(terms, w)
				}
			}
		}
	}

	// Deduplicate
	seen := make(map[string]bool)
	var unique []string
	for _, t := range terms {
		lower := strings.ToLower(t)
		if !seen[lower] {
			seen[lower] = true
			unique = append(unique, t)
		}
	}

	// Limit to top 5 terms
	if len(unique) > 5 {
		unique = unique[:5]
	}

	return unique
}

// isStopWord checks if a word is a common stop word.
func isStopWord(word string) bool {
	stops := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true,
		"have": true, "has": true, "had": true, "do": true,
		"does": true, "did": true, "will": true, "would": true,
		"could": true, "should": true, "may": true, "might": true,
		"can": true, "shall": true, "must": true, "need": true,
		"this": true, "that": true, "these": true, "those": true,
		"what": true, "which": true, "who": true, "whom": true,
		"how": true, "when": true, "where": true, "why": true,
		"not": true, "no": true, "but": true, "and": true,
		"or": true, "if": true, "then": true, "so": true,
		"的": true, "了": true, "是": true, "在": true,
		"有": true, "和": true, "与": true, "或": true,
		"不": true, "也": true, "都": true, "就": true,
		"要": true, "会": true, "能": true, "可以": true,
	}
	return stops[strings.ToLower(word)]
}

// Strategy returns the current rewrite strategy.
func (qr *QueryRewriter) Strategy() RewriteStrategy {
	return qr.strategy
}

// SetStrategy updates the rewrite strategy.
func (qr *QueryRewriter) SetStrategy(s RewriteStrategy) {
	qr.strategy = s
}

// --- MR-3: FollowUpDetector ---

// FollowUpType classifies the type of follow-up.
type FollowUpType string

const (
	FollowUpNone      FollowUpType = "none"      // not a follow-up
	FollowUpClarify   FollowUpType = "clarify"   // asking for clarification
	FollowUpDeepen    FollowUpType = "deepen"    // going deeper on same topic
	FollowUpPivot     FollowUpType = "pivot"     // changing topic
	FollowUpCompare   FollowUpType = "compare"   // comparing with previous results
	FollowUpElaborate FollowUpType = "elaborate" // asking for more detail
)

// FollowUpResult is the result of follow-up detection.
type FollowUpResult struct {
	Type       FollowUpType
	Confidence float64 // 0.0 - 1.0
	IsFollowUp bool
	Reason     string
}

// FollowUpDetector detects whether a query is a follow-up to previous conversation.
type FollowUpDetector struct {
	threshold float64 // confidence threshold for follow-up detection
}

// NewFollowUpDetector creates a new follow-up detector.
func NewFollowUpDetector(threshold float64) *FollowUpDetector {
	if threshold <= 0 {
		threshold = 0.5
	}
	return &FollowUpDetector{threshold: threshold}
}

// Detect analyzes whether the query is a follow-up.
func (fd *FollowUpDetector) Detect(query string, ctx *ConversationContext) FollowUpResult {
	if ctx == nil || ctx.Len() == 0 {
		return FollowUpResult{Type: FollowUpNone, Confidence: 0, IsFollowUp: false}
	}

	lastTurn := ctx.LastTurn()
	if lastTurn == nil {
		return FollowUpResult{Type: FollowUpNone, Confidence: 0, IsFollowUp: false}
	}

	// Check for explicit follow-up indicators
	clarifyIndicators := []string{
		"what do you mean", "can you explain", "I don't understand",
		"clarify", "what is that", "tell me more about",
		"什么意思", "解释一下", "详细说说", "不太明白", "能再说一遍吗",
	}
	deepenIndicators := []string{
		"more about", "tell me more", "go deeper", "elaborate",
		"更多关于", "深入", "展开", "继续说",
	}
	compareIndicators := []string{
		"compare", "difference", "versus", "vs", "how does it compare",
		"比较", "区别", "对比", "不同",
	}
	elaborateIndicators := []string{
		"example", "show me", "like what", "such as",
		"举例", "比如", "例如", "举个例子",
	}

	lowerQuery := strings.ToLower(query)

	// Check deepen (before clarify, since "tell me more" overlaps)
	for _, ind := range deepenIndicators {
		if strings.Contains(lowerQuery, ind) {
			return FollowUpResult{
				Type:       FollowUpDeepen,
				Confidence: 0.85,
				IsFollowUp: true,
				Reason:     "contains deepening indicator: " + ind,
			}
		}
	}

	// Check clarify
	for _, ind := range clarifyIndicators {
		if strings.Contains(lowerQuery, ind) {
			return FollowUpResult{
				Type:       FollowUpClarify,
				Confidence: 0.9,
				IsFollowUp: true,
				Reason:     "contains clarification indicator: " + ind,
			}
		}
	}

	// Check compare
	for _, ind := range compareIndicators {
		if strings.Contains(lowerQuery, ind) {
			return FollowUpResult{
				Type:       FollowUpCompare,
				Confidence: 0.85,
				IsFollowUp: true,
				Reason:     "contains comparison indicator: " + ind,
			}
		}
	}

	// Check elaborate
	for _, ind := range elaborateIndicators {
		if strings.Contains(lowerQuery, ind) {
			return FollowUpResult{
				Type:       FollowUpElaborate,
				Confidence: 0.8,
				IsFollowUp: true,
				Reason:     "contains elaboration indicator: " + ind,
			}
		}
	}

	// Check for pronoun references (implicit follow-up)
	pronouns := []string{"it", "they", "them", "this", "that", "这些", "那个", "这个", "它", "它们"}
	for _, p := range pronouns {
		if strings.Contains(lowerQuery, p) {
			return FollowUpResult{
				Type:       FollowUpDeepen,
				Confidence: 0.7,
				IsFollowUp: true,
				Reason:     "contains pronoun reference: " + p,
			}
		}
	}

	// Check for short queries (likely follow-up)
	if len(strings.Fields(query)) <= 3 && ctx.Len() > 0 {
		return FollowUpResult{
			Type:       FollowUpDeepen,
			Confidence: 0.6,
			IsFollowUp: true,
			Reason:     "short query likely referencing previous context",
		}
	}

	// Check topic overlap with previous turn
	if lastTurn.Role == "assistant" {
		overlap := fd.computeTopicOverlap(query, lastTurn.Content)
		if overlap > fd.threshold {
			return FollowUpResult{
				Type:       FollowUpDeepen,
				Confidence: overlap,
				IsFollowUp: true,
				Reason:     fmt.Sprintf("topic overlap %.2f with previous response", overlap),
			}
		}
	}

	return FollowUpResult{Type: FollowUpNone, Confidence: 0, IsFollowUp: false}
}

// computeTopicOverlap computes a simple word overlap score between two texts.
func (fd *FollowUpDetector) computeTopicOverlap(a, b string) float64 {
	wordsA := make(map[string]bool)
	for _, w := range strings.Fields(strings.ToLower(a)) {
		w = strings.Trim(w, ".,!?;:，。！？；：")
		if len(w) > 2 && !isStopWord(w) {
			wordsA[w] = true
		}
	}

	if len(wordsA) == 0 {
		return 0
	}

	overlap := 0
	for _, w := range strings.Fields(strings.ToLower(b)) {
		w = strings.Trim(w, ".,!?;:，。！？；：")
		if wordsA[w] {
			overlap++
		}
	}

	return float64(overlap) / float64(len(wordsA))
}

// Threshold returns the current confidence threshold.
func (fd *FollowUpDetector) Threshold() float64 {
	return fd.threshold
}

// --- MR-4: ContextAwareRetriever ---

// ContextAwareRetriever combines conversation context with RAG retrieval.
type ContextAwareRetriever struct {
	rag      *RAGManager
	rewriter *QueryRewriter
	detector *FollowUpDetector
	ctx      *ConversationContext
}

// NewContextAwareRetriever creates a new context-aware retriever.
func NewContextAwareRetriever(rag *RAGManager, rewriter *QueryRewriter, detector *FollowUpDetector, ctx *ConversationContext) *ContextAwareRetriever {
	return &ContextAwareRetriever{
		rag:      rag,
		rewriter: rewriter,
		detector: detector,
		ctx:      ctx,
	}
}

// MultiTurnResult is the result of a multi-turn retrieval.
type MultiTurnResult struct {
	Query          string            // original query
	RewrittenQuery []string          // rewritten queries (may be multiple for decomposition)
	IsFollowUp     bool              // whether this was detected as a follow-up
	FollowUpType   FollowUpType      // type of follow-up
	Results        []RetrievalResult // combined results from all queries
	Context        string            // assembled context string
}

// Search performs a context-aware search.
func (car *ContextAwareRetriever) Search(ctx context.Context, query string) (*MultiTurnResult, error) {
	// Detect follow-up
	fuResult := car.detector.Detect(query, car.ctx)

	// Rewrite query
	rewritten, err := car.rewriter.Rewrite(query, car.ctx)
	if err != nil {
		return nil, fmt.Errorf("rewrite query: %w", err)
	}

	// Search with each rewritten query
	var allResults []RetrievalResult
	seen := make(map[string]bool) // deduplicate by chunk ID

	for _, q := range rewritten {
		results, err := car.rag.Search(ctx, q)
		if err != nil {
			continue // skip failed sub-queries
		}
		for _, r := range results {
			if !seen[r.ChunkID] {
				seen[r.ChunkID] = true
				allResults = append(allResults, r)
			}
		}
	}

	// Sort by score
	sortResultsByScore(allResults)

	// Build context
	context := car.rag.Retriever().BuildContext(allResults)

	// Record this turn
	car.ctx.AddUserTurn(query)

	return &MultiTurnResult{
		Query:          query,
		RewrittenQuery: rewritten,
		IsFollowUp:     fuResult.IsFollowUp,
		FollowUpType:   fuResult.Type,
		Results:        allResults,
		Context:        context,
	}, nil
}

// SearchWithResponse performs a search and records the assistant response.
func (car *ContextAwareRetriever) SearchWithResponse(ctx context.Context, query, response string) (*MultiTurnResult, error) {
	result, err := car.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	// Record assistant response
	car.ctx.AddAssistantTurn(response)

	return result, nil
}

// ConversationContext returns the underlying conversation context.
func (car *ContextAwareRetriever) ConversationContext() *ConversationContext {
	return car.ctx
}

// --- MR-5: RAG Feedback Loop ---

// FeedbackType represents the type of feedback on retrieval results.
type FeedbackType string

const (
	FeedbackPositive FeedbackType = "positive" // results were helpful
	FeedbackNegative FeedbackType = "negative" // results were not helpful
	FeedbackPartial  FeedbackType = "partial"  // some results were helpful
)

// RetrievalFeedback records feedback on retrieval results.
type RetrievalFeedback struct {
	Query     string
	Results   []RetrievalResult
	Feedback  FeedbackType
	Timestamp time.Time
	Notes     string
}

// FeedbackStore stores retrieval feedback for learning.
type FeedbackStore struct {
	mu       sync.RWMutex
	feedback []RetrievalFeedback
	limit    int
	stats    FeedbackStats
}

// FeedbackStats tracks aggregate feedback statistics.
type FeedbackStats struct {
	TotalQueries   int
	PositiveCount  int
	NegativeCount  int
	PartialCount   int
	AvgResultCount float64
}

// NewFeedbackStore creates a new feedback store.
func NewFeedbackStore(limit int) *FeedbackStore {
	if limit <= 0 {
		limit = 1000
	}
	return &FeedbackStore{
		feedback: make([]RetrievalFeedback, 0, limit),
		limit:    limit,
	}
}

// Record records feedback on a retrieval result.
func (fs *FeedbackStore) Record(query string, results []RetrievalResult, feedback FeedbackType, notes string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.feedback = append(fs.feedback, RetrievalFeedback{
		Query:     query,
		Results:   results,
		Feedback:  feedback,
		Timestamp: time.Now(),
		Notes:     notes,
	})

	// Update stats
	fs.stats.TotalQueries++
	fs.stats.AvgResultCount = float64(fs.stats.TotalQueries-1)*fs.stats.AvgResultCount/float64(fs.stats.TotalQueries) + float64(len(results))/float64(fs.stats.TotalQueries)
	switch feedback {
	case FeedbackPositive:
		fs.stats.PositiveCount++
	case FeedbackNegative:
		fs.stats.NegativeCount++
	case FeedbackPartial:
		fs.stats.PartialCount++
	}

	// Evict oldest if over limit
	if len(fs.feedback) > fs.limit {
		fs.feedback = fs.feedback[len(fs.feedback)-fs.limit:]
	}
}

// Stats returns aggregate feedback statistics.
func (fs *FeedbackStore) Stats() FeedbackStats {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.stats
}

// RecentFeedback returns the most recent n feedback entries.
func (fs *FeedbackStore) RecentFeedback(n int) []RetrievalFeedback {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	if n >= len(fs.feedback) {
		out := make([]RetrievalFeedback, len(fs.feedback))
		copy(out, fs.feedback)
		return out
	}
	out := make([]RetrievalFeedback, n)
	copy(out, fs.feedback[len(fs.feedback)-n:])
	return out
}

// PositiveRate returns the ratio of positive feedback.
func (fs *FeedbackStore) PositiveRate() float64 {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	if fs.stats.TotalQueries == 0 {
		return 0
	}
	return float64(fs.stats.PositiveCount) / float64(fs.stats.TotalQueries)
}

// ShouldAdjustStrategy determines if the retrieval strategy should be adjusted
// based on recent feedback patterns.
func (fs *FeedbackStore) ShouldAdjustStrategy() (bool, RewriteStrategy) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	if fs.stats.TotalQueries < 5 {
		return false, RewriteNone
	}

	negRate := float64(fs.stats.NegativeCount) / float64(fs.stats.TotalQueries)
	posRate := float64(fs.stats.PositiveCount) / float64(fs.stats.TotalQueries)

	// If negative rate is high, suggest decomposition for better coverage
	if negRate > 0.5 {
		return true, RewriteDecompose
	}

	// If positive rate is high, current strategy works
	if posRate > 0.7 {
		return false, RewriteNone
	}

	// If partial rate is high, suggest expansion
	partialRate := float64(fs.stats.PartialCount) / float64(fs.stats.TotalQueries)
	if partialRate > 0.3 {
		return true, RewriteExpand
	}

	return false, RewriteNone
}

// Clear removes all feedback.
func (fs *FeedbackStore) Clear() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.feedback = fs.feedback[:0]
	fs.stats = FeedbackStats{}
}

// Len returns the number of feedback entries.
func (fs *FeedbackStore) Len() int {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return len(fs.feedback)
}

// --- Helper ---

// sortResultsByScore sorts retrieval results by score descending.
func sortResultsByScore(results []RetrievalResult) {
	sortSlice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}

// sortSlice is a simple in-place sort for slices.
func sortSlice[T any](s []T, less func(i, j int) bool) {
	// Simple insertion sort for small slices
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
