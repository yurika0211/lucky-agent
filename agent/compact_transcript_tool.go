package agent

import "strings"

// transcriptToolLimit matches the compact engine's per-tool transcript cap. The
// engine replaces a tool message body with whatever SummarizeTool returns, so
// returning the compressed body keeps identifiers that only appear part way
// through a long result instead of describing the result in one sentence.
const transcriptToolLimit = 220

// transcriptToolSummary is the TextOptions.SummarizeTool hook handed to the
// compact engine. A one-line description satisfies the shape of a summary but
// drops the file path, error head, or value the next turn needs, so the hook
// returns the compressed body itself.
func transcriptToolSummary(toolName, result string) string {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return ""
	}
	compressed := compressToolResultLines(trimmed, transcriptToolLimit)
	if len([]rune(compressed)) > transcriptToolLimit {
		// Identifier lines outrank the budget inside compressToolResultLines. The
		// engine still caps this message, so cut the middle here to keep both the
		// head and the tail rather than letting a positional cut take one end.
		compressed = truncateToolResultMiddle(compressed, transcriptToolLimit)
	}
	return compressed
}
