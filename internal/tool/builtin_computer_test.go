package tool

import (
	"context"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/computer"
)

type fakeComputerManager struct {
	observed bool
	stepped  computer.Action
}

func (f *fakeComputerManager) Observe(_ context.Context, sessionID string, _ computer.ObserveRequest) (computer.Observation, error) {
	f.observed = sessionID != ""
	return computer.Observation{
		FrameID: "frame-1", CapturedAt: time.Unix(1, 0), FilePath: "/tmp/frame.png", MimeType: "image/png", Width: 100, Height: 80, ScaleFactor: 1,
	}, nil
}

func (f *fakeComputerManager) Step(_ context.Context, sessionID string, action computer.Action) (computer.Observation, error) {
	if sessionID == "" {
		panic("session id must be forwarded")
	}
	f.stepped = action
	return computer.Observation{FrameID: "frame-2", FilePath: "/tmp/frame-2.png", MimeType: "image/png", Width: 100, Height: 80}, nil
}

func TestComputerToolsObserveAndAct(t *testing.T) {
	fake := &fakeComputerManager{}
	service := NewComputerUseToolService(fake, ComputerUseConfig{
		Enabled: true, Mode: "control", AllowTextInput: true,
	})
	r := NewRegistry()
	service.RegisterTools(r)

	observed, err := r.CallDetailedWithContext("computer_observe", map[string]any{"wait_ms": float64(10)}, ExecutionContext{Context: context.Background(), SessionID: "s1", Source: "cli"})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !fake.observed || len(observed.Observations) != 1 || observed.Observations[0].FrameID != "frame-1" {
		t.Fatalf("unexpected observation: %#v", observed)
	}

	acted, err := r.CallDetailedWithContext("computer_act", map[string]any{
		"action": "click", "frame_id": "frame-1", "x": float64(4), "y": float64(5), "reason": "open settings",
	}, ExecutionContext{Context: context.Background(), SessionID: "s1", Source: "cli", AutoApprove: true})
	if err != nil {
		t.Fatalf("act: %v", err)
	}
	if fake.stepped.Kind != computer.ActionClick || fake.stepped.X != 4 || fake.stepped.Y != 5 || len(acted.Observations) != 1 {
		t.Fatalf("unexpected action/result: %#v %#v", fake.stepped, acted)
	}
}

func TestComputerToolPolicies(t *testing.T) {
	fake := &fakeComputerManager{}
	service := NewComputerUseToolService(fake, ComputerUseConfig{Enabled: true, Mode: "observe"})
	r := NewRegistry()
	service.RegisterTools(r)
	_, err := r.CallDetailedWithContext("computer_act", map[string]any{"action": "click", "frame_id": "frame-1", "x": 1, "y": 1, "reason": "test"}, ExecutionContext{SessionID: "s1", Source: "cli", AutoApprove: true})
	if err == nil {
		t.Fatal("expected observe mode to reject control actions")
	}

	service = NewComputerUseToolService(fake, ComputerUseConfig{Enabled: true, Mode: "control", AllowedSources: []string{"tui"}})
	r = NewRegistry()
	service.RegisterTools(r)
	_, err = r.CallDetailedWithContext("computer_observe", nil, ExecutionContext{SessionID: "s1", Source: "telegram"})
	if err == nil {
		t.Fatal("expected source policy rejection")
	}
}

func TestComputerActionAliases(t *testing.T) {
	fake := &fakeComputerManager{}
	service := NewComputerUseToolService(fake, ComputerUseConfig{Enabled: true, Mode: "control", AllowTextInput: true})
	r := NewRegistry()
	service.RegisterTools(r)
	_, err := r.CallDetailedWithContext("computer_act", map[string]any{
		"action": "left_click", "frame_id": "frame-1", "x": float64(1), "y": float64(2), "reason": "close tab",
	}, ExecutionContext{Context: context.Background(), SessionID: "s1", Source: "telegram", AutoApprove: true})
	if err != nil {
		t.Fatalf("left_click alias: %v", err)
	}
	if fake.stepped.Kind != computer.ActionClick {
		t.Fatalf("expected click alias, got %q", fake.stepped.Kind)
	}
}

func TestComputerKeyCombinationInputFormats(t *testing.T) {
	for _, keys := range []any{"ALT+TAB", []string{"ALT+TAB"}, []any{"ALT+TAB"}} {
		fake := &fakeComputerManager{}
		service := NewComputerUseToolService(fake, ComputerUseConfig{Enabled: true, Mode: "control"})
		r := NewRegistry()
		service.RegisterTools(r)
		_, err := r.CallDetailedWithContext("computer_act", map[string]any{
			"action": "keypress", "keys": keys, "frame_id": "frame-1", "reason": "switch window",
		}, ExecutionContext{Context: context.Background(), SessionID: "s1", Source: "telegram", AutoApprove: true})
		if err != nil {
			t.Fatalf("keys=%#v: %v", keys, err)
		}
		if len(fake.stepped.Keys) != 2 || fake.stepped.Keys[0] != "ALT" || fake.stepped.Keys[1] != "TAB" {
			t.Fatalf("keys=%#v parsed as %#v", keys, fake.stepped.Keys)
		}
	}
}
