package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type PersistedState struct {
	Version       int            `json:"version"`
	EngineRunning bool           `json:"engine_running"`
	Jobs          []PersistedJob `json:"jobs"`
}

type PersistedJob struct {
	ID             string               `json:"id"`
	ScheduleText   string               `json:"schedule_text"`
	Command        string               `json:"command"`
	Mode           string               `json:"mode"`
	Paused         bool                 `json:"paused"`
	Status         string               `json:"status,omitempty"`
	RunCount       int                  `json:"run_count,omitempty"`
	ErrorCount     int                  `json:"error_count,omitempty"`
	LastError      string               `json:"last_error,omitempty"`
	CreatedAtUnix  int64                `json:"created_at_unix,omitempty"`
	UpdatedAtUnix  int64                `json:"updated_at_unix,omitempty"`
	LastRunUnix    int64                `json:"last_run_unix,omitempty"`
	NextRunUnix    int64                `json:"next_run_unix,omitempty"`
	DeleteAfterRun bool                 `json:"delete_after_run,omitempty"`
	Schedule       *PersistedSchedule   `json:"schedule,omitempty"`
	RunHistory     []PersistedRunRecord `json:"run_history,omitempty"`
	Metadata       map[string]string    `json:"metadata,omitempty"`
}

type PersistedSchedule struct {
	Kind    string `json:"kind,omitempty"`
	AtUnix  int64  `json:"at_unix,omitempty"`
	EveryMs int64  `json:"every_ms,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

type PersistedRunRecord struct {
	RunAtUnix  int64  `json:"run_at_unix"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Store struct {
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) Save(engine *Engine) error {
	if s == nil || engine == nil {
		return nil
	}

	jobs := engine.ListJobs()
	state := PersistedState{
		Version:       1,
		EngineRunning: engine.IsRunning(),
		Jobs:          make([]PersistedJob, 0, len(jobs)),
	}

	for _, job := range jobs {
		scheduleText := strings.TrimSpace(job.Metadata["schedule_text"])
		if scheduleText == "" {
			scheduleText = DescribeSchedule(job.Schedule)
		}
		command := strings.TrimSpace(job.Metadata["command"])
		mode := strings.TrimSpace(job.Metadata["mode"])
		if command == "" {
			continue
		}
		if mode == "" {
			mode = "shell"
		}
		state.Jobs = append(state.Jobs, PersistedJob{
			ID:             job.ID,
			ScheduleText:   scheduleText,
			Command:        command,
			Mode:           mode,
			Paused:         job.Status == StatusPaused,
			Status:         job.Status.String(),
			RunCount:       job.RunCount,
			ErrorCount:     job.ErrorCount,
			LastError:      job.LastError,
			CreatedAtUnix:  unixOrZero(job.CreatedAt),
			UpdatedAtUnix:  unixOrZero(job.UpdatedAt),
			LastRunUnix:    unixOrZero(job.LastRun),
			NextRunUnix:    unixOrZero(job.NextRun),
			DeleteAfterRun: job.DeleteAfterRun,
			Schedule:       serializeSchedule(job.Schedule),
			RunHistory:     serializeRunHistory(job.RunHistory),
			Metadata:       copyMetadata(job.Metadata),
		})
	}

	sort.Slice(state.Jobs, func(i, j int) bool {
		return state.Jobs[i].ID < state.Jobs[j].ID
	})

	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create cron store dir: %w", err)
	}
	if isMarkdownStorePath(s.path) {
		return s.writeMarkdown(state)
	}
	return s.writeJSON(state)
}

