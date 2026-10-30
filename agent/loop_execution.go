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

	"github.com/yurika0211/luckyagent-runtime/function"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

// buildLoopCallOptions constructs the model-visible tool schema for one user input.
func (a *Agent) buildLoopCallOptions(userInput string, loopCfg LoopConfig) provider.CallOptions {
	fcMgr := function.NewManager(a.tools)
	modelTools := fcMgr.BuildTools()
	opts := a.buildFunctionCallOptionsForInput(userInput, modelTools)
	opts = a.appendEphemeralToolSchemas(opts, loopCfg.SessionID)
	return applyToolAllowlist(opts, loopCfg)
}

func applyToolAllowlist(opts provider.CallOptions, cfg LoopConfig) provider.CallOptions {
	// Reapply the denylist as generated tools may be appended between iterations.
	opts.Tools = filterFunctionTools(opts.Tools, cfg.DisabledTools)
	if cfg.RestrictTools {
		allowed := makeToolNameSet(cfg.AllowedTools)
		filtered := make([]map[string]any, 0, len(opts.Tools))
		for _, schema := range opts.Tools {
			if toolNameInSet(allowed, functionToolNameFromSchema(schema)) {
				filtered = append(filtered, schema)
			}
		}
		opts.Tools = filtered
	}
	opts.ToolChoice = normalizeToolChoiceForTools(opts.ToolChoice, opts.Tools)
	if len(opts.Tools) == 0 {
		opts.ToolChoice = "none"
	}
	return opts
}

