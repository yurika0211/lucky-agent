package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/session"
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
