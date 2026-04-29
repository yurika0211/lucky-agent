package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/tool"
)

/*
cronTaskMode 表示定时任务的执行模式。
*/
type cronTaskMode string

const (
	cronTaskModeShell cronTaskMode = "shell"
	cronTaskModeAgent cronTaskMode = "agent"
)

/*
parseCronSchedule 解析自然语言或标准 cron 表达式为调度对象。
*/
func parseCronSchedule(input string) (cron.Schedule, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, fmt.Errorf("schedule is required")
	}
	schedule, err := cron.ParseNaturalLanguage(trimmed)
	if err == nil {
		return schedule, nil
	}
	return cron.ParseCronExpr(trimmed)
}

/*
saveCronJobs 将当前 cron 引擎中的任务持久化到存储。
*/
func (a *Agent) saveCronJobs() error {
	if a == nil || a.cronStore == nil || a.cronEngine == nil {
		return nil
	}
	return a.cronStore.Save(a.cronEngine)
}

/*
restoreCronJobs 从持久化存储恢复 cron 任务。
*/
func (a *Agent) restoreCronJobs() (int, error) {
	if a == nil || a.cronStore == nil || a.cronEngine == nil {
		return 0, nil
	}
	return a.cronStore.Load(a.cronEngine, func(job cron.PersistedJob) (func() error, map[string]string, error) {
		mode := cronTaskMode(strings.ToLower(strings.TrimSpace(job.Mode)))
		switch mode {
		case cronTaskModeAgent, cronTaskModeShell:
		default:
			mode = cronTaskModeShell
		}

		command := strings.TrimSpace(job.Command)
		if command == "" {
			return nil, nil, fmt.Errorf("command is empty")
		}

		metadata := map[string]string{
			"mode": mode.String(),
		}
		for k, v := range job.Metadata {
			metadata[k] = v
		}
		if strings.TrimSpace(metadata["session_id"]) == "" {
			metadata["session_id"] = "cron-" + job.ID
		}
		return a.buildCronTask(job.ID, mode, command, metadata), metadata, nil
	})
}

/*
String 返回 cronTaskMode 的字符串形式。
*/
func (m cronTaskMode) String() string {
	return string(m)
}

func (a *Agent) registerCronTools() {
	if a == nil || a.tools == nil || a.cronEngine == nil {
		return
	}

	a.tools.Register(&tool.Tool{
		Name:        "cron_add",
		Description: "Add a scheduled job. Accepts natural language schedules like 每天9点, 每30分钟, 工作日18点, 明天10点, or a 5-field cron expression like 0 9 * * *. Mode can be shell or agent.",
		Category:    tool.CatDelegate,
		Source:      "builtin",
		Permission:  tool.PermApprove,
		Parameters: map[string]tool.Param{
			"id":                  {Type: "string", Description: "Optional job ID. Auto-generated when omitted.", Required: false},
			"schedule":            {Type: "string", Description: "Natural language schedule or 5-field cron expression", Required: true},
			"mode":                {Type: "string", Description: "Execution mode: shell or agent", Required: false, Default: "shell"},
			"command":             {Type: "string", Description: "Shell command to run, or agent prompt when mode=agent", Required: true},
			"platform":            {Type: "string", Description: "Optional notification platform, e.g. telegram", Required: false},
			"chat_id":             {Type: "string", Description: "Optional target chat ID for notification delivery", Required: false},
			"reply_to_message_id": {Type: "string", Description: "Optional reply target message ID", Required: false},
		},
		Handler: a.handleCronAdd,
	})
	a.tools.Register(&tool.Tool{
		Name:        "cron_list",
		Description: "List all scheduled jobs and their current status.",
		Category:    tool.CatDelegate,
		Source:      "builtin",
		Permission:  tool.PermAuto,
		Parameters:  map[string]tool.Param{},
		Handler:     a.handleCronList,
	})
	a.tools.Register(&tool.Tool{
		Name:        "cron_remove",
		Description: "Remove a scheduled job by ID.",
		Category:    tool.CatDelegate,
		Source:      "builtin",
		Permission:  tool.PermApprove,
		Parameters: map[string]tool.Param{
			"id": {Type: "string", Description: "Job ID", Required: true},
		},
		Handler: a.handleCronRemove,
	})
	a.tools.Register(&tool.Tool{
		Name:        "cron_pause",
		Description: "Pause a scheduled job by ID.",
		Category:    tool.CatDelegate,
		Source:      "builtin",
		Permission:  tool.PermApprove,
		Parameters: map[string]tool.Param{
			"id": {Type: "string", Description: "Job ID", Required: true},
		},
		Handler: a.handleCronPause,
	})
	a.tools.Register(&tool.Tool{
		Name:        "cron_resume",
		Description: "Resume a paused scheduled job by ID.",
		Category:    tool.CatDelegate,
		Source:      "builtin",
		Permission:  tool.PermApprove,
		Parameters: map[string]tool.Param{
			"id": {Type: "string", Description: "Job ID", Required: true},
		},
		Handler: a.handleCronResume,
	})
	a.tools.Register(&tool.Tool{
		Name:        "cron_status",
		Description: "Get cron engine running status and job counts.",
		Category:    tool.CatDelegate,
		Source:      "builtin",
		Permission:  tool.PermAuto,
		Parameters:  map[string]tool.Param{},
		Handler:     a.handleCronStatus,
	})
}

