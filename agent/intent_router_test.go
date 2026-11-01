package agent

import "testing"

func TestParseIntentProfile(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want RequestProfile
	}{
		{"json", `{"profile":"code_task"}`, RequestProfileCodeTask},
		{"fenced", "```json\n{\"profile\":\"tool_task\"}\n```", RequestProfileToolTask},
		{"extra", `result: {"profile":"chat"}`, RequestProfileChat},
		{"invalid", `{"profile":"unknown"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseIntentProfile(tt.in); got != tt.want {
				t.Fatalf("parseIntentProfile(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
