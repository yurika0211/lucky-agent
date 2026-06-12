package qqofficial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/cli/profile"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/gateway"
	luckycollector "github.com/yurika0211/luckyharness/internal/gateway/collector"
	"github.com/yurika0211/luckyharness/internal/learning"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/metrics"
	"github.com/yurika0211/luckyharness/internal/rag"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/tool"
	"github.com/yurika0211/luckyharness/internal/utils"
)

type commandHandler func(ctx context.Context, msg *gateway.Message) error

type chatTask struct {
	cancel context.CancelFunc
}

type queuedChatRequest struct {
	ctx   context.Context
	msg   *gateway.Message
	input agent.UserTurnInput
}

type chatQueue struct {
	requests []*queuedChatRequest
}

type chatSessionsData struct {
	ChatSessions map[string]string `json:"chat_sessions"`
}

type sender interface {
	gateway.Gateway
	SendPhoto(ctx context.Context, chatID string, replyToMsgID string, source string, caption string) error
	SendDocument(ctx context.Context, chatID string, replyToMsgID string, source string, caption string) error
}

type typingSender interface {
	SetTyping(ctx context.Context, chatID string, userID string) error
}

type messageFeedbackSender interface {
	AcknowledgeMessage(ctx context.Context, chatID string, messageID string) error
}

type forwardedTextSender interface {
	SendForwardedText(ctx context.Context, chatID string, title string, chunks []string) error
}

type HandlerOptions struct {
	PlatformName    string
	DisplayName     string
	LogPrefix       string
	FinalAnswerOnly bool
}

type Handler struct {
	adapter  sender
	agent    *agent.Agent
	commands map[string]commandHandler
	watcher  *cron.Watcher

	mu                sync.RWMutex
	sessions          map[string]string
	tasks             map[string]*chatTask
	queues            map[string]*chatQueue
	lucky             *luckycollector.Lucky
	dataDir           string
	restarting        bool
	chatStreamTimeout time.Duration
	platformName      string
	displayName       string
	logPrefix         string
	finalAnswerOnly   bool
}

func NewHandler(adapter sender, agentRuntime *agent.Agent) *Handler {
	return NewHandlerWithOptions(adapter, agentRuntime, HandlerOptions{})
}

func NewHandlerWithOptions(adapter sender, agentRuntime *agent.Agent, opts HandlerOptions) *Handler {
	platformName := strings.TrimSpace(opts.PlatformName)
	if platformName == "" {
		platformName = "qqofficial"
	}
	displayName := strings.TrimSpace(opts.DisplayName)
	if displayName == "" {
		displayName = "QQ 网关"
	}
	logPrefix := strings.TrimSpace(opts.LogPrefix)
	if logPrefix == "" {
		logPrefix = platformName
	}

	h := &Handler{
		adapter:           adapter,
		agent:             agentRuntime,
		commands:          make(map[string]commandHandler),
		watcher:           cron.NewWatcher(resolveQQCronEngine(agentRuntime)),
		sessions:          make(map[string]string),
		tasks:             make(map[string]*chatTask),
		queues:            make(map[string]*chatQueue),
		lucky:             luckycollector.NewLucky(),
		dataDir:           "",
		chatStreamTimeout: 10 * time.Minute,
		platformName:      platformName,
		displayName:       displayName,
		logPrefix:         logPrefix,
		finalAnswerOnly:   opts.FinalAnswerOnly,
	}
	h.commands = h.buildCommandRegistry()
	return h
}

func (h *Handler) HandleMessage(ctx context.Context, msg *gateway.Message) error {
	if h == nil || h.adapter == nil || h.agent == nil || msg == nil {
		return fmt.Errorf("%s: handler not initialized", h.platform())
	}
	if msg.IsCommand {
		if handler, ok := h.commands[h.commandKey(msg.Command)]; ok {
			return handler(ctx, msg)
		}
		return h.reply(ctx, msg, "暂不支持这个命令。可用命令：/help")
	}

	if collected, err := h.collectLuckyMessageIfActive(ctx, msg); collected || err != nil {
		return err
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" && len(msg.Attachments) == 0 {
		return nil
	}
	input := h.buildUserTurnInput(ctx, text, msg.Attachments)
	return h.dispatchChatAsync(ctx, msg, input)
}

func (h *Handler) buildCommandRegistry() map[string]commandHandler {
	handlers := map[string]commandHandler{
		"start":          h.handleStart,
		"help":           h.handleHelp,
		"chat":           h.handleChatCommand,
		"lucky":          h.handleLucky,
		"review":         h.handleReview,
		"init":           h.handleInit,
		"config":         h.handleConfig,
		"version":        h.handleVersion,
		"model":          h.handleModel,
		"models":         h.handleModels,
		"soul":           h.handleSoul,
		"tools":          h.handleTools,
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
		"reset":          h.handleReset,
		"history":        h.handleHistory,
		"session":        h.handleSession,
		"sessions":       h.handleSessions,
		"resume":         h.handleResume,
		"rename":         h.handleRename,
		"new":            h.handleNew,
		"stop":           h.handleStop,
		"status":         h.handleStatus,
		"restart":        h.handleRestart,
	}
	registry := make(map[string]commandHandler, len(handlers))
	for _, name := range qqCommandNames() {
		if handler, ok := handlers[name]; ok {
			registry[name] = handler
		}
	}
	return registry
}

func (h *Handler) commandKey(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	cmd = strings.TrimPrefix(cmd, "/")
	return strings.ToLower(cmd)
}

func (h *Handler) platform() string {
	if h == nil || strings.TrimSpace(h.platformName) == "" {
		return "qqofficial"
	}
	return strings.TrimSpace(h.platformName)
}

func (h *Handler) display() string {
	if h == nil || strings.TrimSpace(h.displayName) == "" {
		return "QQ 网关"
	}
	return strings.TrimSpace(h.displayName)
}

func (h *Handler) logPrefixValue() string {
	if h == nil || strings.TrimSpace(h.logPrefix) == "" {
		return h.platform()
	}
	return strings.TrimSpace(h.logPrefix)
}

func (h *Handler) reply(ctx context.Context, msg *gateway.Message, text string) error {
	if msg != nil && strings.TrimSpace(msg.ID) != "" {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, text)
	}
	return h.adapter.Send(ctx, msg.Chat.ID, text)
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
	key := luckycollector.KeyForMessage(h.platform(), msg)
	status := collector.Status(key)
	if !status.Active {
		return false, nil
	}
	status, err := collector.Append(key, msg)
	if err != nil {
		return true, h.reply(ctx, msg, fmt.Sprintf("Lucky collection failed: %s", err.Error()))
	}
	return true, h.reply(ctx, msg, fmt.Sprintf("已收集第 %d 段（附件 %d 个）。发送 /lucky off 提交，/lucky cancel 放弃。", status.SegmentCount, status.AttachmentCount))
}

func (h *Handler) SetDataDir(dir string) {
	h.mu.Lock()
	h.dataDir = dir
	h.mu.Unlock()
	h.loadChatSessions()
}

func (h *Handler) chatSessionsPath() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if strings.TrimSpace(h.dataDir) == "" {
		return ""
	}
	return filepath.Join(h.dataDir, "chat_sessions.json")
}

func (h *Handler) loadChatSessions() {
	path := h.chatSessionsPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var csd chatSessionsData
	if err := json.Unmarshal(data, &csd); err != nil {
		fmt.Printf("[%s] warning: failed to parse chat_sessions.json: %v\n", h.logPrefixValue(), err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for chatID, sessionID := range csd.ChatSessions {
		if strings.TrimSpace(chatID) == "" || strings.TrimSpace(sessionID) == "" {
			continue
		}
		if h.agent == nil || h.agent.Sessions() == nil {
			h.sessions[chatID] = sessionID
			continue
		}
		if _, ok := h.agent.Sessions().Get(sessionID); ok {
			h.sessions[chatID] = sessionID
		}
	}
}

func (h *Handler) saveChatSessions() {
	path := h.chatSessionsPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Printf("[%s] warning: failed to create chat session dir: %v\n", h.logPrefixValue(), err)
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
		fmt.Printf("[%s] warning: failed to marshal chat_sessions: %v\n", h.logPrefixValue(), err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Printf("[%s] warning: failed to write chat_sessions.json: %v\n", h.logPrefixValue(), err)
	}
}

func (h *Handler) handleStart(ctx context.Context, msg *gateway.Message) error {
	return h.reply(ctx, msg, "已连接 LuckyHarness QQ 机器人。\n可直接发送消息开始对话，或使用 /help 查看命令。")
}

func (h *Handler) handleHelp(ctx context.Context, msg *gateway.Message) error {
	return h.reply(ctx, msg, qqHelpMessage())
}

func (h *Handler) handleChatCommand(ctx context.Context, msg *gateway.Message) error {
	if strings.TrimSpace(msg.Args) == "" {
		return h.reply(ctx, msg, "请在 /chat 后面带上要发送的内容，例如：/chat 你好")
	}
	return h.dispatchChatAsync(ctx, msg, agent.TextUserTurnInput(msg.Args))
}

func (h *Handler) handleLucky(ctx context.Context, msg *gateway.Message) error {
	action := luckycollector.ParseLuckyAction(msg.Args)
	collector := h.luckyCollector()
	key := luckycollector.KeyForMessage(h.platform(), msg)

	switch action {
	case luckycollector.LuckyActionOn:
		status, err := collector.Start(key)
		if errors.Is(err, luckycollector.ErrAlreadyActive) {
			return h.reply(ctx, msg, fmt.Sprintf("Lucky 已经在收集中：当前 %d 段，附件 %d 个。发送 /lucky off 提交，/lucky cancel 放弃。", status.SegmentCount, status.AttachmentCount))
		}
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("Lucky 开启失败：%s", err.Error()))
		}
		return h.reply(ctx, msg, "Lucky 已开启。接下来发送的多段消息会先被收集；发送 /lucky off 后再统一交给 agent。")
	case luckycollector.LuckyActionOff:
		batch, err := collector.Finish(key)
		if errors.Is(err, luckycollector.ErrInactive) {
			return h.reply(ctx, msg, "当前没有正在进行的 Lucky 收集。发送 /lucky on 开始。")
		}
		if errors.Is(err, luckycollector.ErrEmptyBatch) {
			return h.reply(ctx, msg, "没有收集到消息，已退出 Lucky 收集模式。")
		}
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("Lucky 提交失败：%s", err.Error()))
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
			return h.reply(ctx, msg, "Lucky 未开启。发送 /lucky on 开始收集多段消息。")
		}
		return h.reply(ctx, msg, fmt.Sprintf("Lucky 正在收集：%d 段，附件 %d 个。发送 /lucky off 提交，/lucky cancel 放弃。", status.SegmentCount, status.AttachmentCount))
	case luckycollector.LuckyActionCancel:
		status, ok := collector.Cancel(key)
		if !ok {
			return h.reply(ctx, msg, "当前没有正在进行的 Lucky 收集。")
		}
		return h.reply(ctx, msg, fmt.Sprintf("已取消 Lucky 收集，丢弃 %d 段消息和 %d 个附件。", status.SegmentCount, status.AttachmentCount))
	default:
		return h.reply(ctx, msg, "用法：/lucky on | /lucky off | /lucky status | /lucky cancel")
	}
}

