package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

const memoryGateMaxSuggestedSearches = 3

type memoryToolGate struct {
	query       string
	route       memory.RouteAnalysis
	required    []string
	requiredSet map[string]struct{}
	attempted   map[string]struct{}
	failed      map[string]string
	unavailable []string
}

func (a *Agent) buildMemoryToolGate(query string, disabledTools []string) *memoryToolGate {
	if a == nil || a.memory == nil {
		return nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}

	route := a.memory.Route(query)
	if len(route.RequiredTools) == 0 {
		return nil
	}

	disabled := make(map[string]struct{}, len(disabledTools))
	for _, name := range normalizeToolNameList(disabledTools) {
		disabled[name] = struct{}{}
	}

	gate := &memoryToolGate{
		query:       query,
		route:       route,
		requiredSet: make(map[string]struct{}, len(route.RequiredTools)),
		attempted:   make(map[string]struct{}, len(route.RequiredTools)),
		failed:      make(map[string]string),
	}
	seen := make(map[string]struct{}, len(route.RequiredTools))
	for _, name := range route.RequiredTools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if !a.memoryGateToolExecutable(name, disabled) {
			gate.unavailable = append(gate.unavailable, name)
			continue
		}
		gate.required = append(gate.required, name)
		gate.requiredSet[name] = struct{}{}
	}

	if len(gate.required) == 0 && len(gate.unavailable) == 0 {
		return nil
	}
	return gate
}

func (a *Agent) memoryGateToolExecutable(name string, disabled map[string]struct{}) bool {
	if _, blocked := disabled[name]; blocked {
		return false
	}
	if a == nil || a.tools == nil {
		return false
	}
	t, ok := a.tools.Get(name)
	return ok && t != nil && t.Enabled
}

func (g *memoryToolGate) shouldBlockFinal() bool {
	return len(g.unmetRequiredTools()) > 0
}

func (g *memoryToolGate) unmetRequiredTools() []string {
	if g == nil {
		return nil
	}
	var unmet []string
	for _, name := range g.required {
		if _, ok := g.attempted[name]; !ok {
			unmet = append(unmet, name)
		}
	}
	return unmet
}

func (g *memoryToolGate) attemptedTools() []string {
	if g == nil {
		return nil
	}
	var attempted []string
	for _, name := range g.required {
		if _, ok := g.attempted[name]; ok {
			attempted = append(attempted, name)
		}
	}
	return attempted
}

func (g *memoryToolGate) markExecuted(name, result string) {
	if g == nil {
		return
	}
	name = strings.TrimSpace(name)
	if _, ok := g.requiredSet[name]; !ok {
		return
	}
	g.attempted[name] = struct{}{}
	if isMemoryGateToolFailure(result) {
		g.failed[name] = compactMemoryGateResult(result)
		return
	}
	delete(g.failed, name)
}

func isMemoryGateToolFailure(result string) bool {
	out := strings.ToLower(strings.TrimSpace(result))
	return strings.HasPrefix(out, "error:") ||
		strings.Contains(out, "tool not found") ||
		strings.Contains(out, "tool disabled") ||
		strings.Contains(out, "tool denied")
}

func compactMemoryGateResult(result string) string {
	result = strings.TrimSpace(result)
	if len(result) <= 240 {
		return result
	}
	return result[:240] + "...(truncated)"
}

func (g *memoryToolGate) nextToolCalls() []provider.ToolCall {
	if g == nil {
		return nil
	}
	var calls []provider.ToolCall
	for _, name := range g.unmetRequiredTools() {
		switch name {
		case "web_search":
			for _, query := range g.searchQueries() {
				calls = append(calls, provider.ToolCall{
					ID:        provider.GenerateCallID(),
					Name:      name,
					Arguments: mustJSON(map[string]any{"query": query, "count": 5, "mode": "quick"}),
				})
			}
		case "current_time":
			args := map[string]any{}
			if location := g.locationHint(); location != "" {
				args["location"] = location
			}
			calls = append(calls, provider.ToolCall{
				ID:        provider.GenerateCallID(),
				Name:      name,
				Arguments: mustJSON(args),
			})
		default:
			calls = append(calls, provider.ToolCall{
				ID:        provider.GenerateCallID(),
				Name:      name,
				Arguments: `{}`,
			})
		}
	}
	return calls
}

