package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	appheartbeat "github.com/yurika0211/lucky-agent/agent/heartbeat"
	autonomybridge "github.com/yurika0211/luckyagent-runtime/autonomy"
	"github.com/yurika0211/luckyagent-runtime/bootstrap"
	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/embedder"
	"github.com/yurika0211/luckyagent-runtime/gateway"
	"github.com/yurika0211/luckyagent-runtime/hook"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/mcpclient"
	"github.com/yurika0211/luckyagent-runtime/metrics"
	"github.com/yurika0211/luckyagent-runtime/proactive"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/rag"
	"github.com/yurika0211/luckyagent-runtime/sandbox"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/soul"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-runtime/turn"
	"github.com/yurika0211/luckyagent-runtime/utils"
	"github.com/yurika0211/luckyagent-tools/autonomy"
	"github.com/yurika0211/luckyagent-tools/collab"
	"github.com/yurika0211/luckyagent-tools/computer"
	"github.com/yurika0211/luckyagent-tools/cron"
	"github.com/yurika0211/luckyagent-tools/memory"
	"github.com/yurika0211/luckyagent-tools/memory/diagnostic"
	"github.com/yurika0211/luckyagent-tools/multimodal"
	taskstore "github.com/yurika0211/luckyagent-tools/task"
)

// Agent 是 LuckyAgent 的核心 Agent
type Agent struct {
	components       *bootstrap.Components
	ownComponents    bool
	closeOnce        sync.Once
	closeError       error
	foregroundActive sync.Map // session ID -> active caller; removed when the request ends
	sessionCalls     sync.Map // *sessionCall -> active model/tool request
	recaps           *recapService
	loopMu           sync.Mutex
	loop             *Loop
	cfg              *config.Manager
	soul             *soul.Soul
	tmplMgr          *soul.TemplateManager  // SOUL 模板管理器
	provider         provider.Provider      // 当前活跃 provider (可能是 FallbackChain)
	providerMu       sync.RWMutex           // protects the default provider/model selection
	registry         *provider.Registry     // provider 注册表
	catalog          *provider.ModelCatalog // 模型目录
	modelDiscovery   *provider.ModelDiscovery
	tokenStore       *provider.TokenStore // token 存储
	memory           memory.RuntimeStore
	shortTerms       memory.SessionShortTermStore
	midTerm          memory.MidTermStore // 中期会话摘要存储
	sessions         *session.Manager
	tools            *tool.Registry
	toolServices     *tool.Services
	gateway          *tool.Gateway           // 统一工具网关
	hitl             *HITLGate               // 本地工具审批和 ask_user 等待
	hooks            *hook.Runner            // 工具执行前后的 hook 运行器
	msgGateway       *gateway.GatewayManager // 消息平台网关
	sessionEvents    func(sessionID, content string)
	mcpClient        *mcpclient.Manager      // Runtime-owned MCP connections
	delegate         *tool.DelegateManager   // 子代理委派管理器
	contextWin       *contextx.ContextWindow // 上下文窗口管理器
	contextEst       *contextx.TokenEstimator
	ragManager       *rag.RAGManager         // RAG 知识库管理器
	ragPersist       *rag.Persistence        // RAG 持久化
	streamIndexer    *rag.StreamIndexer      // 流式索引器
	embedderReg      *embedder.Registry      // 嵌入模型注册表
	collabReg        *collab.Registry        // Agent 协作注册表
	collabMgr        *collab.DelegateManager // 协作任务管理器
	// skillMu guards the published skill fields below. It is a plain data guard:
	// never call into SkillRegistry or tool.Registry while holding it, or you
	// introduce a lock order that nothing else in the process observes.
	skillMu       sync.RWMutex
	skills        []*tool.SkillInfo // 已加载的 skill 列表
	skillRegistry *tool.SkillRegistry
	skillsDir     string
	pluginSkills  []*tool.SkillInfo // immutable namespaced plugin workflows
	plugins       *pluginRuntime    // constructed before New returns; changes require restart
	// skillLoadMu serializes whole LoadSkills bodies so two concurrent reloads
	// cannot interleave "retire the previous generation" with "register the new
	// one" and leave the tool Registry holding a mix of both.
	skillLoadMu sync.Mutex
	// skillInstall is built lazily by SkillInstaller; it is guarded by skillMu.
	skillInstall          *tool.InstallManager
	metrics               *metrics.Metrics // 指标收集器
	proactiveStore        *proactive.Store
	proactiveRuntime      *proactive.RuntimeService
	cronEngine            *cron.Engine // 定时任务引擎
	cronStore             *cron.Store
	autonomy              *autonomy.AutonomyKit // 自主工作套件
	computerMgr           *computer.Manager
	sandbox               *sandbox.Manager
	autonomyResultsMu     sync.Mutex
	autonomyResultsCancel context.CancelFunc
	heartbeatSvc          *appheartbeat.Service
	heartbeatMu           sync.Mutex
	heartbeatSessionID    string
	recentTarget          recentChatTarget
	externalReplyAnchors  map[string]externalReplyAnchor
	contextCache          *contextMessageCache
	mediaMu               sync.RWMutex
	mediaProcessor        *multimodal.Processor
	taskStore             taskstore.Store
	autoCompactMu         sync.Mutex
	autoCompactFailures   map[string]int
	activeModel           string
	activeAPIBase         string
}

func buildDelegateChildLoopConfig(delegateCfg tool.DelegateConfig, allowRecursive bool) LoopConfig {
	loopCfg := DefaultLoopConfig()
	loopCfg.Source = "subagent"
	loopCfg.MaxIterations = delegateCfg.ChildMaxIterations
	loopCfg.Timeout = delegateCfg.ChildTimeout
	loopCfg.AutoApprove = delegateCfg.ChildAutoApprove
	loopCfg.RepeatToolCallLimit = delegateCfg.ChildRepeatToolLimit
	loopCfg.ToolOnlyIterationLimit = delegateCfg.ChildToolOnlyLimit
	loopCfg.DuplicateFetchLimit = delegateCfg.ChildDuplicateLimit
	loopCfg.DisabledTools = append([]string(nil), delegateCfg.ChildDisabledTools...)
	if !allowRecursive {
		loopCfg.DisabledTools = append(loopCfg.DisabledTools,
			"subagent",
			"autonomy",
			"delegate_parallel",
			"delegate_to_skill",
			"delegate_to_mcp",
		)
	}
	loopCfg.DisabledTools = normalizeToolNameList(loopCfg.DisabledTools)
	return loopCfg
}

// ReloadHooks rebuilds the tool-execution hook runner from a new config, for
// config hot-reload. Safe to call while tools execute (Runner.Reload locks).
func (a *Agent) ReloadHooks(c *config.Config) {
	if a == nil || a.hooks == nil {
		return
	}
	a.hooks.Reload(a.pluginHookConfig(c))
}

// StartConfigWatch polls the agent's config file and live-applies the changes
// that are safe to reload without a restart (currently: hooks). It returns a
// stop function (always non-nil, safe to defer). Intended for long-running
// entry points such as `lh serve` and `lh msg-gateway start`; one-shot CLI
// commands need not call it.
func (a *Agent) StartConfigWatch(interval time.Duration) (func(), error) {
	if a == nil || a.cfg == nil {
		return func() {}, nil
	}
	w, err := a.cfg.WatchConfig(interval)
	if err != nil {
		return func() {}, err
	}
	w.OnValidate(func(_, newCfg *config.Config) error {
		return a.ValidateRuntimeConfig(newCfg)
	})
	w.OnChange(func(oldCfg, newCfg *config.Config) {
		if err := a.ApplyRuntimeConfig(newCfg); err != nil {
			if oldCfg != nil {
				if restoreErr := a.cfg.Replace(oldCfg); restoreErr == nil {
					_ = a.ApplyRuntimeConfig(oldCfg)
				}
			}
			logger.Warn("configuration reload could not update provider runtime", "error", err)
			return
		}
		_, restartRequired := config.ReloadClassification(oldCfg, newCfg)
		logger.Info("configuration reloaded for subsequent requests", "restart_required", restartRequired)
	})
	w.OnError(func(err error) {
		logger.Warn("configuration reload rejected; keeping previous configuration", "error", err)
	})
	if err := w.Start(); err != nil {
		return func() {}, err
	}
	return w.Stop, nil
}

func (a *Agent) ValidateRuntimeConfig(c *config.Config) error {
	if a == nil {
		return fmt.Errorf("agent is unavailable")
	}
	next, err := config.Normalized(c)
	if err != nil {
		return err
	}
	if err := resolveConfiguredCredentials(a.cfg.HomeDir(), next); err != nil {
		return err
	}
	_, err = buildConfiguredProvider(next, provider.NewRegistry())
	return err
}

