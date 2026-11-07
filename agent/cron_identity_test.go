package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/cron"
)

func TestAddCronJobSameIntentReturnsExistingJob(t *testing.T) {
	engine := cron.NewEngine()
	defer engine.Stop()
	a := &Agent{cronEngine: engine}
	schedule := cron.DailySchedule{Hour: 3, Minute: 5}
	meta := map[string]string{
		"title":           "把 terminal 工具说明写入 Obsidian",
		"intent":          "把 terminal 工具说明写入 Obsidian",
		"desired_outcome": "Obsidian 里有一份 terminal 工具说明",
		"created_by":      "sess-note",
	}
	ids := []string{"terminal-note-extract-code", "terminal-note-write-final-2", "terminal-note-write-final-b"}
	for i, id := range ids {
		err := a.AddCronJob(id, meta["title"], meta["intent"], "shell", "obsidian write terminal", meta, schedule)
		if i == 0 {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var existing *cronExistingJob
		if !errors.As(err, &existing) || existing.job == nil || existing.job.ID != ids[0] {
			t.Fatalf("add %d err = %v", i, err)
		}
	}
	if engine.JobCount() != 1 {
		t.Fatalf("jobs = %d, want 1", engine.JobCount())
	}
	job, ok := engine.GetJob(ids[0])
	if !ok {
		t.Fatal("missing original job")
	}
	for _, key := range []string{"title", "intent", "schedule_kind", "created_by", "desired_outcome", "idempotency_key"} {
		if strings.TrimSpace(job.Metadata[key]) == "" {
			t.Fatalf("metadata %s is empty: %+v", key, job.Metadata)
		}
	}
	if job.Metadata["schedule_kind"] != cronScheduleKindRecurring {
		t.Fatalf("schedule_kind = %s", job.Metadata["schedule_kind"])
	}
	if job.Metadata["idempotency_key"] != job.Metadata["intent_key"] {
		t.Fatalf("keys diverged: %+v", job.Metadata)
	}
}

func TestOneShotFailuresStopAtLimitWithoutNewSchedule(t *testing.T) {
	engine := cron.NewEngine()
	defer engine.Stop()
	a := &Agent{cronEngine: engine}
	a.installCronEventHandler()
	err := a.AddCronJob("write-once", "写一份笔记", "写一份笔记", "shell", "printf note", map[string]string{
		"schedule_kind": cronScheduleKindOneShot,
	}, cron.OnceSchedule{At: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	original, _ := engine.GetJob("write-once")
	originalNext := original.NextRun
	originalID := original.ID

	for attempt := 1; attempt <= cronOneShotRetryLimit; attempt++ {
		a.recordCronRun(cron.Event{JobID: originalID}, &cronTaskError{err: errors.New("shell command exited with non-zero status")})
		job, ok := engine.GetJob(originalID)
		if !ok || job.ID != originalID {
			t.Fatalf("attempt %d changed identity", attempt)
		}
		if attempt < cronOneShotRetryLimit {
			limit := time.Now().Add(cronOneShotBackoff(attempt) + 5*time.Second)
			if job.Status == cron.StatusFailed || job.NextRun.IsZero() || job.NextRun.After(limit) || job.NextRun.Equal(originalNext) {
				t.Fatalf("attempt %d did not schedule a bounded retry: %+v", attempt, job)
			}
			continue
		}
		if job.Status != cron.StatusFailed || !job.NextRun.IsZero() {
			t.Fatalf("exhausted job = status %s next %s", job.Status, job.NextRun)
		}
		if job.Metadata[cronMetaLastErrorClass] != cronErrorClassExhausted {
			t.Fatalf("last_error_class = %s", job.Metadata[cronMetaLastErrorClass])
		}
	}
	if engine.JobCount() != 1 {
		t.Fatalf("retries created jobs: %d", engine.JobCount())
	}
}

func TestDeliveryFailureDoesNotFailTheTask(t *testing.T) {
	engine := cron.NewEngine()
	defer engine.Stop()
	a := &Agent{cronEngine: engine}
	if err := a.AddCronJob("deliver", "晨报", "发送晨报", "agent", "总结昨日", nil, cron.DailySchedule{Hour: 9, Minute: 0}); err != nil {
		t.Fatal(err)
	}
	a.recordCronRun(cron.Event{JobID: "deliver"}, &cronDeliveryError{err: errors.New(`gateway "qqofficial" is not registered`)})
	job, _ := engine.GetJob("deliver")
	if job.Status != cron.StatusIdle {
		t.Fatalf("status = %s, want idle", job.Status)
	}
	if job.Metadata[cronMetaLastErrorClass] != cronErrorClassDelivery || job.Metadata[cronMetaDeliveryStatus] != cronDeliveryUndelivered {
		t.Fatalf("delivery metadata = %+v", job.Metadata)
	}
	if job.ErrorCount != 0 || job.LastError != "" {
		t.Fatalf("task recorded as failed: errors=%d last=%q", job.ErrorCount, job.LastError)
	}
}

func TestDefaultListCollapsesPausedDrafts(t *testing.T) {
	engine := cron.NewEngine()
	defer engine.Stop()
	a := &Agent{cronEngine: engine, tools: tool.NewRegistry()}
	recurring := []struct{ id, title string }{
		{"daily-mail", "晨间邮件"},
		{"daily-backup", "每晚备份"},
		{"daily-digest", "日报"},
	}
	for _, item := range recurring {
		if err := a.AddCronJob(item.id, item.title, item.title, "shell", item.id, nil, cron.DailySchedule{Hour: 9, Minute: 0}); err != nil {
			t.Fatal(err)
		}
	}
	intent := "把 terminal 工具说明写入 Obsidian"
	_, prepared := prepareCronIdentity("terminal-note", intent, intent, "shell", "obsidian write terminal", map[string]string{
		cronMetaScheduleKind: cronScheduleKindOneShot,
	}, cron.OnceSchedule{At: time.Now().Add(time.Hour)})
	key := prepared[cronMetaIntentKey]
	for i := 0; i < 19; i++ {
		// These drafts already exist from before intent dedupe. Loading them
		// through the engine matches a restored store.
		id := "terminal-note-" + string(rune('a'+i))
		meta := map[string]string{}
		for k, v := range prepared {
			meta[k] = v
		}
		if err := engine.AddJobWithMeta(id, intent, intent, cron.OnceSchedule{At: time.Now().Add(time.Duration(i+1) * time.Minute)}, func() error { return nil }, meta); err != nil {
			t.Fatal(err)
		}
		if err := engine.PauseJob(id); err != nil {
			t.Fatal(err)
		}
	}
	a.quiescePausedCronJobs()

	base := func(map[string]any) (string, error) {
		return `{"ok":true,"action":"list","jobs":[]}`, nil
	}
	out, err := a.handleCronToolList(base, map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		RecurringCount int               `json:"recurring_count"`
		DraftGroups    int               `json:"paused_draft_groups"`
		DraftJobs      int               `json:"paused_draft_jobs"`
		Drafts         []cronPausedDraft `json:"paused_drafts"`
		Jobs           []cronListItem    `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RecurringCount != 3 || len(payload.Jobs) != 3 {
		t.Fatalf("visible recurring = count %d jobs %d", payload.RecurringCount, len(payload.Jobs))
	}
	if payload.DraftGroups != 1 || payload.DraftJobs != 19 || len(payload.Drafts) != 1 || payload.Drafts[0].Count != 19 {
		t.Fatalf("drafts = %+v groups=%d jobs=%d", payload.Drafts, payload.DraftGroups, payload.DraftJobs)
	}
	for _, job := range engine.ListJobs() {
		if job.Status == cron.StatusPaused && !job.NextRun.IsZero() {
			t.Fatalf("paused job %s still due at %s", job.ID, job.NextRun)
		}
	}

	archived, err := a.handleCronToolArchive(map[string]any{"action": "archive", "intent_key": key})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(archived, `"count":19`) {
		t.Fatalf("archive = %s", archived)
	}
	out, err = a.handleCronToolList(base, map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DraftGroups != 0 || payload.DraftJobs != 0 || payload.RecurringCount != 3 {
		t.Fatalf("after archive recurring=%d drafts=%d", payload.RecurringCount, payload.DraftJobs)
	}
}

func TestCronToolAddDedupesGeneratedIDs(t *testing.T) {
	engine := cron.NewEngine()
	defer engine.Stop()
	adds := 0
	base := func(args map[string]any) (string, error) {
		adds++
		id, _ := args["id"].(string)
		if _, exists := engine.GetJob(id); exists {
			return "", errors.New("job exists")
		}
		meta := map[string]string{"mode": "shell", "command": "echo hi"}
		if err := engine.AddJobWithMeta(id, "Cron: "+id, "echo hi", cron.DailySchedule{Hour: 9, Minute: 0}, func() error { return nil }, meta); err != nil {
			return "", err
		}
		raw, _ := json.Marshal(map[string]any{"ok": true, "action": "add", "id": id})
		return string(raw), nil
	}
	a := &Agent{cronEngine: engine}
	args := map[string]any{
		"action":   "add",
		"schedule": "每天9点",
		"mode":     "shell",
		"command":  "echo hi",
		"intent":   "每天打一声招呼",
	}
	var firstID string
	for i := 0; i < 3; i++ {
		out, err := a.handleCronToolAdd(base, cloneCronArgs(args))
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstID, _ = payload["id"].(string)
			if !strings.HasPrefix(firstID, "intent-") {
				t.Fatalf("id = %s", firstID)
			}
			continue
		}
		if payload["existing"] != true || payload["id"] != firstID {
			t.Fatalf("add %d = %+v", i, payload)
		}
	}
	if adds != 1 || engine.JobCount() != 1 {
		t.Fatalf("adds=%d jobs=%d", adds, engine.JobCount())
	}
}

func cloneCronArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}
