package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cronpkg "github.com/yurika0211/luckyagent/internal/cron"
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
		Description: "Unified cron management tool. Use action=list|status|validate|add|remove|pause|resume to manage scheduled jobs through one high-level interface.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"action":              {Type: "string", Description: "Action: list, status, validate, add, remove, pause, resume", Required: true},
			"id":                  {Type: "string", Description: "Job ID for add/remove/pause/resume", Required: false},
			"schedule":            {Type: "string", Description: "Natural language schedule or 5-field cron expression", Required: false},
			"mode":                {Type: "string", Description: "Execution mode: shell or agent", Required: false, Default: "shell"},
			"command":             {Type: "string", Description: "Shell command or agent prompt", Required: false},
			"dry_run":             {Type: "boolean", Description: "For action=add, preview the job without adding, starting, or saving it", Required: false, Default: false},
			"start_engine":        {Type: "boolean", Description: "For action=add, start the cron engine after adding the job", Required: false, Default: true},
			"next_runs":           {Type: "number", Description: "For action=validate, number of future run times to preview", Required: false, Default: 3},
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
			"dry_run":             {Type: "boolean", Description: "Preview the job without adding, starting, or saving it", Required: false, Default: false},
			"start_engine":        {Type: "boolean", Description: "Start the cron engine after adding the job", Required: false, Default: true},
			"platform":            {Type: "string", Description: "Optional notification platform, e.g. telegram", Required: false},
			"chat_id":             {Type: "string", Description: "Optional target chat ID for notification delivery", Required: false},
			"reply_to_message_id": {Type: "string", Description: "Optional reply target message ID", Required: false},
			"session_id":          {Type: "string", Description: "Optional existing session ID to use as agent-mode context. Omit to run cron out of chat sessions.", Required: false},
		},
		Handler: s.HandleAdd,
	})
	r.Register(&Tool{
		Name:        "cron_validate",
		Description: "Validate a cron schedule and preview upcoming run times without creating a job.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"schedule":  {Type: "string", Description: "Natural language schedule or 5-field cron expression", Required: true},
			"mode":      {Type: "string", Description: "Execution mode to validate: shell or agent", Required: false, Default: "shell"},
			"command":   {Type: "string", Description: "Optional shell command or agent prompt for risk preview", Required: false},
			"next_runs": {Type: "number", Description: "Number of future run times to preview", Required: false, Default: 3},
		},
		Handler: s.HandleValidate,
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
	case "validate", "preview":
		return s.HandleValidate(args)
	case "add":
		return s.HandleAdd(args)
	case "remove":
		return s.HandleRemove(args)
	case "pause":
		return s.HandlePause(args)
	case "resume":
		return s.HandleResume(args)
	default:
		return "", fmt.Errorf("invalid cron action %q (use list, status, validate, add, remove, pause, resume)", action)
	}
}

func (s *CronToolService) HandleValidate(args map[string]any) (string, error) {
	scheduleText, _ := args["schedule"].(string)
	schedule, parsedBy, err := parseCronScheduleWithSource(scheduleText)
	if err != nil {
		return "", fmt.Errorf("parse schedule: %w", err)
	}
	modeText := "shell"
	if mode, ok := args["mode"].(string); ok && strings.TrimSpace(mode) != "" {
		modeText = mode
	}
	mode, err := parseCronTaskMode(modeText)
	if err != nil {
		return "", err
	}
	command, _ := args["command"].(string)
	command = strings.TrimSpace(command)
	runCount := parseCronPreviewCount(args, 3, 10)
	warnings := cronCommandWarnings(mode, command)
	out, _ := json.Marshal(map[string]any{
		"ok":            true,
		"valid":         true,
		"action":        "validate",
		"schedule":      schedule.String(),
		"schedule_text": strings.TrimSpace(scheduleText),
		"parsed_by":     parsedBy,
		"timezone":      time.Local.String(),
		"next_runs":     nextCronRuns(schedule, time.Now(), runCount),
		"mode":          mode,
		"command":       command,
		"risk":          cronCommandRisk(mode, command),
		"warnings":      warnings,
	})
	return string(out), nil
}

