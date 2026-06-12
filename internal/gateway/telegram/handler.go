package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/cli/profile"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/embedder"
	"github.com/yurika0211/luckyharness/internal/gateway"
	luckycollector "github.com/yurika0211/luckyharness/internal/gateway/collector"
	"github.com/yurika0211/luckyharness/internal/learning"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/metrics"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/rag"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/soul"
	"github.com/yurika0211/luckyharness/internal/tool"
	"github.com/yurika0211/luckyharness/internal/utils"
)

type chatRuntime interface {
	Chat(ctx context.Context, userInput string) (string, error)
	ChatWithSession(ctx context.Context, sessionID, userInput string) (string, error)
	ChatWithSessionInput(ctx context.Context, sessionID string, input agent.UserTurnInput) (string, error)
	ChatWithSessionStream(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error)
	ChatWithSessionStreamInput(ctx context.Context, sessionID string, input agent.UserTurnInput) (<-chan agent.ChatEvent, error)
	ProgressFeedback(ctx context.Context, userInput string, round int, observations []string) (string, error)
	AnalyzeAttachments(ctx context.Context, attachments []gateway.Attachment) (string, error)
}

type stateRuntime interface {
	Sessions() *session.Manager
	Config() agentConfigProvider
	SwitchModel(modelID string) error
	Soul() *soul.Soul
	Tools() *tool.Registry
	CronEngine() *cron.Engine
	Skills() []*tool.SkillInfo
	Metrics() *metrics.Metrics
	Memory() *memory.Store
	Remember(content, category string) error
	RememberLongTerm(content, category string) error
	Recall(query string) []memory.Entry
	MemoryStats() map[memory.Tier]int
	DecayMemory(threshold float64) int
	PromoteMemory(id string) error
	Catalog() *provider.ModelCatalog
	RAG() *rag.RAGManager
	ConnectMCPServer(name, url, apiKey string)
	ContextWindowConfig() contextWindowSnapshot
	ContextCacheStats() map[string]any
	EmbedderRegistry() *embedder.Registry
}

type agentProvider interface {
	chatRuntime
	stateRuntime
}

type activeRouteRecorder interface {
	RecordRecentChatTarget(platform, chatID, replyToMsgID string)
}

type replyAnchorResolver interface {
	ResolveExternalReplyAnchor(platform, chatID, messageID string) (sessionID string, ok bool)
}

type telegramSender interface {
	gateway.StreamGateway
	SendPhoto(ctx context.Context, chatID string, replyToMsgID string, source string, caption string) error
	SendDocument(ctx context.Context, chatID string, replyToMsgID string, source string, caption string) error
	SendHTML(ctx context.Context, chatID string, message string) error
	SendWithReplyHTML(ctx context.Context, chatID string, replyToMsgID string, message string) error
	SendTypingLoop(ctx context.Context, chatID string)
	ReactToMessage(chatID string, messageID string, emoji string)
}

// agentConfigProvider 定义 Handler 需要从 config 获得的能力接口。
type agentConfigProvider interface {
	Get() agentConfigSnapshot
}

type mutableAgentConfigProvider interface {
	agentConfigProvider
	Set(key, value string) error
	Save() error
}

// agentConfigSnapshot 是 config 快照的最小子集。
type agentConfigSnapshot struct {
	HomeDir                   string
	ConfigFile                string
	Model                     string
	Provider                  string
	APIKey                    string
	APIBase                   string
	SoulPath                  string
	MaxTokens                 int
	Temperature               float64
	ServerAddr                string
	DashboardAddr             string
	MsgGatewayPlatform        string
	MsgGatewayStartAll        bool
	MsgGatewayAPIAddr         string
	MsgGatewayTelegramToken   string
	MsgGatewayTelegramProxy   string
	MsgGatewayQQAppID         string
	MsgGatewayQQSandbox       bool
	MsgGatewayWeixinAccountID string
	MsgGatewayOpenClawAccount string
	ChatTimeoutSeconds        int
	ProgressAsMessages        bool
	ProgressAsNaturalLanguage bool
	ProgressSummaryWithLLM    bool
	ShowToolDetailsInResult   bool
	ModelRouterEnabled        bool
}

type contextWindowSnapshot struct {
	MaxTokens            int
	ReservedTokens       int
	Strategy             string
	SlidingWindowSize    int
	MaxConversationTurns int
	MemoryBudget         int
	SummarizeThreshold   float64
}

// agentProviderAdapter 将 *agent.Agent 适配为 agentProvider 接口。
type agentProviderAdapter struct {
	inner *agent.Agent
}

func (a agentProviderAdapter) Sessions() *session.Manager {
	return a.inner.Sessions()
}

func (a agentProviderAdapter) Config() agentConfigProvider {
	return agentConfigWrapper{a.inner.Config()}
}

func (a agentProviderAdapter) SwitchModel(modelID string) error {
	return a.inner.SwitchModel(modelID)
}

func (a agentProviderAdapter) Soul() *soul.Soul {
	return a.inner.Soul()
}

func (a agentProviderAdapter) Tools() *tool.Registry {
	return a.inner.Tools()
}

func (a agentProviderAdapter) Skills() []*tool.SkillInfo {
	return a.inner.Skills()
}

func (a agentProviderAdapter) CronEngine() *cron.Engine {
	return a.inner.CronEngine()
}

func (a agentProviderAdapter) Chat(ctx context.Context, userInput string) (string, error) {
	return a.inner.Chat(ctx, userInput)
}

func (a agentProviderAdapter) ChatWithSession(ctx context.Context, sessionID, userInput string) (string, error) {
	return a.inner.ChatWithSession(ctx, sessionID, userInput)
}

func (a agentProviderAdapter) ChatWithSessionInput(ctx context.Context, sessionID string, input agent.UserTurnInput) (string, error) {
	return a.inner.ChatWithSessionInput(ctx, sessionID, input)
}

func (a agentProviderAdapter) ChatWithSessionStream(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
	return a.inner.ChatWithSessionStream(ctx, sessionID, userInput)
}

func (a agentProviderAdapter) ChatWithSessionStreamInput(ctx context.Context, sessionID string, input agent.UserTurnInput) (<-chan agent.ChatEvent, error) {
	return a.inner.ChatWithSessionStreamInput(ctx, sessionID, input)
}

func (a agentProviderAdapter) ProgressFeedback(ctx context.Context, userInput string, round int, observations []string) (string, error) {
	return a.inner.ProgressFeedback(ctx, userInput, round, observations)
}

func (a agentProviderAdapter) AnalyzeAttachments(ctx context.Context, attachments []gateway.Attachment) (string, error) {
	return a.inner.AnalyzeAttachments(ctx, attachments)
}

func (a agentProviderAdapter) Metrics() *metrics.Metrics {
	return a.inner.Metrics()
}

func (a agentProviderAdapter) Memory() *memory.Store {
	return a.inner.Memory()
}

func (a agentProviderAdapter) Remember(content, category string) error {
	return a.inner.Remember(content, category)
}

func (a agentProviderAdapter) RememberLongTerm(content, category string) error {
	return a.inner.RememberLongTerm(content, category)
}

func (a agentProviderAdapter) Recall(query string) []memory.Entry {
	return a.inner.Recall(query)
}

func (a agentProviderAdapter) MemoryStats() map[memory.Tier]int {
	return a.inner.MemoryStats()
}

func (a agentProviderAdapter) DecayMemory(threshold float64) int {
	return a.inner.DecayMemory(threshold)
}

func (a agentProviderAdapter) PromoteMemory(id string) error {
	return a.inner.PromoteMemory(id)
}

func (a agentProviderAdapter) Catalog() *provider.ModelCatalog {
	return a.inner.Catalog()
}

func (a agentProviderAdapter) RAG() *rag.RAGManager {
	return a.inner.RAG()
}

func (a agentProviderAdapter) ConnectMCPServer(name, url, apiKey string) {
	a.inner.ConnectMCPServer(name, url, apiKey)
}

func (a agentProviderAdapter) ContextWindowConfig() contextWindowSnapshot {
	cw := a.inner.ContextWindow()
	if cw == nil {
		return contextWindowSnapshot{}
	}
	cfg := cw.Config()
	return contextWindowSnapshot{
		MaxTokens:            cfg.MaxTokens,
		ReservedTokens:       cfg.ReservedTokens,
		Strategy:             cfg.Strategy.String(),
		SlidingWindowSize:    cfg.SlidingWindowSize,
		MaxConversationTurns: cfg.MaxConversationTurns,
		MemoryBudget:         cfg.MemoryBudget,
		SummarizeThreshold:   cfg.SummarizeThreshold,
	}
}

func (a agentProviderAdapter) ContextCacheStats() map[string]any {
	return a.inner.ContextCacheStats()
}

func (a agentProviderAdapter) EmbedderRegistry() *embedder.Registry {
	return a.inner.EmbedderRegistry()
}

func (a agentProviderAdapter) ResolveExternalReplyAnchor(platform, chatID, messageID string) (string, bool) {
	return a.inner.ResolveExternalReplyAnchor(platform, chatID, messageID)
}

// agentConfigWrapper 将 *config.Manager 适配为 agentConfigProvider 接口。
type agentConfigWrapper struct {
	mgr *config.Manager
}

func (w agentConfigWrapper) Get() agentConfigSnapshot {
	cfg := w.mgr.Get()
	return agentConfigSnapshot{
		HomeDir:                   w.mgr.HomeDir(),
		ConfigFile:                w.mgr.ConfigFile(),
		Model:                     cfg.Model,
		Provider:                  cfg.Provider,
		APIKey:                    cfg.APIKey,
		APIBase:                   cfg.APIBase,
		SoulPath:                  cfg.SoulPath,
		MaxTokens:                 cfg.MaxTokens,
		Temperature:               cfg.Temperature,
		ServerAddr:                cfg.Server.Addr,
		DashboardAddr:             cfg.Dashboard.Addr,
		MsgGatewayPlatform:        cfg.MsgGateway.Platform,
		MsgGatewayStartAll:        cfg.MsgGateway.StartAll,
		MsgGatewayAPIAddr:         cfg.MsgGateway.APIAddr,
		MsgGatewayTelegramToken:   cfg.MsgGateway.Telegram.Token,
		MsgGatewayTelegramProxy:   cfg.MsgGateway.Telegram.Proxy,
		MsgGatewayQQAppID:         cfg.MsgGateway.QQOfficial.AppID,
		MsgGatewayQQSandbox:       cfg.MsgGateway.QQOfficial.Sandbox,
		MsgGatewayWeixinAccountID: cfg.MsgGateway.Weixin.AccountID,
		MsgGatewayOpenClawAccount: cfg.MsgGateway.OpenClawWeixin.AccountID,
		ChatTimeoutSeconds:        cfg.MsgGateway.Telegram.ChatTimeoutSeconds,
		ProgressAsMessages:        cfg.MsgGateway.Telegram.ProgressAsMessages,
		ProgressAsNaturalLanguage: cfg.MsgGateway.Telegram.ProgressAsNaturalLanguage,
		ProgressSummaryWithLLM:    cfg.MsgGateway.Telegram.ProgressSummaryWithLLM,
		ShowToolDetailsInResult:   cfg.MsgGateway.Telegram.ShowToolDetailsInResult,
		ModelRouterEnabled:        cfg.ModelRouter.Enable,
	}
}

func (w agentConfigWrapper) Set(key, value string) error {
	return w.mgr.Set(key, value)
}

func (w agentConfigWrapper) Save() error {
	return w.mgr.Save()
}

type telegramCommandHandler func(ctx context.Context, msg *gateway.Message) error

// Handler processes Telegram bot commands and messages with per-chat session management.
type Handler struct {
	adapter  telegramSender
	agent    agentProvider
	chat     chatRuntime
	state    stateRuntime
	recorder activeRouteRecorder
	commands map[string]telegramCommandHandler
	watcher  *cron.Watcher

	mu         sync.RWMutex
	sessions   map[string]string // chatID → sessionID
	tasks      map[string]*chatTask
	queues     map[string]*chatQueue
	lucky      *luckycollector.Lucky
	restarting bool

	// v0.44.0: chatID→sessionID 映射持久化
	dataDir string

	// 对话总超时（防止长任务无限占用）；可配置，默认 10 分钟
	chatStreamTimeout time.Duration
	// 中间思考/工具步骤是否作为独立消息发送
	progressAsMessages bool
	// 中间步骤是否转成自然语言进度播报，并在最后统一输出结论
	progressAsNaturalLanguage bool
	// 每轮未完成时是否发送一条由 LLM 生成的总结性反馈
	progressSummaryWithLLM bool
	// 最终回答前是否附上自然语言工具摘要
	showToolDetailsInResult bool

	memeDir         string
	memeProbability float64
	memeCooldown    time.Duration
	memeRand        *rand.Rand
	memeNow         func() time.Time
	memeLastSent    map[string]time.Time
}

type chatTask struct {
	cancel context.CancelFunc
}

type queuedChatRequest struct {
	ctx   context.Context
	msg   *gateway.Message
	input agent.UserTurnInput
}

type chatQueue struct {
	running bool
	items   []*queuedChatRequest
}

const defaultChatStreamTimeout = 10 * time.Minute

// chatSessionsData 是持久化的 chatID→sessionID 映射
type chatSessionsData struct {
	ChatSessions map[string]string `json:"chat_sessions"`
}

// NewHandler creates a new Telegram command handler.
func NewHandler(adapter *Adapter, a *agent.Agent) *Handler {
	var runtime agentProviderAdapter
	var chat chatRuntime
	var state stateRuntime
	var recorder activeRouteRecorder
	if a != nil {
		runtime = agentProviderAdapter{a}
		chat = runtime
		state = runtime
		recorder = a
	}
	memeDir, memeProbability, memeCooldown := resolveRandomMemeConfig()
	h := &Handler{
		adapter:                   adapter,
		agent:                     runtime,
		chat:                      chat,
		state:                     state,
		recorder:                  recorder,
		commands:                  make(map[string]telegramCommandHandler),
		watcher:                   cron.NewWatcher(resolveCronEngine(state)),
		sessions:                  make(map[string]string),
		tasks:                     make(map[string]*chatTask),
		queues:                    make(map[string]*chatQueue),
		lucky:                     luckycollector.NewLucky(),
		dataDir:                   "", // 默认不持久化，需 SetDataDir 启用
		chatStreamTimeout:         resolveChatStreamTimeout(state),
		progressAsMessages:        resolveProgressAsMessages(state),
		progressAsNaturalLanguage: resolveProgressAsNaturalLanguage(state),
		progressSummaryWithLLM:    resolveProgressSummaryWithLLM(state),
		showToolDetailsInResult:   resolveShowToolDetailsInResult(state),
		memeDir:                   memeDir,
		memeProbability:           memeProbability,
		memeCooldown:              memeCooldown,
		memeRand:                  rand.New(rand.NewSource(time.Now().UnixNano())),
		memeNow:                   time.Now,
		memeLastSent:              make(map[string]time.Time),
	}
	h.commands = h.buildCommandRegistry()
	return h
}

func (h *Handler) buildCommandRegistry() map[string]telegramCommandHandler {
	handlers := map[string]telegramCommandHandler{
		"start":   h.handleStart,
		"help":    h.handleHelp,
		"review":  h.handleReview,
		"init":    h.handleInit,
		"config":  h.handleConfig,
		"version": h.handleVersion,
		"model":   h.handleModel,
		"models":  h.handleModels,
		"soul":    h.handleSoul,
		"tools":   h.handleTools,
		"reset":   h.handleReset,
		"history": h.handleHistory,
		"session": h.handleSession,
		"chat": func(ctx context.Context, msg *gateway.Message) error {
			return h.dispatchChatAsync(ctx, msg, agent.TextUserTurnInput(msg.Args))
		},
		"lucky":          h.handleLucky,
		"sessions":       h.handleSessions,
		"resume":         h.handleResume,
		"rename":         h.handleRename,
		"skills":         h.handleSkills,
		"mcp":            h.handleMCP,
		"approve":        h.handleApprove,
		"deny":           h.handleDeny,
		"cron":           h.handleCron,
		"watch":          h.handleWatch,
		"dashboard":      h.handleDashboard,
		"msg_gateway":    h.handleMsgGateway,
		"rag":            h.handleRAG,
		"context":        h.handleContext,
		"fc":             h.handleFC,
		"embedder":       h.handleEmbedder,
		"metrics":        h.handleMetrics,
		"health":         h.handleHealth,
		"learn":          h.handleLearn,
		"learn_start":    h.handleLearnStart,
		"learn_current":  h.handleLearnCurrent,
		"learn_lab":      h.handleLearnLab,
		"learn_submit":   h.handleLearnSubmit,
		"learn_progress": h.handleLearnProgress,
		"remember":       h.handleRemember,
		"remember_long":  h.handleRememberLong,
		"recall":         h.handleRecall,
		"memstats":       h.handleMemStats,
		"memdecay":       h.handleMemDecay,
		"promote":        h.handlePromote,
		"profile":        h.handleProfile,
		"new":            h.handleNew,
		"stop":           h.handleStop,
		"status":         h.handleStatus,
		"restart":        h.handleRestart,
	}
	registry := make(map[string]telegramCommandHandler, len(handlers))
	for _, name := range telegramCommandNames() {
		if handler, ok := handlers[name]; ok {
			registry[name] = handler
		}
	}
	return registry
}

