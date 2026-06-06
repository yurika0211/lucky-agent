package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/function"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/session"
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
	opts := relaxForcedSkillToolChoice(messages, base)
	if forceSearchSynthesis {
		opts.Tools = nil
		opts.ToolChoice = "none"
	}
	return opts
}

func (a *Agent) chatLoopIteration(ctx context.Context, messages []provider.Message, base provider.CallOptions, forceSearchSynthesis bool) (*provider.Response, error) {
	opts := prepareLoopCallOptions(messages, base, forceSearchSynthesis)
	if fcProvider, ok := a.provider.(provider.FunctionCallingProvider); ok && len(opts.Tools) > 0 {
		return fcProvider.ChatWithOptions(ctx, messages, opts)
	}
	return a.provider.Chat(ctx, messages)
}

func (a *Agent) streamLoopIteration(ctx context.Context, messages []provider.Message, base provider.CallOptions, forceSearchSynthesis bool) (<-chan provider.StreamChunk, error) {
	opts := prepareLoopCallOptions(messages, base, forceSearchSynthesis)
	if fcProvider, ok := a.provider.(provider.FunctionCallingProvider); ok && len(opts.Tools) > 0 {
		return fcProvider.ChatStreamWithOptions(ctx, messages, opts)
	}
	return a.provider.ChatStream(ctx, messages)
}

type executedToolCall struct {
	Index       int
	ToolCall    provider.ToolCall
	Result      string
	ShortResult string
	Duration    time.Duration
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
		toolResult, err := a.executeToolMaybeDedup(tc.Name, tc.Arguments, autoApprove, sess, toolURLRepeatCount, toolURLLastResult, duplicateFetchLimit)
		if err != nil {
			toolResult = fmt.Sprintf("Error: %v", err)
		}
		shortResult := toolResult
		if len(shortResult) > 200 {
			shortResult = shortResult[:197] + "..."
		}
		resultCh <- executedToolCall{
			Index:       idx,
			ToolCall:    tc,
			Result:      toolResult,
			ShortResult: shortResult,
			Duration:    time.Since(start),
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
	return results
}

func buildContextToolResult(toolName, rawResult string, successfulSearchEvidence, detailedSearchEvidence *int) string {
	contextResult := compactToolResultForContext(toolName, rawResult)
	if isUsefulSearchEvidence(toolName, rawResult) {
		if successfulSearchEvidence != nil {
			*successfulSearchEvidence = *successfulSearchEvidence + 1
		}
		if toolName == "web_search" && detailedSearchEvidence != nil {
			if *detailedSearchEvidence >= 2 {
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
	events <- ChatEvent{
		Type:    ChatEventToolResult,
		Name:    toolName,
		Result:  shortResult,
		Content: fmt.Sprintf("📋 %s → %s", toolName, shortResult),
	}
}