func (s *Store) Load(engine *Engine, taskBuilder func(job PersistedJob) (func() error, map[string]string, error)) (int, error) {
	if s == nil || engine == nil {
		return 0, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		} else {
			return 0, fmt.Errorf("read cron store: %w", err)
		}
	}

	var (
		state PersistedState
		err2  error
	)
	if isMarkdownStorePath(s.path) {
		state, err2 = parseMarkdownState(string(data))
		if err2 != nil {
			return 0, fmt.Errorf("parse mission store: %w", err2)
		}
	} else {
		if err2 = json.Unmarshal(data, &state); err2 != nil {
			return 0, fmt.Errorf("parse cron store: %w", err2)
		}
	}

	restored := 0
	loadedAt := time.Now()
	for _, pj := range state.Jobs {
		schedule, err := ParsePersistedScheduleJob(pj)
		if err != nil {
			return restored, fmt.Errorf("restore job %s: %w", pj.ID, err)
		}
		task, metadata, err := taskBuilder(pj)
		if err != nil {
			return restored, fmt.Errorf("restore job %s: %w", pj.ID, err)
		}
		if metadata == nil {
			metadata = make(map[string]string)
		}
		for k, v := range pj.Metadata {
			metadata[k] = v
		}
		if strings.TrimSpace(metadata["mode"]) == "" {
			metadata["mode"] = pj.Mode
		}
		metadata["command"] = pj.Command
		if strings.TrimSpace(pj.ScheduleText) != "" {
			metadata["schedule_text"] = pj.ScheduleText
		} else {
			metadata["schedule_text"] = DescribeSchedule(schedule)
		}

		if err := engine.AddJobWithMeta(pj.ID, "Cron: "+pj.ID, pj.Command, schedule, task, metadata); err != nil {
			return restored, fmt.Errorf("restore job %s: %w", pj.ID, err)
		}
		state := buildRestoredJobState(pj)
		state = normalizeRestoredJobState(schedule, state, loadedAt)
		if err := engine.RestoreJobState(pj.ID, state); err != nil {
			return restored, fmt.Errorf("restore state for job %s: %w", pj.ID, err)
		}
		restored++
	}

	if state.EngineRunning && restored > 0 {
		engine.Start()
	}
	return restored, nil
}

func copyMetadata(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func ParsePersistedSchedule(input string) (Schedule, error) {
	trimmed := strings.TrimSpace(input)
	schedule, err := ParseNaturalLanguage(trimmed)
	if err == nil {
		return schedule, nil
	}
	return ParseCronExpr(trimmed)
}

func ParsePersistedScheduleJob(job PersistedJob) (Schedule, error) {
	if job.Schedule != nil {
		switch job.Schedule.Kind {
		case "at":
			if job.Schedule.AtUnix > 0 {
				return OnceSchedule{At: time.UnixMilli(job.Schedule.AtUnix)}, nil
			}
		case "every":
			if job.Schedule.EveryMs > 0 {
				return IntervalSchedule{Interval: time.Duration(job.Schedule.EveryMs) * time.Millisecond}, nil
			}
		case "cron":
			if strings.TrimSpace(job.Schedule.Expr) != "" {
				return ParseCronExpr(job.Schedule.Expr)
			}
		}
	}
	if strings.TrimSpace(job.ScheduleText) != "" {
		return ParsePersistedSchedule(job.ScheduleText)
	}
	return nil, fmt.Errorf("missing schedule definition")
}

func isMarkdownStorePath(path string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(path)), ".md")
}

func (s *Store) writeJSON(state PersistedState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cron store: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("write cron store: %w", err)
	}
	return nil
}

func (s *Store) writeMarkdown(state PersistedState) error {
	var b strings.Builder
	b.WriteString("# LuckyHarness Mission Store\n\n")
	b.WriteString(fmt.Sprintf("engine_running: %t\n\n", state.EngineRunning))

	for _, job := range state.Jobs {
		b.WriteString(fmt.Sprintf("## job:%s\n", job.ID))
		b.WriteString(fmt.Sprintf("schedule: %s\n", job.ScheduleText))
		b.WriteString(fmt.Sprintf("mode: %s\n", job.Mode))
		b.WriteString(fmt.Sprintf("paused: %t\n", job.Paused))
		if job.Status != "" {
			b.WriteString(fmt.Sprintf("status: %s\n", job.Status))
		}
		if job.RunCount > 0 {
			b.WriteString(fmt.Sprintf("run_count: %d\n", job.RunCount))
		}
		if job.ErrorCount > 0 {
			b.WriteString(fmt.Sprintf("error_count: %d\n", job.ErrorCount))
		}
		if job.LastError != "" {
			b.WriteString(fmt.Sprintf("last_error: %s\n", job.LastError))
		}
		if job.CreatedAtUnix > 0 {
			b.WriteString(fmt.Sprintf("created_at_unix: %d\n", job.CreatedAtUnix))
		}
		if job.UpdatedAtUnix > 0 {
			b.WriteString(fmt.Sprintf("updated_at_unix: %d\n", job.UpdatedAtUnix))
		}
		if job.LastRunUnix > 0 {
			b.WriteString(fmt.Sprintf("last_run_unix: %d\n", job.LastRunUnix))
		}
		if job.NextRunUnix > 0 {
			b.WriteString(fmt.Sprintf("next_run_unix: %d\n", job.NextRunUnix))
		}
		if job.DeleteAfterRun {
			b.WriteString("delete_after_run: true\n")
		}
		if job.Schedule != nil {
			scheduleJSON, _ := json.Marshal(job.Schedule)
			b.WriteString(fmt.Sprintf("schedule_def: %s\n", string(scheduleJSON)))
		}
		if len(job.RunHistory) > 0 {
			historyJSON, _ := json.Marshal(job.RunHistory)
			b.WriteString(fmt.Sprintf("run_history: %s\n", string(historyJSON)))
		}
		b.WriteString("command: |\n")
		for _, line := range strings.Split(job.Command, "\n") {
			b.WriteString("  " + line + "\n")
		}
		metaJSON, _ := json.Marshal(job.Metadata)
		b.WriteString(fmt.Sprintf("metadata: %s\n\n", string(metaJSON)))
	}

	if err := os.WriteFile(s.path, []byte(b.String()), 0600); err != nil {
		return fmt.Errorf("write mission store: %w", err)
	}
	return nil
}