func (g *memoryToolGate) searchQueries() []string {
	if g == nil {
		return nil
	}
	var queries []string
	seen := make(map[string]struct{})
	for _, query := range g.route.SuggestedSearches {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		if _, ok := seen[query]; ok {
			continue
		}
		seen[query] = struct{}{}
		queries = append(queries, query)
		if len(queries) >= memoryGateMaxSuggestedSearches {
			return queries
		}
	}
	if len(queries) == 0 && strings.TrimSpace(g.query) != "" {
		queries = append(queries, strings.TrimSpace(g.query))
	}
	return queries
}

func (g *memoryToolGate) locationHint() string {
	if g == nil {
		return ""
	}
	const prefix = "Use location hint: "
	for _, constraint := range g.route.Constraints {
		constraint = strings.TrimSpace(constraint)
		if !strings.HasPrefix(constraint, prefix) {
			continue
		}
		location := strings.TrimSpace(strings.TrimPrefix(constraint, prefix))
		location = strings.TrimSuffix(location, ".")
		if idx := strings.Index(location, ". If "); idx >= 0 {
			location = location[:idx]
		}
		return strings.TrimSpace(location)
	}
	return ""
}

func (g *memoryToolGate) assistantToolCallMessage(calls []provider.ToolCall) provider.Message {
	return provider.Message{
		Role:      "assistant",
		Content:   "Memory gate is auto-executing required tools before the final answer.",
		ToolCalls: calls,
	}
}

func (g *memoryToolGate) synthesisPrompt() provider.Message {
	var lines []string
	lines = append(lines, "Memory gate checks have now been attempted before the final answer.")
	if len(g.required) > 0 {
		lines = append(lines, "- Required tools: "+strings.Join(g.required, ", "))
	}
	if attempted := g.attemptedTools(); len(attempted) > 0 {
		lines = append(lines, "- Attempted tools: "+strings.Join(attempted, ", "))
	}
	if len(g.failed) > 0 {
		var failures []string
		for _, name := range g.required {
			if failure := strings.TrimSpace(g.failed[name]); failure != "" {
				failures = append(failures, fmt.Sprintf("%s=%s", name, failure))
			}
		}
		if len(failures) > 0 {
			lines = append(lines, "- Failed checks: "+strings.Join(failures, " | "))
		}
	}
	if len(g.unavailable) > 0 {
		lines = append(lines, "- Unavailable required tools: "+strings.Join(g.unavailable, ", "))
	}
	if len(g.route.Constraints) > 0 {
		lines = append(lines, "- Memory constraints: "+strings.Join(limitMemoryGateStrings(g.route.Constraints, 4), " | "))
	}
	lines = append(lines, "Use the memory facts and tool outputs now in context. If a live check failed or a required tool was unavailable, state exactly what could not be checked instead of implying it was verified.")
	return provider.Message{Role: "user", Content: strings.Join(lines, "\n")}
}

func (g *memoryToolGate) incompleteMessage() string {
	if g == nil {
		return "Memory gate could not complete required tool checks before the final answer."
	}
	if unmet := g.unmetRequiredTools(); len(unmet) > 0 {
		return "Memory gate blocked the final answer because required tools were not completed: " + strings.Join(unmet, ", ") + "."
	}
	return "Memory gate completed required tool checks, but the loop ended before a final synthesis could be produced."
}

func (a *Agent) executePreparedMemoryGateToolCalls(
	gate *memoryToolGate,
	calls []provider.ToolCall,
	autoApprove bool,
	sess *session.Session,
	toolURLRepeatCount map[string]int,
	toolURLLastResult map[string]string,
	duplicateFetchLimit int,
	allowMixedParallel bool,
) []executedToolCall {
	if gate == nil || len(calls) == 0 {
		return nil
	}
	if a == nil || a.gateway == nil {
		executed := make([]executedToolCall, 0, len(calls))
		for i, call := range calls {
			result := "Error: tool gateway not initialized"
			gate.markExecuted(call.Name, result)
			executed = append(executed, executedToolCall{
				Index:       i,
				ToolCall:    call,
				Result:      result,
				ShortResult: result,
			})
		}
		return executed
	}
	executed := a.executeToolCallsOrdered(calls, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, allowMixedParallel)
	for _, execResult := range executed {
		gate.markExecuted(execResult.ToolCall.Name, execResult.Result)
	}
	return executed
}

