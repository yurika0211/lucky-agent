package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-tools/memory"
)

func TestRecallReturnsBeforeHungScan(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	inner := &hangRecallStore{release: release, started: started, hits: []memory.SearchResult{{
		Entry: memory.Entry{ID: "mem-1", Content: "kept after the scan finishes"},
	}}}
	seen := &lockRecallObserver{}
	store := &recallTimeoutStore{RuntimeStore: inner, timeout: 40 * time.Millisecond, observer: seen}

	start := time.Now()
	results, err := store.Recall(context.Background(), memory.RecallRequest{Query: "anything"})
	elapsed := time.Since(start)
	var timeout *RecallTimeoutError
	if !errors.As(err, &timeout) || timeout.Phase != "scan" || timeout.Partial != nil {
		t.Fatalf("err=%v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results=%d", len(results))
	}
	if elapsed > time.Second {
		t.Fatalf("recall blocked for %s", elapsed)
	}
	close(release)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("scan never started")
	}
	wait := time.NewTimer(time.Second)
	defer wait.Stop()
	for {
		lines := seen.snapshot()
		if len(lines) >= 2 && lines[1].TimedOut && lines[1].TimeoutPhase == "scan" && lines[1].Scan > 0 {
			break
		}
		select {
		case <-wait.C:
			t.Fatalf("finished scan was not recorded: %+v", seen.snapshot())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestRecallTimeoutKeepsPartialResults(t *testing.T) {
	partial := []memory.SearchResult{{Entry: memory.Entry{ID: "mem-1", Content: "partial hit"}}}
	inner := &scriptRecallStore{err: context.DeadlineExceeded, hits: partial}
	var seen recallObservation
	store := &recallTimeoutStore{
		RuntimeStore: inner,
		timeout:      time.Second,
		observer:     captureRecallObserver{dst: &seen},
	}
	results, err := store.Recall(context.Background(), memory.RecallRequest{Query: "name"})
	var timeout *RecallTimeoutError
	if !errors.As(err, &timeout) || timeout.Phase != "scan" {
		t.Fatalf("err=%v", err)
	}
	if len(results) != 1 || results[0].Entry.ID != "mem-1" || len(timeout.Partial) != 1 {
		t.Fatalf("results=%+v partial=%+v", results, timeout.Partial)
	}
	if !seen.TimedOut || !seen.Partial || seen.Hits != 1 || seen.TimeoutPhase != "scan" {
		t.Fatalf("observation=%+v", seen)
	}
}

func TestRecallEmptyResultReturnsWithoutHanging(t *testing.T) {
	inner := &scriptRecallStore{}
	var seen recallObservation
	store := &recallTimeoutStore{
		RuntimeStore: inner,
		timeout:      time.Second,
		observer:     captureRecallObserver{dst: &seen},
	}
	start := time.Now()
	results, err := store.Recall(context.Background(), memory.RecallRequest{Query: "missing"})
	if err != nil || len(results) != 0 {
		t.Fatalf("results=%v err=%v", results, err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("empty recall waited on the timeout")
	}
	if seen.Hits != 0 || seen.TimedOut || seen.ScanScope != 0 {
		t.Fatalf("observation=%+v", seen)
	}
}

func TestRecallRecordsScanGraphAndReadPhases(t *testing.T) {
	inner := &scriptRecallStore{
		count: 122,
		hits: []memory.SearchResult{{
			Entry:      memory.Entry{ID: "mem-9"},
			GraphScore: 0.2,
		}},
	}
	var seen recallObservation
	store := &recallTimeoutStore{
		RuntimeStore: inner,
		timeout:      time.Second,
		observer:     captureRecallObserver{dst: &seen},
	}
	results, err := store.Recall(context.Background(), memory.RecallRequest{
		Query:   "project",
		Options: memory.SearchOptions{IncludeGraph: true, GraphDepth: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Entry.Content != "filled mem-9" {
		t.Fatalf("results=%+v", results)
	}
	if seen.ScanScope != 122 || seen.GraphHits != 1 || seen.ReadCount != 1 || seen.Hits != 1 {
		t.Fatalf("observation=%+v", seen)
	}
	if seen.Scan <= 0 || seen.Graph <= 0 || seen.Read <= 0 {
		t.Fatalf("phase timings missing: %+v", seen)
	}
}

func TestRecallReadTimeoutReturnsNotesAlreadyOpened(t *testing.T) {
	release := make(chan struct{})
	inner := &blockingRecallStore{
		phase:   "read",
		release: release,
		started: make(chan struct{}),
		hits: []memory.SearchResult{
			{Entry: memory.Entry{ID: "open", Content: "already open"}},
			{Entry: memory.Entry{ID: "stuck"}},
		},
	}
	seen := &lockRecallObserver{}
	store := &recallTimeoutStore{
		RuntimeStore: inner,
		timeout:      60 * time.Millisecond,
		observer:     seen,
	}
	start := time.Now()
	results, err := store.Recall(context.Background(), memory.RecallRequest{Query: "notes"})
	elapsed := time.Since(start)
	var timeout *RecallTimeoutError
	if !errors.As(err, &timeout) || timeout.Phase != "read" {
		t.Fatalf("err=%v elapsed=%s", err, elapsed)
	}
	if len(results) != 1 || results[0].Entry.ID != "open" {
		t.Fatalf("results=%+v", results)
	}
	if elapsed > time.Second {
		t.Fatalf("read timeout blocked for %s", elapsed)
	}
	lines := seen.snapshot()
	if len(lines) == 0 || !lines[0].Partial || lines[0].TimeoutPhase != "read" || lines[0].ReadCount != 1 {
		t.Fatalf("lines=%+v err=%v", lines, err)
	}
	close(release)
	wait := time.NewTimer(time.Second)
	defer wait.Stop()
	for {
		lines := seen.snapshot()
		if len(lines) >= 2 && lines[1].Read > 0 && lines[1].Scan > 0 && lines[1].TimeoutPhase == "read" && lines[1].Hits == 1 {
			break
		}
		select {
		case <-wait.C:
			t.Fatalf("finished read was not recorded: %+v", seen.snapshot())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestRecallOnAgentUsesTimeoutWrapper(t *testing.T) {
	a := memoryCapAgent(t, DefaultMaxMemoryNoteBytes)
	if _, ok := a.memory.(*recallTimeoutStore); !ok {
		t.Fatalf("memory store = %T", a.memory)
	}
	_, err := a.WriteMemory(context.Background(), memory.RememberRequest{
		Content: "The recall budget is fifteen seconds.", Category: "fact", Tier: memory.TierMedium, Importance: 0.6,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	results, err := a.RecallMemory(context.Background(), memory.RecallRequest{Query: "budget"})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("small vault recall was slow")
	}
	if len(results) == 0 || !strings.Contains(results[0].Entry.Content, "fifteen seconds") {
		t.Fatalf("results=%+v", results)
	}
	miss, err := a.RecallMemory(context.Background(), memory.RecallRequest{Query: "zzzz-not-stored"})
	if err != nil || len(miss) != 0 {
		t.Fatalf("empty recall results=%v err=%v", miss, err)
	}
}

type discardRecallObserver struct{}

func (discardRecallObserver) observeRecall(recallObservation) {}

// lockRecallObserver keeps every recall line in order. The first is what the
// caller saw; a later one is recorded when a detached search stops.
type lockRecallObserver struct {
	mu  sync.Mutex
	all []recallObservation
}

func (o *lockRecallObserver) observeRecall(obs recallObservation) {
	o.mu.Lock()
	o.all = append(o.all, obs)
	o.mu.Unlock()
}

func (o *lockRecallObserver) snapshot() []recallObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]recallObservation, len(o.all))
	copy(out, o.all)
	return out
}

type captureRecallObserver struct{ dst *recallObservation }

func (c captureRecallObserver) observeRecall(obs recallObservation) {
	if c.dst != nil {
		*c.dst = obs
	}
}

// scriptRecallStore returns a fixed scan. Get fills an id-only hit so the
// read phase has something to time.
type scriptRecallStore struct {
	memory.RuntimeStore
	hits  []memory.SearchResult
	err   error
	count int
}

func (s *scriptRecallStore) Recall(context.Context, memory.RecallRequest) ([]memory.SearchResult, error) {
	return s.hits, s.err
}

func (s *scriptRecallStore) Count() int { return s.count }

func (s *scriptRecallStore) Get(id string) (*memory.Entry, error) {
	return &memory.Entry{ID: id, Content: "filled " + id, Path: "50_Facts/" + id + ".md"}, nil
}