func (s *CronToolService) HandleAdd(args map[string]any) (string, error) {
	if s == nil || s.engine == nil || s.buildTask == nil {
		return "", fmt.Errorf("cron service not initialized")
	}
	id, _ := args["id"].(string)
	scheduleText, _ := args["schedule"].(string)
	schedule, parsedBy, err := parseCronScheduleWithSource(scheduleText)
	if err != nil {
		return "", fmt.Errorf("parse schedule: %w", err)
	}

	modeText := "shell"
	if mode, ok := args["mode"].(string); ok && strings.TrimSpace(mode) != "" {
		modeText = mode
	}
	mode, err := parseCronTaskMode(modeText)
	if err != nil {
		return "", err
	}
	command, _ := args["command"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = buildCronJobID(mode, command)
	}

	meta := map[string]string{
		"mode":          mode,
		"command":       command,
		"schedule_text": strings.TrimSpace(scheduleText),
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
	dryRun := cronBoolArg(args, "dry_run", false)
	startEngine := cronBoolArg(args, "start_engine", true)
	alreadyRunning := s.engine.IsRunning()
	warnings := cronCommandWarnings(mode, command)
	if _, exists := s.engine.GetJob(id); exists {
		return "", fmt.Errorf("job %s already exists", id)
	}
	if dryRun {
		result, _ := json.Marshal(map[string]any{
			"ok":                    true,
			"action":                "add",
			"dry_run":               true,
			"id":                    id,
			"schedule":              schedule.String(),
			"schedule_text":         strings.TrimSpace(scheduleText),
			"parsed_by":             parsedBy,
			"timezone":              time.Local.String(),
			"next_run":              schedule.Next(time.Now()),
			"next_runs":             nextCronRuns(schedule, time.Now(), 3),
			"mode":                  mode,
			"command":               command,
			"running":               alreadyRunning,
			"would_start_engine":    startEngine && !alreadyRunning,
			"engine_running_before": alreadyRunning,
			"risk":                  cronCommandRisk(mode, command),
			"warnings":              warnings,
			"message":               fmt.Sprintf("Dry run: scheduled job %s was not added", id),
		})
		return string(result), nil
	}
	task := s.buildTask(id, mode, command, meta)
	if err := s.engine.AddJobWithMeta(id, "Cron: "+id, command, schedule, task, meta); err != nil {
		return "", err
	}
	engineStartedByTool := false
	if startEngine && !s.engine.IsRunning() {
		s.engine.Start()
		engineStartedByTool = true
	}
	if s.save != nil {
		if err := s.save(); err != nil {
			return "", err
		}
	}

	result, _ := json.Marshal(map[string]any{
		"ok":                     true,
		"action":                 "add",
		"id":                     id,
		"schedule":               schedule.String(),
		"schedule_text":          strings.TrimSpace(scheduleText),
		"parsed_by":              parsedBy,
		"timezone":               time.Local.String(),
		"next_run":               schedule.Next(time.Now()),
		"mode":                   mode,
		"command":                command,
		"running":                s.engine.IsRunning(),
		"engine_running_before":  alreadyRunning,
		"engine_started_by_tool": engineStartedByTool,
		"risk":                   cronCommandRisk(mode, command),
		"warnings":               warnings,
		"message":                fmt.Sprintf("Scheduled job %s added", id),
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
			"last_error":    job.LastError,
			"run_count":     job.RunCount,
			"error_count":   job.ErrorCount,
			"mode":          job.Metadata["mode"],
			"command":       job.Metadata["command"],
			"schedule_text": cronScheduleText(job),
			"metadata":      job.Metadata,
		})
	}
	result, _ := json.Marshal(map[string]any{
		"ok":       true,
		"action":   "list",
		"now":      time.Now(),
		"timezone": time.Local.String(),
		"running":  s.engine.IsRunning(),
		"total":    len(items),
		"jobs":     items,
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
		"ok":          true,
		"action":      "status",
		"now":         time.Now(),
		"timezone":    time.Local.String(),
		"running":     s.engine.IsRunning(),
		"job_count":   len(jobs),
		"paused_jobs": paused,
		"active_jobs": running,
		"failed_jobs": failed,
	})
	return string(result), nil
}

