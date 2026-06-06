package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cronpkg "github.com/yurika0211/luckyharness/internal/cron"
)

// CronTaskFactory builds a runnable task for a cron job.
type CronTaskFactory func(id, mode, command string, metadata map[string]string) func() error

// CronSaveFunc persists cron jobs after mutation.
type CronSaveFunc func() error

// CronToolService implements cron_* handlers in the tool layer.
type CronToolService struct {
	engine    *cronpkg.Engine
	save      CronSaveFunc
	buildTask CronTaskFactory
}

// NewCronToolService creates a cron tool service.
func NewCronToolService(engine *cronpkg.Engine, save CronSaveFunc, buildTask CronTaskFactory) *CronToolService {
	return &CronToolService{
		engine:    engine,
		save:      save,
		buildTask: buildTask,
	}
}

// RegisterTools registers cron-related tools onto the registry.
func (s *CronToolService) RegisterTools(r *Registry) {
	if s == nil || r == nil {
		return
	}

	r.Register(&Tool{
		Name:        "cron",
		Description: "Unified cron management tool. Use action=list|status|add|remove|pause|resume to manage scheduled jobs through one high-level interface.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"action":              {Type: "string", Description: "Action: list, status, add, remove, pause, resume", Required: true},
			"id":                  {Type: "string", Description: "Job ID for add/remove/pause/resume", Required: false},
			"schedule":            {Type: "string", Description: "Natural language schedule or 5-field cron expression", Required: false},
			"mode":                {Type: "string", Description: "Execution mode: shell or agent", Required: false, Default: "shell"},
			"command":             {Type: "string", Description: "Shell command or agent prompt", Required: false},
			"platform":            {Type: "string", Description: "Optional notification platform", Required: false},
			"chat_id":             {Type: "string", Description: "Optional target chat ID for notification delivery", Required: false},
			"reply_to_message_id": {Type: "string", Description: "Optional reply target message ID", Required: false},
			"session_id":          {Type: "string", Description: "Optional existing session ID to use as agent-mode context. Omit to run cron out of chat sessions.", Required: false},
		},
		Handler: s.HandleCron,
	})
	r.Register(&Tool{
		Name:        "cron_add",
		Description: "Add a scheduled job. Accepts natural language schedules like 每天9点, 每30分钟, 工作日18点, 明天10点, or a 5-field cron expression like 0 9 * * *. Mode can be shell or agent.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"id":                  {Type: "string", Description: "Optional job ID. Auto-generated when omitted.", Required: false},
			"schedule":            {Type: "string", Description: "Natural language schedule or 5-field cron expression", Required: true},
			"mode":                {Type: "string", Description: "Execution mode: shell or agent", Required: false, Default: "shell"},
			"command":             {Type: "string", Description: "Shell command to run, or agent prompt when mode=agent", Required: true},
			"platform":            {Type: "string", Description: "Optional notification platform, e.g. telegram", Required: false},
			"chat_id":             {Type: "string", Description: "Optional target chat ID for notification delivery", Required: false},
			"reply_to_message_id": {Type: "string", Description: "Optional reply target message ID", Required: false},
			"session_id":          {Type: "string", Description: "Optional existing session ID to use as agent-mode context. Omit to run cron out of chat sessions.", Required: false},
		},
		Handler: s.HandleAdd,
	})
	r.Register(&Tool{
		Name:        "cron_list",
		Description: "List all scheduled jobs and their current status.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters:  map[string]Param{},
		Handler:     s.HandleList,
	})
	r.Register(&Tool{
		Name:        "cron_remove",
		Description: "Remove a scheduled job by ID.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"id": {Type: "string", Description: "Job ID", Required: true},
		},
		Handler: s.HandleRemove,
	})
	r.Register(&Tool{
		Name:        "cron_pause",
		Description: "Pause a scheduled job by ID.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"id": {Type: "string", Description: "Job ID", Required: true},
		},
		Handler: s.HandlePause,
	})
	r.Register(&Tool{
		Name:        "cron_resume",
		Description: "Resume a paused scheduled job by ID.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"id": {Type: "string", Description: "Job ID", Required: true},
		},
		Handler: s.HandleResume,
	})
	r.Register(&Tool{
		Name:        "cron_status",
		Description: "Get cron engine running status and job counts.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters:  map[string]Param{},
		Handler:     s.HandleStatus,
	})
}

func (s *CronToolService) HandleCron(args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "list":
		return s.HandleList(args)
	case "status":
		return s.HandleStatus(args)
	case "add":
		return s.HandleAdd(args)
	case "remove":
		return s.HandleRemove(args)
	case "pause":
		return s.HandlePause(args)
	case "resume":
		return s.HandleResume(args)
	default:
		return "", fmt.Errorf("invalid cron action %q (use list, status, add, remove, pause, resume)", action)
	}
}

