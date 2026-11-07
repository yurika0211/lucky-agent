package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-tools/memory"
)

// DefaultMemoryRecallTimeout bounds one memory recall. The vault search holds
// its lock until it finishes, so a cancelled caller used to wait out the
// request deadline. 15s is far above a small vault and far under the hangs
// that ran for minutes on an empty result.
const DefaultMemoryRecallTimeout = 15 * time.Second

// RecallTimeoutError is a recall that exceeded its own deadline. Partial is
// set only when a phase finished before the deadline; a hang inside one phase
// returns this error with Partial nil.
type RecallTimeoutError struct {
	Limit   time.Duration
	Phase   string
	Partial []memory.SearchResult
}

func (e *RecallTimeoutError) Error() string {
	if e == nil {
		return "memory recall timed out"
	}
	return "memory recall timed out after " + e.Limit.String() + " during " + e.Phase
}

func (e *RecallTimeoutError) Timeout() bool { return true }

func (e *RecallTimeoutError) Unwrap() error { return context.DeadlineExceeded }

// recallObservation is one recall's phase timings. Zero durations are phases
// that did not run. ScanScope is the vault size the scan walked; ReadCount is
// notes opened one by one after the scan.
type recallObservation struct {
	Scan         time.Duration
	ScanScope    int
	Graph        time.Duration
	GraphHits    int
	Read         time.Duration
	ReadCount    int
	Hits         int
	Partial      bool
	TimedOut     bool
	TimeoutPhase string
}

// recallObserver receives phase timings. Tests install one; production logs.
type recallObserver interface {
	observeRecall(recallObservation)
}

type logRecallObserver struct{}

func (logRecallObserver) observeRecall(obs recallObservation) {
	args := []any{
		"scan_ms", obs.Scan.Milliseconds(),
		"scan_scope", obs.ScanScope,
		"graph_ms", obs.Graph.Milliseconds(),
		"graph_hits", obs.GraphHits,
		"read_ms", obs.Read.Milliseconds(),
		"read_count", obs.ReadCount,
		"hits", obs.Hits,
		"partial", obs.Partial,
		"timed_out", obs.TimedOut,
	}
	if obs.TimeoutPhase != "" {
		args = append(args, "timeout_phase", obs.TimeoutPhase)
	}
	if obs.TimedOut {
		logger.Warn("memory recall timed out", args...)
		return
	}
	logger.Info("memory recall", args...)
}

// recallTimeoutStore bounds Recall and records scan, graph, and per-note read
// time. Ranking stays on the inner store.
type recallTimeoutStore struct {
	memory.RuntimeStore
	timeout  time.Duration
	observer recallObserver
}

func wrapRecallTimeout(store memory.RuntimeStore, timeout time.Duration) memory.RuntimeStore {
	if store == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = DefaultMemoryRecallTimeout
	}
	if existing, ok := store.(*recallTimeoutStore); ok {
		existing.timeout = timeout
		return existing
	}
	return &recallTimeoutStore{
		RuntimeStore: store,
		timeout:      timeout,
		observer:     logRecallObserver{},
	}
}

func (s *recallTimeoutStore) Recall(ctx context.Context, request memory.RecallRequest) ([]memory.SearchResult, error) {
	if s == nil || s.RuntimeStore == nil {
		return nil, errors.New("memory runtime is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := s.timeout
	if timeout <= 0 {
		timeout = DefaultMemoryRecallTimeout
	}
	recallCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	progress := &recallProgress{}
	progress.setPhase("scan")
	done := make(chan recallOutcome, 1)
	go func() {
		outcome := s.runRecall(recallCtx, request, timeout, progress)
		select {
		case <-recallCtx.Done():
			// Caller already logged the deadline. Fill phase times the caller
			// could not see yet, then record the line once.
			outcome.obs.TimedOut = true
			if outcome.obs.TimeoutPhase == "" {
				outcome.obs.TimeoutPhase = progress.phaseName()
			}
			if outcome.obs.Read == 0 {
				outcome.obs.Read = progress.read()
			}
			if outcome.obs.Scan == 0 {
				outcome.obs.Scan = progress.scan()
			}
			s.observe(outcome.obs)
		default:
		}
		done <- outcome
	}()

	select {
	case outcome := <-done:
		s.observe(outcome.obs)
		return outcome.results, outcome.err
	case <-recallCtx.Done():
		// The vault search keeps the lock until it returns and does not look
		// at ctx. Record the phase the caller is stuck in and return now.
		phase := progress.phaseName()
		partial := progress.partial()
		s.observe(recallObservation{
			Scan:         progress.scan(),
			ScanScope:    storeCount(s.RuntimeStore),
			Graph:        progress.graph(),
			GraphHits:    progress.graphHits(),
			Read:         progress.read(),
			ReadCount:    progress.reads(),
			Hits:         len(partial),
			Partial:      len(partial) > 0,
			TimedOut:     true,
			TimeoutPhase: phase,
		})
		return partial, &RecallTimeoutError{Limit: timeout, Phase: phase, Partial: partial}
	}
}

// recallProgress is what the caller can see while runRecall is still inside
// a phase that does not stop for cancellation.
type recallProgress struct {
	mu        sync.Mutex
	phase     string
	opened    []memory.SearchResult
	readCount    int
	scanElapsed  time.Duration
	graphElapsed time.Duration
	graphEdges   int
	readElapsed  time.Duration
}

func (p *recallProgress) setPhase(phase string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.phase = phase
	p.mu.Unlock()
}

func (p *recallProgress) phaseName() string {
	if p == nil {
		return "scan"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase == "" {
		return "scan"
	}
	return p.phase
}

func (p *recallProgress) setScan(d time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.scanElapsed = d
	p.mu.Unlock()
}

func (p *recallProgress) setGraph(d time.Duration, hits int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.graphElapsed = d
	p.graphEdges = hits
	p.mu.Unlock()
}

func (p *recallProgress) scan() time.Duration {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scanElapsed
}

func (p *recallProgress) graph() time.Duration {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.graphElapsed
}

func (p *recallProgress) graphHits() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.graphEdges
}

func (p *recallProgress) setRead(d time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.readElapsed = d
	p.mu.Unlock()
}

func (p *recallProgress) read() time.Duration {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readElapsed
}

func (p *recallProgress) setReadCount(n int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.readCount = n
	p.mu.Unlock()
}

func (p *recallProgress) add(result memory.SearchResult) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.opened = append(p.opened, result)
	p.mu.Unlock()
}

func (p *recallProgress) reads() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readCount
}

