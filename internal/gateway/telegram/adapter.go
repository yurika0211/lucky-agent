package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	xproxy "golang.org/x/net/proxy"

	"github.com/yurika0211/luckyharness/internal/gateway"
)

const defaultAttachmentDownloadLimit = 1 << 30
const internalOutputFilteredFallback = "已完成处理。内部工具调用日志已自动隐藏。"

// Adapter implements gateway.Gateway for Telegram.
type Adapter struct {
	cfg     Config
	bot     *tgbotapi.BotAPI
	running bool
	cancel  context.CancelFunc

	mu        sync.RWMutex
	handler   gateway.MessageHandler
	rateLimit map[string]*rateBucket

	// Bot username for mention detection
	botUsername string
}

// rateBucket implements simple per-chat rate limiting.
type rateBucket struct {
	lastSent time.Time
}

// NewAdapter creates a new Telegram adapter.
func NewAdapter(cfg Config) *Adapter {
	if cfg.MaxMessageLen <= 0 {
		cfg.MaxMessageLen = 4000
	}
	if cfg.RateLimit <= 0 {
		cfg.RateLimit = 1
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = 30
	}

	return &Adapter{
		cfg:       cfg,
		rateLimit: make(map[string]*rateBucket),
	}
}

// Name returns the platform name.
func (a *Adapter) Name() string {
	return "telegram"
}

// SetHandler sets the message handler callback.
func (a *Adapter) SetHandler(handler gateway.MessageHandler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handler = handler
}

// Start connects to Telegram and begins polling for updates.
func (a *Adapter) Start(ctx context.Context) error {
	if a.cfg.Token == "" {
		return fmt.Errorf("telegram: bot token is required")
	}

	client, err := a.newHTTPClient()
	if err != nil {
		return err
	}

	bot, err := tgbotapi.NewBotAPIWithClient(a.cfg.Token, tgbotapi.APIEndpoint, client)
	if err != nil {
		return fmt.Errorf("telegram: create bot: %w", err)
	}

	a.bot = bot
	a.botUsername = bot.Self.UserName
	if err := a.registerBotCommands(); err != nil {
		fmt.Printf("[telegram] warning: failed to register bot commands: %v\n", err)
	}

	// Create cancellable context for the polling loop
	pollCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.running = true

	// Start polling in background
	go a.poll(pollCtx)

	return nil
}

func (a *Adapter) registerBotCommands() error {
	if a == nil || a.bot == nil {
		return fmt.Errorf("telegram: bot is not initialized")
	}
	commands := telegramBotCommands()
	if len(commands) == 0 {
		return nil
	}
	_, err := a.bot.Request(tgbotapi.NewSetMyCommands(commands...))
	return err
}

func (a *Adapter) newHTTPClient() (*http.Client, error) {
	if strings.TrimSpace(a.cfg.Proxy) == "" {
		return &http.Client{}, nil
	}

	proxyURL, err := url.Parse(a.cfg.Proxy)
	if err != nil {
		return nil, fmt.Errorf("telegram: parse proxy URL: %w", err)
	}

	transport := &http.Transport{}

	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(proxyURL)
	case "socks5", "socks5h":
		baseDialer := &net.Dialer{}
		dialer, err := xproxy.FromURL(proxyURL, baseDialer)
		if err != nil {
			return nil, fmt.Errorf("telegram: create SOCKS5 proxy dialer: %w", err)
		}
		if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
			transport.DialContext = contextDialer.DialContext
		} else {
			transport.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		}
	default:
		return nil, fmt.Errorf("telegram: unsupported proxy scheme %q", proxyURL.Scheme)
	}

	return &http.Client{Transport: transport}, nil
}

func parseReplyToMessageID(replyToMsgID string) (int, error) {
	if strings.TrimSpace(replyToMsgID) == "" {
		return 0, nil
	}

	replyToID, err := strconv.Atoi(replyToMsgID)
	if err != nil {
		return 0, fmt.Errorf("telegram: invalid reply-to message ID %q: %w", replyToMsgID, err)
	}
	return replyToID, nil
}

func telegramRequestFileData(source string) (tgbotapi.RequestFileData, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("telegram: empty media source")
	}

	if strings.HasPrefix(strings.ToLower(source), "sandbox:/") {
		source = strings.TrimPrefix(source, "sandbox:")
	}

	if strings.HasPrefix(strings.ToLower(source), "file://") {
		u, err := url.Parse(source)
		if err != nil {
			return nil, fmt.Errorf("telegram: parse file URL: %w", err)
		}
		if strings.TrimSpace(u.Path) == "" {
			return nil, fmt.Errorf("telegram: file URL has empty path")
		}
		if _, err := os.Stat(u.Path); err != nil {
			return nil, fmt.Errorf("telegram: stat media file: %w", err)
		}
		return tgbotapi.FilePath(u.Path), nil
	}

	if u, err := url.Parse(source); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return tgbotapi.FileURL(source), nil
	}

	info, err := os.Stat(source)
	if err != nil {
		return nil, fmt.Errorf("telegram: stat media file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("telegram: media source %q is a directory", source)
	}
	return tgbotapi.FilePath(source), nil
}

