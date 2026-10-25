package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

func TestShouldHandOffExplorationAfterRepeatedLargeReads(t *testing.T) {
	state := newLoopRuntimeState()
	state.consecutiveToolOnlyIters = 2
	state.explorationBytes = explorationHandoffBytes
	executed := []executedToolCall{{
		ToolCall: provider.ToolCall{ID: "1", Name: "file_read"},
		Result:   strings.Repeat("x", 100),
	}}

	if !shouldHandOffExploration(DefaultLoopConfig(), state, executed) {
		t.Fatal("expected exploration handoff")
	}
}

func TestShouldNotHandOffExplorationForEditsOrSubagents(t *testing.T) {
	state := newLoopRuntimeState()
	state.consecutiveToolOnlyIters = 2
	state.explorationBytes = explorationHandoffBytes
	edit := []executedToolCall{{
		ToolCall: provider.ToolCall{ID: "1", Name: "file_read"},
	}, {
		ToolCall: provider.ToolCall{ID: "2", Name: "file_write"},
	}}
	if shouldHandOffExploration(DefaultLoopConfig(), state, edit) {
		t.Fatal("file writes must stay in the parent context")
	}

	child := DefaultLoopConfig()
	child.Source = "subagent"
	if shouldHandOffExploration(child, state, []executedToolCall{{
		ToolCall: provider.ToolCall{ID: "1", Name: "file_read"},
	}}) {
		t.Fatal("sub-agent loops must not hand off again")
	}
}

func TestReplaceExplorationToolResultsKeepsOnlyAShortRecord(t *testing.T) {
	raw := strings.Repeat("log line\n", 2000)
	executed := []executedToolCall{{
		ToolCall: provider.ToolCall{ID: "call-1", Name: "file_read"},
		Result:   raw,
	}}
	messages := []provider.Message{{
		Role:       "tool",
		ToolCallID: "call-1",
		Name:       "file_read",
		Content:    raw,
	}}

	replaced := replaceExplorationToolResults(messages, executed)
	if strings.Contains(replaced[0].Content, "log line") {
		t.Fatal("raw tool output remained in parent context")
	}
	if !strings.Contains(replaced[0].Content, "exploration handoff") {
		t.Fatalf("replacement = %q", replaced[0].Content)
	}
	if !strings.Contains(replaced[0].Content, "16000 bytes") && !strings.Contains(replaced[0].Content, "bytes") {
		t.Fatalf("replacement did not record the removed size: %q", replaced[0].Content)
	}
}

func TestExplorationHandoffClassifiesMergedActions(t *testing.T) {
	tests := []struct {
		name string
		args string
		read bool
	}{
		{"data", `{"action":"query"}`, true},
		{"data", `{"action":"log","mode":"grep"}`, true},
		{"data", `{"action":"csv"}`, true},
		{"data", `{"action":"sql"}`, true},
		{"data", `{"action":"schema"}`, true},
		{"memory", `{"action":"recall"}`, true},
		{"document_read", `{}`, true},
		{"log_file", `{}`, true},
		{"data_query", `{}`, true},
		{"recall", `{}`, true},
		{"file_patch", `{}`, false},
		{"file_delete", `{}`, false},
		{"file_move", `{}`, false},
		{"file_mkdir", `{}`, false},
		{"file_write", `{}`, false},
		{"rag_index", `{}`, false},
		{"memory", `{"action":"remember"}`, false},
		{"memory", `{"action":"hygiene","operation":"delete"}`, false},
		{"memory", `{}`, false},
		{"memory", `{broken`, false},
		{"data", `{"action":"unknown"}`, false},
		{"terminal", `{"command":"rm file.txt"}`, false},
		{"opencli", `{"action":"raw"}`, false},
		{"subagent", `{"action":"delegate"}`, false},
		{"computer_act", `{}`, false},
		{"request_credential", `{}`, false},
		{"new_unknown_tool", `{}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.args, func(t *testing.T) {
			call := provider.ToolCall{ID: "candidate", Name: tt.name, Arguments: tt.args}
			state := newLoopRuntimeState()
			state.consecutiveToolOnlyIters = 2
			state.noteExplorationResult(call, explorationHandoffBytes)
			if got := state.explorationBytes > 0; got != tt.read {
				t.Fatalf("counted as exploration = %v, want %v", got, tt.read)
			}
			state.explorationBytes = explorationHandoffBytes
			executed := []executedToolCall{
				{ToolCall: provider.ToolCall{ID: "read", Name: "file_read"}, Result: "read output"},
				{ToolCall: call, Result: "candidate output"},
			}
			if got := shouldHandOffExploration(DefaultLoopConfig(), state, executed); got != tt.read {
				t.Fatalf("mixed batch handoff = %v, want %v", got, tt.read)
			}
			messages := []provider.Message{{Role: "tool", ToolCallID: call.ID, Content: "candidate output"}}
			got := replaceExplorationToolResults(messages, executed)
			if replaced := got[0].Content != messages[0].Content; replaced != tt.read {
				t.Fatalf("result replaced = %v, want %v", replaced, tt.read)
			}
		})
	}
}
