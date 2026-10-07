package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/tool"
)

type approvalsResponse struct {
	Approvals []tool.PendingApproval `json:"approvals"`
	Count     int                    `json:"count"`
}

type approvalResolutionRequest struct {
	Provider   string `json:"provider"`
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"`
	Input      string `json:"input,omitempty"`
}

// handleApprovals lists pending Codex/Grok gateway approvals and resolves one
// from a client-facing choice. Values are intentionally provider-neutral so
// Android and other clients can render the same approval card.
func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	if s.agent == nil {
		s.sendError(w, "agent unavailable", http.StatusServiceUnavailable, "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		approvals := s.agent.PendingApprovals()
		provider := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
		if provider != "" {
			filtered := approvals[:0]
			for _, approval := range approvals {
				if strings.EqualFold(approval.Provider, provider) {
					filtered = append(filtered, approval)
				}
			}
			approvals = filtered
		}
		if sessionID := strings.TrimSpace(r.URL.Query().Get("session_id")); sessionID != "" {
			filtered := approvals[:0]
			for _, approval := range approvals {
				if approval.SessionID == sessionID {
					filtered = append(filtered, approval)
				}
			}
			approvals = filtered
		}
		if approvals == nil {
			approvals = []tool.PendingApproval{}
		}
		s.sendJSON(w, http.StatusOK, approvalsResponse{Approvals: approvals, Count: len(approvals)})
	case http.MethodPost:
		var req approvalResolutionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
			s.sendError(w, "invalid approval request", http.StatusBadRequest, err.Error())
			return
		}
		req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
		req.ApprovalID = strings.TrimSpace(req.ApprovalID)
		req.Decision = strings.TrimSpace(req.Decision)
		if req.Provider == "" || req.ApprovalID == "" || req.Decision == "" {
			s.sendError(w, "provider, approval_id and decision are required", http.StatusBadRequest, "")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		resolved, err := s.agent.RespondApprovalWithInput(ctx, req.Provider, req.ApprovalID, req.Decision, req.Input)
		if err != nil {
			s.sendError(w, "resolve approval failed", http.StatusBadRequest, err.Error())
			return
		}
		s.sendJSON(w, http.StatusOK, map[string]any{"status": "resolved", "approval": resolved})
	default:
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
	}
}