func truncateTelegramCaption(caption string) string {
	caption = strings.TrimSpace(caption)
	if len(caption) <= 1024 {
		return caption
	}
	return caption[:1021] + "..."
}

// Stop gracefully shuts down the adapter.
func (a *Adapter) Stop() error {
	if a.cancel != nil {
		a.cancel()
	}
	a.running = false
	return nil
}

// Send sends a message to a chat, splitting if necessary.
func (a *Adapter) Send(ctx context.Context, chatID string, message string) error {
	_, err := a.SendWithReceipt(ctx, chatID, message)
	return err
}

// SendWithReceipt sends a message and returns the first Telegram message ID.
func (a *Adapter) SendWithReceipt(ctx context.Context, chatID string, message string) (gateway.SentMessage, error) {
	if !a.running || a.bot == nil {
		return gateway.SentMessage{}, fmt.Errorf("telegram: adapter not running")
	}

	message = sanitizeOutgoingText(message)
	chunks := a.splitMessage(message)
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return gateway.SentMessage{}, fmt.Errorf("telegram: invalid chat ID %q: %w", chatID, err)
	}

	receipt := gateway.SentMessage{ChatID: chatID}
	for _, chunk := range chunks {
		sentID, err := a.sendChunk(ctx, chatIDInt, 0, chunk)
		if err != nil {
			return gateway.SentMessage{}, err
		}
		if receipt.ID == "" && sentID > 0 {
			receipt.ID = strconv.Itoa(sentID)
		}
		// Rate limit between chunks
		a.waitRateLimit(chatID)
	}

	return receipt, nil
}

// SendHTML sends a pre-rendered Telegram HTML message without markdown reformatting.
func (a *Adapter) SendHTML(ctx context.Context, chatID string, message string) error {
	if !a.running || a.bot == nil {
		return fmt.Errorf("telegram: adapter not running")
	}

	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	chunks := a.splitHTMLMessage(message)
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("telegram: invalid chat ID %q: %w", chatID, err)
	}

	for _, chunk := range chunks {
		if err := a.sendChunkHTML(ctx, chatIDInt, 0, chunk); err != nil {
			return err
		}
		a.waitRateLimit(chatID)
	}

	return nil
}

// SendWithReply sends a message as a reply to a specific message.
func (a *Adapter) SendWithReply(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	_, err := a.SendWithReplyReceipt(ctx, chatID, replyToMsgID, message)
	return err
}

// SendWithReplyReceipt sends a reply and returns the first Telegram message ID.
func (a *Adapter) SendWithReplyReceipt(ctx context.Context, chatID string, replyToMsgID string, message string) (gateway.SentMessage, error) {
	if !a.running || a.bot == nil {
		return gateway.SentMessage{}, fmt.Errorf("telegram: adapter not running")
	}

	message = sanitizeOutgoingText(message)
	chunks := a.splitMessage(message)
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return gateway.SentMessage{}, fmt.Errorf("telegram: invalid chat ID %q: %w", chatID, err)
	}

	replyToID, err := strconv.Atoi(replyToMsgID)
	if err != nil {
		return gateway.SentMessage{}, fmt.Errorf("telegram: invalid reply-to message ID %q: %w", replyToMsgID, err)
	}

	receipt := gateway.SentMessage{ChatID: chatID}
	for i, chunk := range chunks {
		replyID := 0
		if i == 0 {
			replyID = replyToID
		}
		sentID, err := a.sendChunk(ctx, chatIDInt, replyID, chunk)
		if err != nil {
			return gateway.SentMessage{}, err
		}
		if receipt.ID == "" && sentID > 0 {
			receipt.ID = strconv.Itoa(sentID)
		}
		a.waitRateLimit(chatID)
	}

	return receipt, nil
}

