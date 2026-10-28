package agent

import (
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/utils"
)

// loopFinalizer owns the durable side effects that happen after a turn has a
// final answer. Keeping these operations outside Loop.RunWithProviderSnapshot
// leaves the execution core focused on provider calls and tool convergence.
// The runtime contract is intentionally narrow: callers can replace the
// provider/context/tool implementation without rebuilding this service.
type loopFinalizer struct {
	runtime                 loopRuntime
	appendRunningTaskNotice func(string) string
	result                  *LoopResult
	sess                    *session.Session
	loopCfg                 LoopConfig
	sessionID               string
	routingText             string
}

func newLoopFinalizer(runtime loopRuntime, appendRunningTaskNotice func(string) string, result *LoopResult, sess *session.Session, loopCfg LoopConfig, sessionID, routingText string) *loopFinalizer {
	return &loopFinalizer{
		runtime:                 runtime,
		appendRunningTaskNotice: appendRunningTaskNotice,
		result:                  result,
		sess:                    sess,
		loopCfg:                 loopCfg,
		sessionID:               sessionID,
		routingText:             routingText,
	}
}

// Finalize applies the final response and persists the turn's durable
// artifacts. It preserves the provider-message order and side-effect policy
// used by the original inline closure in Loop.RunWithProviderSnapshot.
func (f *loopFinalizer) Finalize(response, reasoningContent string) error {
	if f == nil || f.result == nil {
		return nil
	}
	if strings.TrimSpace(response) == "" {
		response = emptyFinalResponseMessage
	}
	response = utils.SanitizeToolProtocolOutput(response)
	if f.runtime != nil {
		response = f.runtime.appendNaturalCitationsIfEnabled(response, f.result.ToolCalls)
	}
	if f.appendRunningTaskNotice != nil {
		response = f.appendRunningTaskNotice(response)
	}
	f.result.Response = response
	f.result.State = StateDone
	createdAt := time.Now().UTC()
	f.result.CreatedAt = &createdAt

	// Keep provider-level message order: user -> assistant(tool call) -> tool -> assistant(final).
	if f.sess != nil {
		msg := provider.Message{Role: "assistant", Content: response, ReasoningContent: reasoningContent, CreatedAt: f.result.CreatedAt}
		if f.result.Usage != nil && f.result.Usage.TotalTokens > 0 {
			usage := *f.result.Usage
			msg.Usage = &usage
		}
		if err := f.sess.AddProviderMessageWithError(msg); err != nil {
			return sessionError(err)
		}
		if err := f.sess.Save(); err != nil {
			return sessionError(err)
		}
	}

	// Final answers are not indexed into RAG by default: indexed source
	// material should stay separate from model-generated conclusions.
	if !f.loopCfg.Ephemeral && f.runtime != nil && f.runtime.loopHasRAG() && autoIndexFinalAnswersEnabled() {
		f.runtime.indexConversationTurn(f.routingText, response)
	}

	if !f.loopCfg.Ephemeral && f.runtime != nil {
		if saveErr := f.runtime.saveFinalAnswerDocument(f.sessionID, f.routingText, response); saveErr != nil {
			logger.Warn("save final answer document failed", "session_id", f.sessionID, "error", saveErr)
		}
	}

	// v0.24.1: save the session to disk.
	if f.sess != nil {
		if f.runtime != nil {
			f.runtime.maybeRollSession(f.sess)
		}
		if !f.loopCfg.Ephemeral {
			if owner, ok := f.runtime.(interface{ queueSessionRecap(*session.Session) }); ok {
				owner.queueSessionRecap(f.sess)
			}
		}
	}
	return nil
}