func (s *CronToolService) HandleAdd(args map[string]any) (string, error) {
	if s == nil || s.engine == nil || s.buildTask == nil {
		return "", fmt.Errorf("cron service not initialized")
	}
	id, _ := args["id"].(string)
	scheduleText, _ := args["schedule"].(string)
	schedule, err := parseCronSchedule(scheduleText)
	if err != nil {
		return "", fmt.Errorf("parse schedule: %w", err)
	}

	modeText := "shell"
	if mode, ok := args["mode"].(string); ok && strings.TrimSpace(mode) != "" {
		modeText = strings.ToLower(strings.TrimSpace(mode))
	}
	command, _ := args["command"].(string)
	if strings.TrimSpace(command) == "" {
		if legacy, ok := args["task"].(string); ok && strings.TrimSpace(legacy) != "" {
			command = legacy
		}
	}
	if strings.TrimSpace(command) == "" {
		if legacy, ok := args["prompt"].(string); ok && strings.TrimSpace(legacy) != "" {
			command = legacy
		}
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("command is required (task/prompt are accepted as legacy aliases)")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = buildCronJobID(modeText, command)
	}

	mode := normalizeCronTaskMode(modeText)
	meta := map[string]string{
		"mode":          mode,
		"command":       command,
		"schedule_text": scheduleText,
	}
	if sessionID, ok := args["session_id"].(string); ok && strings.TrimSpace(sessionID) != "" {
		meta["session_id"] = strings.TrimSpace(sessionID)
	}
	if platform, ok := args["platform"].(string); ok && strings.TrimSpace(platform) != "" {
		meta["platform"] = strings.TrimSpace(platform)
	}
	if chatID, ok := args["chat_id"].(string); ok && strings.TrimSpace(chatID) != "" {
		meta["chatID"] = strings.TrimSpace(chatID)
	}
	if replyTo, ok := args["reply_to_message_id"].(string); ok && strings.TrimSpace(replyTo) != "" {
		meta["replyToMsgID"] = strings.TrimSpace(replyTo)
	}
	task := s.buildTask(id, mode, command, meta)
	if err := s.engine.AddJobWithMeta(id, "Cron: "+id, command, schedule, task, meta); err != nil {
		return "", err
	}
	if !s.engine.IsRunning() {
		s.engine.Start()
	}
	if s.save != nil {
		if err := s.save(); err != nil {
			return "", err
		}
	}

	result, _ := json.Marshal(map[string]any{
		"id":       id,
		"schedule": schedule.String(),
		"mode":     mode,
		"command":  command,
		"running":  s.engine.IsRunning(),
		"message":  fmt.Sprintf("Scheduled job %s added", id),
	})
	return string(result), nil
}

func (s *CronToolService) HandleList(args map[string]any) (string, error) {
	if s == nil || s.engine == nil {
		return "", fmt.Errorf("cron service not initialized")
	}
	jobs := s.engine.ListJobs()
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, map[string]any{
			"id":            job.ID,
			"schedule":      job.Schedule.String(),
			"status":        job.Status.String(),
			"next_run":      job.NextRun,
			"last_run":      job.LastRun,
			"run_count":     job.RunCount,
			"error_count":   job.ErrorCount,
			"mode":          job.Metadata["mode"],
			"command":       job.Metadata["command"],
			"schedule_text": cronScheduleText(job),
		})
	}
	result, _ := json.Marshal(map[string]any{
		"running": s.engine.IsRunning(),
		"total":   len(items),
		"jobs":    items,
	})
	return string(result), nil
}

func (s *CronToolService) HandleRemove(args map[string]any) (string, error) {
	if s == nil || s.engine == nil {
		return "", fmt.Errorf("cron service not initialized")
	}
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.engine.RemoveJob(id); err != nil {
		return "", err
	}
	if s.save != nil {
		if err := s.save(); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf(`{"id":"%s","message":"removed"}`, id), nil
}

func (s *CronToolService) HandlePause(args map[string]any) (string, error) {
	if s == nil || s.engine == nil {
		return "", fmt.Errorf("cron service not initialized")
	}
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.engine.PauseJob(id); err != nil {
		return "", err
	}
	if s.save != nil {
		if err := s.save(); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf(`{"id":"%s","message":"paused"}`, id), nil
}

func (s *CronToolService) HandleResume(args map[string]any) (string, error) {
	if s == nil || s.engine == nil {
		return "", fmt.Errorf("cron service not initialized")
	}
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.engine.ResumeJob(id); err != nil {
		return "", err
	}
	if s.save != nil {
		if err := s.save(); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf(`{"id":"%s","message":"resumed"}`, id), nil
}

func (s *CronToolService) HandleStatus(args map[string]any) (string, error) {
	if s == nil || s.engine == nil {
		return "", fmt.Errorf("cron service not initialized")
	}
	jobs := s.engine.ListJobs()
	paused, running, failed := 0, 0, 0
	for _, job := range jobs {
		switch job.Status {
		case cronpkg.StatusPaused:
			paused++
		case cronpkg.StatusRunning:
			running++
		case cronpkg.StatusFailed:
			failed++
		}
	}
	result, _ := json.Marshal(map[string]any{
		"running":     s.engine.IsRunning(),
		"job_count":   len(jobs),
		"paused_jobs": paused,
		"active_jobs": running,
		"failed_jobs": failed,
	})
	return string(result), nil
}

func parseCronSchedule(input string) (cronpkg.Schedule, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, fmt.Errorf("schedule is required")
	}
	schedule, err := cronpkg.ParseNaturalLanguage(trimmed)
	if err == nil {
		return schedule, nil
	}
	return cronpkg.ParseCronExpr(trimmed)
}

func normalizeCronTaskMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "agent":
		return "agent"
	default:
		return "shell"
	}
}

func buildCronJobID(mode, command string) string {
	base := strings.ToLower(strings.TrimSpace(mode + "-" + command))
	base = strings.ReplaceAll(base, "_", "-")
	base = strings.ReplaceAll(base, " ", "-")

	var b strings.Builder
	lastDash := false
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}

	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "cron-job"
	}
	if len(id) > 48 {
		id = strings.Trim(id[:48], "-")
	}
	return fmt.Sprintf("%s-%d", id, time.Now().Unix())
}

func cronScheduleText(job *cronpkg.Job) string {
	if job == nil {
		return ""
	}
	if text := strings.TrimSpace(job.Metadata["schedule_text"]); text != "" {
		return text
	}
	return cronpkg.DescribeSchedule(job.Schedule)
}
