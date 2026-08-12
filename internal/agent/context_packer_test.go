package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

func TestBuildTypedMemoryBodyIncludesTypedSections(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096)}
	route := memory.RouteAnalysis{
		RequiredTools:     []string{"current_time", "web_search"},
		SuggestedSearches: []string{"Shanghai pollen forecast"},
		RiskFlags:         []string{"pollen_allergy"},
		Constraints:       []string{"Check air quality before final answer."},
		TemporalNotes:     []string{"Superseded memory ignored: old-ref."},
		SupersededRefs:    []string{"old-ref"},
		EvidenceRefs:      []string{"memory.md#block"},
	}
	entries := []memory.Entry{
		{Content: "User's daughter has active pollen allergy.", Category: "health", Tier: memory.TierLong},
	}

	body := planner.buildTypedMemoryBody(route, entries, 2048)
	for _, want := range []string{
		"[Memory Router]",
		"[Must Use Facts]",
		"[Required Tools]",
		"[Answer Constraints]",
		"[Temporal Warnings]",
		"[Evidence Refs]",
		"[Suggested web_search queries]",
		"current_time",
		"web_search",
		"Check air quality",
		"Temporal resolution:",
		"Superseded refs:",
		"User's daughter has active pollen allergy",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected typed memory body to contain %q, got:\n%s", want, body)
		}
	}
}

func TestSelectIntentAwareRecentHistoryDropsUnrelatedFiller(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096)}
	messages := []provider.Message{
		{Role: "user", Content: "recipe filler user chatter"},
		{Role: "assistant", Content: "recipe filler assistant chatter with no benchmark relevance"},
		{Role: "user", Content: "Context packer benchmark acceptance gates: CMR >= 0.95 and P95PackerMS <= 10."},
		{Role: "assistant", Content: "Keep Quality >= baseline and track context noise."},
		{Role: "tool", Name: "terminal", Content: "go test ./cmd/lh-context-packer-bench ./internal/agent"},
		{Role: "assistant", Content: "Next summarize prompt tokens and bucket tokens."},
	}

	terms := historyIntentTerms("context-packer-bench long history", messages)
	selected := planner.selectIntentAwareRecentHistory(messages, terms)
	text := strings.ToLower(messagesToTestText(selected))
	if strings.Contains(text, "recipe filler") {
		t.Fatalf("expected unrelated filler to be dropped, got:\n%s", text)
	}
	for _, want := range []string{"cmr", "quality", "go test", "bucket tokens"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected selected history to contain %q, got:\n%s", want, text)
		}
	}
}

func TestHistoryMessageRelevantHonorsExplicitIrrelevance(t *testing.T) {
	msg := provider.Message{
		Role:    "assistant",
		Content: "recipe filler assistant chatter with no benchmark relevance",
	}
	if historyMessageRelevant(msg, []string{"benchmark", "quality"}) {
		t.Fatalf("expected explicit irrelevant history to be dropped")
	}
}

func TestSelectIntentAwareRecentHistoryDropsExplicitlyIrrelevantTail(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096)}
	messages := []provider.Message{
		{Role: "user", Content: "Context Packer trace replay acceptance: strict clean=true and CMR >= 0.95."},
		{Role: "assistant", Content: "Evidence file should be docs/reports/context-packer-hardcases-v4-trace-capable-20260607.jsonl."},
		{Role: "user", Content: "travel filler unrelated note"},
		{Role: "assistant", Content: "travel filler unrelated response with no current task relevance"},
	}

	terms := historyIntentTerms("Context Packer trace replay", messages)
	selected := planner.selectIntentAwareRecentHistory(messages, terms)
	text := strings.ToLower(messagesToTestText(selected))
	if strings.Contains(text, "travel filler") {
		t.Fatalf("expected explicitly irrelevant tail to be dropped, got:\n%s", text)
	}
	for _, want := range []string{"strict clean=true", "context-packer-hardcases"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected selected history to contain %q, got:\n%s", want, text)
		}
	}
}

func TestSelectIntentAwareRecentHistoryPreservesLatestUserTurn(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096)}
	messages := []provider.Message{
		{Role: "user", Content: "old memory topic about seven-second novel"},
		{Role: "assistant", Content: "old answer about seven-second novel"},
		{Role: "tool", Name: "recall", Content: "Memory recall. Query: seven-second novel"},
		{Role: "assistant", Content: "retrieved unrelated prior task"},
		{Role: "user", Content: "中文输出。行啊，开始工作吧，用刚才的大纲输出正文"},
		{Role: "assistant", Content: "我会继续当前九拍双线大纲"},
	}

	terms := []string{"seven-second", "novel", "memory"}
	selected := planner.selectIntentAwareRecentHistory(messages, terms)
	text := messagesToTestText(selected)
	if !strings.Contains(text, "用刚才的大纲输出正文") {
		t.Fatalf("expected latest user instruction to be preserved, got:\n%s", text)
	}
	if !strings.Contains(text, "继续当前九拍双线大纲") {
		t.Fatalf("expected assistant response after latest user turn to be preserved, got:\n%s", text)
	}
}

