package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
)

// snapshotTestProvider records which provider handled a call.  The small
// barrier makes both calls overlap so a shared Agent.provider would be exposed
// by the test.
type snapshotTestProvider struct {
	name    string
	started chan<- string
	barrier *sync.WaitGroup
}

func (p *snapshotTestProvider) Name() string { return p.name }

func (p *snapshotTestProvider) Chat(ctx context.Context, _ []provider.Message) (*provider.Response, error) {
	p.started <- p.name
	if p.barrier != nil {
		p.barrier.Done()
	}
	return &provider.Response{Content: p.name, Model: p.name}, nil
}

func (p *snapshotTestProvider) ChatStream(context.Context, []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("stream not used in snapshot test")
}

func (p *snapshotTestProvider) Validate() error { return nil }

func TestChatLoopIterationUsesTurnProviderSnapshot(t *testing.T) {
	started := make(chan string, 2)
	var barrier sync.WaitGroup
	barrier.Add(2)
	first := &snapshotTestProvider{name: "provider-first", started: started, barrier: &barrier}
	second := &snapshotTestProvider{name: "provider-second", started: started, barrier: &barrier}
	a := &Agent{provider: first}
	var calls sync.WaitGroup
	calls.Add(2)

	call := func(snap providerSnapshot, out chan<- *provider.Response) {
		defer calls.Done()
		resp, err := a.chatLoopIteration(context.Background(), nil, provider.CallOptions{}, false, snap)
		if err != nil {
			t.Errorf("chatLoopIteration() error = %v", err)
			return
		}
		out <- resp
	}
	results := make(chan *provider.Response, 2)
	go call(providerSnapshot{provider: first, model: "model-first"}, results)
	go call(providerSnapshot{provider: second, model: "model-second"}, results)

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case name := <-started:
			seen[name] = true
		case <-make(chan struct{}):
			t.Fatal("unreachable")
		}
	}
	barrier.Wait()
	calls.Wait()
	close(results)
	for resp := range results {
		if resp == nil || !seen[resp.Content] {
			t.Fatalf("unexpected provider response: %+v (seen=%v)", resp, seen)
		}
	}
	if !seen["provider-first"] || !seen["provider-second"] {
		t.Fatalf("both turn providers must be called, got %v", seen)
	}
}
