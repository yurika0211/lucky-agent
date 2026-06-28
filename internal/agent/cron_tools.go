package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/cron"
	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

/*
cronTaskMode 表示定时任务的执行模式。
*/
type cronTaskMode string

const (
	cronTaskModeShell cronTaskMode = "shell"
	cronTaskModeAgent cronTaskMode = "agent"
)

func getCronNotificationSystemPrompt() string {
	loader := getPromptLoader()
	defaultPrompt := `【后台任务开场设定】
你是我非常信任、观察力敏锐且语气自然的私人助理。你刚刚在后台为我跑完了一项定时任务（Cron Job），现在需要写一段发给用户的简短开场。

输出要求：
- 只写 1 到 2 句话，说明你刚完成了什么，以及结果大体是成功还是失败。
- 不要复述完整原始结果，系统会在你的开场后面自动附上完整内容。
- 不要说"要不要我发原文""要不要我推送完整内容"这类话，因为完整内容会直接发送。
- 不要杜撰不存在的结果。
- 默认用中文，语气自然。`

	return loader.LoadOrDefault("functions/cron_notification.md", defaultPrompt)
}

var cronAgentDisabledTools = []string{
	"cron",
	"cron_add",
	"cron_remove",
	"cron_pause",
	"cron_resume",
}

const cronNotificationForwardChunkLimit = 1200

type cronNotificationPayload struct {
	JobID     string
	Mode      string
	Command   string
	Outcome   string
	RawResult string
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

func (a *Agent) installCronEventHandler() {
	if a == nil || a.cronEngine == nil {
		return
	}
	a.cronEngine.SetEventHandler(func(event cron.Event) {
		switch event.Type {
		case cron.EventJobStarted:
			fmt.Printf("[cron] job %s started\n", event.JobName)
		case cron.EventJobCompleted:
			fmt.Printf("[cron] job %s completed\n", event.JobName)
			if err := a.saveCronJobs(); err != nil {
				fmt.Printf("[cron] save failed: %v\n", err)
			}
		case cron.EventJobFailed:
			fmt.Printf("[cron] job %s failed: %v\n", event.JobName, event.Error)
			if err := a.saveCronJobs(); err != nil {
				fmt.Printf("[cron] save failed: %v\n", err)
			}
		}
	})
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
		return a.buildCronTask(job.ID, mode, command, metadata), metadata, nil
	})
}