func resolveRandomMemeConfig() (string, float64, time.Duration) {
	dir := strings.TrimSpace(os.Getenv("LH_TG_MEME_DIR"))
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			for _, candidate := range []string{
				filepath.Join(wd, "assets", "memes"),
				filepath.Join(wd, "memes"),
			} {
				if st, err := os.Stat(candidate); err == nil && st.IsDir() {
					dir = candidate
					break
				}
			}
		}
	}

	probability := 0.30
	if raw := strings.TrimSpace(os.Getenv("LH_TG_MEME_PROBABILITY")); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v >= 0 && v <= 1 {
			probability = v
		}
	}

	cooldown := 5 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("LH_TG_MEME_COOLDOWN_SECONDS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			cooldown = time.Duration(v) * time.Second
		}
	}

	return dir, probability, cooldown
}

func resolveChatStreamTimeout(state stateRuntime) time.Duration {
	timeout := defaultChatStreamTimeout
	if state == nil {
		return timeout
	}
	cfg := state.Config().Get()
	if cfg.ChatTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.ChatTimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = defaultChatStreamTimeout
	}
	return timeout
}

func resolveCronEngine(state stateRuntime) *cron.Engine {
	if state != nil {
		if engine := state.CronEngine(); engine != nil {
			return engine
		}
	}
	return cron.NewEngine()
}

func resolveProgressAsMessages(state stateRuntime) bool {
	enabled := true
	if state == nil {
		return enabled
	}
	cfg := state.Config().Get()
	return cfg.ProgressAsMessages
}

func resolveProgressAsNaturalLanguage(state stateRuntime) bool {
	if state == nil {
		return false
	}
	cfg := state.Config().Get()
	return cfg.ProgressAsNaturalLanguage
}

func resolveProgressSummaryWithLLM(state stateRuntime) bool {
	if state == nil {
		return false
	}
	cfg := state.Config().Get()
	return cfg.ProgressSummaryWithLLM
}

func resolveShowToolDetailsInResult(state stateRuntime) bool {
	if state == nil {
		return false
	}
	cfg := state.Config().Get()
	return cfg.ShowToolDetailsInResult
}

func (h *Handler) sessionManager() *session.Manager {
	if h == nil {
		return nil
	}
	if h.state != nil {
		return h.state.Sessions()
	}
	if h.agent != nil {
		return h.agent.Sessions()
	}
	return nil
}

func (h *Handler) chatService() chatRuntime {
	if h == nil {
		return nil
	}
	if h.chat != nil {
		return h.chat
	}
	if h.agent != nil {
		return h.agent
	}
	return nil
}

func (h *Handler) stateService() stateRuntime {
	if h == nil {
		return nil
	}
	if h.state != nil {
		return h.state
	}
	if h.agent != nil {
		return h.agent
	}
	return nil
}

func (h *Handler) routeRecorder() activeRouteRecorder {
	if h == nil {
		return nil
	}
	if h.recorder != nil {
		return h.recorder
	}
	if h.agent != nil {
		if recorder, ok := any(h.agent).(activeRouteRecorder); ok {
			return recorder
		}
	}
	return nil
}

func (h *Handler) replyAnchorResolver() replyAnchorResolver {
	if h == nil {
		return nil
	}
	if h.agent != nil {
		if resolver, ok := h.agent.(replyAnchorResolver); ok {
			return resolver
		}
	}
	return nil
}

func (h *Handler) tools() *tool.Registry {
	state := h.stateService()
	if state == nil {
		return nil
	}
	return state.Tools()
}

func (h *Handler) metricsCollector() *metrics.Metrics {
	state := h.stateService()
	if state == nil {
		return nil
	}
	return state.Metrics()
}

func (h *Handler) memoryStore() *memory.Store {
	state := h.stateService()
	if state == nil {
		return nil
	}
	return state.Memory()
}

func (h *Handler) cronEngine() *cron.Engine {
	state := h.stateService()
	return resolveCronEngine(state)
}

func (h *Handler) watcherService() *cron.Watcher {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.watcher == nil {
		h.watcher = cron.NewWatcher(resolveCronEngine(h.stateService()))
	}
	return h.watcher
}

func (h *Handler) skillsList() []*tool.SkillInfo {
	state := h.stateService()
	if state == nil {
		return nil
	}
	return state.Skills()
}

func (h *Handler) configSnapshot() agentConfigSnapshot {
	state := h.stateService()
	if state == nil {
		return agentConfigSnapshot{}
	}
	return state.Config().Get()
}

func (h *Handler) effectiveChatStreamTimeout() time.Duration {
	if h.chatStreamTimeout > 0 {
		return h.chatStreamTimeout
	}
	return defaultChatStreamTimeout
}

func (h *Handler) effectiveProgressAsMessages() bool {
	return h.progressAsMessages
}

func (h *Handler) effectiveProgressAsNaturalLanguage() bool {
	return h.progressAsNaturalLanguage
}

func (h *Handler) effectiveProgressSummaryWithLLM() bool {
	return h.progressSummaryWithLLM
}

func (h *Handler) effectiveShowToolDetailsInResult() bool {
	return h.showToolDetailsInResult
}

func shouldPrependToolNarratives(showToolDetails, narrativeMode bool) bool {
	return showToolDetails && !narrativeMode
}

// SetDataDir 设置数据目录并从磁盘恢复 chatID→sessionID 映射
func (h *Handler) SetDataDir(dir string) {
	h.mu.Lock()
	h.dataDir = dir
	h.mu.Unlock()

	// 确保目录存在
	if err := os.MkdirAll(dir, 0700); err != nil {
		fmt.Printf("[telegram] warning: failed to create data dir %s: %v\n", dir, err)
		return
	}

	// 从磁盘恢复映射
	h.loadChatSessions()
}

// chatSessionsPath 返回持久化文件路径
func (h *Handler) chatSessionsPath() string {
	if h.dataDir == "" {
		return ""
	}
	return filepath.Join(h.dataDir, "chat_sessions.json")
}

// loadChatSessions 从磁盘加载 chatID→sessionID 映射
func (h *Handler) loadChatSessions() {
	path := h.chatSessionsPath()
	if path == "" {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// 文件不存在是正常的
		return
	}

	var csd chatSessionsData
	if err := json.Unmarshal(data, &csd); err != nil {
		fmt.Printf("[telegram] warning: failed to parse chat_sessions.json: %v\n", err)
		return
	}

	h.mu.Lock()
	for chatID, sessionID := range csd.ChatSessions {
		sessions := h.sessionManager()
		if sessions == nil {
			continue
		}
		if _, ok := sessions.Get(sessionID); ok {
			h.sessions[chatID] = sessionID
		}
	}
	h.mu.Unlock()

	fmt.Printf("[telegram] restored %d chat→session mappings from disk\n", len(h.sessions))
}

// saveChatSessions 持久化 chatID→sessionID 映射到磁盘
func (h *Handler) saveChatSessions() {
	path := h.chatSessionsPath()
	if path == "" {
		return
	}

	h.mu.RLock()
	csd := chatSessionsData{
		ChatSessions: make(map[string]string, len(h.sessions)),
	}
	for k, v := range h.sessions {
		csd.ChatSessions[k] = v
	}
	h.mu.RUnlock()

	data, err := json.MarshalIndent(csd, "", "  ")
	if err != nil {
		fmt.Printf("[telegram] warning: failed to marshal chat_sessions: %v\n", err)
		return
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		fmt.Printf("[telegram] warning: failed to write chat_sessions.json: %v\n", err)
	}
}

// getSessionID returns the session ID for a chat, creating one if needed.
func (h *Handler) getSessionID(chatID string) string {
	h.mu.RLock()
	if sid, ok := h.sessions[chatID]; ok {
		h.mu.RUnlock()
		return sid
	}
	h.mu.RUnlock()

	// Create new session via agent
	sessions := h.sessionManager()
	if sessions == nil {
		return ""
	}
	sess := sessions.New()
	h.mu.Lock()
	h.sessions[chatID] = sess.ID
	h.mu.Unlock()
	h.saveChatSessions()
	return sess.ID
}

// resetSession creates a new session for the chat, discarding the old one.
func (h *Handler) resetSession(chatID string) string {
	sessions := h.sessionManager()
	if sessions == nil {
		return ""
	}
	sess := sessions.New()
	h.mu.Lock()
	h.sessions[chatID] = sess.ID
	h.mu.Unlock()
	h.saveChatSessions()
	return sess.ID
}

// hasSession checks if a chat already has an assigned session.
func (h *Handler) hasSession(chatID string) bool {
	h.mu.RLock()
	_, ok := h.sessions[chatID]
	h.mu.RUnlock()
	return ok
}

// setSessionID directly sets the session ID for a chat (for testing).
func (h *Handler) setSessionID(chatID, sessionID string) {
	h.mu.Lock()
	h.sessions[chatID] = sessionID
	h.mu.Unlock()
}

func (h *Handler) currentSessionID(chatID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[chatID]
}

func (h *Handler) bindSessionID(chatID, sessionID string) {
	chatID = strings.TrimSpace(chatID)
	sessionID = strings.TrimSpace(sessionID)
	if chatID == "" || sessionID == "" {
		return
	}
	h.mu.Lock()
	h.sessions[chatID] = sessionID
	h.mu.Unlock()
	h.saveChatSessions()
}

func shortSessionID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func truncateForTelegramList(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func valueOrUnset(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(unset)"
	}
	return value
}

func maskSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "(unset)"
	}
	if len(secret) <= 8 {
		return "***"
	}
	return secret[:8] + "..."
}

type sessionLookupStatus int

const (
	sessionLookupNotFound sessionLookupStatus = iota
	sessionLookupMatched
	sessionLookupAmbiguous
)

type sessionLookupResult struct {
	status  sessionLookupStatus
	session *session.Session
	id      string
	matches []session.SessionInfo
}

func normalizeSessionLookupText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func findSessionByIDTitleOrPrefix(sessions *session.Manager, query string) sessionLookupResult {
	if sessions == nil {
		return sessionLookupResult{status: sessionLookupNotFound}
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return sessionLookupResult{status: sessionLookupNotFound}
	}
	if sess, ok := sessions.Get(query); ok {
		return sessionLookupResult{status: sessionLookupMatched, session: sess, id: query}
	}

	infos := sessions.ListInfo()
	if result := uniqueSessionMatch(sessions, infos, func(info session.SessionInfo) bool {
		return strings.HasPrefix(info.ID, query)
	}); result.status != sessionLookupNotFound {
		return result
	}

	normalizedQuery := normalizeSessionLookupText(query)
	if result := uniqueSessionMatch(sessions, infos, func(info session.SessionInfo) bool {
		return normalizeSessionLookupText(info.Title) == normalizedQuery
	}); result.status != sessionLookupNotFound {
		return result
	}
	if result := uniqueSessionMatch(sessions, infos, func(info session.SessionInfo) bool {
		title := normalizeSessionLookupText(info.Title)
		return title != "" && strings.HasPrefix(title, normalizedQuery)
	}); result.status != sessionLookupNotFound {
		return result
	}
	return uniqueSessionMatch(sessions, infos, func(info session.SessionInfo) bool {
		title := normalizeSessionLookupText(info.Title)
		return title != "" && strings.Contains(title, normalizedQuery)
	})
}

func uniqueSessionMatch(sessions *session.Manager, infos []session.SessionInfo, match func(session.SessionInfo) bool) sessionLookupResult {
	matches := make([]session.SessionInfo, 0, 2)
	for _, info := range infos {
		if match(info) {
			matches = append(matches, info)
		}
	}
	if len(matches) == 0 {
		return sessionLookupResult{status: sessionLookupNotFound}
	}
	if len(matches) > 1 {
		return sessionLookupResult{status: sessionLookupAmbiguous, matches: matches}
	}
	sess, ok := sessions.Get(matches[0].ID)
	if !ok {
		return sessionLookupResult{status: sessionLookupNotFound}
	}
	return sessionLookupResult{status: sessionLookupMatched, session: sess, id: matches[0].ID}
}

func formatAmbiguousSessionSwitchMessage(query string, matches []session.SessionInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Multiple sessions match `%s`:\n\n", query))
	limit := min(len(matches), 5)
	for i := 0; i < limit; i++ {
		title := strings.TrimSpace(matches[i].Title)
		if title == "" {
			title = "(untitled)"
		}
		sb.WriteString(fmt.Sprintf("• `%s` — %s (%d msgs)\n",
			matches[i].ID,
			truncateForTelegramList(title, 56),
			matches[i].MessageCount,
		))
	}
	if len(matches) > limit {
		sb.WriteString(fmt.Sprintf("\n... and %d more", len(matches)-limit))
	}
	sb.WriteString("\n\nUse `/resume <id>` or rename one session.")
	return sb.String()
}

func (h *Handler) dispatchChatAsync(ctx context.Context, msg *gateway.Message, input agent.UserTurnInput) error {
	msgCopy := *msg
	position, startWorker := h.enqueueChatRequest(msg.Chat.ID, &queuedChatRequest{
		ctx:   ctx,
		msg:   &msgCopy,
		input: input,
	})
	if startWorker {
		go h.runChatQueue(msg.Chat.ID)
	}
	if position > 1 {
		h.notifyQueued(msg.Chat.ID, msg.ID, position-1)
	}
	return nil
}

func (h *Handler) enqueueChatRequest(chatID string, req *queuedChatRequest) (position int, startWorker bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.queues == nil {
		h.queues = make(map[string]*chatQueue)
	}
	q := h.queues[chatID]
	if q == nil {
		q = &chatQueue{}
		h.queues[chatID] = q
	}

	position = len(q.items) + 1
	if h.tasks != nil && h.tasks[chatID] != nil {
		position++
	}
	q.items = append(q.items, req)
	if !q.running {
		q.running = true
		startWorker = true
	}
	return position, startWorker
}

func (h *Handler) dequeueChatRequest(chatID string) (*queuedChatRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	q := h.queues[chatID]
	if q == nil || len(q.items) == 0 {
		if q != nil {
			q.running = false
			delete(h.queues, chatID)
		}
		return nil, false
	}

	req := q.items[0]
	q.items = q.items[1:]
	return req, true
}

func (h *Handler) runChatQueue(chatID string) {
	for {
		req, ok := h.dequeueChatRequest(chatID)
		if !ok {
			return
		}
		if err := h.handleChat(req.ctx, req.msg, req.input); err != nil {
			fmt.Printf("[telegram] chat error: %v\n", err)
		}
	}
}

func (h *Handler) notifyQueued(chatID string, replyToMsgID string, ahead int) {
	if h.adapter == nil || ahead <= 0 {
		return
	}
	sendCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	text := fmt.Sprintf("⏳ 已加入消息队列，前面还有 %d 个任务", ahead)
	if strings.TrimSpace(replyToMsgID) != "" {
		_ = h.adapter.SendWithReply(sendCtx, chatID, replyToMsgID, text)
		return
	}
	_ = h.adapter.Send(sendCtx, chatID, text)
}

func (h *Handler) beginChatTask(chatID string, parent context.Context) (context.Context, *chatTask) {
	h.mu.Lock()
	if h.tasks == nil {
		h.tasks = make(map[string]*chatTask)
	}
	taskCtx, cancel := context.WithCancel(parent)
	task := &chatTask{cancel: cancel}
	h.tasks[chatID] = task
	h.mu.Unlock()
	return taskCtx, task
}

func (h *Handler) finishChatTask(chatID string, task *chatTask) {
	if task == nil {
		return
	}
	h.mu.Lock()
	if cur, ok := h.tasks[chatID]; ok && cur == task {
		delete(h.tasks, chatID)
	}
	h.mu.Unlock()
	task.cancel()
}

func (h *Handler) cancelChatTask(chatID string) bool {
	h.mu.Lock()
	if h.tasks == nil {
		h.mu.Unlock()
		return false
	}
	task, ok := h.tasks[chatID]
	if ok {
		delete(h.tasks, chatID)
	}
	h.mu.Unlock()
	if !ok || task == nil {
		return false
	}
	task.cancel()
	return true
}

func (h *Handler) queueStatus(chatID string) (running bool, queued int) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if task := h.tasks[chatID]; task != nil {
		running = true
	}
	if q := h.queues[chatID]; q != nil {
		queued = len(q.items)
	}
	return running, queued
}

func (h *Handler) luckyCollector() *luckycollector.Lucky {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lucky == nil {
		h.lucky = luckycollector.NewLucky()
	}
	return h.lucky
}