func (h *Handler) handleReview(ctx context.Context, msg *gateway.Message) error {
	var sb strings.Builder
	sb.WriteString("工作区检查：\n")
	if wd, err := os.Getwd(); err == nil {
		sb.WriteString(fmt.Sprintf("workspace: %s\n", wd))
	}
	cfg := h.agent.Config().Get()
	sb.WriteString(fmt.Sprintf("provider: %s\n", valueOrUnset(cfg.Provider)))
	sb.WriteString(fmt.Sprintf("model: %s\n", valueOrUnset(cfg.Model)))

	sb.WriteString("\nGit status:\n")
	if status := readGitSnippet("status", "--short", "--branch"); status != "" {
		sb.WriteString(status)
		sb.WriteString("\n")
	} else {
		sb.WriteString("clean or unavailable\n")
	}

	sb.WriteString("\nRecent commits:\n")
	if log := readGitSnippet("log", "--oneline", "--decorate=short", "-n", "5"); log != "" {
		sb.WriteString(log)
		sb.WriteString("\n")
	} else {
		sb.WriteString("unavailable\n")
	}

	if sessions := h.sessionManager(); sessions != nil {
		infos := sessions.ListInfo()
		sb.WriteString(fmt.Sprintf("\nRecent sessions: %d\n", len(infos)))
		for i := 0; i < min(len(infos), 5); i++ {
			title := strings.TrimSpace(infos[i].Title)
			if title == "" {
				title = "(untitled)"
			}
			sb.WriteString(fmt.Sprintf("%s (%d msgs)\n", truncateForQQ(title, 56), infos[i].MessageCount))
		}
	}
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleInit(ctx context.Context, msg *gateway.Message) error {
	mgr := h.agent.Config()
	cfg := mgr.Get()
	var sb strings.Builder
	sb.WriteString("LuckyHarness 初始化状态：\n")
	sb.WriteString(fmt.Sprintf("home: %s\n", valueOrUnset(mgr.HomeDir())))
	sb.WriteString(fmt.Sprintf("config: %s\n", valueOrUnset(mgr.ConfigFile())))
	sb.WriteString(fmt.Sprintf("provider: %s\n", valueOrUnset(cfg.Provider)))
	sb.WriteString(fmt.Sprintf("model: %s\n", valueOrUnset(cfg.Model)))
	if cfg.APIKey == "" {
		sb.WriteString("\napi_key: 未配置。请在主机运行 lh config set api_key <key>。")
	} else {
		sb.WriteString(fmt.Sprintf("\napi_key: %s", maskSecret(cfg.APIKey)))
	}
	return h.reply(ctx, msg, sb.String())
}

func (h *Handler) handleConfig(ctx context.Context, msg *gateway.Message) error {
	args := strings.Fields(strings.TrimSpace(msg.Args))
	if len(args) == 0 || args[0] == "list" {
		return h.sendConfigList(ctx, msg)
	}
	if args[0] == "get" {
		if len(args) < 2 {
			return h.reply(ctx, msg, "用法：/config get <key>")
		}
		value, ok := h.configValue(args[1])
		if !ok {
			return h.reply(ctx, msg, fmt.Sprintf("%s 不在 QQ 配置视图里。", args[1]))
		}
		return h.reply(ctx, msg, fmt.Sprintf("%s = %s", args[1], value))
	}
	if len(args) == 1 {
		if value, ok := h.configValue(args[0]); ok {
			return h.reply(ctx, msg, fmt.Sprintf("%s = %s", args[0], value))
		}
	}
	if args[0] == "set" {
		return h.reply(ctx, msg, "QQ 里暂不开放写配置。请在主机运行 lh config set <key> <value>。")
	}
	return h.reply(ctx, msg, "用法：/config [list|get <key>]")
}

func (h *Handler) sendConfigList(ctx context.Context, msg *gateway.Message) error {
	cfg := h.agent.Config().Get()
	info := fmt.Sprintf("配置：\nprovider: %s\napi_key: %s\napi_base: %s\nmodel: %s\nsoul_path: %s\nmax_tokens: %d\ntemperature: %.1f\nserver.addr: %s\ndashboard.addr: %s\nmsg_gateway.platform: %s\nmsg_gateway.api_addr: %s\nmsg_gateway.telegram.token: %s\nmsg_gateway.qqofficial.app_id: %s\nmsg_gateway.qqofficial.sandbox: %t\nmsg_gateway.napcat.listen_addr: %s\nmsg_gateway.napcat.path: %s",
		valueOrUnset(cfg.Provider),
		maskSecret(cfg.APIKey),
		valueOrUnset(cfg.APIBase),
		valueOrUnset(cfg.Model),
		valueOrUnset(cfg.SoulPath),
		cfg.MaxTokens,
		cfg.Temperature,
		valueOrUnset(cfg.Server.Addr),
		valueOrUnset(cfg.Dashboard.Addr),
		valueOrUnset(cfg.MsgGateway.Platform),
		valueOrUnset(cfg.MsgGateway.APIAddr),
		maskSecret(cfg.MsgGateway.Telegram.Token),
		valueOrUnset(cfg.MsgGateway.QQOfficial.AppID),
		cfg.MsgGateway.QQOfficial.Sandbox,
		valueOrUnset(cfg.MsgGateway.NapCat.ListenAddr),
		valueOrUnset(cfg.MsgGateway.NapCat.Path),
	)
	return h.reply(ctx, msg, info)
}

func (h *Handler) configValue(key string) (string, bool) {
	mgr := h.agent.Config()
	cfg := mgr.Get()
	switch key {
	case "home", "home_dir":
		return valueOrUnset(mgr.HomeDir()), true
	case "config", "config_file":
		return valueOrUnset(mgr.ConfigFile()), true
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
		return valueOrUnset(cfg.Server.Addr), true
	case "dashboard.addr":
		return valueOrUnset(cfg.Dashboard.Addr), true
	case "msg_gateway.platform":
		return valueOrUnset(cfg.MsgGateway.Platform), true
	case "msg_gateway.api_addr":
		return valueOrUnset(cfg.MsgGateway.APIAddr), true
	case "msg_gateway.telegram.token":
		return maskSecret(cfg.MsgGateway.Telegram.Token), true
	case "msg_gateway.telegram.proxy":
		return valueOrUnset(cfg.MsgGateway.Telegram.Proxy), true
	case "msg_gateway.qqofficial.app_id":
		return valueOrUnset(cfg.MsgGateway.QQOfficial.AppID), true
	case "msg_gateway.qqofficial.sandbox":
		return strconv.FormatBool(cfg.MsgGateway.QQOfficial.Sandbox), true
	case "msg_gateway.napcat.listen_addr":
		return valueOrUnset(cfg.MsgGateway.NapCat.ListenAddr), true
	case "msg_gateway.napcat.path":
		return valueOrUnset(cfg.MsgGateway.NapCat.Path), true
	default:
		return "", false
	}
}

func (h *Handler) handleVersion(ctx context.Context, msg *gateway.Message) error {
	version, commit, date := buildInfo()
	info := fmt.Sprintf("LuckyHarness 版本：\nversion: %s\ncommit: %s\ndate: %s\ngo: %s\nos/arch: %s/%s",
		version,
		commit,
		date,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	)
	return h.reply(ctx, msg, info)
}

func (h *Handler) handleModels(ctx context.Context, msg *gateway.Message) error {
	if h.agent.Catalog() == nil {
		return h.reply(ctx, msg, "模型目录当前不可用。")
	}
	models := h.agent.Catalog().List()
	if len(models) == 0 {
		return h.reply(ctx, msg, "模型目录为空。")
	}

	cfg := h.agent.Config().Get()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("可用模型（%d）：\n", len(models)))
	currentProvider := ""
	limit := min(len(models), 40)
	for i := 0; i < limit; i++ {
		m := models[i]
		if m.Provider != currentProvider {
			currentProvider = m.Provider
			sb.WriteString(fmt.Sprintf("\n%s:\n", currentProvider))
		}
		marker := " "
		if m.ID == cfg.Model {
			marker = "当前"
		}
		cost := "free/local"
		if m.CostPer1kIn > 0 || m.CostPer1kOut > 0 {
			cost = fmt.Sprintf("$%.4f/$%.4f per 1k", m.CostPer1kIn, m.CostPer1kOut)
		}
		sb.WriteString(fmt.Sprintf("%s %s : %s (%s)\n", marker, m.ID, truncateForQQ(m.DisplayName, 48), cost))
	}
	if len(models) > limit {
		sb.WriteString(fmt.Sprintf("\n还有 %d 个未展示。", len(models)-limit))
	}
	sb.WriteString("\n使用 /model <id> 切换。")
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleModel(ctx context.Context, msg *gateway.Message) error {
	if strings.TrimSpace(msg.Args) == "" {
		cfg := h.agent.Config().Get()
		routerNote := ""
		if cfg.ModelRouter.Enable {
			routerNote = "\n模型路由已启用，后续请求仍可能按任务自动路由。"
		}
		return h.reply(ctx, msg, fmt.Sprintf("当前模型：%s\nProvider：%s%s", cfg.Model, cfg.Provider, routerNote))
	}
	modelID := strings.TrimSpace(msg.Args)
	providerName := ""
	if catalog := h.agent.Catalog(); catalog != nil {
		if resolved, err := catalog.ResolveProvider(modelID); err == nil {
			providerName = resolved
		}
	}
	if err := h.agent.SwitchModel(modelID); err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("切换模型失败：%s", err.Error()))
	}
	mgr := h.agent.Config()
	if providerName != "" {
		if err := mgr.Set("provider", providerName); err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("运行时已切换到 %s，但写入 provider 失败：%s", modelID, err.Error()))
		}
	}
	if err := mgr.Set("model", modelID); err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("运行时已切换到 %s，但写入 model 失败：%s", modelID, err.Error()))
	}
	if err := mgr.Save(); err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("运行时已切换到 %s，但保存配置失败：%s", modelID, err.Error()))
	}
	return h.reply(ctx, msg, fmt.Sprintf("已切换并保存模型：%s", modelID))
}

func (h *Handler) handleSoul(ctx context.Context, msg *gateway.Message) error {
	if h.agent == nil || h.agent.Soul() == nil {
		return h.reply(ctx, msg, "当前没有可用的 SOUL 配置。")
	}
	prompt := h.agent.Soul().SystemPrompt()
	if len(prompt) > 500 {
		prompt = prompt[:500] + "..."
	}
	return h.reply(ctx, msg, fmt.Sprintf("当前 SOUL：\n\n%s", prompt))
}

