package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/utils"
)

const CompactBoundaryName = "compact_boundary"

// CompactMetadata records a session compaction boundary.
type CompactMetadata struct {
	ID                  string              `json:"id"`
	Trigger             string              `json:"trigger"`
	Summary             string              `json:"summary"`
	FromMessage         int                 `json:"from_message,omitempty"`
	ToMessage           int                 `json:"to_message,omitempty"`
	ContentHash         string              `json:"content_hash,omitempty"`
	PolicyVersion       string              `json:"policy_version,omitempty"`
	CreatedAt           time.Time           `json:"created_at"`
	PreTokenEstimate    int                 `json:"pre_token_estimate,omitempty"`
	PostTokenEstimate   int                 `json:"post_token_estimate,omitempty"`
	SummaryTokens       int                 `json:"summary_tokens,omitempty"`
	DroppedMessages     int                 `json:"dropped_messages,omitempty"`
	RetainedMessages    int                 `json:"retained_messages,omitempty"`
	RestoredAttachments int                 `json:"restored_attachments,omitempty"`
	SummarySource       string              `json:"summary_source,omitempty"`
	Attachments         []CompactAttachment `json:"attachments,omitempty"`
}

type CompactAttachment struct {
	Kind     string `json:"kind"`
	Source   string `json:"source,omitempty"`
	Content  string `json:"content"`
	Priority int    `json:"priority,omitempty"`
	Tokens   int    `json:"tokens,omitempty"`
}

type CompactTrace struct {
	BoundaryID          string    `json:"boundary_id"`
	Trigger             string    `json:"trigger"`
	CreatedAt           time.Time `json:"created_at"`
	PreTokenEstimate    int       `json:"pre_token_estimate,omitempty"`
	PostTokenEstimate   int       `json:"post_token_estimate,omitempty"`
	SummaryTokens       int       `json:"summary_tokens,omitempty"`
	DroppedMessages     int       `json:"dropped_messages,omitempty"`
	RetainedMessages    int       `json:"retained_messages,omitempty"`
	RestoredAttachments int       `json:"restored_attachments,omitempty"`
	SummarySource       string    `json:"summary_source,omitempty"`
	FromMessage         int       `json:"from_message,omitempty"`
	ToMessage           int       `json:"to_message,omitempty"`
	ContentHash         string    `json:"content_hash,omitempty"`
	PolicyVersion       string    `json:"policy_version,omitempty"`
}

// Session 代表一次对话会话
type Session struct {
	mu        sync.RWMutex
	saveMu    sync.Mutex
	loadMu    sync.Mutex
	ID        string
	Title     string
	Messages  []provider.Message
	CreatedAt time.Time
	UpdatedAt time.Time
	dir       string
	// ShellContext 持久化 shell 环境（跨工具调用保持）
	ShellContext ShellContext

	// v0.44.0: 懒加载支持
	messagesLoaded bool // 是否已加载完整消息
	messageCount   int  // 元数据中的消息数量（未加载时使用）

	// 分页请求只缓存最近一小段消息，避免为了 history 首屏把整个旧会话
	// 常驻在内存中。完整 GetMessages 仍会按需加载全部消息。
	pageCache      []provider.Message
	pageCacheStart int
	pageCacheTotal int
	pageCacheValid bool
}

// ShellContext 保存 shell 会话的环境状态
type ShellContext struct {
	Cwd string            `json:"cwd"` // 当前工作目录
	Env map[string]string `json:"env"` // 自定义环境变量
}

// GetCwd 返回当前工作目录
func (s *Session) GetCwd() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ShellContext.Cwd == "" {
		return ""
	}
	return s.ShellContext.Cwd
}

// SetCwd 设置当前工作目录
func (s *Session) SetCwd(cwd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ShellContext.Cwd = cwd
	s.UpdatedAt = time.Now()
}

// RestoreShellContext restores a durable task's working directory and
// environment after replaying an already completed terminal operation.
func (s *Session) RestoreShellContext(sc ShellContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ShellContext = ShellContext{Cwd: sc.Cwd, Env: make(map[string]string, len(sc.Env))}
	for key, value := range sc.Env {
		s.ShellContext.Env[key] = value
	}
	s.UpdatedAt = time.Now()
}