func (a *Agent) ApplyRuntimeConfig(c *config.Config) error {
	if a == nil {
		return fmt.Errorf("agent is unavailable")
	}
	next, err := config.Normalized(c)
	if err != nil {
		return err
	}
	c = next
	if err := resolveConfiguredCredentials(a.cfg.HomeDir(), c); err != nil {
		return err
	}
	registry := provider.NewRegistry()
	nextProvider, err := buildConfiguredProvider(c, registry)
	if err != nil {
		return err
	}
	nextCatalog := provider.NewModelCatalog()
	if a.catalog != nil {
		for _, model := range a.catalog.List() {
			if model.Discovered {
				nextCatalog.RegisterDiscovered(model)
			}
		}
	}
	for _, custom := range c.CustomModels {
		if strings.TrimSpace(custom.ID) == "" {
			continue
		}
		nextCatalog.Register(provider.ModelInfo{
			ID:            custom.ID,
			Provider:      custom.Provider,
			DisplayName:   custom.DisplayName,
			Capabilities:  append([]string(nil), custom.Capabilities...),
			ContextWindow: custom.ContextWindow,
			CostPer1kIn:   custom.CostPer1kIn,
			CostPer1kOut:  custom.CostPer1kOut,
		})
	}
	a.providerMu.Lock()
	a.provider = wrapProviderWithMiddleware(nextProvider, c)
	a.registry = registry
	a.catalog = nextCatalog
	a.activeModel = c.Model
	a.activeAPIBase = c.APIBase
	a.providerMu.Unlock()
	a.reloadMediaRuntime(c)
	a.ReloadHooks(c)
	return nil
}

func (a *Agent) ReloadConfig() (config.ReloadResult, error) {
	if a == nil || a.cfg == nil {
		return config.ReloadResult{}, fmt.Errorf("agent configuration is unavailable")
	}
	result, err := a.cfg.ReloadValidated(func(_, newCfg *config.Config) error {
		return a.ValidateRuntimeConfig(newCfg)
	})
	if err != nil || !result.Changed {
		return result, err
	}
	if err := a.ApplyRuntimeConfig(result.New); err != nil {
		// ReloadValidated swaps the manager snapshot before the runtime provider
		// is rebuilt. Restore both the in-memory and persisted snapshots when
		// applying the new provider fails, so callers can rely on the rejected
		// configuration never becoming the active one.
		if result.Old != nil {
			if restoreErr := a.cfg.Replace(result.Old); restoreErr == nil {
				_ = a.ApplyRuntimeConfig(result.Old)
			}
		}
		return config.ReloadResult{}, err
	}
	return result, nil
}

// New creates the default application runtime for compatibility.
// Prefer NewWithDependencies for embedding or app/bootstrap.New for a full app.
func New(cfg *config.Manager) (*Agent, error) { return newAgent(cfg, nil) }

// NewWithMemory accepts a complete memory runtime; Agent takes ownership of its
// Store.Close. Session history remains a separate application resource.
func NewWithMemory(cfg *config.Manager, runtime memory.Runtime) (*Agent, error) {
	return newAgent(cfg, &runtime)
}

func newAgent(cfg *config.Manager, injected *memory.Runtime) (*Agent, error) {
	deps, err := bootstrap.Open(cfg, injected)
	if err != nil {
		return nil, err
	}
	a, err := NewWithDependencies(Options{TakeOwnership: true}, deps)
	if err != nil {
		_ = deps.Close()
		return nil, err
	}
	if err := deps.Start(context.Background(), a.StartupHooks()); err != nil {
		_ = a.Close()
		return nil, err
	}
	return a, nil
}

func delegatedSubagentSystemPrompt() string {
	profile := "You are a delegated sub-agent in LuckyAgent. Work only on the assigned task, use the available tools within their limits, preserve evidence, and return a concise result with blockers and unfinished work. Do not claim that work was completed without checking it."
	return profile
}

// Chat 执行一次对话
/*
Chat 在新会话中执行一次完整对话。
*/
func (a *Agent) Chat(ctx context.Context, userInput string) (string, error) {
	sess := a.sessions.New()
	return a.chatWithSessionInput(ctx, sess, TextUserTurnInput(userInput))
}

// ChatWithSession 在已有会话中继续对话，实现多轮上下文。
func (a *Agent) ChatWithSession(ctx context.Context, sessionID string, userInput string) (string, error) {
	sess, ok := a.sessions.Get(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	return a.chatWithSessionInput(ctx, sess, TextUserTurnInput(userInput))
}

// ChatWithSessionInput 在已有会话中继续对话，支持结构化多模态输入。
func (a *Agent) ChatWithSessionInput(ctx context.Context, sessionID string, input UserTurnInput) (string, error) {
	sess, ok := a.sessions.Get(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	return a.chatWithSessionInput(ctx, sess, input)
}

// ChatWithSessionTurn accepts the provider-independent turn DTO used by edge
// adapters. Conversion to provider.Message stays inside the agent runtime.
func (a *Agent) ChatWithSessionTurn(ctx context.Context, sessionID string, input turn.Input) (string, error) {
	return a.ChatWithSessionInput(ctx, sessionID, UserTurnInputFromTurn(input))
}

// ProgressFeedback generates a concise model-authored progress update for an unfinished round.
func (a *Agent) ProgressFeedback(ctx context.Context, userInput string, round int, observations []string) (string, error) {
	return a.ProgressFeedbackWithPrompt(ctx, userInput, round, observations, "")
}

// ProgressFeedbackWithPrompt generates a progress update using an optional
// presentation prompt. Core evidence and safety constraints remain enforced
// even when the presentation prompt is configured by the runtime owner.
func (a *Agent) ProgressFeedbackWithPrompt(ctx context.Context, userInput string, round int, observations []string, presentationPrompt string) (string, error) {
	turnProvider := a.providerSnapshotForTurn(userInput)
	if !turnProvider.valid() {
		return "", fmt.Errorf("provider not initialized")
	}
	if len(observations) == 0 {
		return "", nil
	}

	systemPrompt := progressFeedbackSystemPrompt(presentationPrompt)

	var userPrompt strings.Builder
	var previousUpdates []string
	var newObservations []string
	for _, line := range observations {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Previous user-facing update: ") {
			previousUpdates = append(previousUpdates, strings.TrimPrefix(line, "Previous user-facing update: "))
			continue
		}
		newObservations = append(newObservations, line)
	}

	userPrompt.WriteString("Original user request:\n")
	userPrompt.WriteString(strings.TrimSpace(userInput))
	userPrompt.WriteString("\n\nCurrent round:\n")
	userPrompt.WriteString(fmt.Sprintf("%d", round))
	if len(previousUpdates) > 0 {
		userPrompt.WriteString("\n\nPrevious user-facing updates already shown:\n")
		for _, line := range previousUpdates {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			userPrompt.WriteString("- ")
			userPrompt.WriteString(line)
			userPrompt.WriteString("\n")
		}
	}
	userPrompt.WriteString("\n\nNew observed progress since the last update:\n")
	for _, line := range newObservations {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		userPrompt.WriteString("- ")
		userPrompt.WriteString(line)
		userPrompt.WriteString("\n")
	}
	userPrompt.WriteString("\nWrite a single progress update for the user that clearly continues from the previous updates and focuses on what changed.")

	resp, err := a.chatLoopIteration(ctx, []provider.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt.String()},
	}, provider.CallOptions{}, true, turnProvider)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

const defaultProgressFeedbackPresentationPrompt = `Write in English.

The update should sound like a human investigator thinking aloud in a compact way.
It should read like a short natural reasoning paragraph, not like a checklist, template, or repeated report.

If previous user-facing updates are provided, treat them as messages that the user has already seen. Continue naturally from them instead of restarting the narration from scratch.
Prioritize what changed since the previous update.

What to include when relevant:
- what you have checked,
- what that currently suggests,
- what you are verifying now and why,
- what is still uncertain,
- what likely matters next.

Style requirements:
- use 2 to 4 short connected sentences,
- use natural transitions, but vary them across updates,
- make the first sentence anchor to the newest change or signal, not to a generic restart,
- do not start every update with the same pattern such as "I first checked...",
- do not repeatedly open with first-person patterns like "I've...", "I have...", or "I'm..." unless there is a strong reason,
- prefer continuity cues such as "So far,", "At this point,", "That suggests,", "The latest result shows,", or "This narrows it down because..." when they fit,
- avoid rigid labels like "Verified", "Checking", "Uncertain", "Next",
- avoid repeating the same rhetorical skeleton from one round to the next,
- include brief causal links and small explanations, not just status labels,
- do not use rigid headings like "Verified:" or "Checking:" unless the user explicitly asked for a checklist.`