func (h *Handler) handleTools(ctx context.Context, msg *gateway.Message) error {
	reg := h.agent.Tools()
	if reg == nil {
		return h.reply(ctx, msg, "当前没有可用的工具注册表。")
	}
	allTools := reg.List()
	if len(allTools) == 0 {
		return h.reply(ctx, msg, "当前没有可用工具。")
	}

	var sb strings.Builder
	sb.WriteString("可用工具：\n")
	for i, t := range allTools {
		if i >= 20 {
			sb.WriteString(fmt.Sprintf("\n... 还有 %d 个工具未展示", len(allTools)-20))
			break
		}
		status := "启用"
		if !t.Enabled {
			status = "禁用"
		}
		sb.WriteString(fmt.Sprintf("%s：%s，%s\n", status, t.Name, t.Description))
	}
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleReset(ctx context.Context, msg *gateway.Message) error {
	newID := h.resetSession(msg.Chat.ID)
	shortID := shortSessionID(newID)
	return h.reply(ctx, msg, fmt.Sprintf("会话已重置，新会话：%s", shortID))
}

func (h *Handler) handleSession(ctx context.Context, msg *gateway.Message) error {
	if strings.TrimSpace(msg.Args) != "" {
		return h.handleSessionSwitch(ctx, msg, strings.TrimSpace(msg.Args))
	}

	h.mu.RLock()
	sessionID, ok := h.sessions[msg.Chat.ID]
	h.mu.RUnlock()
	if !ok {
		return h.reply(ctx, msg, "当前还没有活跃会话，先发一条消息试试。")
	}
	sessions := h.agent.Sessions()
	if sessions == nil {
		return h.reply(ctx, msg, "会话管理器当前不可用。")
	}
	sess, ok := sessions.Get(sessionID)
	if !ok {
		return h.reply(ctx, msg, fmt.Sprintf("会话 %s 未找到，可能已过期。", shortSessionID(sessionID)))
	}
	info := fmt.Sprintf(
		"当前会话：%s\n标题：%s\n消息数：%d\n创建时间：%s\n更新时间：%s",
		shortSessionID(sessionID),
		sess.Title,
		sess.MessageCount(),
		sess.CreatedAt.Format("2006-01-02 15:04"),
		sess.UpdatedAt.Format("2006-01-02 15:04"),
	)
	return h.reply(ctx, msg, info)
}

func (h *Handler) handleSessions(ctx context.Context, msg *gateway.Message) error {
	sessions := h.sessionManager()
	if sessions == nil {
		return h.reply(ctx, msg, "会话管理器当前不可用。")
	}
	infos := sessions.ListInfo()
	if len(infos) == 0 {
		return h.reply(ctx, msg, "当前还没有会话，先发一条消息试试。")
	}

	currentID := h.currentSessionID(msg.Chat.ID)
	var sb strings.Builder
	sb.WriteString("最近会话：\n")
	limit := min(len(infos), 10)
	for i := 0; i < limit; i++ {
		info := infos[i]
		marker := " "
		if info.ID == currentID {
			marker = "当前"
		}
		title := strings.TrimSpace(info.Title)
		if title == "" {
			title = "(untitled)"
		}
		sb.WriteString(fmt.Sprintf("%s %s : %s (%d msgs, updated %s)\n",
			marker,
			info.ID,
			truncateForQQ(title, 48),
			info.MessageCount,
			info.UpdatedAt.Format("01-02 15:04"),
		))
	}
	if len(infos) > limit {
		sb.WriteString(fmt.Sprintf("\n还有 %d 个未展示。", len(infos)-limit))
	}
	sb.WriteString("\n使用 /resume <title|id> 切换。")
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleResume(ctx context.Context, msg *gateway.Message) error {
	query := strings.TrimSpace(msg.Args)
	if query == "" {
		return h.reply(ctx, msg, "用法：/resume <title|id>")
	}
	return h.handleSessionSwitch(ctx, msg, query)
}

func (h *Handler) handleRename(ctx context.Context, msg *gateway.Message) error {
	title := strings.TrimSpace(msg.Args)
	if title == "" {
		return h.reply(ctx, msg, "用法：/rename <title>")
	}
	sessions := h.sessionManager()
	if sessions == nil {
		return h.reply(ctx, msg, "会话管理器当前不可用。")
	}
	sessionID := h.getSessionID(msg.Chat.ID)
	sess, ok := sessions.Get(sessionID)
	if !ok || sess == nil {
		return h.reply(ctx, msg, "当前会话未找到。")
	}
	sess.SetTitle(title)
	if err := sess.Save(); err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("重命名失败：%s", err.Error()))
	}
	return h.reply(ctx, msg, fmt.Sprintf("当前会话已重命名为：%s", truncateForQQ(title, 120)))
}

func (h *Handler) handleSessionSwitch(ctx context.Context, msg *gateway.Message, sessionQuery string) error {
	sessions := h.sessionManager()
	if sessions == nil {
		return h.reply(ctx, msg, "会话管理器当前不可用。")
	}

	result := findSessionByIDTitleOrPrefix(sessions, sessionQuery)
	switch result.status {
	case sessionLookupMatched:
	case sessionLookupAmbiguous:
		return h.reply(ctx, msg, formatAmbiguousSessionSwitchMessage(sessionQuery, result.matches))
	default:
		return h.reply(ctx, msg, fmt.Sprintf("没找到会话：%s。发送 /sessions 查看最近会话。", sessionQuery))
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
		return h.reply(ctx, msg, fmt.Sprintf("已经在这个会话：%s\n标题：%s", shortSessionID(matchedID), truncateForQQ(title, 60)))
	}
	return h.reply(ctx, msg, fmt.Sprintf("已切换会话：%s\n标题：%s\n消息数：%d",
		shortSessionID(matchedID),
		truncateForQQ(title, 80),
		sess.MessageCount(),
	))
}

func (h *Handler) handleHistory(ctx context.Context, msg *gateway.Message) error {
	sessionID := h.getSessionID(msg.Chat.ID)
	sessions := h.agent.Sessions()
	if sessions == nil {
		return h.reply(ctx, msg, "当前无法读取会话历史。")
	}
	sess, ok := sessions.Get(sessionID)
	if !ok {
		return h.reply(ctx, msg, "当前没有可用的会话历史。")
	}
	messages := sess.GetMessages()
	if len(messages) == 0 {
		return h.reply(ctx, msg, "这个会话里还没有消息。")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("最近消息（共 %d 条）：\n", len(messages)))
	start := 0
	if len(messages) > 20 {
		start = len(messages) - 20
		sb.WriteString(fmt.Sprintf("(仅展示最近 %d 条)\n", len(messages)-start))
	}
	for i := start; i < len(messages); i++ {
		role := messages[i].Role
		content := strings.TrimSpace(messages[i].Content)
		if len(content) > 80 {
			content = content[:80] + "..."
		}
		if content == "" {
			content = "(空内容)"
		}
		sb.WriteString(fmt.Sprintf("[%s] %s\n", role, content))
	}
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleSkills(ctx context.Context, msg *gateway.Message) error {
	skills := h.agent.Skills()
	if len(skills) == 0 {
		return h.reply(ctx, msg, "当前没有加载技能。")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("已加载技能（%d）：\n", len(skills)))
	for i, s := range skills {
		if i >= 30 {
			sb.WriteString(fmt.Sprintf("\n... 还有 %d 个技能未展示", len(skills)-30))
			break
		}
		desc := s.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s：%s\n", s.Name, desc))
	}
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleMCP(ctx context.Context, msg *gateway.Message) error {
	parts := strings.Fields(msg.Args)
	if len(parts) < 2 {
		return h.reply(ctx, msg, "用法：/mcp <name> <url> [api_key]")
	}
	apiKey := ""
	if len(parts) > 2 {
		apiKey = parts[2]
	}
	h.agent.ConnectMCPServer(parts[0], parts[1], apiKey)
	return h.reply(ctx, msg, fmt.Sprintf("已连接 MCP server：%s (%s)", parts[0], parts[1]))
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
		return h.reply(ctx, msg, "用法：/approve <tool> 或 /deny <tool>")
	}
	reg := h.agent.Tools()
	if reg == nil {
		return h.reply(ctx, msg, "当前没有可用的工具注册表。")
	}
	if err := reg.SetPermissionOverride(name, perm); err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("设置失败：%s", err.Error()))
	}
	return h.reply(ctx, msg, fmt.Sprintf("工具 %s 已设为 %s。", name, label))
}

func (h *Handler) handleCron(ctx context.Context, msg *gateway.Message) error {
	engine := h.agent.CronEngine()
	if engine == nil {
		return h.reply(ctx, msg, "当前没有可用的定时任务引擎。")
	}
	args := strings.TrimSpace(msg.Args)

	if args == "" || args == "list" {
		jobs := engine.ListJobs()
		if len(jobs) == 0 {
			return h.reply(ctx, msg, "当前没有定时任务。用法：/cron add <id> <schedule> <prompt>")
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("定时任务（%d）：\n", len(jobs)))
		for _, job := range jobs {
			sb.WriteString(fmt.Sprintf("%s [%s] %s | 运行次数：%d\n", job.ID, cronStatusLabel(job.Status), cron.DescribeSchedule(job.Schedule), job.RunCount))
		}
		return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
	}

	parts := strings.Fields(args)
	if len(parts) < 1 {
		return h.reply(ctx, msg, "用法：/cron [list|add|remove|pause|resume|start|stop]")
	}

	switch parts[0] {
	case "add":
		if len(parts) < 4 {
			return h.reply(ctx, msg, "用法：/cron add <id> <schedule> <prompt>")
		}
		id := parts[1]
		scheduleText := parts[2]
		command := strings.Join(parts[3:], " ")
		if strings.TrimSpace(command) == "" {
			return h.reply(ctx, msg, "缺少要执行的 prompt。")
		}
		reg := h.agent.Tools()
		if reg == nil {
			return h.reply(ctx, msg, "当前没有可用的 cron 工具。")
		}
		sessionID := h.getSessionID(msg.Chat.ID)
		resp, err := reg.Call("cron_add", map[string]any{
			"id":                  id,
			"schedule":            scheduleText,
			"mode":                "agent",
			"command":             command,
			"platform":            h.platform(),
			"chat_id":             msg.Chat.ID,
			"reply_to_message_id": msg.ID,
			"session_id":          sessionID,
		})
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("添加任务失败：%s", err.Error()))
		}

		var out struct {
			ID       string `json:"id"`
			Schedule string `json:"schedule"`
		}
		if json.Unmarshal([]byte(resp), &out) == nil && strings.TrimSpace(out.ID) != "" {
			return h.reply(ctx, msg, fmt.Sprintf("已添加定时任务：%s\n计划：%s", out.ID, out.Schedule))
		}
		return h.reply(ctx, msg, "已添加定时任务。")

	case "remove":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/cron remove <id>")
		}
		reg := h.agent.Tools()
		if reg == nil {
			return h.reply(ctx, msg, "当前没有可用的 cron 工具。")
		}
		if _, err := reg.Call("cron_remove", map[string]any{"id": parts[1]}); err != nil {
			return h.reply(ctx, msg, err.Error())
		}
		return h.reply(ctx, msg, fmt.Sprintf("已删除定时任务：%s", parts[1]))

	case "pause":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/cron pause <id>")
		}
		reg := h.agent.Tools()
		if reg == nil {
			return h.reply(ctx, msg, "当前没有可用的 cron 工具。")
		}
		if _, err := reg.Call("cron_pause", map[string]any{"id": parts[1]}); err != nil {
			return h.reply(ctx, msg, err.Error())
		}
		return h.reply(ctx, msg, fmt.Sprintf("已暂停定时任务：%s", parts[1]))

	case "resume":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/cron resume <id>")
		}
		reg := h.agent.Tools()
		if reg == nil {
			return h.reply(ctx, msg, "当前没有可用的 cron 工具。")
		}
		if _, err := reg.Call("cron_resume", map[string]any{"id": parts[1]}); err != nil {
			return h.reply(ctx, msg, err.Error())
		}
		return h.reply(ctx, msg, fmt.Sprintf("已恢复定时任务：%s", parts[1]))

	case "start":
		engine.Start()
		return h.reply(ctx, msg, "Cron engine 已启动。")

	case "stop":
		engine.Stop()
		return h.reply(ctx, msg, "Cron engine 已停止。")
	}

	return h.reply(ctx, msg, "用法：/cron [list|add|remove|pause|resume|start|stop]")
}