func (p *recallProgress) partial() []memory.SearchResult {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.opened) == 0 {
		return nil
	}
	out := make([]memory.SearchResult, len(p.opened))
	copy(out, p.opened)
	return out
}

// runRecall is the phase sequence. It may outlive the caller when the inner
// search ignores cancellation. It always records what it measured.
func (s *recallTimeoutStore) runRecall(ctx context.Context, request memory.RecallRequest, timeout time.Duration, progress *recallProgress) recallOutcome {
	obs := recallObservation{ScanScope: storeCount(s.RuntimeStore)}
	if progress != nil {
		progress.setPhase("scan")
	}
	started := time.Now()
	// One search. ScanScope is the vault size it walked. When the caller asked
	// for graph expansion, that work is inside this same call: GraphHits is
	// the edges in the result, and a deadline during it is reported as graph.
	graphOn := request.Options.IncludeGraph && request.Options.GraphDepth > 0
	if graphOn && progress != nil {
		progress.setPhase("graph")
	}
	results, err := s.RuntimeStore.Recall(ctx, request)
	elapsed := time.Since(started)
	obs.GraphHits = countRecallGraphEdges(results)
	if graphOn {
		obs.Graph = elapsed
		if progress != nil {
			progress.setGraph(elapsed, obs.GraphHits)
		}
	}
	obs.Scan = elapsed
	if progress != nil {
		progress.setScan(elapsed)
	}

	if err == nil && len(results) > 0 {
		obs.ReadCount = countUnfilledRecallHits(results)
		if obs.ReadCount > 0 {
			if progress != nil {
				progress.setReadCount(obs.ReadCount)
				progress.setPhase("read")
			}
			readStarted := time.Now()
			filled, readErr := fillRecallEntries(ctx, s.RuntimeStore, results, progress)
			obs.Read = time.Since(readStarted)
			if progress != nil {
				progress.setRead(obs.Read)
			}
			if readErr != nil && ctx.Err() != nil {
				obs.Partial = len(filled) > 0
				obs.Hits = len(filled)
				obs.TimedOut = true
				obs.TimeoutPhase = "read"
				if len(filled) == 0 {
					return recallOutcome{results: results, err: &RecallTimeoutError{Limit: timeout, Phase: "read"}, obs: obs}
				}
				return recallOutcome{results: filled, err: &RecallTimeoutError{Limit: timeout, Phase: "read", Partial: filled}, obs: obs}
			}
			if readErr != nil {
				return recallOutcome{results: results, err: readErr, obs: obs}
			}
			results = filled
		}
	}

	obs.Hits = len(results)
	if err == nil && ctx.Err() != nil {
		obs.Partial = len(results) > 0
		obs.TimedOut = true
		obs.TimeoutPhase = "scan"
		return recallOutcome{results: results, err: &RecallTimeoutError{Limit: timeout, Phase: "scan", Partial: results}, obs: obs}
	}
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
		phase := "scan"
		if request.Options.IncludeGraph && request.Options.GraphDepth > 0 && obs.GraphHits == 0 && obs.Scan > 0 {
			phase = "graph"
		}
		if obs.ReadCount > 0 && obs.Read == 0 {
			phase = "read"
		}
		obs.TimedOut = true
		obs.TimeoutPhase = phase
		obs.Partial = len(results) > 0
		return recallOutcome{results: results, err: &RecallTimeoutError{Limit: timeout, Phase: phase, Partial: results}, obs: obs}
	}
	return recallOutcome{results: results, err: err, obs: obs}
}

type recallOutcome struct {
	results []memory.SearchResult
	err     error
	obs     recallObservation
}

