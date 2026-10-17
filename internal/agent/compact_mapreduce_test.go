package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

type countingCompactProvider struct {
	name     string
	calls    atomic.Int32
	maxCalls int32
	mu       sync.Mutex
	prompts  []string
}

func (p *countingCompactProvider) Name() string { return p.name }
func (p *countingCompactProvider) Validate() error {
	return nil
}
func (p *countingCompactProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("unexpected stream")
}
func (p *countingCompactProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	n := p.calls.Add(1)
	if p.maxCalls > 0 && n > p.maxCalls {
		return nil, fmt.Errorf("too many chat calls: %d", n)
	}
	var user string
	for _, m := range messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	p.mu.Lock()
	p.prompts = append(p.prompts, user)
	p.mu.Unlock()

	// Detect merge vs segment by prompt wording.
	body := validCompactSummaryText()
	if strings.Contains(user, "Merge the segment summaries") {
		body = strings.Replace(body, "Continue implementing", "Merged continue implementing", 1)
	}
	return &provider.Response{Content: body}, nil
}

func validCompactSummaryText() string {
	return strings.Join([]string{
		"Current user goal:",
		"Continue implementing LuckyAgent compact map-reduce for internal/agent/context_planner.go.",
		"Completed work:",
		"Sliced long history and verified go test ./internal/agent for compact chunks.",
		"Pending work:",
		"Keep retained turns outside the compacted range.",
		"Key files and functions:",
		"internal/agent/compact_mapreduce.go, internal/agent/agent.go",
		"Commands and test results:",
		"go test ./internal/agent passed for map-reduce compact.",
		"User constraints:",
		"Commit small chunks after each phase.",
		"Uncertain facts:",
		"No unresolved provider-specific compact behavior was verified.",
	}, "\n")
}

func TestGenerateMapReduceCompactSummaryParallelChunks(t *testing.T) {
	est := contextx.NewTokenEstimator(4096)
	var raw []provider.Message
	for i := 0; i < 8; i++ {
		raw = append(raw,
			provider.Message{Role: "user", Content: fmt.Sprintf("goal-%d fix internal/agent/foo.go %s", i, strings.Repeat("pad ", 100))},
			provider.Message{Role: "assistant", Content: fmt.Sprintf("work-%d %s", i, strings.Repeat("done ", 100))},
			provider.Message{Role: "tool", Name: "terminal", Content: "go test ./internal/agent failed once then passed"},
		)
	}
	full := estimateProviderMessages(est, raw)
	chunkBudget := full/4 + 1

	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	_ = cfg.Set("context.compact_max_chunk_tokens", fmt.Sprintf("%d", chunkBudget))
	_ = cfg.Set("context.compact_max_parallel", "4")

	cp := &countingCompactProvider{name: "count"}
	a := &Agent{
		cfg:        cfg,
		contextEst: est,
		provider:   cp,
	}
	snap := providerSnapshot{provider: cp, model: "test-model"}
	summary, source, err := a.generateMapReduceCompactSummary(context.Background(), raw, snap, CompactSessionOptions{})
	if err != nil {
		t.Fatalf("generateMapReduceCompactSummary: %v", err)
	}
	if source != "llm-mapreduce" {
		t.Fatalf("expected llm-mapreduce source, got %q", source)
	}
	calls := int(cp.calls.Load())
	if calls < 3 {
		t.Fatalf("expected multiple map calls plus merge, got %d calls", calls)
	}
	if !strings.Contains(summary, "Current user goal:") || !strings.Contains(summary, "Pending work:") {
		t.Fatalf("unexpected summary:\n%s", summary)
	}
	// Last call should be merge.
	cp.mu.Lock()
	last := ""
	if len(cp.prompts) > 0 {
		last = cp.prompts[len(cp.prompts)-1]
	}
	cp.mu.Unlock()
	if !strings.Contains(last, "Merge the segment summaries") {
		t.Fatalf("last prompt should be merge, got prefix: %s", truncate(last, 120))
	}
}

func TestCompactSessionMapReduceWritesBoundary(t *testing.T) {
	est := contextx.NewTokenEstimator(4096)
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	_ = cfg.Set("context.compact_max_chunk_tokens", "200")
	_ = cfg.Set("context.compact_max_parallel", "3")

	sess := session.NewSession("compact-mapreduce", t.TempDir())
	for i := 0; i < 6; i++ {
		sess.AddMessage("user", fmt.Sprintf("goal-%d fix internal/agent/context_planner.go %s", i, strings.Repeat("x", 120)))
		sess.AddMessage("assistant", fmt.Sprintf("progress-%d %s", i, strings.Repeat("y", 120)))
		sess.AddToolMessage("terminal", "go test ./internal/agent passed")
	}

	cp := &countingCompactProvider{name: "count"}
	a := &Agent{cfg: cfg, contextEst: est, provider: cp}
	result, err := a.CompactSession(context.Background(), sess, "manual")
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	if result.SummarySource != "llm-mapreduce" && result.SummarySource != "llm" {
		// small envs may fit one chunk depending on estimator
		t.Fatalf("unexpected summary source: %s calls=%d", result.SummarySource, cp.calls.Load())
	}
	if result.BoundaryID == "" {
		t.Fatal("expected boundary id")
	}
	if !session.IsCompactBoundary(sess.GetMessages()[len(sess.GetMessages())-1]) {
		t.Fatal("expected compact boundary written")
	}
}