func (h *Handler) handleWatch(ctx context.Context, msg *gateway.Message) error {
	watcher := h.watcher
	if watcher == nil {
		return h.reply(ctx, msg, "Watch runtime 当前不可用。")
	}

	args := strings.TrimSpace(msg.Args)
	if args == "" || args == "list" {
		patterns := watcher.ListPatterns()
		if len(patterns) == 0 {
			return h.reply(ctx, msg, "当前没有 watch pattern。用法：/watch add <id> <glob> <interval>")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Watch patterns（%d）：\n", len(patterns)))
		for _, p := range patterns {
			lastCheck := "N/A"
			if !p.LastCheck.IsZero() {
				lastCheck = p.LastCheck.Format("2006-01-02 15:04:05")
			}
			sb.WriteString(fmt.Sprintf("%s : %s\ninterval: %s | last: %s | result: %s\n",
				p.ID,
				truncateForQQ(p.Pattern, 80),
				p.Interval,
				lastCheck,
				valueOrUnset(p.LastResult),
			))
		}
		return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
	}

	parts := strings.Fields(args)
	if len(parts) == 0 {
		return h.reply(ctx, msg, "用法：/watch [list|add|remove|start|stop]")
	}
	switch parts[0] {
	case "add":
		if len(parts) < 4 {
			return h.reply(ctx, msg, "用法：/watch add <id> <glob> <interval>\n示例：/watch add logs logs/*.log 1m")
		}
		interval, err := time.ParseDuration(parts[3])
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("interval 无效：%s", err.Error()))
		}
		id := parts[1]
		pattern := parts[2]
		if err := watcher.AddPattern(id, "Watch: "+id, pattern, pattern, interval, nil); err != nil {
			return h.reply(ctx, msg, err.Error())
		}
		return h.reply(ctx, msg, fmt.Sprintf("已添加 watch：%s\npattern: %s\ninterval: %s", id, pattern, interval))
	case "remove":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/watch remove <id>")
		}
		if err := watcher.RemovePattern(parts[1]); err != nil {
			return h.reply(ctx, msg, err.Error())
		}
		return h.reply(ctx, msg, fmt.Sprintf("已删除 watch：%s", parts[1]))
	case "start":
		watcher.Start()
		return h.reply(ctx, msg, "Watcher 已启动。")
	case "stop":
		watcher.Stop()
		return h.reply(ctx, msg, "Watcher 已停止。")
	default:
		return h.reply(ctx, msg, "用法：/watch [list|add|remove|start|stop]")
	}
}

func (h *Handler) handleDashboard(ctx context.Context, msg *gateway.Message) error {
	parts := strings.Fields(strings.TrimSpace(msg.Args))
	subcmd := "status"
	if len(parts) > 0 {
		subcmd = parts[0]
	}
	cfg := h.agent.Config().Get()
	addr := cfg.Dashboard.Addr
	if strings.TrimSpace(addr) == "" {
		addr = ":8765"
	}
	switch subcmd {
	case "status", "list", "":
		return h.reply(ctx, msg, fmt.Sprintf("Dashboard：\nconfigured addr: %s\nurl hint: %s\n\nstart/stop 属于本机进程管理，请在主机运行 lh dashboard start 或 lh dashboard stop。", addr, dashboardURLHint(addr)))
	case "start", "stop":
		return h.reply(ctx, msg, "QQ 里不直接启停 Dashboard。请在主机运行 lh dashboard start 或 lh dashboard stop。")
	default:
		return h.reply(ctx, msg, "用法：/dashboard [status]")
	}
}

func (h *Handler) handleMsgGateway(ctx context.Context, msg *gateway.Message) error {
	parts := strings.Fields(strings.TrimSpace(msg.Args))
	subcmd := "status"
	if len(parts) > 0 {
		subcmd = parts[0]
	}
	cfg := h.agent.Config().Get()
	switch subcmd {
	case "status", "":
		info := fmt.Sprintf("Message Gateway：\nplatform: %s\nstart_all: %t\napi_addr: %s\ntelegram.token: %s\ntelegram.proxy: %s\nqqofficial.app_id: %s\nqqofficial.sandbox: %t\nnapcat.listen_addr: %s\nnapcat.path: %s\nweixin.account_id: %s\nopenclawweixin.account_id: %s\n\n运行状态需要看启动该网关的主机终端。",
			valueOrUnset(cfg.MsgGateway.Platform),
			cfg.MsgGateway.StartAll,
			valueOrUnset(cfg.MsgGateway.APIAddr),
			maskSecret(cfg.MsgGateway.Telegram.Token),
			valueOrUnset(cfg.MsgGateway.Telegram.Proxy),
			valueOrUnset(cfg.MsgGateway.QQOfficial.AppID),
			cfg.MsgGateway.QQOfficial.Sandbox,
			valueOrUnset(cfg.MsgGateway.NapCat.ListenAddr),
			valueOrUnset(cfg.MsgGateway.NapCat.Path),
			valueOrUnset(cfg.MsgGateway.Weixin.AccountID),
			valueOrUnset(cfg.MsgGateway.OpenClawWeixin.AccountID),
		)
		return h.reply(ctx, msg, info)
	case "start", "stop":
		return h.reply(ctx, msg, "QQ 里不直接启停 Message Gateway。请在主机终端运行 lh msg-gateway start，并用 Ctrl+C 停止。")
	default:
		return h.reply(ctx, msg, "用法：/msg_gateway [status]")
	}
}

func (h *Handler) handleRAG(ctx context.Context, msg *gateway.Message) error {
	ragMgr := h.agent.RAG()
	if ragMgr == nil {
		return h.reply(ctx, msg, "RAG 系统当前不可用。")
	}
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
			return h.reply(ctx, msg, "知识库为空。")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("已索引文档（%d）：\n", len(ids)))
		limit := min(len(ids), 25)
		for i := 0; i < limit; i++ {
			id := ids[i]
			doc, ok := ragMgr.GetDocument(id)
			if !ok || doc == nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("%s : %s (%d chunks)\n", shortSessionID(id), truncateForQQ(doc.Title, 72), len(doc.Chunks)))
		}
		if len(ids) > limit {
			sb.WriteString(fmt.Sprintf("\n还有 %d 个未展示。", len(ids)-limit))
		}
		return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
	case "search", "query":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/rag search <query>")
		}
		query := strings.Join(parts[1:], " ")
		results, err := ragMgr.Search(ctx, query)
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("检索失败：%s", err.Error()))
		}
		if len(results) == 0 {
			return h.reply(ctx, msg, "没有匹配结果。")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("RAG 检索结果（%d）：\n", len(results)))
		limit := min(len(results), 8)
		for i := 0; i < limit; i++ {
			res := results[i]
			sb.WriteString(fmt.Sprintf("%d. score %.2f | %s\n%s\n\n", i+1, res.Score, truncateForQQ(res.DocTitle, 64), truncateForQQ(res.Content, 180)))
		}
		return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
	case "remove":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/rag remove <doc_id>")
		}
		if ragMgr.RemoveDocument(parts[1]) {
			return h.reply(ctx, msg, fmt.Sprintf("已删除文档：%s", parts[1]))
		}
		return h.reply(ctx, msg, fmt.Sprintf("没有找到文档：%s", parts[1]))
	case "index":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/rag index <file_or_directory_path>")
		}
		path := strings.Join(parts[1:], " ")
		info, err := os.Stat(path)
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("路径不存在：%s", err.Error()))
		}
		if info.IsDir() {
			docs, err := ragMgr.IndexDirectory(path)
			if err != nil {
				return h.reply(ctx, msg, fmt.Sprintf("目录索引失败：%s", err.Error()))
			}
			return h.reply(ctx, msg, fmt.Sprintf("已索引目录：%s\n文档数：%d", path, len(docs)))
		}
		doc, err := ragMgr.IndexFile(path)
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("文件索引失败：%s", err.Error()))
		}
		return h.reply(ctx, msg, fmt.Sprintf("已索引文件：%s\nchunks: %d", doc.Title, len(doc.Chunks)))
	default:
		return h.reply(ctx, msg, "用法：/rag [stats|store|list|search|remove|index]")
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
	return h.reply(ctx, msg, fmt.Sprintf("RAG 统计：\ndocuments: %d\nchunks: %d\nstore: %s\nvectors: %d",
		stats.DocumentCount, stats.ChunkCount, store, vectorCount))
}

func (h *Handler) sendRAGStore(ctx context.Context, msg *gateway.Message, ragMgr *rag.RAGManager) error {
	if ragMgr.IsSQLite() {
		sqlStore := ragMgr.SQLiteStore()
		if sqlStore == nil {
			return h.reply(ctx, msg, "SQLite store 当前不可用。")
		}
		count, dbSize, err := sqlStore.Stats()
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("store 统计失败：%s", err.Error()))
		}
		return h.reply(ctx, msg, fmt.Sprintf("SQLite RAG store：\npath: %s\nvectors: %d\nsize: %d bytes\ndimension: %d",
			sqlStore.Path(), count, dbSize, sqlStore.Dimension()))
	}
	store := ragMgr.Store()
	if store == nil {
		return h.reply(ctx, msg, "RAG store 当前不可用。")
	}
	return h.reply(ctx, msg, fmt.Sprintf("Memory RAG store：\nvectors: %d\ndimension: %d", store.Len(), store.Dimension()))
}

