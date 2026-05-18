package qqofficial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/metrics"
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

type Handler struct {
	adapter  *Adapter
	agent    *agent.Agent
	commands map[string]commandHandler

	mu                sync.RWMutex
	sessions          map[string]string
	tasks             map[string]*chatTask
	queues            map[string]*chatQueue
	dataDir           string
	restarting        bool
	chatStreamTimeout time.Duration
}

func NewHandler(adapter *Adapter, agentRuntime *agent.Agent) *Handler {
	h := &Handler{
		adapter:           adapter,
		agent:             agentRuntime,
		commands:          make(map[string]commandHandler),
		sessions:          make(map[string]string),
		tasks:             make(map[string]*chatTask),
		queues:            make(map[string]*chatQueue),
		dataDir:           "",
		chatStreamTimeout: 10 * time.Minute,
	}
	h.commands = h.buildCommandRegistry()
	return h
}

func (h *Handler) HandleMessage(ctx context.Context, msg *gateway.Message) error {
	if h == nil || h.adapter == nil || h.agent == nil || msg == nil {
		return fmt.Errorf("qqofficial: handler not initialized")
	}
	if msg.IsCommand {
		if handler, ok := h.commands[h.commandKey(msg.Command)]; ok {
			return handler(ctx, msg)
		}
		return h.reply(ctx, msg, "暂不支持这个命令。可用命令：/help")
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" && len(msg.Attachments) == 0 {
		return nil
	}
	input := h.buildUserTurnInput(ctx, text, msg.Attachments)
	return h.dispatchChatAsync(ctx, msg, input)
}

func (h *Handler) buildCommandRegistry() map[string]commandHandler {
	return map[string]commandHandler{
		"start":   h.handleStart,
		"help":    h.handleHelp,
		"chat":    h.handleChatCommand,
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

func (h *Handler) commandKey(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	cmd = strings.TrimPrefix(cmd, "/")
	return strings.ToLower(cmd)
}

func (h *Handler) reply(ctx context.Context, msg *gateway.Message, text string) error {
	if msg != nil && strings.TrimSpace(msg.ID) != "" {
		return h.adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, text)
	}
	return h.adapter.Send(ctx, msg.Chat.ID, text)
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
		fmt.Printf("[qqofficial] warning: failed to parse chat_sessions.json: %v\n", err)
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
		fmt.Printf("[qqofficial] warning: failed to create chat session dir: %v\n", err)
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
		fmt.Printf("[qqofficial] warning: failed to marshal chat_sessions: %v\n", err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Printf("[qqofficial] warning: failed to write chat_sessions.json: %v\n", err)
	}
}

func (h *Handler) handleStart(ctx context.Context, msg *gateway.Message) error {
	return h.reply(ctx, msg, "已连接 LuckyHarness QQ 机器人。\n可直接发送消息开始对话，或使用 /help 查看命令。")
}

func (h *Handler) handleHelp(ctx context.Context, msg *gateway.Message) error {
	help := `可用命令：
/help - 查看帮助
/chat <消息> - 显式发起对话
/model [模型] - 查看或切换模型
/soul - 查看当前 SOUL
/tools - 查看可用工具
/skills - 查看已加载技能
/cron [list|add|remove|pause|resume] - 管理定时任务
/metrics - 查看运行指标
/health - 查看系统健康状态
/status - 查看当前运行状态
/new - 开启新会话
/reset - 重置当前会话
/stop - 停止当前任务
/restart - 重启 QQ 网关
/session - 查看当前会话
/history - 查看最近会话历史

也可以直接发送普通消息开始对话。`
	return h.reply(ctx, msg, help)
}

func (h *Handler) handleChatCommand(ctx context.Context, msg *gateway.Message) error {
	if strings.TrimSpace(msg.Args) == "" {
		return h.reply(ctx, msg, "请在 /chat 后面带上要发送的内容，例如：/chat 你好")
	}
	return h.dispatchChatAsync(ctx, msg, agent.TextUserTurnInput(msg.Args))
}

func (h *Handler) handleModel(ctx context.Context, msg *gateway.Message) error {
	if strings.TrimSpace(msg.Args) == "" {
		cfg := h.agent.Config().Get()
		return h.reply(ctx, msg, fmt.Sprintf("当前模型：%s\nProvider：%s", cfg.Model, cfg.Provider))
	}
	if err := h.agent.SwitchModel(strings.TrimSpace(msg.Args)); err != nil {
		return h.reply(ctx, msg, fmt.Sprintf("切换模型失败：%s", err.Error()))
	}
	return h.reply(ctx, msg, fmt.Sprintf("已切换到模型：%s", strings.TrimSpace(msg.Args)))
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
		sb.WriteString(fmt.Sprintf("- [%s] %s：%s\n", status, t.Name, t.Description))
	}
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
}

func (h *Handler) handleReset(ctx context.Context, msg *gateway.Message) error {
	newID := h.resetSession(msg.Chat.ID)
	shortID := shortSessionID(newID)
	return h.reply(ctx, msg, fmt.Sprintf("会话已重置，新会话：%s", shortID))
}