func parseMarkdownState(content string) (PersistedState, error) {
	state := PersistedState{
		Version: 1,
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		i++
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		if strings.HasPrefix(line, "engine_running:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "engine_running:"))
			state.EngineRunning = strings.EqualFold(v, "true")
			continue
		}
		if strings.HasPrefix(line, "## job:") {
			id := strings.TrimSpace(strings.TrimPrefix(line, "## job:"))
			if id == "" {
				continue
			}
			pj := PersistedJob{
				ID:       id,
				Mode:     "shell",
				Metadata: map[string]string{},
			}
			for i < len(lines) {
				raw := lines[i]
				trimmed := strings.TrimSpace(raw)
				if strings.HasPrefix(trimmed, "## job:") {
					break
				}
				i++
				if trimmed == "" {
					continue
				}
				switch {
				case strings.HasPrefix(trimmed, "schedule:"):
					pj.ScheduleText = strings.TrimSpace(strings.TrimPrefix(trimmed, "schedule:"))
				case strings.HasPrefix(trimmed, "mode:"):
					pj.Mode = strings.TrimSpace(strings.TrimPrefix(trimmed, "mode:"))
				case strings.HasPrefix(trimmed, "paused:"):
					pj.Paused = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "paused:")), "true")
				case strings.HasPrefix(trimmed, "status:"):
					pj.Status = strings.TrimSpace(strings.TrimPrefix(trimmed, "status:"))
				case strings.HasPrefix(trimmed, "run_count:"):
					_, _ = fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(trimmed, "run_count:")), "%d", &pj.RunCount)
				case strings.HasPrefix(trimmed, "error_count:"):
					_, _ = fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(trimmed, "error_count:")), "%d", &pj.ErrorCount)
				case strings.HasPrefix(trimmed, "last_error:"):
					pj.LastError = strings.TrimSpace(strings.TrimPrefix(trimmed, "last_error:"))
				case strings.HasPrefix(trimmed, "created_at_unix:"):
					_, _ = fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(trimmed, "created_at_unix:")), "%d", &pj.CreatedAtUnix)
				case strings.HasPrefix(trimmed, "updated_at_unix:"):
					_, _ = fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(trimmed, "updated_at_unix:")), "%d", &pj.UpdatedAtUnix)
				case strings.HasPrefix(trimmed, "last_run_unix:"):
					_, _ = fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(trimmed, "last_run_unix:")), "%d", &pj.LastRunUnix)
				case strings.HasPrefix(trimmed, "next_run_unix:"):
					_, _ = fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(trimmed, "next_run_unix:")), "%d", &pj.NextRunUnix)
				case strings.HasPrefix(trimmed, "delete_after_run:"):
					pj.DeleteAfterRun = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "delete_after_run:")), "true")
				case strings.HasPrefix(trimmed, "schedule_def:"):
					raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "schedule_def:"))
					if raw != "" && raw != "null" {
						var schedule PersistedSchedule
						if json.Unmarshal([]byte(raw), &schedule) == nil {
							pj.Schedule = &schedule
						}
					}
				case strings.HasPrefix(trimmed, "run_history:"):
					raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "run_history:"))
					if raw != "" && raw != "null" {
						_ = json.Unmarshal([]byte(raw), &pj.RunHistory)
					}
				case strings.HasPrefix(trimmed, "metadata:"):
					metaRaw := strings.TrimSpace(strings.TrimPrefix(trimmed, "metadata:"))
					if metaRaw != "" && metaRaw != "null" {
						_ = json.Unmarshal([]byte(metaRaw), &pj.Metadata)
					}
				case trimmed == "command: |":
					var cmdLines []string
					for i < len(lines) {
						if strings.HasPrefix(lines[i], "  ") {
							cmdLines = append(cmdLines, strings.TrimPrefix(lines[i], "  "))
							i++
							continue
						}
						break
					}
					pj.Command = strings.Join(cmdLines, "\n")
				}
			}
			if pj.Schedule == nil && strings.TrimSpace(pj.ScheduleText) == "" {
				continue
			}
			if strings.TrimSpace(pj.Command) == "" {
				continue
			}
			if pj.Metadata == nil {
				pj.Metadata = map[string]string{}
			}
			state.Jobs = append(state.Jobs, pj)
		}
	}
	return state, nil
}