func (h *Handler) handleLearn(ctx context.Context, msg *gateway.Message) error {
	var sb strings.Builder
	sb.WriteString("LuckyHarness Learning Mode\n\n")
	sb.WriteString("命令：\n")
	sb.WriteString("/learn_start lh-agent-systems : start or resume the built-in project course\n")
	sb.WriteString("/learn_current : show the current module\n")
	sb.WriteString("/learn_lab : show the current lab\n")
	sb.WriteString("/learn_submit <evidence> : submit lab evidence and advance\n")
	sb.WriteString("/learn_progress : show course progress\n\n")
	sb.WriteString("Courses:\n")
	for _, course := range learning.BuiltinCourses() {
		sb.WriteString(fmt.Sprintf("%s : %s (%d modules)\n", course.ID, course.Title, len(course.Modules)))
	}
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleLearnStart(ctx context.Context, msg *gateway.Message) error {
	courseID := strings.TrimSpace(msg.Args)
	if courseID == "" {
		return h.reply(ctx, msg, "用法：/learn_start <course>\n示例：/learn_start lh-agent-systems")
	}
	course, ok := learning.FindCourse(courseID)
	if !ok {
		return h.reply(ctx, msg, fmt.Sprintf("未知课程：%s\n发送 /learn 查看课程列表。", courseID))
	}
	store, err := h.learningStore()
	if err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("Learning store 不可用：%s", err.Error()))
	}
	cp, err := store.StartCourse(course)
	if err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("课程启动失败：%s", err.Error()))
	}
	module, _ := course.ModuleByID(cp.CurrentModule)
	return h.reply(ctx, msg, fmt.Sprintf("Learning started：\ncourse: %s\ncurrent: %s : %s\nprogress: %s\n\n发送 /learn_lab 打开第一个 lab。",
		course.Title, module.ID, module.Title, store.Path()))
}

func (h *Handler) handleLearnCurrent(ctx context.Context, msg *gateway.Message) error {
	course, cp, store, err := h.activeLearningState()
	if err != nil {
		return h.reply(ctx, msg, err.Error())
	}
	module, ok := course.ModuleByID(cp.CurrentModule)
	if !ok {
		return h.reply(ctx, msg, fmt.Sprintf("当前 module 未找到：%s", cp.CurrentModule))
	}
	done, total := learning.CourseCompletion(course, cp)
	return h.reply(ctx, msg, fmt.Sprintf("当前学习模块：\ncourse: %s\nmodule: %s : %s\nobjective: %s\ncompletion: %d/%d\nprogress: %s",
		course.Title, module.ID, module.Title, module.Objective, done, total, store.Path()))
}

func (h *Handler) handleLearnLab(ctx context.Context, msg *gateway.Message) error {
	course, cp, _, err := h.activeLearningState()
	if err != nil {
		return h.reply(ctx, msg, err.Error())
	}
	module, ok := course.ModuleByID(cp.CurrentModule)
	if !ok {
		return h.reply(ctx, msg, fmt.Sprintf("当前 module 未找到：%s", cp.CurrentModule))
	}
	return h.reply(ctx, msg, formatQQLearningLab(module))
}

func (h *Handler) handleLearnSubmit(ctx context.Context, msg *gateway.Message) error {
	evidence := strings.TrimSpace(msg.Args)
	if evidence == "" {
		return h.reply(ctx, msg, "用法：/learn_submit <evidence>")
	}
	course, _, store, err := h.activeLearningState()
	if err != nil {
		return h.reply(ctx, msg, err.Error())
	}
	cp, mp, err := store.SubmitEvidence(course, evidence, true)
	if err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("提交 evidence 失败：%s", err.Error()))
	}
	var sb strings.Builder
	sb.WriteString("Learning evidence accepted\n")
	sb.WriteString(fmt.Sprintf("module: %s\n", mp.ModuleID))
	sb.WriteString(fmt.Sprintf("attempts: %d\n", mp.Attempts))
	if cp.CompletedAt != nil {
		sb.WriteString(fmt.Sprintf("\n课程已完成：%s", course.Title))
		return h.reply(ctx, msg, sb.String())
	}
	next, _ := course.ModuleByID(cp.CurrentModule)
	sb.WriteString(fmt.Sprintf("\nNext: %s : %s\n发送 /learn_lab 继续。", next.ID, next.Title))
	return h.reply(ctx, msg, sb.String())
}

func (h *Handler) handleLearnProgress(ctx context.Context, msg *gateway.Message) error {
	course, cp, store, err := h.activeLearningState()
	if err != nil {
		return h.reply(ctx, msg, err.Error())
	}
	done, total := learning.CourseCompletion(course, cp)
	var sb strings.Builder
	sb.WriteString("Learning progress\n")
	sb.WriteString(fmt.Sprintf("course: %s\n", course.Title))
	sb.WriteString(fmt.Sprintf("completion: %d/%d\n\n", done, total))
	for _, module := range course.Modules {
		mp := cp.Modules[module.ID]
		status := mp.Status
		if status == "" {
			status = "pending"
		}
		sb.WriteString(fmt.Sprintf("%s : %s (attempts=%d)\n", module.ID, status, mp.Attempts))
	}
	sb.WriteString(fmt.Sprintf("\nprogress: %s", store.Path()))
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) learningStore() (*learning.ProgressStore, error) {
	home := ""
	if h.agent != nil && h.agent.Config() != nil {
		home = strings.TrimSpace(h.agent.Config().HomeDir())
	}
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
		return learning.Course{}, learning.CourseProgress{}, nil, fmt.Errorf("没有 active course，请先发送 /learn_start lh-agent-systems")
	}
	course, ok := learning.FindCourse(progress.ActiveCourseID)
	if !ok {
		return learning.Course{}, learning.CourseProgress{}, nil, fmt.Errorf("active course %s 未安装", progress.ActiveCourseID)
	}
	cp, ok := progress.Courses[progress.ActiveCourseID]
	if !ok {
		return learning.Course{}, learning.CourseProgress{}, nil, fmt.Errorf("active course %s 没有 progress", progress.ActiveCourseID)
	}
	return course, cp, store, nil
}

func formatQQLearningLab(module learning.Module) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Lab: %s\n\n", module.Lab.ID))
	sb.WriteString(fmt.Sprintf("module: %s : %s\n", module.ID, module.Title))
	sb.WriteString(fmt.Sprintf("prompt: %s\n", module.Lab.Prompt))
	if len(module.Concepts) > 0 {
		sb.WriteString(fmt.Sprintf("concepts: %s\n", strings.Join(module.Concepts, ", ")))
	}
	if len(module.Lab.AgentRoles) > 0 {
		sb.WriteString(fmt.Sprintf("agent roles: %s\n", strings.Join(module.Lab.AgentRoles, ", ")))
	}
	if len(module.Lab.Commands) > 0 {
		sb.WriteString("\ncommands:\n")
		for _, c := range module.Lab.Commands {
			sb.WriteString(c)
			sb.WriteByte('\n')
		}
	}
	if len(module.Lab.Evidence) > 0 {
		sb.WriteString("\nevidence:\n")
		for _, e := range module.Lab.Evidence {
			sb.WriteString(e)
			sb.WriteByte('\n')
		}
	}
	if len(module.Rubric) > 0 {
		sb.WriteString("\nrubric:\n")
		for _, r := range module.Rubric {
			sb.WriteString(r)
			sb.WriteByte('\n')
		}
	}
	sb.WriteString(fmt.Sprintf("\ndeliverable: %s\n", module.Lab.Deliverable))
	sb.WriteString("\nSubmit with /learn_submit <evidence>.")
	return strings.TrimSpace(sb.String())
}

func (h *Handler) handleRemember(ctx context.Context, msg *gateway.Message) error {
	content := strings.TrimSpace(msg.Args)
	if content == "" {
		return h.reply(ctx, msg, "用法：/remember <content>")
	}
	if h.agent.Memory() == nil {
		return h.reply(ctx, msg, "Memory runtime 当前不可用。")
	}
	if err := h.agent.Remember(content, "user"); err != nil {
		return h.reply(ctx, msg, err.Error())
	}
	return h.reply(ctx, msg, "已保存到中期记忆。")
}

func (h *Handler) handleRememberLong(ctx context.Context, msg *gateway.Message) error {
	content := strings.TrimSpace(msg.Args)
	if content == "" {
		return h.reply(ctx, msg, "用法：/remember_long <content>")
	}
	if h.agent.Memory() == nil {
		return h.reply(ctx, msg, "Memory runtime 当前不可用。")
	}
	if err := h.agent.RememberLongTerm(content, "user"); err != nil {
		return h.reply(ctx, msg, err.Error())
	}
	return h.reply(ctx, msg, "已保存到长期记忆。")
}

func (h *Handler) handleRecall(ctx context.Context, msg *gateway.Message) error {
	query := strings.TrimSpace(msg.Args)
	if query == "" {
		return h.reply(ctx, msg, "用法：/recall <query>")
	}
	if h.agent.Memory() == nil {
		return h.reply(ctx, msg, "Memory runtime 当前不可用。")
	}
	results := h.agent.Recall(query)
	if len(results) == 0 {
		return h.reply(ctx, msg, "没有匹配的记忆。")
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("记忆检索结果（%d）：\n", len(results)))
	limit := min(len(results), 10)
	for i := 0; i < limit; i++ {
		e := results[i]
		sb.WriteString(fmt.Sprintf("%d. %s [%s] %.2f\n%s\n\n",
			i+1, shortSessionID(e.ID), e.Tier.String(), e.Importance, truncateForQQ(e.Content, 180)))
	}
	if len(results) > limit {
		sb.WriteString(fmt.Sprintf("还有 %d 条未展示。", len(results)-limit))
	}
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleMemStats(ctx context.Context, msg *gateway.Message) error {
	if h.agent.Memory() == nil {
		return h.reply(ctx, msg, "Memory runtime 当前不可用。")
	}
	stats := h.agent.MemoryStats()
	total := stats[memory.TierShort] + stats[memory.TierMedium] + stats[memory.TierLong]
	return h.reply(ctx, msg, fmt.Sprintf("Memory stats：\nshort: %d\nmedium: %d\nlong: %d\ntotal: %d",
		stats[memory.TierShort], stats[memory.TierMedium], stats[memory.TierLong], total))
}

func (h *Handler) handleMemDecay(ctx context.Context, msg *gateway.Message) error {
	if h.agent.Memory() == nil {
		return h.reply(ctx, msg, "Memory runtime 当前不可用。")
	}
	deleted := h.agent.DecayMemory(0.05)
	return h.reply(ctx, msg, fmt.Sprintf("已衰减 %d 条低权重记忆。", deleted))
}

func (h *Handler) handlePromote(ctx context.Context, msg *gateway.Message) error {
	id := strings.TrimSpace(msg.Args)
	if id == "" {
		return h.reply(ctx, msg, "用法：/promote <memory_id>")
	}
	if h.agent.Memory() == nil {
		return h.reply(ctx, msg, "Memory runtime 当前不可用。")
	}
	if err := h.agent.PromoteMemory(id); err != nil {
		return h.reply(ctx, msg, err.Error())
	}
	return h.reply(ctx, msg, fmt.Sprintf("已提升记忆：%s", id))
}

func (h *Handler) handleProfile(ctx context.Context, msg *gateway.Message) error {
	home := h.agent.Config().HomeDir()
	if strings.TrimSpace(home) == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("定位 home dir 失败：%s", err.Error()))
		}
		home = filepath.Join(userHome, ".luckyharness")
	}
	mgr, err := profile.NewManager(home)
	if err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("Profile manager 不可用：%s", err.Error()))
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
			return h.reply(ctx, msg, "当前没有 profile。")
		}
		var sb strings.Builder
		sb.WriteString("Profiles：\n")
		for _, info := range infos {
			marker := " "
			if info.Active {
				marker = "当前"
			}
			sb.WriteString(fmt.Sprintf("%s %s : %s/%s\n", marker, info.Name, valueOrUnset(info.Provider), valueOrUnset(info.Model)))
		}
		sb.WriteString("\n使用 /profile switch <name> 切换下次启动使用的 active profile。")
		return h.reply(ctx, msg, sb.String())
	case "switch":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/profile switch <name>")
		}
		if err := mgr.Switch(parts[1]); err != nil {
			return h.reply(ctx, msg, err.Error())
		}
		return h.reply(ctx, msg, fmt.Sprintf("已切换 active profile：%s。下次启动生效。", parts[1]))
	default:
		return h.reply(ctx, msg, "用法：/profile [list|switch <name>]")
	}
}