func (h *Handler) handleSession(ctx context.Context, msg *gateway.Message) error {
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
		sb.WriteString(fmt.Sprintf("- %s：%s\n", s.Name, desc))
	}
	return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
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
			sb.WriteString(fmt.Sprintf("- %s [%s] %s | 运行次数：%d\n", job.ID, cronStatusLabel(job.Status), cron.DescribeSchedule(job.Schedule), job.RunCount))
		}
		return h.reply(ctx, msg, strings.TrimSpace(sb.String()))
	}

	parts := strings.Fields(args)
	if len(parts) < 1 {
		return h.reply(ctx, msg, "用法：/cron [list|add|remove|pause|resume]")
	}

	reg := h.agent.Tools()
	if reg == nil {
		return h.reply(ctx, msg, "当前没有可用的工具注册表。")
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
		resp, err := reg.Call("cron_add", map[string]any{
			"id":                  id,
			"schedule":            scheduleText,
			"mode":                "agent",
			"command":             command,
			"platform":            "qqofficial",
			"chat_id":             msg.Chat.ID,
			"reply_to_message_id": msg.ID,
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
		if _, err := reg.Call("cron_remove", map[string]any{"id": parts[1]}); err != nil {
			return h.reply(ctx, msg, err.Error())
		}
		return h.reply(ctx, msg, fmt.Sprintf("已删除定时任务：%s", parts[1]))

	case "pause":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/cron pause <id>")
		}
		if _, err := reg.Call("cron_pause", map[string]any{"id": parts[1]}); err != nil {
			return h.reply(ctx, msg, err.Error())
		}
		return h.reply(ctx, msg, fmt.Sprintf("已暂停定时任务：%s", parts[1]))

	case "resume":
		if len(parts) < 2 {
			return h.reply(ctx, msg, "用法：/cron resume <id>")
		}
		if _, err := reg.Call("cron_resume", map[string]any{"id": parts[1]}); err != nil {
			return h.reply(ctx, msg, err.Error())
		}
		return h.reply(ctx, msg, fmt.Sprintf("已恢复定时任务：%s", parts[1]))
	}

	return h.reply(ctx, msg, "用法：/cron [list|add|remove|pause|resume]")
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
	sb.WriteString("- Agent：运行中\n")

	if engine := h.agent.CronEngine(); engine != nil {
		if engine.IsRunning() {
			sb.WriteString(fmt.Sprintf("- Cron：运行中（%d 个任务）\n", engine.JobCount()))
		} else {
			sb.WriteString("- Cron：未运行\n")
		}
	} else {
		sb.WriteString("- Cron：不可用\n")
	}

	sb.WriteString(fmt.Sprintf("- Skills：%d 个已加载\n", len(h.agent.Skills())))

	if h.agent.Memory() != nil {
		sb.WriteString("- Memory：可用\n")
	} else {
		sb.WriteString("- Memory：不可用\n")
	}

	if m := h.agent.Metrics(); m != nil {
		snapshot := m.Snapshot()
		sb.WriteString(fmt.Sprintf("- Total requests：%d\n", snapshot.TotalRequests))
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
	sb.WriteString(fmt.Sprintf("- Model：%s\n", cfg.Model))

	metricsVal := h.agent.Metrics()
	if metricsVal != nil {
		uptime := time.Since(metricsVal.StartTime)
		sb.WriteString(fmt.Sprintf("- Uptime：%s\n", utils.FormatDurationCompact(uptime)))
		snapshot := metricsVal.Snapshot()
		sb.WriteString(fmt.Sprintf("- Total requests：%d\n", snapshot.TotalRequests))
	}

	if sessions := h.agent.Sessions(); sessions != nil {
		if sess, ok := sessions.Get(sessionID); ok && sess != nil {
			sb.WriteString(fmt.Sprintf("- Session messages：%d\n", sess.MessageCount()))
		}
	}

	running, queued := h.queueStatus(msg.Chat.ID)
	if running {
		sb.WriteString("- Current task：running\n")
	} else {
		sb.WriteString("- Current task：idle\n")
	}
	sb.WriteString(fmt.Sprintf("- Queue pending：%d", queued))

	return h.reply(ctx, msg, sb.String())
}

func (h *Handler) handleRestart(ctx context.Context, msg *gateway.Message) error {
	h.mu.Lock()
	if h.restarting {
		h.mu.Unlock()
		return h.reply(ctx, msg, "QQ 网关正在重启中，请稍候。")
	}
	h.restarting = true
	h.mu.Unlock()

	_ = h.reply(ctx, msg, "正在重启 QQ 网关...")

	go func(chatID string, replyTo *gateway.Message) {
		defer func() {
			h.mu.Lock()
			h.restarting = false
			h.mu.Unlock()
		}()

		h.cancelChatTask(chatID)

		if err := h.adapter.Stop(); err != nil {
			fmt.Printf("[qqofficial] restart stop failed: %v\n", err)
		}
		time.Sleep(1200 * time.Millisecond)

		if err := h.adapter.Start(context.Background()); err != nil {
			fmt.Printf("[qqofficial] restart start failed: %v\n", err)
			if replyTo != nil {
				_ = h.reply(context.Background(), replyTo, fmt.Sprintf("QQ 网关重启失败：%v", err))
			}
			return
		}
		if replyTo != nil {
			_ = h.reply(context.Background(), replyTo, "QQ 网关已重连并恢复接收消息。")
		}
	}(msg.Chat.ID, msg)

	return nil
}

func (h *Handler) dispatchChatAsync(ctx context.Context, msg *gateway.Message, input agent.UserTurnInput) error {
	input = input.Normalize()
	if strings.TrimSpace(input.RoutingText) == "" && strings.TrimSpace(input.Message.Content) == "" {
		return nil
	}

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
			fmt.Printf("[qqofficial] chat error: %v\n", err)
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
	input = input.Normalize()
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
	return h.reply(ctx, msg, response)
}

func (h *Handler) buildUserTurnInput(ctx context.Context, baseText string, attachments []gateway.Attachment) agent.UserTurnInput {
	baseText = strings.TrimSpace(baseText)
	if len(attachments) == 0 {
		return agent.TextUserTurnInput(baseText)
	}
	return agent.TextUserTurnInput(h.composeAttachmentInput(ctx, baseText, attachments))
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
		return fmt.Errorf("qqofficial: adapter or message is nil")
	}

	text, media, err := resolveOutboundMediaResponse(response)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) != "" {
		if err := h.reply(ctx, msg, text); err != nil {
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

func (h *Handler) sendAssistantResponseWithTrace(ctx context.Context, msg *gateway.Message, response string, trace *qqProgressTrace) error {
	if err := h.sendAssistantResponse(ctx, msg, response); err != nil {
		return err
	}
	if err := h.sendProgressTrace(ctx, msg, trace); err != nil {
		fmt.Printf("[qqofficial] warning: failed to send progress trace: %v\n", err)
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
		return fmt.Errorf("qqofficial: agent not initialized")
	}
	input = input.Normalize()
	if strings.TrimSpace(input.RoutingText) == "" && strings.TrimSpace(input.Message.Content) == "" {
		return nil
	}

	sessionID := h.getSessionID(msg.Chat.ID)
	h.agent.RecordRecentChatTarget("qqofficial", msg.Chat.ID, msg.ID)

	chatCtx, chatCancel := context.WithTimeout(ctx, h.chatStreamTimeout)
	defer chatCancel()

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

	var finalContent strings.Builder
	currentRound := 1
	trace := newQQProgressTrace()

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
				return h.sendAssistantResponseWithTrace(context.Background(), msg, out, trace)
			}
			switch evt.Type {
			case agent.ChatEventThinking:
				if nextRound := extractQQRoundNumber(evt.Content); nextRound > currentRound {
					currentRound = nextRound
				}
				trace.AddThinking(evt.Content, currentRound)
				h.sendProgress(context.Background(), msg, qqThinkingMessage(evt.Content, currentRound))
			case agent.ChatEventToolCall:
				trace.AddToolCall(evt.Name, evt.Args)
				h.sendProgress(context.Background(), msg, qqToolCallMessage(evt.Name, evt.Args))
			case agent.ChatEventToolResult:
				result := qqToolResultText(evt)
				trace.AddToolResult(evt.Name, result)
				h.sendProgress(context.Background(), msg, qqToolResultMessage(evt.Name, result))
			case agent.ChatEventContent:
				finalContent.WriteString(evt.Content)
			case agent.ChatEventDone:
				if strings.TrimSpace(finalContent.String()) == "" {
					finalContent.WriteString(evt.Content)
				}
				out := strings.TrimSpace(finalContent.String())
				if out == "" {
					out = "我这边暂时还没有整理出可发送的结果。"
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
		return fmt.Sprintf("我刚调了一下 `%s` 这个工具。", name)
	}
	return fmt.Sprintf("我刚调了一下 `%s` 这个工具，参数大概是：%s", name, args)
}

func qqToolResultMessage(name, result string) string {
	name = strings.TrimSpace(name)
	result = strings.TrimSpace(result)
	if len(result) > 180 {
		result = result[:177] + "..."
	}
	if result == "" {
		return fmt.Sprintf("`%s` 已经返回结果了。", name)
	}
	return fmt.Sprintf("`%s` 这边已经有结果了，我先记下来：%s", name, result)
}

const (
	qqProgressTraceMaxEntries = 18
	qqProgressTraceChunkLimit = 1800
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
		t.add(fmt.Sprintf("调用工具 `%s`。", name))
		return
	}
	t.add(fmt.Sprintf("调用工具 `%s`，参数摘要：%s", name, args))
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
		t.add(fmt.Sprintf("工具 `%s` 返回完成。", name))
		return
	}
	t.add(fmt.Sprintf("工具 `%s` 返回摘要：%s", name, result))
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
		return "qqofficial:" + chatID
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
		return "qqofficial:" + chatID
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

func shortSessionID(sessionID string) string {
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	return sessionID
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
