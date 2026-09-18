package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/computer"
)

type workflowComputerManager struct {
	fakeComputerManager
	request  computer.ObserveRequest
	actions  []computer.Action
	deadline bool
}

func (f *workflowComputerManager) Observe(ctx context.Context, _ string, req computer.ObserveRequest) (computer.Observation, error) {
	f.request = req
	_, f.deadline = ctx.Deadline()
	return computer.Observation{FrameID: "tree-1", Accessibility: &computer.AccessibilityTree{Nodes: []computer.AccessibilityNode{{ID: "control", Role: "text", Enabled: true, Editable: true}}}}, nil
}
func (f *workflowComputerManager) StepBatch(ctx context.Context, _ string, actions []computer.Action) (computer.Observation, error) {
	f.actions = actions
	_, f.deadline = ctx.Deadline()
	return computer.Observation{FrameID: "frame-2", FilePath: "/tmp/frame.png", Width: 100, Height: 80}, nil
}

func TestComputerTreeAndRegionArguments(t *testing.T) {
	m := &workflowComputerManager{}
	s := NewComputerUseToolService(m, ComputerUseConfig{Enabled: true, StepTimeoutSeconds: 2})
	result, err := s.observe(ExecutionContext{SessionID: "test"}, map[string]any{"format": "both", "window": "active", "region": map[string]any{"x": 10.0, "y": 20.0, "width": 100.0, "height": 80.0}})
	if err != nil {
		t.Fatal(err)
	}
	if m.request.Format != "both" || m.request.Target.Region.Width != 100 || !m.deadline {
		t.Fatal("missing observation request/deadline", m.request)
	}
	if len(result.Observations) != 0 || !strings.Contains(result.Output, `"id":"control"`) {
		t.Fatal("tree not delivered in model-readable output", result)
	}
	for _, region := range []any{"bad", map[string]any{"x": 1.1, "y": 0, "width": 20, "height": 20}, map[string]any{"x": 0, "y": 0, "width": -1, "height": 20}} {
		if _, err := s.observe(ExecutionContext{SessionID: "test"}, map[string]any{"region": region}); err == nil {
			t.Fatalf("accepted invalid region %#v", region)
		}
	}
}

func TestComputerBatchPoliciesBeforeAnyInput(t *testing.T) {
	m := &workflowComputerManager{}
	cfg := ComputerUseConfig{Enabled: true, Mode: "control", RequireApproval: true, AllowTextInput: false}
	s := NewComputerUseToolService(m, cfg)
	args := map[string]any{"frame_id": "frame-1", "reason": "fill input", "actions": []any{map[string]any{"action": "click", "x": 1, "y": 2}, map[string]any{"action": "type", "text": " 你好\n"}}}
	_, err := s.act(ExecutionContext{SessionID: "test"}, args)
	var approval *ApprovalRequiredError
	if !errors.As(err, &approval) || len(approval.Actions) != 2 || len(m.actions) != 0 {
		t.Fatal("batch approval lost actions", err)
	}
	_, err = s.act(ExecutionContext{SessionID: "test", AutoApprove: true}, args)
	if err == nil || len(m.actions) != 0 {
		t.Fatal("batch bypassed text-input policy")
	}
	s.config.AllowTextInput = true
	result, err := s.act(ExecutionContext{SessionID: "test", AutoApprove: true}, args)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.actions) != 2 || m.actions[1].Text != " 你好\n" || m.actions[1].FrameID != "frame-1" || result.Metadata["completed_actions"] != 2 || !m.deadline {
		t.Fatal("incorrect batch forwarding", m.actions, result)
	}
}

func TestComputerSetTextRequiresInputPermission(t *testing.T) {
	m := &workflowComputerManager{}
	s := NewComputerUseToolService(m, ComputerUseConfig{Enabled: true, Mode: "control"})
	_, err := s.act(ExecutionContext{SessionID: "test", AutoApprove: true}, map[string]any{"action": "set_text", "frame_id": "tree-1", "element_id": "control", "text": "", "reason": "clear entry"})
	if err == nil || !strings.Contains(err.Error(), "text input is disabled") || m.stepped.Kind != "" {
		t.Fatal("set_text bypassed input permission")
	}
}

func TestComputerRejectsMalformedPointerCoordinates(t *testing.T) {
	for _, args := range []map[string]any{
		{"action": "click", "frame_id": "x", "y": 1},
		{"action": "click", "frame_id": "x", "x": "100", "y": 1},
		{"action": "drag", "frame_id": "x", "x": 1, "y": 1, "end_x": 1.5, "end_y": 2},
	} {
		args["reason"] = "test invalid coordinates"
		if _, err := parseComputerAction(args); err == nil {
			t.Fatalf("invalid coordinates accepted: %#v", args)
		}
	}
}

func TestComputerOperationPreservesEarlierDeadline(t *testing.T) {
	s := NewComputerUseToolService(nil, ComputerUseConfig{StepTimeoutSeconds: 30})
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	child, done := s.operationContext(ExecutionContext{Context: parent})
	defer done()
	p, _ := parent.Deadline()
	c, _ := child.Deadline()
	if !p.Equal(c) {
		t.Fatal("tool timeout extended the parent deadline")
	}
}

func TestComputerNestedToolSchemas(t *testing.T) {
	s := NewComputerUseToolService(nil, ComputerUseConfig{})
	format := s.ActTool().ToOpenAIFormat()
	params := format["function"].(map[string]any)["parameters"].(map[string]any)["properties"].(map[string]any)
	items := params["actions"].(map[string]any)["items"].(map[string]any)
	if items["type"] != "object" || items["properties"].(map[string]any)["action"] == nil {
		t.Fatal("batch actions incorrectly advertised as strings", items)
	}
	if params["keys"].(map[string]any)["items"].(map[string]any)["type"] != "string" {
		t.Fatal("keyboard array schema regressed")
	}
	region := s.ObserveTool().ToOpenAIFormat()["function"].(map[string]any)["parameters"].(map[string]any)["properties"].(map[string]any)["region"].(map[string]any)
	if region["properties"].(map[string]any)["width"].(map[string]any)["type"] != "integer" {
		t.Fatal("region schema missing integer dimensions")
	}
}