// SendWithReplyHTML sends a pre-rendered Telegram HTML message as a reply.
func (a *Adapter) SendWithReplyHTML(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	if !a.running || a.bot == nil {
		return fmt.Errorf("telegram: adapter not running")
	}

	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	chunks := a.splitHTMLMessage(message)
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("telegram: invalid chat ID %q: %w", chatID, err)
	}

	replyToID, err := strconv.Atoi(replyToMsgID)
	if err != nil {
		return fmt.Errorf("telegram: invalid reply-to message ID %q: %w", replyToMsgID, err)
	}

	for i, chunk := range chunks {
		replyID := 0
		if i == 0 {
			replyID = replyToID
		}
		if err := a.sendChunkHTML(ctx, chatIDInt, replyID, chunk); err != nil {
			return err
		}
		a.waitRateLimit(chatID)
	}

	return nil
}

// SendPhoto sends a photo to a chat, optionally replying to a message.
func (a *Adapter) SendPhoto(_ context.Context, chatID string, replyToMsgID string, source string, caption string) error {
	if !a.running || a.bot == nil {
		return fmt.Errorf("telegram: adapter not running")
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("telegram: invalid chat ID %q: %w", chatID, err)
	}

	replyToID, err := parseReplyToMessageID(replyToMsgID)
	if err != nil {
		return err
	}

	fileData, err := telegramRequestFileData(source)
	if err != nil {
		return err
	}

	msg := tgbotapi.NewPhoto(chatIDInt, fileData)
	msg.Caption = truncateTelegramCaption(formatTelegramRichText(caption))
	msg.ParseMode = tgbotapi.ModeHTML
	if replyToID > 0 {
		msg.ReplyToMessageID = replyToID
	}

	if _, err := a.bot.Send(msg); err != nil {
		return fmt.Errorf("telegram: send photo: %w", err)
	}
	return nil
}

// SendDocument sends a document to a chat, optionally replying to a message.
func (a *Adapter) SendDocument(_ context.Context, chatID string, replyToMsgID string, source string, caption string) error {
	if !a.running || a.bot == nil {
		return fmt.Errorf("telegram: adapter not running")
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("telegram: invalid chat ID %q: %w", chatID, err)
	}

	replyToID, err := parseReplyToMessageID(replyToMsgID)
	if err != nil {
		return err
	}

	fileData, err := telegramRequestFileData(source)
	if err != nil {
		return err
	}

	msg := tgbotapi.NewDocument(chatIDInt, fileData)
	msg.Caption = truncateTelegramCaption(formatTelegramRichText(caption))
	msg.ParseMode = tgbotapi.ModeHTML
	if replyToID > 0 {
		msg.ReplyToMessageID = replyToID
	}

	if _, err := a.bot.Send(msg); err != nil {
		return fmt.Errorf("telegram: send document: %w", err)
	}
	return nil
}

// IsRunning returns whether the adapter is currently connected.
func (a *Adapter) IsRunning() bool {
	return a.running
}

// SendTypingLoop 持续发送 typing indicator，直到 ctx 被取消。
// Telegram 的 typing 状态持续 5 秒，所以每 4.5 秒刷新一次。
func (a *Adapter) SendTypingLoop(ctx context.Context, chatID string) {
	if a.bot == nil {
		return
	}
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return
	}

	ticker := time.NewTicker(4500 * time.Millisecond)
	defer ticker.Stop()

	// 立即发一次
	a.sendTypingOnce(chatIDInt)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.sendTypingOnce(chatIDInt)
		}
	}
}

// sendTypingOnce 发送一次 typing action
func (a *Adapter) sendTypingOnce(chatID int64) {
	action := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	a.bot.Request(action) // 忽略错误，typing 是 best-effort
}

// ReactToMessage 给消息添加 emoji reaction（👍 等）
// 使用 Telegram Bot API setMessageReaction（v5.5.1 未封装，复用 bot HTTP client 调用）
func (a *Adapter) ReactToMessage(chatID string, messageID string, emoji string) {
	if a.bot == nil {
		return
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" {
		return
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return
	}
	msgIDInt, err := strconv.Atoi(messageID)
	if err != nil {
		return
	}

	// best-effort，不阻塞消息处理主路径。
	go a.callSetMessageReaction(chatIDInt, msgIDInt, emoji)
}

// callSetMessageReaction 调用 Telegram setMessageReaction API
func (a *Adapter) callSetMessageReaction(chatID int64, messageID int, emoji string) error {
	if a.bot == nil {
		return fmt.Errorf("telegram: bot is not initialized")
	}
	params := tgbotapi.Params{
		"chat_id":    strconv.FormatInt(chatID, 10),
		"message_id": strconv.Itoa(messageID),
	}
	if err := params.AddInterface("reaction", []map[string]string{
		{
			"type":  "emoji",
			"emoji": emoji,
		},
	}); err != nil {
		return err
	}
	_, err := a.bot.MakeRequest("setMessageReaction", params)
	return err
}

