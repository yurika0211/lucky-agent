package agent

import (
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

const compactSummaryTimeout = compactlib.SummaryTimeout

func toCompactMessages(messages []provider.Message) []compactlib.Message {
	out := make([]compactlib.Message, len(messages))
	for i, message := range messages {
		calls := make([]compactlib.ToolCall, len(message.ToolCalls))
		for j, call := range message.ToolCalls {
			calls[j] = compactlib.ToolCall{Name: call.Name}
		}
		out[i] = compactlib.Message{Role: message.Role, Content: message.Content, ReasoningContent: message.ReasoningContent, Name: message.Name, ToolCalls: calls, CompactBoundary: session.IsCompactBoundary(message)}
	}
	return out
}

func compactEstimator(est *contextx.TokenEstimator) compactlib.Estimator {
	if est == nil {
		return nil
	}
	return est
}

func compactTextOptions() compactlib.TextOptions {
	return compactlib.TextOptions{
		SummarizeTool:     summarizeToolResult,
		FallbackGoal:      "Continue the current LuckyAgent session from the available prior messages.",
		MergeFallbackGoal: "Continue the current LuckyAgent session from prior segment summaries.",
	}
}
