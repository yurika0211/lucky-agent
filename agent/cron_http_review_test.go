package agent

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/cron"
)

type immediateOnceSchedule struct{ used atomic.Bool }

func (s *immediateOnceSchedule) Next(now time.Time) time.Time {
	if s.used.Swap(true) {
		return time.Time{}
	}
	return now
}

func (*immediateOnceSchedule) String() string { return "immediately once" }

func TestAddCronJobInitializesFlagBeforeAnImmediateTask(t *testing.T) {
	engine := cron.NewEngine()
	observed := make(chan bool, 1)
	finished := make(chan struct{})
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{Name: "terminal", Handler: func(map[string]any) (string, error) {
		job, ok := engine.GetJob("immediate")
		observed <- ok && job.DeleteAfterRun
		return "", nil
	}})
	a := &Agent{cronEngine: engine, tools: registry, gateway: tool.NewGateway(registry)}
	engine.SetEventHandler(func(event cron.Event) {
		switch event.Type {
		case cron.EventJobAdded:
			// Keep AddJobWithMeta in flight while the running engine wakes.
			// An ungated task finishes before its caller can set the flag.
			select {
			case <-finished:
			case <-time.After(100 * time.Millisecond):
			}
		case cron.EventJobCompleted:
			close(finished)
		}
	})
	engine.Start()
	defer engine.Stop()
	if err := a.AddCronJob("immediate", "", "", "shell", "stub only", map[string]string{"delete_after_run": "true"}, &immediateOnceSchedule{}); err != nil {
		t.Fatal(err)
	}
	select {
	case flag := <-observed:
		if !flag {
			t.Fatal("task executed before delete_after_run was initialized")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("immediate task did not execute")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("task did not finish")
	}
	if _, ok := engine.GetJob("immediate"); ok {
		t.Fatal("completed one-shot job was not deleted")
	}
}

func TestAddCronJobDeleteAfterRunBooleanValues(t *testing.T) {
	for _, value := range []string{"", "false", "0", "no", "true", "1", " yes "} {
		t.Run(value, func(t *testing.T) {
			engine := cron.NewEngine()
			defer engine.Stop()
			a := &Agent{cronEngine: engine}
			err := a.AddCronJob("future", "", "", "shell", "not executed", map[string]string{"delete_after_run": value}, cron.IntervalSchedule{Interval: 24 * time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			job, ok := engine.GetJob("future")
			want := value == "true" || value == "1" || value == " yes "
			if !ok || job.DeleteAfterRun != want || job.NextRun.IsZero() || job.RunCount != 0 || job.Status != cron.StatusIdle {
				t.Fatalf("unexpected initial state for %q: %+v", value, job)
			}
		})
	}
}

func TestAddCronJobSaveFailureDoesNotStartOrLeaveJob(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block store creation"), 0600); err != nil {
		t.Fatal(err)
	}
	engine := cron.NewEngine()
	defer engine.Stop()
	a := &Agent{cronEngine: engine, cronStore: cron.NewStore(filepath.Join(blocker, "jobs.json"))}
	err := a.AddCronJob("unsaved", "", "", "shell", "must not execute", map[string]string{"delete_after_run": "true"}, &immediateOnceSchedule{})
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if engine.IsRunning() {
		t.Fatal("unsaved scheduler was started")
	}
	if _, ok := engine.GetJob("unsaved"); ok {
		t.Fatal("failed creation left a job in memory")
	}
}