/*
String 返回 cronTaskMode 的字符串形式。
*/
func (m cronTaskMode) String() string {
	return string(m)
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
			runCfg.DisabledTools = append(runCfg.DisabledTools, cronAgentDisabledTools...)

			sessionID := strings.TrimSpace(metadata["session_id"])
			var sess *session.Session
			if sessionID != "" {
				if existing, ok := a.Sessions().Get(sessionID); ok {
					sess = existing
				} else {
					sess = session.NewSession(sessionID, a.Sessions().Dir())
					sess.Title = "cron-" + id
					a.Sessions().Upsert(sess)
				}
			} else {
				runCfg.Ephemeral = true
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
			res, err := a.gateway.Execute("terminal", map[string]any{
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
	if metadata == nil {
		metadata = map[string]string{}
	}
	platform := strings.TrimSpace(metadata["platform"])
	chatID := firstCronMetadataValue(metadata, "chatID", "chat_id")
	replyToMsgID := firstCronMetadataValue(metadata, "replyToMsgID", "reply_to_message_id")
	sessionID := strings.TrimSpace(metadata["session_id"])
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
	if forwarder, ok := gw.(gateway.ForwardedTextSender); ok && cronNotificationShouldForward(message) {
		if err := forwarder.SendForwardedText(sendCtx, chatID, "LuckyHarness", splitCronNotificationChunks(message, cronNotificationForwardChunkLimit)); err == nil {
			return
		}
	}
	if receiptGW, ok := gw.(gateway.ReceiptGateway); ok {
		var (
			sent gateway.SentMessage
			err  error
		)
		if replyToMsgID != "" {
			sent, err = receiptGW.SendWithReplyReceipt(sendCtx, chatID, replyToMsgID, message)
		} else {
			sent, err = receiptGW.SendWithReceipt(sendCtx, chatID, message)
		}
		if err == nil && strings.TrimSpace(sent.ID) != "" && sessionID != "" {
			a.RecordExternalReplyAnchor(platform, chatID, sent.ID, sessionID, payload.JobID)
		}
		return
	}
	if replyToMsgID != "" {
		_ = gw.SendWithReply(sendCtx, chatID, replyToMsgID, message)
		return
	}
	_ = gw.Send(sendCtx, chatID, message)
}

func cronNotificationShouldForward(message string) bool {
	return len([]rune(strings.TrimSpace(message))) > cronNotificationForwardChunkLimit
}

func splitCronNotificationChunks(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if limit <= 0 || len([]rune(text)) <= limit {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	for _, line := range strings.Split(text, "\n") {
		for len([]rune(line)) > limit {
			if strings.TrimSpace(current.String()) != "" {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			head, tail := splitCronNotificationRunes(line, limit)
			chunks = append(chunks, strings.TrimSpace(head))
			line = tail
		}

		extra := len([]rune(line))
		if current.Len() > 0 {
			extra++
		}
		if current.Len() > 0 && len([]rune(current.String()))+extra > limit {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}
	if strings.TrimSpace(current.String()) != "" {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}
	return chunks
}

func splitCronNotificationRunes(text string, limit int) (string, string) {
	runes := []rune(text)
	if limit >= len(runes) {
		return text, ""
	}
	return string(runes[:limit]), string(runes[limit:])
}

func firstCronMetadataValue(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func (a *Agent) formatCronNotification(payload cronNotificationPayload) string {
	fallback := buildCronNotificationMessage(fallbackCronNotificationIntro(payload), payload)
	if a == nil || a.provider == nil {
		return fallback
	}

	rawResult := strings.TrimSpace(payload.RawResult)
	if len(rawResult) > 4000 {
		rawResult = rawResult[:4000] + "\n...（结果较长，开场只参考了前半部分，完整内容会在下方附上）"
	}

	var userPrompt strings.Builder
	userPrompt.WriteString("下面是一次后台定时任务的执行信息，请按设定写一段简短开场。\n\n")
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
		{Role: "system", Content: getCronNotificationSystemPrompt()},
		{Role: "user", Content: userPrompt.String()},
	})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return fallback
	}
	intro := strings.TrimSpace(resp.Content)
	if strings.Contains(intro, "要不要") {
		intro = fallbackCronNotificationIntro(payload)
	}
	return buildCronNotificationMessage(intro, payload)
}

func fallbackCronNotificationIntro(payload cronNotificationPayload) string {
	jobID := strings.TrimSpace(payload.JobID)
	command := strings.TrimSpace(payload.Command)

	if jobID == "" {
		jobID = "unknown-job"
	}
	if command == "" {
		command = "这项后台任务"
	}

	switch strings.ToLower(strings.TrimSpace(payload.Outcome)) {
	case "failed":
		return fmt.Sprintf("我刚刚替你跑了一下定时任务\"%s\"，不过这次没顺利完成。完整错误我直接贴在下面。", command)
	default:
		return fmt.Sprintf("我刚刚把定时任务\"%s\"跑完了。完整结果我直接贴在下面。", command)
	}
}

func buildCronNotificationMessage(intro string, payload cronNotificationPayload) string {
	intro = strings.TrimSpace(intro)
	rawResult := strings.TrimSpace(payload.RawResult)
	if intro == "" {
		intro = fallbackCronNotificationIntro(payload)
	}
	if rawResult == "" {
		return intro
	}

	heading := "完整结果"
	if strings.EqualFold(strings.TrimSpace(payload.Outcome), "failed") {
		heading = "完整错误"
	}
	return fmt.Sprintf("%s\n\n%s：\n%s", intro, heading, rawResult)
}