func serializeSchedule(schedule Schedule) *PersistedSchedule {
	switch s := schedule.(type) {
	case IntervalSchedule:
		return &PersistedSchedule{Kind: "every", EveryMs: s.Interval.Milliseconds()}
	case *IntervalSchedule:
		return &PersistedSchedule{Kind: "every", EveryMs: s.Interval.Milliseconds()}
	case OnceSchedule:
		return &PersistedSchedule{Kind: "at", AtUnix: s.At.UnixMilli()}
	case *OnceSchedule:
		return &PersistedSchedule{Kind: "at", AtUnix: s.At.UnixMilli()}
	case CronSchedule:
		return &PersistedSchedule{Kind: "cron", Expr: cronExprString(s)}
	case *CronSchedule:
		return &PersistedSchedule{Kind: "cron", Expr: cronExprString(*s)}
	case DailySchedule:
		return &PersistedSchedule{Kind: "cron", Expr: fmt.Sprintf("%d %d * * *", s.Minute, s.Hour)}
	case *DailySchedule:
		return &PersistedSchedule{Kind: "cron", Expr: fmt.Sprintf("%d %d * * *", s.Minute, s.Hour)}
	default:
		return nil
	}
}

func serializeRunHistory(history []RunRecord) []PersistedRunRecord {
	if len(history) == 0 {
		return nil
	}
	out := make([]PersistedRunRecord, 0, len(history))
	for _, item := range history {
		out = append(out, PersistedRunRecord{
			RunAtUnix:  unixOrZero(item.RunAt),
			Status:     item.Status,
			DurationMs: item.Duration.Milliseconds(),
			Error:      item.Error,
		})
	}
	return out
}

func buildRestoredJobState(job PersistedJob) RestoredJobState {
	return RestoredJobState{
		Status:         job.Status,
		Paused:         job.Paused,
		LastRun:        unixPtr(job.LastRunUnix),
		NextRun:        unixPtr(job.NextRunUnix),
		RunCount:       job.RunCount,
		ErrorCount:     job.ErrorCount,
		LastError:      job.LastError,
		CreatedAt:      unixPtr(job.CreatedAtUnix),
		UpdatedAt:      unixPtr(job.UpdatedAtUnix),
		RunHistory:     restoreRunHistory(job.RunHistory),
		DeleteAfterRun: job.DeleteAfterRun,
	}
}

func normalizeRestoredJobState(schedule Schedule, state RestoredJobState, now time.Time) RestoredJobState {
	if schedule == nil || state.Paused || state.NextRun == nil || state.NextRun.After(now) {
		return state
	}
	next := schedule.Next(now)
	if next.IsZero() {
		state.NextRun = nil
		return state
	}
	state.NextRun = &next
	return state
}

func restoreRunHistory(history []PersistedRunRecord) []RunRecord {
	if len(history) == 0 {
		return nil
	}
	out := make([]RunRecord, 0, len(history))
	for _, item := range history {
		out = append(out, RunRecord{
			RunAt:    time.UnixMilli(item.RunAtUnix),
			Status:   item.Status,
			Duration: time.Duration(item.DurationMs) * time.Millisecond,
			Error:    item.Error,
		})
	}
	return out
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func unixPtr(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms)
	return &t
}

func cronExprString(s CronSchedule) string {
	return fmt.Sprintf("%s %s %s %s %s",
		fieldString(s.Minute),
		fieldString(s.Hour),
		fieldString(s.Day),
		fieldString(s.Month),
		fieldString(s.Weekday),
	)
}

func cronExprDisplay(s CronSchedule) string {
	return cronExprString(s)
}

func intervalDisplay(interval time.Duration) string {
	if interval > 0 && interval%time.Hour == 0 {
		hours := int(interval / time.Hour)
		if hours == 1 {
			return "每小时"
		}
		return fmt.Sprintf("每%d小时", hours)
	}
	if interval > 0 && interval%time.Minute == 0 {
		minutes := int(interval / time.Minute)
		if minutes == 1 {
			return "每分钟"
		}
		return fmt.Sprintf("每%d分钟", minutes)
	}
	return fmt.Sprintf("every %s", interval)
}

func fieldString(values []int) string {
	if len(values) == 0 {
		return "*"
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%d", v))
	}
	return strings.Join(parts, ",")
}
