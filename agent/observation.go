package agent

import (
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

func toolObservation(exec executedToolCall) (started *time.Time, durationMS int64, status string) {
	// Duration is the wall clock from execute start to result, including
	// timeout, non-zero exit, and sandbox reject. Zero means the call was
	// not measured; do not invent a start time for that case.
	if exec.Duration > 0 {
		begin := time.Now().Add(-exec.Duration)
		started = &begin
		durationMS = exec.Duration.Milliseconds()
		if durationMS < 1 {
			durationMS = 1
		}
	}
	return started, durationMS, toolResultStatus(exec.Result)
}

func toolResultStatus(result string) string {
	text := strings.TrimSpace(result)
	switch {
	case strings.Contains(text, "command timed out"):
		return "timeout"
	case strings.Contains(text, "restricted path") || strings.Contains(text, "outside sandbox") || strings.Contains(text, "access denied"):
		return "sandbox"
	case strings.Contains(text, "password is required") || strings.Contains(text, "terminal is required"):
		return "sudo"
	case strings.Contains(text, "executable file not found") || strings.Contains(text, "not found in $PATH") || strings.Contains(text, "pdftotext"):
		return "missing"
	case strings.HasPrefix(text, "Error:") || strings.HasPrefix(text, "error:") || strings.Contains(text, "[exit code:"):
		return "error"
	default:
		return "ok"
	}
}

func observedToolMessage(runID string, exec executedToolCall, content string) provider.Message {
	started, durationMS, status := toolObservation(exec)
	return provider.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: exec.ToolCall.ID,
		Name:       exec.ToolCall.Name,
		RunID:      runID,
		StartedAt:  started,
		DurationMS: durationMS,
		Status:     status,
	}
}
