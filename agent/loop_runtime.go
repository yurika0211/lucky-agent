package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/sandbox"
	"github.com/yurika0211/luckyagent-runtime/session"
)

// agentLoopRuntime adapts the coordinator's services to the small runtime
// contract consumed by Loop. Keeping this adapter separate is deliberate:
// Agent no longer has to implement loopRuntime itself, so adding a service to
// the coordinator does not silently make it part of the turn execution API.
//
// The adapter currently delegates to the existing Agent methods. That keeps
// this extraction behavior preserving while giving the next steps a concrete
// seam for moving provider, context, tool, and memory services out one at a
// time.
type agentLoopRuntime struct {
	agent *Agent
}

var _ loopRuntime = (*agentLoopRuntime)(nil)

func newAgentLoopRuntime(a *Agent) loopRuntime {
	return &agentLoopRuntime{agent: a}
}

func (r *agentLoopRuntime) providerSnapshotForTurn(input string) providerSnapshot {
	if r == nil || r.agent == nil {
		return providerSnapshot{}
	}
	return r.agent.providerSnapshotForTurn(input)
}

func (r *agentLoopRuntime) sandboxSnapshot(sess *session.Session) (*sandbox.Snapshot, error) {
	if r == nil || r.agent == nil {
		return nil, nil
	}
	return r.agent.sandboxSnapshot(sess)
}

func (r *agentLoopRuntime) SandboxMode() sandbox.Mode {
	if r == nil || r.agent == nil {
		return sandbox.ModeDev
	}
	return r.agent.SandboxMode()
}

func (r *agentLoopRuntime) applySandboxToolPolicy(cfg *LoopConfig, snapshot *sandbox.Snapshot) {
	if r == nil || r.agent == nil {
		return
	}
	r.agent.applySandboxToolPolicy(cfg, snapshot)
}

func (r *agentLoopRuntime) applyIntentToolGating(cfg *LoopConfig, input string) {
	if r == nil || r.agent == nil {
		return
	}
	r.agent.applyIntentToolGating(cfg, input)
}

func (r *agentLoopRuntime) lastRequestProfile(sessionID string) RequestProfile {
	if r == nil || r.agent == nil {
		return ""
	}
	return r.agent.lastRequestProfile(sessionID)
}

func (r *agentLoopRuntime) rememberRequestProfile(sessionID string, profile RequestProfile) {
	if r == nil || r.agent == nil {
		return
	}
	r.agent.rememberRequestProfile(sessionID, profile)
}

func (r *agentLoopRuntime) classifyRequestProfile(ctx context.Context, input string, cfg LoopConfig, snapshot providerSnapshot) RequestProfileDecision {
	if r == nil || r.agent == nil {
		return classifyRequestProfile(input, cfg)
	}
	return r.agent.classifyRequestProfileWithModel(ctx, input, cfg, snapshot)
}

func (r *agentLoopRuntime) providerSnapshotForProfile(snapshot providerSnapshot, profile RequestProfile) providerSnapshot {
	if r == nil || r.agent == nil {
		return snapshot
	}
	return r.agent.providerSnapshotForProfile(snapshot, profile)
}

func (r *agentLoopRuntime) providerSnapshotForProfileSource(snapshot providerSnapshot, profile RequestProfile, source string) providerSnapshot {
	if r == nil || r.agent == nil {
		return snapshot
	}
	return r.agent.providerSnapshotForProfileSource(snapshot, profile, source)
}

func (r *agentLoopRuntime) applyVisionToolPolicy(cfg *LoopConfig, snapshot providerSnapshot) {
	if r == nil || r.agent == nil {
		return
	}
	r.agent.applyVisionToolPolicy(cfg, snapshot)
}

func (r *agentLoopRuntime) getStreamMode() StreamMode {
	if r == nil || r.agent == nil {
		return DefaultStreamMode
	}
	return r.agent.getStreamMode()
}

func (r *agentLoopRuntime) appendNaturalCitationsIfEnabled(response string, calls []toolCallLog) string {
	if r == nil || r.agent == nil {
		return response
	}
	return r.agent.appendNaturalCitationsIfEnabled(response, calls)
}

