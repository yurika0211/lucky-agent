package server

import (
	"net/http"
	"strings"

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
	records := sessionToolTraceRecords(sess.GetMessages(), templates)
	successes := 0
	failures := 0
	for _, record := range records {
		if record.Success {
			successes++
		} else {
			failures++
		}
	}
	rate := 0.0
	if len(records) > 0 {
		rate = float64(successes) / float64(len(records))
	}

	s.sendJSON(w, http.StatusOK, map[string]any{
		"session_id":   sess.ID,
		"tools":        records,
		"total_calls":  len(records),
		"successes":    successes,
		"failures":     failures,
		"success_rate": rate,
	})
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