// callTelegramAPI 调用 Telegram Bot API 的通用方法
func (a *Adapter) callTelegramAPI(method string, params url.Values) ([]byte, error) {
	if a.bot == nil {
		return nil, fmt.Errorf("telegram: bot is not initialized")
	}
	apiParams := tgbotapi.Params{}
	for key, values := range params {
		if len(values) > 0 {
			apiParams[key] = values[0]
		}
	}
	resp, err := a.bot.MakeRequest(method, apiParams)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resp)
}

// SendStream implements gateway.StreamGateway.
// Creates a streaming message that can be updated in real-time.
func (a *Adapter) SendStream(ctx context.Context, chatID string, replyToMsgID string) (gateway.StreamSender, error) {
	if !a.running || a.bot == nil {
		return nil, fmt.Errorf("telegram: adapter not running")
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("telegram: invalid chat ID %q: %w", chatID, err)
	}

	replyToID := 0
	if replyToMsgID != "" {
		replyToID, _ = strconv.Atoi(replyToMsgID)
	}

	// 发送初始 "思考中" 消息
	initialText := "🧠 Thinking..."
	msg := tgbotapi.NewMessage(chatIDInt, initialText)
	if replyToID > 0 {
		msg.ReplyToMessageID = replyToID
	}

	sent, err := a.bot.Send(msg)
	if err != nil && replyToID > 0 {
		msg.ReplyToMessageID = 0
		sent, err = a.bot.Send(msg)
	}
	if err != nil {
		return nil, fmt.Errorf("telegram: send stream initial: %w", err)
	}

	return &telegramStreamSender{
		adapter:   a,
		chatID:    chatIDInt,
		messageID: sent.MessageID,
		chatIDStr: chatID,
		content:   "",
		thinking:  "🧠 Thinking...",
		editCount: 0,
		lastEdit:  time.Now(),
	}, nil
}

// telegramStreamSender implements gateway.StreamSender for Telegram.
type telegramStreamSender struct {
	adapter   *Adapter
	chatID    int64
	messageID int
	chatIDStr string

	mu        sync.Mutex
	content   string // 已生成的正文内容
	thinking  string // 当前思考/工具调用标签
	editCount int
	lastEdit  time.Time
	finished  bool
}

// minEditInterval 是两次消息编辑之间的最小间隔（避免触发 Telegram 限流）
const minEditInterval = 800 * time.Millisecond

// maxEdits 是单条消息最大编辑次数（超过后不再编辑，等 Finish 一次性更新）
const maxEdits = 40

func (s *telegramStreamSender) Append(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		return fmt.Errorf("stream sender already finished")
	}

	s.content += content
	// 追加内容时清除思考标签
	s.thinking = ""
	return s.throttledEdit()
}

func (s *telegramStreamSender) SetThinking(label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		return nil
	}

	s.thinking = fmt.Sprintf("🧠 %s", label)
	return s.throttledEdit()
}

func (s *telegramStreamSender) SetToolCall(name, args string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		return nil
	}

	if strings.TrimSpace(args) == "" {
		s.thinking = fmt.Sprintf("🔧 %s", name)
		return s.throttledEdit()
	}

	// 截断过长的参数
	shortArgs := args
	if len(shortArgs) > 80 {
		shortArgs = shortArgs[:77] + "..."
	}
	s.thinking = fmt.Sprintf("🔧 %s(%s)", name, shortArgs)
	return s.throttledEdit()
}

func (s *telegramStreamSender) SetResult(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		return nil
	}

	s.content = sanitizeOutgoingText(content)
	s.thinking = ""
	return s.throttledEdit()
}

func (s *telegramStreamSender) SetHTMLCard(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		return nil
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	s.content = content
	s.thinking = ""
	return s.editMessageHTML(content)
}

func (s *telegramStreamSender) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finished {
		return nil
	}
	s.finished = true
	s.thinking = ""

	// 最终编辑：显示完整内容
	display := s.renderContent()
	return s.editMessageHTML(display)
}

func (s *telegramStreamSender) MessageID() string {
	return strconv.Itoa(s.messageID)
}

// throttledEdit 限流编辑：避免过于频繁调用 Telegram API
func (s *telegramStreamSender) throttledEdit() error {
	// 超过最大编辑次数，跳过中间编辑
	if s.editCount >= maxEdits {
		return nil
	}

	// 距离上次编辑太近，跳过
	if time.Since(s.lastEdit) < minEditInterval {
		return nil
	}

	display := s.renderContent()
	return s.editMessage(display)
}