func (r *agentLoopRuntime) loopHasRAG() bool {
	return r != nil && r.agent != nil && r.agent.loopHasRAG()
}

func (r *agentLoopRuntime) indexConversationTurn(userInput, assistantResponse string) {
	if r == nil || r.agent == nil {
		return
	}
	r.agent.indexConversationTurn(userInput, assistantResponse)
}

func (r *agentLoopRuntime) saveFinalAnswerDocument(sessionID, userInput, assistantResponse string) error {
	if r == nil || r.agent == nil {
		return nil
	}
	return r.agent.saveFinalAnswerDocument(sessionID, userInput, assistantResponse)
}

func (r *agentLoopRuntime) maybeRollSession(sess *session.Session) {
	if r == nil || r.agent == nil {
		return
	}
	r.agent.maybeRollSession(sess)
}

func (r *agentLoopRuntime) buildMemoryToolGate(query string, scope TurnScope, disabled []string) *memoryToolGate {
	if r == nil || r.agent == nil {
		return nil
	}
	return r.agent.buildMemoryToolGate(query, scope, disabled)
}

func (r *agentLoopRuntime) maybeAutoCompactSessionWithProvider(ctx context.Context, sess *session.Session, input string, ephemeral bool, snapshot providerSnapshot, onProgress func(CompactProgress)) {
	if r == nil || r.agent == nil {
		return
	}
	r.agent.maybeAutoCompactSessionWithProvider(ctx, sess, input, ephemeral, snapshot, onProgress)
}

func (r *agentLoopRuntime) buildContextMessagesForInputWithProvider(ctx context.Context, sess *session.Session, input UserTurnInput, opts contextBuildOptions, snapshot providerSnapshot) []provider.Message {
	if r == nil || r.agent == nil {
		return nil
	}
	return r.agent.buildContextMessagesForInputWithProvider(ctx, sess, input, opts, snapshot)
}

func (r *agentLoopRuntime) buildLoopCallOptions(input string, cfg LoopConfig) provider.CallOptions {
	if r == nil || r.agent == nil {
		return provider.CallOptions{}
	}
	return r.agent.buildLoopCallOptions(input, cfg)
}

func (r *agentLoopRuntime) appendEphemeralToolSchemas(opts provider.CallOptions, sessionID string) provider.CallOptions {
	if r == nil || r.agent == nil {
		return opts
	}
	return r.agent.appendEphemeralToolSchemas(opts, sessionID)
}

func (r *agentLoopRuntime) chatLoopIterationWithSoftRetry(ctx context.Context, timeout time.Duration, messages []provider.Message, opts provider.CallOptions, forceSearch bool, snapshot providerSnapshot, onRetry func()) (*provider.Response, error, bool) {
	if r == nil || r.agent == nil {
		return nil, fmt.Errorf("agent loop runtime not initialized"), false
	}
	return r.agent.chatLoopIterationWithSoftRetry(ctx, timeout, messages, opts, forceSearch, snapshot, onRetry)
}

func (r *agentLoopRuntime) processToolCallBatch(resp *provider.Response, cfg LoopConfig, result *LoopResult, sess *session.Session, messages []provider.Message, state *loopRuntimeState, gate *memoryToolGate, input string) ([]provider.Message, bool, string) {
	if r == nil || r.agent == nil {
		return messages, false, ""
	}
	return r.agent.processToolCallBatch(resp, cfg, result, sess, messages, state, gate, input)
}

func (r *agentLoopRuntime) executeMemoryGateForLoop(gate *memoryToolGate, cfg LoopConfig, result *LoopResult, sess *session.Session, messages []provider.Message, state *loopRuntimeState) ([]provider.Message, bool) {
	if r == nil || r.agent == nil {
		return messages, false
	}
	return r.agent.executeMemoryGateForLoop(gate, cfg, result, sess, messages, state)
}

func (r *agentLoopRuntime) processDirectResponse(resp *provider.Response, messages []provider.Message, state *loopRuntimeState) ([]provider.Message, bool, string) {
	if r == nil || r.agent == nil {
		return messages, true, emptyFinalResponseMessage
	}
	return r.agent.processDirectResponse(resp, messages, state)
}
