package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/metrics"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/soul"
	"github.com/yurika0211/luckyharness/internal/tool"
	"github.com/yurika0211/luckyharness/internal/utils"
)

// agentProvider 定义 Handler 需要从 Agent 获得的能力接口。
// 生产代码使用 *agent.Agent 实现，测试代码可注入 mock。
type agentProvider interface {
	Sessions() *session.Manager
	Config() agentConfigProvider
	SwitchModel(modelID string) error
	Soul() *soul.Soul
	Tools() *tool.Registry
	Skills() []*tool.SkillInfo
	CronEngine() *cron.Engine
	Chat(ctx context.Context, userInput string) (string, error)
	ChatWithSession(ctx context.Context, sessionID, userInput string) (string, error)
	ChatWithSessionStream(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error)
	ProgressFeedback(ctx context.Context, userInput string, round int, observations []string) (string, error)
	AnalyzeAttachments(ctx context.Context, attachments []gateway.Attachment) (string, error)
	Metrics() *metrics.Metrics
	Memory() *memory.Store
}

// agentConfigProvider 定义 Handler 需要从 config 获得的能力接口。
type agentConfigProvider interface {
	Get() agentConfigSnapshot
}

// agentConfigSnapshot 是 config 快照的最小子集。
type agentConfigSnapshot struct {
	Model                     string
	Provider                  string
	ChatTimeoutSeconds        int
	ProgressAsMessages        bool
	ProgressAsNaturalLanguage bool
	ProgressSummaryWithLLM    bool
	ShowToolDetailsInResult   bool
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

func (a agentProviderAdapter) ChatWithSessionStream(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
	return a.inner.ChatWithSessionStream(ctx, sessionID, userInput)
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

// agentConfigWrapper 将 *config.Manager 适配为 agentConfigProvider 接口。
type agentConfigWrapper struct {
	mgr *config.Manager
}

func (w agentConfigWrapper) Get() agentConfigSnapshot {
	cfg := w.mgr.Get()
	return agentConfigSnapshot{
		Model:                     cfg.Model,
		Provider:                  cfg.Provider,
		ChatTimeoutSeconds:        cfg.MsgGateway.Telegram.ChatTimeoutSeconds,
		ProgressAsMessages:        cfg.MsgGateway.Telegram.ProgressAsMessages,
		ProgressAsNaturalLanguage: cfg.MsgGateway.Telegram.ProgressAsNaturalLanguage,
		ProgressSummaryWithLLM:    cfg.MsgGateway.Telegram.ProgressSummaryWithLLM,
		ShowToolDetailsInResult:   cfg.MsgGateway.Telegram.ShowToolDetailsInResult,
	}
}

type telegramCommandHandler func(ctx context.Context, msg *gateway.Message) error

// Handler processes Telegram bot commands and messages with per-chat session management.
type Handler struct {
	adapter  *Adapter
	agent    agentProvider
	commands map[string]telegramCommandHandler

	mu         sync.RWMutex
	sessions   map[string]string // chatID → sessionID
	tasks      map[string]*chatTask
	queues     map[string]*chatQueue
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
	ctx       context.Context
	msg       *gateway.Message
	inputText string
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
	var ap agentProvider
	if a != nil {
		ap = agentProviderAdapter{a}
	}
	memeDir, memeProbability, memeCooldown := resolveRandomMemeConfig()
	h := &Handler{
		adapter:                   adapter,
		agent:                     ap,
		commands:                  make(map[string]telegramCommandHandler),
		sessions:                  make(map[string]string),
		tasks:                     make(map[string]*chatTask),
		queues:                    make(map[string]*chatQueue),
		dataDir:                   "", // 默认不持久化，需 SetDataDir 启用
		chatStreamTimeout:         resolveChatStreamTimeout(ap),
		progressAsMessages:        resolveProgressAsMessages(ap),
		progressAsNaturalLanguage: resolveProgressAsNaturalLanguage(ap),
		progressSummaryWithLLM:    resolveProgressSummaryWithLLM(ap),
		showToolDetailsInResult:   resolveShowToolDetailsInResult(ap),
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
	return map[string]telegramCommandHandler{
		"start":   h.handleStart,
		"help":    h.handleHelp,
		"chat":    func(ctx context.Context, msg *gateway.Message) error { return h.dispatchChatAsync(ctx, msg, msg.Args) },
		"model":   h.handleModel,
		"soul":    h.handleSoul,
		"tools":   h.handleTools,
		"reset":   h.handleReset,
		"history": h.handleHistory,
		"session": h.handleSession,
		"skills":  h.handleSkills,
		"cron":    h.handleCron,
		"metrics": h.handleMetrics,
		"health":  h.handleHealth,
		"new":     h.handleNew,
		"stop":    h.handleStop,
		"status":  h.handleStatus,
		"restart": h.handleRestart,
	}
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

func resolveChatStreamTimeout(ap agentProvider) time.Duration {
	timeout := defaultChatStreamTimeout
	if ap == nil {
		return timeout
	}
	cfg := ap.Config().Get()
	if cfg.ChatTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.ChatTimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = defaultChatStreamTimeout
	}
	return timeout
}

func resolveProgressAsMessages(ap agentProvider) bool {
	enabled := true
	if ap == nil {
		return enabled
	}
	cfg := ap.Config().Get()
	return cfg.ProgressAsMessages
}

func resolveProgressAsNaturalLanguage(ap agentProvider) bool {
	if ap == nil {
		return false
	}
	cfg := ap.Config().Get()
	return cfg.ProgressAsNaturalLanguage
}

func resolveProgressSummaryWithLLM(ap agentProvider) bool {
	if ap == nil {
		return false
	}
	cfg := ap.Config().Get()
	return cfg.ProgressSummaryWithLLM
}

func resolveShowToolDetailsInResult(ap agentProvider) bool {
	if ap == nil {
		return false
	}
	cfg := ap.Config().Get()
	return cfg.ShowToolDetailsInResult
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
		// 验证 session 是否还存在
		if _, ok := h.agent.Sessions().Get(sessionID); ok {
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
	sess := h.agent.Sessions().New()
	h.mu.Lock()
	h.sessions[chatID] = sess.ID
	h.mu.Unlock()
	h.saveChatSessions()
	return sess.ID
}

// resetSession creates a new session for the chat, discarding the old one.
func (h *Handler) resetSession(chatID string) string {
	sess := h.agent.Sessions().New()
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

func (h *Handler) dispatchChatAsync(ctx context.Context, msg *gateway.Message, inputText string) error {
	msgCopy := *msg
	position, startWorker := h.enqueueChatRequest(msg.Chat.ID, &queuedChatRequest{
		ctx:       ctx,
		msg:       &msgCopy,
		inputText: inputText,
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
		if err := h.handleChat(req.ctx, req.msg, req.inputText); err != nil {
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

	// v0.36.0: 如果有附件，构造多媒体描述
	inputText := msg.Text
	if len(msg.Attachments) > 0 {
		inputText = h.composeAttachmentInput(ctx, inputText, msg.Attachments)
	}

	// Regular text in private chats → forward to Agent
	if msg.Chat.Type == gateway.ChatPrivate {
		return h.dispatchChatAsync(ctx, msg, inputText)
	}

	// Group chats: only respond if mentioned or replied to (already filtered by adapter)
	return h.dispatchChatAsync(ctx, msg, inputText)
}

func (h *Handler) composeAttachmentInput(ctx context.Context, baseText string, attachments []gateway.Attachment) string {
	var sections []string
	if strings.TrimSpace(baseText) != "" {
		sections = append(sections, strings.TrimSpace(baseText))
	}

	if h.agent != nil {
		analysis, err := h.agent.AnalyzeAttachments(ctx, attachments)
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
	if handler, ok := h.commands[msg.Command]; ok {
		return handler(ctx, msg)
	}
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("Unknown command: /%s\nType /help for available commands.", msg.Command))
}

// handleStart sends a welcome message.
func (h *Handler) handleStart(ctx context.Context, msg *gateway.Message) error {
	welcome := `🍀 *LuckyHarness Bot*

I'm an AI assistant powered by LuckyHarness.

*Available commands:*
/chat _message_ — Send a message to the AI
/model — Show current model
/soul — Show current SOUL info
/tools — List available tools
/skills — List loaded skills
/cron — Manage scheduled tasks
/metrics — Show usage metrics
/health — System health check
/reset — Reset conversation
/history — Show conversation history
/session — Show current session info
/help — Show this help

You can also just type a message directly!
Send me photos, voice messages, or files!`

	return h.adapter.Send(ctx, msg.Chat.ID, welcome)
}

// handleHelp lists available commands.
func (h *Handler) handleHelp(ctx context.Context, msg *gateway.Message) error {
	help := `*Available Commands:*

*🍀 基础命令*
/start — 欢迎消息
/help — 显示此帮助
/chat _消息_ — 发送消息给 AI

*⚙️ 系统管理*
/model \[name] — 查看/设置当前模型
/soul — 查看 SOUL 信息
/tools — 列出可用工具
/skills — 列出已加载技能
/cron \[list|add|remove] — 管理定时任务
/metrics — 查看使用指标
/health — 系统健康检查

*💬 会话管理*
/reset — 重置对话
/history — 查看对话历史
/session — 查看会话信息
/new — 开启新对话（清空历史）
/stop — 停止当前任务
/status — 查看状态
/restart — 重启 bot

*Tips:*
• 私聊直接发送消息即可
• 群聊需要 @bot 或回复 bot 消息
• Each chat has its own conversation session
• Send photos, voice, or files for multimodal processing`

	return h.adapter.Send(ctx, msg.Chat.ID, help)
}

// handleChat sends a message to the agent and returns the response.
// Uses streaming output with thinking/tool-call visualization when available.
func (h *Handler) handleChat(ctx context.Context, msg *gateway.Message, text string) error {
	if strings.TrimSpace(text) == "" {
		return h.adapter.Send(ctx, msg.Chat.ID, "Please provide a message. Usage: /chat <message>")
	}

	taskCtx, task := h.beginChatTask(msg.Chat.ID, ctx)
	defer h.finishChatTask(msg.Chat.ID, task)

	// 收到消息后给用户点赞 👍
	go h.adapter.ReactToMessage(msg.Chat.ID, msg.ID, "👍")

	sessionID := h.getSessionID(msg.Chat.ID)

	// 群聊中在输入文本前加上发送者名字，让 agent 知道是谁在说话
	inputText := text
	if msg.IsGroupTrigger && msg.Sender.DisplayName() != "" {
		inputText = fmt.Sprintf("[%s]: %s", msg.Sender.DisplayName(), text)
	}
	inputText = telegramMediaDeliveryGuidance(inputText)

	// 自然语言进度模式：直接按步骤发独立消息，最终结论也作为“最后一条新消息”发送。
	// 这样可以避免结论写回到最早的占位流消息，导致视觉上跑到最上面。
	if h.effectiveProgressAsMessages() && h.effectiveProgressAsNaturalLanguage() {
		return h.handleChatNarrativeStream(taskCtx, msg, inputText, sessionID)
	}

	// 尝试流式输出（Adapter 已实现 StreamGateway）
	sender, err := h.adapter.SendStream(taskCtx, msg.Chat.ID, msg.ID)
	if err == nil {
		return h.handleChatStream(taskCtx, sender, msg, inputText, sessionID)
	}
	// SendStream 失败，回退到非流式

	// 回退到非流式
	return h.handleChatSync(taskCtx, msg, inputText, sessionID)
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

func extractRoundNumber(thinking string) int {
	var round int
	if _, err := fmt.Sscanf(strings.TrimSpace(thinking), "Thinking... (round %d)", &round); err == nil && round > 0 {
		return round
	}
	return 0
}

func (h *Handler) generateRoundProgressFeedback(ctx context.Context, msg *gateway.Message, userInput string, round int, observations []string, lastProgress string) string {
	if len(observations) == 0 || h.agent == nil {
		return ""
	}
	summaryCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	summary, err := h.agent.ProgressFeedback(summaryCtx, userInput, round, observations)
	if err != nil {
		return ""
	}
	summary = strings.TrimSpace(summary)
	if summary == "" || summary == lastProgress {
		return ""
	}
	return summary
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

func (h *Handler) openChatEventStream(ctx context.Context, chatID, text, sessionID string) (<-chan agent.ChatEvent, error) {
	events, err := h.agent.ChatWithSessionStream(ctx, sessionID, text)
	if err == nil {
		return events, nil
	}
	if !strings.Contains(err.Error(), "session not found") {
		return nil, err
	}

	h.resetSession(chatID)
	retrySessionID := h.getSessionID(chatID)
	return h.agent.ChatWithSessionStream(ctx, retrySessionID, text)
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
func (h *Handler) handleChatNarrativeStream(ctx context.Context, msg *gateway.Message, text, sessionID string) error {
	// 启动 typing indicator（每 5 秒刷新一次，直到完成）
	typingCtx, typingCancel := context.WithCancel(context.Background())
	defer typingCancel()
	go h.adapter.SendTypingLoop(typingCtx, msg.Chat.ID)

	chatCtx, chatCancel := context.WithTimeout(ctx, h.effectiveChatStreamTimeout())
	defer chatCancel()

	events, err := h.openChatEventStream(chatCtx, msg.Chat.ID, text, sessionID)
	if err != nil {
		switch {
		case isTaskTimeoutError(err):
			h.sendProgressMessage(msg, "⏱ 请求超时")
		case isTaskCanceledError(err):
			h.sendProgressMessage(msg, "🛑 当前任务已停止")
		default:
			h.sendProgressMessage(msg, fmt.Sprintf("❌ Error: %s", utils.TruncateKeepLength(err.Error(), 200)))
		}
		return nil
	}

	var finalContent strings.Builder
	toolCallCount := 0
	lastProgress := ""
	var toolNarratives []string
	currentRound := 1
	var roundObservations []string
	summaryMode := h.effectiveProgressSummaryWithLLM()

	sentResult := runChatEventLoop(
		chatCtx,
		events,
		func(err error) bool {
			if errors.Is(err, context.DeadlineExceeded) {
				h.sendProgressMessage(msg, "⏱ 请求超时")
			} else {
				h.sendProgressMessage(msg, "🛑 当前任务已停止")
			}
			return true
		},
		func(evt agent.ChatEvent) (bool, bool) {
			switch evt.Type {
			case agent.ChatEventThinking:
				if summaryMode {
					if nextRound := extractRoundNumber(evt.Content); nextRound > currentRound {
						if progress := h.generateRoundProgressFeedback(chatCtx, msg, text, currentRound, roundObservations, lastProgress); progress != "" {
							h.sendProgressMessage(msg, progress)
							lastProgress = progress
						}
						roundObservations = nil
						currentRound = nextRound
					}
				} else {
					progress := humanizeThinkingProgress(evt.Content)
					if strings.TrimSpace(progress) != "" && progress != lastProgress {
						h.sendProgressMessage(msg, progress)
						lastProgress = progress
					}
				}

			case agent.ChatEventToolCall:
				toolCallCount++
				if summaryMode {
					if line := humanizeToolCall(evt.Name, evt.Args); line != "" {
						roundObservations = append(roundObservations, "Tool call: "+line)
					}
				} else {
					progress := humanizeToolCallProgress(toolCallCount, evt.Name, evt.Args)
					if strings.TrimSpace(progress) != "" && progress != lastProgress {
						h.sendProgressMessage(msg, progress)
						lastProgress = progress
					}
				}

			case agent.ChatEventToolResult:
				if h.effectiveShowToolDetailsInResult() {
					if line := humanizeToolResult(evt.Name, evt.Result); line != "" {
						toolNarratives = append(toolNarratives, line)
					}
				}
				if summaryMode {
					if line := humanizeToolResult(evt.Name, evt.Result); line != "" {
						roundObservations = append(roundObservations, "Tool result: "+line)
					}
				} else {
					progress := humanizeToolResultProgress(toolCallCount, evt.Name, evt.Result)
					if strings.TrimSpace(progress) != "" && progress != lastProgress {
						h.sendProgressMessage(msg, progress)
						lastProgress = progress
					}
				}

			case agent.ChatEventContent:
				finalContent.WriteString(evt.Content)

			case agent.ChatEventDone:
				if finalContent.Len() == 0 {
					finalContent.WriteString(evt.Content)
				}
				finalOutput := strings.TrimSpace(finalContent.String())
				if h.effectiveShowToolDetailsInResult() && finalOutput != "" {
					finalOutput = prependToolNarratives(toolNarratives, finalOutput)
				}
				h.sendFinalAssistantResponse(msg, wrapFinalConclusion(finalOutput))
				return true, true

			case agent.ChatEventError:
				if isTaskTimeoutError(evt.Err) {
					h.sendProgressMessage(msg, "⏱ 请求超时")
				} else if isTaskCanceledError(evt.Err) {
					h.sendProgressMessage(msg, "🛑 当前任务已停止")
				} else {
					errMsg := evt.Err.Error()
					if len(errMsg) > 200 {
						errMsg = errMsg[:197] + "..."
					}
					h.sendProgressMessage(msg, fmt.Sprintf("❌ Error: %s", errMsg))
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
			if h.effectiveShowToolDetailsInResult() {
				finalOutput = prependToolNarratives(toolNarratives, finalOutput)
			}
			h.sendFinalAssistantResponse(msg, wrapFinalConclusion(finalOutput))
		case errors.Is(chatCtx.Err(), context.DeadlineExceeded):
			h.sendProgressMessage(msg, "⏱ 请求超时")
		case errors.Is(chatCtx.Err(), context.Canceled):
			h.sendProgressMessage(msg, "🛑 当前任务已停止")
		default:
			h.sendProgressMessage(msg, "❌ Error: stream ended unexpectedly, please retry")
		}
	}

	return nil
}

// handleChatStream 流式对话处理（Telegram 专用）
func (h *Handler) handleChatStream(ctx context.Context, sender gateway.StreamSender, msg *gateway.Message, text, sessionID string) error {
	// 启动 typing indicator（每 5 秒刷新一次，直到完成）
	typingCtx, typingCancel := context.WithCancel(context.Background())
	defer typingCancel()
	go h.adapter.SendTypingLoop(typingCtx, msg.Chat.ID)

	chatCtx, chatCancel := context.WithTimeout(ctx, h.effectiveChatStreamTimeout())
	defer chatCancel()

	events, err := h.openChatEventStream(chatCtx, msg.Chat.ID, text, sessionID)
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
	narrativeMode := h.effectiveProgressAsMessages() && h.effectiveProgressAsNaturalLanguage()
	summaryMode := narrativeMode && h.effectiveProgressSummaryWithLLM()
	currentRound := 1
	var roundObservations []string

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
						if progress := h.generateRoundProgressFeedback(chatCtx, msg, text, currentRound, roundObservations, lastProgress); progress != "" {
							h.sendProgressMessage(msg, progress)
							lastProgress = progress
						}
						roundObservations = nil
						currentRound = nextRound
					}
				} else if h.effectiveProgressAsMessages() {
					progress := "🧠 " + clipOneLine(evt.Content, 180)
					if narrativeMode {
						progress = humanizeThinkingProgress(evt.Content)
					}
					if strings.TrimSpace(progress) != "" && progress != "🧠 " && progress != lastProgress {
						h.sendProgressMessage(msg, progress)
						lastProgress = progress
					}
				} else {
					// 兼容旧模式：在同一条消息里更新思考前缀
					sender.SetThinking(evt.Content)
				}

			case agent.ChatEventToolCall:
				toolCallCount++
				if summaryMode {
					if line := humanizeToolCall(evt.Name, evt.Args); line != "" {
						roundObservations = append(roundObservations, "Tool call: "+line)
					}
				} else if h.effectiveProgressAsMessages() {
					// 工具调用作为独立自然语言消息发送（Nanobot 风格）。
					progress := "🔧 " + humanizeToolCall(evt.Name, evt.Args)
					if narrativeMode {
						progress = humanizeToolCallProgress(toolCallCount, evt.Name, evt.Args)
					}
					h.sendProgressMessage(msg, progress)
					lastProgress = progress
				} else {
					// 兼容旧模式：显示工具调用标签
					sender.SetToolCall(evt.Name, evt.Args)
				}

			case agent.ChatEventToolResult:
				if h.effectiveShowToolDetailsInResult() {
					if line := humanizeToolResult(evt.Name, evt.Result); line != "" {
						toolNarratives = append(toolNarratives, line)
					}
				}
				if summaryMode {
					if line := humanizeToolResult(evt.Name, evt.Result); line != "" {
						roundObservations = append(roundObservations, "Tool result: "+line)
					}
				} else if h.effectiveProgressAsMessages() {
					progress := fmt.Sprintf("✅ 已完成第 %d 个步骤", toolCallCount)
					if narrativeMode {
						progress = humanizeToolResultProgress(toolCallCount, evt.Name, evt.Result)
					}
					if progress != lastProgress {
						h.sendProgressMessage(msg, progress)
						lastProgress = progress
					}
				} else {
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
				if finalContent.Len() == 0 {
					finalContent.WriteString(evt.Content)
				}
				finalOutput := finalContent.String()
				if h.effectiveShowToolDetailsInResult() {
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
		if h.effectiveShowToolDetailsInResult() && finalOutput != "" {
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
	var b strings.Builder
	b.WriteString("我刚刚先做了这些事：\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		b.WriteString("1. ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(finalOutput))
	return b.String()
}

// handleChatSync 非流式对话处理（回退方案）
func (h *Handler) handleChatSync(ctx context.Context, msg *gateway.Message, text, sessionID string) error {
	// 启动 typing indicator
	typingCtx, typingCancel := context.WithCancel(context.Background())
	defer typingCancel()
	go h.adapter.SendTypingLoop(typingCtx, msg.Chat.ID)

	response, err := h.agent.ChatWithSession(ctx, sessionID, text)
	if err != nil {
		// If session is broken, try with a fresh session
		if strings.Contains(err.Error(), "session not found") {
			h.resetSession(msg.Chat.ID)
			sessionID = h.getSessionID(msg.Chat.ID)
			response, err = h.agent.ChatWithSession(ctx, sessionID, text)
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
	if msg.Args == "" {
		// Show current model
		cfg := h.agent.Config().Get()
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("Current model: %s (provider: %s)", cfg.Model, cfg.Provider))
	}

	// Set model
	if err := h.agent.SwitchModel(msg.Args); err != nil {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Failed to switch model: %s", err.Error()))
	}

	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Switched to model: %s", msg.Args))
}

// handleSoul shows the current SOUL info.
func (h *Handler) handleSoul(ctx context.Context, msg *gateway.Message) error {
	s := h.agent.Soul()
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
	tools := h.agent.Tools()
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
	return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("🔄 Conversation reset. New session: `%s`", newID[:8]))
}

// handleHistory shows the conversation history for this chat.
func (h *Handler) handleHistory(ctx context.Context, msg *gateway.Message) error {
	sessionID := h.getSessionID(msg.Chat.ID)

	sess, ok := h.agent.Sessions().Get(sessionID)
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

// handleSession shows current session info.
func (h *Handler) handleSession(ctx context.Context, msg *gateway.Message) error {
	h.mu.RLock()
	sessionID, ok := h.sessions[msg.Chat.ID]
	h.mu.RUnlock()

	if !ok {
		return h.adapter.Send(ctx, msg.Chat.ID, "No active session. Send a message to start one!")
	}

	sess, ok := h.agent.Sessions().Get(sessionID)
	if !ok {
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("Session `%s` not found. It may have been cleaned up.", sessionID[:8]))
	}

	info := fmt.Sprintf("📋 *Session Info:*\n\n• ID: `%s`\n• Title: %s\n• Messages: %d\n• Created: %s\n• Updated: %s",
		sessionID[:8],
		sess.Title,
		sess.MessageCount(),
		sess.CreatedAt.Format("2006-01-02 15:04"),
		sess.UpdatedAt.Format("2006-01-02 15:04"),
	)

	return h.adapter.Send(ctx, msg.Chat.ID, info)
}

// handleSkills lists loaded skills.
func (h *Handler) handleSkills(ctx context.Context, msg *gateway.Message) error {
	skills := h.agent.Skills()
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

// handleCron manages scheduled tasks.
func (h *Handler) handleCron(ctx context.Context, msg *gateway.Message) error {
	engine := h.agent.CronEngine()
	args := strings.TrimSpace(msg.Args)

	if args == "" || args == "list" {
		// List all cron jobs
		jobs := engine.ListJobs()
		if len(jobs) == 0 {
			return h.adapter.Send(ctx, msg.Chat.ID, "⏰ No scheduled tasks. Use /cron add <name> <interval|cron> <prompt>")
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
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron add <id> <name> <interval_seconds> <prompt>")
		}
		id := parts[1]
		name := parts[2]
		var intervalSec int
		if _, err := fmt.Sscanf(parts[3], "%d", &intervalSec); err != nil || intervalSec <= 0 {
			return h.adapter.Send(ctx, msg.Chat.ID, "❌ Invalid interval. Must be positive integer (seconds).")
		}
		prompt := strings.Join(parts[4:], " ")
		if prompt == "" {
			prompt = name
		}

		err := engine.AddJobWithMeta(id, name, prompt, cron.IntervalSchedule{Interval: time.Duration(intervalSec) * time.Second}, func() error {
			// 执行时调用 agent chat，并把结果发回创建任务的聊天
			chatCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			response, err := h.agent.Chat(chatCtx, prompt)
			if err != nil {
				// 执行失败，通知聊天
				_ = h.adapter.Send(context.Background(), msg.Chat.ID,
					fmt.Sprintf("⏰ 定时任务 [%s] 执行失败: %s", name, utils.TruncateKeepLength(err.Error(), 200)))
				return err
			}

			// 执行成功，把 agent 回复发到聊天
			if response != "" {
				resultMsg := fmt.Sprintf("⏰ 定时任务 [%s] 执行结果:\n\n%s", name, response)
				_ = h.adapter.Send(context.Background(), msg.Chat.ID, resultMsg)
			}
			return nil
		}, map[string]string{
			"chatID":      msg.Chat.ID,
			"triggerType": "cron",
		})
		if err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ Failed to add job: %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Job `%s` added: %s (every %ds)", id, name, intervalSec))

	case "remove":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron remove <id>")
		}
		if err := engine.RemoveJob(parts[1]); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("✅ Job `%s` removed", parts[1]))

	case "pause":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron pause <id>")
		}
		if err := engine.PauseJob(parts[1]); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("⏸ Job `%s` paused", parts[1]))

	case "resume":
		if len(parts) < 2 {
			return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron resume <id>")
		}
		if err := engine.ResumeJob(parts[1]); err != nil {
			return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("❌ %s", err.Error()))
		}
		return h.adapter.Send(ctx, msg.Chat.ID, fmt.Sprintf("▶️ Job `%s` resumed", parts[1]))

	default:
		return h.adapter.Send(ctx, msg.Chat.ID, "Usage: /cron [list|add|remove|pause|resume]")
	}
}

// handleMetrics shows usage metrics.
func (h *Handler) handleMetrics(ctx context.Context, msg *gateway.Message) error {
	m := h.agent.Metrics()
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
	cronEngine := h.agent.CronEngine()
	if cronEngine.IsRunning() {
		sb.WriteString(fmt.Sprintf("• Cron Engine: ✅ Running (%d jobs)\n", cronEngine.JobCount()))
	} else {
		sb.WriteString("• Cron Engine: ❌ Stopped\n")
	}

	// Skills
	skills := h.agent.Skills()
	sb.WriteString(fmt.Sprintf("• Skills: ✅ %d loaded\n", len(skills)))

	// Sessions
	sb.WriteString("• Sessions: ✅ Active\n")

	// Memory
	mem := h.agent.Memory()
	if mem != nil {
		sb.WriteString("• Memory: ✅ Active\n")
	}

	// Metrics
	m := h.agent.Metrics()
	snapshot := m.Snapshot()
	sb.WriteString(fmt.Sprintf("• Total requests: %d\n", snapshot.TotalRequests))

	return h.adapter.Send(ctx, msg.Chat.ID, sb.String())
}

// v0.56.0: nanobot 风格内置命令

// handleNew 开启新对话（创建新会话）
func (h *Handler) handleNew(ctx context.Context, msg *gateway.Message) error {
	chatID := msg.Chat.ID

	// 创建新会话
	newSess := h.agent.Sessions().New()

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
	cfg := h.agent.Config().Get()
	sb.WriteString(fmt.Sprintf("• Model: %s\n", cfg.Model))

	// 运行时间
	uptime := time.Since(h.agent.Metrics().StartTime)
	sb.WriteString(fmt.Sprintf("• Uptime: %s\n", formatDuration(uptime)))

	// 会话历史
	sess, ok := h.agent.Sessions().Get(sessionID)
	msgCount := 0
	if ok && sess != nil {
		msgCount = sess.MessageCount()
	}
	sb.WriteString(fmt.Sprintf("• Session messages: %d\n", msgCount))

	// 指标
	m := h.agent.Metrics()
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
