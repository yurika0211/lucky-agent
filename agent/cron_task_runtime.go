package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

// cronTaskRuntime owns the execution dependencies of a scheduled task. Result
// delivery is supplied separately so task closures do not retain the Agent.
type cronTaskRuntime struct {
	loop     *Loop
	sessions *session.Manager
	cfg      *config.Manager
	gateway  *tool.Gateway
	notify   func(map[string]string, cronNotificationPayload) error
}

func (r cronTaskRuntime) buildTask(id string, mode cronTaskMode, command string, metadata map[string]string) func() error {
	return func() error {
		fmt.Printf("\n⏰ [cron:%s] %s\n", id, command)

		switch mode {
		case cronTaskModeAgent:
			runCfg := DefaultLoopConfig()
			if r.cfg != nil {
				cfg := r.cfg.Get()
				ApplyAgentLoopConfig(&runCfg, cfg.Agent)
			}
			runCfg.AutoApprove = true
			runCfg.Foreground = false
			runCfg.DisabledTools = append(runCfg.DisabledTools, cronAgentDisabledTools...)

			sessionID := strings.TrimSpace(metadata["session_id"])
			var sess *session.Session
			if sessionID != "" {
				if existing, ok := r.sessions.Get(sessionID); ok {
					sess = existing
				} else {
					sess = session.NewSession(sessionID, r.sessions.Dir())
					sess.Title = "cron-" + id
					r.sessions.Upsert(sess)
				}
			} else {
				runCfg.Ephemeral = true
			}
			result, err := r.loop.Run(context.Background(), sess, TextUserTurnInput(command), runCfg)
			if err != nil {
				notificationErr := r.notify(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "failed",
					RawResult: err.Error(),
				})
				return combineCronTaskAndNotificationError(err, notificationErr)
			}
			if out := strings.TrimSpace(result.Response); out != "" {
				fmt.Println(out)
				if err := r.notify(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "succeeded",
					RawResult: out,
				}); err != nil {
					return &cronDeliveryError{err: err}
				}
			}
			return nil

		default:
			if r.gateway == nil {
				return fmt.Errorf("gateway is not initialized")
			}
			res, err := r.gateway.Execute("terminal", map[string]any{
				"command": command,
				"timeout": 300,
			}, "")
			if err != nil {
				notificationErr := r.notify(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "failed",
					RawResult: err.Error(),
				})
				return combineCronTaskAndNotificationError(err, notificationErr)
			}
			if res != nil && strings.Contains(res.Output, "[exit code:") {
				taskErr := errors.New("shell command exited with non-zero status")
				notificationErr := r.notify(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "failed",
					RawResult: taskErr.Error(),
				})
				return combineCronTaskAndNotificationError(taskErr, notificationErr)
			}
			if res != nil && strings.TrimSpace(res.Output) != "" {
				out := strings.TrimSpace(res.Output)
				fmt.Println(out)
				if err := r.notify(metadata, cronNotificationPayload{
					JobID:     id,
					Mode:      mode.String(),
					Command:   command,
					Outcome:   "succeeded",
					RawResult: out,
				}); err != nil {
					return &cronDeliveryError{err: err}
				}
			}
			return nil
		}
	}
}