// renderContent 渲染当前消息内容：思考标签 + 正文
func (s *telegramStreamSender) renderContent() string {
	var sb strings.Builder

	// 思考/工具调用标签（作为前缀）
	if s.thinking != "" {
		sb.WriteString(s.thinking)
		sb.WriteString("\n\n")
	}

	// 正文内容
	if s.content != "" {
		content := s.content
		// 预留思考标签的空间
		maxLen := 3900
		if s.thinking != "" {
			maxLen -= len(s.thinking) + 2
		}
		if len(content) > maxLen {
			content = content[:maxLen-3] + "..."
		}
		sb.WriteString(content)
	}

	// 如果两者都为空，显示默认思考状态
	if s.thinking == "" && s.content == "" {
		return "🧠 Thinking..."
	}

	return sanitizeOutgoingText(sb.String())
}

// editMessage 调用 Telegram API 编辑消息
func (s *telegramStreamSender) editMessage(text string) error {
	return s.editMessageWithMode(text, "")
}

func (s *telegramStreamSender) editMessageHTML(text string) error {
	return s.editMessageWithMode(formatTelegramRichText(text), tgbotapi.ModeHTML)
}

func (s *telegramStreamSender) editMessageWithMode(text string, parseMode string) error {
	if s.adapter.bot == nil {
		return fmt.Errorf("bot not available")
	}

	edit := tgbotapi.NewEditMessageText(s.chatID, s.messageID, text)
	// 中间流式编辑默认保持纯文本；最终落版可切到 HTML。
	edit.ParseMode = parseMode

	_, err := s.adapter.bot.Send(edit)
	if err != nil {
		// 编辑失败不中断流，静默忽略
		return nil
	}

	s.editCount++
	s.lastEdit = time.Now()
	return nil
}

// poll runs the long-polling loop.
func (a *Adapter) poll(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = a.cfg.PollTimeout

	updates := a.bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			return
		case update := <-updates:
			// v0.36.0: 处理所有消息类型（文本、图片、语音、视频、文件）
			if update.Message == nil {
				continue
			}
			a.processUpdate(ctx, update)
		}
	}
}

// processUpdate converts a Telegram update to a gateway.Message and dispatches it.
func (a *Adapter) processUpdate(ctx context.Context, update tgbotapi.Update) {
	tgMsg := update.Message
	if tgMsg == nil {
		return
	}

	chatID := strconv.FormatInt(tgMsg.Chat.ID, 10)
	fmt.Printf("[telegram] received msg: chatID=%s, chatType=%s, from=%v, text=%q\n",
		chatID, tgMsg.Chat.Type, tgMsg.From.UserName, func() string {
			if len(tgMsg.Text) > 80 {
				return tgMsg.Text[:80]
			}
			return tgMsg.Text
		}())

	// Check chat whitelist
	if !a.cfg.IsChatAllowed(chatID) {
		return
	}

	msg := a.convertMessage(tgMsg)

	// In group chats, only respond to @bot mentions or replies to bot's own messages
	if msg.Chat.Type != gateway.ChatPrivate {
		mentioned := a.isMentioned(tgMsg)
		replyToBot := a.isReplyToBot(tgMsg)
		fmt.Printf("[telegram] group msg: chatID=%s, mentioned=%v, replyToBot=%v, botUsername=%s, text=%q\n",
			chatID, mentioned, replyToBot, a.botUsername, msg.Text[:min(80, len(msg.Text))])
		if !mentioned && !replyToBot {
			return
		}
		// Strip @botusername from text
		if a.botUsername != "" {
			msg.Text = strings.ReplaceAll(msg.Text, "@"+a.botUsername, "")
			msg.Text = strings.TrimSpace(msg.Text)
			msg.Args = strings.TrimSpace(strings.TrimPrefix(msg.Args, "@"+a.botUsername))
		}
		// 标记群聊触发方式，供 handler 使用
		msg.IsGroupTrigger = true
		msg.TriggerType = "mention"
		if replyToBot && !mentioned {
			msg.TriggerType = "reply"
		}
	}

	a.mu.RLock()
	handler := a.handler
	a.mu.RUnlock()

	if handler != nil {
		if err := handler(ctx, msg); err != nil {
			fmt.Printf("[telegram] handler error: %v\n", err)
		}
	}
}

