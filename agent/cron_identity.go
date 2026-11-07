package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/yurika0211/luckyagent-tools/cron"
)

const (
	cronMetaTitle           = "title"
	cronMetaIntent          = "intent"
	cronMetaIntentKey       = "intent_key"
	cronMetaScheduleKind    = "schedule_kind"
	cronMetaCreatedBy       = "created_by"
	cronMetaDesiredOutcome  = "desired_outcome"
	cronMetaIdempotencyKey  = "idempotency_key"
	cronMetaLastErrorClass  = "last_error_class"
	cronMetaDeliveryStatus  = "delivery_status"
	cronMetaDeliveryError   = "delivery_error"
	cronMetaArchived        = "archived"
	cronMetaRetryCount      = "retry_count"
	cronMetaScheduleDisplay = "schedule_text"

	cronScheduleKindOneShot   = "one_shot"
	cronScheduleKindRecurring = "recurring"

	cronErrorClassNone      = ""
	cronErrorClassCommand   = "command"
	cronErrorClassDelivery  = "delivery"
	cronErrorClassExhausted = "retry_exhausted"

	cronDeliveryDelivered   = "delivered"
	cronDeliveryUndelivered = "undelivered"
	cronDeliverySkipped     = "skipped"

	// One-shot work retries a bounded number of times, then stays failed.
	// The original schedule is not rewritten into another long-lived job.
	cronOneShotRetryLimit = 3
	cronOneShotRetryBase  = time.Minute
)

// cronTaskError marks a failure of the scheduled command or agent turn.
// Delivery problems use cronDeliveryError so a missing gateway cannot be
// stored as another failure of the task itself.
type cronTaskError struct{ err error }

func (e *cronTaskError) Error() string {
	if e == nil || e.err == nil {
		return "cron task failed"
	}
	return e.err.Error()
}

func (e *cronTaskError) Unwrap() error { return e.err }

type cronDeliveryError struct{ err error }

func (e *cronDeliveryError) Error() string {
	if e == nil || e.err == nil {
		return "deliver cron result"
	}
	return "deliver cron result: " + e.err.Error()
}

func (e *cronDeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// cronExistingJob is returned when add hits an intent that already has a job.
// Callers keep the existing id instead of minting terminal-note-write-final-2.
type cronExistingJob struct{ job *cron.Job }

func (e *cronExistingJob) Error() string {
	id := ""
	if e != nil && e.job != nil {
		id = e.job.ID
	}
	return fmt.Sprintf("cron intent already registered as %s", id)
}

func prepareCronIdentity(id, name, description, mode, command string, metadata map[string]string, schedule cron.Schedule) (string, map[string]string) {
	meta := map[string]string{}
	for key, value := range metadata {
		meta[key] = strings.TrimSpace(value)
	}
	meta["mode"] = mode
	meta["command"] = command

	title := firstNonEmpty(meta[cronMetaTitle], strings.TrimSpace(name), "Cron: "+id)
	intent := firstNonEmpty(meta[cronMetaIntent], strings.TrimSpace(description), command)
	outcome := firstNonEmpty(meta[cronMetaDesiredOutcome], intent)
	createdBy := firstNonEmpty(meta[cronMetaCreatedBy], meta["session_id"], "user")
	kind := cronScheduleKind(schedule, meta)
	key := firstNonEmpty(meta[cronMetaIdempotencyKey], meta[cronMetaIntentKey])
	if key == "" {
		key = cronIntentKey(mode, command, kind, intent)
	}

	meta[cronMetaTitle] = title
	meta[cronMetaIntent] = intent
	meta[cronMetaDesiredOutcome] = outcome
	meta[cronMetaCreatedBy] = createdBy
	meta[cronMetaScheduleKind] = kind
	meta[cronMetaIdempotencyKey] = key
	meta[cronMetaIntentKey] = key
	if _, ok := meta[cronMetaLastErrorClass]; !ok {
		meta[cronMetaLastErrorClass] = cronErrorClassNone
	}
	return key, meta
}

func cronScheduleKind(schedule cron.Schedule, metadata map[string]string) string {
	switch strings.ToLower(strings.TrimSpace(metadata[cronMetaScheduleKind])) {
	case cronScheduleKindOneShot, "once", "one-shot", "oneshot":
		return cronScheduleKindOneShot
	case cronScheduleKindRecurring, "cron":
		return cronScheduleKindRecurring
	}
	if schedule == nil {
		return cronScheduleKindRecurring
	}
	switch schedule.(type) {
	case cron.OnceSchedule, *cron.OnceSchedule:
		return cronScheduleKindOneShot
	case cron.IntervalSchedule, *cron.IntervalSchedule, cron.DailySchedule, *cron.DailySchedule, cron.CronSchedule, *cron.CronSchedule:
		return cronScheduleKindRecurring
	default:
		// Unknown schedules are recurring unless the caller says otherwise.
		// Probing Next would consume a one-shot schedule before the engine
		// records its first run.
		return cronScheduleKindRecurring
	}
}

func cronIntentKey(mode, command, kind, intent string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.ToLower(strings.TrimSpace(mode)),
		normalizeCronIntentText(command),
		kind,
		normalizeCronIntentText(intent),
	}, "\n")))
	return hex.EncodeToString(sum[:8])
}