func (a *Agent) handleCronAdd(args map[string]any) (string, error) {
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

	mode := cronTaskMode(modeText)
	switch mode {
	case cronTaskModeShell, cronTaskModeAgent:
	default:
		return "", fmt.Errorf("invalid mode %q (use shell or agent)", modeText)
	}

	meta := map[string]string{
		"mode":          string(mode),
		"command":       command,
		"schedule_text": scheduleText,
		"session_id":    "cron-" + id,
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
	task := a.buildCronTask(id, mode, command, meta)
	if err := a.cronEngine.AddJobWithMeta(id, "Cron: "+id, command, schedule, task, meta); err != nil {
		return "", err
	}
	if !a.cronEngine.IsRunning() {
		a.cronEngine.Start()
	}
	if err := a.saveCronJobs(); err != nil {
		return "", err
	}

	result, _ := json.Marshal(map[string]any{
		"id":       id,
		"schedule": schedule.String(),
		"mode":     mode,
		"command":  command,
		"running":  a.cronEngine.IsRunning(),
		"message":  fmt.Sprintf("Scheduled job %s added", id),
	})
	return string(result), nil
}

/*
buildCronJobID 根据模式与命令生成较稳定的任务 ID。
*/
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

func (a *Agent) handleCronList(args map[string]any) (string, error) {
	jobs := a.cronEngine.ListJobs()
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
		"running": a.cronEngine.IsRunning(),
		"total":   len(items),
		"jobs":    items,
	})
	return string(result), nil
}

/*
cronScheduleText 返回定时任务的人类可读调度文本。
*/
func cronScheduleText(job *cron.Job) string {
	if job == nil {
		return ""
	}
	if text := strings.TrimSpace(job.Metadata["schedule_text"]); text != "" {
		return text
	}
	return cron.DescribeSchedule(job.Schedule)
}

func (a *Agent) handleCronRemove(args map[string]any) (string, error) {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := a.cronEngine.RemoveJob(id); err != nil {
		return "", err
	}
	if err := a.saveCronJobs(); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"id":"%s","message":"removed"}`, id), nil
}

func (a *Agent) handleCronPause(args map[string]any) (string, error) {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := a.cronEngine.PauseJob(id); err != nil {
		return "", err
	}
	if err := a.saveCronJobs(); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"id":"%s","message":"paused"}`, id), nil
}