func TestBuildHistoryMessagesRespectsCompactBoundary(t *testing.T) {
	sess := session.NewSession("compact-history", t.TempDir())
	sess.AddMessage("user", "old raw secret before compact")
	sess.AddMessage("assistant", "old raw assistant before compact")
	sess.AddCompactBoundary(session.CompactMetadata{
		ID:      "compact-test",
		Trigger: "manual",
		Summary: "compact summary says prior task touched internal/agent/context_planner.go",
		Attachments: []session.CompactAttachment{
			{Kind: "file_state", Source: "recent_history", Content: "internal/agent/context_planner.go"},
			{Kind: "tool_result", Source: "terminal", Content: "go test ./internal/agent passed"},
		},
	})
	sess.AddMessage("user", "new user request after compact")
	sess.AddMessage("assistant", "new assistant response after compact")

	planner := &contextPlanner{
		est: contextx.NewTokenEstimator(4096),
		budget: contextBudget{
			History: 1200,
			Memory:  400,
		},
		options: contextBuildOptions{
			IncludeHistory: true,
			HistoryRecent:  6,
			HistoryMiddle:  12,
		},
	}
	messages := planner.buildHistoryMessages(sess, "continue compact task")
	text := messagesToTestText(messages)
	if strings.Contains(text, "old raw secret") || strings.Contains(text, "old raw assistant") {
		t.Fatalf("expected raw pre-compact history to be dropped, got:\n%s", text)
	}
	for _, want := range []string{"[Compact Summary]", "[Post-Compact Restore]", "context_planner.go", "go test ./internal/agent passed", "new user request", "new assistant response"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in compacted history, got:\n%s", want, text)
		}
	}
}

func TestBuildHistoryMessagesKeepsRawHistoryStableWithoutPerTurnSummary(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	counting := &countingProvider{}
	planner := &contextPlanner{
		agent: &Agent{
			cfg:      cfg,
			provider: counting,
		},
		est: contextx.NewTokenEstimator(4096),
		budget: contextBudget{
			History: 1200,
			Memory:  400,
		},
		options: contextBuildOptions{
			IncludeHistory: true,
			HistoryRecent:  2,
			HistoryMiddle:  2,
		},
	}

	sess := session.NewSession("history-local-summary", t.TempDir())
	for i := 0; i < 10; i++ {
		sess.AddMessage("user", "hydrology forecast calibration task message")
		sess.AddMessage("assistant", "hydrology forecast calibration progress")
	}

	messages := planner.buildHistoryMessages(sess, "hydrology forecast")
	if counting.chatCalls != 0 {
		t.Fatalf("context history compression must not call provider, got %d calls", counting.chatCalls)
	}
	text := messagesToTestText(messages)
	if strings.Contains(text, "[Conversation Summary]") || strings.Contains(text, "[Conversation Themes]") {
		t.Fatalf("per-turn history summaries must not be generated, got:\n%s", text)
	}
	if !strings.Contains(text, "hydrology forecast calibration task message") {
		t.Fatalf("expected stable raw history, got:\n%s", text)
	}
}

func TestBuildHistoryMessagesAppendsImmutableSummarySegments(t *testing.T) {
	sess := session.NewSession("compact-segment-history", t.TempDir())
	sess.AddMessage("user", "first old request")
	sess.AddMessage("assistant", "first old answer")
	sess.AddCompactBoundary(session.CompactMetadata{ID: "segment-1", Summary: "first immutable summary", FromMessage: 0, ToMessage: 2})
	sess.AddMessage("user", "second old request")
	sess.AddMessage("assistant", "second old answer")
	sess.AddCompactBoundary(session.CompactMetadata{ID: "segment-2", Summary: "second immutable summary", FromMessage: 2, ToMessage: 4})
	sess.AddMessage("user", "current raw tail")

	planner := &contextPlanner{
		est:    contextx.NewTokenEstimator(4096),
		budget: contextBudget{History: 2400, Memory: 400},
	}
	messages := planner.buildHistoryMessages(sess, "current query")
	text := messagesToTestText(messages)
	first := strings.Index(text, "first immutable summary")
	second := strings.Index(text, "second immutable summary")
	tail := strings.Index(text, "current raw tail")
	if first < 0 || second <= first || tail <= second {
		t.Fatalf("expected ordered immutable summaries followed by raw tail, got:\n%s", text)
	}
	if strings.Contains(text, "first old request") || strings.Contains(text, "second old request") {
		t.Fatalf("covered raw history leaked into context:\n%s", text)
	}
}

