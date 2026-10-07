package tool

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ApprovalOption is the client-facing choice for a pending external-agent
// permission request. ID is the exact value accepted by the provider bridge.
type ApprovalOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"`
}

// PendingApproval is deliberately provider-neutral so HTTP and mobile clients
// do not need to know the Codex JSON-RPC or Grok ACP wire formats.
type PendingApproval struct {
	Provider  string           `json:"provider"`
	ID        string           `json:"id"`
	Method    string           `json:"method,omitempty"`
	ThreadID  string           `json:"thread_id,omitempty"`
	SessionID string           `json:"session_id,omitempty"`
	TurnID    string           `json:"turn_id,omitempty"`
	Reason    string           `json:"reason,omitempty"`
	Summary   string           `json:"summary,omitempty"`
	Params    map[string]any   `json:"params,omitempty"`
	Options   []ApprovalOption `json:"options"`
	CreatedAt time.Time        `json:"created_at"`
}

// PendingApprovals aggregates optional external coding-agent bridges.
// Current Codex and Grok services resolve approvals through their own tools,
// so this list stays empty until those bridges expose a snapshot again.
func (s *Services) PendingApprovals() []PendingApproval {
	if s == nil {
		return nil
	}
	return nil
}

// RespondApproval resolves a pending approval using the provider-neutral API.
func (s *Services) RespondApproval(ctx context.Context, provider, approvalID, decision string) (PendingApproval, error) {
	if s == nil {
		return PendingApproval{}, fmt.Errorf("approval services are unavailable")
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "codex":
		if s.Codex == nil {
			return PendingApproval{}, fmt.Errorf("codex approval service is disabled")
		}
		if _, err := s.Codex.respondApproval(map[string]any{
			"approval_id": approvalID,
			"decision":    decision,
		}); err != nil {
			return PendingApproval{}, err
		}
		return PendingApproval{Provider: "codex", ID: approvalID}, nil
	case "grok":
		if s.Grok == nil {
			return PendingApproval{}, fmt.Errorf("grok approval service is disabled")
		}
		if _, err := s.Grok.respondApproval(map[string]any{
			"approval_id": approvalID,
			"decision":    decision,
		}); err != nil {
			return PendingApproval{}, err
		}
		return PendingApproval{Provider: "grok", ID: approvalID}, nil
	default:
		return PendingApproval{}, fmt.Errorf("unsupported approval provider %q", provider)
	}
}

func approvalOptionLabel(kind, name, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "allow_once", "accept_once", "accept":
		return "允许一次"
	case "allow_always", "accept_for_session", "acceptforsession":
		return "本会话允许同类"
	case "reject_once", "deny", "decline", "reject":
		return "拒绝"
	case "cancel", "cancelled":
		return "取消"
	}
	if text := strings.TrimSpace(name); text != "" {
		return text
	}
	return strings.TrimSpace(fallback)
}