func (h *Handler) collectLuckyMessageIfActive(ctx context.Context, msg *gateway.Message) (bool, error) {
	if msg == nil {
		return false, nil
	}
	collector := h.luckyCollector()
	key := luckycollector.KeyForMessage("telegram", msg)
	status := collector.Status(key)
	if !status.Active {
		return false, nil
	}
	h.bindSessionFromReplyAnchor(msg)
	status, err := collector.Append(key, msg)
	if err != nil {
		return true, h.sendLuckyNotice(ctx, msg, fmt.Sprintf("Lucky collection failed: %s", err.Error()))
	}
	return true, h.sendLuckyNotice(ctx, msg, fmt.Sprintf("已收集第 %d 段（附件 %d 个）。发送 /lucky off 提交，/lucky cancel 放弃。", status.SegmentCount, status.AttachmentCount))
}

func (h *Handler) sendLuckyNotice(ctx context.Context, msg *gateway.Message, text string) error {
	if h == nil || h.adapter == nil || msg == nil {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if msg.Chat.Type != gateway.ChatPrivate && strings.TrimSpace(msg.ID) != "" {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, text)
	}
	return h.adapter.Send(ctx, msg.Chat.ID, text)
}

func isTaskCanceledError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled")
}

func isTaskTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context deadline exceeded")
}

// HandleMessage processes an incoming gateway message.
func (h *Handler) HandleMessage(ctx context.Context, msg *gateway.Message) error {
	if msg.IsCommand {
		return h.handleCommand(ctx, msg)
	}

	if collected, err := h.collectLuckyMessageIfActive(ctx, msg); collected || err != nil {
		return err
	}

	h.bindSessionFromReplyAnchor(msg)
	input := h.buildUserTurnInput(ctx, msg.Text, msg.Attachments)
	if msg.ReplyTo != nil {
		input = h.withReplyAnchorContext(input, msg)
	}

	// Regular text in private chats → forward to Agent
	if msg.Chat.Type == gateway.ChatPrivate {
		return h.dispatchChatAsync(ctx, msg, input)
	}

	// Group chats: only respond if mentioned or replied to (already filtered by adapter)
	return h.dispatchChatAsync(ctx, msg, input)
}

func (h *Handler) bindSessionFromReplyAnchor(msg *gateway.Message) bool {
	if h == nil || msg == nil || msg.ReplyTo == nil {
		return false
	}
	resolver := h.replyAnchorResolver()
	if resolver == nil {
		return false
	}
	sessionID, ok := resolver.ResolveExternalReplyAnchor("telegram", msg.Chat.ID, msg.ReplyTo.ID)
	if !ok {
		return false
	}
	if sessions := h.sessionManager(); sessions != nil {
		if _, exists := sessions.Get(sessionID); !exists {
			return false
		}
	}
	h.bindSessionID(msg.Chat.ID, sessionID)
	return true
}

func (h *Handler) buildUserTurnInput(ctx context.Context, baseText string, attachments []gateway.Attachment) agent.UserTurnInput {
	baseText = strings.TrimSpace(baseText)
	if len(attachments) == 0 {
		return agent.TextUserTurnInput(baseText)
	}

	return agent.TextUserTurnInput(h.composeAttachmentInput(ctx, baseText, attachments))
}

func (h *Handler) withReplyAnchorContext(input agent.UserTurnInput, msg *gateway.Message) agent.UserTurnInput {
	if msg == nil || msg.ReplyTo == nil {
		return input
	}
	repliedText := strings.TrimSpace(msg.ReplyTo.Text)
	if repliedText == "" {
		return input
	}

	input = input.Normalize()
	userRequest := strings.TrimSpace(input.RoutingText)
	if userRequest == "" {
		userRequest = "(no additional text)"
	}

	replyAwareText := fmt.Sprintf(`You are answering a Telegram reply to a previous LuckyHarness or cron message.

[Replied Telegram message]
%s

[User request]
%s

Use the replied message as the primary context for this turn. If the user asks "看看", "看一眼摘要", "摘要", "总结", or "summary", summarize the replied message directly. Do not consult unrelated runtime, cron, or session state unless the user explicitly asks for a fresh status check.`, repliedText, userRequest)

	input.Message.ContentParts = nil
	return input.WithRoutingText(replyAwareText)
}

func (h *Handler) composeAttachmentInput(ctx context.Context, baseText string, attachments []gateway.Attachment) string {
	var sections []string
	if strings.TrimSpace(baseText) != "" {
		sections = append(sections, strings.TrimSpace(baseText))
	}

	if chat := h.chatService(); chat != nil {
		analysis, err := chat.AnalyzeAttachments(ctx, attachments)
		if err == nil && strings.TrimSpace(analysis) != "" {
			sections = append(sections, analysis)
			return strings.Join(sections, "\n\n")
		}
	}

	var mediaDesc strings.Builder
	mediaDesc.WriteString("[Multimedia Attachments]\n")
	for i, att := range attachments {
		switch att.Type {
		case gateway.AttachmentImage:
			mediaDesc.WriteString(fmt.Sprintf("Image %d: %s (mime: %s, url: %s)\n", i+1, att.FileName, att.MimeType, att.FileURL))
		case gateway.AttachmentAudio:
			mediaDesc.WriteString(fmt.Sprintf("Audio %d: %s (mime: %s, url: %s)\n", i+1, att.FileName, att.MimeType, att.FileURL))
		case gateway.AttachmentVideo:
			mediaDesc.WriteString(fmt.Sprintf("Video %d: %s (mime: %s, url: %s)\n", i+1, att.FileName, att.MimeType, att.FileURL))
		case gateway.AttachmentDocument:
			mediaDesc.WriteString(fmt.Sprintf("Document %d: %s (mime: %s, url: %s)\n", i+1, att.FileName, att.MimeType, att.FileURL))
		}
	}
	sections = append(sections, strings.TrimSpace(mediaDesc.String()))
	return strings.Join(sections, "\n\n")
}

// handleCommand dispatches bot commands.
func (h *Handler) handleCommand(ctx context.Context, msg *gateway.Message) error {
	command := normalizeTelegramCommandName(msg.Command)
	if handler, ok := h.commands[command]; ok {
		msg.Command = command
		return handler(ctx, msg)
	}
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("Unknown command: /%s\nType /help for available commands.", msg.Command))
}

func normalizeTelegramCommandName(command string) string {
	command = strings.TrimSpace(command)
	command = strings.TrimPrefix(command, "/")
	command = strings.ToLower(command)
	switch command {
	case "msg-gateway":
		return "msg_gateway"
	case "remember-long":
		return "remember_long"
	case "learn-start":
		return "learn_start"
	case "learn-current":
		return "learn_current"
	case "learn-lab":
		return "learn_lab"
	case "learn-submit":
		return "learn_submit"
	case "learn-progress":
		return "learn_progress"
	default:
		return command
	}
}

func (h *Handler) handleLucky(ctx context.Context, msg *gateway.Message) error {
	action := luckycollector.ParseLuckyAction(msg.Args)
	collector := h.luckyCollector()
	key := luckycollector.KeyForMessage("telegram", msg)

	switch action {
	case luckycollector.LuckyActionOn:
		h.bindSessionFromReplyAnchor(msg)
		status, err := collector.Start(key)
		if errors.Is(err, luckycollector.ErrAlreadyActive) {
			return h.sendLuckyNotice(ctx, msg, fmt.Sprintf("Lucky 已经在收集中：当前 %d 段，附件 %d 个。发送 /lucky off 提交，/lucky cancel 放弃。", status.SegmentCount, status.AttachmentCount))
		}
		if err != nil {
			return h.sendLuckyNotice(ctx, msg, fmt.Sprintf("Lucky 开启失败：%s", err.Error()))
		}
		return h.sendLuckyNotice(ctx, msg, "Lucky 已开启。接下来发送的多段消息会先被收集；发送 /lucky off 后再统一交给 agent。")

	case luckycollector.LuckyActionOff:
		batch, err := collector.Finish(key)
		if errors.Is(err, luckycollector.ErrInactive) {
			return h.sendLuckyNotice(ctx, msg, "当前没有正在进行的 Lucky 收集。发送 /lucky on 开始。")
		}
		if errors.Is(err, luckycollector.ErrEmptyBatch) {
			return h.sendLuckyNotice(ctx, msg, "没有收集到消息，已退出 Lucky 收集模式。")
		}
		if err != nil {
			return h.sendLuckyNotice(ctx, msg, fmt.Sprintf("Lucky 提交失败：%s", err.Error()))
		}
		input := batch.UserTurnInput()
		finalMsg := *msg
		finalMsg.IsCommand = false
		finalMsg.Command = ""
		finalMsg.Args = ""
		finalMsg.Text = input.RoutingText
		finalMsg.Attachments = batch.Attachments()
		finalMsg.IsGroupTrigger = false
		return h.dispatchChatAsync(ctx, &finalMsg, input)

	case luckycollector.LuckyActionStatus:
		status := collector.Status(key)
		if !status.Active {
			return h.sendLuckyNotice(ctx, msg, "Lucky 未开启。发送 /lucky on 开始收集多段消息。")
		}
		return h.sendLuckyNotice(ctx, msg, fmt.Sprintf("Lucky 正在收集：%d 段，附件 %d 个。发送 /lucky off 提交，/lucky cancel 放弃。", status.SegmentCount, status.AttachmentCount))

	case luckycollector.LuckyActionCancel:
		status, ok := collector.Cancel(key)
		if !ok {
			return h.sendLuckyNotice(ctx, msg, "当前没有正在进行的 Lucky 收集。")
		}
		return h.sendLuckyNotice(ctx, msg, fmt.Sprintf("已取消 Lucky 收集，丢弃 %d 段消息和 %d 个附件。", status.SegmentCount, status.AttachmentCount))

	default:
		return h.sendLuckyNotice(ctx, msg, "用法：/lucky on | /lucky off | /lucky status | /lucky cancel")
	}
}

// handleStart sends a welcome message.
func (h *Handler) handleStart(ctx context.Context, msg *gateway.Message) error {
	return h.adapter.Send(ctx, msg.Chat.ID, telegramWelcomeMessage())
}

// handleHelp lists available commands.
func (h *Handler) handleHelp(ctx context.Context, msg *gateway.Message) error {
	return h.adapter.Send(ctx, msg.Chat.ID, telegramHelpMessage())
}

// handleChat sends a message to the agent and returns the response.
// Uses streaming output with thinking/tool-call visualization when available.
func (h *Handler) handleChat(ctx context.Context, msg *gateway.Message, input agent.UserTurnInput) error {
	input = input.Normalize()
	if recorder := h.routeRecorder(); recorder != nil {
		recorder.RecordRecentChatTarget("telegram", msg.Chat.ID, msg.ID)
	}
	if strings.TrimSpace(input.RoutingText) == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Please provide a message. Usage: /chat <message>")
	}

	taskCtx, task := h.beginChatTask(msg.Chat.ID, ctx)
	defer h.finishChatTask(msg.Chat.ID, task)

	// 收到消息后给用户点赞 👍
	go h.adapter.ReactToMessage(msg.Chat.ID, msg.ID, "👍")

	sessionID := h.getSessionID(msg.Chat.ID)

	// 群聊中在输入文本前加上发送者名字，让 agent 知道是谁在说话
	if msg.IsGroupTrigger && msg.Sender.DisplayName() != "" {
		input = input.WithRoutingText(fmt.Sprintf("[%s]: %s", msg.Sender.DisplayName(), input.RoutingText))
	}
	input = input.WithRoutingText(telegramMediaDeliveryGuidance(input.RoutingText))
	routingText := input.RoutingText

	if agent.IsSimpleLocalInspectionTask(routingText) {
		return h.handleChatSync(taskCtx, msg, input, sessionID)
	}

	// 自然语言进度模式：直接按步骤发独立消息，最终结论也作为“最后一条新消息”发送。
	// 这样可以避免结论写回到最早的占位流消息，导致视觉上跑到最上面。
	if h.effectiveProgressAsMessages() && h.effectiveProgressAsNaturalLanguage() {
		return h.handleChatNarrativeStream(taskCtx, msg, input, sessionID)
	}

	// 尝试流式输出（Adapter 已实现 StreamGateway）
	sender, err := h.adapter.SendStream(taskCtx, msg.Chat.ID, msg.ID)
	if err == nil {
		return h.handleChatStream(taskCtx, sender, msg, input, sessionID)
	}
	// SendStream 失败，回退到非流式

	// 回退到非流式
	return h.handleChatSync(taskCtx, msg, input, sessionID)
}

func (h *Handler) sendProgressMessage(msg *gateway.Message, text string) {
	text = strings.TrimSpace(text)
	if text == "" || h.adapter == nil || msg == nil {
		return
	}

	sendCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// 群聊里用 reply 方式，让中间步骤挂在原消息下，阅读更清晰。
	if msg.Chat.Type != gateway.ChatPrivate && strings.TrimSpace(msg.ID) != "" {
		_ = h.adapter.SendWithReply(sendCtx, msg.Chat.ID, msg.ID, text)
		return
	}
	_ = h.adapter.Send(sendCtx, msg.Chat.ID, text)
}

func (h *Handler) sendProgressMessageHTML(msg *gateway.Message, text string) {
	text = strings.TrimSpace(text)
	if text == "" || h.adapter == nil || msg == nil {
		return
	}

	sendCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	if msg.Chat.Type != gateway.ChatPrivate && strings.TrimSpace(msg.ID) != "" {
		_ = h.adapter.SendWithReplyHTML(sendCtx, msg.Chat.ID, msg.ID, text)
		return
	}
	_ = h.adapter.SendHTML(sendCtx, msg.Chat.ID, text)
}

func (h *Handler) newProgressCardUpdater(msg *gateway.Message) func(string) {
	if h == nil || h.adapter == nil || msg == nil {
		return func(text string) {
			h.sendProgressMessage(msg, text)
		}
	}

	var sender gateway.StreamSender
	var stream *telegramStreamSender
	var parts []string
	fallbackSent := false

	return func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		section := normalizeTelegramProgressSection(text)
		if section == "" {
			return
		}
		parts = appendTelegramProgressSection(parts, section)
		combined := renderTelegramProgressHistoryCard(parts)
		if strings.TrimSpace(combined) == "" {
			return
		}
		if stream == nil && sender == nil && !fallbackSent {
			sendCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()

			s, err := h.adapter.SendStream(sendCtx, msg.Chat.ID, msg.ID)
			if err == nil {
				sender = s
				if ts, ok := s.(*telegramStreamSender); ok {
					stream = ts
				}
			}
		}
		if stream != nil {
			_ = stream.SetHTMLCard(combined)
			return
		}
		if sender != nil {
			_ = sender.SetResult(combined)
			_ = sender.Finish()
			return
		}
		if !fallbackSent {
			h.sendProgressMessage(msg, combined)
			fallbackSent = true
		}
	}
}

func appendTelegramProgressSection(parts []string, text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return parts
	}
	if len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == text {
		return parts
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == text {
			return parts
		}
	}
	return append(parts, text)
}

func normalizeTelegramProgressSection(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	const openTag = "<blockquote expandable>"
	const closeTag = "</blockquote>"
	if start := strings.Index(text, openTag); start >= 0 {
		start += len(openTag)
		if end := strings.LastIndex(text, closeTag); end > start {
			text = text[start:end]
		}
	}
	text = strings.ReplaceAll(text, "<br>", "\n")
	text = strings.ReplaceAll(text, "<br/>", "\n")
	text = strings.ReplaceAll(text, "<br />", "\n")
	text = strings.TrimSpace(text)
	return strings.TrimSpace(html.UnescapeString(text))
}

func extractRoundNumber(thinking string) int {
	var round int
	if _, err := fmt.Sscanf(strings.TrimSpace(thinking), "Thinking... (round %d)", &round); err == nil && round > 0 {
		return round
	}
	return 0
}

func (h *Handler) generateRoundProgressFeedback(ctx context.Context, msg *gateway.Message, userInput string, round int, observations []string, progressHistory []string, lastProgress string) string {
	chat := h.chatService()
	if len(observations) == 0 || chat == nil {
		return ""
	}
	summaryCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	progressObservations := append([]string(nil), observations...)
	for _, prev := range progressHistory {
		prev = strings.TrimSpace(prev)
		if prev == "" {
			continue
		}
		progressObservations = append([]string{"Previous user-facing update: " + prev}, progressObservations...)
	}

	summary, err := chat.ProgressFeedback(summaryCtx, userInput, round, progressObservations)
	if err != nil {
		return ""
	}
	summary = strings.TrimSpace(summary)
	if summary == "" || summary == lastProgress {
		return ""
	}
	return smoothProgressSummary(summary, round, progressHistory)
}

func (h *Handler) flushRoundProgress(ctx context.Context, msg *gateway.Message, userInput string, round int, observations []string, progressHistory *[]string, lastProgress *string) {
	h.flushRoundProgressWithEmitter(ctx, msg, userInput, round, observations, progressHistory, lastProgress, h.sendProgressMessage)
}

