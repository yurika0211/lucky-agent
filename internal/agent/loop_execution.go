package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/function"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

// buildLoopCallOptions constructs the model-visible tool schema for one user input.
func (a *Agent) buildLoopCallOptions(userInput string, loopCfg LoopConfig) provider.CallOptions {
	fcMgr := function.NewManager(a.tools)
	opts := a.buildFunctionCallOptionsForInput(userInput, fcMgr.BuildTools())
	opts.Tools = filterFunctionTools(opts.Tools, loopCfg.DisabledTools)
	opts.ToolChoice = normalizeToolChoiceForTools(opts.ToolChoice, opts.Tools)
	if len(opts.Tools) == 0 {
		opts.ToolChoice = "none"
	}
	return opts
}

func filterFunctionTools(tools []map[string]any, disabled []string) []map[string]any {
	if len(tools) == 0 || len(disabled) == 0 {
		return tools
	}
	disabledSet := make(map[string]struct{}, len(disabled))
	for _, name := range disabled {
		name = strings.TrimSpace(name)
		if name != "" {
			disabledSet[name] = struct{}{}
		}
	}
	if len(disabledSet) == 0 {
		return tools
	}

	filtered := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if _, blocked := disabledSet[functionToolNameFromSchema(t)]; blocked {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

func functionToolNameFromSchema(tool map[string]any) string {
	fn, ok := tool["function"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := fn["name"].(string)
	return strings.TrimSpace(name)
}

func normalizeToolChoiceForTools(choice any, tools []map[string]any) any {
	name := forcedToolChoiceName(choice)
	if name == "" {
		return choice
	}
	for _, t := range tools {
		if functionToolNameFromSchema(t) == name {
			return choice
		}
	}
	if len(tools) == 0 {
		return "none"
	}
	return "auto"
}

func forcedToolChoiceName(choice any) string {
	m, ok := choice.(map[string]any)
	if !ok {
		return ""
	}
	fn, ok := m["function"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := fn["name"].(string)
	return strings.TrimSpace(name)
}

func prepareLoopCallOptions(messages []provider.Message, base provider.CallOptions, forceSearchSynthesis bool) provider.CallOptions {
	opts := constrainForcedToolChoice(messages, base)
	if forceSearchSynthesis {
		opts.Tools = nil
		opts.ToolChoice = "none"
	}
	return opts
}

func (a *Agent) chatLoopIteration(ctx context.Context, messages []provider.Message, base provider.CallOptions, forceSearchSynthesis bool, turnProvider providerSnapshot) (*provider.Response, error) {
	if !turnProvider.valid() {
		return nil, fmt.Errorf("provider not initialized")
	}
	opts := prepareLoopCallOptions(messages, base, forceSearchSynthesis)
	if fcProvider, ok := turnProvider.provider.(provider.FunctionCallingProvider); ok && len(opts.Tools) > 0 {
		return fcProvider.ChatWithOptions(ctx, messages, opts)
	}
	return turnProvider.provider.Chat(ctx, messages)
}

func (a *Agent) streamLoopIteration(ctx context.Context, messages []provider.Message, base provider.CallOptions, forceSearchSynthesis bool, turnProvider providerSnapshot) (<-chan provider.StreamChunk, error) {
	if !turnProvider.valid() {
		return nil, fmt.Errorf("provider not initialized")
	}
	opts := prepareLoopCallOptions(messages, base, forceSearchSynthesis)
	if fcProvider, ok := turnProvider.provider.(provider.FunctionCallingProvider); ok && len(opts.Tools) > 0 {
		return fcProvider.ChatStreamWithOptions(ctx, messages, opts)
	}
	return turnProvider.provider.ChatStream(ctx, messages)
}

func constrainForcedToolChoice(messages []provider.Message, opts provider.CallOptions) provider.CallOptions {
	name := forcedToolChoiceName(opts.ToolChoice)
	if name == "" {
		return opts
	}
	if hasUsedToolInMessages(messages, name) {
		opts.ToolChoice = "auto"
		return opts
	}
	opts.Tools = filterFunctionToolsByName(opts.Tools, name)
	if len(opts.Tools) == 0 {
		opts.ToolChoice = "none"
	}
	return opts
}

func filterFunctionToolsByName(tools []map[string]any, name string) []map[string]any {
	name = strings.TrimSpace(name)
	if name == "" || len(tools) == 0 {
		return nil
	}
	filtered := make([]map[string]any, 0, 1)
	for _, t := range tools {
		if functionToolNameFromSchema(t) == name {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

type executedToolCall struct {
	Index       int
	ToolCall    provider.ToolCall
	Result      string
	ShortResult string
	Duration    time.Duration
	Metadata    map[string]any
}

func (a *Agent) executeToolCallsOrdered(
	toolCalls []provider.ToolCall,
	autoApprove bool,
	sess *session.Session,
	toolURLRepeatCount map[string]int,
	toolURLLastResult map[string]string,
	duplicateFetchLimit int,
	allowMixedParallel bool,
) []executedToolCall {
	resultCh := make(chan executedToolCall, len(toolCalls))

	runOne := func(idx int, tc provider.ToolCall) {
		start := time.Now()
		toolResult, err := a.executeToolMaybeDedupDetailed(tc.Name, tc.Arguments, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit)
		resultText := toolResult.Output
		if err != nil {
			resultText = fmt.Sprintf("Error: %v", err)
		}
		shortResult := resultText
		if len(shortResult) > 200 {
			shortResult = shortResult[:197] + "..."
		}
		shortResult = appendMemoryTracePayload(shortResult, toolResult.Metadata)
		resultCh <- executedToolCall{
			Index:       idx,
			ToolCall:    tc,
			Result:      resultText,
			ShortResult: shortResult,
			Duration:    time.Since(start),
			Metadata:    toolResult.Metadata,
		}
	}

	var parallelIdx []int
	var serialIdx []int
	if allowMixedParallel {
		for i, tc := range toolCalls {
			if a.isToolParallelSafe(tc.Name) {
				parallelIdx = append(parallelIdx, i)
			} else {
				serialIdx = append(serialIdx, i)
			}
		}
	} else {
		allParallelSafe := true
		for _, tc := range toolCalls {
			if !a.isToolParallelSafe(tc.Name) {
				allParallelSafe = false
				break
			}
		}
		for i := range toolCalls {
			if allParallelSafe {
				parallelIdx = append(parallelIdx, i)
			} else {
				serialIdx = append(serialIdx, i)
			}
		}
	}

	for _, idx := range parallelIdx {
		tc := toolCalls[idx]
		go runOne(idx, tc)
	}
	for _, idx := range serialIdx {
		runOne(idx, toolCalls[idx])
	}

	results := make([]executedToolCall, 0, len(toolCalls))
	for i := 0; i < len(toolCalls); i++ {
		results = append(results, <-resultCh)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Index < results[j].Index
	})
	for _, result := range results {
		a.recordProactiveToolEvent(sess, result, false)
	}
	return results
}

func (a *Agent) executeToolCallsOrderedGuarded(
	toolCalls []provider.ToolCall,
	autoApprove bool,
	sess *session.Session,
	toolURLRepeatCount map[string]int,
	toolURLLastResult map[string]string,
	duplicateFetchLimit int,
	allowMixedParallel bool,
	guard *toolExecutionGuard,
) []executedToolCall {
	hooksActive := a.hooks.Enabled()
	if (guard == nil && !hooksActive) || len(toolCalls) == 0 {
		return a.executeToolCallsOrdered(toolCalls, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, allowMixedParallel)
	}

	source := "" // TODO: 接入网关来源（cli/telegram/qq/...），供 hook 按来源差异化匹配
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}

	// blockedResults 按原始下标记录被拦截的调用；allowed 保存放行（PreToolUse
	// 可能改写过参数）的调用，交给底层并行/串行执行。
	blockedResults := make(map[int]executedToolCall, len(toolCalls))
	allowed := make([]provider.ToolCall, 0, len(toolCalls))
	for idx, tc := range toolCalls {
		if msg, blocked := guard.blockMessage(tc); blocked {
			result := executedToolCall{Index: idx, ToolCall: tc, Result: msg, ShortResult: msg}
			blockedResults[idx] = result
			a.recordProactiveToolEvent(sess, result, true)
			continue
		}
		if hooksActive {
			finalArgs, blocked, blockMsg := a.hooks.RunPre(tc.Name, tc.Arguments, source, sessionID)
			if blocked {
				result := executedToolCall{Index: idx, ToolCall: tc, Result: blockMsg, ShortResult: blockMsg}
				blockedResults[idx] = result
				a.recordProactiveToolEvent(sess, result, true)
				continue
			}
			tc.Arguments = finalArgs
		}
		allowed = append(allowed, tc)
	}

	executed := a.executeToolCallsOrdered(allowed, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, allowMixedParallel)
	results := make([]executedToolCall, 0, len(toolCalls))
	next := 0
	for idx := range toolCalls {
		if blocked, ok := blockedResults[idx]; ok {
			results = append(results, blocked)
			continue
		}
		if next >= len(executed) {
			break
		}
		execResult := executed[next]
		execResult.Index = idx
		results = append(results, execResult)
		next++
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Index < results[j].Index
	})
	return results
}

func buildContextToolResult(toolName, rawResult string, successfulSearchEvidence, detailedSearchEvidence *int) string {
	contextResult := compactToolResultForContext(toolName, rawResult)
	if isUsefulSearchEvidence(toolName, rawResult) {
		if successfulSearchEvidence != nil {
			*successfulSearchEvidence = *successfulSearchEvidence + 1
		}
		if toolName == "web_search" && detailedSearchEvidence != nil {
			if *detailedSearchEvidence >= 4 {
				contextResult = "[Additional web_search results omitted to save context. Use the earlier search evidence to synthesize the answer.]"
			} else {
				*detailedSearchEvidence = *detailedSearchEvidence + 1
			}
		}
	}
	return contextResult
}

func maybeAppendSearchSynthesisMessage(messages []provider.Message, forceSearchSynthesis *bool, successfulSearchEvidence, consecutiveToolOnlyIters int) []provider.Message {
	if forceSearchSynthesis == nil || *forceSearchSynthesis {
		return messages
	}
	if shouldForceSearchSynthesis(successfulSearchEvidence, consecutiveToolOnlyIters) {
		*forceSearchSynthesis = true
		messages = append(messages, provider.Message{
			Role:    "user",
			Content: searchSynthesisPrompt,
		})
	}
	return messages
}

func emitChatToolCallEvents(events chan<- ChatEvent, toolCalls []provider.ToolCall) {
	for _, tc := range toolCalls {
		shortArgs := tc.Arguments
		if len(shortArgs) > 100 {
			shortArgs = shortArgs[:97] + "..."
		}
		events <- ChatEvent{
			Type:    ChatEventToolCall,
			Name:    tc.Name,
			Args:    shortArgs,
			Content: fmt.Sprintf("🔧 %s", tc.Name),
		}
	}
}

func emitChatToolResultEvent(events chan<- ChatEvent, toolName, shortResult string) {
	displayResult, memoryTracePayload, hasMemoryTrace := splitMemoryTracePayload(shortResult)
	events <- ChatEvent{
		Type:    ChatEventToolResult,
		Name:    toolName,
		Result:  displayResult,
		Content: fmt.Sprintf("📋 %s → %s", toolName, displayResult),
	}
	if hasMemoryTrace {
		events <- ChatEvent{
			Type:    ChatEventToolResult,
			Name:    chatEventMemoryTraceName,
			Result:  memoryTracePayload,
			Content: "Memory Trace",
		}
	}
}

const chatEventMemoryTraceName = "__memory_trace"
const memoryTracePayloadMarker = "\n\n__LUCKYAGENT_MEMORY_TRACE__"

func appendMemoryTracePayload(shortResult string, metadata map[string]any) string {
	if len(metadata) == 0 {
		return shortResult
	}
	trace, ok := metadata["memory_trace"]
	if !ok || trace == nil {
		return shortResult
	}
	data, err := json.Marshal(trace)
	if err != nil || len(data) == 0 {
		return shortResult
	}
	return shortResult + memoryTracePayloadMarker + string(data)
}

func splitMemoryTracePayload(shortResult string) (displayResult string, payload string, ok bool) {
	idx := strings.Index(shortResult, memoryTracePayloadMarker)
	if idx < 0 {
		return shortResult, "", false
	}
	display := strings.TrimSpace(shortResult[:idx])
	payload = strings.TrimSpace(shortResult[idx+len(memoryTracePayloadMarker):])
	return display, payload, payload != ""
}
