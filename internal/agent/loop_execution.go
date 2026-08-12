package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/function"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
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
	Index        int
	ToolCall     provider.ToolCall
	Result       string
	ShortResult  string
	Duration     time.Duration
	Metadata     map[string]any
	Observations []tool.Observation
}

func (a *Agent) executeToolCallsOrdered(
	toolCalls []provider.ToolCall,
	autoApprove bool,
	sess *session.Session,
	toolURLRepeatCount map[string]int,
	toolURLLastResult map[string]string,
	duplicateFetchLimit int,
	allowMixedParallel bool,
	sourceOpt ...string,
) []executedToolCall {
	source := "cli"
	if len(sourceOpt) > 0 && strings.TrimSpace(sourceOpt[0]) != "" {
		source = strings.TrimSpace(sourceOpt[0])
	}
	resultCh := make(chan executedToolCall, len(toolCalls))

	runOne := func(idx int, tc provider.ToolCall) {
		start := time.Now()
		toolResult, err := a.executeToolMaybeDedupDetailed(tc.Name, tc.Arguments, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, source)
		resultText := toolResult.Output
		if err != nil {
			resultText = fmt.Sprintf("Error: %v", err)
			var approvalErr *tool.ApprovalRequiredError
			if errors.As(err, &approvalErr) {
				toolResult.Metadata = map[string]any{
					"approval_required": map[string]any{
						"tool":     approvalErr.Tool,
						"action":   string(approvalErr.Action.Kind),
						"reason":   approvalErr.Reason,
						"frame_id": approvalErr.Action.FrameID,
					},
				}
			}
		}
		shortResult := resultText
		if len(shortResult) > 200 {
			shortResult = shortResult[:197] + "..."
		}
		shortResult = appendMemoryTracePayload(shortResult, toolResult.Metadata)
		resultCh <- executedToolCall{
			Index:        idx,
			ToolCall:     tc,
			Result:       resultText,
			ShortResult:  shortResult,
			Duration:     time.Since(start),
			Metadata:     toolResult.Metadata,
			Observations: toolResult.Observations,
		}
	}

	// Computer-use calls mutate and observe one shared desktop state. Even when
	// another tool is marked ParallelSafe (or a caller explicitly enables mixed
	// parallelism), a batch containing computer_observe/computer_act must run as
	// one serial sequence. Otherwise a concurrent terminal/API call can race a
	// screenshot or GUI action and make the next frame stale or misleading.
	if containsComputerUseToolCall(toolCalls) {
		allowMixedParallel = false
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

func containsComputerUseToolCall(toolCalls []provider.ToolCall) bool {
	for _, call := range toolCalls {
		if isComputerUseToolName(call.Name) {
			return true
		}
	}
	return false
}

func isComputerUseToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "computer_observe", "computer_act":
		return true
	default:
		return false
	}
}

// appendLatestComputerObservation adds the newest computer frame as a
// transient user image. The tool messages must already be appended before this
// helper is called so the function-calling protocol remains well-formed.
// Older transient frames are removed to prevent screenshot accumulation.
func appendLatestComputerObservation(messages []provider.Message, executed []executedToolCall) []provider.Message {
	messages = removeTransientComputerObservations(messages)
	var latest *tool.Observation
	for i := range executed {
		for j := range executed[i].Observations {
			obs := executed[i].Observations[j]
			if strings.TrimSpace(obs.FilePath) == "" && len(obs.ImageData) == 0 {
				continue
			}
			copy := obs
			latest = &copy
		}
	}
	if latest == nil {
		return messages
	}
	part := provider.ContentPart{Type: "image", Image: &provider.ImagePart{
		FilePath: strings.TrimSpace(latest.FilePath),
		MimeType: strings.TrimSpace(latest.MimeType),
	}}
	if part.Image.FilePath == "" && len(latest.ImageData) > 0 {
		// In-process/fake backends may return bytes without persisting a file.
		// Providers understand data URLs, so keep this fallback transient too.
		part.Image.URL = "data:" + imageMimeType(latest.MimeType) + ";base64," + base64.StdEncoding.EncodeToString(latest.ImageData)
	}
	frame := strings.TrimSpace(latest.FrameID)
	label := "[Computer Observation]"
	if frame != "" {
		label = "[Computer Observation " + frame + "]"
	}
	return append(messages, provider.Message{Role: "user", Content: label, ContentParts: []provider.ContentPart{part}})
}