func (h *Handler) flushRoundProgressWithEmitter(ctx context.Context, msg *gateway.Message, userInput string, round int, observations []string, progressHistory *[]string, lastProgress *string, emit func(*gateway.Message, string)) {
	if !h.effectiveProgressSummaryWithLLM() {
		return
	}
	var history []string
	if progressHistory != nil {
		history = append(history, (*progressHistory)...)
	}
	progress := h.generateRoundProgressFeedback(ctx, msg, userInput, round, observations, history, strings.TrimSpace(*lastProgress))
	if progress == "" {
		return
	}
	if emit == nil {
		emit = h.sendProgressMessage
	}
	emit(msg, formatTelegramProgressSummary(progress))
	*lastProgress = progress
	appendProgressHistory(progressHistory, progress)
}

func formatTelegramProgressSummary(progress string) string {
	card := renderTelegramSummaryCard(progress)
	if strings.TrimSpace(card) == "" {
		return progress
	}
	return card
}

func appendProgressHistory(history *[]string, progress string) {
	if history == nil {
		return
	}
	progress = strings.TrimSpace(progress)
	if progress == "" {
		return
	}
	entries := append([]string(nil), (*history)...)
	if len(entries) > 0 && strings.TrimSpace(entries[len(entries)-1]) == progress {
		*history = entries
		return
	}
	entries = append(entries, progress)
	if len(entries) > 3 {
		entries = entries[len(entries)-3:]
	}
	*history = entries
}

func smoothProgressSummary(summary string, round int, history []string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" || len(history) == 0 {
		return summary
	}
	lower := strings.ToLower(summary)
	switch {
	case strings.HasPrefix(lower, "i've "),
		strings.HasPrefix(lower, "i’m "),
		strings.HasPrefix(lower, "i'm "),
		strings.HasPrefix(lower, "i have "):
		return continuityCue(round) + summary
	case strings.HasPrefix(lower, "i've started"),
		strings.HasPrefix(lower, "i started"),
		strings.HasPrefix(lower, "i’m still"),
		strings.HasPrefix(lower, "i'm still"):
		return continuityCue(round) + summary
	default:
		return summary
	}
}

func continuityCue(round int) string {
	switch round % 4 {
	case 0:
		return "That suggests "
	case 1:
		return "So far, "
	case 2:
		return "At this point, "
	default:
		return "The latest result shows "
	}
}

func (h *Handler) sendAssistantResponse(ctx context.Context, msg *gateway.Message, response string) error {
	if h.adapter == nil || msg == nil {
		return fmt.Errorf("telegram: adapter or message is nil")
	}

	text, media, err := resolveOutboundMediaResponse(response)
	if err != nil {
		return err
	}
	if len(media) == 0 {
		if msg.Chat.Type != gateway.ChatPrivate && strings.TrimSpace(msg.ID) != "" {
			if err := h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, response); err != nil {
				return err
			}
		} else if err := h.adapter.Send(ctx, msg.Chat.ID, response); err != nil {
			return err
		}
		return h.sendRandomMemeIfNeeded(ctx, msg, response)
	}

	replyToMsgID := ""
	if msg.Chat.Type != gateway.ChatPrivate && strings.TrimSpace(msg.ID) != "" {
		replyToMsgID = msg.ID
	}

	if strings.TrimSpace(text) != "" {
		if replyToMsgID != "" {
			if err := h.adapter.SendWithReply(ctx, msg.Chat.ID, replyToMsgID, text); err != nil {
				return err
			}
		} else if err := h.adapter.Send(ctx, msg.Chat.ID, text); err != nil {
			return err
		}
	}

	if err := h.sendAssistantMedia(ctx, msg, media); err != nil {
		return err
	}
	return h.sendRandomMemeIfNeeded(ctx, msg, text)
}

func (h *Handler) sendAssistantMedia(ctx context.Context, msg *gateway.Message, media []outboundMedia) error {
	if h.adapter == nil || msg == nil || len(media) == 0 {
		return nil
	}

	replyToMsgID := ""
	if msg.Chat.Type != gateway.ChatPrivate && strings.TrimSpace(msg.ID) != "" {
		replyToMsgID = msg.ID
	}

	for _, item := range media {
		switch item.Kind {
		case outboundMediaPhoto:
			if err := h.adapter.SendPhoto(ctx, msg.Chat.ID, replyToMsgID, item.Source, item.Caption); err != nil {
				return err
			}
		case outboundMediaDocument:
			if err := h.adapter.SendDocument(ctx, msg.Chat.ID, replyToMsgID, item.Source, item.Caption); err != nil {
				return err
			}
		}
	}

	return nil
}

func (h *Handler) sendRandomMemeIfNeeded(ctx context.Context, msg *gateway.Message, text string) error {
	if !h.shouldSendRandomMeme(msg, text) {
		return nil
	}
	memePath, ok := h.pickRandomMemePath()
	if !ok {
		return nil
	}
	if err := h.adapter.SendPhoto(ctx, msg.Chat.ID, "", memePath, ""); err != nil {
		return err
	}
	h.recordMemeSent(msg.Chat.ID)
	return nil
}

func (h *Handler) shouldSendRandomMeme(msg *gateway.Message, text string) bool {
	if h == nil || h.adapter == nil || msg == nil {
		return false
	}
	if strings.TrimSpace(h.memeDir) == "" || h.memeProbability <= 0 || h.memeRand == nil || h.memeNow == nil {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 400 || strings.Contains(text, "```") {
		return false
	}

	lower := strings.ToLower(text)
	for _, blocked := range []string{"❌", "error:", "请求超时", "failed", "traceback", "panic:"} {
		if strings.Contains(lower, strings.ToLower(blocked)) {
			return false
		}
	}

	for _, blocked := range []string{"```", "panic:", "traceback", "stack trace", "unexpected status code"} {
		if strings.Contains(lower, blocked) {
			return false
		}
	}

	// Long, dense technical replies are usually a bad place to inject a meme.
	if strings.Count(text, "\n") > 12 {
		return false
	}

	now := h.memeNow()
	h.mu.RLock()
	lastSent := h.memeLastSent[msg.Chat.ID]
	h.mu.RUnlock()
	if !lastSent.IsZero() && now.Sub(lastSent) < h.memeCooldown {
		return false
	}

	probability := h.memeProbability
	for _, signal := range []string{"搞定", "完成", "哈哈", "好耶", "ok", "nice", "done", "已处理", "已完成", "可以", "当然", "没问题", "行", "收到"} {
		if strings.Contains(lower, strings.ToLower(signal)) {
			probability += 0.20
			break
		}
	}
	if probability > 0.90 {
		probability = 0.90
	}
	return h.memeRand.Float64() < probability
}

func (h *Handler) pickRandomMemePath() (string, bool) {
	entries, err := os.ReadDir(h.memeDir)
	if err != nil {
		return "", false
	}
	candidates := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !slices.Contains([]string{".png", ".jpg", ".jpeg", ".webp", ".gif"}, ext) {
			continue
		}
		candidates = append(candidates, filepath.Join(h.memeDir, entry.Name()))
	}
	if len(candidates) == 0 {
		return "", false
	}
	return candidates[h.memeRand.Intn(len(candidates))], true
}

func (h *Handler) recordMemeSent(chatID string) {
	if h == nil || h.memeNow == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.memeLastSent[chatID] = h.memeNow()
}

func (h *Handler) sendFinalAssistantResponse(msg *gateway.Message, response string) {
	if msg == nil || h.adapter == nil {
		return
	}

	sendCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := h.sendAssistantResponse(sendCtx, msg, response); err != nil {
		fallback := fmt.Sprintf("❌ Failed to send media response: %s", utils.TruncateKeepLength(err.Error(), 200))
		if msg.Chat.Type != gateway.ChatPrivate && strings.TrimSpace(msg.ID) != "" {
			_ = h.adapter.SendWithReply(sendCtx, msg.Chat.ID, msg.ID, fallback)
			return
		}
		_ = h.adapter.Send(sendCtx, msg.Chat.ID, fallback)
	}
}

func (h *Handler) openChatEventStream(ctx context.Context, chatID string, input agent.UserTurnInput, sessionID string) (<-chan agent.ChatEvent, error) {
	chat := h.chatService()
	if chat == nil {
		return nil, fmt.Errorf("chat runtime not available")
	}
	events, err := chat.ChatWithSessionStreamInput(ctx, sessionID, input)
	if err == nil {
		return events, nil
	}
	if !strings.Contains(err.Error(), "session not found") {
		return nil, err
	}

	h.resetSession(chatID)
	retrySessionID := h.getSessionID(chatID)
	return chat.ChatWithSessionStreamInput(ctx, retrySessionID, input)
}

func runChatEventLoop(
	chatCtx context.Context,
	events <-chan agent.ChatEvent,
	onTimeout func(err error) (sentResult bool),
	onEvent func(evt agent.ChatEvent) (stop bool, sentResult bool),
) (sentResult bool) {
	for {
		select {
		case <-chatCtx.Done():
			if onTimeout == nil {
				return false
			}
			return onTimeout(chatCtx.Err())

		case evt, ok := <-events:
			if !ok {
				return sentResult
			}
			stop, marked := onEvent(evt)
			if marked {
				sentResult = true
			}
			if stop {
				return sentResult
			}
		}
	}
}

// handleChatNarrativeStream 自然语言进度模式（不使用流式占位消息）。
// 中间步骤和最终结论都作为独立消息发送，保证“结论在最后”。
func (h *Handler) handleChatNarrativeStream(ctx context.Context, msg *gateway.Message, input agent.UserTurnInput, sessionID string) error {
	routingText := input.RoutingText
	emitProgress := h.newProgressCardUpdater(msg)
	emitProgressForMsg := func(_ *gateway.Message, text string) {
		emitProgress(text)
	}
	// 启动 typing indicator（每 5 秒刷新一次，直到完成）
	typingCtx, typingCancel := context.WithCancel(context.Background())
	defer typingCancel()
	go h.adapter.SendTypingLoop(typingCtx, msg.Chat.ID)

	chatCtx, chatCancel := context.WithTimeout(ctx, h.effectiveChatStreamTimeout())
	defer chatCancel()

	events, err := h.openChatEventStream(chatCtx, msg.Chat.ID, input, sessionID)
	if err != nil {
		switch {
		case isTaskTimeoutError(err):
			emitProgress("⏱ 请求超时")
		case isTaskCanceledError(err):
			emitProgress("🛑 当前任务已停止")
		default:
			emitProgress(fmt.Sprintf("❌ Error: %s", utils.TruncateKeepLength(err.Error(), 200)))
		}
		return nil
	}

	var finalContent strings.Builder
	toolCallCount := 0
	lastProgress := ""
	var toolNarratives []string
	var toolTraceSteps []telegramToolTraceStep
	toolTraceSent := false
	agentTraceSent := false
	currentRound := 1
	var roundObservations []string
	var progressHistory []string
	summaryMode := h.effectiveProgressSummaryWithLLM()

	sentResult := runChatEventLoop(
		chatCtx,
		events,
		func(err error) bool {
			if errors.Is(err, context.DeadlineExceeded) {
				emitProgress("⏱ 请求超时")
			} else {
				emitProgress("🛑 当前任务已停止")
			}
			return true
		},
		func(evt agent.ChatEvent) (bool, bool) {
			switch evt.Type {
			case agent.ChatEventThinking:
				if summaryMode {
					if nextRound := extractRoundNumber(evt.Content); nextRound > currentRound {
						if progress := h.generateRoundProgressFeedback(chatCtx, msg, routingText, currentRound, roundObservations, progressHistory, lastProgress); progress != "" {
							emitProgress(formatTelegramProgressSummary(progress))
							lastProgress = progress
							appendProgressHistory(&progressHistory, progress)
						}
						roundObservations = nil
						currentRound = nextRound
					}
				} else {
					progress := renderTelegramThinkingCard(evt.Content)
					if strings.TrimSpace(progress) == "" {
						progress = humanizeThinkingProgress(evt.Content)
					}
					if strings.TrimSpace(progress) != "" && progress != lastProgress {
						emitProgress(progress)
						lastProgress = progress
					}
				}

			case agent.ChatEventToolCall:
				toolCallCount++
				toolTraceSteps = append(toolTraceSteps, telegramToolTraceStep{
					Name: evt.Name,
					Args: evt.Args,
				})
				if summaryMode {
					if line := humanizeToolCall(evt.Name, evt.Args); line != "" {
						roundObservations = append(roundObservations, "Tool call: "+line)
					}
				}

			case agent.ChatEventToolResult:
				if len(toolTraceSteps) > 0 {
					for i := len(toolTraceSteps) - 1; i >= 0; i-- {
						if toolTraceSteps[i].Name == evt.Name && toolTraceSteps[i].Result == "" {
							toolTraceSteps[i].Result = evt.Result
							toolTraceSteps[i].Success = !strings.HasPrefix(strings.ToLower(strings.TrimSpace(evt.Result)), "error:")
							break
						}
					}
				}
				if shouldPrependToolNarratives(h.effectiveShowToolDetailsInResult(), true) {
					if line := humanizeToolResult(evt.Name, evt.Result); line != "" {
						toolNarratives = append(toolNarratives, line)
					}
				}

			case agent.ChatEventContent:
				finalContent.WriteString(evt.Content)

			case agent.ChatEventDone:
				if summaryMode {
					h.flushRoundProgressWithEmitter(chatCtx, msg, routingText, currentRound, roundObservations, &progressHistory, &lastProgress, emitProgressForMsg)
					roundObservations = nil
				}
				if !toolTraceSent {
					if card := renderTelegramToolTraceCard(toolTraceSteps); strings.TrimSpace(card) != "" {
						h.sendProgressMessageHTML(msg, card)
						toolTraceSent = true
					}
				}
				if !agentTraceSent {
					if card := renderTelegramAgentTraceCard(toolTraceSteps); strings.TrimSpace(card) != "" {
						h.sendProgressMessageHTML(msg, card)
						agentTraceSent = true
					}
				}
				if evt.Content != "" {
					finalContent.Reset()
					finalContent.WriteString(evt.Content)
				}
				finalOutput := strings.TrimSpace(finalContent.String())
				if shouldPrependToolNarratives(h.effectiveShowToolDetailsInResult(), true) && finalOutput != "" {
					finalOutput = prependToolNarratives(toolNarratives, finalOutput)
				}
				h.sendFinalAssistantResponse(msg, wrapFinalConclusion(finalOutput))
				return true, true

			case agent.ChatEventError:
				fmt.Printf("[telegram] chat event error: chatID=%s sessionID=%s err=%v\n", msg.Chat.ID, sessionID, evt.Err)
				if isTaskTimeoutError(evt.Err) {
					emitProgress("⏱ 请求超时")
				} else if isTaskCanceledError(evt.Err) {
					emitProgress("🛑 当前任务已停止")
				} else {
					errMsg := evt.Err.Error()
					if len(errMsg) > 200 {
						errMsg = errMsg[:197] + "..."
					}
					emitProgress(fmt.Sprintf("❌ Error: %s", errMsg))
				}
				return true, true
			}
			return false, false
		},
	)

	if !sentResult {
		finalOutput := strings.TrimSpace(finalContent.String())
		switch {
		case finalOutput != "":
			if summaryMode {
				h.flushRoundProgressWithEmitter(chatCtx, msg, routingText, currentRound, roundObservations, &progressHistory, &lastProgress, emitProgressForMsg)
			}
			if !toolTraceSent {
				if card := renderTelegramToolTraceCard(toolTraceSteps); strings.TrimSpace(card) != "" {
					h.sendProgressMessageHTML(msg, card)
					toolTraceSent = true
				}
			}
			if !agentTraceSent {
				if card := renderTelegramAgentTraceCard(toolTraceSteps); strings.TrimSpace(card) != "" {
					h.sendProgressMessageHTML(msg, card)
					agentTraceSent = true
				}
			}
			if shouldPrependToolNarratives(h.effectiveShowToolDetailsInResult(), true) {
				finalOutput = prependToolNarratives(toolNarratives, finalOutput)
			}
			h.sendFinalAssistantResponse(msg, wrapFinalConclusion(finalOutput))
		case errors.Is(chatCtx.Err(), context.DeadlineExceeded):
			emitProgress("⏱ 请求超时")
		case errors.Is(chatCtx.Err(), context.Canceled):
			emitProgress("🛑 当前任务已停止")
		default:
			emitProgress("❌ Error: stream ended unexpectedly, please retry")
		}
	}

	return nil
}