func (h *Handler) handleContext(ctx context.Context, msg *gateway.Message) error {
	cw := h.agent.ContextWindow()
	if cw == nil {
		return h.reply(ctx, msg, "Context runtime 当前不可用。")
	}
	cfg := cw.Config()
	cacheStats := h.agent.ContextCacheStats()
	return h.reply(ctx, msg, fmt.Sprintf("Context window：\nmax tokens: %d\nreserved: %d\navailable: %d\nstrategy: %s\nsliding window: %d\nmax turns: %d\nmemory budget: %d\nsummary threshold: %.0f%%\n\nContext cache：\nentries: %v\nhits: %v\nmisses: %v",
		cfg.MaxTokens,
		cfg.ReservedTokens,
		cfg.MaxTokens-cfg.ReservedTokens,
		cfg.Strategy.String(),
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
	reg := h.agent.Tools()
	if reg == nil {
		return h.reply(ctx, msg, "当前没有可用的工具注册表。")
	}
	parts := strings.Fields(strings.TrimSpace(msg.Args))
	subcmd := "tools"
	if len(parts) > 0 {
		subcmd = parts[0]
	}
	switch subcmd {
	case "tools", "list":
		enabled := reg.ListEnabled()
		if len(enabled) == 0 {
			return h.reply(ctx, msg, "当前没有可用的 function-calling 工具。")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Function tools（%d）：\n", len(enabled)))
		limit := min(len(enabled), 25)
		for i := 0; i < limit; i++ {
			t := enabled[i]
			sb.WriteString(fmt.Sprintf("%s [%s] : %s\n", t.Name, t.Permission.String(), truncateForQQ(t.Description, 90)))
		}
		if len(enabled) > limit {
			sb.WriteString(fmt.Sprintf("\n还有 %d 个未展示。", len(enabled)-limit))
		}
		return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
	case "history":
		return h.reply(ctx, msg, "Function-calling history 存在会话历史里。可以用 /history 或 /sessions 查看。")
	case "clear":
		return h.reply(ctx, msg, "Function-calling transient history 已清理。")
	default:
		return h.reply(ctx, msg, "用法：/fc [tools|history|clear]")
	}
}

func (h *Handler) handleEmbedder(ctx context.Context, msg *gateway.Message) error {
	reg := h.agent.EmbedderRegistry()
	if reg == nil {
		return h.reply(ctx, msg, "Embedder registry 当前不可用。")
	}
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
			return h.reply(ctx, msg, "当前没有注册 embedder。")
		}
		var sb strings.Builder
		sb.WriteString("Embedders：\n")
		for _, info := range list {
			marker := " "
			if info.Active {
				marker = "当前"
			}
			sb.WriteString(fmt.Sprintf("%s %s : %s/%s dim=%d\n", marker, info.ID, info.Name, info.Model, info.Dimension))
		}
		return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
	case "switch":
		if subarg == "" {
			return h.reply(ctx, msg, "用法：/embedder switch <id>")
		}
		if !reg.Switch(subarg) {
			return h.reply(ctx, msg, fmt.Sprintf("没有找到 embedder：%s", subarg))
		}
		active := reg.Active()
		if active == nil {
			return h.reply(ctx, msg, fmt.Sprintf("已切换 embedder：%s", subarg))
		}
		return h.reply(ctx, msg, fmt.Sprintf("已切换 embedder：%s (%s/%s dim=%d)", subarg, active.Name(), active.Model(), active.Dimension()))
	case "test":
		text := subarg
		if text == "" {
			text = "Hello, world!"
		}
		active := reg.Active()
		if active == nil {
			return h.reply(ctx, msg, "当前没有 active embedder。")
		}
		vec, err := active.Embed(ctx, text)
		if err != nil {
			return h.reply(ctx, msg, fmt.Sprintf("embed 失败：%s", err.Error()))
		}
		sampleLen := min(len(vec), 5)
		return h.reply(ctx, msg, fmt.Sprintf("Embedder test：\nmodel: %s/%s\ndim: %d\ninput: %q\nfirst %d values: %v",
			active.Name(), active.Model(), len(vec), text, sampleLen, vec[:sampleLen]))
	default:
		return h.reply(ctx, msg, "用法：/embedder [list|switch|test]")
	}
}

func (h *Handler) handleMetrics(ctx context.Context, msg *gateway.Message) error {
	m := h.agent.Metrics()
	if m == nil {
		return h.reply(ctx, msg, "当前没有可用的指标收集器。")
	}
	snapshot := m.Snapshot()
	info := fmt.Sprintf(
		"运行指标：\n总请求数：%d\n聊天请求：%d\n工具调用：%d\n错误数：%d\n运行时长：%s",
		snapshot.TotalRequests,
		snapshot.ChatRequests,
		snapshot.ToolCalls,
		snapshot.ErrorRequests,
		snapshot.Uptime,
	)
	return h.reply(ctx, msg, info)
}

func (h *Handler) handleHealth(ctx context.Context, msg *gateway.Message) error {
	var sb strings.Builder
	sb.WriteString("系统健康状态：\n")
	sb.WriteString("Agent：运行中\n")

	if engine := h.agent.CronEngine(); engine != nil {
		if engine.IsRunning() {
			sb.WriteString(fmt.Sprintf("Cron：运行中（%d 个任务）\n", engine.JobCount()))
		} else {
			sb.WriteString("Cron：未运行\n")
		}
	} else {
		sb.WriteString("Cron：不可用\n")
	}

	sb.WriteString(fmt.Sprintf("Skills：%d 个已加载\n", len(h.agent.Skills())))

	if h.agent.Memory() != nil {
		sb.WriteString("Memory：可用\n")
	} else {
		sb.WriteString("Memory：不可用\n")
	}

	if m := h.agent.Metrics(); m != nil {
		snapshot := m.Snapshot()
		sb.WriteString(fmt.Sprintf("Total requests：%d\n", snapshot.TotalRequests))
	}

	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleNew(ctx context.Context, msg *gateway.Message) error {
	sessions := h.agent.Sessions()
	if sessions == nil {
		return h.reply(ctx, msg, "会话管理器当前不可用。")
	}

	newSess := sessions.New()

	h.mu.Lock()
	oldSessionID, hadOld := h.sessions[msg.Chat.ID]
	h.sessions[msg.Chat.ID] = newSess.ID
	h.mu.Unlock()
	h.saveChatSessions()

	info := ""
	if hadOld {
		info = fmt.Sprintf("旧会话：%s\n", oldSessionID)
	}
	return h.reply(ctx, msg, fmt.Sprintf("已开启新会话。\n%s新会话 ID：%s", info, newSess.ID))
}

func (h *Handler) handleStop(ctx context.Context, msg *gateway.Message) error {
	if !h.cancelChatTask(msg.Chat.ID) {
		return h.reply(ctx, msg, "当前没有运行中的任务。")
	}
	return h.reply(ctx, msg, "已停止当前任务。")
}

func (h *Handler) handleStatus(ctx context.Context, msg *gateway.Message) error {
	sessionID := h.getSessionID(msg.Chat.ID)

	var sb strings.Builder
	sb.WriteString("LuckyHarness 状态：\n")

	cfg := h.agent.Config().Get()
	sb.WriteString(fmt.Sprintf("Model：%s\n", cfg.Model))

	metricsVal := h.agent.Metrics()
	if metricsVal != nil {
		uptime := time.Since(metricsVal.StartTime)
		sb.WriteString(fmt.Sprintf("Uptime：%s\n", utils.FormatDurationCompact(uptime)))
		snapshot := metricsVal.Snapshot()
		sb.WriteString(fmt.Sprintf("Total requests：%d\n", snapshot.TotalRequests))
	}

	if sessions := h.agent.Sessions(); sessions != nil {
		if sess, ok := sessions.Get(sessionID); ok && sess != nil {
			sb.WriteString(fmt.Sprintf("Session messages：%d\n", sess.MessageCount()))
		}
	}

	running, queued := h.queueStatus(msg.Chat.ID)
	if running {
		sb.WriteString("Current task：running\n")
	} else {
		sb.WriteString("Current task：idle\n")
	}
	sb.WriteString(fmt.Sprintf("Queue pending：%d", queued))

	return h.reply(ctx, msg, sb.String())
}

func (h *Handler) handleRestart(ctx context.Context, msg *gateway.Message) error {
	h.mu.Lock()
	if h.restarting {
		h.mu.Unlock()
		return h.reply(ctx, msg, h.display()+"正在重启中，请稍候。")
	}
	h.restarting = true
	h.mu.Unlock()

	_ = h.reply(ctx, msg, "正在重启 "+h.display()+"...")

	go func(chatID string, replyTo *gateway.Message) {
		defer func() {
			h.mu.Lock()
			h.restarting = false
			h.mu.Unlock()
		}()

		h.cancelChatTask(chatID)

		if err := h.adapter.Stop(); err != nil {
			fmt.Printf("[%s] restart stop failed: %v\n", h.logPrefixValue(), err)
		}
		time.Sleep(1200 * time.Millisecond)

		if err := h.adapter.Start(context.Background()); err != nil {
			fmt.Printf("[%s] restart start failed: %v\n", h.logPrefixValue(), err)
			if replyTo != nil {
				_ = h.reply(context.Background(), replyTo, fmt.Sprintf("%s重启失败：%v", h.display(), err))
			}
			return
		}
		if replyTo != nil {
			_ = h.reply(context.Background(), replyTo, h.display()+"已重连并恢复接收消息。")
		}
	}(msg.Chat.ID, msg)

	return nil
}

func (h *Handler) dispatchChatAsync(ctx context.Context, msg *gateway.Message, input agent.UserTurnInput) error {
	input = input.Normalize()
	if strings.TrimSpace(input.RoutingText) == "" && strings.TrimSpace(input.Message.Content) == "" {
		return nil
	}
	input = qqInputWithMediaDeliveryGuidance(input)
	h.acknowledgeIncomingMessage(msg)

	req := &queuedChatRequest{
		ctx:   ctx,
		msg:   msg,
		input: input,
	}
	position, startWorker := h.enqueueChatRequest(msg.Chat.ID, req)
	if position > 1 {
		h.notifyQueued(msg, position-1)
	}
	if startWorker {
		go h.runChatQueue(msg.Chat.ID)
	}
	return nil
}

func (h *Handler) acknowledgeIncomingMessage(msg *gateway.Message) {
	feedback, ok := h.adapter.(messageFeedbackSender)
	if !ok || msg == nil || msg.Chat.Type != gateway.ChatGroup || strings.TrimSpace(msg.ID) == "" {
		return
	}
	go func(chatID, messageID string) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := feedback.AcknowledgeMessage(ctx, chatID, messageID); err != nil {
			fmt.Printf("[%s] warning: failed to acknowledge message: %v\n", h.logPrefixValue(), err)
		}
	}(msg.Chat.ID, msg.ID)
}

func qqInputWithMediaDeliveryGuidance(input agent.UserTurnInput) agent.UserTurnInput {
	input = input.Normalize()
	if strings.TrimSpace(input.RoutingText) == "" && strings.TrimSpace(input.Message.Content) == "" {
		return input
	}
	input.RoutingText = qqMediaDeliveryGuidance(input.RoutingText)
	input.Message.Content = input.RoutingText
	input.Message.ContentParts = nil
	return input.Normalize()
}

func (h *Handler) enqueueChatRequest(chatID string, req *queuedChatRequest) (position int, startWorker bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	q := h.queues[chatID]
	if q == nil {
		q = &chatQueue{}
		h.queues[chatID] = q
	}
	q.requests = append(q.requests, req)
	position = len(q.requests)
	startWorker = h.tasks[chatID] == nil
	return position, startWorker
}

func (h *Handler) dequeueChatRequest(chatID string) (*queuedChatRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	q := h.queues[chatID]
	if q == nil || len(q.requests) == 0 {
		delete(h.queues, chatID)
		return nil, false
	}

	req := q.requests[0]
	q.requests = q.requests[1:]
	if len(q.requests) == 0 {
		delete(h.queues, chatID)
	}
	return req, true
}

func (h *Handler) runChatQueue(chatID string) {
	for {
		req, ok := h.dequeueChatRequest(chatID)
		if !ok {
			return
		}

		taskCtx, task := h.beginChatTask(chatID, req.ctx)
		err := h.handleChatStream(taskCtx, req.msg, req.input)
		h.finishChatTask(chatID, task)

		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Printf("[%s] chat error: %v\n", h.logPrefixValue(), err)
			_ = h.reply(context.Background(), req.msg, fmt.Sprintf("处理消息时出错：%v", err))
		}
	}
}

