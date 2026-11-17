package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/metrics"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
)

func TestRequiredCompactIdentifiersSkipEphemeralStatusLine(t *testing.T) {
	status := []provider.Message{{Role: "tool", Content: "- status error dur 11 sid 324151"}}
	got := requiredCompactIdentifiers(status)
	for _, id := range got {
		if strings.Contains(id, "dur") || strings.Contains(id, "sid") || strings.Contains(id, "324151") {
			t.Fatalf("status line entered required identifiers: %v", got)
		}
	}

	errHead := []provider.Message{{Role: "tool", Content: "error: operation failed at /srv/app/main.go"}}
	required := requiredCompactIdentifiers(errHead)
	joined := strings.Join(required, "\n")
	if !strings.Contains(joined, "error: operation failed at /srv/app/main.go") && !strings.Contains(joined, "/srv/app/main.go") {
		t.Fatalf("real error head was dropped: %v", required)
	}

	command := []provider.Message{{Role: "tool", Content: "git status --short"}}
	if got := requiredCompactIdentifiers(command); len(got) != 1 || got[0] != "git status --short" {
		t.Fatalf("command left required set: %v", got)
	}
}

func TestAutoCompactCancelStillForceLocal(t *testing.T) {
	// Originally verified that a canceled parent still writes a local
	// boundary through ForceLocal. It still does that, and identifier
	// validation stays on. The old three-message fixture fit inside an
	// illegal target-0.2 tail. The diagnosed legal pair is window 16000/512,
	// threshold 0.01, target 0.009, summary reserve 1, retain 6. The same
	// seven-turn shape leaves the last complete turn and compacts the older
	// turns, which still contain /srv/app/main.go.
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Context.MaxContextTokens = 16000
	cfg.Context.AutoCompact = true
	cfg.Context.AutoCompactMinMessages = 1
	cfg.Context.AutoCompactThreshold = 0.01
	cfg.Context.AutoCompactTargetRatio = 0.009
	cfg.Context.AutoCompactCooldownTurns = 1
	cfg.Context.AutoCompactReservedSummaryTokens = 1
	cfg.Context.AutoCompactRetainTurns = 6
	cfg.ContextBudget.ReservedRatio = 512.0 / 16000.0
	mgr.SetConfig(cfg)

	sess := session.NewSession("compact-cancel", t.TempDir())
	for i := 0; i < 7; i++ {
		sess.AddMessage("user", fmt.Sprintf("turn %d keep /srv/app/main.go %s", i, strings.Repeat("turn-note ", 30)))
		sess.AddMessage("assistant", fmt.Sprintf("turn %d noted /srv/app/main.go", i))
	}

	a := &Agent{
		cfg:        mgr,
		provider:   &staticChatProvider{name: "static", err: context.Canceled},
		contextEst: contextx.NewTokenEstimator(16000),
		contextWin: contextx.NewContextWindow(contextx.WindowConfig{MaxTokens: 16000, ReservedTokens: 512}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := sess.MessageCount()
	a.maybeAutoCompactSessionWithProvider(ctx, sess, "keep /srv/app/main.go", false, a.providerSnapshotForTurn(""), nil)
	if sess.MessageCount() <= before {
		t.Fatal("canceled parent did not write a local compact boundary")
	}
	last := sess.GetMessages()[sess.MessageCount()-1]
	meta, ok := session.ParseCompactMetadata(last)
	if !ok || meta.SummarySource != "local" || meta.ID == "" {
		t.Fatalf("expected local boundary, ok=%t meta=%+v", ok, meta)
	}
}

func TestChatLoopIterationRecordsProviderCall(t *testing.T) {
	m := metrics.NewMetrics()
	a := &Agent{
		metrics:  m,
		provider: &staticChatProvider{name: "static", content: "ok"},
	}
	snap := providerSnapshot{provider: a.provider, model: "static"}
	if _, err := a.chatLoopIteration(context.Background(), []provider.Message{{Role: "user", Content: "hi"}}, provider.CallOptions{}, false, snap); err != nil {
		t.Fatal(err)
	}
	got := m.Snapshot().Providers["static"].Calls
	if got != 1 {
		t.Fatalf("RecordProviderCall count = %d, want 1", got)
	}
}

func TestFinalizeStreamHTTPDoesNotRecordChatRequest(t *testing.T) {
	m := metrics.NewMetrics()
	before := m.Snapshot().ChatRequests
	a := &Agent{metrics: m}
	events := make(chan ChatEvent, 4)
	input := TextUserTurnInput("hello").WithScope(TurnScope{Platform: "http"})
	a.finalizeStreamWithRecap(events, nil, input, "done", "", nil, false, 0, 12)
	if got := m.Snapshot().ChatRequests; got != before {
		t.Fatalf("HTTP finalize recorded chat request: before %d after %d", before, got)
	}
	select {
	case ev := <-events:
		if ev.Type != ChatEventDone {
			t.Fatalf("event type %v", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("finalize did not emit done")
	}

	cli := TextUserTurnInput("hello")
	a.finalizeStreamWithRecap(events, nil, cli, "done", "", nil, false, 0, 0)
	if got := m.Snapshot().ChatRequests; got != before+1 {
		t.Fatalf("non-HTTP finalize count = %d, want %d", got, before+1)
	}
}

func TestNewSessionRecordsSessionOpen(t *testing.T) {
	m := metrics.NewMetrics()
	sessions, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{metrics: m, sessions: sessions, cfg: mgr}
	_, err = a.Chat(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "provider not initialized") {
		t.Fatalf("Chat error = %v, want provider not initialized after New", err)
	}
	if got := m.Snapshot().TotalSessions; got != 1 {
		t.Fatalf("Chat RecordSessionOpen count = %d, want 1", got)
	}
	if _, err := a.ChatStream(context.Background(), "hello again"); err == nil || !strings.Contains(err.Error(), "provider not initialized") {
		t.Fatalf("ChatStream error = %v, want provider not initialized after New", err)
	}
	if got := m.Snapshot().TotalSessions; got != 2 {
		t.Fatalf("ChatStream RecordSessionOpen count = %d, want 2", got)
	}
}