// handleChatStream 流式对话处理（Telegram 专用）
func (h *Handler) handleChatStream(ctx context.Context, sender gateway.StreamSender, msg *gateway.Message, input agent.UserTurnInput, sessionID string) error {
	routingText := input.RoutingText
	emitProgress := h.newProgressCardUpdater(msg)
	// 启动 typing indicator（每 5 秒刷新一次，直到完成）
	typingCtx, typingCancel := context.WithCancel(context.Background())
	defer typingCancel()
	go h.adapter.SendTypingLoop(typingCtx, msg.Chat.ID)

	chatCtx, chatCancel := context.WithTimeout(ctx, h.effectiveChatStreamTimeout())
	defer chatCancel()

	events, err := h.openChatEventStream(chatCtx, msg.Chat.ID, input, sessionID)
	if err != nil {
		if isTaskTimeoutError(err) {
			sender.SetResult("⏱ 请求超时")
			sender.Finish()
			return nil
		}
		if isTaskCanceledError(err) {
			sender.SetResult("🛑 当前任务已停止")
			sender.Finish()
			return nil
		}
		sender.SetResult(fmt.Sprintf("❌ Error: %s", utils.TruncateKeepLength(err.Error(), 200)))
		sender.Finish()
		return nil
	}

	var finalContent strings.Builder
	toolCallCount := 0
	lastProgress := ""
	var toolNarratives []string
	var toolTraceSteps []telegramToolTraceStep
	toolTraceSent := false
	agentTraceSent := false
	narrativeMode := h.effectiveProgressAsMessages() && h.effectiveProgressAsNaturalLanguage()
	summaryMode := narrativeMode && h.effectiveProgressSummaryWithLLM()
	currentRound := 1
	var roundObservations []string
	var progressHistory []string

	sentResult := runChatEventLoop(
		chatCtx,
		events,
		func(err error) bool {
			if errors.Is(err, context.DeadlineExceeded) {
				sender.SetResult("⏱ 请求超时")
			} else {
				sender.SetResult("🛑 当前任务已停止")
			}
			sender.Finish()
			return true
		},
		func(evt agent.ChatEvent) (bool, bool) {
			switch evt.Type {
			case agent.ChatEventThinking:
				if summaryMode {
					if nextRound := extractRoundNumber(evt.Content); nextRound > currentRound {
						if progress := h.generateRoundProgressFeedback(chatCtx, msg, routingText, currentRound, roundObservations, progressHistory, lastProgress); progress != "" {
							emitProgress(formatTelegramProgressSummary(progress))
							lastProgress = progress
							appendProgressHistory(&progressHistory, progress)
						}
						roundObservations = nil
						currentRound = nextRound
					}
				} else if h.effectiveProgressAsMessages() && toolCallCount == 0 && lastProgress == "" {
					progress := "🧠 " + clipOneLine(evt.Content, 180)
					if narrativeMode {
						progress = humanizeThinkingProgress(evt.Content)
					}
					if strings.TrimSpace(progress) != "" && progress != "🧠 " && progress != lastProgress {
						emitProgress(progress)
						lastProgress = progress
					}
				} else {
					// 兼容旧模式：在同一条消息里更新思考前缀
					sender.SetThinking(evt.Content)
				}

			case agent.ChatEventToolCall:
				toolCallCount++
				toolTraceSteps = append(toolTraceSteps, telegramToolTraceStep{
					Name: evt.Name,
					Args: evt.Args,
				})
				if summaryMode {
					if line := humanizeToolCall(evt.Name, evt.Args); line != "" && len(roundObservations) < 2 {
						roundObservations = append(roundObservations, "Tool call: "+line)
					}
				} else if h.effectiveProgressAsMessages() && toolCallCount == 1 {
					// 工具调用作为独立自然语言消息发送（Nanobot 风格）。
					progress := "🔧 " + humanizeToolCall(evt.Name, evt.Args)
					if narrativeMode {
						progress = humanizeToolCallProgress(1, evt.Name, evt.Args)
					}
					if strings.TrimSpace(progress) != "" && progress != lastProgress {
						emitProgress(progress)
						lastProgress = progress
					}
				} else {
					// 兼容旧模式：显示工具调用标签
					sender.SetToolCall(evt.Name, evt.Args)
				}

			case agent.ChatEventToolResult:
				if len(toolTraceSteps) > 0 {
					for i := len(toolTraceSteps) - 1; i >= 0; i-- {
						if toolTraceSteps[i].Name == evt.Name && toolTraceSteps[i].Result == "" {
							toolTraceSteps[i].Result = evt.Result
							toolTraceSteps[i].Success = !strings.HasPrefix(strings.ToLower(strings.TrimSpace(evt.Result)), "error:")
							break
						}
					}
				}
				if shouldPrependToolNarratives(h.effectiveShowToolDetailsInResult(), narrativeMode) {
					if line := humanizeToolResult(evt.Name, evt.Result); line != "" {
						toolNarratives = append(toolNarratives, line)
					}
				}
				if !summaryMode && !h.effectiveProgressAsMessages() {
					// 兼容旧模式：工具结果后切回思考态
					sender.SetThinking(fmt.Sprintf("Continuing... (%d tools used)", toolCallCount))
				}

			case agent.ChatEventContent:
				// 内容流式追加
				finalContent.WriteString(evt.Content)
				if !narrativeMode {
					sender.Append(evt.Content)
				}

			case agent.ChatEventDone:
				if summaryMode {
					h.flushRoundProgress(chatCtx, msg, routingText, currentRound, roundObservations, &progressHistory, &lastProgress)
					roundObservations = nil
				}
				if narrativeMode && !toolTraceSent {
					if card := renderTelegramToolTraceCard(toolTraceSteps); strings.TrimSpace(card) != "" {
						h.sendProgressMessageHTML(msg, card)
						toolTraceSent = true
					}
				}
				if narrativeMode && !agentTraceSent {
					if card := renderTelegramAgentTraceCard(toolTraceSteps); strings.TrimSpace(card) != "" {
						h.sendProgressMessageHTML(msg, card)
						agentTraceSent = true
					}
				}
				if evt.Content != "" {
					finalContent.Reset()
					finalContent.WriteString(evt.Content)
				}
				finalOutput := finalContent.String()
				if shouldPrependToolNarratives(h.effectiveShowToolDetailsInResult(), narrativeMode) {
					finalOutput = prependToolNarratives(toolNarratives, finalOutput)
				}
				if narrativeMode {
					finalOutput = wrapFinalConclusion(finalOutput)
				}
				textOnly, media, resolveErr := resolveOutboundMediaResponse(finalOutput)
				if resolveErr != nil {
					sender.SetResult(fmt.Sprintf("❌ Error: %s", utils.TruncateKeepLength(resolveErr.Error(), 200)))
					sender.Finish()
					return true, true
				}
				if len(media) > 0 {
					placeholder := textOnly
					if strings.TrimSpace(placeholder) == "" {
						placeholder = summarizeOutboundMedia(media)
					}
					sender.SetResult(placeholder)
					sender.Finish()
					if err := h.sendAssistantMedia(context.Background(), msg, media); err != nil {
						h.sendProgressMessage(msg, fmt.Sprintf("❌ Failed to send media response: %s", utils.TruncateKeepLength(err.Error(), 200)))
					}
					return true, true
				}
				// 最终结果替换整个消息
				sender.SetResult(finalOutput)
				sender.Finish()
				return true, true

			case agent.ChatEventError:
				fmt.Printf("[telegram] chat event error: chatID=%s sessionID=%s err=%v\n", msg.Chat.ID, sessionID, evt.Err)
				if isTaskTimeoutError(evt.Err) {
					sender.SetResult("⏱ 请求超时")
					sender.Finish()
					return true, true
				}
				if isTaskCanceledError(evt.Err) {
					sender.SetResult("🛑 当前任务已停止")
					sender.Finish()
					return true, true
				}
				errMsg := evt.Err.Error()
				if len(errMsg) > 200 {
					errMsg = errMsg[:197] + "..."
				}
				sender.SetResult(fmt.Sprintf("❌ Error: %s", errMsg))
				sender.Finish()
				return true, true
			}
			return false, false
		},
	)

	if !sentResult {
		finalOutput := finalContent.String()
		if summaryMode && finalOutput != "" {
			h.flushRoundProgress(chatCtx, msg, routingText, currentRound, roundObservations, &progressHistory, &lastProgress)
		}
		if narrativeMode && !toolTraceSent && finalOutput != "" {
			if card := renderTelegramToolTraceCard(toolTraceSteps); strings.TrimSpace(card) != "" {
				h.sendProgressMessageHTML(msg, card)
				toolTraceSent = true
			}
		}
		if narrativeMode && !agentTraceSent && finalOutput != "" {
			if card := renderTelegramAgentTraceCard(toolTraceSteps); strings.TrimSpace(card) != "" {
				h.sendProgressMessageHTML(msg, card)
				agentTraceSent = true
			}
		}
		if shouldPrependToolNarratives(h.effectiveShowToolDetailsInResult(), narrativeMode) && finalOutput != "" {
			finalOutput = prependToolNarratives(toolNarratives, finalOutput)
		}
		if narrativeMode && finalOutput != "" {
			finalOutput = wrapFinalConclusion(finalOutput)
		}
		switch {
		case finalContent.Len() > 0:
			textOnly, media, resolveErr := resolveOutboundMediaResponse(finalOutput)
			if resolveErr != nil {
				sender.SetResult(fmt.Sprintf("❌ Error: %s", utils.TruncateKeepLength(resolveErr.Error(), 200)))
			} else if len(media) > 0 {
				placeholder := textOnly
				if strings.TrimSpace(placeholder) == "" {
					placeholder = summarizeOutboundMedia(media)
				}
				sender.SetResult(placeholder)
				if err := h.sendAssistantMedia(context.Background(), msg, media); err != nil {
					h.sendProgressMessage(msg, fmt.Sprintf("❌ Failed to send media response: %s", utils.TruncateKeepLength(err.Error(), 200)))
				}
			} else {
				sender.SetResult(finalOutput)
			}
		case errors.Is(chatCtx.Err(), context.DeadlineExceeded):
			sender.SetResult("⏱ 请求超时")
		case errors.Is(chatCtx.Err(), context.Canceled):
			sender.SetResult("🛑 当前任务已停止")
		default:
			sender.SetResult("❌ Error: stream ended unexpectedly, please retry")
		}
		sender.Finish()
	}

	return nil
}

func prependToolNarratives(lines []string, finalOutput string) string {
	if len(lines) == 0 {
		return finalOutput
	}
	seen := make(map[string]struct{}, len(lines))
	summaryLines := make([]string, 0, min(8, len(lines)))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		summaryLines = append(summaryLines, line)
		if len(summaryLines) >= 8 {
			break
		}
	}
	if len(summaryLines) == 0 {
		return finalOutput
	}

	var b strings.Builder
	b.WriteString("过程摘要：\n||")
	b.WriteString("我刚刚先做了这些事：\n")
	for _, line := range summaryLines {
		b.WriteString("1. ")
		b.WriteString(clipOneLine(line, 120))
		b.WriteString("\n")
	}
	if len(lines) > len(summaryLines) {
		b.WriteString("1. 还有更多中间步骤，已省略\n")
	}
	b.WriteString("||")
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(finalOutput))
	return b.String()
}

// handleChatSync 非流式对话处理（回退方案）
func (h *Handler) handleChatSync(ctx context.Context, msg *gateway.Message, input agent.UserTurnInput, sessionID string) error {
	if recorder := h.routeRecorder(); recorder != nil {
		recorder.RecordRecentChatTarget("telegram", msg.Chat.ID, msg.ID)
	}
	// 启动 typing indicator
	typingCtx, typingCancel := context.WithCancel(context.Background())
	defer typingCancel()
	go h.adapter.SendTypingLoop(typingCtx, msg.Chat.ID)

	chat := h.chatService()
	if chat == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Chat runtime unavailable")
	}
	response, err := chat.ChatWithSessionInput(ctx, sessionID, input)
	if err != nil {
		// If session is broken, try with a fresh session
		if strings.Contains(err.Error(), "session not found") {
			h.resetSession(msg.Chat.ID)
			sessionID = h.getSessionID(msg.Chat.ID)
			response, err = chat.ChatWithSessionInput(ctx, sessionID, input)
		}
		if err != nil {
			if isTaskTimeoutError(err) {
				return h.adapter.Send(ctx, msg.Chat.ID, "⏱ 请求超时")
			}
			if isTaskCanceledError(err) {
				return h.adapter.Send(context.Background(), msg.Chat.ID, "🛑 当前任务已停止")
			}
			errMsg := fmt.Sprintf("❌ Error: %s", err.Error())
			if len(errMsg) > 200 {
				errMsg = errMsg[:200] + "..."
			}
			return h.adapter.Send(ctx, msg.Chat.ID, errMsg)
		}
	}

	return h.sendAssistantResponse(ctx, msg, response)
}

// handleModel shows or sets the current model.
func (h *Handler) handleModel(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	if msg.Args == "" {
		cfg := h.configSnapshot()
		routerNote := ""
		if cfg.ModelRouterEnabled {
			routerNote = "\nModel router is enabled; future turns may still route by task."
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("Current configured model: %s (provider: %s)%s", cfg.Model, cfg.Provider, routerNote))
	}

	modelID := strings.TrimSpace(msg.Args)
	if state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Model switching unavailable")
	}
	providerName := ""
	if catalog := state.Catalog(); catalog != nil {
		if resolved, err := catalog.ResolveProvider(modelID); err == nil {
			providerName = resolved
		}
	}
	if err := state.SwitchModel(modelID); err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Failed to switch model: %s", err.Error()))
	}

	cfgProvider := state.Config()
	if mutable, ok := cfgProvider.(mutableAgentConfigProvider); ok {
		if providerName != "" {
			if err := mutable.Set("provider", providerName); err != nil {
				return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("⚠️ Switched runtime model to `%s`, but failed to update provider config: %s", modelID, err.Error()))
			}
		}
		if err := mutable.Set("model", modelID); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("⚠️ Switched runtime model to `%s`, but failed to update config: %s", modelID, err.Error()))
		}
		if err := mutable.Save(); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("⚠️ Switched runtime model to `%s`, but failed to save config: %s", modelID, err.Error()))
		}
		cfg := mutable.Get()
		routerNote := ""
		if cfg.ModelRouterEnabled {
			routerNote = "\nModel router is enabled; future turns may still route by task."
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Switched model: `%s`\nSaved to config.%s", modelID, routerNote))
	}

	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Switched runtime model: `%s`\nThis config provider does not support saving, so restart may restore the configured model.", modelID))
}

func (h *Handler) handleReview(ctx context.Context, msg *gateway.Message) error {
	var sb strings.Builder
	sb.WriteString("🔎 *Workspace Review:*\n\n")
	if wd, err := os.Getwd(); err == nil {
		sb.WriteString(fmt.Sprintf("• Workspace: `%s`\n", wd))
	}
	cfg := h.configSnapshot()
	sb.WriteString(fmt.Sprintf("• Provider: %s\n", valueOrUnset(cfg.Provider)))
	sb.WriteString(fmt.Sprintf("• Model: %s\n", valueOrUnset(cfg.Model)))

	sb.WriteString("\n*Git Status:*\n")
	status := readGitSnippet("status", "--short", "--branch")
	if status == "" {
		sb.WriteString("clean or unavailable\n")
	} else {
		sb.WriteString(status)
		sb.WriteString("\n")
	}

	sb.WriteString("\n*Recent Commits:*\n")
	log := readGitSnippet("log", "--oneline", "--decorate=short", "-n", "5")
	if log == "" {
		sb.WriteString("unavailable\n")
	} else {
		sb.WriteString(log)
		sb.WriteString("\n")
	}

	if sessions := h.sessionManager(); sessions != nil {
		infos := sessions.ListInfo()
		sb.WriteString(fmt.Sprintf("\n*Recent Sessions:* %d\n", len(infos)))
		limit := min(len(infos), 5)
		for i := 0; i < limit; i++ {
			title := strings.TrimSpace(infos[i].Title)
			if title == "" {
				title = "(untitled)"
			}
			sb.WriteString(fmt.Sprintf("• %s (%d msgs)\n", truncateForTelegramList(title, 56), infos[i].MessageCount))
		}
	}

	return h.adapter.Send(ctx, msg.Chat.ID, strings.TrimSpace(sb.String()))
}

func readGitSnippet(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	limit := min(len(lines), 12)
	return strings.Join(lines[:limit], "\n")
}

func (h *Handler) handleInit(ctx context.Context, msg *gateway.Message) error {
	cfg := h.configSnapshot()
	var sb strings.Builder
	sb.WriteString("🍀 *LuckyHarness Init Status:*\n\n")
	sb.WriteString(fmt.Sprintf("• Home: `%s`\n", valueOrUnset(cfg.HomeDir)))
	sb.WriteString(fmt.Sprintf("• Config: `%s`\n", valueOrUnset(cfg.ConfigFile)))
	sb.WriteString(fmt.Sprintf("• Provider: %s\n", valueOrUnset(cfg.Provider)))
	sb.WriteString(fmt.Sprintf("• Model: %s\n", valueOrUnset(cfg.Model)))
	if cfg.APIKey == "" {
		sb.WriteString("\nAPI key is not configured. Run `lh config set api_key <key>` on the host.")
	} else {
		sb.WriteString(fmt.Sprintf("\nAPI key: %s", maskSecret(cfg.APIKey)))
	}
	return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
}