// GetEnv 获取所有自定义环境变量
func (s *Session) GetEnv() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ShellContext.Env == nil {
		return map[string]string{}
	}
	cp := make(map[string]string, len(s.ShellContext.Env))
	for k, v := range s.ShellContext.Env {
		cp[k] = v
	}
	return cp
}

// SetEnv 设置一个环境变量
func (s *Session) SetEnv(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ShellContext.Env == nil {
		s.ShellContext.Env = make(map[string]string)
	}
	s.ShellContext.Env[key] = value
	s.UpdatedAt = time.Now()
}

// UnsetEnv 删除一个环境变量
func (s *Session) UnsetEnv(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ShellContext.Env != nil {
		delete(s.ShellContext.Env, key)
	}
	s.UpdatedAt = time.Now()
}

// SetTitle updates the session title.
func (s *Session) SetTitle(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Title = strings.TrimSpace(title)
	s.UpdatedAt = time.Now()
}

// NewSession 创建新会话
func NewSession(id, dir string) *Session {
	now := time.Now()
	return &Session{
		ID:             id,
		Messages:       make([]provider.Message, 0),
		CreatedAt:      now,
		UpdatedAt:      now,
		dir:            dir,
		messagesLoaded: true,
	}
}

// AddProviderMessage 添加完整 provider 消息（保留 tool_calls / tool_call_id 等结构化字段）
func (s *Session) AddProviderMessage(msg provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if msg.CreatedAt == nil {
		now := time.Now().UTC()
		msg.CreatedAt = &now
	}

	if msg.Role == "assistant" {
		msg.Content = utils.SanitizeToolProtocolOutput(msg.Content)
	}

	// 确保消息已加载
	if !s.messagesLoaded {
		s.Messages = make([]provider.Message, 0)
		s.messagesLoaded = true
	}

	s.Messages = append(s.Messages, msg)
	s.messageCount = len(s.Messages)
	s.invalidatePageCacheLocked()
	s.UpdatedAt = time.Now()

	// 自动生成标题：取第一条用户消息的前 50 字符
	if s.Title == "" && msg.Role == "user" {
		title := msg.Content
		if len(title) > 50 {
			title = title[:50] + "..."
		}
		s.Title = title
	}
}

// AddMessage 添加消息
func (s *Session) AddMessage(role, content string) {
	s.AddProviderMessage(provider.Message{Role: role, Content: content})
}

// AddToolMessage 添加工具结果消息
func (s *Session) AddToolMessage(toolName, result string) {
	s.AddProviderMessage(provider.Message{
		Role:    "tool",
		Content: fmt.Sprintf("[Tool: %s] %s", toolName, result),
		Name:    toolName,
	})
}

// AddToolMessageWithCallID 添加带 tool_call_id 的工具结果消息（function calling 兼容）
func (s *Session) AddToolMessageWithCallID(callID, toolName, result string) {
	s.AddProviderMessage(provider.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: callID,
		Name:       toolName,
	})
}

// AddCompactBoundary appends a compact boundary marker. The marker summary
// represents prior raw history for future context construction.
func (s *Session) AddCompactBoundary(meta CompactMetadata) provider.Message {
	if strings.TrimSpace(meta.ID) == "" {
		meta.ID = fmt.Sprintf("compact-%d", time.Now().UnixNano())
	}
	if strings.TrimSpace(meta.Trigger) == "" {
		meta.Trigger = "manual"
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now()
	}
	msg := CompactBoundaryMessage(meta)
	s.AddProviderMessage(msg)
	return msg
}