func (a *Agent) executeMemoryGateForLoop(
	gate *memoryToolGate,
	loopCfg LoopConfig,
	result *LoopResult,
	sess *session.Session,
	messages []provider.Message,
	loopState *loopRuntimeState,
) ([]provider.Message, bool) {
	if gate == nil || !gate.shouldBlockFinal() {
		return messages, false
	}
	calls := gate.nextToolCalls()
	if len(calls) == 0 {
		return messages, false
	}

	assistantMsg := gate.assistantToolCallMessage(calls)
	messages = append(messages, assistantMsg)
	if sess != nil {
		sess.AddProviderMessage(assistantMsg)
	}

	executed := a.executePreparedMemoryGateToolCalls(
		gate,
		calls,
		true,
		sess,
		loopState.toolURLRepeatCount,
		loopState.toolURLLastResult,
		loopCfg.DuplicateFetchLimit,
		true,
	)

	for _, execResult := range executed {
		tcLog := toolCallLog{
			Name:      execResult.ToolCall.Name,
			Arguments: execResult.ToolCall.Arguments,
			Result:    execResult.Result,
			Duration:  execResult.Duration,
		}
		result.ToolCalls = append(result.ToolCalls, tcLog)
		contextToolMsg := provider.Message{
			Role:       "tool",
			Content:    buildContextToolResult(execResult.ToolCall.Name, execResult.Result, &loopState.successfulSearchEvidence, &loopState.detailedSearchEvidence),
			ToolCallID: execResult.ToolCall.ID,
			Name:       execResult.ToolCall.Name,
		}
		loopState.toolCallLastResult[toolCallSignature(tcLog.Name, tcLog.Arguments)] = tcLog.Result
		if key := normalizedToolTarget(tcLog.Name, tcLog.Arguments); key != "" {
			loopState.toolURLLastResult[key] = tcLog.Result
		}
		messages = append(messages, contextToolMsg)
		if sess != nil {
			sess.AddProviderMessage(contextToolMsg)
		}
	}

	messages = append(messages, gate.synthesisPrompt())
	messages = a.fitContextWindow(messages)
	result.State = StateObserve
	return messages, true
}

func (a *Agent) continueAfterStreamMemoryGate(
	ctx context.Context,
	events chan<- ChatEvent,
	messages []provider.Message,
	callOpts provider.CallOptions,
	sess *session.Session,
	turnInput UserTurnInput,
	round int,
	remaining int,
	state *streamConvergenceState,
) bool {
	if state == nil || state.memoryGate == nil || !state.memoryGate.shouldBlockFinal() {
		return false
	}
	calls := state.memoryGate.nextToolCalls()
	if len(calls) == 0 {
		return false
	}

	messages = append(messages, state.memoryGate.assistantToolCallMessage(calls))
	emitChatToolCallEvents(events, calls)
	executed := a.executePreparedMemoryGateToolCalls(
		state.memoryGate,
		calls,
		true,
		sess,
		state.toolURLRepeatCount,
		state.toolURLLastResult,
		state.duplicateFetchLimit,
		true,
	)
	for _, execResult := range executed {
		emitChatToolResultEvent(events, execResult.ToolCall.Name, execResult.ShortResult)
		messages = append(messages, provider.Message{
			Role:       "tool",
			Content:    buildContextToolResult(execResult.ToolCall.Name, execResult.Result, &state.successfulSearchEvidence, &state.detailedSearchEvidence),
			ToolCallID: execResult.ToolCall.ID,
			Name:       execResult.ToolCall.Name,
		})
		state.rememberToolCallResult(execResult.ToolCall.Name, execResult.ToolCall.Arguments, execResult.Result, execResult.Duration)
	}

	messages = append(messages, state.memoryGate.synthesisPrompt())
	messages = a.fitContextWindow(messages)
	if remaining <= 1 {
		a.finalizeStreamWithState(events, sess, turnInput, state.memoryGate.incompleteMessage(), state)
		return true
	}
	nextRound := round + 1
	events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
	a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
	return true
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{}`
	}
	return string(b)
}

func limitMemoryGateStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}