// appendEphemeralToolSchemas adds only the current session's generated tools
// to an already prepared call option set. This is needed after the factory
// creates a tool in the middle of an agent loop.
func (a *Agent) appendEphemeralToolSchemas(opts provider.CallOptions, sessionID string) provider.CallOptions {
	if a == nil || a.toolServices == nil || (a.toolServices.Ephemeral == nil && a.toolServices.EphemeralTerminal == nil) {
		return opts
	}
	seen := make(map[string]struct{}, len(opts.Tools))
	for _, item := range opts.Tools {
		if name := functionToolNameFromSchema(item); name != "" {
			seen[name] = struct{}{}
		}
	}
	if a.toolServices.Ephemeral != nil {
		for _, item := range a.toolServices.Ephemeral.VisibleTools(sessionID) {
			name := functionToolNameFromSchema(item)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			opts.Tools = append(opts.Tools, item)
			seen[name] = struct{}{}
		}
	}
	if a.toolServices.EphemeralTerminal != nil {
		for _, item := range a.toolServices.EphemeralTerminal.VisibleTools(sessionID) {
			name := functionToolNameFromSchema(item)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			opts.Tools = append(opts.Tools, item)
			seen[name] = struct{}{}
		}
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
		if toolNameInSet(disabledSet, functionToolNameFromSchema(t)) {
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
	if forcedToolChoiceName(opts.ToolChoice) == "" && hasPendingComputerActDirective(messages) {
		opts = forceComputerActToolChoice(opts)
	}
	if forceSearchSynthesis {
		opts.Tools = nil
		opts.ToolChoice = "none"
	}
	return opts
}

func hasPendingComputerActDirective(messages []provider.Message) bool {
	directiveIndex, actionIndex := -1, -1
	for i, message := range messages {
		if strings.HasPrefix(strings.TrimSpace(message.Content), computerActDirectiveMarker) {
			directiveIndex = i
		}
		if message.Role != "assistant" {
			continue
		}
		for _, call := range message.ToolCalls {
			if strings.TrimSpace(call.Name) == "computer_act" {
				actionIndex = i
			}
		}
	}
	return directiveIndex > actionIndex
}

func forceComputerActToolChoice(opts provider.CallOptions) provider.CallOptions {
	tools := filterFunctionToolsByName(opts.Tools, "computer_act")
	if len(tools) == 0 {
		return opts
	}
	opts.Tools = tools
	opts.ToolChoice = map[string]any{
		"type":     "function",
		"function": map[string]any{"name": "computer_act"},
	}
	return opts
}

func (a *Agent) chatLoopIteration(ctx context.Context, messages []provider.Message, base provider.CallOptions, forceSearchSynthesis bool, turnProvider providerSnapshot) (*provider.Response, error) {
	if !turnProvider.valid() {
		return nil, fmt.Errorf("provider not initialized")
	}
	opts := prepareLoopCallOptions(messages, base, forceSearchSynthesis)
	messages, opts, err := a.prepareModelRequest(messages, opts, turnProvider)
	if err != nil {
		return nil, err
	}
	if fcProvider, ok := turnProvider.provider.(provider.FunctionCallingProvider); ok && len(opts.Tools) > 0 {
		return fcProvider.ChatWithOptions(ctx, messages, opts)
	}
	return turnProvider.provider.Chat(ctx, messages)
}

const maxAgentIterationSoftRetryTimeout = 60 * time.Second

func agentIterationSoftRetryTimeout(parent context.Context, err error, originalTimeout time.Duration) (time.Duration, bool) {
	if err == nil || errors.Is(err, context.Canceled) || (parent != nil && parent.Err() != nil) {
		return 0, false
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "timeout") && !strings.Contains(message, "timed out") && !strings.Contains(message, "deadline exceeded") {
			return 0, false
		}
	}

	retryTimeout := originalTimeout
	if retryTimeout <= 0 || retryTimeout > maxAgentIterationSoftRetryTimeout {
		retryTimeout = maxAgentIterationSoftRetryTimeout
	}
	return retryTimeout, true
}

// chatLoopIterationWithSoftRetry gives a timed-out model round one fresh,
// shorter deadline before its caller handles the failure. Tool execution is
// outside this call, so retrying here cannot replay completed tool operations.
func (a *Agent) chatLoopIterationWithSoftRetry(
	parent context.Context,
	timeout time.Duration,
	messages []provider.Message,
	base provider.CallOptions,
	forceSearchSynthesis bool,
	turnProvider providerSnapshot,
	onRetry func(),
) (*provider.Response, error, bool) {
	if parent == nil {
		parent = context.Background()
	}
	attempt := func(attemptTimeout time.Duration) (*provider.Response, error) {
		if attemptTimeout <= 0 {
			return a.chatLoopIteration(parent, messages, base, forceSearchSynthesis, turnProvider)
		}
		attemptCtx, cancel := context.WithTimeout(parent, attemptTimeout)
		defer cancel()
		return a.chatLoopIteration(attemptCtx, messages, base, forceSearchSynthesis, turnProvider)
	}

	resp, err := attempt(timeout)
	retryTimeout, retry := agentIterationSoftRetryTimeout(parent, err, timeout)
	if !retry {
		return resp, err, false
	}
	logger.Warn("agent model iteration timed out; retrying once",
		"retry_timeout_ms", retryTimeout.Milliseconds(),
		"error", err,
	)
	if onRetry != nil {
		onRetry()
	}
	resp, err = attempt(retryTimeout)
	return resp, err, true
}

func (a *Agent) streamLoopIteration(ctx context.Context, messages []provider.Message, base provider.CallOptions, forceSearchSynthesis bool, turnProvider providerSnapshot) (<-chan provider.StreamChunk, error) {
	if !turnProvider.valid() {
		return nil, fmt.Errorf("provider not initialized")
	}
	opts := prepareLoopCallOptions(messages, base, forceSearchSynthesis)
	messages, opts, err := a.prepareModelRequest(messages, opts, turnProvider)
	if err != nil {
		return nil, err
	}
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
	return a.executeToolCallsOrderedContext(context.Background(), toolCalls, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, allowMixedParallel, sourceOpt...)
}

func (a *Agent) executeToolCallsOrderedContext(
	ctx context.Context,
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
	executionOpts := []string{source}
	if len(sourceOpt) > 1 {
		executionOpts = append(executionOpts, sourceOpt[1])
	}

	runOne := func(idx int, tc provider.ToolCall) {
		start := time.Now()
		toolResult, err := a.executeToolMaybeDedupDetailedContext(ctx, tc.Name, tc.Arguments, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, executionOpts...)
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
		resultCh <- executedToolCall{
			Index:        idx,
			ToolCall:     tc,
			Result:       resultText,
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
	if redundant, target := redundantComputerObservation(messages, executed); redundant {
		// The model already has this display from the preceding action. Replacing
		// it with another full screenshot costs a model turn and shows nothing new.
		messages = append(removeComputerObservationReuse(messages), provider.Message{Role: "user", Content: computerObservationReuseNote(target)})
		return messages
	}
	messages = removeTransientComputerObservations(messages)
	messages = removeComputerObservationReuse(messages)
	var latest *tool.Observation
	for i := range executed {
		for j := range executed[i].Observations {
			obs := executed[i].Observations[j]
			if imageReadCall(executed[i].ToolCall) || obs.Kind == "mcp_image" {
				part := provider.ContentPart{Type: "image", Image: &provider.ImagePart{FilePath: obs.FilePath, URL: obs.ImageURL, MimeType: obs.MimeType}}
				if part.Image.FilePath == "" && len(obs.ImageData) > 0 {
					part.Image.URL = "data:" + imageMimeType(obs.MimeType) + ";base64," + base64.StdEncoding.EncodeToString(obs.ImageData)
				}
				messages = append(messages, provider.Message{Role: "user", Content: "[Loaded image]", ContentParts: []provider.ContentPart{part}})
				continue
			}
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

// redundantComputerObservation reports a computer_observe that only repeats a
// frame already shown to the model. A different display or window is a new
// target and still needs its screenshot. The capture itself has already run;
// this only stops the duplicate image from starting another model turn.
func redundantComputerObservation(messages []provider.Message, executed []executedToolCall) (bool, string) {
	if !computerFrameCameFromAction(messages) {
		return false, ""
	}
	var observeTarget string
	observed := false
	for _, call := range executed {
		switch strings.TrimSpace(call.ToolCall.Name) {
		case "computer_act":
			return false, ""
		case "computer_observe":
			observed = true
			observeTarget = computerObservationTarget(call.ToolCall.Arguments)
		}
	}
	if !observed {
		return false, ""
	}
	return true, observeTarget
}

func computerFrameCameFromAction(messages []provider.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !isTransientComputerObservation(msg) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			previous := messages[j]
			if previous.Role != "tool" {
				continue
			}
			switch strings.TrimSpace(previous.Name) {
			case "computer_act":
				return true
			case "computer_observe":
				return false
			}
		}
		return false
	}
	return false
}

func computerObservationReuseNote(target string) string {
	note := "[computer observation reused] The current frame is already in context. Use it; do not request another screenshot of the same display unless the target changed."
	if target != "" {
		note += " Requested target: " + target + "."
	}
	return note
}

func removeComputerObservationReuse(messages []provider.Message) []provider.Message {
	out := messages[:0]
	for _, msg := range messages {
		if msg.Role == "user" && strings.HasPrefix(strings.TrimSpace(msg.Content), "[computer observation reused]") {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func computerObservationTarget(arguments string) string {
	var args struct {
		DisplayID string `json:"display_id"`
		Window    string `json:"window"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return ""
	}
	if target := strings.TrimSpace(args.Window); target != "" {
		return target
	}
	return strings.TrimSpace(args.DisplayID)
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
	return a.executeToolCallsOrderedGuardedContext(context.Background(), toolCalls, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, allowMixedParallel, guard, sourceOpt...)
}

func (a *Agent) executeToolCallsOrderedGuardedContext(
	ctx context.Context,
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
		return a.executeToolCallsOrderedContext(ctx, toolCalls, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, allowMixedParallel, sourceOpt...)
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
		// Providers receive OpenAI-compatible names (for example
		// codex_start_turn), while guard rules use the registry's canonical
		// names (codex.start_turn). Resolve the name before policy checks so an
		// aliased call cannot bypass the turn guard.
		guardCall := tc
		guardCall.Name = a.canonicalToolName(tc.Name)
		if msg, blocked := guard.blockMessage(guardCall); blocked {
			result := executedToolCall{Index: idx, ToolCall: tc, Result: msg}
			blockedResults[idx] = result
			a.recordProactiveToolEvent(sess, result, true)
			continue
		}
		if hooksActive {
			finalArgs, blocked, blockMsg := a.hooks.RunPre(a.canonicalToolName(tc.Name), tc.Arguments, source, sessionID)
			if blocked {
				result := executedToolCall{Index: idx, ToolCall: tc, Result: blockMsg}
				blockedResults[idx] = result
				a.recordProactiveToolEvent(sess, result, true)
				continue
			}
			tc.Arguments = finalArgs
			guardCall.Arguments = finalArgs
			if msg, blocked := guard.blockMessage(guardCall); blocked {
				result := executedToolCall{Index: idx, ToolCall: tc, Result: msg}
				blockedResults[idx] = result
				a.recordProactiveToolEvent(sess, result, true)
				continue
			}
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
			one := a.executeToolCallsOrderedContext(ctx, []provider.ToolCall{tc}, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, false, sourceOpt...)
			executed = append(executed, one...)
		}
	} else {
		executed = a.executeToolCallsOrderedContext(ctx, allowed, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit, allowMixedParallel, sourceOpt...)
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
	contextResult := compactToolResultForContextKey(toolName, rawResult, "", strings.HasPrefix(strings.TrimSpace(rawResult), "[Tool Error:"))
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
		events <- ChatEvent{
			Type:    ChatEventToolCall,
			Name:    tc.Name,
			Args:    tc.Arguments,
			Content: fmt.Sprintf("🔧 %s", tc.Name),
		}
	}
}

func emitChatToolResultEvent(events chan<- ChatEvent, toolName, result string, metadata map[string]any, duration time.Duration) {
	if delta, note := takeOpenUIDelta(toolName, result); delta != nil {
		events <- ChatEvent{Type: ChatEventUIDelta, UI: delta}
		result = note
	}
	events <- ChatEvent{
		Type:     ChatEventToolResult,
		Name:     toolName,
		Result:   result,
		Duration: duration,
		Content:  fmt.Sprintf("📋 %s → %s", toolName, result),
	}
	if memoryTracePayload, ok := marshalMemoryTracePayload(metadata); ok {
		events <- ChatEvent{
			Type:    ChatEventToolResult,
			Name:    chatEventMemoryTraceName,
			Result:  memoryTracePayload,
			Content: "Memory Trace",
		}
	}
}

func emitChatObservationEvents(events chan<- ChatEvent, result executedToolCall) {
	emitObservationEvents(func(event ChatEvent) { events <- event }, result)
}

func emitObservationEvents(emit func(ChatEvent), result executedToolCall) {
	for _, obs := range result.Observations {
		// image_read supplies model input, not a new artifact to send back to
		// the user. Computer tools use the same image observation payload.
		if imageReadCall(result.ToolCall) {
			continue
		}
		if strings.TrimSpace(obs.FrameID) == "" && strings.TrimSpace(obs.FilePath) == "" && len(obs.ImageData) == 0 {
			continue
		}
		emit(ChatEvent{
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
		})
	}
	if approval := approvalEventFromMetadata(result.Metadata, result.ToolCall.Name); approval != nil {
		emit(ChatEvent{Type: ChatEventApprovalRequired, Name: result.ToolCall.Name, Content: "Approval required", Approval: approval})
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
		e.Kind, _ = m["kind"].(string)
		e.Prompt, _ = m["prompt"].(string)
		if secure, ok := m["secure"].(bool); ok {
			e.Secure = secure
		}
		e.Fields = credentialFieldsFromForm(m)
	}
	return e
}

func credentialFieldsFromForm(form map[string]any) map[string]string {
	fields := map[string]string{}
	for _, key := range []string{"id", "credential_kind", "scope", "bind"} {
		value, _ := form[key].(string)
		value = strings.TrimSpace(value)
		if value != "" {
			fields[key] = value
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

const chatEventMemoryTraceName = "__memory_trace"

func marshalMemoryTracePayload(metadata map[string]any) (payload string, ok bool) {
	if len(metadata) == 0 {
		return "", false
	}
	trace, ok := metadata["memory_trace"]
	if !ok || trace == nil {
		return "", false
	}
	data, err := json.Marshal(trace)
	if err != nil || len(data) == 0 {
		return "", false
	}
	return string(data), true
}