// convertMessage converts a Telegram message to a gateway.Message.
func (a *Adapter) convertMessage(tgMsg *tgbotapi.Message) *gateway.Message {
	chatType := gateway.ChatPrivate
	switch tgMsg.Chat.Type {
	case "group":
		chatType = gateway.ChatGroup
	case "supergroup":
		chatType = gateway.ChatSuperGroup
	case "channel":
		chatType = gateway.ChatChannel
	}

	msg := &gateway.Message{
		ID: strconv.Itoa(tgMsg.MessageID),
		Chat: gateway.Chat{
			ID:       strconv.FormatInt(tgMsg.Chat.ID, 10),
			Type:     chatType,
			Title:    tgMsg.Chat.Title,
			Username: tgMsg.Chat.UserName,
		},
		Sender: gateway.User{
			ID:        strconv.FormatInt(tgMsg.From.ID, 10),
			Username:  tgMsg.From.UserName,
			FirstName: tgMsg.From.FirstName,
			LastName:  tgMsg.From.LastName,
		},
		Text:      tgMsg.Text,
		Timestamp: time.Unix(int64(tgMsg.Date), 0),
	}
	if strings.TrimSpace(msg.Text) == "" {
		msg.Text = tgMsg.Caption
	}

	// v0.36.0: 提取多媒体附件
	a.extractAttachments(tgMsg, msg)

	// 如果没有文本但有附件，构造描述文本
	if msg.Text == "" && len(msg.Attachments) > 0 {
		var parts []string
		for _, att := range msg.Attachments {
			switch att.Type {
			case gateway.AttachmentImage:
				parts = append(parts, "[用户发送了一张图片]")
			case gateway.AttachmentAudio:
				parts = append(parts, "[用户发送了一段语音]")
			case gateway.AttachmentVideo:
				parts = append(parts, "[用户发送了一段视频]")
			case gateway.AttachmentDocument:
				parts = append(parts, fmt.Sprintf("[用户发送了文件: %s]", att.FileName))
			}
		}
		msg.Text = strings.Join(parts, " ")
	}

	// Parse command
	if tgMsg.IsCommand() {
		msg.IsCommand = true
		msg.Command = tgMsg.Command()
		msg.Args = tgMsg.CommandArguments()
	}

	// Parse reply
	if tgMsg.ReplyToMessage != nil {
		replyMsg := a.convertMessage(tgMsg.ReplyToMessage)
		msg.ReplyTo = replyMsg
	}

	return msg
}

// extractAttachments 从 Telegram 消息中提取多媒体附件
func (a *Adapter) extractAttachments(tgMsg *tgbotapi.Message, msg *gateway.Message) {
	if a.bot == nil {
		return
	}

	// 图片
	if tgMsg.Photo != nil && len(tgMsg.Photo) > 0 {
		// 取最大尺寸的图片
		photo := tgMsg.Photo[len(tgMsg.Photo)-1]
		att := gateway.Attachment{
			Type:     gateway.AttachmentImage,
			FileID:   photo.FileID,
			FileName: "photo.jpg",
			MimeType: "image/jpeg",
			FileSize: int64(photo.FileSize),
		}
		// 尝试下载图片
		if url, err := a.bot.GetFileDirectURL(photo.FileID); err == nil {
			att.FileURL = url
			a.populateAttachmentData(&att)
		}
		msg.Attachments = append(msg.Attachments, att)
	}

	// 语音消息
	if tgMsg.Voice != nil {
		att := gateway.Attachment{
			Type:     gateway.AttachmentAudio,
			FileID:   tgMsg.Voice.FileID,
			FileName: "voice.ogg",
			MimeType: tgMsg.Voice.MimeType,
			FileSize: int64(tgMsg.Voice.FileSize),
		}
		if url, err := a.bot.GetFileDirectURL(tgMsg.Voice.FileID); err == nil {
			att.FileURL = url
			a.populateAttachmentData(&att)
		}
		msg.Attachments = append(msg.Attachments, att)
	}

	// 音频文件
	if tgMsg.Audio != nil {
		att := gateway.Attachment{
			Type:     gateway.AttachmentAudio,
			FileID:   tgMsg.Audio.FileID,
			FileName: tgMsg.Audio.FileName,
			MimeType: tgMsg.Audio.MimeType,
			FileSize: int64(tgMsg.Audio.FileSize),
		}
		if url, err := a.bot.GetFileDirectURL(tgMsg.Audio.FileID); err == nil {
			att.FileURL = url
			a.populateAttachmentData(&att)
		}
		msg.Attachments = append(msg.Attachments, att)
	}

	// 视频
	if tgMsg.Video != nil {
		att := gateway.Attachment{
			Type:     gateway.AttachmentVideo,
			FileID:   tgMsg.Video.FileID,
			FileName: tgMsg.Video.FileName,
			MimeType: tgMsg.Video.MimeType,
			FileSize: int64(tgMsg.Video.FileSize),
		}
		if url, err := a.bot.GetFileDirectURL(tgMsg.Video.FileID); err == nil {
			att.FileURL = url
			a.populateAttachmentData(&att)
		}
		msg.Attachments = append(msg.Attachments, att)
	}

	// 文档
	if tgMsg.Document != nil {
		att := gateway.Attachment{
			Type:     gateway.AttachmentDocument,
			FileID:   tgMsg.Document.FileID,
			FileName: tgMsg.Document.FileName,
			MimeType: tgMsg.Document.MimeType,
			FileSize: int64(tgMsg.Document.FileSize),
		}
		if url, err := a.bot.GetFileDirectURL(tgMsg.Document.FileID); err == nil {
			att.FileURL = url
			a.populateAttachmentData(&att)
		}
		msg.Attachments = append(msg.Attachments, att)
	}
}