func (h *Handler) handleConfig(ctx context.Context, msg *gateway.Message) error {
	args := strings.Fields(strings.TrimSpace(msg.Args))
	if len(args) == 0 || args[0] == "list" {
		return h.sendConfigList(ctx, msg)
	}
	if args[0] == "get" {
		if len(args) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /config get <key>")
		}
		value, ok := configSnapshotValue(h.configSnapshot(), args[1])
		if !ok {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("`%s` is not exposed in Telegram config view.", args[1]))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("%s = %s", args[1], value))
	}
	if len(args) == 1 {
		value, ok := configSnapshotValue(h.configSnapshot(), args[0])
		if ok {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("%s = %s", args[0], value))
		}
	}
	if args[0] == "set" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Config writes from Telegram are disabled. Run `lh config set <key> <value>` on the host.")
	}
	return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /config [list|get <key>]")
}

func (h *Handler) sendConfigList(ctx context.Context, msg *gateway.Message) error {
	cfg := h.configSnapshot()
	info := fmt.Sprintf("⚙️ *Configuration:*\n\n• provider: %s\n• api_key: %s\n• api_base: %s\n• model: %s\n• soul_path: `%s`\n• max_tokens: %d\n• temperature: %.1f\n• server.addr: %s\n• dashboard.addr: %s\n• msg_gateway.platform: %s\n• msg_gateway.api_addr: %s\n• msg_gateway.telegram.token: %s",
		valueOrUnset(cfg.Provider),
		maskSecret(cfg.APIKey),
		valueOrUnset(cfg.APIBase),
		valueOrUnset(cfg.Model),
		valueOrUnset(cfg.SoulPath),
		cfg.MaxTokens,
		cfg.Temperature,
		valueOrUnset(cfg.ServerAddr),
		valueOrUnset(cfg.DashboardAddr),
		valueOrUnset(cfg.MsgGatewayPlatform),
		valueOrUnset(cfg.MsgGatewayAPIAddr),
		maskSecret(cfg.MsgGatewayTelegramToken),
	)
	return h.adapter.Send(ctx, msg.Chat.ID, info)
}

func configSnapshotValue(cfg agentConfigSnapshot, key string) (string, bool) {
	switch key {
	case "home", "home_dir":
		return valueOrUnset(cfg.HomeDir), true
	case "config", "config_file":
		return valueOrUnset(cfg.ConfigFile), true
	case "provider":
		return valueOrUnset(cfg.Provider), true
	case "api_key":
		return maskSecret(cfg.APIKey), true
	case "api_base":
		return valueOrUnset(cfg.APIBase), true
	case "model":
		return valueOrUnset(cfg.Model), true
	case "soul_path":
		return valueOrUnset(cfg.SoulPath), true
	case "max_tokens":
		return strconv.Itoa(cfg.MaxTokens), true
	case "temperature":
		return fmt.Sprintf("%.1f", cfg.Temperature), true
	case "server.addr":
		return valueOrUnset(cfg.ServerAddr), true
	case "dashboard.addr":
		return valueOrUnset(cfg.DashboardAddr), true
	case "msg_gateway.platform":
		return valueOrUnset(cfg.MsgGatewayPlatform), true
	case "msg_gateway.api_addr":
		return valueOrUnset(cfg.MsgGatewayAPIAddr), true
	case "msg_gateway.telegram.token":
		return maskSecret(cfg.MsgGatewayTelegramToken), true
	case "msg_gateway.telegram.proxy":
		return valueOrUnset(cfg.MsgGatewayTelegramProxy), true
	default:
		return "", false
	}
}

func (h *Handler) handleVersion(ctx context.Context, msg *gateway.Message) error {
	version, commit, date := buildInfo()
	info := fmt.Sprintf("🍀 *LuckyHarness Version:*\n\n• Version: %s\n• Commit: %s\n• Date: %s\n• Go: %s\n• OS/Arch: %s/%s",
		version,
		commit,
		date,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	)
	return h.adapter.Send(ctx, msg.Chat.ID, info)
}

func buildInfo() (version, commit, date string) {
	version = "dev"
	commit = "unknown"
	date = "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		if strings.TrimSpace(info.Main.Version) != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if setting.Value != "" {
					commit = setting.Value
				}
			case "vcs.time":
				if setting.Value != "" {
					date = setting.Value
				}
			}
		}
	}
	return version, commit, date
}

func (h *Handler) handleModels(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	if state == nil || state.Catalog() == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Model catalog unavailable")
	}

	models := state.Catalog().List()
	if len(models) == 0 {
		return h.adapter.Send(ctx, msg.Chat.ID, "📋 Model catalog is empty.")
	}

	cfg := h.configSnapshot()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 *Available Models* (%d):\n\n", len(models)))
	currentProvider := ""
	limit := min(len(models), 40)
	for i := 0; i < limit; i++ {
		m := models[i]
		if m.Provider != currentProvider {
			currentProvider = m.Provider
			sb.WriteString(fmt.Sprintf("\n[%s]\n", currentProvider))
		}
		marker := "•"
		if m.ID == cfg.Model {
			marker = "✅"
		}
		cost := "free/local"
		if m.CostPer1kIn > 0 || m.CostPer1kOut > 0 {
			cost = fmt.Sprintf("$%.4f/$%.4f per 1k", m.CostPer1kIn, m.CostPer1kOut)
		}
		sb.WriteString(fmt.Sprintf("%s `%s` — %s (%s)\n", marker, m.ID, truncateForTelegramList(m.DisplayName, 48), cost))
	}
	if len(models) > limit {
		sb.WriteString(fmt.Sprintf("\n... and %d more", len(models)-limit))
	}
	sb.WriteString("\n\nUse `/model <id>` to switch.")
	return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
}

// handleSoul shows the current SOUL info.
func (h *Handler) handleSoul(ctx context.Context, msg *gateway.Message) error {
	if h.state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "No SOUL configured.")
	}
	s := h.state.Soul()
	if s == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "No SOUL configured.")
	}

	prompt := s.SystemPrompt()
	if len(prompt) > 500 {
		prompt = prompt[:500] + "..."
	}

	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🧠 *Current SOUL:*\n\n%s", prompt))
}

// handleTools lists available tools.
func (h *Handler) handleTools(ctx context.Context, msg *gateway.Message) error {
	tools := h.tools()
	if tools == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Tool registry unavailable")
	}
	allTools := tools.List()

	if len(allTools) == 0 {
		return h.adapter.Send(ctx, msg.Chat.ID, "No tools available.")
	}

	var sb strings.Builder
	sb.WriteString("🔧 *Available Tools:*\n\n")

	for i, t := range allTools {
		if i >= 20 {
			sb.WriteString(fmt.Sprintf("\n... and %d more", len(allTools)-20))
			break
		}
		status := "✅"
		if !t.Enabled {
			status = "❌"
		}
		sb.WriteString(fmt.Sprintf("%s %s — %s\n", status, t.Name, t.Description))
	}

	return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
}

// handleReset resets the conversation for this chat.
func (h *Handler) handleReset(ctx context.Context, msg *gateway.Message) error {
	newID := h.resetSession(msg.Chat.ID)
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🔄 Conversation reset. New session: `%s`", shortSessionID(newID)))
}

// handleHistory shows the conversation history for this chat.
func (h *Handler) handleHistory(ctx context.Context, msg *gateway.Message) error {
	sessionID := h.getSessionID(msg.Chat.ID)

	sessions := h.sessionManager()
	if sessions == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "No conversation history.")
	}
	sess, ok := sessions.Get(sessionID)
	if !ok {
		return h.adapter.Send(ctx, msg.Chat.ID, "No conversation history.")
	}

	messages := sess.GetMessages()
	if len(messages) == 0 {
		return h.adapter.Send(ctx, msg.Chat.ID, "No messages in this session yet.")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📜 *History* (%d messages):\n\n", len(messages)))

	maxShow := 20
	start := 0
	if len(messages) > maxShow {
		start = len(messages) - maxShow
		sb.WriteString(fmt.Sprintf("_(showing last %d of %d)_\n\n", maxShow, len(messages)))
	}

	for i := start; i < len(messages); i++ {
		m := messages[i]
		role := ""
		switch m.Role {
		case "user":
			role = "👤"
		case "assistant":
			role = "🤖"
		case "tool":
			role = "🔧"
		default:
			role = "💬"
		}

		content := m.Content
		if len(content) > 80 {
			content = content[:80] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s %s\n", role, content))
	}

	return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
}

// handleSession shows current session info, or switches when an ID is supplied.
func (h *Handler) handleSession(ctx context.Context, msg *gateway.Message) error {
	if strings.TrimSpace(msg.Args) != "" {
		return h.handleSessionSwitch(ctx, msg, strings.TrimSpace(msg.Args))
	}

	h.mu.RLock()
	sessionID, ok := h.sessions[msg.Chat.ID]
	h.mu.RUnlock()

	if !ok {
		return h.adapter.Send(ctx, msg.Chat.ID, "No active session. Send a message to start one!")
	}

	sessions := h.sessionManager()
	if sessions == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("Session `%s` not found. It may have been cleaned up.", shortSessionID(sessionID)))
	}
	sess, ok := sessions.Get(sessionID)
	if !ok {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("Session `%s` not found. It may have been cleaned up.", shortSessionID(sessionID)))
	}

	info := fmt.Sprintf("📋 *Session Info:*\n\n• ID: `%s`\n• Title: %s\n• Messages: %d\n• Created: %s\n• Updated: %s",
		sessionID,
		sess.Title,
		sess.MessageCount(),
		sess.CreatedAt.Format("2006-01-02 15:04"),
		sess.UpdatedAt.Format("2006-01-02 15:04"),
	)

	return h.adapter.Send(ctx, msg.Chat.ID, info)
}

// handleSessions lists recent sessions so Telegram users can pick one to resume.
func (h *Handler) handleSessions(ctx context.Context, msg *gateway.Message) error {
	sessions := h.sessionManager()
	if sessions == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Session manager unavailable")
	}

	infos := sessions.ListInfo()
	if len(infos) == 0 {
		return h.adapter.Send(ctx, msg.Chat.ID, "No sessions yet. Send a message to start one!")
	}

	currentID := h.currentSessionID(msg.Chat.ID)
	var sb strings.Builder
	sb.WriteString("📚 *Recent Sessions:*\n\n")

	limit := 10
	if len(infos) < limit {
		limit = len(infos)
	}
	for i := 0; i < limit; i++ {
		info := infos[i]
		marker := "•"
		if info.ID == currentID {
			marker = "✅"
		}
		title := strings.TrimSpace(info.Title)
		if title == "" {
			title = "(untitled)"
		}
		sb.WriteString(fmt.Sprintf("%s `%s` — %s (%d msgs, updated %s)\n",
			marker,
			info.ID,
			truncateForTelegramList(title, 48),
			info.MessageCount,
			info.UpdatedAt.Format("01-02 15:04"),
		))
	}
	if len(infos) > limit {
		sb.WriteString(fmt.Sprintf("\n... and %d more", len(infos)-limit))
	}
	sb.WriteString("\n\nUse `/resume <title>` or `/resume <id>` to switch.")

	return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
}

func (h *Handler) handleResume(ctx context.Context, msg *gateway.Message) error {
	sessionID := strings.TrimSpace(msg.Args)
	if sessionID == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /resume <session_title_or_id>")
	}
	return h.handleSessionSwitch(ctx, msg, sessionID)
}

func (h *Handler) handleRename(ctx context.Context, msg *gateway.Message) error {
	title := strings.TrimSpace(msg.Args)
	if title == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /rename <title>")
	}
	sessions := h.sessionManager()
	if sessions == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Session manager unavailable")
	}
	sessionID := h.getSessionID(msg.Chat.ID)
	sess, ok := sessions.Get(sessionID)
	if !ok || sess == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Current session not found")
	}
	sess.SetTitle(title)
	if err := sess.Save(); err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Failed to rename session: %s", err.Error()))
	}
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Renamed current session to: %s", truncateForTelegramList(title, 120)))
}

func (h *Handler) handleSessionSwitch(ctx context.Context, msg *gateway.Message, sessionQuery string) error {
	sessions := h.sessionManager()
	if sessions == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Session manager unavailable")
	}

	result := findSessionByIDTitleOrPrefix(sessions, sessionQuery)
	switch result.status {
	case sessionLookupMatched:
	case sessionLookupAmbiguous:
		return h.adapter.Send(ctx, msg.Chat.ID, formatAmbiguousSessionSwitchMessage(sessionQuery, result.matches))
	default:
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("Session `%s` not found. Use /sessions to list recent sessions.", sessionQuery))
	}

	sess := result.session
	matchedID := result.id
	h.mu.Lock()
	oldSessionID, hadOld := h.sessions[msg.Chat.ID]
	h.sessions[msg.Chat.ID] = matchedID
	h.mu.Unlock()

	h.saveChatSessions()

	title := strings.TrimSpace(sess.Title)
	if title == "" {
		title = "(untitled)"
	}
	if hadOld && oldSessionID == matchedID {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Already on session `%s` — %s", shortSessionID(matchedID), truncateForTelegramList(title, 60)))
	}

	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Switched session: `%s`\nTitle: %s\nMessages: %d",
		shortSessionID(matchedID),
		truncateForTelegramList(title, 80),
		sess.MessageCount(),
	))
}

// handleSkills lists loaded skills.
func (h *Handler) handleSkills(ctx context.Context, msg *gateway.Message) error {
	skills := h.skillsList()
	if len(skills) == 0 {
		return h.adapter.Send(ctx, msg.Chat.ID, "No skills loaded.")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🎯 *Loaded Skills* (%d):\n\n", len(skills)))

	maxShow := 30
	for i, s := range skills {
		if i >= maxShow {
			sb.WriteString(fmt.Sprintf("\n... and %d more", len(skills)-maxShow))
			break
		}
		desc := s.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("• %s — %s\n", s.Name, desc))
	}

	return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
}

func (h *Handler) handleMCP(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	if state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ MCP runtime unavailable")
	}
	parts := strings.Fields(msg.Args)
	if len(parts) < 2 {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /mcp <name> <url> [api_key]")
	}
	apiKey := ""
	if len(parts) > 2 {
		apiKey = parts[2]
	}
	state.ConnectMCPServer(parts[0], parts[1], apiKey)
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Connected MCP server: %s (%s)", parts[0], parts[1]))
}

func (h *Handler) handleApprove(ctx context.Context, msg *gateway.Message) error {
	return h.handleToolPermission(ctx, msg, tool.PermAuto, "auto-approved")
}

func (h *Handler) handleDeny(ctx context.Context, msg *gateway.Message) error {
	return h.handleToolPermission(ctx, msg, tool.PermDeny, "denied")
}

func (h *Handler) handleToolPermission(ctx context.Context, msg *gateway.Message, perm tool.PermissionLevel, label string) error {
	name := strings.TrimSpace(msg.Args)
	if name == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /approve <tool> or /deny <tool>")
	}
	tools := h.tools()
	if tools == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Tool registry unavailable")
	}
	if err := tools.SetPermissionOverride(name, perm); err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
	}
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Tool `%s` is now %s.", name, label))
}