func (s *Session) UndoLatestCompactBoundary(keepAfter bool) (CompactMetadata, error) {
	if err := s.loadMessages(); err != nil {
		return CompactMetadata{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	last := -1
	for i, msg := range s.Messages {
		if IsCompactBoundary(msg) {
			last = i
		}
	}
	if last < 0 {
		return CompactMetadata{}, fmt.Errorf("compact boundary not found")
	}
	if last < len(s.Messages)-1 && !keepAfter {
		return CompactMetadata{}, fmt.Errorf("compact boundary has newer messages; pass keepAfter to preserve them while removing the boundary")
	}
	meta, _ := ParseCompactMetadata(s.Messages[last])
	s.Messages = append(s.Messages[:last], s.Messages[last+1:]...)
	s.messageCount = len(s.Messages)
	s.invalidatePageCacheLocked()
	s.UpdatedAt = time.Now()
	return meta, nil
}

// CompactBoundaryMessage converts metadata to a system marker message.
func CompactBoundaryMessage(meta CompactMetadata) provider.Message {
	meta.Summary = strings.TrimSpace(meta.Summary)
	data, err := json.Marshal(meta)
	content := meta.Summary
	if err == nil {
		content = string(data)
	}
	return provider.Message{
		Role:    "system",
		Name:    CompactBoundaryName,
		Content: content,
	}
}

func IsCompactBoundary(msg provider.Message) bool {
	return msg.Role == "system" && msg.Name == CompactBoundaryName
}

func ParseCompactMetadata(msg provider.Message) (CompactMetadata, bool) {
	if !IsCompactBoundary(msg) {
		return CompactMetadata{}, false
	}
	var meta CompactMetadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(msg.Content)), &meta); err == nil {
		return meta, true
	}
	summary := strings.TrimSpace(msg.Content)
	if summary == "" {
		return CompactMetadata{}, false
	}
	return CompactMetadata{Summary: summary}, true
}

// CompactSegments returns immutable compact summaries in append order and the
// raw message tail not covered by any summary. Ranges count non-boundary
// provider messages and use [FromMessage, ToMessage) semantics.
func CompactSegments(messages []provider.Message) ([]CompactMetadata, []provider.Message, int) {
	if len(messages) == 0 {
		return nil, nil, 0
	}

	raw := make([]provider.Message, 0, len(messages))
	segments := make([]CompactMetadata, 0, 4)
	covered := 0
	for _, msg := range messages {
		if !IsCompactBoundary(msg) {
			raw = append(raw, msg)
			continue
		}
		meta, ok := ParseCompactMetadata(msg)
		if !ok {
			continue
		}
		// Legacy compaction folded the previous summary into each new boundary.
		// Treat it as one cumulative segment so old sessions do not inject every
		// superseded summary alongside the latest one.
		legacyRange := meta.ToMessage <= meta.FromMessage
		if legacyRange {
			segments = segments[:0]
			covered = 0
			meta.FromMessage = 0
			meta.ToMessage = len(raw)
		}
		if meta.FromMessage < covered {
			meta.FromMessage = covered
		}
		if meta.ToMessage > len(raw) {
			meta.ToMessage = len(raw)
		}
		if meta.ToMessage <= meta.FromMessage {
			// Some legacy sessions persist only the boundary marker because their
			// raw history was already removed. Keep the summary and trace visible.
			if legacyRange && strings.TrimSpace(meta.Summary) != "" {
				segments = append(segments, meta)
			}
			continue
		}
		segments = append(segments, meta)
		if meta.ToMessage > covered {
			covered = meta.ToMessage
		}
	}
	if covered > len(raw) {
		covered = len(raw)
	}
	return segments, append([]provider.Message(nil), raw[covered:]...), covered
}

// MessagesAfterLastCompactBoundary returns the logical raw tail after all
// compact segments, the latest segment metadata, and the cumulative number of
// raw messages represented by summaries.
func MessagesAfterLastCompactBoundary(messages []provider.Message) ([]provider.Message, CompactMetadata, int, bool) {
	segments, after, covered := CompactSegments(messages)
	if len(segments) == 0 {
		return messages, CompactMetadata{}, 0, false
	}
	return after, segments[len(segments)-1], covered, true
}

func (s *Session) LatestCompactTrace() (CompactTrace, bool) {
	messages := s.GetMessages()
	_, meta, _, ok := MessagesAfterLastCompactBoundary(messages)
	if !ok {
		return CompactTrace{}, false
	}
	return CompactTraceFromMetadata(meta), true
}