func (a *Adapter) populateAttachmentData(att *gateway.Attachment) {
	if att == nil || strings.TrimSpace(att.FileURL) == "" {
		return
	}
	if att.FileSize > defaultAttachmentDownloadLimit {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, att.FileURL, nil)
	if err != nil {
		return
	}

	var client interface {
		Do(*http.Request) (*http.Response, error)
	} = http.DefaultClient
	if a.bot != nil && a.bot.Client != nil {
		client = a.bot.Client
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}

	dir, err := telegramAttachmentStorageDir()
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}

	fileName := telegramAttachmentFileName(att)
	tmpFile, err := os.CreateTemp(dir, fileName+".*.part")
	if err != nil {
		return
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
	}()

	reader := io.LimitReader(resp.Body, defaultAttachmentDownloadLimit+1)
	written, err := io.Copy(tmpFile, reader)
	if err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	if written > defaultAttachmentDownloadLimit {
		_ = os.Remove(tmpPath)
		return
	}

	finalPath := filepath.Join(dir, fileName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	att.FilePath = finalPath
}

func telegramAttachmentStorageDir() (string, error) {
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".luckyharness", "data", "telegram", "attachments"), nil
	}
	return filepath.Join(os.TempDir(), "luckyharness", "telegram", "attachments"), nil
}

func telegramAttachmentFileName(att *gateway.Attachment) string {
	name := strings.TrimSpace(att.FileName)
	if name == "" {
		switch att.Type {
		case gateway.AttachmentImage:
			name = "image"
		case gateway.AttachmentAudio:
			name = "audio"
		case gateway.AttachmentVideo:
			name = "video"
		default:
			name = "document"
		}
	}

	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		default:
			return r
		}
	}, name)
	if strings.TrimSpace(name) == "" {
		name = "attachment"
	}

	prefix := strings.TrimSpace(att.FileID)
	if prefix == "" {
		prefix = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	prefix = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, prefix)
	return prefix + "_" + name
}

// isMentioned checks if the bot is mentioned in the message.
func (a *Adapter) isMentioned(tgMsg *tgbotapi.Message) bool {
	if a.botUsername == "" {
		return false
	}

	// Check text for @botusername
	if strings.Contains(tgMsg.Text, "@"+a.botUsername) {
		return true
	}

	// Check entities for mention / text_mention
	for _, entity := range tgMsg.Entities {
		switch entity.Type {
		case "mention":
			mention := tgMsg.Text[entity.Offset : entity.Offset+entity.Length]
			if mention == "@"+a.botUsername {
				return true
			}
		case "text_mention":
			// text_mention 用于没有 username 的用户 @bot，检查 User 字段
			if entity.User != nil && entity.User.UserName == a.botUsername {
				return true
			}
		}
	}

	return false
}

// isReplyToBot checks if the message is a reply to the bot's own message.
func (a *Adapter) isReplyToBot(tgMsg *tgbotapi.Message) bool {
	if tgMsg.ReplyToMessage == nil {
		return false
	}
	// 检查被回复消息的发送者是否是 bot 自己
	return tgMsg.ReplyToMessage.From != nil && tgMsg.ReplyToMessage.From.IsBot
}

// sendChunk sends a single message chunk to Telegram.
func (a *Adapter) sendChunk(_ context.Context, chatID int64, replyTo int, text string) (int, error) {
	plainText := text
	msg := tgbotapi.NewMessage(chatID, formatTelegramRichText(text))
	if replyTo > 0 {
		msg.ReplyToMessageID = replyTo
	}
	msg.ParseMode = tgbotapi.ModeHTML

	sent, err := a.bot.Send(msg)
	if err != nil {
		// If rich text rendering fails, fall back to plain text.
		msg.ParseMode = ""
		msg.Text = plainText
		sent, err = a.bot.Send(msg)
		if err != nil {
			return 0, fmt.Errorf("telegram: send message: %w", err)
		}
	}

	return sent.MessageID, nil
}