func storeCount(store memory.RuntimeStore) int {
	if store == nil {
		return 0
	}
	counter, ok := store.(interface{ Count() int })
	if !ok || counter == nil {
		return 0
	}
	return counter.Count()
}

func (s *recallTimeoutStore) observe(obs recallObservation) {
	if s == nil || s.observer == nil {
		return
	}
	s.observer.observeRecall(obs)
}

func countRecallGraphEdges(results []memory.SearchResult) int {
	total := 0
	for _, result := range results {
		total += len(result.Paths)
		if len(result.Paths) == 0 && result.GraphScore > 0 {
			total++
		}
	}
	return total
}

func countUnfilledRecallHits(results []memory.SearchResult) int {
	missing := 0
	for _, result := range results {
		if result.Entry.ID != "" && result.Entry.Content == "" && result.Entry.Path == "" {
			missing++
		}
	}
	return missing
}

type recallEntryGetter interface {
	Get(string) (*memory.Entry, error)
}

// getRecallEntry stops waiting when ctx ends. A note read that ignores
// cancellation must not keep the recall in the read phase after the deadline.
func getRecallEntry(ctx context.Context, getter recallEntryGetter, id string) (*memory.Entry, error) {
	type got struct {
		entry *memory.Entry
		err   error
	}
	done := make(chan got, 1)
	go func() {
		entry, err := getter.Get(id)
		done <- got{entry: entry, err: err}
	}()
	select {
	case result := <-done:
		return result.entry, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func fillRecallEntries(ctx context.Context, store memory.RuntimeStore, results []memory.SearchResult, progress *recallProgress) ([]memory.SearchResult, error) {
	if store == nil {
		return results, nil
	}
	getter, ok := store.(interface {
		Get(string) (*memory.Entry, error)
	})
	if !ok {
		return results, nil
	}
	filled := make([]memory.SearchResult, 0, len(results))
	for _, result := range results {
		if err := ctx.Err(); err != nil {
			if len(filled) == 0 {
				return results, err
			}
			return filled, err
		}
		if result.Entry.ID == "" || result.Entry.Content != "" || result.Entry.Path != "" {
			filled = append(filled, result)
			progress.add(result)
			continue
		}
		entry, err := getRecallEntry(ctx, getter, result.Entry.ID)
		if err != nil {
			if ctx.Err() != nil && len(filled) > 0 {
				return filled, ctx.Err()
			}
			return filled, err
		}
		if entry != nil {
			result.Entry = *entry
		}
		filled = append(filled, result)
		progress.add(result)
	}
	return filled, nil
}

// blockingRecallStore stalls Recall until release is closed or ctx ends.
// phase selects where it stalls: "scan", "graph", or "read".
type blockingRecallStore struct {
	memory.RuntimeStore
	phase   string
	release chan struct{}
	started chan struct{}
	once    sync.Once
	hits    []memory.SearchResult
}

func (s *blockingRecallStore) Recall(ctx context.Context, request memory.RecallRequest) ([]memory.SearchResult, error) {
	if s.phase == "read" {
		s.markStarted()
		return s.hits, nil
	}
	if s.phase == "graph" && !(request.Options.IncludeGraph && request.Options.GraphDepth > 0) {
		return s.RuntimeStore.Recall(ctx, request)
	}
	s.markStarted()
	select {
	case <-s.release:
		if len(s.hits) > 0 {
			return s.hits, nil
		}
		return s.RuntimeStore.Recall(ctx, request)
	case <-ctx.Done():
		// Honor cancellation. The production vault search does not; tests
		// that need that shape use hangRecallStore.
		return nil, ctx.Err()
	}
}

func (s *blockingRecallStore) Get(id string) (*memory.Entry, error) {
	if s.phase == "read" {
		s.markStarted()
		select {
		case <-s.release:
		case <-time.After(time.Hour):
			return nil, errors.New("memory note read timed out")
		}
	}
	if s.RuntimeStore == nil {
		return &memory.Entry{ID: id, Content: "filled"}, nil
	}
	return s.RuntimeStore.Get(id)
}

func (s *blockingRecallStore) markStarted() {
	s.once.Do(func() {
		if s.started != nil {
			close(s.started)
		}
	})
}

// hangRecallStore ignores ctx until release closes, matching the vault search
// that holds its lock through the whole scan.
type hangRecallStore struct {
	memory.RuntimeStore
	release chan struct{}
	started chan struct{}
	once    sync.Once
	hits    []memory.SearchResult
}

func (s *hangRecallStore) Count() int {
	if s == nil || s.RuntimeStore == nil {
		return 0
	}
	return s.RuntimeStore.Count()
}

func (s *blockingRecallStore) Count() int {
	if s == nil || s.RuntimeStore == nil {
		return 0
	}
	return s.RuntimeStore.Count()
}

func (s *hangRecallStore) Recall(ctx context.Context, request memory.RecallRequest) ([]memory.SearchResult, error) {
	s.once.Do(func() {
		if s.started != nil {
			close(s.started)
		}
	})
	<-s.release
	if ctx.Err() != nil {
		return s.hits, ctx.Err()
	}
	return s.hits, nil
}