func CompactTraceFromMetadata(meta CompactMetadata) CompactTrace {
	return CompactTrace{
		BoundaryID:          meta.ID,
		Trigger:             meta.Trigger,
		CreatedAt:           meta.CreatedAt,
		PreTokenEstimate:    meta.PreTokenEstimate,
		PostTokenEstimate:   meta.PostTokenEstimate,
		SummaryTokens:       meta.SummaryTokens,
		DroppedMessages:     meta.DroppedMessages,
		RetainedMessages:    meta.RetainedMessages,
		RestoredAttachments: meta.RestoredAttachments,
		SummarySource:       meta.SummarySource,
		FromMessage:         meta.FromMessage,
		ToMessage:           meta.ToMessage,
		ContentHash:         meta.ContentHash,
		PolicyVersion:       meta.PolicyVersion,
	}
}

// GetMessages 获取消息（懒加载 + 滑动窗口）
// maxTurns: 最大对话轮数（0=全部），一轮 = 一条 user + 一条 assistant
func (s *Session) GetMessages(maxTurns ...int) []provider.Message {
	// 懒加载
	if err := s.loadMessages(); err != nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.Messages) == 0 {
		return nil
	}

	// 无窗口限制，返回全部
	if len(maxTurns) == 0 || maxTurns[0] <= 0 {
		cp := make([]provider.Message, len(s.Messages))
		copy(cp, s.Messages)
		return cp
	}

	window := maxTurns[0]
	// 保留最后 window*2 条消息（user+assistant 对）
	maxMsgs := window * 2
	if maxMsgs > len(s.Messages) {
		maxMsgs = len(s.Messages)
	}

	start := len(s.Messages) - maxMsgs
	// 对齐到 user 消息开头（避免从 assistant 中间截断）
	for start > 0 && s.Messages[start].Role != "user" {
		start--
	}

	cp := make([]provider.Message, len(s.Messages)-start)
	copy(cp, s.Messages[start:])
	return cp
}

const maxPageCacheMessages = 500

// GetMessagesPage returns a newest-first page without materializing the whole
// legacy session file. offset counts messages skipped from the newest end.
// The returned messages remain in chronological order, matching GetMessages.
func (s *Session) GetMessagesPage(limit, offset int) ([]provider.Message, int, bool, error) {
	if limit <= 0 {
		return nil, 0, false, fmt.Errorf("history page limit must be positive")
	}
	if offset < 0 {
		offset = 0
	}

	s.mu.RLock()
	if s.messagesLoaded {
		total := len(s.Messages)
		page, ok := selectMessagePage(s.Messages, 0, total, limit, offset)
		s.mu.RUnlock()
		if !ok {
			page = []provider.Message{}
		}
		return page, total, pageStart(total, limit, offset) > 0, nil
	}
	if s.pageCacheValid {
		page, ok := selectMessagePage(s.pageCache, s.pageCacheStart, s.pageCacheTotal, limit, offset)
		total := s.pageCacheTotal
		s.mu.RUnlock()
		if ok {
			return page, total, pageStart(total, limit, offset) > 0, nil
		}
	} else {
		s.mu.RUnlock()
	}

	// Serialize cold page reads for this session. This is the singleflight
	// boundary for legacy files: concurrent requests do not parse the same
	// multi-megabyte JSON document independently.
	s.loadMu.Lock()
	defer s.loadMu.Unlock()

	s.mu.RLock()
	if s.messagesLoaded {
		total := len(s.Messages)
		page, ok := selectMessagePage(s.Messages, 0, total, limit, offset)
		s.mu.RUnlock()
		if !ok {
			page = []provider.Message{}
		}
		return page, total, pageStart(total, limit, offset) > 0, nil
	}
	if s.pageCacheValid {
		page, ok := selectMessagePage(s.pageCache, s.pageCacheStart, s.pageCacheTotal, limit, offset)
		total := s.pageCacheTotal
		s.mu.RUnlock()
		if ok {
			return page, total, pageStart(total, limit, offset) > 0, nil
		}
	} else {
		s.mu.RUnlock()
	}

	keep := limit + offset
	if keep <= maxPageCacheMessages {
		keep = maxPageCacheMessages
	}
	tail, total, err := readSessionTail(filepath.Join(s.dir, s.ID+".md"), keep)
	if err != nil {
		// Preserve the historical behavior for malformed or externally-created
		// sessions: fall back to the normal loader, which treats them as empty.
		if loadErr := s.loadMessagesLocked(); loadErr != nil {
			return nil, 0, false, loadErr
		}
		s.mu.RLock()
		total = len(s.Messages)
		page, ok := selectMessagePage(s.Messages, 0, total, limit, offset)
		s.mu.RUnlock()
		if !ok {
			page = []provider.Message{}
		}
		return page, total, pageStart(total, limit, offset) > 0, nil
	}

	s.mu.Lock()
	if keep <= maxPageCacheMessages {
		s.pageCache = append([]provider.Message(nil), tail...)
		s.pageCacheStart = total - len(tail)
		if s.pageCacheStart < 0 {
			s.pageCacheStart = 0
		}
		s.pageCacheTotal = total
		s.pageCacheValid = true
	}
	s.mu.Unlock()

	page, ok := selectMessagePage(tail, total-len(tail), total, limit, offset)
	if !ok {
		page = []provider.Message{}
	}
	return page, total, pageStart(total, limit, offset) > 0, nil
}