func progressFeedbackSystemPrompt(presentationPrompt string) string {
	presentationPrompt = strings.TrimSpace(presentationPrompt)
	if presentationPrompt == "" {
		presentationPrompt = defaultProgressFeedbackPresentationPrompt
	}

	return `You are generating one concise progress update for the user during an unfinished task.

Report real progress only. Stay close to the observed evidence.

Presentation instructions:
` + presentationPrompt + `

Non-negotiable constraints:
- keep the update concrete and evidence-driven;
- do not expose hidden chain-of-thought;
- do not mention internal event types, implementation details, or tool protocol syntax;
- do not pretend the task is complete if it is not.`
}

func (a *Agent) chatWithSessionInput(ctx context.Context, sess *session.Session, input UserTurnInput) (string, error) {
	input = a.expandPluginCommand(input)
	routingText := input.RoutingText
	turnProvider := a.providerSnapshotForTurn(routingText)

	// 优先使用 RunLoop（支持 function calling / 工具调用）
	loopCfg := DefaultLoopConfig()
	agentLoopCfg := config.AgentLoopConfig{}
	if a.cfg != nil {
		cfg := a.cfg.Get()
		agentLoopCfg = cfg.Agent
		ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
	}
	applySimpleTaskLoopTuning(&loopCfg, routingText, agentLoopCfg)
	loopCfg.Source = input.Scope.Platform
	if strings.TrimSpace(loopCfg.Source) == "" {
		loopCfg.Source = "cli"
	}
	loopCfg.AutoApprove = true // Telegram 场景自动批准工具调用

	result, err := a.runLoopWithProviderSnapshot(ctx, sess, input, loopCfg, turnProvider)
	if err != nil {
		if loopCfg.Foreground || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isSessionError(err) {
			return "", err
		}
		// 如果 RunLoop 失败，回退到简单流式聊天
		response, chatErr := a.chatStreamSimpleInputWithProvider(ctx, sess, input, turnProvider)
		if chatErr != nil {
			a.recordProactiveChatEvent(sess, input, "", 0, fmt.Errorf("runloop: %w; fallback chat: %w", err, chatErr))
			return "", fmt.Errorf("runloop: %w; fallback chat: %w", err, chatErr)
		}
		// v0.36.0: 记录指标
		a.metrics.RecordChatRequest()
		a.recordProactiveChatEvent(sess, input, response, 0, err)
		return response, nil
	}

	response := result.Response
	if result.foregroundControl {
		return response, nil
	}
	if result.foregroundInput != nil {
		input = *result.foregroundInput
	}

	// 自动记忆（去重 + 智能分类 + 截断）。计数和维护 cadence 由 memory
	// runtime 持有，Agent 只执行返回的维护动作。
	maintenance := a.recordMemoryTurn(sess)
	a.saveConversationMemoryFromSession(sess, input, response)
	a.applyMemoryMaintenance(maintenance)

	// v0.36.0: 记录指标
	a.metrics.RecordChatRequest()
	if len(result.ToolCalls) > 0 {
		for range result.ToolCalls {
			a.metrics.RecordToolCall()
		}
	}
	a.recordProactiveChatEvent(sess, input, response, len(result.ToolCalls), nil)

	return response, nil
}

var simpleLocalInspectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(list|show|check|inspect|read|open|verify|confirm|find)\b.*\b(file|files|dir|directory|folder|path|workspace)\b`),
	regexp.MustCompile(`(?i)\b(can|could|should)\b.*\b(send|attach|upload)\b.*\b(file|files|document|documents)\b`),
	regexp.MustCompile(`(?i)\bwhat\b.*\b(in|inside)\b.*\b(directory|folder|workspace)\b`),
	regexp.MustCompile(`查看.{0,10}(目录|文件|路径|工作区|文件夹)`),
	regexp.MustCompile(`检查.{0,10}(目录|文件|路径|工作区|文件夹)`),
	regexp.MustCompile(`确认.{0,14}(路径|文件|目录|能不能发|是否可发|是否可以发送|是否可发送)`),
	regexp.MustCompile(`(能不能|是否可以|是否可).{0,8}(发送|发出|上传).{0,10}(文件|附件)`),
	regexp.MustCompile(`列出.{0,8}(目录|文件)`),
}

func IsSimpleLocalInspectionTask(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	for _, re := range simpleLocalInspectionPatterns {
		if re.MatchString(input) {
			return true
		}
	}
	return false
}

func applySimpleTaskLoopTuning(loopCfg *LoopConfig, userInput string, cfg config.AgentLoopConfig) {
	if loopCfg == nil || !IsSimpleLocalInspectionTask(userInput) {
		return
	}
	maxIterations := cfg.SimpleLocalInspection.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 3
	}
	timeout := time.Duration(cfg.SimpleLocalInspection.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	repeatToolCallLimit := cfg.SimpleLocalInspection.RepeatToolCallLimit
	if repeatToolCallLimit <= 0 {
		repeatToolCallLimit = 2
	}
	toolOnlyIterationLimit := cfg.SimpleLocalInspection.ToolOnlyIterationLimit
	if toolOnlyIterationLimit <= 0 {
		toolOnlyIterationLimit = 2
	}

	if loopCfg.MaxIterations > maxIterations {
		loopCfg.MaxIterations = maxIterations
	}
	if loopCfg.Timeout > timeout {
		loopCfg.Timeout = timeout
	}
	if loopCfg.RepeatToolCallLimit > repeatToolCallLimit {
		loopCfg.RepeatToolCallLimit = repeatToolCallLimit
	}
	if loopCfg.ToolOnlyIterationLimit > toolOnlyIterationLimit {
		loopCfg.ToolOnlyIterationLimit = toolOnlyIterationLimit
	}
}

func (a *Agent) chatStreamSimpleInput(ctx context.Context, sess *session.Session, input UserTurnInput) (string, error) {
	input = input.Normalize()
	return a.chatStreamSimpleInputWithProvider(ctx, sess, input, a.providerSnapshotForTurn(input.RoutingText))
}

func (a *Agent) chatStreamSimpleInputWithProvider(ctx context.Context, sess *session.Session, input UserTurnInput, turnProvider providerSnapshot) (string, error) {
	ctx, finish, err := a.beginSessionCall(ctx, sess)
	if err != nil {
		return "", err
	}
	defer finish()
	input = input.Normalize()
	if !turnProvider.valid() {
		return "", fmt.Errorf("provider not initialized")
	}
	messages := a.buildContextMessagesForInputWithProvider(ctx, sess, input, defaultContextBuildOptions(), turnProvider)

	// 调用 Provider
	ch, err := a.streamLoopIteration(ctx, messages, provider.CallOptions{}, true, turnProvider)
	if err != nil {
		return "", fmt.Errorf("chat: %w", err)
	}

	var result strings.Builder
	var usageDetails *provider.UsageDetails
	model := turnProvider.model
	for chunk := range ch {
		result.WriteString(chunk.Content)
		if chunk.Usage != nil {
			usageDetails = provider.MergeUsageDetails(usageDetails, chunk.Usage)
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Done {
			break
		}
	}

	response := utils.SanitizeToolProtocolOutput(result.String())
	if err := sess.AddProviderMessageWithError(input.Message); err != nil {
		return "", sessionError(err)
	}
	msg := provider.Message{Role: "assistant", Content: response}
	if usageDetails != nil {
		usage := &provider.TokenUsage{}
		usage.AddDetails(usageDetails, model)
		if usage.TotalTokens > 0 {
			msg.Usage = usage
		}
	}
	if err := sess.AddProviderMessageWithError(msg); err != nil {
		return "", sessionError(err)
	}

	// 保存会话
	if err := sess.Save(); err != nil {
		return "", sessionError(err)
	}
	a.maybeRollSession(sess)
	a.queueSessionRecap(sess)

	// 自动记忆：将对话存为短期记忆（去重 + 智能分类 + 截断）。计数和
	// 维护 cadence 由 memory runtime 持有。
	maintenance := a.recordMemoryTurn(sess)
	a.saveConversationMemoryFromSession(sess, input, response)
	a.applyMemoryMaintenance(maintenance)

	return response, nil
}

// ChatStream 执行流式对话
func (a *Agent) ChatStream(ctx context.Context, userInput string) (<-chan provider.StreamChunk, error) {
	sess := a.sessions.New()
	events, err := a.ChatWithSessionStream(ctx, sess.ID, userInput)
	if err != nil {
		return nil, err
	}
	chunks := make(chan provider.StreamChunk, 64)
	go func() {
		defer close(chunks)
		var streamed strings.Builder
		for evt := range events {
			switch evt.Type {
			case ChatEventContent:
				if evt.Content != "" {
					streamed.WriteString(evt.Content)
					chunks <- provider.StreamChunk{Content: evt.Content}
				}
			case ChatEventDone:
				if delta := strings.TrimPrefix(evt.Content, streamed.String()); delta != "" {
					chunks <- provider.StreamChunk{Content: delta}
				}
				chunks <- provider.StreamChunk{Done: true, FinishReason: "stop"}
				return
			case ChatEventError:
				chunks <- provider.StreamChunk{Done: true, FinishReason: "error"}
				return
			}
		}
	}()
	return chunks, nil
}

/*
buildContextMessages 为一次请求构造送入模型的完整上下文消息序列。
*/
func (a *Agent) buildContextMessages(ctx context.Context, sess *session.Session, userInput string, opts contextBuildOptions) []provider.Message {
	return a.buildContextMessagesForInput(ctx, sess, TextUserTurnInput(userInput), opts)
}

func (a *Agent) buildContextMessagesForInput(ctx context.Context, sess *session.Session, input UserTurnInput, opts contextBuildOptions) []provider.Message {
	return a.buildContextMessagesForInputWithProvider(ctx, sess, input, opts, a.baseProviderSnapshot())
}

// buildContextMessagesForInputWithProvider binds context construction to the
// same immutable provider snapshot used by the surrounding turn.
func (a *Agent) buildContextMessagesForInputWithProvider(ctx context.Context, sess *session.Session, input UserTurnInput, opts contextBuildOptions, turnProvider providerSnapshot) []provider.Message {
	planner := newContextPlannerWithProvider(a, opts, turnProvider)
	return planner.BuildInput(ctx, sess, input)
}

// buildMemoryContext 构建分层记忆上下文
func (a *Agent) buildMemoryContext(messages []provider.Message) []provider.Message {
	return a.buildMemoryContextForSession(nil, messages)
}

// buildMemoryContextForSession is the session-aware variant used when callers
// need to include volatile short-term summaries. Persistent memory remains
// process-wide by design; only the short-term window is keyed by sess.ID.
func (a *Agent) buildMemoryContextForSession(sess *session.Session, messages []provider.Message) []provider.Message {
	if a.memory == nil {
		return messages
	}
	var memCtx strings.Builder

	// 长期记忆：全部注入（核心身份/偏好）
	longs := a.memory.ByTierWithOptions(memory.TierLong, memory.RouteOptions{})
	if len(longs) > 0 {
		memCtx.WriteString("[Core Memory — Long-term]\n")
		for _, e := range longs {
			memCtx.WriteString("- " + e.Content + "\n")
		}
		memCtx.WriteString("\n")
	}

	// v0.43.0: 中期记忆 — 从 MidTermStore 检索相关历史会话摘要
	// 如果当前有用户输入，用它检索；否则取最近 3 条
	if a.midTerm != nil {
		recentSummaries := a.midTerm.ListAll()
		limit := 3
		if len(recentSummaries) < limit {
			limit = len(recentSummaries)
		}
		if limit > 0 {
			memCtx.WriteString("[Session History — Mid-term]\n")
			for i := 0; i < limit; i++ {
				sm := recentSummaries[i]
				memCtx.WriteString("- [" + sm.CreatedAt.Format("2006-01-02") + "] ")
				if len(sm.Topics) > 0 {
					memCtx.WriteString("[" + strings.Join(sm.Topics, ",") + "] ")
				}
				memCtx.WriteString(sm.RawSummary + "\n")
			}
			memCtx.WriteString("\n")
		}
	}

	//  短期记忆 — 使用 ShortTermBuffer 的滑动窗口 + 摘要
	if short := a.shortTermBufferForSession(sess, false); short != nil {
		shortCtx := short.GetContext()
		if len(shortCtx) > 0 {
			// ShortTermBuffer.GetContext() 已包含摘要 + 最近消息
			// 只注入摘要部分（system role），对话消息由 session 管理
			for _, msg := range shortCtx {
				if msg.Role == "system" {
					memCtx.WriteString("[Recent Conversation Summary — Short-term]\n")
					memCtx.WriteString(msg.Content + "\n\n")
					break
				}
			}
		}
	}

	if memCtx.Len() > 0 {
		messages = append(messages, provider.Message{Role: "system", Content: memCtx.String()})
	}

	return messages
}

// shortTermBufferForSession resolves only explicitly identified session state.
func (a *Agent) shortTermBufferForSession(sess *session.Session, create bool) memory.ShortTermBuffer {
	if a == nil || a.shortTerms == nil || sess == nil || strings.TrimSpace(sess.ID) == "" {
		return nil
	}
	if create {
		return a.shortTerms.Buffer(sess.ID)
	}
	buf, _ := a.shortTerms.Get(sess.ID)
	return buf
}

// ForgetShortTermSession releases volatile state after a session is deleted
// or intentionally reset. It is safe to call when no store is configured.
func (a *Agent) ForgetShortTermSession(sessionID string) {
	if a == nil {
		return
	}
	if a.toolServices != nil {
		a.toolServices.RevokeEphemeralSession(sessionID)
	}
	if a.shortTerms != nil {
		a.shortTerms.Clear(sessionID)
	}
	if a.memory != nil {
		a.memory.ForgetSession(sessionID)
	}
}

func (a *Agent) saveConversationMemoryFromSession(sess *session.Session, turnInput UserTurnInput, assistantResponse string) {
	turnInput = turnInput.Normalize()
	userInput := turnInput.RoutingText
	assistantResponse = utils.SanitizeToolProtocolOutput(assistantResponse)
	// Volatile buffers require an explicit session; durable capture below uses
	// the trusted turn scope independently of conversation buffering.
	if short := a.shortTermBufferForSession(sess, true); short != nil {
		short.Add("user", userInput)
		short.Add("assistant", utils.TrimToRunes(assistantResponse, 300))
	}

	userCategory := inferCategory(userInput)
	if a.memory != nil && shouldPersistUserTurnMemory(userCategory, userInput) {
		userImportance := inferImportance(userInput)
		content := utils.TrimToRunes(userInput, 180)
		tags := turnInput.Scope.MemoryTags()
		if _, err := memory.Write(context.Background(), a.memory, memory.RememberRequest{
			Content: content, Category: userCategory, Tier: memory.TierShort, Importance: userImportance,
			Options: memory.SaveOptions{Tags: tags}, EntryFilter: memory.ExactScope(tags),
		}); err != nil {
			logger.Warn("automatic memory capture failed", "category", userCategory, "error", err)
		}
	}
}

func shouldPersistUserTurnMemory(category string, input string) bool {
	if strings.TrimSpace(input) == "" {
		return false
	}
	switch category {
	case "preference", "project", "identity":
		return true
	default:
		return false
	}
}

// inferCategory 从用户输入推断记忆分类
/*
inferCategory 根据用户输入粗略推断记忆分类。
*/
func inferCategory(input string) string {
	lower := strings.ToLower(input)

	// 偏好类
	preferenceKeywords := []string{"喜欢", "偏好", "prefer", "like", "想要", "习惯", "讨厌", "hate", "dislike"}
	for _, kw := range preferenceKeywords {
		if strings.Contains(lower, kw) {
			return "preference"
		}
	}

	// 项目类
	projectKeywords := []string{"项目", "project", "代码", "code", "bug", "部署", "deploy", "仓库", "repo", "pr", "merge"}
	for _, kw := range projectKeywords {
		if strings.Contains(lower, kw) {
			return "project"
		}
	}

	// 知识类
	knowledgeKeywords := []string{"什么是", "怎么", "如何", "为什么", "what is", "how to", "why", "解释", "explain", "调研", "研究"}
	for _, kw := range knowledgeKeywords {
		if strings.Contains(lower, kw) {
			return "knowledge"
		}
	}

	// 身份类
	identityKeywords := []string{"我叫", "我是", "我的名字", "my name", "i am", "住", "学校", "公司"}
	for _, kw := range identityKeywords {
		if strings.Contains(lower, kw) {
			return "identity"
		}
	}

	return "conversation"
}

// inferImportance 根据内容推断重要性
/*
inferImportance 根据输入内容估算记忆的重要性权重。
*/
func inferImportance(input string) float64 {
	lower := strings.ToLower(input)

	// 高重要性关键词
	highKeywords := []string{"重要", "记住", "别忘", "important", "remember", "必须", "密码", "password", "key", "token"}
	for _, kw := range highKeywords {
		if strings.Contains(lower, kw) {
			return 0.7
		}
	}

	// 中等重要性：包含具体信息
	if len(input) > 50 {
		return 0.4
	}

	// 短消息（如"你好"）低重要性
	return 0.2
}

// autoSummarize requests scoped durable-note maintenance. It must not copy
// durable facts into the independently queried conversation summary store.
func (a *Agent) autoSummarize() {
	if a == nil || a.memory == nil {
		return
	}
	if _, err := a.memory.CompactShortTerm(context.Background(), 5, 500); err != nil {
		logger.Warn("memory compaction failed", "error", err)
	}
}

// Remember 保存一条中期记忆
func (a *Agent) Remember(content, category string) error {
	_, err := a.WriteMemory(context.Background(), memory.RememberRequest{
		Content: content, Category: category, Tier: memory.TierMedium, Importance: .5,
	})
	return err
}

// RememberLongTerm 保存一条长期记忆
func (a *Agent) RememberLongTerm(content, category string) error {
	_, err := a.WriteMemory(context.Background(), memory.RememberRequest{
		Content: content, Category: category, Tier: memory.TierLong, Importance: .9,
	})
	return err
}

// WriteMemory is the typed, cancellable host write boundary used by transports.
func (a *Agent) WriteMemory(ctx context.Context, request memory.RememberRequest) (memory.RememberResult, error) {
	return memory.Write(ctx, a.memory, request)
}

// Recall 搜索记忆
func (a *Agent) Recall(query string) []memory.Entry {
	results, err := a.RecallMemory(context.Background(), memory.RecallRequest{Query: query, Options: memory.SearchOptions{IncludeGraph: true, GraphDepth: 1}})
	if err != nil {
		logger.Warn("memory recall failed", "error", err)
		return nil
	}
	entries := make([]memory.Entry, len(results))
	for i, r := range results {
		entries[i] = r.Entry
	}
	return entries
}

// RecallMidTerm 从中期记忆检索相关历史会话摘要
func (a *Agent) RecallMidTerm(query string, topK int) []memory.SessionSummary {
	if a.midTerm == nil {
		return nil
	}
	return a.midTerm.SearchSummaries(query, topK)
}

// MemoryStats 返回记忆统计
func (a *Agent) MemoryStats() map[memory.Tier]int {
	if a.memory == nil {
		return nil
	}
	return a.memory.Stats()
}

// TidalMemoryStats returns optional backend diagnostics through an explicit port.
func (a *Agent) TidalMemoryStats() (diagnostic.TidalStoreStats, bool, error) {
	if a == nil || a.memory == nil {
		return diagnostic.TidalStoreStats{}, false, nil
	}
	return a.memory.TidalStats()
}

// DecayMemory 执行记忆衰减
func (a *Agent) DecayMemory(threshold float64) int {
	if a.memory == nil {
		return 0
	}
	return a.memory.Decay(threshold)
}

// PromoteMemory 提升记忆层级
func (a *Agent) PromoteMemory(id string) error {
	if a.memory == nil {
		return fmt.Errorf("memory is not configured")
	}
	return a.memory.Promote(id)
}

// ExpireMidTermMemory 过期清理中期记忆
func (a *Agent) ExpireMidTermMemory(olderThan time.Duration) int {
	if a.midTerm == nil {
		return 0
	}
	return a.midTerm.ExpireOldSummaries(olderThan)
}

// Soul 返回当前 SOUL
func (a *Agent) Soul() *soul.Soul {
	return a.soul
}

// TemplateManager 返回 SOUL 模板管理器
func (a *Agent) TemplateManager() *soul.TemplateManager {
	return a.tmplMgr
}

// Tools 返回工具注册表
func (a *Agent) Tools() *tool.Registry {
	return a.tools
}

// PendingApprovals returns runtime HITL waits plus external coding-agent
// permission requests in a provider-neutral shape for HTTP/mobile/GUI surfaces.
func (a *Agent) PendingApprovals() []tool.PendingApproval {
	if a == nil {
		return nil
	}
	var out []tool.PendingApproval
	if a.hitl != nil {
		out = append(out, a.hitl.list()...)
	}
	if a.toolServices != nil {
		out = append(out, a.toolServices.PendingApprovals()...)
	}
	return out
}

// RespondApproval resolves a pending approval. provider "runtime" (or empty)
// targets the local agent HITL gate; "codex"/"grok" target external bridges.
func (a *Agent) RespondApproval(ctx context.Context, providerName, approvalID, decision string) (tool.PendingApproval, error) {
	return a.RespondApprovalWithInput(ctx, providerName, approvalID, decision, "")
}

// ResolvePendingText matches a chat reply to a pending runtime HITL request.
// handled is false when the text should continue as a normal user turn.
func (a *Agent) ResolvePendingText(sessionID, text string) (tool.PendingApproval, bool, error) {
	if a == nil || a.hitl == nil {
		return tool.PendingApproval{}, false, nil
	}
	return a.hitl.ResolveText(sessionID, text)
}

// RespondApprovalWithInput resolves a pending approval or user-input request.
func (a *Agent) RespondApprovalWithInput(ctx context.Context, providerName, approvalID, decision, input string) (tool.PendingApproval, error) {
	if a == nil {
		return tool.PendingApproval{}, fmt.Errorf("approval services are unavailable")
	}
	_ = ctx
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	if providerName == "" || providerName == hitlProviderRuntime || providerName == "agent" || providerName == "local" {
		if a.hitl == nil {
			return tool.PendingApproval{}, fmt.Errorf("runtime hitl gate is unavailable")
		}
		return a.hitl.Resolve(approvalID, decision, input)
	}
	if a.toolServices == nil {
		return tool.PendingApproval{}, fmt.Errorf("approval services are unavailable")
	}
	return a.toolServices.RespondApproval(ctx, providerName, approvalID, decision)
}

// Catalog 返回模型目录
func (a *Agent) Catalog() *provider.ModelCatalog {
	if a == nil {
		return nil
	}
	a.providerMu.RLock()
	catalog := a.catalog
	a.providerMu.RUnlock()
	return catalog
}

// Provider 返回当前 provider
func (a *Agent) Provider() provider.Provider {
	if a == nil {
		return nil
	}
	a.providerMu.RLock()
	p := a.provider
	a.providerMu.RUnlock()
	return p
}

// Registry 返回 provider 注册表
func (a *Agent) Registry() *provider.Registry {
	return a.registry
}

// SwitchModel switches the chat model without persisting it. It preserves the
// historic API; new callers should use SwitchModelKind with Persist enabled.
func (a *Agent) SwitchModel(modelID string) error {
	return a.SwitchModelKind(config.ModelKindChat, modelID, SwitchModelOptions{})
}

// MCPClient is retained for in-process compatibility. Hosts should inject the
// narrower MCPAdmin management port into command handlers.
func (a *Agent) MCPClient() *mcpclient.Manager {
	if a == nil {
		return nil
	}
	return a.mcpClient
}

func (a *Agent) MCPAdmin() mcpclient.Admin {
	if a == nil || a.mcpClient == nil {
		return nil
	}
	return a.mcpClient
}

// Delegate 返回子代理委派管理器
func (a *Agent) Delegate() *tool.DelegateManager {
	return a.delegate
}

// Autonomy 返回自主工作套件 (v0.38.0)
func (a *Agent) Autonomy() *autonomy.AutonomyKit {
	return a.autonomy
}

// StartAutonomy 启动自主工作套件（WorkerPool + HeartbeatEngine）。
// Autonomy 是进程级后台组件，不应绑定到单次请求的取消信号。
func (a *Agent) StartAutonomy(ctx context.Context) error {
	return a.startAutonomy(ctx, false)
}

// StartAutonomyNow explicitly starts autonomy even when autonomy.enabled is
// false. This is used by the model-visible autonomy tool: an explicit tool call
// is treated as direct intent to use the autonomy runtime for this process.
func (a *Agent) StartAutonomyNow(ctx context.Context) error {
	return a.startAutonomy(ctx, true)
}

func (a *Agent) startAutonomy(ctx context.Context, force bool) error {
	if a.autonomy == nil {
		return fmt.Errorf("autonomy kit not initialized")
	}
	if !force && a.cfg != nil {
		cfg := a.cfg.Get()
		enabled := cfg.Autonomy.Enabled
		if !enabled {
			raw := strings.TrimSpace(cfg.Extra["autonomy.enabled"])
			if raw != "" {
				parsed, err := strconv.ParseBool(strings.ToLower(raw))
				if err == nil {
					enabled = parsed
				}
			}
		}
		if !enabled {
			return nil
		}
	}

	// Create executor adapter that bridges Agent to AgentExecutor interface
	delegateCfg := a.delegate.Config()
	executor := &agentExecutorAdapter{loop: a.loopRunner(), sessions: a.sessions, delegateConfig: &delegateCfg}
	a.autonomy.SetExecutor(executor)

	if a.autonomy.Status().Started {
		a.startAutonomyResultReporter()
		return nil
	}

	if err := a.autonomy.Start(context.Background()); err != nil {
		if strings.Contains(err.Error(), "already started") {
			return nil
		}
		return err
	}
	a.startAutonomyResultReporter()

	return nil
}

// agentExecutorAdapter bridges Agent to autonomy.AgentExecutor interface
/*
agentExecutorAdapter 将 Agent 适配为 autonomy 所需的执行器接口。
*/
type agentExecutorAdapter struct {
	loop           *Loop
	sessions       *session.Manager
	delegateConfig *tool.DelegateConfig
}

/*
RunLoopWithSession 按 autonomy 接口要求在指定会话中执行一轮 Agent Loop。
*/
func (a *agentExecutorAdapter) RunLoopWithSession(ctx context.Context, sessionID string, userInput string, cfg autonomy.LoopConfig) (*autonomy.LoopResult, error) {
	if a == nil || a.loop == nil || a.sessions == nil {
		return nil, fmt.Errorf("agent loop executor not initialized")
	}
	// Look up session by ID
	sess, ok := a.sessions.Get(sessionID)
	if !ok {
		sess = a.sessions.Ensure(sessionID)
	}
	profile := strings.TrimSpace(cfg.SystemPrompt)
	if profile != "" {
		sess.SetAgentProfile("worker", profile)
	}

	var execution turn.DurableExecution
	if cfg.Execution != nil {
		execution = autonomybridge.NewTurnExecution(cfg.Execution)
	}
	loopCfg := LoopConfig{
		Execution:              execution,
		Source:                 "autonomy",
		MaxIterations:          cfg.MaxIterations,
		Timeout:                cfg.Timeout,
		AutoApprove:            cfg.AutoApprove,
		RepeatToolCallLimit:    cfg.RepeatToolCallLimit,
		ToolOnlyIterationLimit: cfg.ToolOnlyIterationLimit,
		DuplicateFetchLimit:    cfg.DuplicateFetchLimit,
		DisabledTools:          append([]string(nil), cfg.DisabledTools...),
	}
	if cfg.Execution != nil && a.delegateConfig != nil {
		task, err := cfg.Execution.Snapshot()
		if err != nil {
			return nil, err
		}
		if task.Metadata["delegate_task_id"] != "" {
			child := buildDelegateChildLoopConfig(*a.delegateConfig, task.Metadata["delegate_allow_recursive"] == "true" && a.delegateConfig.ChildAllowRecursive)
			child.Execution, child.Source = execution, "autonomy"
			loopCfg = child
			if cwd := task.Metadata["delegate_workspace"]; cwd != "" {
				sess.SetCwd(cwd)
			}
		}
	}

	result, err := a.loop.Run(ctx, sess, TextUserTurnInput(userInput), loopCfg)
	if result == nil {
		return nil, err
	}
	return &autonomy.LoopResult{
		Response:     result.Response,
		TokensUsed:   result.TokensUsed,
		Iterations:   result.Iterations,
		Verified:     result.Verified,
		Verification: result.Verification,
	}, err
}

/*
NewSession 创建 autonomy 使用的新会话并返回其 ID。
*/
func (a *agentExecutorAdapter) NewSession(title string) string {
	if a == nil || a.sessions == nil {
		return ""
	}
	sess := a.sessions.NewWithTitle(title)
	return sess.ID
}

// EnsureSession returns a deterministic session for a long-lived worker or
// another runtime-owned actor. Existing history is preserved across process
// restarts; a new session receives the supplied title.
func (a *agentExecutorAdapter) EnsureSession(id, title string) string {
	if a == nil || a.sessions == nil {
		return ""
	}
	sess := a.sessions.Ensure(strings.TrimSpace(id))
	if strings.TrimSpace(sess.Title) == "" && strings.TrimSpace(title) != "" {
		sess.SetTitle(strings.TrimSpace(title))
	}
	if strings.HasPrefix(strings.TrimSpace(id), "worker-") {
		sess.SetAgentProfile("worker", "You are a long-lived LuckyAgent worker. Process queued work within the assigned limits, preserve evidence, and report blockers or unfinished work clearly.")
	}
	// Worker sessions are UI entry points and may have no task messages of
	// their own (work runs in isolated task sessions). Persist their metadata
	// now so the deterministic id and role survive a process restart.
	_ = sess.Save()
	return sess.ID
}

// Gateway 返回统一工具网关
func (a *Agent) Gateway() *tool.Gateway {
	return a.gateway
}

// MsgGateway 返回消息平台网关管理器 (v0.6.0)
func (a *Agent) MsgGateway() *gateway.GatewayManager {
	return a.msgGateway
}

// LoadSkills 从目录加载 Skill 插件。
//
// The whole body runs under skillLoadMu so two concurrent reloads cannot
// interleave. skillMu is taken only for the final field swap and is never held
// across a call into SkillRegistry or tool.Registry.
// SetSessionEventSink receives assistant text that should appear in a chat
// session even when no WebSocket client started the run. Cron delivery uses it.
func (a *Agent) SetSessionEventSink(sink func(sessionID, content string)) {
	if a == nil {
		return
	}
	a.sessionEvents = sink
}

// LoadSkills 从目录加载 Skill 插件。
//
// The whole body runs under skillLoadMu so two concurrent reloads cannot
// interleave. skillMu is taken only for the final field swap and is never held
// across a call into SkillRegistry or tool.Registry.
func (a *Agent) LoadSkills(skillsDir string) (int, error) {
	a.skillLoadMu.Lock()
	defer a.skillLoadMu.Unlock()

	if a.tools == nil {
		a.tools = tool.NewRegistry()
	}
	loader := tool.NewSkillLoader(skillsDir)
	skillRegistry := tool.NewSkillRegistry(a.tools, loader)

	// Parse phase. Nothing has been written into a.tools yet, so a failure here
	// leaves the previous generation completely intact.
	if _, err := skillRegistry.Discover(); err != nil {
		return 0, fmt.Errorf("discover skills: %w", err)
	}
	if err := skillRegistry.LoadAll(); err != nil {
		return 0, fmt.Errorf("load skills: %w", err)
	}
	if err := skillRegistry.ValidateAll(); err != nil {
		return 0, fmt.Errorf("validate skills: %w", err)
	}

	// Retire the previous generation. Without this, LoadSkills leaked: old
	// skill_<name>_<tool> entries stayed in a.tools forever and were only ever
	// replaced when the new generation happened to produce a byte-identical full
	// name. Renamed, removed, or re-tooled skills left orphans behind — still
	// enabled, still advertised to the model.
	expected := make(map[string]struct{})
	for _, info := range skillRegistry.SkillInfos() {
		for _, td := range info.Tools {
			expected[fmt.Sprintf("skill_%s_%s", info.Name, td.Name)] = struct{}{}
		}
	}
	a.skillMu.RLock()
	prev := a.skillRegistry
	a.skillMu.RUnlock()
	if prev != nil {
		prev.UnloadAll()
	}
	// Belt and braces: sweep any skill-sourced tool that the new generation does
	// not claim. This also catches orphans from a previous registry that had
	// already errored out partway through. skill_read carries no Source, which is
	// how it is excluded here.
	for _, t := range a.tools.ListByCategory(tool.CatSkill) {
		if t.Source == "" {
			continue
		}
		if _, keep := expected[t.Name]; keep {
			continue
		}
		a.tools.Unregister(t.Name)
	}

	if err := skillRegistry.RegisterAll(); err != nil {
		return 0, fmt.Errorf("register skills: %w", err)
	}
	// Skills land registered-but-disabled when auto-enable is off, so a freshly
	// installed skill is not callable until it is explicitly turned on.
	if !a.requireExplicitSkillEnable() {
		if err := skillRegistry.EnableAll(); err != nil {
			return 0, fmt.Errorf("enable skills: %w", err)
		}
	}

	skills := skillRegistry.SkillInfos()

	// skill_read closes over the slice handed to NewSkillToolService, so it must
	// be rebuilt against the new generation. Registry.Register overwrites by name,
	// so the previous service instance simply becomes garbage.
	a.skillMu.RLock()
	readableSkills := append(append([]*tool.SkillInfo(nil), skills...), a.pluginSkills...)
	a.skillMu.RUnlock()
	tool.NewSkillToolService(readableSkills).RegisterReadTool(a.tools)

	a.skillMu.Lock()
	a.skillRegistry = skillRegistry
	a.skills = skills
	a.skillsDir = skillsDir
	a.skillMu.Unlock()

	return len(skills), nil
}

// SkillInstaller returns the install manager, lazily constructing it from the
// current config. It is nil only when the agent has no config manager.
func (a *Agent) SkillInstaller() *tool.InstallManager {
	if a == nil || a.cfg == nil {
		return nil
	}
	a.skillMu.RLock()
	existing := a.skillInstall
	a.skillMu.RUnlock()
	if existing != nil {
		return existing
	}

	// Everything below is computed outside the lock: skillMu guards fields only,
	// and taking it around config or registry calls would invent a lock order.
	c := a.cfg.Get()
	home := a.cfg.HomeDir()
	inst := c.Skills.Install

	mgr := tool.NewInstallManager(tool.InstallManagerConfig{
		SkillsDir:   resolveSkillsDir(a.cfg),
		StagingDir:  filepath.Join(home, "skills-staging"),
		VersionsDir: filepath.Join(home, "skills-versions"),
		LedgerPath:  filepath.Join(home, "runtime", "skills.json"),
		Limits: tool.InstallLimits{
			MaxArchiveBytes:      inst.MaxArchiveBytes,
			MaxUncompressedBytes: inst.MaxUncompressedBytes,
			MaxSingleFileBytes:   inst.MaxSingleFileBytes,
			MaxFiles:             inst.MaxFiles,
			MaxCompressionRatio:  inst.MaxCompressionRatio,
			MaxPathDepth:         inst.MaxPathDepth,
			BlockOnSecrets:       inst.BlockOnSecrets,
		},
		ProbeCLI:          inst.ProbeCLI,
		ProbeTimeout:      time.Duration(inst.ProbeTimeoutSeconds) * time.Second,
		TrialBudget:       time.Duration(inst.TrialTimeoutSeconds) * time.Second,
		AllowedLocalRoots: inst.AllowedLocalRoots,
		KeepVersions:      inst.KeepVersions,
		StagingTTL:        time.Duration(inst.StagingTTLHours) * time.Hour,
	})
	// ReloadSkills takes skillLoadMu, and SkillRegistry/Tools take skillMu — none
	// of which is held here by the time these closures run.
	mgr.SetRuntime(a.ReloadSkills, a.SkillRegistry, a.Tools)

	a.skillMu.Lock()
	defer a.skillMu.Unlock()
	// Another caller may have won the race while we were building; prefer theirs
	// so every caller shares one manager and therefore one set of per-name locks.
	if a.skillInstall == nil {
		a.skillInstall = mgr
	}
	return a.skillInstall
}

// resolveSkillsDir returns the configured skills directory, defaulting to
// <home>/skills.
func resolveSkillsDir(cfg *config.Manager) string {
	if cfg == nil {
		return ""
	}
	if c := cfg.Get(); c != nil {
		if dir := strings.TrimSpace(c.Skills.Dir); dir != "" {
			return dir
		}
	}
	return filepath.Join(cfg.HomeDir(), "skills")
}

// requireExplicitSkillEnable reports whether skills must be turned on by hand
// after loading. Absent configuration means "auto-enable", preserving the
// behavior of every config.json written before the skills block existed.
func (a *Agent) requireExplicitSkillEnable() bool {
	if a == nil || a.cfg == nil {
		return false
	}
	c := a.cfg.Get()
	if c == nil || c.Skills.AutoEnable == nil {
		return false
	}
	return !*c.Skills.AutoEnable
}

// ReloadSkills re-runs LoadSkills against the directory the agent last loaded
// from, falling back to the configured skills directory.
func (a *Agent) ReloadSkills() (int, error) {
	dir := a.SkillsDir()
	if dir == "" {
		dir = resolveSkillsDir(a.cfg)
	}
	if dir == "" {
		return 0, fmt.Errorf("skills directory is not configured")
	}
	return a.LoadSkills(dir)
}

// SkillRegistry returns the lifecycle registry for loaded skills.
func (a *Agent) SkillRegistry() *tool.SkillRegistry {
	if a == nil {
		return nil
	}
	a.skillMu.RLock()
	defer a.skillMu.RUnlock()
	return a.skillRegistry
}

// Skills 返回已加载的 skill 列表。
//
// The returned slice is treated as immutable after publication: SkillInfos()
// already hands back per-skill copies, and LoadSkills replaces the whole slice
// rather than mutating it in place.
func (a *Agent) Skills() []*tool.SkillInfo {
	return a.snapshotSkills()
}

// snapshotSkills reads the published skill slice under the guard. Internal
// callers on the chat hot path should call this once and reuse the result rather
// than touching a.skills directly.
func (a *Agent) snapshotSkills() []*tool.SkillInfo {
	if a == nil {
		return nil
	}
	a.skillMu.RLock()
	defer a.skillMu.RUnlock()
	return append(append([]*tool.SkillInfo(nil), a.skills...), a.pluginSkills...)
}

// SkillsDir returns the directory the agent last loaded skills from.
func (a *Agent) SkillsDir() string {
	if a == nil {
		return ""
	}
	a.skillMu.RLock()
	defer a.skillMu.RUnlock()
	return a.skillsDir
}

// ConnectMCPServer 连接 MCP Server
func (a *Agent) ConnectMCPServer(name, url, apiKey string) error {
	if a == nil || a.mcpClient == nil {
		return fmt.Errorf("MCP runtime unavailable")
	}
	return a.mcpClient.Connect(context.Background(), name, mcpclient.ServerConfig{URL: url, APIKey: apiKey})
}

// Sessions 返回会话管理器
func (a *Agent) Sessions() *session.Manager {
	return a.sessions
}

func (a *Agent) TaskStore() taskstore.Store {
	if a == nil {
		return nil
	}
	return a.taskStore
}

// Config 返回配置管理器
func (a *Agent) Config() *config.Manager {
	return a.cfg
}

// ContextEstimator returns the token estimator used by context planning.
// The planner consumes it through its narrow runtime contract.
func (a *Agent) ContextEstimator() *contextx.TokenEstimator {
	if a == nil {
		return nil
	}
	return a.contextEst
}

// ContextCache exposes the planner cache through its minimal storage contract.
func (a *Agent) ContextCache() contextMessageCacheStore {
	if a == nil {
		return nil
	}
	return a.contextCache
}

// Metrics 返回指标收集器
func (a *Agent) Metrics() *metrics.Metrics {
	return a.metrics
}

// CronEngine 返回定时任务引擎
func (a *Agent) CronEngine() *cron.Engine {
	return a.cronEngine
}

// CronStore 返回 cron 持久化存储
func (a *Agent) CronStore() *cron.Store {
	return a.cronStore
}

// Memory 返回记忆存储
func (a *Agent) Memory() memory.Inspector {
	return a.memory
}

// ContextMemory exposes only the evidence and feedback operations used while
// planning a turn. Context assembly cannot write facts or invoke maintenance.
func (a *Agent) ContextMemory() memory.ContextReader {
	if a == nil {
		return nil
	}
	return a.memory
}

// MidTermStore returns the store used for query-aware session summaries.
func (a *Agent) MidTermStore() memory.MidTermStore {
	if a == nil {
		return nil
	}
	return a.midTerm
}

// ShortTermSummary returns the session-scoped short-term summary used by the
// context planner.
func (a *Agent) ShortTermSummary(sess *session.Session) string {
	if a == nil {
		return ""
	}
	if a.shortTerms != nil && sess != nil {
		return a.shortTerms.Summary(sess.ID)
	}
	return ""
}

// ContextWindow 返回上下文窗口管理器
func (a *Agent) ContextWindow() *contextx.ContextWindow {
	return a.contextWin
}

// FitContext 裁剪消息列表到上下文窗口内
func (a *Agent) FitContext(messages []contextx.Message) ([]contextx.Message, contextx.TrimResult) {
	return a.contextWin.Fit(messages)
}

// ContextStats 返回上下文窗口统计
func (a *Agent) ContextStats(messages []contextx.Message) contextx.ContextStats {
	return a.contextWin.Stats(messages)
}

// ContextCacheStats returns local context cache statistics.
func (a *Agent) ContextCacheStats() map[string]any {
	if a == nil || a.contextCache == nil {
		return map[string]any{}
	}
	stats := a.contextCache.Stats()
	return map[string]any{
		"entries":   stats.Entries,
		"hits":      stats.Hits,
		"misses":    stats.Misses,
		"evictions": stats.Evictions,
		"expired":   stats.Expired,
		"ttl":       stats.TTL.String(),
	}
}

// RAG 返回 RAG 管理器
func (a *Agent) RAG() *rag.RAGManager {
	return a.ragManager
}

// MemoryVaultPath returns the configured durable memory vault path.
func (a *Agent) MemoryVaultPath() string {
	if a == nil {
		return ""
	}
	return a.memoryVaultPath()
}

// RAGPersist 返回 RAG 持久化管理器
func (a *Agent) RAGPersist() *rag.Persistence {
	return a.ragPersist
}

// StreamIndexer 返回流式索引器 (v0.23.0)
func (a *Agent) StreamIndexer() *rag.StreamIndexer {
	return a.streamIndexer
}

// EmbedderRegistry 返回嵌入模型注册表
func (a *Agent) EmbedderRegistry() *embedder.Registry {
	return a.embedderReg
}

// SwitchEmbedder changes the active embedding provider and rebinds the RAG
// pipeline. A populated index cannot change embedding identity or dimensions;
// callers must rebuild it first.
func (a *Agent) SwitchEmbedder(id string) error {
	if a.embedderReg == nil {
		return fmt.Errorf("embedder registry not available")
	}
	next, ok := a.embedderReg.Get(id)
	if !ok {
		return fmt.Errorf("embedder not found: %s", id)
	}
	if a.ragManager != nil {
		cached := embedder.NewCachedEmbedder(next, 512)
		if err := a.ragManager.ReconfigureEmbedder(cached); err != nil {
			return err
		}
	}
	if !a.embedderReg.Switch(id) {
		return fmt.Errorf("embedder not found: %s", id)
	}
	return nil
}

// AgentRegistry 返回 Agent 协作注册表 (v0.22.0)
func (a *Agent) AgentRegistry() *collab.Registry {
	return a.collabReg
}

// CollabManager 返回协作任务管理器 (v0.22.0)
func (a *Agent) CollabManager() *collab.DelegateManager {
	return a.collabMgr
}

// Close 释放资源，保存持久化数据
func (a *Agent) Close() error {
	if a == nil {
		return nil
	}
	a.recaps.close()
	if a.components != nil {
		a.closeOnce.Do(func() {
			a.stopAutonomyResultReporter()
			if a.heartbeatSvc != nil {
				a.closeError = a.heartbeatSvc.Stop()
			}
			if a.ownComponents {
				if err := a.components.Close(); err != nil && a.closeError == nil {
					a.closeError = err
				}
			}
		})
		return a.closeError
	}

	var firstErr error

	if a.toolServices != nil {
		if err := a.toolServices.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close tool services: %w", err)
		}
	}

	if a.autonomy != nil && a.autonomy.Status().Started {
		a.stopAutonomyResultReporter()
		if err := a.autonomy.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stop autonomy: %w", err)
		}
	}
	if a.cronEngine != nil {
		a.cronEngine.Stop()
	}
	if a.heartbeatSvc != nil {
		if err := a.heartbeatSvc.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stop heartbeat: %w", err)
		}
	}
	if a.proactiveRuntime != nil {
		if err := a.proactiveRuntime.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stop proactive runtime: %w", err)
		}
	}
	if a.memory != nil {
		if err := a.memory.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close memory: %w", err)
		}
	}
	if a.proactiveStore != nil {
		if err := a.proactiveStore.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close proactive store: %w", err)
		}
	}
	if a.computerMgr != nil {
		if err := a.computerMgr.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close computer manager: %w", err)
		}
	}
	if a.mcpClient != nil {
		if err := a.mcpClient.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close MCP manager: %w", err)
		}
	}

	// SQLite 后端自动持久化，只需关闭连接
	if a.ragManager != nil && a.ragManager.IsSQLite() {
		if err := a.ragManager.CloseStore(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close sqlite store: %w", err)
		}
	} else if a.ragPersist != nil && a.ragManager != nil {
		// 内存后端：关闭时保存到 JSON
		if err := a.ragPersist.Save(a.ragManager); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("save RAG index: %w", err)
		}
	}

	return firstErr
}

// toContextMessages 将 provider.Message 转换为 contextx.Message
/*
toContextMessages 将 provider 消息转换为带优先级的上下文消息。
*/
func (a *Agent) toContextMessages(messages []provider.Message) []contextx.Message {
	result := make([]contextx.Message, len(messages))
	latestUser := latestUserMessageIndex(messages)
	latestCall, latestCallEnd := -1, -1
	for i, msg := range messages {
		if len(msg.ToolCalls) > 0 {
			latestCall, latestCallEnd = i, i+1
			for latestCallEnd < len(messages) && messages[latestCallEnd].Role == "tool" {
				latestCallEnd++
			}
		}
	}
	hasCurrentTask := false
	for _, msg := range messages {
		hasCurrentTask = hasCurrentTask || strings.HasPrefix(msg.Content, "[Current Task]")
	}
	for i, msg := range messages {
		priority := contextx.PriorityNormal
		category := msg.Role

		// system 消息是 critical
		if msg.Role == "system" {
			priority = contextx.PriorityCritical
			category = "system"
		}

		// 记忆上下文按层级分配优先级
		if msg.Role == "system" && len(msg.Content) > 0 {
			switch {
			case strings.HasPrefix(msg.Content, "[Task State]"):
				priority = contextx.PriorityHigh
				category = "task_state"
			case isContextEvidence(msg.Content):
				priority = contextx.PriorityNormal
				category = "history_summary"
			case strings.HasPrefix(msg.Content, "[Core Memory"):
				priority = contextx.PriorityHigh
				category = "memory_long"
			case strings.HasPrefix(msg.Content, "[Working Memory"):
				priority = contextx.PriorityNormal
				category = "memory_medium"
			case strings.HasPrefix(msg.Content, "[Recent Context"):
				priority = contextx.PriorityLow
				category = "memory_short"
			case strings.HasPrefix(msg.Content, "[Session History"):
				priority = contextx.PriorityNormal
				category = "memory_mid"
			case strings.HasPrefix(msg.Content, "[Conversation Summary"), strings.HasPrefix(msg.Content, "[Conversation Themes"):
				priority = contextx.PriorityLow
				category = "conversation_summary"
			case strings.HasPrefix(msg.Content, "## Retrieved Knowledge"), strings.HasPrefix(msg.Content, "[Retrieved Knowledge"):
				priority = contextx.PriorityHigh
				category = "rag"
			}
		}
		if msg.Role == "tool" {
			priority = contextx.PriorityNormal
			category = "tool_result"
		}
		if i == latestUser && !hasCurrentTask {
			priority = contextx.PriorityCritical
			category = "current_request"
		}
		if i >= latestCall && i < latestCallEnd && latestCall >= 0 {
			priority = contextx.PriorityHigh
		}

		result[i] = contextx.Message{
			Role:             msg.Role,
			Content:          msg.Content,
			ReasoningContent: msg.ReasoningContent,
			Name:             msg.Name,
			ToolCallID:       msg.ToolCallID,
			ToolCalls:        toContextToolCalls(msg.ToolCalls),
			Priority:         priority,
			Category:         category,
			Timestamp:        time.Now(),
		}
	}
	return result
}

// fromContextMessages 将 contextx.Message 转换回 provider.Message
/*
fromContextMessages 将上下文消息转换回 provider 消息格式。
*/
func (a *Agent) fromContextMessages(messages []contextx.Message) []provider.Message {
	result := make([]provider.Message, len(messages))
	for i, msg := range messages {
		result[i] = provider.Message{
			Role:             msg.Role,
			Content:          msg.Content,
			ReasoningContent: msg.ReasoningContent,
			ToolCallID:       msg.ToolCallID,
			Name:             msg.Name,
			ToolCalls:        fromContextToolCalls(msg.ToolCalls),
		}
	}
	return result
}

func toContextToolCalls(calls []provider.ToolCall) []contextx.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]contextx.ToolCall, len(calls))
	for i, call := range calls {
		out[i] = contextx.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
		}
	}
	return out
}

func fromContextToolCalls(calls []contextx.ToolCall) []provider.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]provider.ToolCall, len(calls))
	for i, call := range calls {
		out[i] = provider.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
		}
	}
	return out
}