func removeTransientComputerObservations(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return messages
	}
	out := messages[:0]
	for _, msg := range messages {
		if isTransientComputerObservation(msg) {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func isTransientComputerObservation(msg provider.Message) bool {
	return msg.Role == "user" && strings.HasPrefix(strings.TrimSpace(msg.Content), "[Computer Observation")
}

func imageMimeType(mime string) string {
	if strings.TrimSpace(mime) == "" {
		return "image/png"
	}
	return strings.TrimSpace(mime)
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
	sourceOpt ...string,
) []executedToolCall {
	source := "cli"
	if len(sourceOpt) > 0 && strings.TrimSpace(sourceOpt[0]) != "" {
		source = strings.TrimSpace(sourceOpt[0])
	}
	hooksActive := a.hooks.Enabled()
	if (guard == nil && !hooksActive) || len(toolCalls) == 0 {
		return a.executeToolCallsOrdered(toolCalls, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, allowMixedParallel, source)
	}

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

	// Preserve the serial barrier even if a computer-use call was blocked by a
	// guard/hook and therefore is absent from allowed. The original batch still
	// contained a desktop operation, so any surviving calls must not race it (or
	// a hook that may inspect/alter desktop state).
	if containsComputerUseToolCall(toolCalls) {
		allowMixedParallel = false
	}
	var executed []executedToolCall
	if containsComputerUseToolCall(toolCalls) && !containsComputerUseToolCall(allowed) {
		// A computer call may have been blocked by the guard or a hook. Keep the
		// remaining calls serial nevertheless: the original model batch still
		// represented one desktop-dependent plan, and running its survivors in
		// parallel would make the blocked/approved ordering nondeterministic.
		for _, tc := range allowed {
			one := a.executeToolCallsOrdered([]provider.ToolCall{tc}, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, false, source)
			executed = append(executed, one...)
		}
	} else {
		executed = a.executeToolCallsOrdered(allowed, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, allowMixedParallel, source)
	}
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

func emitChatObservationEvents(events chan<- ChatEvent, result executedToolCall) {
	for _, obs := range result.Observations {
		if strings.TrimSpace(obs.FrameID) == "" && strings.TrimSpace(obs.FilePath) == "" && len(obs.ImageData) == 0 {
			continue
		}
		events <- ChatEvent{
			Type: ChatEventObservation,
			Name: result.ToolCall.Name,
			Content: "Computer observation" + func() string {
				if strings.TrimSpace(obs.FrameID) == "" {
					return ""
				}
				return " " + strings.TrimSpace(obs.FrameID)
			}(),
			Observation: &ObservationEvent{
				FrameID: obs.FrameID, FilePath: obs.FilePath, MimeType: obs.MimeType,
				Width: obs.Width, Height: obs.Height,
				ScaleFactor: obs.ScaleFactor, DisplayID: obs.DisplayID,
				ActiveWindow: obs.ActiveWindow,
			},
		}
	}
	if approval := approvalEventFromMetadata(result.Metadata, result.ToolCall.Name); approval != nil {
		events <- ChatEvent{Type: ChatEventApprovalRequired, Name: result.ToolCall.Name, Content: "Approval required", Approval: approval}
	}
}

func approvalEventFromMetadata(metadata map[string]any, toolName string) *ApprovalEvent {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["approval_required"]
	if !ok || raw == nil {
		return nil
	}
	e := &ApprovalEvent{Tool: toolName}
	if m, ok := raw.(map[string]any); ok {
		e.RequestID, _ = m["request_id"].(string)
		e.Action, _ = m["action"].(string)
		e.Reason, _ = m["reason"].(string)
		e.FrameID, _ = m["frame_id"].(string)
	}
	return e
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