func pageStart(total, limit, offset int) int {
	end := total - offset
	if end < 0 {
		end = 0
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	return start
}

func selectMessagePage(messages []provider.Message, base, total, limit, offset int) ([]provider.Message, bool) {
	start := pageStart(total, limit, offset)
	end := total - offset
	if end < 0 {
		end = 0
	}
	if start < base || end > base+len(messages) || start > end {
		return nil, false
	}
	page := make([]provider.Message, end-start)
	copy(page, messages[start-base:end-base])
	return page, true
}

func (s *Session) invalidatePageCacheLocked() {
	s.pageCache = nil
	s.pageCacheStart = 0
	s.pageCacheTotal = 0
	s.pageCacheValid = false
}

// LastMessage 获取最后一条消息
func (s *Session) LastMessage() *provider.Message {
	// 懒加载
	if err := s.loadMessages(); err != nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.Messages) == 0 {
		return nil
	}
	m := s.Messages[len(s.Messages)-1]
	return &m
}

// MessageCount 返回消息数量（不需要加载完整消息）
func (s *Session) MessageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.messageCountLocked()
}

func (s *Session) messageCountLocked() int {
	if s.messagesLoaded {
		return len(s.Messages)
	}
	return s.messageCount
}

// Save 保存会话到磁盘 (Markdown + JSON code fence)
func (s *Session) Save() error {
	// A metadata-only session must not be overwritten with an empty messages
	// array when a caller updates its title or shell context.
	if err := s.loadMessages(); err != nil {
		return err
	}

	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	s.mu.RLock()
	messages := append([]provider.Message(nil), s.Messages...)
	env := map[string]string(nil)
	if s.ShellContext.Env != nil {
		env = make(map[string]string, len(s.ShellContext.Env))
		for k, v := range s.ShellContext.Env {
			env[k] = v
		}
	}
	data := sessionData{
		ID:        s.ID,
		Title:     s.Title,
		Messages:  messages,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		ShellContext: ShellContext{
			Cwd: s.ShellContext.Cwd,
			Env: env,
		},
	}
	dir := s.dir
	id := s.ID
	s.mu.RUnlock()

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	var b strings.Builder
	b.Grow(len(jsonData) + 72)
	b.WriteString("# LuckyAgent Session\n\n")
	b.WriteString("自动生成，请勿手动编辑 JSON 块。\n\n")
	b.WriteString("```json\n")
	b.Write(jsonData)
	b.WriteString("\n```\n")

	path := filepath.Join(dir, id+".md")
	if err := utils.WriteFileAtomic(path, []byte(b.String()), 0600); err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat session: %w", err)
	}
	meta := sessionMetadata{
		ID:           data.ID,
		Title:        data.Title,
		MessageCount: len(data.Messages),
		CreatedAt:    data.CreatedAt,
		UpdatedAt:    data.UpdatedAt,
		ByteSize:     info.Size(),
		Format:       "legacy_md",
		ShellContext: data.ShellContext,
	}
	metaData, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal session metadata: %w", err)
	}
	if err := utils.WriteFileAtomic(sessionMetadataPath(dir, id), metaData, 0600); err != nil {
		return fmt.Errorf("write session metadata: %w", err)
	}
	return nil
}