func parseCronSchedule(input string) (cronpkg.Schedule, error) {
	schedule, _, err := parseCronScheduleWithSource(input)
	return schedule, err
}

func parseCronScheduleWithSource(input string) (cronpkg.Schedule, string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, "", fmt.Errorf("schedule is required")
	}
	schedule, err := cronpkg.ParseNaturalLanguage(trimmed)
	if err == nil {
		return schedule, "natural_language", nil
	}
	schedule, err = cronpkg.ParseCronExpr(trimmed)
	if err != nil {
		return nil, "", err
	}
	return schedule, "cron_expression", nil
}

func parseCronTaskMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "shell":
		return "shell", nil
	case "agent":
		return "agent", nil
	default:
		return "", fmt.Errorf("invalid cron mode %q (use shell or agent)", mode)
	}
}

func normalizeCronTaskMode(mode string) string {
	parsed, err := parseCronTaskMode(mode)
	if err != nil {
		return "shell"
	}
	return parsed
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

func cronBoolArg(args map[string]any, key string, fallback bool) bool {
	if args == nil {
		return fallback
	}
	raw, ok := args[key]
	if !ok || raw == nil {
		return fallback
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "y", "on":
			return true
		case "false", "0", "no", "n", "off":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func parseCronPreviewCount(args map[string]any, fallback, max int) int {
	n, err := parsePositiveCountArg(args, "next_runs", fallback)
	if err != nil {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}

func nextCronRuns(schedule cronpkg.Schedule, from time.Time, count int) []time.Time {
	if schedule == nil || count <= 0 {
		return nil
	}
	runs := make([]time.Time, 0, count)
	cursor := from
	for len(runs) < count {
		next := schedule.Next(cursor)
		if next.IsZero() {
			break
		}
		runs = append(runs, next)
		cursor = next.Add(time.Second)
	}
	return runs
}

func cronCommandWarnings(mode, command string) []string {
	risk := cronCommandRisk(mode, command)
	if risk.Level == "low" {
		return nil
	}
	out := make([]string, 0, len(risk.Reasons))
	for _, reason := range risk.Reasons {
		out = append(out, "shell command risk: "+reason)
	}
	return out
}

type cronCommandRiskResult struct {
	Level   string   `json:"level"`
	Reasons []string `json:"reasons,omitempty"`
}

func cronCommandRisk(mode, command string) cronCommandRiskResult {
	if mode != "shell" {
		return cronCommandRiskResult{Level: "low"}
	}
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return cronCommandRiskResult{Level: "low"}
	}
	var reasons []string
	addIf := func(cond bool, reason string) {
		if cond {
			reasons = append(reasons, reason)
		}
	}
	addIf(hasCronShellWord(lower, "rm") || hasCronShellWord(lower, "mv") || strings.Contains(lower, ">") || hasCronShellWord(lower, "truncate"), "may delete, move, truncate, or overwrite files")
	addIf(hasCronShellWord(lower, "curl") || hasCronShellWord(lower, "wget") || hasCronShellWord(lower, "scp") || hasCronShellWord(lower, "rsync"), "may transfer data over the network")
	addIf(hasCronShellWord(lower, "chmod") || hasCronShellWord(lower, "chown") || hasCronShellWord(lower, "sudo"), "may change permissions or run privileged commands")
	addIf(hasCronShellWord(lower, "nohup") || hasCronShellWord(lower, "systemctl") || strings.Contains(lower, "&"), "may create or control background services")
	addIf(strings.Contains(lower, ".env") || strings.Contains(lower, "id_rsa") || strings.Contains(lower, ".pem"), "may access secret material")
	if len(reasons) == 0 {
		return cronCommandRiskResult{Level: "low"}
	}
	level := "medium"
	if len(reasons) > 1 {
		level = "high"
	}
	return cronCommandRiskResult{Level: level, Reasons: reasons}
}

func hasCronShellWord(command, word string) bool {
	fields := strings.FieldsFunc(command, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-'
	})
	for _, field := range fields {
		if field == word {
			return true
		}
	}
	return false
}
