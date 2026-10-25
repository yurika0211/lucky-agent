package agent

import "testing"

func TestExplicitReadSurvivesNegatedWrite(t *testing.T) {
	prompt := "严格按顺序做，不要解释。\n第一轮只调用一次 file_read，读取 internal/gateway/telegram/handler.go。\n不要调用 file_write、ask_user、subagent。"
	a := agentWithIntentGateTools(t)
	t.Setenv(toolIntentGatingEnv, "")
	loopCfg := DefaultLoopConfig()
	sanitizeLoopConfig(&loopCfg)

	a.applyIntentToolGating(&loopCfg, prompt)
	opts := a.buildLoopCallOptions(prompt, loopCfg)
	visible := toolNamesFromSchemas(opts.Tools)

	assertEnabledTools(t, visible, "file_read")
	if len(visible) != 1 || visible[0] != "file_read" {
		t.Fatalf("expected only the explicitly requested file_read schema, got %v", visible)
	}
}

func TestEnglishRequiredToolSchemaRespectsNegationWindow(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{name: "period after another tool", prompt: "Don't use terminal. Must call file_read", want: "file_read"},
		{name: "period before use", prompt: "Never call file_write. Use file_read.", want: "file_read"},
		{name: "implicit sentence boundary", prompt: "Do not call file_write. call file_read", want: "file_read"},
		{name: "same tool mentioned twice", prompt: "Don't use file_read. Now call file_read for real.", want: "file_read"},
		{name: "comma does not end negation", prompt: "Never call file_write, use file_read. Must call current_time", want: "current_time"},
		{name: "comma keeps negation", prompt: "Don't call file_read, use file_write. Must call current_time", want: "current_time"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := agentWithIntentGateTools(t)
			loopCfg := DefaultLoopConfig()
			sanitizeLoopConfig(&loopCfg)

			a.applyIntentToolGating(&loopCfg, tt.prompt)
			opts := a.buildLoopCallOptions(tt.prompt, loopCfg)
			visible := toolNamesFromSchemas(opts.Tools)

			if tt.want == "" {
				if len(visible) != 0 {
					t.Fatalf("expected no tool schemas for negated tools, got %v", visible)
				}
				return
			}
			if len(visible) != 1 || visible[0] != tt.want {
				t.Fatalf("expected only the required %s schema, got %v", tt.want, visible)
			}
		})
	}
}