// sessionData 是内部序列化格式
type sessionData struct {
	ID           string             `json:"id"`
	Title        string             `json:"title"`
	Messages     []provider.Message `json:"messages"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
	ShellContext ShellContext       `json:"shell_context"`
}

type sessionMetadata struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	MessageCount int          `json:"message_count"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	ByteSize     int64        `json:"byte_size"`
	Format       string       `json:"format"`
	ShellContext ShellContext `json:"shell_context,omitempty"`
}

func sessionMetadataPath(dir, id string) string {
	return filepath.Join(dir, id+".meta.json")
}

// SessionInfo 是会话的摘要信息（用于列表展示）
type SessionInfo struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Manager 管理多个会话
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	dir      string
}

// NewManager 创建会话管理器
func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}

	m := &Manager{
		sessions: make(map[string]*Session),
		dir:      dir,
	}

	// 加载已有会话
	if err := m.loadFromDisk(); err != nil {
		// 非致命错误：加载失败时继续使用空 map
		fmt.Printf("[session] warning: failed to load sessions from disk: %v\n", err)
	}

	return m, nil
}

// loadFromDisk 从磁盘加载所有会话（仅元数据，消息按需加载）
func (m *Manager) loadFromDisk() error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read sessions dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(m.dir, entry.Name())
		id := strings.TrimSuffix(entry.Name(), ".md")
		meta, metaOK := readSessionMetadata(m.dir, id, path)
		if !metaOK {
			// Legacy installations do not have sidecars yet. The streaming
			// reader still avoids allocating the complete messages slice.
			sd, count, readErr := readSessionFile(path, nil)
			if readErr != nil {
				continue // 跳过无法解析的文件
			}
			meta = sessionMetadata{
				ID:           sd.ID,
				Title:        sd.Title,
				MessageCount: count,
				CreatedAt:    sd.CreatedAt,
				UpdatedAt:    sd.UpdatedAt,
				Format:       "legacy_md",
			}
		}
		if meta.ID == "" {
			meta.ID = id
		}

		s := &Session{
			ID:             meta.ID,
			Title:          meta.Title,
			Messages:       nil, // 不加载消息，按需加载
			CreatedAt:      meta.CreatedAt,
			UpdatedAt:      meta.UpdatedAt,
			dir:            m.dir,
			ShellContext:   meta.ShellContext,
			messagesLoaded: false,
			messageCount:   meta.MessageCount,
		}
		m.sessions[s.ID] = s
	}

	return nil
}

// loadMessages 懒加载 session 的完整消息
func (s *Session) loadMessages() error {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	return s.loadMessagesLocked()
}

func (s *Session) loadMessagesLocked() error {
	s.mu.RLock()
	if s.messagesLoaded {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	path := filepath.Join(s.dir, s.ID+".md")
	var messages []provider.Message
	sd, _, err := readSessionFile(path, func(msg provider.Message) error {
		messages = append(messages, msg)
		return nil
	})
	sd.Messages = messages
	if err != nil {
		if os.IsNotExist(err) {
			sd.Messages = []provider.Message{}
		} else {
			// Keep the historical behavior for malformed files: callers get an
			// empty loaded session rather than a nil slice.
			sd.Messages = []provider.Message{}
		}
	}

	s.mu.Lock()
	s.Messages = sd.Messages
	if s.Messages == nil {
		s.Messages = make([]provider.Message, 0)
	}
	s.messageCount = len(s.Messages)
	s.messagesLoaded = true
	s.invalidatePageCacheLocked()
	s.mu.Unlock()
	return nil
}

func readSessionMetadata(dir, id, sessionPath string) (sessionMetadata, bool) {
	metaPath := sessionMetadataPath(dir, id)
	metaInfo, err := os.Stat(metaPath)
	if err != nil {
		return sessionMetadata{}, false
	}
	sessionInfo, err := os.Stat(sessionPath)
	if err != nil || metaInfo.ModTime().Before(sessionInfo.ModTime()) {
		return sessionMetadata{}, false
	}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return sessionMetadata{}, false
	}
	var meta sessionMetadata
	if err := json.Unmarshal(data, &meta); err != nil || meta.ID == "" {
		return sessionMetadata{}, false
	}
	return meta, true
}

