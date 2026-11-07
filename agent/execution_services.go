package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

// Product schedulers and child-task engines are optional capabilities. A tool
// loop can be embedded without preparing memory, cron, delegate, or autonomy.
func (a *Agent) bindOptionalToolServices() {
	if a.toolServices == nil {
		return
	}
	if a.cronEngine != nil {
		a.toolServices.Cron = tool.NewCronToolService(a.cronEngine, a.saveCronJobs,
			func(id, mode, command string, metadata map[string]string) func() error {
				return a.buildCronTask(id, cronTaskMode(mode), command, metadata)
			})
		a.installCronToolPolicy()
	}
	if a.autonomy != nil {
		a.toolServices.Autonomy = tool.NewAutonomyToolService(a.autonomy, func() error {
			return a.StartAutonomyNow(context.Background())
		})
	}
	a.toolServices.Heartbeat = tool.NewHeartbeatToolService(a.handleHeartbeatTrigger, a.handleHeartbeatStatus)
	a.toolServices.RegisterCoreTools(a.tools)
	// Role validation wraps the published handler first. Isolation wraps that
	// guard so both checks run, and a bad isolation value still fails before
	// a task is created.
	a.installSubagentDelegateGuard()
	a.installDelegateIsolation()
}

func (a *Agent) bindOptionalExecutionServices() {
	loop, sessions := a.loopRunner(), a.sessions
	delegateCfg := tool.DefaultDelegateConfig()
	if a.delegate != nil {
		delegateCfg = a.delegate.Config()
		delegateMgr := a.delegate
		delegateMgr.SetSessionInitializer(func(task *tool.DelegateTask) error {
			sess := sessions.Ensure(task.ExecutionSessionID)
			sess.SetTitle("delegate-task")
			roleName, _, prompt, _, _ := takeSubagentRoleMarker(task.Context)
			if strings.TrimSpace(prompt) == "" {
				prompt = delegatedSubagentSystemPrompt()
			}
			role := "subagent"
			if roleName != "" {
				role = roleName
			}
			sess.SetAgentProfile(role, prompt)
			if task.Workspace != "" {
				sess.SetCwd(task.Workspace)
			}
			return sess.Save()
		})
		delegateMgr.SetAgentExecutorWithOptions(func(ctx context.Context, description, contextStr string, options tool.AgentExecutorOptions) (string, error) {
			sessionID := tool.DelegateExecutionSessionID(ctx)
			var sess *session.Session
			if sessionID != "" {
				sess = sessions.Ensure(sessionID)
				sess.SetTitle("delegate-task")
			} else {
				sess = sessions.NewWithTitle("delegate-task")
			}
			taskID := tool.DelegateTaskID(ctx)
			worktree, snap, prepErr := a.prepareDelegatedWorkspace(ctx, taskID, contextStr, sess)
			if prepErr != nil {
				return "", prepErr
			}
			if snap != nil {
				defer snap.Close()
			}
			if worktree.Path == "" {
				if pending, ok := a.worktrees.get(taskID); ok && pending.ParentRoot != "" {
					a.worktrees.forget(taskID)
					sess.SetCwd(pending.ParentRoot)
				} else if workspace := tool.ExtractDelegateWorkspace(contextStr); workspace != "" {
					if err := os.MkdirAll(workspace, 0o755); err == nil {
						sess.SetCwd(workspace)
					}
				}
			}
			roleWorkspace := ""
			if worktree.Path != "" {
				roleWorkspace = worktree.ParentRoot
			}
			cfg, contextStr, err := a.childLoopForDelegate(delegateCfg, options.AllowRecursiveDelegate, contextStr, roleWorkspace)
			if err != nil {
				if note := a.finishDelegatedWorktree(worktree, err); note != "" {
					return note, err
				}
				return "", err
			}
			if snap != nil {
				cfg.Sandbox = snap
			}
			prompt := description
			if contextStr != "" {
				prompt = fmt.Sprintf("%s\n\nContext: %s", description, contextStr)
			}
			var result *LoopResult
			if a.delegateChildRun != nil {
				result, err = a.delegateChildRun(ctx, sess, TextUserTurnInput(prompt), cfg)
			} else {
				result, err = a.runDelegatedChild(ctx, loop, sess, prompt, cfg)
			}
			if result != nil {
				lastTool := ""
				if n := len(result.ToolCalls); n > 0 {
					lastTool = result.ToolCalls[n-1].Name
				}
				delegateMgr.RecordExecutionMetrics(taskID, len(result.ToolCalls), lastTool)
			}
			response := ""
			if result != nil {
				response = result.Response
			}
			if worktree.Path != "" && err == nil && delegateTaskWasCancelled(delegateMgr, taskID) {
				err = fmt.Errorf("delegate task cancelled")
			}
			if note := a.finishDelegatedWorktree(worktree, err); note != "" {
				if response != "" {
					response += "\n"
				}
				response += note
			}
			if err != nil {
				return response, err
			}
			return response, nil
		})
		delegateMgr.SetTaskPlanner(a.planCollaboration)
		delegateMgr.SetResultVerifier(a.verifyCollaborationSubTask)
		delegateMgr.SetQueueExecutor(a.executeQueuedDelegate)
	}
	if a.autonomy != nil {
		a.autonomy.SetExecutor(&agentExecutorAdapter{loop: loop, sessions: sessions, delegateConfig: &delegateCfg})
	}
	if a.collabMgr != nil {
		a.collabMgr.SetHandler(collaborationHandler{agent: a})
		a.collabMgr.SetTaskPlanner(a.planCollaboration)
	}
}
