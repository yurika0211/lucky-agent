package sdk

import (
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/provider"
)

func TestMapEventCarriesApprovalObservationAndUsage(t *testing.T) {
	now := time.Now()
	ev := mapEvent(agent.ChatEvent{
		Type:    agent.ChatEventApprovalRequired,
		Content: "Approval required",
		Name:    "computer_act",
		Approval: &agent.ApprovalEvent{
			RequestID: "req-1",
			Tool:      "computer_act",
			Action:    "click",
			Reason:    "desktop control",
			FrameID:   "frame-9",
		},
		Observation: &agent.ObservationEvent{
			FrameID:      "frame-9",
			MimeType:     "image/png",
			Width:        1280,
			Height:       720,
			ActiveWindow: "Terminal",
		},
		Usage: &provider.TokenUsage{
			InputTokens:  3,
			OutputTokens: 5,
			TotalTokens:  8,
			Model:        "gpt-test",
		},
		CreatedAt: &now,
	})
	if ev.Type != EventApprovalRequired {
		t.Fatalf("type=%s", ev.Type)
	}
	if ev.Approval == nil || ev.Approval.RequestID != "req-1" || ev.Approval.FrameID != "frame-9" {
		t.Fatalf("approval=%+v", ev.Approval)
	}
	if ev.Observation == nil || ev.Observation.Width != 1280 || ev.Observation.ActiveWindow != "Terminal" {
		t.Fatalf("observation=%+v", ev.Observation)
	}
	if ev.Usage == nil || ev.Usage.TotalTokens != 8 || ev.Usage.Model != "gpt-test" {
		t.Fatalf("usage=%+v", ev.Usage)
	}
	if ev.CreatedAt == nil || ev.CreatedAt.UnixNano() != now.UnixNano() {
		t.Fatalf("createdAt=%v want %v", ev.CreatedAt, now)
	}
}
