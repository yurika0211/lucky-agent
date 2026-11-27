package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
)

type sessionCall struct {
	id     string
	cancel context.CancelCauseFunc
}

var errSessionCallCancelled = errors.New("session execution cancelled")

type sessionStorageError struct{ cause error }

func (e *sessionStorageError) Error() string { return "session storage: " + e.cause.Error() }
func (e *sessionStorageError) Unwrap() error { return e.cause }

func sessionError(err error) error {
	if err == nil {
		return nil
	}
	return &sessionStorageError{cause: err}
}

func isSessionError(err error) bool {
	var storageErr *sessionStorageError
	return errors.As(err, &storageErr) || errors.Is(err, session.ErrSessionDeleted)
}

func checkSessionHistory(ctx context.Context, sess *session.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sess != nil {
		if _, err := sess.GetMessagesWithError(1); err != nil {
			return sessionError(err)
		}
	}
	return nil
}

func appendStreamSessionMessage(events chan<- ChatEvent, sess *session.Session, msg provider.Message) bool {
	if sess != nil {
		if err := sess.AddProviderMessageWithError(msg); err != nil {
			events <- ChatEvent{Type: ChatEventError, Err: sessionError(err)}
			return false
		}
	}
	return true
}

func (a *Agent) beginSessionCall(ctx context.Context, sess *session.Session) (context.Context, func(), error) {
	if sess == nil {
		return ctx, func() {}, ctx.Err()
	}
	ctx, cancel := context.WithCancelCause(ctx)
	call := &sessionCall{id: sess.ID, cancel: cancel}
	a.sessionCalls.Store(call, struct{}{})
	finish := func() {
		cancel(nil)
		a.sessionCalls.Delete(call)
	}
	// Register before checking the handle: deletion either cancels this call,
	// or its tombstone is observed here before model/tool work starts.
	if err := checkSessionHistory(ctx, sess); err != nil {
		finish()
		return nil, nil, err
	}
	return ctx, finish, nil
}

// CancelSession requests cancellation of active calls owned by this Agent.
// Providers and tools must observe their context to stop external work.
func (a *Agent) CancelSession(sessionID string) bool {
	if a == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	cancelled := false
	a.sessionCalls.Range(func(key, _ any) bool {
		call := key.(*sessionCall)
		if call.id == sessionID {
			call.cancel(errSessionCallCancelled)
			cancelled = true
		}
		return true
	})
	return cancelled
}

// DeleteSession applies the stronger session-deletion boundary: cancel active
// calls and owned delegated tasks, then discard any unmerged delegate
// worktrees. Ordinary CancelSession intentionally keeps those worktrees for a
// later resume; callers should use this method only after the session tombstone
// has been committed.
func (a *Agent) DeleteSession(sessionID string) bool {
	if a == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	a.invalidatePrecomputedCompact(sessionID, "session deleted")
	cancelled := a.CancelSession(sessionID)
	for _, taskID := range a.delegateTasksForSession(sessionID) {
		if a.delegate != nil {
			if err := a.delegate.CancelTask(taskID, "session deleted"); err == nil {
				cancelled = true
			}
		}
		if item, ok := a.worktrees.get(taskID); ok {
			if item.Path != "" {
				if err := removeDelegateWorktree(item.ParentRoot, item.Path); err != nil {
					a.delegateCleanupErr = err
					continue
				}
			}
			a.worktrees.forget(taskID)
		}
		a.forgetDelegateOwner(taskID)
	}
	if a.delegateCleanupErr != nil {
		return false
	}
	return cancelled
}

// SessionCleanupError reports a delete that stopped work but left a worktree
// or child process behind. Callers must not report that delete as success.
func (a *Agent) SessionCleanupError() error {
	if a == nil {
		return nil
	}
	return a.delegateCleanupErr
}

func (r *agentLoopRuntime) beginSessionCall(ctx context.Context, sess *session.Session) (context.Context, func(), error) {
	if r == nil || r.agent == nil {
		return nil, nil, fmt.Errorf("agent loop runtime not initialized")
	}
	return r.agent.beginSessionCall(ctx, sess)
}
