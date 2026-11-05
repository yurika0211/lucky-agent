package agent

import (
	"strings"
	"time"
)

// RequestProfile selects the cheapest execution budget that can satisfy a turn.
// It is deliberately separate from tool permission gating: a profile describes
// resource policy, while the permission gate decides which calls may execute.
type RequestProfile string

const (
	RequestProfileChat     RequestProfile = "chat"
	RequestProfileToolTask RequestProfile = "tool_task"
	RequestProfileCodeTask RequestProfile = "code_task"
	RequestProfileLongTask RequestProfile = "long_task"
)

// RequestProfileDecision is recorded on LoopConfig for logging and tests.
type RequestProfileDecision struct {
	Profile    RequestProfile
	Source     string
	Confidence float32
}

func classifyRequestProfile(input string, cfg LoopConfig) RequestProfileDecision {
	if cfg.Execution != nil {
		return RequestProfileDecision{Profile: RequestProfileLongTask, Source: "explicit_execution", Confidence: 1}
	}
	if cfg.Foreground {
		return RequestProfileDecision{Profile: RequestProfileLongTask, Source: "explicit_foreground", Confidence: 1}
	}
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return RequestProfileDecision{Profile: RequestProfileChat, Source: "empty_input", Confidence: 1}
	}
	if containsAny(text, "执行", "运行", "修复", "修改", "编译", "测试", "提交代码", "写代码", "run ", "fix ", "implement ", "build ", "test ") {
		return RequestProfileDecision{Profile: RequestProfileCodeTask, Source: "deterministic_code_intent", Confidence: 0.85}
	}
	if containsAny(text, "查询", "查一下", "看看", "检查", "打开", "搜索", "列出", "状态", "search", "inspect", "check ", "list ") {
		return RequestProfileDecision{Profile: RequestProfileToolTask, Source: "deterministic_tool_intent", Confidence: 0.8}
	}
	return RequestProfileDecision{Profile: RequestProfileChat, Source: "fallback_low_risk", Confidence: 0.55}
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

// applyRequestProfile chooses conservative defaults only when the caller did
// not explicitly provide tighter limits. It runs before tool permission
// gating, so permission policy never changes the resource profile.
func applyRequestProfile(cfg *LoopConfig, input string) RequestProfileDecision {
	if cfg == nil {
		return RequestProfileDecision{Profile: RequestProfileChat, Source: "nil_config", Confidence: 0}
	}
	decision := classifyRequestProfile(input, *cfg)
	applyRequestProfileDecision(cfg, decision)
	return decision
}

func applyRequestProfileDecision(cfg *LoopConfig, decision RequestProfileDecision) {
	if cfg == nil {
		return
	}
	cfg.Profile = decision.Profile
	cfg.ProfileSource = decision.Source
	cfg.ProfileConfidence = decision.Confidence
	switch decision.Profile {
	case RequestProfileChat:
		if cfg.MaxIterations <= 0 || cfg.MaxIterations > 6 {
			cfg.MaxIterations = 6
		}
		if cfg.Timeout <= 0 || cfg.Timeout > 45*time.Second {
			cfg.Timeout = 45 * time.Second
		}
	case RequestProfileToolTask:
		if cfg.MaxIterations <= 0 || cfg.MaxIterations > 10 {
			cfg.MaxIterations = 10
		}
		if cfg.Timeout <= 0 || cfg.Timeout > 90*time.Second {
			cfg.Timeout = 90 * time.Second
		}
	case RequestProfileCodeTask:
		if cfg.MaxIterations <= 0 || cfg.MaxIterations > 20 {
			cfg.MaxIterations = 20
		}
		if cfg.Timeout <= 0 || cfg.Timeout > 3*time.Minute {
			cfg.Timeout = 3 * time.Minute
		}
	case RequestProfileLongTask:
		// The long-task budget is the loop hard cap. Only explicit Execution
		// or Foreground classification reaches this branch.
		if cfg.MaxIterations <= 0 || cfg.MaxIterations > maxAllowedIterations {
			cfg.MaxIterations = maxAllowedIterations
		}
		if cfg.Timeout <= 0 || cfg.Timeout > 10*time.Minute {
			cfg.Timeout = 10 * time.Minute
		}
	}
}

func contextBuildOptionsForProfile(profile RequestProfile) contextBuildOptions {
	opts := defaultContextBuildOptions()
	switch profile {
	case RequestProfileChat:
		opts.HistoryRecent = 4
		opts.MaxContextTokens = 24000
	case RequestProfileToolTask:
		opts.HistoryRecent = 6
		opts.MaxContextTokens = 48000
	case RequestProfileCodeTask, RequestProfileLongTask:
		opts.HistoryRecent = 12
	}
	return opts
}
