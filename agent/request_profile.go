package agent

import (
	"strings"
	"time"
	"unicode"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/provider"
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
	if inherited, ok := continuationProfile(text, cfg); ok {
		return RequestProfileDecision{Profile: inherited, Source: "session_continue", Confidence: 0.9}
	}
	if isExplicitChat(text) {
		return RequestProfileDecision{Profile: RequestProfileChat, Source: "explicit_chat", Confidence: 0.9}
	}
	if inherited, ok := inheritedUnknownProfile(cfg); ok {
		return RequestProfileDecision{Profile: inherited, Source: "session_unknown", Confidence: 0.7}
	}
	return RequestProfileDecision{Profile: RequestProfileToolTask, Source: "fallback_needs_tools", Confidence: 0.6}
}

var explicitChatPhrases = []string{
	"你好",
	"谢谢",
	"在吗",
	"hi",
	"hello",
	"thanks",
	"ok",
	"好的",
}

func isExplicitChat(text string) bool {
	trimmed := stripEdgePunct(text)
	for _, phrase := range explicitChatPhrases {
		if trimmed == phrase {
			return true
		}
	}
	return false
}

func inheritedUnknownProfile(cfg LoopConfig) (RequestProfile, bool) {
	last := cfg.inheritProfile
	if last == "" || last == RequestProfileChat {
		return "", false
	}
	if last == RequestProfileLongTask && cfg.Execution == nil && !cfg.Foreground {
		return RequestProfileCodeTask, true
	}
	return last, true
}

// continuationPhrases are whole-turn follow-ups. A longer request that only
// contains one of these words still goes through keyword classification.
var continuationPhrases = []string{
	"继续",
	"接着做",
	"接着",
	"go on",
	"keep going",
	"keep doing",
	"continue",
}

var continuationTokens = []string{
	"继续",
	"接着",
	"go on",
	"continue",
	"keep going",
	"keep doing",
}