// handleCron manages scheduled tasks.
func (h *Handler) handleCron(ctx context.Context, msg *gateway.Message) error {
	engine := h.cronEngine()
	args := strings.TrimSpace(msg.Args)

	if args == "" || args == "list" {
		// List all cron jobs
		jobs := engine.ListJobs()
		if len(jobs) == 0 {
			return h.adapter.Send(ctx, msg.Chat.ID, "⏰ No scheduled tasks. Use /cron add <id> <schedule> <prompt>\nExample: /cron add drink-water 每两个小时 提醒我喝水")
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("⏰ *Scheduled Tasks* (%d):\n\n", len(jobs)))
		for _, job := range jobs {
			status := "🟢"
			switch job.Status {
			case cron.StatusRunning:
				status = "🔵"
			case cron.StatusPaused:
				status = "🟡"
			case cron.StatusFailed:
				status = "🔴"
			}
			sb.WriteString(fmt.Sprintf("%s %s — %s\n  Schedule: %s | Runs: %d\n", status, job.ID, job.Name, job.Schedule, job.RunCount))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
	}

	parts := strings.Fields(args)
	if len(parts) < 1 {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron [list|add|remove|pause|resume]")
	}

	switch parts[0] {
	case "add":
		if len(parts) < 4 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron add <id> <schedule> <prompt>\nExample: /cron add drink-water 每两个小时 提醒我喝水")
		}
		id := parts[1]
		scheduleText := parts[2]
		command := strings.Join(parts[3:], " ")
		if strings.TrimSpace(command) == "" {
			return h.adapter.Send(ctx, msg.Chat.ID, "❌ Missing prompt/command for cron job.")
		}
		sessionID := h.getSessionID(msg.Chat.ID)

		tools := h.tools()
		if tools == nil {
			return h.adapter.Send(ctx, msg.Chat.ID, "❌ Cron tools unavailable")
		}
		resp, err := tools.Call("cron_add", map[string]any{
			"id":                  id,
			"schedule":            scheduleText,
			"mode":                "agent",
			"command":             command,
			"platform":            "telegram",
			"chat_id":             msg.Chat.ID,
			"reply_to_message_id": msg.ID,
			"session_id":          sessionID,
		})
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Failed to add job: %s", err.Error()))
		}

		var out struct {
			ID       string `json:"id"`
			Schedule string `json:"schedule"`
			Message  string `json:"message"`
		}
		if json.Unmarshal([]byte(resp), &out) == nil && strings.TrimSpace(out.ID) != "" {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Job `%s` added.\nSchedule: %s", out.ID, out.Schedule))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, "✅ Cron job added.")

	case "remove":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron remove <id>")
		}
		tools := h.tools()
		if tools == nil {
			return h.adapter.Send(ctx, msg.Chat.ID, "❌ Cron tools unavailable")
		}
		if _, err := tools.Call("cron_remove", map[string]any{"id": parts[1]}); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Job `%s` removed", parts[1]))

	case "pause":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron pause <id>")
		}
		tools := h.tools()
		if tools == nil {
			return h.adapter.Send(ctx, msg.Chat.ID, "❌ Cron tools unavailable")
		}
		if _, err := tools.Call("cron_pause", map[string]any{"id": parts[1]}); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("⏸ Job `%s` paused", parts[1]))

	case "resume":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron resume <id>")
		}
		tools := h.tools()
		if tools == nil {
			return h.adapter.Send(ctx, msg.Chat.ID, "❌ Cron tools unavailable")
		}
		if _, err := tools.Call("cron_resume", map[string]any{"id": parts[1]}); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("▶️ Job `%s` resumed", parts[1]))

	case "start":
		engine.Start()
		return h.adapter.Send(ctx, msg.Chat.ID, "▶️ Cron engine started.")

	case "stop":
		engine.Stop()
		return h.adapter.Send(ctx, msg.Chat.ID, "⏹ Cron engine stopped.")

	default:
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron [list|add|remove|pause|resume|start|stop]")
	}
}

func (h *Handler) handleWatch(ctx context.Context, msg *gateway.Message) error {
	watcher := h.watcherService()
	if watcher == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Watch runtime unavailable")
	}

	args := strings.TrimSpace(msg.Args)
	if args == "" || args == "list" {
		patterns := watcher.ListPatterns()
		if len(patterns) == 0 {
			return h.adapter.Send(ctx, msg.Chat.ID, "📋 No watch patterns. Use /watch add <id> <glob> <interval>")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🔍 *Watch Patterns* (%d):\n\n", len(patterns)))
		for _, p := range patterns {
			lastCheck := "N/A"
			if !p.LastCheck.IsZero() {
				lastCheck = p.LastCheck.Format("2006-01-02 15:04:05")
			}
			sb.WriteString(fmt.Sprintf("• `%s` — %s\n  Interval: %s | Last: %s | Result: %s\n",
				p.ID,
				truncateForTelegramList(p.Pattern, 80),
				p.Interval,
				lastCheck,
				valueOrUnset(p.LastResult),
			))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
	}

	parts := strings.Fields(args)
	if len(parts) == 0 {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /watch [list|add|remove|start|stop]")
	}

	switch parts[0] {
	case "add":
		if len(parts) < 4 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /watch add <id> <glob> <interval>\nExample: /watch add logs \"logs/*.log\" 1m")
		}
		interval, err := time.ParseDuration(parts[3])
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Invalid interval: %s", err.Error()))
		}
		id := parts[1]
		pattern := parts[2]
		if err := watcher.AddPattern(id, "Watch: "+id, pattern, pattern, interval, nil); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Watch `%s` added for `%s` every %s.", id, pattern, interval))
	case "remove":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /watch remove <id>")
		}
		if err := watcher.RemovePattern(parts[1]); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Watch `%s` removed.", parts[1]))
	case "start":
		watcher.Start()
		return h.adapter.Send(ctx, msg.Chat.ID, "▶️ Watcher started.")
	case "stop":
		watcher.Stop()
		return h.adapter.Send(ctx, msg.Chat.ID, "⏹ Watcher stopped.")
	default:
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /watch [list|add|remove|start|stop]")
	}
}

func (h *Handler) handleDashboard(ctx context.Context, msg *gateway.Message) error {
	parts := strings.Fields(strings.TrimSpace(msg.Args))
	subcmd := "status"
	if len(parts) > 0 {
		subcmd = parts[0]
	}
	cfg := h.configSnapshot()
	addr := cfg.DashboardAddr
	if strings.TrimSpace(addr) == "" {
		addr = ":8765"
	}
	switch subcmd {
	case "status", "list", "":
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🌐 *Dashboard:*\n\n• Configured addr: `%s`\n• URL hint: %s\n\nStart/stop is process-local. From Telegram, run dashboard service management on the host with `lh dashboard start` or `lh dashboard stop`.", addr, dashboardURLHint(addr)))
	case "start", "stop":
		return h.adapter.Send(ctx, msg.Chat.ID, "Dashboard start/stop from Telegram is disabled. Run `lh dashboard start` or `lh dashboard stop` on the host.")
	default:
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /dashboard [status]")
	}
}

func dashboardURLHint(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = ":8765"
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}

func (h *Handler) handleMsgGateway(ctx context.Context, msg *gateway.Message) error {
	parts := strings.Fields(strings.TrimSpace(msg.Args))
	subcmd := "status"
	if len(parts) > 0 {
		subcmd = parts[0]
	}
	cfg := h.configSnapshot()
	switch subcmd {
	case "status", "":
		info := fmt.Sprintf("📨 *Message Gateway:*\n\n• platform: %s\n• start_all: %t\n• api_addr: %s\n• telegram.token: %s\n• telegram.proxy: %s\n• qqofficial.app_id: %s\n• qqofficial.sandbox: %t\n• weixin.account_id: %s\n• openclawweixin.account_id: %s\n\nRuntime status is not exposed by the gateway. Check the host terminal that started `lh msg-gateway start`.",
			valueOrUnset(cfg.MsgGatewayPlatform),
			cfg.MsgGatewayStartAll,
			valueOrUnset(cfg.MsgGatewayAPIAddr),
			maskSecret(cfg.MsgGatewayTelegramToken),
			valueOrUnset(cfg.MsgGatewayTelegramProxy),
			valueOrUnset(cfg.MsgGatewayQQAppID),
			cfg.MsgGatewayQQSandbox,
			valueOrUnset(cfg.MsgGatewayWeixinAccountID),
			valueOrUnset(cfg.MsgGatewayOpenClawAccount),
		)
		return h.adapter.Send(ctx, msg.Chat.ID, info)
	case "start", "stop":
		return h.adapter.Send(ctx, msg.Chat.ID, "Message gateway start/stop from Telegram is disabled. Run `lh msg-gateway start` in the host terminal, and stop it there with Ctrl+C.")
	default:
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /msg_gateway [status]")
	}
}

func (h *Handler) handleRAG(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	if state == nil || state.RAG() == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ RAG system unavailable")
	}
	ragMgr := state.RAG()
	parts := strings.Fields(strings.TrimSpace(msg.Args))
	if len(parts) == 0 {
		return h.sendRAGStats(ctx, msg, ragMgr)
	}

	switch parts[0] {
	case "stats":
		return h.sendRAGStats(ctx, msg, ragMgr)
	case "store":
		return h.sendRAGStore(ctx, msg, ragMgr)
	case "list":
		ids := ragMgr.ListDocuments()
		if len(ids) == 0 {
			return h.adapter.Send(ctx, msg.Chat.ID, "📚 Knowledge base is empty.")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📚 *Indexed Documents* (%d):\n\n", len(ids)))
		limit := min(len(ids), 25)
		for i := 0; i < limit; i++ {
			id := ids[i]
			doc, ok := ragMgr.GetDocument(id)
			if !ok || doc == nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("• `%s` — %s (%d chunks)\n", shortSessionID(id), truncateForTelegramList(doc.Title, 72), len(doc.Chunks)))
		}
		if len(ids) > limit {
			sb.WriteString(fmt.Sprintf("\n... and %d more", len(ids)-limit))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
	case "search", "query":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /rag search <query>")
		}
		query := strings.Join(parts[1:], " ")
		results, err := ragMgr.Search(ctx, query)
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Search failed: %s", err.Error()))
		}
		if len(results) == 0 {
			return h.adapter.Send(ctx, msg.Chat.ID, "🔍 No matching results.")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🔍 *RAG Results* (%d):\n\n", len(results)))
		limit := min(len(results), 8)
		for i := 0; i < limit; i++ {
			res := results[i]
			sb.WriteString(fmt.Sprintf("%d. [%.2f] %s\n%s\n\n", i+1, res.Score, truncateForTelegramList(res.DocTitle, 64), truncateForTelegramList(res.Content, 180)))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, strings.TrimSpace(sb.String()))
	case "remove":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /rag remove <doc_id>")
		}
		if ragMgr.RemoveDocument(parts[1]) {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Removed document: `%s`", parts[1]))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Document not found: `%s`", parts[1]))
	case "index":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /rag index <file_or_directory_path>")
		}
		path := strings.Join(parts[1:], " ")
		info, err := os.Stat(path)
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Path not found: %s", err.Error()))
		}
		if info.IsDir() {
			docs, err := ragMgr.IndexDirectory(path)
			if err != nil {
				return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Index directory failed: %s", err.Error()))
			}
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Indexed %d documents from `%s`.", len(docs), path))
		}
		doc, err := ragMgr.IndexFile(path)
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Index file failed: %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Indexed `%s` (%d chunks).", doc.Title, len(doc.Chunks)))
	default:
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /rag [stats|store|list|search|remove|index]")
	}
}

func (h *Handler) sendRAGStats(ctx context.Context, msg *gateway.Message, ragMgr *rag.RAGManager) error {
	stats := ragMgr.Stats()
	store := "memory"
	vectorCount := 0
	if ragMgr.IsSQLite() {
		store = "sqlite"
		if sqlStore := ragMgr.SQLiteStore(); sqlStore != nil {
			vectorCount, _, _ = sqlStore.Stats()
		}
	} else if ragMgr.Store() != nil {
		vectorCount = ragMgr.Store().Len()
	}
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("📚 *RAG Stats:*\n\n• Documents: %d\n• Chunks: %d\n• Store: %s\n• Vectors: %d",
		stats.DocumentCount, stats.ChunkCount, store, vectorCount))
}

func (h *Handler) sendRAGStore(ctx context.Context, msg *gateway.Message, ragMgr *rag.RAGManager) error {
	if ragMgr.IsSQLite() {
		sqlStore := ragMgr.SQLiteStore()
		if sqlStore == nil {
			return h.adapter.Send(ctx, msg.Chat.ID, "🗄 SQLite store unavailable")
		}
		count, dbSize, err := sqlStore.Stats()
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Store stats failed: %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🗄 *SQLite RAG Store:*\n\n• Path: `%s`\n• Vectors: %d\n• Size: %d bytes\n• Dimension: %d",
			sqlStore.Path(), count, dbSize, sqlStore.Dimension()))
	}
	store := ragMgr.Store()
	if store == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "💾 RAG store unavailable")
	}
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("💾 *Memory RAG Store:*\n\n• Vectors: %d\n• Dimension: %d", store.Len(), store.Dimension()))
}

// handleMetrics shows usage metrics.
func (h *Handler) handleMetrics(ctx context.Context, msg *gateway.Message) error {
	m := h.metricsCollector()
	snapshot := m.Snapshot()

	info := fmt.Sprintf("📊 *Metrics:*\n\n• Total requests: %d\n• Tool calls: %d\n• Errors: %d\n• Uptime: %s",
		snapshot.TotalRequests,
		snapshot.ToolCalls,
		snapshot.ErrorRequests,
		snapshot.Uptime,
	)

	return h.adapter.Send(ctx, msg.Chat.ID, info)
}

// handleHealth shows system health.
func (h *Handler) handleHealth(ctx context.Context, msg *gateway.Message) error {
	var sb strings.Builder
	sb.WriteString("🏥 *System Health:*\n\n")

	// Agent 状态
	sb.WriteString("• Agent: ✅ Running\n")

	// Cron 引擎
	cronEngine := h.cronEngine()
	if cronEngine.IsRunning() {
		sb.WriteString(fmt.Sprintf("• Cron Engine: ✅ Running (%d jobs)\n", cronEngine.JobCount()))
	} else {
		sb.WriteString("• Cron Engine: ❌ Stopped\n")
	}

	// Skills
	skills := h.skillsList()
	sb.WriteString(fmt.Sprintf("• Skills: ✅ %d loaded\n", len(skills)))

	// Sessions
	sb.WriteString("• Sessions: ✅ Active\n")

	// Memory
	mem := h.memoryStore()
	if mem != nil {
		sb.WriteString("• Memory: ✅ Active\n")
	}

	// Metrics
	m := h.metricsCollector()
	snapshot := m.Snapshot()
	sb.WriteString(fmt.Sprintf("• Total requests: %d\n", snapshot.TotalRequests))

	return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
}

func (h *Handler) handleLearn(ctx context.Context, msg *gateway.Message) error {
	var sb strings.Builder
	sb.WriteString("🎓 *LuckyHarness Learning Mode*\n\n")
	sb.WriteString("*Commands:*\n")
	sb.WriteString("• `/learn_start lh-agent-systems` — start or resume the built-in project course\n")
	sb.WriteString("• `/learn_current` — show the current module\n")
	sb.WriteString("• `/learn_lab` — show the current lab\n")
	sb.WriteString("• `/learn_submit <evidence>` — submit lab evidence and advance\n")
	sb.WriteString("• `/learn_progress` — show course progress\n\n")
	sb.WriteString("*Courses:*\n")
	for _, course := range learning.BuiltinCourses() {
		sb.WriteString(fmt.Sprintf("• `%s` — %s (%d modules)\n", course.ID, course.Title, len(course.Modules)))
	}
	return h.adapter.Send(ctx, msg.Chat.ID, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleLearnStart(ctx context.Context, msg *gateway.Message) error {
	courseID := strings.TrimSpace(msg.Args)
	if courseID == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /learn_start <course>\nExample: `/learn_start lh-agent-systems`")
	}
	course, ok := learning.FindCourse(courseID)
	if !ok {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Unknown course: `%s`\nUse `/learn` to list courses.", courseID))
	}
	store, err := h.learningStore()
	if err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Learning store unavailable: %s", err.Error()))
	}
	cp, err := store.StartCourse(course)
	if err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Failed to start course: %s", err.Error()))
	}
	module, _ := course.ModuleByID(cp.CurrentModule)
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🎓 *Learning Started*\n\n• Course: %s\n• Current: `%s` — %s\n• Progress: `%s`\n\nUse `/learn_lab` to open the first lab.",
		course.Title, module.ID, module.Title, store.Path()))
}

func (h *Handler) handleLearnCurrent(ctx context.Context, msg *gateway.Message) error {
	course, cp, store, err := h.activeLearningState()
	if err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
	}
	module, ok := course.ModuleByID(cp.CurrentModule)
	if !ok {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Current module not found: `%s`", cp.CurrentModule))
	}
	done, total := learning.CourseCompletion(course, cp)
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🎓 *Current Learning Module*\n\n• Course: %s\n• Module: `%s` — %s\n• Objective: %s\n• Completion: %d/%d\n• Progress: `%s`",
		course.Title, module.ID, module.Title, module.Objective, done, total, store.Path()))
}

func (h *Handler) handleLearnLab(ctx context.Context, msg *gateway.Message) error {
	course, cp, _, err := h.activeLearningState()
	if err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
	}
	module, ok := course.ModuleByID(cp.CurrentModule)
	if !ok {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Current module not found: `%s`", cp.CurrentModule))
	}
	return h.adapter.Send(ctx, msg.Chat.ID, formatTelegramLearningLab(module))
}

func (h *Handler) handleLearnSubmit(ctx context.Context, msg *gateway.Message) error {
	evidence := strings.TrimSpace(msg.Args)
	if evidence == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /learn_submit <evidence>")
	}
	course, _, store, err := h.activeLearningState()
	if err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
	}
	cp, mp, err := store.SubmitEvidence(course, evidence, true)
	if err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Failed to submit evidence: %s", err.Error()))
	}
	var sb strings.Builder
	sb.WriteString("✅ *Learning Evidence Accepted*\n\n")
	sb.WriteString(fmt.Sprintf("• Module: `%s`\n", mp.ModuleID))
	sb.WriteString(fmt.Sprintf("• Attempts: %d\n", mp.Attempts))
	if cp.CompletedAt != nil {
		sb.WriteString(fmt.Sprintf("\n🎉 Course completed: %s", course.Title))
		return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
	}
	next, _ := course.ModuleByID(cp.CurrentModule)
	sb.WriteString(fmt.Sprintf("\nNext: `%s` — %s\nUse `/learn_lab` to continue.", next.ID, next.Title))
	return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
}

