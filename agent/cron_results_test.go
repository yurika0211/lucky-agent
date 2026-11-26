package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-tools/cron"
)

func TestCronEventsPersistAccountExecutionAfterResultCommit(t *testing.T) {
	engine := cron.NewEngine()
	scheduled := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if err := engine.AddJobWithMeta("job-1", "Job", "description", cron.OnceSchedule{At: scheduled}, func() error { return nil }, map[string]string{"platform": "android"}); err != nil {
		t.Fatal(err)
	}
	store := cron.NewMemoryResultStore()
	a := &Agent{
		cronEngine:           engine,
		cronResults:          store,
		cronResultExecutions: make(map[string]string),
		cronResultPayloads:   make(map[string]cronNotificationPayload),
	}
	a.beginCronResultExecution(cron.Event{Type: cron.EventJobStarted, JobID: "job-1", JobName: "Job", Time: scheduled.Add(time.Minute), ScheduledAt: scheduled})
	a.recordCronDeliveryPayload(cronNotificationPayload{JobID: "job-1", Outcome: "succeeded", RawResult: "answer"})
	a.finishCronResultExecution(cron.Event{Type: cron.EventJobCompleted, JobID: "job-1", Time: scheduled.Add(2 * time.Minute), ScheduledAt: scheduled}, nil)

	executions := store.ListExecutions("job-1")
	if len(executions) != 1 || executions[0].Status != cron.ExecutionSucceeded || executions[0].Result != "answer" {
		t.Fatalf("unexpected executions: %#v", executions)
	}
	if got := store.ListDeliveries(executions[0].ID); len(got) != 1 || got[0].Status != cron.DeliveryPending {
		t.Fatalf("unexpected deliveries: %#v", got)
	}
}

func TestCronDeliveryFailureDoesNotFailExecution(t *testing.T) {
	engine := cron.NewEngine()
	scheduled := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if err := engine.AddJobWithMeta("job-1", "Job", "description", cron.OnceSchedule{At: scheduled}, func() error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	store := cron.NewMemoryResultStore()
	a := &Agent{cronEngine: engine, cronResults: store, cronResultExecutions: map[string]string{}, cronResultPayloads: map[string]cronNotificationPayload{}}
	a.beginCronResultExecution(cron.Event{JobID: "job-1", Time: scheduled.Add(time.Minute), ScheduledAt: scheduled})
	a.recordCronDeliveryPayload(cronNotificationPayload{JobID: "job-1", Outcome: "succeeded", RawResult: "answer"})
	a.finishCronResultExecution(cron.Event{JobID: "job-1", Time: scheduled.Add(2 * time.Minute), ScheduledAt: scheduled}, &cronDeliveryError{err: errors.New("gateway unavailable")})
	got := store.ListExecutions("job-1")
	if len(got) != 1 || got[0].Status != cron.ExecutionSucceeded {
		t.Fatalf("delivery failure changed execution: %#v", got)
	}
}