func normalizeCronIntentText(text string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func findCronJobByIntent(jobs []*cron.Job, key string) *cron.Job {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	var found *cron.Job
	for _, job := range jobs {
		if job == nil || cronJobArchived(job) {
			continue
		}
		if cronJobIntentKey(job) != key {
			continue
		}
		if found == nil || job.CreatedAt.Before(found.CreatedAt) {
			found = job
		}
	}
	return found
}

func cronJobIntentKey(job *cron.Job) string {
	if job == nil || job.Metadata == nil {
		return ""
	}
	return firstNonEmpty(job.Metadata[cronMetaIntentKey], job.Metadata[cronMetaIdempotencyKey])
}

func cronJobArchived(job *cron.Job) bool {
	return job != nil && metadataBool(job.Metadata, cronMetaArchived)
}

// cronListView is the default task list: long-lived jobs stay visible, and
// paused one-shot drafts collapse under the intent they already share.
type cronListView struct {
	Recurring      []cronListItem    `json:"recurring"`
	OneShot        []cronListItem    `json:"one_shot"`
	PausedDrafts   []cronPausedDraft `json:"paused_drafts"`
	RecurringCount int               `json:"recurring_count"`
	OneShotCount   int               `json:"one_shot_count"`
	DraftGroups    int               `json:"paused_draft_groups"`
	DraftJobs      int               `json:"paused_draft_jobs"`
}

type cronListItem struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Intent         string `json:"intent"`
	ScheduleKind   string `json:"schedule_kind"`
	CreatedBy      string `json:"created_by"`
	DesiredOutcome string `json:"desired_outcome"`
	IdempotencyKey string `json:"idempotency_key"`
	LastErrorClass string `json:"last_error_class"`
	Status         string `json:"status"`
	NextRun        string `json:"next_run,omitempty"`
	DeliveryStatus string `json:"delivery_status,omitempty"`
	Command        string `json:"command,omitempty"`
}

type cronPausedDraft struct {
	IntentKey string   `json:"intent_key"`
	Title     string   `json:"title"`
	Intent    string   `json:"intent"`
	Count     int      `json:"count"`
	IDs       []string `json:"ids"`
	Status    string   `json:"status"`
}

func buildCronListView(jobs []*cron.Job) cronListView {
	view := cronListView{}
	drafts := map[string]*cronPausedDraft{}
	for _, job := range jobs {
		if job == nil || cronJobArchived(job) {
			continue
		}
		if job.Status == cron.StatusPaused && cronScheduleKind(job.Schedule, job.Metadata) == cronScheduleKindOneShot {
			key := firstNonEmpty(cronJobIntentKey(job), job.ID)
			group := drafts[key]
			if group == nil {
				group = &cronPausedDraft{
					IntentKey: key,
					Title:     cronMetaOr(job, cronMetaTitle, job.Name),
					Intent:    cronMetaOr(job, cronMetaIntent, job.Description),
					Status:    "paused",
				}
				drafts[key] = group
			}
			group.Count++
			group.IDs = append(group.IDs, job.ID)
			view.DraftJobs++
			continue
		}
		item := cronListItemFromJob(job)
		if item.ScheduleKind == cronScheduleKindRecurring {
			view.Recurring = append(view.Recurring, item)
			continue
		}
		view.OneShot = append(view.OneShot, item)
	}
	view.PausedDrafts = make([]cronPausedDraft, 0, len(drafts))
	for _, group := range drafts {
		sort.Strings(group.IDs)
		view.PausedDrafts = append(view.PausedDrafts, *group)
	}
	sort.Slice(view.PausedDrafts, func(i, j int) bool {
		return view.PausedDrafts[i].IntentKey < view.PausedDrafts[j].IntentKey
	})
	sort.Slice(view.Recurring, func(i, j int) bool { return view.Recurring[i].ID < view.Recurring[j].ID })
	sort.Slice(view.OneShot, func(i, j int) bool { return view.OneShot[i].ID < view.OneShot[j].ID })
	view.RecurringCount = len(view.Recurring)
	view.OneShotCount = len(view.OneShot)
	view.DraftGroups = len(view.PausedDrafts)
	return view
}

func cronListItemFromJob(job *cron.Job) cronListItem {
	item := cronListItem{
		ID:             job.ID,
		Title:          cronMetaOr(job, cronMetaTitle, job.Name),
		Intent:         cronMetaOr(job, cronMetaIntent, job.Description),
		ScheduleKind:   cronScheduleKind(job.Schedule, job.Metadata),
		CreatedBy:      cronMetaOr(job, cronMetaCreatedBy, ""),
		DesiredOutcome: cronMetaOr(job, cronMetaDesiredOutcome, ""),
		IdempotencyKey: cronJobIntentKey(job),
		LastErrorClass: cronMetaOr(job, cronMetaLastErrorClass, ""),
		Status:         job.Status.String(),
		DeliveryStatus: cronMetaOr(job, cronMetaDeliveryStatus, ""),
		Command:        cronMetaOr(job, "command", ""),
	}
	if !job.NextRun.IsZero() && job.Status != cron.StatusPaused && job.Status != cron.StatusFailed && job.Status != cron.StatusDone {
		item.NextRun = job.NextRun.Format(time.RFC3339)
	}
	return item
}

func cronMetaOr(job *cron.Job, key, fallback string) string {
	if job != nil && job.Metadata != nil {
		if value := strings.TrimSpace(job.Metadata[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(fallback)
}

// archiveCronIntent marks every job that shares key. ListJobs returns a shallow
// copy, and its Metadata map is the one the engine persists.
func archiveCronIntent(jobs []*cron.Job, key string) []string {
	key = strings.TrimSpace(key)
	var ids []string
	for _, job := range jobs {
		if job == nil || job.Metadata == nil || cronJobArchived(job) {
			continue
		}
		if cronJobIntentKey(job) != key {
			continue
		}
		job.Metadata[cronMetaArchived] = "true"
		ids = append(ids, job.ID)
	}
	sort.Strings(ids)
	return ids
}
