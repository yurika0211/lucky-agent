package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/provider"
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

const cronNotificationSystemPrompt = `【后台任务汇报设定】
你是我非常信任、观察力敏锐且语气自然的私人助理。你刚刚在后台为我跑完了一项定时任务（Cron Job），现在需要向我汇报结果。

请彻底抛弃死板的机器日志格式（绝对不要使用“执行状态：成功”、“影响行数：5”这种机械词汇）。你的汇报必须像真实人类一样，流畅、有温度且富有细节。

在汇报时，请自然地融合以下几点：

1. 情境带入：用一句口语化的开场白告诉我你刚忙完什么。
2. 提炼高价值细节：如果一切正常，一句话带过；如果发现异常、波动或有趣趋势，就把它当重点说出来。
3. 结果具象化：把数据转成“事情”或“物品”，不要机械罗列。
4. 主动的下一步：基于结果顺水推舟地给出一个建议或询问。

输出要求：
- 直接输出给用户看的最终汇报，不要解释你是怎么写的。
- 默认用中文。
- 控制在 2 到 5 句话。
- 如果执行失败，要说清楚卡在哪里，但仍然保持自然语气。
- 不要杜撰不存在的结果。`

type cronNotificationPayload struct {
	JobID     string
	Mode      string
	Command   string
	Outcome   string
	RawResult string
}

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
		Name:        "cron",
		Description: "Unified cron management tool. Use action=list|status|add|remove|pause|resume to manage scheduled jobs through one high-level interface.",
		Category:    tool.CatDelegate,
		Source:      "builtin",
		Permission:  tool.PermApprove,
		Parameters: map[string]tool.Param{
			"action":              {Type: "string", Description: "Action: list, status, add, remove, pause, resume", Required: true},
			"id":                  {Type: "string", Description: "Job ID for add/remove/pause/resume", Required: false},
			"schedule":            {Type: "string", Description: "Natural language schedule or 5-field cron expression", Required: false},
			"mode":                {Type: "string", Description: "Execution mode: shell or agent", Required: false, Default: "shell"},
			"command":             {Type: "string", Description: "Shell command or agent prompt", Required: false},
			"platform":            {Type: "string", Description: "Optional notification platform", Required: false},
			"chat_id":             {Type: "string", Description: "Optional target chat ID for notification delivery", Required: false},
			"reply_to_message_id": {Type: "string", Description: "Optional reply target message ID", Required: false},
		},
		Handler: a.handleCron,
	})
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

func (a *Agent) handleCron(args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "list":
		return a.handleCronList(args)
	case "status":
		return a.handleCronStatus(args)
	case "add":
		return a.handleCronAdd(args)
	case "remove":
		return a.handleCronRemove(args)
	case "pause":
		return a.handleCronPause(args)
	case "resume":
		return a.handleCronResume(args)
	default:
		return "", fmt.Errorf("invalid cron action %q (use list, status, add, remove, pause, resume)", action)
	}
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
				a.sendCronNotification(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "failed",
					RawResult: err.Error(),
				})
				return err
			}
			if out := strings.TrimSpace(result.Response); out != "" {
				fmt.Println(out)
				a.sendCronNotification(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "succeeded",
					RawResult: out,
				})
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
				a.sendCronNotification(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "succeeded",
					RawResult: strings.TrimSpace(res.Output),
				})
			}
			if err != nil {
				a.sendCronNotification(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "failed",
					RawResult: err.Error(),
				})
				return err
			}
			if res != nil && strings.Contains(res.Output, "[exit code:") {
				a.sendCronNotification(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "failed",
					RawResult: "shell command exited with non-zero status",
				})
				return fmt.Errorf("shell command exited with non-zero status")
			}
			return nil
		}
	}
}

func (a *Agent) sendCronNotification(metadata map[string]string, payload cronNotificationPayload) {
	message := a.formatCronNotification(payload)
	if a == nil || a.msgGateway == nil || strings.TrimSpace(message) == "" {
		return
	}
	platform := strings.TrimSpace(metadata["platform"])
	chatID := strings.TrimSpace(metadata["chatID"])
	replyToMsgID := strings.TrimSpace(metadata["replyToMsgID"])
	if platform == "" || chatID == "" {
		target := a.pickRecentChatTarget()
		if platform == "" {
			platform = strings.TrimSpace(target.Platform)
		}
		if chatID == "" {
			chatID = strings.TrimSpace(target.ChatID)
		}
		if replyToMsgID == "" {
			replyToMsgID = strings.TrimSpace(target.ReplyToMsgID)
		}
	}
	if platform == "" || chatID == "" {
		return
	}
	gw, ok := a.msgGateway.Get(platform)
	if !ok || gw == nil || !gw.IsRunning() {
		return
	}
	sendCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if replyToMsgID != "" {
		_ = gw.SendWithReply(sendCtx, chatID, replyToMsgID, message)
		return
	}
	_ = gw.Send(sendCtx, chatID, message)
}

func (a *Agent) formatCronNotification(payload cronNotificationPayload) string {
	fallback := fallbackCronNotification(payload)
	if a == nil || a.provider == nil {
		return fallback
	}

	rawResult := strings.TrimSpace(payload.RawResult)
	if len(rawResult) > 4000 {
		rawResult = rawResult[:4000] + "\n...（结果过长，已截断）"
	}

	var userPrompt strings.Builder
	userPrompt.WriteString("下面是一次后台定时任务的执行信息，请按设定写成发给用户的自然汇报。\n\n")
	userPrompt.WriteString("job_id: ")
	userPrompt.WriteString(strings.TrimSpace(payload.JobID))
	userPrompt.WriteString("\nmode: ")
	userPrompt.WriteString(strings.TrimSpace(payload.Mode))
	userPrompt.WriteString("\ncommand: ")
	userPrompt.WriteString(strings.TrimSpace(payload.Command))
	userPrompt.WriteString("\noutcome: ")
	userPrompt.WriteString(strings.TrimSpace(payload.Outcome))
	userPrompt.WriteString("\n\n原始执行结果：\n")
	userPrompt.WriteString(rawResult)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := a.provider.Chat(ctx, []provider.Message{
		{Role: "system", Content: cronNotificationSystemPrompt},
		{Role: "user", Content: userPrompt.String()},
	})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return fallback
	}
	return strings.TrimSpace(resp.Content)
}

func fallbackCronNotification(payload cronNotificationPayload) string {
	jobID := strings.TrimSpace(payload.JobID)
	command := strings.TrimSpace(payload.Command)
	rawResult := strings.TrimSpace(payload.RawResult)

	if jobID == "" {
		jobID = "unknown-job"
	}
	if command == "" {
		command = "这项后台任务"
	}

	switch strings.ToLower(strings.TrimSpace(payload.Outcome)) {
	case "failed":
		return fmt.Sprintf("我刚刚替你跑了一下定时任务“%s”，不过这次没顺利完成，主要卡在这里：%s。要不要我接着帮你看看是配置问题、网络问题，还是任务本身需要调整？", command, rawResult)
	default:
		if rawResult == "" {
			return fmt.Sprintf("我刚刚把定时任务“%s”跑完了，整体没有发现特别异常。要不要我顺手把下一步也一起处理掉？", command)
		}
		return fmt.Sprintf("我刚刚把定时任务“%s”跑完了，结果我先帮你整理好了：%s。你要是愿意，我可以继续往下帮你判断这里面有没有值得跟进的地方。", command, rawResult)
	}
}