func stripEdgePunct(text string) string {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimFunc(trimmed, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
	return strings.ToLower(strings.TrimSpace(trimmed))
}

func isContinuationPhrase(text string) bool {
	trimmed := stripEdgePunct(text)
	for _, phrase := range continuationPhrases {
		if trimmed == phrase {
			return true
		}
	}
	runes := []rune(trimmed)
	if len(runes) == 0 || len(runes) > 24 {
		return false
	}
	for _, token := range continuationTokens {
		if strings.Contains(trimmed, token) {
			return true
		}
	}
	return false
}

func (a *Agent) lastRequestProfile(sessionID string) RequestProfile {
	if a == nil {
		return ""
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	value, ok := a.requestProfiles.Load(sessionID)
	if !ok {
		return ""
	}
	profile, _ := value.(RequestProfile)
	return profile
}

func (a *Agent) rememberRequestProfile(sessionID string, profile RequestProfile) {
	if a == nil || profile == "" {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	a.requestProfiles.Store(sessionID, profile)
}

func (a *Agent) forgetRequestProfile(sessionID string) {
	if a == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	a.requestProfiles.Delete(sessionID)
}

func (a *Agent) requestProfileCount() int {
	if a == nil {
		return 0
	}
	n := 0
	a.requestProfiles.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

func continuationProfile(text string, cfg LoopConfig) (RequestProfile, bool) {
	if !isContinuationPhrase(text) {
		return "", false
	}
	last, ok := cfg.inheritProfile, cfg.inheritProfile != ""
	if !ok || last == RequestProfileChat {
		return "", false
	}
	if last == RequestProfileLongTask && cfg.Execution == nil && !cfg.Foreground {
		return RequestProfileCodeTask, true
	}
	return last, true
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
	cfg.requestProfileClosingPrompt = ""
	switch decision.Profile {
	case RequestProfileChat, RequestProfileToolTask, RequestProfileCodeTask:
		budget := requestProfileBudget(cfg.requestProfiles, decision.Profile)
		applyProfileBudgetFillOnly(cfg, budget.MaxIterations, time.Duration(budget.TimeoutSeconds)*time.Second)
		if requestProfilesConfigured(cfg.requestProfiles) {
			cfg.requestProfileClosingPrompt = budget.ClosingPrompt
		}
	case RequestProfileLongTask:
		// The long-task budget is the loop hard cap. Only explicit Execution
		// or Foreground classification reaches this branch. Configured global
		// values stay; an unset budget is still hard-capped. No wrap-up prompt.
		applyProfileBudget(cfg, maxAllowedIterations, 10*time.Minute)
	}
}

func requestProfilesConfigured(profiles config.RequestProfilesConfig) bool {
	return profiles != (config.RequestProfilesConfig{})
}

func requestProfileBudget(profiles config.RequestProfilesConfig, profile RequestProfile) config.RequestProfileBudgetConfig {
	defaults := config.DefaultConfig().Agent.RequestProfiles
	var got config.RequestProfileBudgetConfig
	var def config.RequestProfileBudgetConfig
	switch profile {
	case RequestProfileChat:
		got, def = profiles.Chat, defaults.Chat
	case RequestProfileToolTask:
		got, def = profiles.ToolTask, defaults.ToolTask
	case RequestProfileCodeTask:
		got, def = profiles.CodeTask, defaults.CodeTask
	default:
		return config.RequestProfileBudgetConfig{}
	}
	if got.MaxIterations <= 0 {
		got.MaxIterations = def.MaxIterations
	}
	if got.TimeoutSeconds <= 0 {
		got.TimeoutSeconds = def.TimeoutSeconds
	}
	if strings.TrimSpace(got.ClosingPrompt) == "" {
		got.ClosingPrompt = def.ClosingPrompt
	}
	return got
}

// appendRequestProfileClosingMessage adds the selected profile's wrap-up once,
// after context build. An empty prompt, including long_task, leaves the
// messages unchanged. The text is folded into the first system message so a
// later tool-call and tool-result tail stays adjacent for the provider. The
// standing system prompt builder is not changed. With no system message, the
// wrap-up is appended as its own system message.
func appendRequestProfileClosingMessage(messages []provider.Message, cfg LoopConfig) []provider.Message {
	prompt := strings.TrimSpace(cfg.requestProfileClosingPrompt)
	if prompt == "" {
		return messages
	}
	out := append([]provider.Message(nil), messages...)
	for i := range out {
		if out[i].Role != "system" {
			continue
		}
		if strings.TrimSpace(out[i].Content) == "" {
			out[i].Content = prompt
		} else {
			out[i].Content = out[i].Content + "\n\n" + prompt
		}
		return out
	}
	return append(out, provider.Message{Role: "system", Content: prompt})
}

// applyProfileBudget fills a missing budget and caps an unset one. Values
// copied from agent config are left alone; sanitizeLoopConfig still enforces
// the 300-iteration and 10-minute hard caps. long_task uses this. chat,
// tool_task, and code_task use applyProfileBudgetFillOnly.
func applyProfileBudget(cfg *LoopConfig, iterations int, timeout time.Duration) {
	if !cfg.maxIterationsFromConfig && (cfg.MaxIterations <= 0 || cfg.MaxIterations > iterations) {
		cfg.MaxIterations = iterations
	}
	if !cfg.timeoutFromConfig && (cfg.Timeout <= 0 || cfg.Timeout > timeout) {
		cfg.Timeout = timeout
	}
}

// applyProfileBudgetFillOnly writes a profile default only when that field is
// missing. An already-set per-turn budget is neither lowered nor raised.
// Values copied from agent.max_iterations / agent.timeout_seconds stay.
func applyProfileBudgetFillOnly(cfg *LoopConfig, iterations int, timeout time.Duration) {
	if !cfg.maxIterationsFromConfig && cfg.MaxIterations <= 0 {
		cfg.MaxIterations = iterations
	}
	if !cfg.timeoutFromConfig && cfg.Timeout <= 0 {
		cfg.Timeout = timeout
	}
}

func contextBuildOptionsForProfile(profile RequestProfile) contextBuildOptions {
	return contextBuildOptionsForProfileSource(profile, "")
}

// contextBuildOptionsForProfileSource shrinks history only for a real chat
// turn. Any other chat source uses the tool_task window.
func contextBuildOptionsForProfileSource(profile RequestProfile, source string) contextBuildOptions {
	opts := defaultContextBuildOptions()
	switch profile {
	case RequestProfileChat:
		if source == "explicit_chat" || source == "empty_input" {
			opts.HistoryRecent = 4
			opts.MaxContextTokens = 24000
		} else {
			opts.HistoryRecent = 6
			opts.MaxContextTokens = 48000
		}
	case RequestProfileToolTask:
		opts.HistoryRecent = 6
		opts.MaxContextTokens = 48000
	case RequestProfileCodeTask, RequestProfileLongTask:
		opts.HistoryRecent = 12
	}
	return opts
}
