package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

const intentRouterSystemPrompt = `Classify the user's request into exactly one profile.
Return JSON only: {"profile":"chat|tool_task|code_task|long_task"}.
Use long_task only when the user explicitly asks for a sustained/background task.
Use code_task for writing, changing, testing, building, debugging, or committing code.
Use tool_task for lookup, search, inspection, listing, or checking with tools.
Use chat for explanation, conversation, or anything that does not need tools.`

type intentRouterResponse struct {
	Profile string `json:"profile"`
}

// classifyRequestProfileWithModel lets the configured chat provider decide the
// request profile before tool gating. The provider's fallback chain is reused,
// so its final reasoning_effort=none node is also the final classifier retry.
func (a *Agent) classifyRequestProfileWithModel(ctx context.Context, input string, cfg LoopConfig, snapshot providerSnapshot) RequestProfileDecision {
	if a == nil || a.cfg == nil || !snapshot.valid() || !a.cfg.Get().ModelRouter.Enable || cfg.Execution != nil || cfg.Foreground {
		return classifyRequestProfile(input, cfg)
	}

	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resp, err := snapshot.provider.Chat(callCtx, []provider.Message{
		{Role: "system", Content: intentRouterSystemPrompt},
		{Role: "user", Content: strings.TrimSpace(input)},
	})
	if err != nil || resp == nil {
		return classifyRequestProfile(input, cfg)
	}
	profile := parseIntentProfile(resp.Content)
	if profile == "" {
		return classifyRequestProfile(input, cfg)
	}
	return RequestProfileDecision{Profile: profile, Source: "model_intent", Confidence: 0.9}
}

func parseIntentProfile(content string) RequestProfile {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(strings.TrimPrefix(content, "json"))
	var parsed intentRouterResponse
	if json.Unmarshal([]byte(content), &parsed) != nil {
		start, end := strings.IndexByte(content, '{'), strings.LastIndexByte(content, '}')
		if start < 0 || end <= start || json.Unmarshal([]byte(content[start:end+1]), &parsed) != nil {
			return ""
		}
	}
	switch RequestProfile(strings.ToLower(strings.TrimSpace(parsed.Profile))) {
	case RequestProfileChat, RequestProfileToolTask, RequestProfileCodeTask, RequestProfileLongTask:
		return RequestProfile(strings.ToLower(strings.TrimSpace(parsed.Profile)))
	default:
		return ""
	}
}
