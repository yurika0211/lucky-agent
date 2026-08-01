package memory

import (
	"fmt"
	"sync"
	"testing"
)

func TestMaintenanceCoordinatorCadenceAndSessionAttribution(t *testing.T) {
	c := NewMaintenanceCoordinator(MaintenanceConfig{
		DecayEvery:     2,
		SummarizeEvery: 3,
		ExpireEvery:    5,
	})

	for i := uint64(1); i <= 5; i++ {
		event := c.RecordTurn("session-a")
		if event.RuntimeCount != i || event.SessionCount != i {
			t.Fatalf("turn %d: unexpected counts: %#v", i, event)
		}
		if event.RunDecay != (i%2 == 0) {
			t.Errorf("turn %d: RunDecay=%v", i, event.RunDecay)
		}
		if event.RunSummarize != (i%3 == 0) {
			t.Errorf("turn %d: RunSummarize=%v", i, event.RunSummarize)
		}
		if event.RunExpire != (i%5 == 0) {
			t.Errorf("turn %d: RunExpire=%v", i, event.RunExpire)
		}
	}

	event := c.RecordTurn("session-b")
	if event.RuntimeCount != 6 || event.SessionCount != 1 || event.SessionID != "session-b" {
		t.Fatalf("unexpected second-session event: %#v", event)
	}
	if got := c.SessionCount("session-a"); got != 5 {
		t.Fatalf("session-a count = %d, want 5", got)
	}
	if got := c.SessionCount("session-b"); got != 1 {
		t.Fatalf("session-b count = %d, want 1", got)
	}

	c.ForgetSession("session-a")
	if got := c.SessionCount("session-a"); got != 0 {
		t.Fatalf("session-a count after ForgetSession = %d, want 0", got)
	}
	if got := c.RuntimeCount(); got != 6 {
		t.Fatalf("runtime count changed after forgetting session: %d", got)
	}
}

func TestMaintenanceCoordinatorConcurrentRecordTurn(t *testing.T) {
	const (
		workers        = 12
		turnsPerWorker = 250
	)
	c := NewMaintenanceCoordinator(MaintenanceConfig{})

	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			sessionID := fmt.Sprintf("session-%d", worker%3)
			for i := 0; i < turnsPerWorker; i++ {
				c.RecordTurn(sessionID)
			}
		}()
	}
	wg.Wait()

	wantTotal := uint64(workers * turnsPerWorker)
	if got := c.RuntimeCount(); got != wantTotal {
		t.Fatalf("runtime count = %d, want %d", got, wantTotal)
	}
	for worker := 0; worker < 3; worker++ {
		want := uint64(workers / 3 * turnsPerWorker)
		if worker < workers%3 {
			want += uint64(turnsPerWorker)
		}
		if got := c.SessionCount(fmt.Sprintf("session-%d", worker)); got != want {
			t.Fatalf("session-%d count = %d, want %d", worker, got, want)
		}
	}
}

func TestStoreRecordTurnSupportsZeroValue(t *testing.T) {
	var store Store
	event := store.RecordTurn("session-zero")
	if event.RuntimeCount != 1 || event.SessionCount != 1 {
		t.Fatalf("unexpected zero-value store event: %#v", event)
	}
	if got := store.MaintenanceCoordinator().RuntimeCount(); got != 1 {
		t.Fatalf("store runtime count = %d, want 1", got)
	}
}
