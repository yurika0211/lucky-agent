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

func TestEnglishRequiredToolSurvivesPriorNegationInSchema(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
	}{
		{name: "period after another tool", prompt: "Don't use terminal. Must call file_read"},
		{name: "period before use", prompt: "Never call file_write. Use file_read."},
		{name: "implicit sentence boundary", prompt: "Do not call file_write. call file_read"},
		{name: "same tool mentioned twice", prompt: "Don't use file_read. Now call file_read for real."},
		{name: "comma boundary", prompt: "Never call file_write, use file_read"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := agentWithIntentGateTools(t)
			t.Setenv(toolIntentGatingEnv, "")
			loopCfg := DefaultLoopConfig()
			sanitizeLoopConfig(&loopCfg)

			a.applyIntentToolGating(&loopCfg, tt.prompt)
			opts := a.buildLoopCallOptions(tt.prompt, loopCfg)
			visible := toolNamesFromSchemas(opts.Tools)

			if len(visible) != 1 || visible[0] != "file_read" {
				t.Fatalf("expected only the required file_read schema, got %v", visible)
			}
		})
	}
}