func (a *Agent) handleCronResume(args map[string]any) (string, error) {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := a.cronEngine.ResumeJob(id); err != nil {
		return "", err
	}
	if err := a.saveCronJobs(); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"id":"%s","message":"resumed"}`, id), nil
}

func (a *Agent) handleCronStatus(args map[string]any) (string, error) {
	jobs := a.cronEngine.ListJobs()
	paused, running, failed := 0, 0, 0
	for _, job := range jobs {
		switch job.Status {
		case cron.StatusPaused:
			paused++
		case cron.StatusRunning:
			running++
		case cron.StatusFailed:
			failed++
		}
	}
	result, _ := json.Marshal(map[string]any{
		"running":     a.cronEngine.IsRunning(),
		"job_count":   len(jobs),
		"paused_jobs": paused,
		"active_jobs": running,
		"failed_jobs": failed,
	})
	return string(result), nil
}

func (a *Agent) buildCronTask(id string, mode cronTaskMode, command string, metadata map[string]string) func() error {
	return func() error {
		fmt.Printf("\n⏰ [cron:%s] %s\n", id, command)

		switch mode {
		case cronTaskModeAgent:
			runCfg := DefaultLoopConfig()
			if a.cfg != nil {
				cfg := a.cfg.Get()
				ApplyAgentLoopConfig(&runCfg, cfg.Agent)
			}
			runCfg.AutoApprove = true

			sessionID := strings.TrimSpace(metadata["session_id"])
			var sess *session.Session
			if sessionID != "" {
				if existing, ok := a.Sessions().Get(sessionID); ok {
					sess = existing
				}
			}
			if sess == nil {
				sess = session.NewSession(sessionID, a.Sessions().Dir())
				sess.Title = "cron-" + id
				a.Sessions().Upsert(sess)
			}
			result, err := a.RunLoopWithSession(context.Background(), sess, command, runCfg)
			if err != nil {
				a.sendCronNotification(metadata, fmt.Sprintf("⏰ 定时任务 [%s] 执行失败: %s", id, err.Error()))
				return err
			}
			if out := strings.TrimSpace(result.Response); out != "" {
				fmt.Println(out)
				a.sendCronNotification(metadata, fmt.Sprintf("⏰ 定时任务 [%s] 执行结果:\n\n%s", id, out))
			}
			return nil

		default:
			if a.gateway == nil {
				return fmt.Errorf("gateway is not initialized")
			}
			res, err := a.gateway.Execute("shell", map[string]any{
				"command": command,
				"timeout": 300,
			}, "")
			if res != nil && strings.TrimSpace(res.Output) != "" {
				fmt.Println(res.Output)
				a.sendCronNotification(metadata, fmt.Sprintf("⏰ 定时任务 [%s] 执行结果:\n\n%s", id, strings.TrimSpace(res.Output)))
			}
			if err != nil {
				a.sendCronNotification(metadata, fmt.Sprintf("⏰ 定时任务 [%s] 执行失败: %s", id, err.Error()))
				return err
			}
			if res != nil && strings.Contains(res.Output, "[exit code:") {
				a.sendCronNotification(metadata, fmt.Sprintf("⏰ 定时任务 [%s] 执行失败: shell command exited with non-zero status", id))
				return fmt.Errorf("shell command exited with non-zero status")
			}
			return nil
		}
	}
}

func (a *Agent) sendCronNotification(metadata map[string]string, message string) {
	if a == nil || a.msgGateway == nil || strings.TrimSpace(message) == "" {
		return
	}
	platform := strings.TrimSpace(metadata["platform"])
	chatID := strings.TrimSpace(metadata["chatID"])
	if platform == "" || chatID == "" {
		return
	}
	gw, ok := a.msgGateway.Get(platform)
	if !ok || gw == nil || !gw.IsRunning() {
		return
	}
	replyToMsgID := strings.TrimSpace(metadata["replyToMsgID"])
	sendCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if replyToMsgID != "" {
		_ = gw.SendWithReply(sendCtx, chatID, replyToMsgID, message)
		return
	}
	_ = gw.Send(sendCtx, chatID, message)
}
