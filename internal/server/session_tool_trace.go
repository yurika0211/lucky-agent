package server

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
)

// handleSessionToolTrace returns a completed, annotated trace reconstructed
// from the session's persisted provider messages.
func (s *Server) handleSessionToolTrace(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	var templates map[string]string
	if s.agent != nil && s.agent.Config() != nil {
		templates = s.agent.Config().Get().ToolTrace.Templates
	}
	allRecords := sessionToolTraceRecords(sess.GetMessages(), templates)
	successes := 0
	failures := 0
	for _, record := range allRecords {
		if record.Success {
			successes++
		} else {
			failures++
		}
	}
	rate := 0.0
	if len(allRecords) > 0 {
		rate = float64(successes) / float64(len(allRecords))
	}
	limit := boundedQueryInt(r, "limit", defaultToolTraceLimit, 1, maxToolTraceLimit)
	offset := boundedQueryInt(r, "offset", 0, 0, 1_000_000)
	start, end := tracePageBounds(len(allRecords), limit, offset)
	records := truncateTraceRecords(allRecords[start:end])

	s.sendJSON(w, http.StatusOK, map[string]any{
		"session_id":   sess.ID,
		"tools":        records,
		"total_calls":  len(allRecords),
		"successes":    successes,
		"failures":     failures,
		"success_rate": rate,
		"limit":        limit,
		"offset":       offset,
		"returned":     len(records),
		"has_more":     start > 0,
	})
}

func tracePageBounds(total, limit, offset int) (int, int) {
	if total <= 0 || offset >= total {
		return total, total
	}
	end := total - offset
	start := end - limit
	if start < 0 {
		start = 0
	}
	return start, end
}

const (
	traceArgumentMaxChars   = 4096
	traceResultMaxChars     = 4096
	traceAnnotationMaxChars = 1024
	traceErrorMaxChars      = 1024
)

func truncateTraceRecords(records []tool.TraceRecord) []tool.TraceRecord {
	result := make([]tool.TraceRecord, len(records))
	for i, record := range records {
		record.Arguments = truncateTraceText(record.Arguments, traceArgumentMaxChars)
		record.Result = truncateTraceText(record.Result, traceResultMaxChars)
		record.Annotation = truncateTraceText(record.Annotation, traceAnnotationMaxChars)
		record.Error = truncateTraceText(record.Error, traceErrorMaxChars)
		result[i] = record
	}
	return result
}

func truncateTraceText(text string, maxChars int) string {
	if maxChars <= 0 || utf8.RuneCountInString(text) <= maxChars {
		return text
	}
	marker := "\n...[truncated]"
	keep := maxChars - utf8.RuneCountInString(marker)
	if keep < 0 {
		keep = 0
	}
	return string([]rune(text)[:keep]) + marker
}

func sessionToolTraceRecords(messages []provider.Message, templateSets ...map[string]string) []tool.TraceRecord {
	records := make([]tool.TraceRecord, 0)
	var templates map[string]string
	if len(templateSets) > 0 {
		templates = templateSets[0]
	}
	pendingByID := make(map[string]int)
	pendingByName := make(map[string][]int)

	for _, message := range messages {
		if message.Role == "assistant" {
			for _, call := range message.ToolCalls {
				records = append(records, tool.NewTraceRecordWithTemplates(call.Name, call.Arguments, "", 0, templates))
				index := len(records) - 1
				if call.ID != "" {
					pendingByID[call.ID] = index
				}
				name := strings.TrimSpace(call.Name)
				pendingByName[name] = append(pendingByName[name], index)
			}
			continue
		}
		if message.Role != "tool" {
			continue
		}

		index, found := pendingByID[message.ToolCallID]
		if !found {
			name := strings.TrimSpace(message.Name)
			for _, candidate := range pendingByName[name] {
				if records[candidate].Result == "" {
					index, found = candidate, true
					break
				}
			}
		}
		if !found {
			records = append(records, tool.NewTraceRecordWithTemplates(message.Name, "", message.Content, 0, templates))
			continue
		}

		records[index] = tool.NewTraceRecordWithTemplates(records[index].Name, records[index].Arguments, message.Content, 0, templates)
	}
	return records
}