func TestBuildHistoryMessagesPreservesPrefixWhenRawTurnAppends(t *testing.T) {
	sess := session.NewSession("stable-history-prefix", t.TempDir())
	sess.AddMessage("user", "old request")
	sess.AddMessage("assistant", "old answer")
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096), budget: contextBudget{History: 2400}}

	before := planner.buildHistoryMessages(sess, "query one")
	sess.AddMessage("user", "new request")
	after := planner.buildHistoryMessages(sess, "unrelated query two")
	if len(after) <= len(before) {
		t.Fatalf("expected appended history, before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if !reflect.DeepEqual(before[i], after[i]) {
			t.Fatalf("history prefix changed at %d: before=%+v after=%+v", i, before[i], after[i])
		}
	}
}

func TestBuildHistoryMessagesRedactsHistoricalMediaPaths(t *testing.T) {
	sess := session.NewSession("stale-media-history", t.TempDir())
	sess.AddMessage("user", "打开浏览器")
	sess.AddMessage("assistant", "浏览器已打开。\n\nMEDIA:/tmp/screenshot_1738216169.png")

	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096), budget: contextBudget{History: 2400}}
	messages := planner.buildHistoryMessages(sess, "成都今天的天气")
	text := messagesToTestText(messages)
	if strings.Contains(text, "MEDIA:/tmp/screenshot_1738216169.png") {
		t.Fatalf("stale historical MEDIA path leaked into context: %s", text)
	}
}

func TestContextPlannerPlacesDynamicMemoryAfterStableSessionHistory(t *testing.T) {
	a := newTestAgentWithMemory(t)
	if err := a.memory.SaveWithTier("cache-layout-memory-marker", "project", memory.TierLong, 0.9); err != nil {
		t.Fatalf("save memory marker: %v", err)
	}
	sess := newTestSession(t)
	sess.AddMessage("user", "cache-layout-history-marker")
	sess.AddMessage("assistant", "prior stable answer")

	messages := newContextPlanner(a, defaultContextBuildOptions()).Build(context.Background(), sess, "cache-layout-memory-marker")
	historyIndex, memoryIndex, currentIndex := -1, -1, -1
	for i, msg := range messages {
		if strings.Contains(msg.Content, "cache-layout-history-marker") {
			historyIndex = i
		}
		if msg.Role == "system" && strings.Contains(msg.Content, "cache-layout-memory-marker") {
			memoryIndex = i
		}
		if msg.Role == "user" && msg.Content == "cache-layout-memory-marker" {
			currentIndex = i
		}
	}
	if historyIndex < 0 || memoryIndex <= historyIndex || currentIndex <= memoryIndex {
		t.Fatalf("expected history -> dynamic memory -> current user, indexes history=%d memory=%d user=%d messages=%+v", historyIndex, memoryIndex, currentIndex, messages)
	}
}

func TestRewriteRAGFollowUpUsesPreviousUserTopic(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess := session.NewSession("rag-follow-up", t.TempDir())
	sess.AddMessage("user", "解释 LuckyAgent 的 SQLite 向量索引事务")
	sess.AddMessage("assistant", "它使用文档、分块和向量表。")
	planner := &contextPlanner{agent: &Agent{cfg: cfg}}

	got := planner.rewriteRAGFollowUp(sess, "这个为什么需要原子替换？")
	if !strings.Contains(got, "SQLite 向量索引事务") || !strings.Contains(got, "Follow-up:") {
		t.Fatalf("follow-up query was not grounded in session topic: %q", got)
	}
	standalone := planner.rewriteRAGFollowUp(sess, "Go channel 实现")
	if standalone != "Go channel 实现" {
		t.Fatalf("standalone query should not be rewritten: %q", standalone)
	}
}

func TestRewriteRAGFollowUpRecognizesUTF8Chinese(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess := session.NewSession("rag-follow-up-utf8", t.TempDir())
	sess.AddMessage("user", "解释 LuckyAgent 的 SQLite 混合检索实现")
	sess.AddMessage("assistant", "它使用向量和词法分数混合排序。")
	planner := &contextPlanner{agent: &Agent{cfg: cfg}}

	got := planner.rewriteRAGFollowUp(sess, "这个为什么需要原子替换？")
	if !strings.Contains(got, "SQLite 混合检索实现") || !strings.Contains(got, "Follow-up:") {
		t.Fatalf("UTF-8 Chinese follow-up was not grounded in session topic: %q", got)
	}
}

type countingProvider struct {
	chatCalls int
}

func (p *countingProvider) Name() string { return "counting" }

func (p *countingProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	p.chatCalls++
	return &provider.Response{Content: "provider summary should not be used"}, nil
}

func (p *countingProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func (p *countingProvider) Validate() error { return nil }

func messagesToTestText(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}
