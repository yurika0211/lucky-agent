package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

func TestToolMessageDurationRoundTrips(t *testing.T) {
	msg := observedToolMessage("run-1", executedToolCall{
		ToolCall: provider.ToolCall{ID: "call-1", Name: "terminal"},
		Result:   "failed [exit code: 1]",
		Duration: 1500 * time.Millisecond,
	}, "failed [exit code: 1]")
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var decoded provider.Message
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.DurationMS != 1500 || decoded.ToolCallID != "call-1" || decoded.Name != "terminal" {
		t.Fatalf("decoded = %#v", decoded)
	}
	var old provider.Message
	if err := json.Unmarshal([]byte(`{"role":"tool","content":"ok","tool_call_id":"old","name":"terminal"}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.DurationMS != 0 {
		t.Fatalf("missing duration_ms decoded as %d", old.DurationMS)
	}
}