func readSessionTail(path string, keep int) ([]provider.Message, int, error) {
	if keep < 1 {
		keep = 1
	}
	ring := make([]provider.Message, keep)
	seen := 0
	_, total, err := readSessionFile(path, func(msg provider.Message) error {
		ring[seen%keep] = msg
		seen++
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []provider.Message{}, 0, nil
	}
	available := total
	if available > keep {
		available = keep
	}
	tail := make([]provider.Message, available)
	start := total - available
	for i := 0; i < available; i++ {
		tail[i] = ring[(start+i)%keep]
	}
	return tail, total, nil
}

// readSessionFile streams the JSON object inside a session markdown file.
// When onMessage is nil, message values are skipped without allocating a
// provider.Message. This keeps manager startup metadata-only.
func readSessionFile(path string, onMessage func(provider.Message) error) (sessionData, int, error) {
	file, reader, err := openSessionJSON(path)
	if err != nil {
		return sessionData{}, 0, err
	}
	defer file.Close()

	decoder := json.NewDecoder(reader)
	token, err := decoder.Token()
	if err != nil {
		return sessionData{}, 0, fmt.Errorf("read session JSON: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return sessionData{}, 0, fmt.Errorf("session JSON must start with an object")
	}

	var data sessionData
	messageCount := 0
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return sessionData{}, 0, fmt.Errorf("read session field: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return sessionData{}, 0, fmt.Errorf("session field name is not a string")
		}

		switch key {
		case "id":
			if err := decoder.Decode(&data.ID); err != nil {
				return sessionData{}, 0, err
			}
		case "title":
			if err := decoder.Decode(&data.Title); err != nil {
				return sessionData{}, 0, err
			}
		case "created_at":
			if err := decoder.Decode(&data.CreatedAt); err != nil {
				return sessionData{}, 0, err
			}
		case "updated_at":
			if err := decoder.Decode(&data.UpdatedAt); err != nil {
				return sessionData{}, 0, err
			}
		case "shell_context":
			if err := decoder.Decode(&data.ShellContext); err != nil {
				return sessionData{}, 0, err
			}
		case "messages":
			arrayToken, err := decoder.Token()
			if err != nil {
				return sessionData{}, 0, err
			}
			if delimiter, ok := arrayToken.(json.Delim); !ok || delimiter != '[' {
				return sessionData{}, 0, fmt.Errorf("session messages must be an array")
			}
			for decoder.More() {
				if onMessage == nil {
					if err := skipJSONValue(decoder); err != nil {
						return sessionData{}, 0, err
					}
				} else {
					var message provider.Message
					if err := decoder.Decode(&message); err != nil {
						return sessionData{}, 0, err
					}
					if err := onMessage(message); err != nil {
						return sessionData{}, 0, err
					}
				}
				messageCount++
			}
			if _, err := decoder.Token(); err != nil {
				return sessionData{}, 0, err
			}
		default:
			if err := skipJSONValue(decoder); err != nil {
				return sessionData{}, 0, err
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return sessionData{}, 0, err
	}
	return data, messageCount, nil
}

func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
	case '[':
		for decoder.More() {
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
	}
	return err
}

func openSessionJSON(path string) (*os.File, io.Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	reader := bufio.NewReader(file)
	if peek, peekErr := reader.Peek(4096); peekErr == nil || len(peek) > 0 {
		for _, b := range peek {
			if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
				continue
			}
			if b == '{' {
				return file, reader, nil
			}
			break
		}
	}
	for {
		line, readErr := reader.ReadString('\n')
		if strings.Contains(line, "```json") {
			return file, reader, nil
		}
		if readErr != nil {
			file.Close()
			if readErr == io.EOF {
				return nil, nil, fmt.Errorf("session JSON code fence not found")
			}
			return nil, nil, readErr
		}
	}
}

// New 创建新会话
func (m *Manager) New() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.nextSessionIDLocked("")
	s := NewSession(id, m.dir)
	s.messagesLoaded = true // 新会话消息已在内存
	s.messageCount = 0
	m.sessions[id] = s
	return s
}

