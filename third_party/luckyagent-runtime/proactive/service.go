package proactive

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RuntimeService periodically runs the proactive decision pipeline.
type RuntimeService struct {
	Runner   Runner
	Executor ActionExecutor
	Store    *Store
	Interval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

type RuntimeServiceOptions struct {
	Runner   Runner
	Executor ActionExecutor
	Store    *Store
	Interval time.Duration
}

func NewRuntimeService(opts RuntimeServiceOptions) *RuntimeService {
	return &RuntimeService{
		Runner:   opts.Runner,
		Executor: opts.Executor,
		Store:    opts.Store,
		Interval: opts.Interval,
	}
}

func (s *RuntimeService) RunOnce(ctx context.Context) (Decision, []ActionExecution, error) {
	if s == nil {
		return Decision{}, nil, nil
	}
	decision, err := s.Runner.RunDryRun(ctx)
	if err != nil {
		return Decision{}, nil, err
	}
	executions, err := s.Executor.Execute(ctx, decision)
	if err != nil {
		return decision, nil, err
	}
	if s.Store != nil {
		if err := s.Store.RecordActionExecutions(executions); err != nil {
			return decision, executions, fmt.Errorf("persist proactive action executions: %w", err)
		}
	}
	return decision, executions, nil
}

func (s *RuntimeService) Start(ctx context.Context) error {
	if s == nil || s.Interval <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.loop(runCtx, s.done)
	return nil
}

func (s *RuntimeService) Stop() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

func (s *RuntimeService) Started() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel != nil
}

func (s *RuntimeService) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, _, err := s.RunOnce(ctx); err != nil {
				if s.Store != nil {
					now := time.Now()
					_ = s.Store.RecordRuntimeEvent(RuntimeEvent{
						ID:        fmt.Sprintf("runtime-proactive-error-%d", now.UnixNano()),
						Source:    "proactive",
						Type:      "proactive_error",
						Name:      "runtime_service",
						Value:     1,
						Metadata:  map[string]string{"error": err.Error()},
						CreatedAt: now,
					})
				}
			}
		}
	}
}