func (h *Handler) handleLearnProgress(ctx context.Context, msg *gateway.Message) error {
	course, cp, store, err := h.activeLearningState()
	if err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
	}
	done, total := learning.CourseCompletion(course, cp)
	var sb strings.Builder
	sb.WriteString("📚 *Learning Progress*\n\n")
	sb.WriteString(fmt.Sprintf("• Course: %s\n", course.Title))
	sb.WriteString(fmt.Sprintf("• Completion: %d/%d\n\n", done, total))
	for _, module := range course.Modules {
		mp := cp.Modules[module.ID]
		status := mp.Status
		if status == "" {
			status = "pending"
		}
		icon := "○"
		switch status {
		case "completed":
			icon = "✅"
		case "active":
			icon = "▶"
		}
		sb.WriteString(fmt.Sprintf("%s `%s` — %s (attempts=%d)\n", icon, module.ID, status, mp.Attempts))
	}
	sb.WriteString(fmt.Sprintf("\nProgress: `%s`", store.Path()))
	return h.adapter.Send(ctx, msg.Chat.ID, strings.TrimSpace(sb.String()))
}

func (h *Handler) learningStore() (*learning.ProgressStore, error) {
	home := strings.TrimSpace(h.configSnapshot().HomeDir)
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("locate home dir: %w", err)
		}
		home = filepath.Join(userHome, ".luckyharness")
	}
	return learning.NewProgressStore(home), nil
}

func (h *Handler) activeLearningState() (learning.Course, learning.CourseProgress, *learning.ProgressStore, error) {
	store, err := h.learningStore()
	if err != nil {
		return learning.Course{}, learning.CourseProgress{}, nil, err
	}
	progress, err := store.Load()
	if err != nil {
		return learning.Course{}, learning.CourseProgress{}, nil, err
	}
	if progress.ActiveCourseID == "" {
		return learning.Course{}, learning.CourseProgress{}, nil, fmt.Errorf("no active course; use `/learn_start lh-agent-systems`")
	}
	course, ok := learning.FindCourse(progress.ActiveCourseID)
	if !ok {
		return learning.Course{}, learning.CourseProgress{}, nil, fmt.Errorf("active course `%s` is not installed", progress.ActiveCourseID)
	}
	cp, ok := progress.Courses[progress.ActiveCourseID]
	if !ok {
		return learning.Course{}, learning.CourseProgress{}, nil, fmt.Errorf("active course `%s` has no progress", progress.ActiveCourseID)
	}
	return course, cp, store, nil
}

func formatTelegramLearningLab(module learning.Module) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🧪 *Lab: %s*\n\n", module.Lab.ID))
	sb.WriteString(fmt.Sprintf("• Module: `%s` — %s\n", module.ID, module.Title))
	sb.WriteString(fmt.Sprintf("• Prompt: %s\n", module.Lab.Prompt))
	if len(module.Concepts) > 0 {
		sb.WriteString(fmt.Sprintf("• Concepts: %s\n", strings.Join(module.Concepts, ", ")))
	}
	if len(module.Lab.AgentRoles) > 0 {
		sb.WriteString(fmt.Sprintf("• Agent roles: %s\n", strings.Join(module.Lab.AgentRoles, ", ")))
	}
	if len(module.Lab.Commands) > 0 {
		sb.WriteString("\n*Commands:*\n")
		for _, c := range module.Lab.Commands {
			sb.WriteString(fmt.Sprintf("• `%s`\n", c))
		}
	}
	if len(module.Lab.Evidence) > 0 {
		sb.WriteString("\n*Evidence:*\n")
		for _, e := range module.Lab.Evidence {
			sb.WriteString(fmt.Sprintf("• %s\n", e))
		}
	}
	if len(module.Rubric) > 0 {
		sb.WriteString("\n*Rubric:*\n")
		for _, r := range module.Rubric {
			sb.WriteString(fmt.Sprintf("• %s\n", r))
		}
	}
	sb.WriteString(fmt.Sprintf("\n*Deliverable:* %s\n", module.Lab.Deliverable))
	sb.WriteString("\nSubmit with `/learn_submit <evidence>`.")
	return strings.TrimSpace(sb.String())
}

func (h *Handler) handleRemember(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	content := strings.TrimSpace(msg.Args)
	if content == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /remember <content>")
	}
	if state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Memory runtime unavailable")
	}
	if err := state.Remember(content, "user"); err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
	}
	return h.adapter.Send(ctx, msg.Chat.ID, "💾 Saved to medium-term memory.")
}

func (h *Handler) handleRememberLong(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	content := strings.TrimSpace(msg.Args)
	if content == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /remember_long <content>")
	}
	if state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Memory runtime unavailable")
	}
	if err := state.RememberLongTerm(content, "user"); err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
	}
	return h.adapter.Send(ctx, msg.Chat.ID, "🧠 Saved to long-term memory.")
}

func (h *Handler) handleRecall(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	query := strings.TrimSpace(msg.Args)
	if query == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /recall <query>")
	}
	if state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Memory runtime unavailable")
	}
	results := state.Recall(query)
	if len(results) == 0 {
		return h.adapter.Send(ctx, msg.Chat.ID, "🔍 No matching memories.")
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 *Memories* (%d):\n\n", len(results)))
	limit := min(len(results), 10)
	for i := 0; i < limit; i++ {
		e := results[i]
		sb.WriteString(fmt.Sprintf("%d. `%s` [%s] %.2f\n%s\n\n",
			i+1, shortSessionID(e.ID), e.Tier.String(), e.Importance, truncateForTelegramList(e.Content, 180)))
	}
	if len(results) > limit {
		sb.WriteString(fmt.Sprintf("... and %d more", len(results)-limit))
	}
	return h.adapter.Send(ctx, msg.Chat.ID, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleMemStats(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	if state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Memory runtime unavailable")
	}
	stats := state.MemoryStats()
	total := stats[memory.TierShort] + stats[memory.TierMedium] + stats[memory.TierLong]
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("📊 *Memory Stats:*\n\n• Short: %d\n• Medium: %d\n• Long: %d\n• Total: %d",
		stats[memory.TierShort], stats[memory.TierMedium], stats[memory.TierLong], total))
}

func (h *Handler) handleMemDecay(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	if state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Memory runtime unavailable")
	}
	deleted := state.DecayMemory(0.05)
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🗑 Decayed %d low-weight memories.", deleted))
}

func (h *Handler) handlePromote(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	id := strings.TrimSpace(msg.Args)
	if id == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /promote <memory_id>")
	}
	if state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Memory runtime unavailable")
	}
	if err := state.PromoteMemory(id); err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
	}
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("⬆️ Promoted memory: `%s`", id))
}

func (h *Handler) handleProfile(ctx context.Context, msg *gateway.Message) error {
	home := h.configSnapshot().HomeDir
	if strings.TrimSpace(home) == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Failed to locate home dir: %s", err.Error()))
		}
		home = filepath.Join(home, ".luckyharness")
	}
	mgr, err := profile.NewManager(home)
	if err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Profile manager unavailable: %s", err.Error()))
	}

	parts := strings.Fields(strings.TrimSpace(msg.Args))
	subcmd := "list"
	if len(parts) > 0 {
		subcmd = parts[0]
	}
	switch subcmd {
	case "list", "":
		infos := mgr.ListWithInfo()
		if len(infos) == 0 {
			return h.adapter.Send(ctx, msg.Chat.ID, "📋 No profiles.")
		}
		var sb strings.Builder
		sb.WriteString("👤 *Profiles:*\n\n")
		for _, info := range infos {
			marker := "•"
			if info.Active {
				marker = "✅"
			}
			sb.WriteString(fmt.Sprintf("%s `%s` — %s/%s\n", marker, info.Name, valueOrUnset(info.Provider), valueOrUnset(info.Model)))
		}
		sb.WriteString("\nUse `/profile switch <name>` to change the active profile for future starts.")
		return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
	case "switch":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /profile switch <name>")
		}
		if err := mgr.Switch(parts[1]); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Switched active profile to `%s`. It takes effect on next startup.", parts[1]))
	default:
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /profile [list|switch <name>]")
	}
}

func (h *Handler) handleContext(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	if state == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Context runtime unavailable")
	}
	cfg := state.ContextWindowConfig()
	cacheStats := state.ContextCacheStats()
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("📐 *Context Window:*\n\n• Max tokens: %d\n• Reserved: %d\n• Available: %d\n• Strategy: %s\n• Sliding window: %d\n• Max turns: %d\n• Memory budget: %d\n• Summary threshold: %.0f%%\n\n*Context Cache:*\n• Entries: %v\n• Hits: %v\n• Misses: %v",
		cfg.MaxTokens,
		cfg.ReservedTokens,
		cfg.MaxTokens-cfg.ReservedTokens,
		cfg.Strategy,
		cfg.SlidingWindowSize,
		cfg.MaxConversationTurns,
		cfg.MemoryBudget,
		cfg.SummarizeThreshold*100,
		cacheStats["entries"],
		cacheStats["hits"],
		cacheStats["misses"],
	))
}

func (h *Handler) handleFC(ctx context.Context, msg *gateway.Message) error {
	tools := h.tools()
	if tools == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Tool registry unavailable")
	}
	parts := strings.Fields(strings.TrimSpace(msg.Args))
	subcmd := "tools"
	if len(parts) > 0 {
		subcmd = parts[0]
	}
	switch subcmd {
	case "tools", "list":
		enabled := tools.ListEnabled()
		if len(enabled) == 0 {
			return h.adapter.Send(ctx, msg.Chat.ID, "📋 No function-calling tools available.")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🔧 *Function Tools* (%d):\n\n", len(enabled)))
		limit := min(len(enabled), 25)
		for i := 0; i < limit; i++ {
			t := enabled[i]
			sb.WriteString(fmt.Sprintf("• %s [%s] — %s\n", t.Name, t.Permission.String(), truncateForTelegramList(t.Description, 90)))
		}
		if len(enabled) > limit {
			sb.WriteString(fmt.Sprintf("\n... and %d more", len(enabled)-limit))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
	case "history":
		return h.adapter.Send(ctx, msg.Chat.ID, "📋 Function-calling history is stored in session history. Use /sessions or /history.")
	case "clear":
		return h.adapter.Send(ctx, msg.Chat.ID, "✅ Function-calling transient history cleared.")
	default:
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /fc [tools|history|clear]")
	}
}

func (h *Handler) handleEmbedder(ctx context.Context, msg *gateway.Message) error {
	state := h.stateService()
	if state == nil || state.EmbedderRegistry() == nil {
		return h.adapter.Send(ctx, msg.Chat.ID, "❌ Embedder registry unavailable")
	}
	reg := state.EmbedderRegistry()
	parts := strings.SplitN(strings.TrimSpace(msg.Args), " ", 2)
	subcmd := ""
	subarg := ""
	if len(parts) > 0 {
		subcmd = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		subarg = strings.TrimSpace(parts[1])
	}

	switch subcmd {
	case "", "list":
		list := reg.List()
		if len(list) == 0 {
			return h.adapter.Send(ctx, msg.Chat.ID, "📋 No embedders registered.")
		}
		var sb strings.Builder
		sb.WriteString("📋 *Embedders:*\n\n")
		for _, info := range list {
			marker := "•"
			if info.Active {
				marker = "✅"
			}
			sb.WriteString(fmt.Sprintf("%s `%s` — %s/%s dim=%d\n", marker, info.ID, info.Name, info.Model, info.Dimension))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
	case "switch":
		if subarg == "" {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /embedder switch <id>")
		}
		if !reg.Switch(subarg) {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Embedder not found: `%s`", subarg))
		}
		active := reg.Active()
		if active == nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Switched embedder to: `%s`", subarg))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Switched embedder to `%s` (%s/%s dim=%d)", subarg, active.Name(), active.Model(), active.Dimension()))
	case "test":
		text := subarg
		if text == "" {
			text = "Hello, world!"
		}
		active := reg.Active()
		if active == nil {
			return h.adapter.Send(ctx, msg.Chat.ID, "❌ No active embedder.")
		}
		vec, err := active.Embed(ctx, text)
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Embed failed: %s", err.Error()))
		}
		sampleLen := min(len(vec), 5)
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🧮 *Embedder Test:*\n\n• Model: %s/%s\n• Dim: %d\n• Input: %q\n• First %d values: %v",
			active.Name(), active.Model(), len(vec), text, sampleLen, vec[:sampleLen]))
	default:
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /embedder [list|switch|test]")
	}
}

// v0.56.0: nanobot 风格内置命令

// handleNew 开启新对话（创建新会话）
func (h *Handler) handleNew(ctx context.Context, msg *gateway.Message) error {
	chatID := msg.Chat.ID

	// 创建新会话
	sessions := h.sessionManager()
	if sessions == nil {
		return h.adapter.Send(ctx, chatID, "❌ Session manager unavailable")
	}
	newSess := sessions.New()

	h.mu.Lock()
	oldSessionID, hadOld := h.sessions[chatID]
	h.sessions[chatID] = newSess.ID
	h.mu.Unlock()

	h.saveChatSessions()

	info := ""
	if hadOld {
		info = fmt.Sprintf("旧会话：%s\n", oldSessionID)
	}

	return h.adapter.Send(ctx, chatID, fmt.Sprintf("✅ New session started.\n%s新会话 ID: `%s`", info, newSess.ID))
}

// handleStop 停止当前任务
func (h *Handler) handleStop(ctx context.Context, msg *gateway.Message) error {
	chatID := msg.Chat.ID
	if !h.cancelChatTask(chatID) {
		return h.adapter.Send(ctx, chatID, "ℹ️ 当前没有运行中的任务")
	}
	return h.adapter.Send(ctx, chatID, "🛑 已停止当前任务")
}

// handleStatus 查看状态
func (h *Handler) handleStatus(ctx context.Context, msg *gateway.Message) error {
	chatID := msg.Chat.ID
	sessionID := h.getSessionID(chatID)

	var sb strings.Builder
	sb.WriteString("📊 *LuckyHarness Status*\n\n")

	// 版本
	sb.WriteString(fmt.Sprintf("• Version: v%s\n", "0.55.0"))

	// 模型
	cfg := h.configSnapshot()
	sb.WriteString(fmt.Sprintf("• Model: %s\n", cfg.Model))

	// 运行时间
	metricsVal := h.metricsCollector()
	if metricsVal == nil {
		return h.adapter.Send(ctx, chatID, "❌ Metrics unavailable")
	}
	uptime := time.Since(metricsVal.StartTime)
	sb.WriteString(fmt.Sprintf("• Uptime: %s\n", formatDuration(uptime)))

	// 会话历史
	sessions := h.sessionManager()
	if sessions == nil {
		return h.adapter.Send(ctx, chatID, "❌ Session manager unavailable")
	}
	sess, ok := sessions.Get(sessionID)
	msgCount := 0
	if ok && sess != nil {
		msgCount = sess.MessageCount()
	}
	sb.WriteString(fmt.Sprintf("• Session messages: %d\n", msgCount))

	// 指标
	m := metricsVal
	snapshot := m.Snapshot()
	sb.WriteString(fmt.Sprintf("• Total requests: %d\n", snapshot.TotalRequests))

	running, queued := h.queueStatus(chatID)
	if running {
		sb.WriteString("• Current task: running\n")
	} else {
		sb.WriteString("• Current task: idle\n")
	}
	sb.WriteString(fmt.Sprintf("• Queue pending: %d\n", queued))

	return h.adapter.Send(ctx, chatID, sb.String())
}

// handleRestart 重启 bot
func (h *Handler) handleRestart(ctx context.Context, msg *gateway.Message) error {
	chatID := msg.Chat.ID

	h.mu.Lock()
	if h.restarting {
		h.mu.Unlock()
		return h.adapter.Send(ctx, chatID, "ℹ️ Bot 正在重启中，请稍候")
	}
	h.restarting = true
	h.mu.Unlock()

	// 先通知，再执行重连
	_ = h.adapter.Send(ctx, chatID, "🔄 Restarting bot gateway...")

	go func() {
		defer func() {
			h.mu.Lock()
			h.restarting = false
			h.mu.Unlock()
		}()

		// 停止当前 chat 的任务，避免重启期间残留 goroutine
		h.cancelChatTask(chatID)

		if err := h.adapter.Stop(); err != nil {
			fmt.Printf("[telegram] restart stop failed: %v\n", err)
		}
		time.Sleep(1200 * time.Millisecond)

		if err := h.adapter.Start(context.Background()); err != nil {
			fmt.Printf("[telegram] restart start failed: %v\n", err)
			return
		}
		_ = h.adapter.Send(context.Background(), chatID, "✅ Bot 已重连并恢复轮询")
	}()

	return nil
}

// formatDuration 格式化运行时间
func formatDuration(d time.Duration) string {
	return utils.FormatDurationCompact(d)
}