// NewWithTitle 创建带标题的新会话
func (m *Manager) NewWithTitle(title string) *Session {
	s := m.New()
	s.mu.Lock()
	s.Title = title
	s.mu.Unlock()
	return s
}

// Get 获取会话
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

// Ensure 获取指定 ID 的会话；若不存在则创建一个同 ID 新会话。
func (m *Manager) Ensure(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	id = m.nextSessionIDLocked(id)
	if s, ok := m.sessions[id]; ok {
		return s
	}

	s := NewSession(id, m.dir)
	s.messagesLoaded = true
	s.messageCount = 0
	m.sessions[id] = s
	return s
}

func (m *Manager) nextSessionIDLocked(preferred string) string {
	if preferred != "" {
		return preferred
	}

	for {
		id := fmt.Sprintf("%d", time.Now().UnixNano())
		if _, exists := m.sessions[id]; !exists {
			return id
		}
		time.Sleep(time.Microsecond)
	}
}

// List 列出所有会话
func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// ListInfo 列出所有会话的摘要信息（按更新时间排序）
func (m *Manager) ListInfo() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.RLock()
		infos = append(infos, SessionInfo{
			ID:           s.ID,
			Title:        s.Title,
			MessageCount: s.messageCountLocked(),
			CreatedAt:    s.CreatedAt,
			UpdatedAt:    s.UpdatedAt,
		})
		s.mu.RUnlock()
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
	})

	return infos
}

// Search 搜索包含关键词的会话
func (m *Manager) Search(query string) []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []SessionInfo
	lowerQuery := strings.ToLower(query)

	for _, s := range m.sessions {
		s.mu.RLock()
		// 搜索标题
		if strings.Contains(strings.ToLower(s.Title), lowerQuery) {
			results = append(results, SessionInfo{
				ID:           s.ID,
				Title:        s.Title,
				MessageCount: s.messageCountLocked(),
				CreatedAt:    s.CreatedAt,
				UpdatedAt:    s.UpdatedAt,
			})
			s.mu.RUnlock()
			continue
		}

		// 搜索消息内容
		for _, msg := range s.Messages {
			if strings.Contains(strings.ToLower(msg.Content), lowerQuery) {
				results = append(results, SessionInfo{
					ID:           s.ID,
					Title:        s.Title,
					MessageCount: s.messageCountLocked(),
					CreatedAt:    s.CreatedAt,
					UpdatedAt:    s.UpdatedAt,
				})
				break
			}
		}
		s.mu.RUnlock()
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})

	return results
}

// Delete 删除会话
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}

	// 删除磁盘文件
	path := filepath.Join(s.dir, s.ID+".md")
	os.Remove(path)                             // 忽略错误，文件可能不存在
	os.Remove(sessionMetadataPath(s.dir, s.ID)) // 旁路元数据同样可选

	delete(m.sessions, id)
	return nil
}

// SaveAll 保存所有会话到磁盘
func (m *Manager) SaveAll() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var errs []error
	for _, s := range m.sessions {
		if err := s.Save(); err != nil {
			errs = append(errs, fmt.Errorf("save session %s: %w", s.ID, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("save errors: %v", errs)
	}
	return nil
}

// Count 返回会话数量
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// Dir returns the backing session directory.
func (m *Manager) Dir() string {
	if m == nil {
		return ""
	}
	return m.dir
}

// Upsert registers a session object with the manager.
func (m *Manager) Upsert(s *Session) {
	if m == nil || s == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
}

func extractJSONCodeFence(md string) string {
	start := strings.Index(md, "```json")
	if start == -1 {
		return md
	}
	start += len("```json")
	rest := md[start:]
	end := strings.LastIndex(rest, "\n```")
	if end == -1 {
		end = strings.LastIndex(rest, "```")
	}
	if end == -1 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}