func (h *Handler) notifyQueued(msg *gateway.Message, ahead int) {
	text := fmt.Sprintf("消息已加入队列，前面还有 %d 条任务。", ahead)
	sendCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.reply(sendCtx, msg, text)
}

func (h *Handler) beginChatTask(chatID string, parent context.Context) (context.Context, *chatTask) {
	ctx, cancel := context.WithCancel(parent)

	h.mu.Lock()
	defer h.mu.Unlock()
	task := &chatTask{cancel: cancel}
	h.tasks[chatID] = task
	return ctx, task
}

func (h *Handler) finishChatTask(chatID string, task *chatTask) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.tasks[chatID]; ok && cur == task {
		delete(h.tasks, chatID)
	}
}

func (h *Handler) cancelChatTask(chatID string) bool {
	h.mu.Lock()
	task, ok := h.tasks[chatID]
	if ok {
		delete(h.tasks, chatID)
	}
	h.mu.Unlock()

	if !ok || task == nil || task.cancel == nil {
		return false
	}
	task.cancel()
	return true
}

func (h *Handler) queueStatus(chatID string) (running bool, queued int) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.tasks[chatID] != nil {
		running = true
	}
	if q := h.queues[chatID]; q != nil {
		queued = len(q.requests)
	}
	return running, queued
}

func (h *Handler) handleChatSync(ctx context.Context, msg *gateway.Message, input agent.UserTurnInput) error {
	input = qqInputWithMediaDeliveryGuidance(input)
	if strings.TrimSpace(input.RoutingText) == "" && strings.TrimSpace(input.Message.Content) == "" {
		return nil
	}

	sessionID := h.getSessionID(msg.Chat.ID)
	response, err := h.agent.ChatWithSessionInput(ctx, sessionID, input)
	if err != nil && strings.Contains(err.Error(), "session not found") {
		sessionID = h.resetSession(msg.Chat.ID)
		response, err = h.agent.ChatWithSessionInput(ctx, sessionID, input)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		return err
	}

	response = strings.TrimSpace(response)
	if response == "" {
		response = "我这边暂时还没有整理出可发送的结果。"
	}
	return h.sendAssistantResponse(ctx, msg, response)
}

func (h *Handler) buildUserTurnInput(ctx context.Context, baseText string, attachments []gateway.Attachment) agent.UserTurnInput {
	baseText = strings.TrimSpace(baseText)
	if len(attachments) == 0 {
		return agent.TextUserTurnInput(baseText)
	}
	return agent.MultimodalUserTurnInput(h.composeAttachmentInput(ctx, baseText, attachments), attachments)
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
		label := string(att.Type)
		if strings.TrimSpace(label) == "" {
			label = "attachment"
		}
		name := strings.TrimSpace(att.FileName)
		if name == "" {
			name = "unnamed"
		}
		mediaDesc.WriteString(fmt.Sprintf("%s %d: %s (mime: %s)\n", strings.Title(label), i+1, name, att.MimeType))
	}
	sections = append(sections, strings.TrimSpace(mediaDesc.String()))
	return strings.Join(sections, "\n\n")
}

func (h *Handler) openChatEventStream(ctx context.Context, chatID string, input agent.UserTurnInput, sessionID string) (<-chan agent.ChatEvent, error) {
	events, err := h.agent.ChatWithSessionStreamInput(ctx, sessionID, input)
	if err == nil {
		return events, nil
	}
	if !strings.Contains(err.Error(), "session not found") {
		return nil, err
	}
	h.resetSession(chatID)
	retrySessionID := h.getSessionID(chatID)
	return h.agent.ChatWithSessionStreamInput(ctx, retrySessionID, input)
}

func (h *Handler) sendProgress(ctx context.Context, msg *gateway.Message, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	_ = h.reply(ctx, msg, text)
}

func (h *Handler) sendAssistantResponse(ctx context.Context, msg *gateway.Message, response string) error {
	if h.adapter == nil || msg == nil {
		return fmt.Errorf("%s: adapter or message is nil", h.platform())
	}

	text, media, err := resolveOutboundMediaResponse(response)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) != "" {
		if err := h.sendAssistantText(ctx, msg, text); err != nil {
			return err
		}
	}
	for _, item := range media {
		switch item.Kind {
		case outboundMediaPhoto:
			if err := h.adapter.SendPhoto(ctx, msg.Chat.ID, msg.ID, item.Source, item.Caption); err != nil {
				return err
			}
		case outboundMediaDocument:
			if err := h.adapter.SendDocument(ctx, msg.Chat.ID, msg.ID, item.Source, item.Caption); err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(text) == "" && len(media) == 0 {
		return h.reply(ctx, msg, "我这边暂时还没有整理出可发送的结果。")
	}
	return nil
}

func (h *Handler) sendAssistantText(ctx context.Context, msg *gateway.Message, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if qqRuneLen(text) <= qqLongMessageForwardThreshold {
		return h.reply(ctx, msg, text)
	}
	forwarder, ok := h.adapter.(forwardedTextSender)
	if !ok {
		return h.reply(ctx, msg, text)
	}
	chunks := splitQQMessageChunks(text, qqForwardNodeChunkLimit)
	if len(chunks) == 0 {
		return h.reply(ctx, msg, text)
	}
	if err := forwarder.SendForwardedText(ctx, msg.Chat.ID, h.forwardedTextTitle(), chunks); err != nil {
		fmt.Printf("[%s] warning: failed to send forwarded long message: %v\n", h.logPrefixValue(), err)
		return h.reply(ctx, msg, text)
	}
	return nil
}

func (h *Handler) forwardedTextTitle() string {
	name := strings.TrimSpace(h.displayName)
	if name == "" {
		name = "LuckyHarness"
	}
	return name
}

func (h *Handler) sendAssistantResponseWithTrace(ctx context.Context, msg *gateway.Message, response string, trace *qqProgressTrace) error {
	if err := h.sendAssistantResponse(ctx, msg, response); err != nil {
		return err
	}
	if err := h.sendProgressTrace(ctx, msg, trace); err != nil {
		fmt.Printf("[%s] warning: failed to send progress trace: %v\n", h.logPrefixValue(), err)
	}
	return nil
}

func (h *Handler) sendProgressTrace(ctx context.Context, msg *gateway.Message, trace *qqProgressTrace) error {
	if trace == nil {
		return nil
	}
	text := trace.Message()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	for _, chunk := range splitQQMessageChunks(text, qqProgressTraceChunkLimit) {
		if err := h.reply(ctx, msg, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) handleChatStream(ctx context.Context, msg *gateway.Message, input agent.UserTurnInput) error {
	if h.agent == nil {
		return fmt.Errorf("%s: agent not initialized", h.platform())
	}
	input = input.Normalize()
	if strings.TrimSpace(input.RoutingText) == "" && strings.TrimSpace(input.Message.Content) == "" {
		return nil
	}

	sessionID := h.getSessionID(msg.Chat.ID)
	h.agent.RecordRecentChatTarget(h.platform(), msg.Chat.ID, msg.ID)

	chatCtx, chatCancel := context.WithTimeout(ctx, h.chatStreamTimeout)
	defer chatCancel()
	stopTyping := h.startTypingIndicator(chatCtx, msg)
	defer stopTyping()

	events, err := h.openChatEventStream(chatCtx, msg.Chat.ID, input, sessionID)
	if err != nil {
		if isTaskTimeoutError(err) {
			h.sendProgress(ctx, msg, "⏱ 请求超时")
			return nil
		}
		if isTaskCanceledError(err) {
			h.sendProgress(ctx, msg, "🛑 当前任务已停止")
			return nil
		}
		return err
	}

	return h.handleChatEventStream(chatCtx, msg, events)
}

func (h *Handler) startTypingIndicator(ctx context.Context, msg *gateway.Message) context.CancelFunc {
	typing, ok := h.adapter.(typingSender)
	if !ok || msg == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		sendTyping := func() {
			sendCtx, sendCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer sendCancel()
			_ = typing.SetTyping(sendCtx, msg.Chat.ID, msg.Sender.ID)
		}
		sendTyping()
		ticker := time.NewTicker(6 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sendTyping()
			}
		}
	}()
	return cancel
}

func (h *Handler) handleChatEventStream(chatCtx context.Context, msg *gateway.Message, events <-chan agent.ChatEvent) error {
	var finalContent strings.Builder
	currentRound := 1
	var trace *qqProgressTrace
	if !h.finalAnswerOnly {
		trace = newQQProgressTrace()
	}

	for {
		select {
		case <-chatCtx.Done():
			if errors.Is(chatCtx.Err(), context.DeadlineExceeded) {
				h.sendProgress(context.Background(), msg, "⏱ 请求超时")
			} else {
				h.sendProgress(context.Background(), msg, "🛑 当前任务已停止")
			}
			return nil
		case evt, ok := <-events:
			if !ok {
				out := strings.TrimSpace(finalContent.String())
				if out == "" {
					out = "我这边暂时还没有整理出可发送的结果。"
				}
				if h.finalAnswerOnly {
					return h.sendAssistantResponse(context.Background(), msg, out)
				}
				return h.sendAssistantResponseWithTrace(context.Background(), msg, out, trace)
			}
			switch evt.Type {
			case agent.ChatEventThinking:
				if nextRound := extractQQRoundNumber(evt.Content); nextRound > currentRound {
					currentRound = nextRound
				}
				if h.finalAnswerOnly {
					continue
				}
				trace.AddThinking(evt.Content, currentRound)
				h.sendProgress(context.Background(), msg, qqThinkingMessage(evt.Content, currentRound))
			case agent.ChatEventToolCall:
				if h.finalAnswerOnly {
					continue
				}
				trace.AddToolCall(evt.Name, evt.Args)
				h.sendProgress(context.Background(), msg, qqToolCallMessage(evt.Name, evt.Args))
			case agent.ChatEventToolResult:
				if h.finalAnswerOnly {
					continue
				}
				result := qqToolResultText(evt)
				trace.AddToolResult(evt.Name, result)
				h.sendProgress(context.Background(), msg, qqToolResultMessage(evt.Name, result))
			case agent.ChatEventContent:
				finalContent.WriteString(evt.Content)
			case agent.ChatEventDone:
				if evt.Content != "" {
					finalContent.Reset()
					finalContent.WriteString(evt.Content)
				}
				out := strings.TrimSpace(finalContent.String())
				if out == "" {
					out = "我这边暂时还没有整理出可发送的结果。"
				}
				if h.finalAnswerOnly {
					return h.sendAssistantResponse(context.Background(), msg, out)
				}
				return h.sendAssistantResponseWithTrace(context.Background(), msg, out, trace)
			case agent.ChatEventError:
				if isTaskTimeoutError(evt.Err) {
					h.sendProgress(context.Background(), msg, "⏱ 请求超时")
					return nil
				}
				if isTaskCanceledError(evt.Err) {
					h.sendProgress(context.Background(), msg, "🛑 当前任务已停止")
					return nil
				}
				return h.reply(context.Background(), msg, fmt.Sprintf("处理消息时出错：%v", evt.Err))
			}
		}
	}
}

func extractQQRoundNumber(thinking string) int {
	var round int
	if _, err := fmt.Sscanf(strings.TrimSpace(thinking), "Thinking... (round %d)", &round); err == nil && round > 0 {
		return round
	}
	return 0
}

func qqToolResultText(evt agent.ChatEvent) string {
	if strings.TrimSpace(evt.Result) != "" {
		return evt.Result
	}
	return evt.Content
}

func qqThinkingMessage(raw string, round int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if round > 1 {
			return fmt.Sprintf("我继续往下处理了，现在在看第 %d 轮结果。", round)
		}
		return "我先帮你看一下这个问题。"
	}
	if round > 1 {
		if extractQQRoundNumber(raw) > 0 || strings.EqualFold(raw, "Thinking...") {
			return fmt.Sprintf("我继续往下处理了，现在在看第 %d 轮结果。", round)
		}
		return fmt.Sprintf("我继续往下处理了，现在在看第 %d 轮：%s", round, raw)
	}
	return "我先帮你看一下这个问题。"
}