func (a *Adapter) sendChunkHTML(_ context.Context, chatID int64, replyTo int, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	if replyTo > 0 {
		msg.ReplyToMessageID = replyTo
	}
	msg.ParseMode = tgbotapi.ModeHTML

	if _, err := a.bot.Send(msg); err != nil {
		return fmt.Errorf("telegram: send html message: %w", err)
	}
	return nil
}

// splitMessage splits a message into chunks that fit within Telegram's 4096 char limit.
func (a *Adapter) splitMessage(message string) []string {
	maxLen := a.cfg.MaxMessageLen
	if maxLen <= 0 || maxLen > 4096 {
		maxLen = 4096
	}

	if len(message) <= maxLen {
		return []string{message}
	}

	return splitTelegramMessageChunks(message, maxLen)
}

func (a *Adapter) splitHTMLMessage(message string) []string {
	maxLen := a.cfg.MaxMessageLen
	if maxLen <= 0 || maxLen > 4096 {
		maxLen = 4096
	}
	if len(message) <= maxLen {
		return []string{message}
	}

	lines := strings.SplitAfter(message, "\n")
	var chunks []string
	var current strings.Builder
	for _, line := range lines {
		if current.Len()+len(line) > maxLen && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if len(line) > maxLen {
			for len(line) > maxLen {
				chunks = append(chunks, line[:maxLen])
				line = line[maxLen:]
			}
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func splitTelegramMessageChunks(message string, maxLen int) []string {
	lines := strings.SplitAfter(message, "\n")
	var chunks []string
	var current strings.Builder
	inFence := false
	pendingFenceReopen := false
	fenceOpener := ""

	for _, originalLine := range lines {
		line := originalLine
		lineIsFence := isMarkdownFenceLine(originalLine)

		for {
			if pendingFenceReopen && current.Len() == 0 {
				current.WriteString(fenceOpener)
				if !strings.HasSuffix(fenceOpener, "\n") {
					current.WriteString("\n")
				}
				pendingFenceReopen = false
			}

			limit := maxLen
			if inFence && !lineIsFence {
				limit -= len("\n```")
				if limit <= 0 {
					limit = 1
				}
			}

			if current.Len()+len(line) > limit {
				if current.Len() == 0 || chunkHasOnlyFencePrefix(current.String(), fenceOpener) {
					take := limit - current.Len()
					if take <= 0 {
						take = maxLen
					}
					current.WriteString(line[:take])
					line = line[take:]
					chunk, reopen := finalizeTelegramChunk(current.String(), inFence)
					if chunk != "" {
						chunks = append(chunks, chunk)
					}
					current.Reset()
					pendingFenceReopen = reopen && fenceOpener != ""
					continue
				}

				chunk, reopen := finalizeTelegramChunk(current.String(), inFence)
				if chunk != "" {
					chunks = append(chunks, chunk)
				}
				current.Reset()
				pendingFenceReopen = reopen && fenceOpener != ""
				continue
			}

			current.WriteString(line)
			break
		}

		if lineIsFence {
			trimmed := strings.TrimRight(originalLine, "\n")
			if !inFence {
				inFence = true
				fenceOpener = trimmed
			} else {
				inFence = false
				pendingFenceReopen = false
				fenceOpener = ""
			}
		}
	}

	if current.Len() > 0 {
		chunk, _ := finalizeTelegramChunk(current.String(), inFence)
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
	}

	return chunks
}

func isMarkdownFenceLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimRight(line, "\n"))
	if len(trimmed) < 3 {
		return false
	}
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func finalizeTelegramChunk(chunk string, inFence bool) (string, bool) {
	if chunk == "" {
		return "", false
	}
	if !inFence {
		return chunk, false
	}
	if !strings.HasSuffix(chunk, "\n") {
		chunk += "\n"
	}
	chunk += "```"
	return chunk, true
}

func chunkHasOnlyFencePrefix(chunk string, fenceOpener string) bool {
	if fenceOpener == "" {
		return false
	}
	return chunk == fenceOpener || chunk == fenceOpener+"\n"
}

// waitRateLimit enforces per-chat rate limiting.
func (a *Adapter) waitRateLimit(chatID string) {
	a.mu.Lock()
	bucket, exists := a.rateLimit[chatID]
	if !exists {
		bucket = &rateBucket{}
		a.rateLimit[chatID] = bucket
	}
	a.mu.Unlock()

	elapsed := time.Since(bucket.lastSent)
	minInterval := time.Second / time.Duration(a.cfg.RateLimit)
	if elapsed < minInterval {
		time.Sleep(minInterval - elapsed)
	}

	bucket.lastSent = time.Now()
}