func qqToolCallMessage(name, args string) string {
	name = strings.TrimSpace(name)
	args = strings.TrimSpace(args)
	if len(args) > 120 {
		args = args[:117] + "..."
	}
	if args == "" {
		return fmt.Sprintf("我刚调了一下 %s 这个工具。", name)
	}
	return fmt.Sprintf("我刚调了一下 %s 这个工具，参数大概是：%s", name, args)
}

func qqToolResultMessage(name, result string) string {
	name = strings.TrimSpace(name)
	result = strings.TrimSpace(result)
	if len(result) > 180 {
		result = result[:177] + "..."
	}
	if result == "" {
		return fmt.Sprintf("%s 已经返回结果了。", name)
	}
	return fmt.Sprintf("%s 这边已经有结果了，我先记下来：%s", name, result)
}

const (
	qqProgressTraceMaxEntries     = 18
	qqProgressTraceChunkLimit     = 1800
	qqLongMessageForwardThreshold = 1200
	qqForwardNodeChunkLimit       = 1200
)

type qqProgressTrace struct {
	entries   []string
	seen      map[string]struct{}
	hasTool   bool
	truncated bool
}

func newQQProgressTrace() *qqProgressTrace {
	return &qqProgressTrace{
		seen: make(map[string]struct{}),
	}
}

func (t *qqProgressTrace) AddThinking(_ string, round int) {
	if t == nil {
		return
	}
	if round > 1 {
		t.add(fmt.Sprintf("进入第 %d 轮整理和校验。", round))
		return
	}
	t.add("开始分析用户请求。")
}

func (t *qqProgressTrace) AddToolCall(name, args string) {
	if t == nil {
		return
	}
	name = strings.TrimSpace(name)
	args = qqCompactTraceText(args, 180)
	t.hasTool = true
	if name == "" {
		name = "unknown"
	}
	if args == "" {
		t.add(fmt.Sprintf("调用工具 %s。", name))
		return
	}
	t.add(fmt.Sprintf("调用工具 %s，参数摘要：%s", name, args))
}

func (t *qqProgressTrace) AddToolResult(name, result string) {
	if t == nil {
		return
	}
	name = strings.TrimSpace(name)
	result = qqCompactTraceText(result, 220)
	t.hasTool = true
	if name == "" {
		name = "unknown"
	}
	if result == "" {
		t.add(fmt.Sprintf("工具 %s 返回完成。", name))
		return
	}
	t.add(fmt.Sprintf("工具 %s 返回摘要：%s", name, result))
}

func (t *qqProgressTrace) add(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if _, ok := t.seen[line]; ok {
		return
	}
	if len(t.entries) >= qqProgressTraceMaxEntries {
		t.truncated = true
		return
	}
	t.seen[line] = struct{}{}
	t.entries = append(t.entries, line)
}

func (t *qqProgressTrace) Message() string {
	if t == nil || len(t.entries) == 0 {
		return ""
	}
	if !t.hasTool && len(t.entries) <= 1 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("本轮公开执行轨迹：\n")
	for i, entry := range t.entries {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, entry))
	}
	if t.truncated {
		sb.WriteString(fmt.Sprintf("%d. 后续步骤已省略，只保留关键轨迹。\n", len(t.entries)+1))
	}
	sb.WriteString("\n说明：这是可公开的进度和工具摘要，不包含模型隐藏推理。")
	return strings.TrimSpace(sb.String())
}

func qqCompactTraceText(text string, maxLen int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	return utils.TruncateKeepLength(text, maxLen)
}

func splitQQMessageChunks(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if limit <= 0 || qqRuneLen(text) <= limit {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	for _, line := range strings.Split(text, "\n") {
		for qqRuneLen(line) > limit {
			if strings.TrimSpace(current.String()) != "" {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			head, tail := splitRunes(line, limit)
			chunks = append(chunks, strings.TrimSpace(head))
			line = tail
		}

		extra := qqRuneLen(line)
		if current.Len() > 0 {
			extra++
		}
		if current.Len() > 0 && qqRuneLen(current.String())+extra > limit {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}
	if strings.TrimSpace(current.String()) != "" {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}
	return chunks
}

func splitRunes(text string, limit int) (string, string) {
	runes := []rune(text)
	if limit >= len(runes) {
		return text, ""
	}
	return string(runes[:limit]), string(runes[limit:])
}

func qqRuneLen(text string) int {
	return len([]rune(text))
}

func isTaskTimeoutError(err error) bool {
	return err != nil && (errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout"))
}

func isTaskCanceledError(err error) bool {
	return err != nil && errors.Is(err, context.Canceled)
}

func (h *Handler) getSessionID(chatID string) string {
	h.mu.RLock()
	if sid, ok := h.sessions[chatID]; ok && strings.TrimSpace(sid) != "" {
		h.mu.RUnlock()
		return sid
	}
	h.mu.RUnlock()

	sessions := h.sessionManager()
	if sessions == nil {
		return h.platform() + ":" + chatID
	}
	sess := sessions.New()

	h.mu.Lock()
	h.sessions[chatID] = sess.ID
	h.mu.Unlock()
	h.saveChatSessions()
	return sess.ID
}

func (h *Handler) resetSession(chatID string) string {
	sessions := h.sessionManager()
	if sessions == nil {
		return h.platform() + ":" + chatID
	}
	sess := sessions.New()

	h.mu.Lock()
	h.sessions[chatID] = sess.ID
	h.mu.Unlock()
	h.saveChatSessions()
	return sess.ID
}

func (h *Handler) sessionManager() *session.Manager {
	if h == nil || h.agent == nil {
		return nil
	}
	return h.agent.Sessions()
}

func resolveQQCronEngine(a *agent.Agent) *cron.Engine {
	if a == nil {
		return nil
	}
	return a.CronEngine()
}

func (h *Handler) currentSessionID(chatID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[chatID]
}

func shortSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	return sessionID
}

func truncateForQQ(value string, limit int) string {
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
	sb.WriteString(fmt.Sprintf("有多个会话匹配：%s\n\n", query))
	limit := min(len(matches), 5)
	for i := 0; i < limit; i++ {
		title := strings.TrimSpace(matches[i].Title)
		if title == "" {
			title = "(untitled)"
		}
		sb.WriteString(fmt.Sprintf("%s : %s (%d msgs)\n",
			matches[i].ID,
			truncateForQQ(title, 56),
			matches[i].MessageCount,
		))
	}
	if len(matches) > limit {
		sb.WriteString(fmt.Sprintf("\n还有 %d 个匹配项。", len(matches)-limit))
	}
	sb.WriteString("\n\n请用 /resume <id> 精确切换，或先重命名其中一个会话。")
	return sb.String()
}

func cronStatusLabel(status cron.JobStatus) string {
	switch status {
	case cron.StatusRunning:
		return "running"
	case cron.StatusPaused:
		return "paused"
	case cron.StatusFailed:
		return "failed"
	case cron.StatusDone:
		return "done"
	default:
		return "idle"
	}
}

var _ = (*metrics.Metrics)(nil)
var _ = (*tool.Registry)(nil)
