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

	appservice "github.com/yurika0211/luckyagent-runtime/appservice"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/utils"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
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

type CompactAttachment = compactlib.Attachment

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
	mu sync.RWMutex
	// saveMu serializes transcript mutations, persistence and deletion. Acquire
	// it before loadMu or mu; callers already holding it use the *Locked helpers.
	saveMu         sync.Mutex
	loadMu         sync.Mutex
	ID             string
	Title          string
	recap          string
	recapUpdatedAt *time.Time
	// AgentRole and AgentSystemPrompt identify a runtime-owned worker or
	// sub-agent session and survive process restarts with the session metadata.
	AgentRole         string
	AgentSystemPrompt string
	Pinned            bool
	Project           string
	Messages          []provider.Message
	CreatedAt         time.Time
	UpdatedAt         time.Time
	dir               string
	manager           *Manager
	deleted           bool
	storageBytes      int64
	storageBytesValid bool
	// ShellContext 持久化 shell 环境（跨工具调用保持）
	ShellContext ShellContext

	// format is FormatSegmentV1 (default for new sessions) or FormatLegacyMD.
	format string
	// persistedCount is how many leading messages are already on disk for
	// segment_v1 append-only writes.
	persistedCount int
	// needsFullRewrite forces a full jsonl rewrite (message removal/reorder).
	needsFullRewrite bool

	// v0.44.0: 懒加载支持
	messagesLoaded bool // 是否已加载完整消息
	messageCount   int  // 元数据中的消息数量（未加载时使用）
	lastAccess     time.Time

	// 分页请求只缓存最近一小段消息，避免为了 history 首屏把整个旧会话
	// 常驻在内存中。完整 GetMessages 仍会按需加载全部消息。
	pageCache      []provider.Message
	pageCacheStart int
	pageCacheTotal int
	pageCacheValid bool
	pageIndex      *messagePageIndex
	// tailOffset is the byte position of message tailStart in the durable JSONL.
	// A cold page inside the latest 500 messages starts there. tailStart is the
	// message ordinal at that position.
	tailOffset int64
	tailStart  int
	tailSize   int64
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

// SetAgentProfile assigns runtime-owned role instructions for this session.
// User turns remain ordinary messages and cannot replace this profile.
func (s *Session) SetAgentProfile(role, systemPrompt string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.AgentRole = strings.TrimSpace(role)
	s.AgentSystemPrompt = strings.TrimSpace(systemPrompt)
	s.UpdatedAt = time.Now()
	s.mu.Unlock()
}

// AgentProfile returns the runtime-owned role and system instructions.
func (s *Session) AgentProfile() (role, systemPrompt string) {
	if s == nil {
		return "", ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AgentRole, s.AgentSystemPrompt
}

// SetPinned marks the session for list pinning without rewriting messages.
func (s *Session) SetPinned(pinned bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Pinned = pinned
	s.UpdatedAt = time.Now()
}

// SetProject assigns a display project. Empty clears the grouping.
func (s *Session) SetProject(project string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Project = strings.TrimSpace(project)
	s.UpdatedAt = time.Now()
}

// Organization returns the list fields that are stored beside the transcript.
func (s *Session) Organization() (pinned bool, project string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Pinned, s.Project
}

// NewSession 创建新会话
func NewSession(id, dir string) *Session {
	now := time.Now()
	return &Session{
		ID:                id,
		Messages:          make([]provider.Message, 0),
		CreatedAt:         now,
		UpdatedAt:         now,
		dir:               dir,
		format:            FormatSegmentV1,
		messagesLoaded:    true,
		storageBytesValid: true,
		lastAccess:        now,
	}
}

// Format returns the on-disk format (legacy_md or segment_v1).
func (s *Session) Format() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.format == "" {
		return FormatLegacyMD
	}
	return s.format
}

// AddProviderMessage 添加完整 provider 消息（保留 tool_calls / tool_call_id 等结构化字段）
func (s *Session) AddProviderMessage(msg provider.Message) {
	_ = s.AddProviderMessageWithError(msg)
}

// AddProviderMessageWithError appends a message after loading durable history.
// Failed loads, deleted sessions and blob write failures are reported without
// changing the transcript.
func (s *Session) AddProviderMessageWithError(msg provider.Message) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.checkLiveLocked(); err != nil {
		return err
	}
	// Metadata-only sessions must load the on-disk history before any append.
	// Treating messagesLoaded=false as an empty slice and then Save() wipes
	// the file. A load failure must not mark the session loaded.
	if err := s.loadMessages(); err != nil {
		return err
	}

	if msg.CreatedAt == nil {
		now := time.Now().UTC()
		msg.CreatedAt = &now
	}

	if msg.Role == "assistant" {
		msg.Content = utils.SanitizeToolProtocolOutput(msg.Content)
	}

	// Blob I/O outside the session mutex; externalize before appending.
	originalBlob := msg.BlobHash
	if err := s.externalizeLargeContent(&msg); err != nil {
		return fmt.Errorf("externalize session message: %w", err)
	}
	if originalBlob == "" && msg.BlobHash != "" {
		s.refreshStorageBytesLocked()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// loadMessages may have failed to observe a concurrent clear; never invent
	// an empty history here.
	if !s.messagesLoaded {
		return fmt.Errorf("session history is not loaded")
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
	return nil
}

// AddMessage 添加消息
func (s *Session) AddMessage(role, content string) {
	s.AddProviderMessage(provider.Message{Role: role, Content: content})
}

// AddMessageWithError is the error-reporting form of AddMessage.
func (s *Session) AddMessageWithError(role, content string) error {
	return s.AddProviderMessageWithError(provider.Message{Role: role, Content: content})
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
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.checkLiveLocked(); err != nil {
		return CompactMetadata{}, err
	}
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
	s.needsFullRewrite = true
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
	messages, _ := s.GetMessagesWithError(maxTurns...)
	return messages
}

// GetMessagesWithError preserves load and lifecycle errors for callers that
// must distinguish unavailable history from an empty conversation.
func (s *Session) GetMessagesWithError(maxTurns ...int) ([]provider.Message, error) {
	if s == nil {
		return nil, fmt.Errorf("session is nil")
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.checkLiveLocked(); err != nil {
		return nil, err
	}
	// 懒加载
	if err := s.loadMessages(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.touchAccessLocked()
	s.mu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.Messages) == 0 {
		return nil, nil
	}

	// 无窗口限制，返回全部
	if len(maxTurns) == 0 || maxTurns[0] <= 0 {
		cp := make([]provider.Message, len(s.Messages))
		copy(cp, s.Messages)
		return cp, nil
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
	return cp, nil
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
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.checkLiveLocked(); err != nil {
		return nil, 0, false, err
	}

	s.mu.RLock()
	useTail := s.format == FormatSegmentV1 && s.tailSize > 0 && s.pageIndex == nil
	if s.messagesLoaded && !useTail {
		total := len(s.Messages)
		page, ok := selectMessagePage(s.Messages, 0, total, limit, offset)
		s.mu.RUnlock()
		if !ok {
			page = []provider.Message{}
		}
		return page, total, pageStart(total, limit, offset) > 0, nil
	}
	if s.format == FormatSegmentV1 {
		index := s.pageIndex
		expected := s.messageCount
		tailOffset, tailStart, tailSize := s.tailOffset, s.tailStart, s.tailSize
		s.mu.RUnlock()
		s.loadMu.Lock()
		defer s.loadMu.Unlock()
		page, total, more, next, err := readMessagesJSONLPageFrom(s.messagesJSONLPath(), limit, offset, index, tailOffset, tailSize, tailStart, expected)
		if err == nil {
			// A readable file whose size no longer matches the recorded window
			// is an external replacement. A damaged or truncated record still
			// fails the read and keeps the acknowledged message count.
			replaced := false
			if tailSize > 0 && total < expected {
				if info, statErr := os.Stat(s.messagesJSONLPath()); statErr == nil && info.Size() != tailSize {
					if readable, countErr := countReadableMessagesJSONL(s.messagesJSONLPath()); countErr == nil && readable == total && next != nil {
						replaced = true
					}
				}
			}
			if total < expected && !replaced {
				return nil, 0, false, fmt.Errorf("load session history: found %d messages, expected at least %d persisted messages", total, expected)
			}
			s.mu.Lock()
			s.pageIndex = next
			if replaced {
				s.clearTailLocked()
			}
			s.messageCount = total
			s.persistedCount = total
			s.mu.Unlock()
			if next != index {
				// Rebuilds also detect external replacements or an append whose
				// metadata update was interrupted. Refresh size once per change.
				s.refreshStorageBytesLocked()
			}
			return page, total, more, nil
		}
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

	s.mu.RLock()
	format := s.format
	s.mu.RUnlock()

	var tail []provider.Message
	var total int
	var err error
	switch format {
	case FormatSegmentV1:
		tail, total, err = readMessagesJSONLTail(s.messagesJSONLPath(), keep)
	default:
		tail, total, err = readSessionTail(filepath.Join(s.dir, s.ID+".md"), keep)
	}
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
	s.pageIndex = nil
}

// LastMessage 获取最后一条消息
func (s *Session) LastMessage() *provider.Message {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.checkLiveLocked() != nil {
		return nil
	}
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

// Save 保存会话到磁盘。新会话默认 segment_v1（jsonl append + blobs）；
// 仍为 legacy_md 的旧会话在首次 Save 时迁移到 segment_v1。
func (s *Session) Save() error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	return s.saveLocked()
}

// saveLocked persists a consistent transcript while saveMu is held. Structural
// operations such as Roll and RestoreBackup use this instead of reentering Save.
func (s *Session) saveLocked() error {
	if err := s.checkLiveLocked(); err != nil {
		return err
	}
	// A metadata-only session must not be overwritten with an empty messages
	// array when a caller updates its title or shell context.
	if err := s.loadMessages(); err != nil {
		return err
	}

	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	s.mu.RLock()
	format := s.format
	persisted := s.persistedCount
	needsRewrite := s.needsFullRewrite
	if format == "" || format == FormatLegacyMD {
		format = FormatSegmentV1
		needsRewrite = true
		persisted = 0
	}
	total := len(s.Messages)
	start := persisted
	if needsRewrite || start > total {
		start = 0
	}
	// A normal append only copies and externalizes the unpersisted tail. The
	// lifecycle lock keeps the prefix stable while disk I/O runs without mu.
	messages := append([]provider.Message(nil), s.Messages[start:]...)
	title := s.Title
	recap, recapUpdatedAt := s.recap, s.recapUpdatedAt
	agentRole := s.AgentRole
	agentSystemPrompt := s.AgentSystemPrompt
	pinned := s.Pinned
	project := s.Project
	createdAt := s.CreatedAt
	updatedAt := s.UpdatedAt
	id := s.ID
	dir := s.dir
	env := map[string]string(nil)
	if s.ShellContext.Env != nil {
		env = make(map[string]string, len(s.ShellContext.Env))
		for k, v := range s.ShellContext.Env {
			env[k] = v
		}
	}
	shell := ShellContext{Cwd: s.ShellContext.Cwd, Env: env}
	s.mu.RUnlock()

	if err := s.externalizeMessages(messages); err != nil {
		return fmt.Errorf("externalize session blobs: %w", err)
	}

	var byteSize int64
	var appended jsonlAppend
	var rewritten jsonlRewrite
	var didAppend, didRewrite bool
	switch format {
	case FormatSegmentV1:
		if needsRewrite || persisted > total {
			written, err := s.rewriteMessagesJSONL(messages)
			if err != nil {
				return fmt.Errorf("rewrite segment messages: %w", err)
			}
			rewritten, didRewrite = written, true
		} else if persisted < total {
			written, err := s.appendMessagesJSONL(messages)
			if err != nil {
				return fmt.Errorf("append segment messages: %w", err)
			}
			appended, didAppend = written, true
		}
		byteSize = segmentStorageBytes(s.sessionRootDir())
		// Drop legacy single-file body after a successful segment write so
		// loadFromDisk does not register the same id twice.
		legacyPath := filepath.Join(dir, id+".md")
		_ = os.Remove(legacyPath)
	default:
		if err := s.saveLegacyMarkdownWithProfile(dir, id, title, agentRole, agentSystemPrompt, project, pinned, createdAt, updatedAt, shell, messages); err != nil {
			return err
		}
		byteSize = fileByteSize(filepath.Join(dir, id+".md"))
	}

	var tailStart int
	var tailOffset, tailSize int64
	var haveTail bool
	if didRewrite {
		tailStart, tailOffset, tailSize = rewritten.TailStart, rewritten.TailOffset, rewritten.Size
		haveTail = true
	} else if didAppend {
		tailStart, tailOffset, tailSize, haveTail = s.resolveTail(persisted, appended)
	}
	s.mu.Lock()
	copy(s.Messages[start:], messages)
	s.messageCount = total
	s.persistedCount = total
	s.needsFullRewrite = false
	s.format = format
	s.storageBytes = byteSize
	s.storageBytesValid = true
	s.invalidatePageCacheLocked()
	if s.format == FormatSegmentV1 && (didRewrite || didAppend) {
		if haveTail {
			s.tailStart = tailStart
			s.tailOffset = tailOffset
			s.tailSize = tailSize
		} else {
			s.clearTailLocked()
		}
	} else if s.format != FormatSegmentV1 {
		s.clearTailLocked()
	}
	s.mu.Unlock()

	meta := sessionMetadata{
		ID:                id,
		Title:             title,
		Recap:             recap,
		RecapUpdatedAt:    recapUpdatedAt,
		AgentRole:         agentRole,
		AgentSystemPrompt: agentSystemPrompt,
		Pinned:            pinned,
		Project:           project,
		MessageCount:      total,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		ByteSize:          byteSize,
		Format:            format,
		ShellContext:      shell,
	}
	metaData, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal session metadata: %w", err)
	}
	metaPath := sessionMetadataPath(dir, id)
	if err := utils.WriteFileAtomic(metaPath, metaData, 0600); err != nil {
		return fmt.Errorf("write session metadata: %w", err)
	}
	s.publishIndexLocked(meta, metaPath)
	if s.manager != nil {
		s.manager.mu.Lock()
		s.manager.evictLocked(s.ID)
		s.manager.mu.Unlock()
	}
	return nil
}

// resolveTail locates the latest 500-message window after an append. New
// records contribute their write positions. When the window moves into older
// records, only the messages that left the window are read from the recorded
// offset. A gap or a damaged record drops the hint.
func (s *Session) resolveTail(previous int, written jsonlAppend) (start int, offset, size int64, ok bool) {
	if len(written.Offsets) == 0 {
		return 0, 0, 0, false
	}
	last := previous + len(written.Offsets) - 1
	window := last - maxPageCacheMessages + 1
	if window < 0 {
		window = 0
	}
	if window >= previous {
		return window, written.Offsets[window-previous], written.Size, true
	}
	s.mu.RLock()
	curStart, curOffset, curSize := s.tailStart, s.tailOffset, s.tailSize
	s.mu.RUnlock()
	if window == curStart && curSize == written.Start {
		return curStart, curOffset, written.Size, true
	}
	if window == 0 && curStart == 0 && (curSize == written.Start || curSize == 0) {
		return 0, 0, written.Size, true
	}
	if curSize != written.Start || window < curStart {
		return 0, 0, 0, false
	}
	pos, err := jsonlOffsetAfter(s.messagesJSONLPath(), curOffset, window-curStart)
	if err != nil {
		return 0, 0, 0, false
	}
	return window, pos, written.Size, true
}

func (s *Session) clearTailLocked() {
	s.tailOffset = 0
	s.tailStart = 0
	s.tailSize = 0
}

// saveLegacyMarkdown preserves the historical helper signature used by
// package tests and older callers. Runtime profiles are written by the
// profile-aware helper below.
func (s *Session) saveLegacyMarkdown(dir, id, title, project string, pinned bool, createdAt, updatedAt time.Time, shell ShellContext, messages []provider.Message) error {
	return s.saveLegacyMarkdownWithProfile(dir, id, title, "", "", project, pinned, createdAt, updatedAt, shell, messages)
}

func (s *Session) saveLegacyMarkdownWithProfile(dir, id, title, agentRole, agentSystemPrompt, project string, pinned bool, createdAt, updatedAt time.Time, shell ShellContext, messages []provider.Message) error {
	data := sessionData{
		ID:                id,
		Title:             title,
		AgentRole:         agentRole,
		AgentSystemPrompt: agentSystemPrompt,
		Pinned:            pinned,
		Project:           project,
		Messages:          messages,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		ShellContext:      shell,
	}
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
	return utils.WriteFileAtomic(path, []byte(b.String()), 0600)
}

// sessionData 是内部序列化格式
type sessionData struct {
	ID                string             `json:"id"`
	Title             string             `json:"title"`
	AgentRole         string             `json:"agent_role,omitempty"`
	AgentSystemPrompt string             `json:"agent_system_prompt,omitempty"`
	Pinned            bool               `json:"pinned,omitempty"`
	Project           string             `json:"project,omitempty"`
	Messages          []provider.Message `json:"messages"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	ShellContext      ShellContext       `json:"shell_context"`
}

type sessionMetadata struct {
	Recap             string       `json:"recap,omitempty"`
	RecapUpdatedAt    *time.Time   `json:"recap_updated_at,omitempty"`
	ID                string       `json:"id"`
	Title             string       `json:"title"`
	AgentRole         string       `json:"agent_role,omitempty"`
	AgentSystemPrompt string       `json:"agent_system_prompt,omitempty"`
	Pinned            bool         `json:"pinned,omitempty"`
	Project           string       `json:"project,omitempty"`
	MessageCount      int          `json:"message_count"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
	ByteSize          int64        `json:"byte_size"`
	Format            string       `json:"format"`
	ShellContext      ShellContext `json:"shell_context,omitempty"`
}

func sessionMetadataPath(dir, id string) string {
	return filepath.Join(dir, id+".meta.json")
}

// SessionInfo 是会话的摘要信息（用于列表展示）
type SessionInfo = appservice.SessionInfo

// Manager 管理多个会话
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	dir      string
	index    *sessionIndex
	evictMax int
	evictTTL time.Duration
}

// NewManager 创建会话管理器
func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}

	m := &Manager{
		sessions: make(map[string]*Session),
		dir:      dir,
		evictMax: defaultSessionEvictMax,
		evictTTL: defaultSessionEvictIdle,
	}
	if index, err := openSessionIndex(dir); err != nil {
		fmt.Printf("[session] warning: session index unavailable, scanning files: %v\n", err)
	} else {
		m.index = index
	}

	// 加载已有会话
	if err := m.loadFromDisk(); err != nil {
		// 非致命错误：加载失败时继续使用空 map
		fmt.Printf("[session] warning: failed to load sessions from disk: %v\n", err)
	}

	return m, nil
}

// loadFromDisk 从磁盘加载所有会话（仅元数据，消息按需加载）。
// A fresh index row stands in for its sidecar. A changed source file is read
// and the row is corrected. Sessions missing from the index are inserted.
func (m *Manager) loadFromDisk() error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read sessions dir: %w", err)
	}

	indexed := map[string]indexedSession{}
	if m.index != nil {
		loaded, err := m.index.List()
		if err != nil {
			fmt.Printf("[session] warning: session index unreadable, scanning files: %v\n", err)
		} else {
			indexed = loaded
		}
	}
	seen := make(map[string]struct{})

	// Prefer segment_v1 sessions discovered via meta sidecars or data dirs.
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".meta.json") {
			id := strings.TrimSuffix(name, ".meta.json")
			if id == "" || strings.HasPrefix(id, ".") {
				continue
			}
			metaPath := filepath.Join(m.dir, name)
			size, mod, ok := sourceFingerprint(metaPath)
			if !ok {
				continue
			}
			row, fresh := indexed[id]
			var meta sessionMetadata
			if fresh && indexRowFresh(row, size, mod) {
				meta = row.Meta
			} else {
				data, readErr := os.ReadFile(metaPath)
				if readErr != nil {
					continue
				}
				if json.Unmarshal(data, &meta) != nil || meta.ID == "" {
					meta.ID = id
				}
			}
			if meta.ID == "" {
				meta.ID = id
			}
			format := meta.Format
			if format == "" {
				if _, err := os.Stat(filepath.Join(m.dir, id, messagesJSONLName)); err == nil {
					format = FormatSegmentV1
				} else if _, err := os.Stat(filepath.Join(m.dir, id+".md")); err == nil {
					format = FormatLegacyMD
				} else {
					continue
				}
			}
			if format == FormatSegmentV1 {
				if meta.MessageCount == 0 {
					if n, err := countMessagesJSONL(filepath.Join(m.dir, id, messagesJSONLName)); err == nil {
						meta.MessageCount = n
					}
				}
				meta.Format = FormatSegmentV1
				stub := m.newSessionStub(meta, FormatSegmentV1)
				if fresh && indexRowFresh(row, size, mod) {
					stub.tailOffset = row.TailOffset
					stub.tailStart = row.TailStart
					stub.tailSize = row.FileSize
				}
				m.sessions[meta.ID] = stub
				seen[meta.ID] = struct{}{}
				m.correctIndex(meta, metaPath, size, mod, stub.tailOffset, stub.tailSize, fresh && indexRowFresh(row, size, mod))
			}
			continue
		}
		if entry.IsDir() && !strings.HasPrefix(name, ".") {
			jsonl := filepath.Join(m.dir, name, messagesJSONLName)
			if _, err := os.Stat(jsonl); err != nil {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			meta, metaOK := readSessionMetadataLoose(m.dir, name)
			if !metaOK {
				n, _ := countMessagesJSONL(jsonl)
				meta = sessionMetadata{
					ID:           name,
					MessageCount: n,
					Format:       FormatSegmentV1,
					UpdatedAt:    time.Now(),
					CreatedAt:    time.Now(),
				}
			}
			if meta.ID == "" {
				meta.ID = name
			}
			meta.Format = FormatSegmentV1
			m.sessions[meta.ID] = m.newSessionStub(meta, FormatSegmentV1)
			seen[meta.ID] = struct{}{}
			source := sessionMetadataPath(m.dir, name)
			size, mod, ok := sourceFingerprint(source)
			if ok {
				row, fresh := indexed[meta.ID]
				m.correctIndex(meta, source, size, mod, 0, 0, fresh && indexRowFresh(row, size, mod))
			}
		}
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(m.dir, entry.Name())
		id := strings.TrimSuffix(entry.Name(), ".md")
		if _, ok := seen[id]; ok {
			continue
		}
		size, mod, ok := sourceFingerprint(path)
		if !ok {
			continue
		}
		row, fresh := indexed[id]
		var meta sessionMetadata
		if fresh && indexRowFresh(row, size, mod) && row.Meta.Format == FormatLegacyMD {
			meta = row.Meta
		} else {
			var metaOK bool
			meta, metaOK = readSessionMetadata(m.dir, id, path)
			if !metaOK {
				// Legacy installations do not have sidecars yet. The streaming
				// reader still avoids allocating the complete messages slice.
				sd, count, readErr := readSessionFile(path, nil)
				if readErr != nil {
					continue // 跳过无法解析的文件
				}
				meta = sessionMetadata{
					ID:                sd.ID,
					Title:             sd.Title,
					AgentRole:         sd.AgentRole,
					AgentSystemPrompt: sd.AgentSystemPrompt,
					Pinned:            sd.Pinned,
					Project:           sd.Project,
					ShellContext:      sd.ShellContext,
					MessageCount:      count,
					CreatedAt:         sd.CreatedAt,
					UpdatedAt:         sd.UpdatedAt,
					ByteSize:          fileByteSize(path),
					Format:            FormatLegacyMD,
				}
			}
		}
		if meta.ID == "" {
			meta.ID = id
		}
		if meta.Format == FormatSegmentV1 {
			// Meta claims segment but we already skipped if registered; treat as segment.
			m.sessions[meta.ID] = m.newSessionStub(meta, FormatSegmentV1)
			seen[meta.ID] = struct{}{}
			continue
		}
		meta.Format = FormatLegacyMD
		m.sessions[meta.ID] = m.newSessionStub(meta, FormatLegacyMD)
		seen[meta.ID] = struct{}{}
		m.correctIndex(meta, path, size, mod, 0, 0, fresh && indexRowFresh(row, size, mod) && row.Meta.Format == FormatLegacyMD)
	}

	for id := range indexed {
		if _, ok := seen[id]; ok {
			continue
		}
		// A row whose source file is gone is stale. Keep the row when the
		// source still exists but this scan skipped it for another reason.
		if _, _, ok := sourceFingerprint(sessionMetadataPath(m.dir, id)); ok {
			continue
		}
		if _, _, ok := sourceFingerprint(filepath.Join(m.dir, id+".md")); ok {
			continue
		}
		if m.index != nil {
			_ = m.index.Delete(id)
		}
	}

	return nil
}

func (m *Manager) correctIndex(meta sessionMetadata, source string, size int64, mod time.Time, tailOffset, fileSize int64, fresh bool) {
	if m == nil || m.index == nil || fresh {
		return
	}
	_ = m.index.Upsert(indexedSession{
		Meta:       meta,
		SourceSize: size,
		SourceTime: mod,
		TailOffset: tailOffset,
		FileSize:   fileSize,
	})
	_ = source
}

func (m *Manager) newSessionStub(meta sessionMetadata, format string) *Session {
	if format == "" {
		format = meta.Format
	}
	if format == "" {
		format = FormatLegacyMD
	}
	if meta.ByteSize == 0 {
		if format == FormatSegmentV1 {
			meta.ByteSize = segmentStorageBytes(filepath.Join(m.dir, meta.ID))
		} else {
			meta.ByteSize = fileByteSize(filepath.Join(m.dir, meta.ID+".md"))
		}
	}
	return &Session{
		ID:                meta.ID,
		Title:             meta.Title,
		recap:             meta.Recap,
		recapUpdatedAt:    meta.RecapUpdatedAt,
		AgentRole:         meta.AgentRole,
		AgentSystemPrompt: meta.AgentSystemPrompt,
		Pinned:            meta.Pinned,
		Project:           meta.Project,
		Messages:          nil,
		CreatedAt:         meta.CreatedAt,
		UpdatedAt:         meta.UpdatedAt,
		dir:               m.dir,
		manager:           m,
		ShellContext:      meta.ShellContext,
		format:            format,
		messagesLoaded:    false,
		messageCount:      meta.MessageCount,
		persistedCount:    meta.MessageCount,
		storageBytes:      meta.ByteSize,
		storageBytesValid: true,
	}
}

func readSessionMetadataLoose(dir, id string) (sessionMetadata, bool) {
	metaPath := sessionMetadataPath(dir, id)
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
	format := s.format
	expected := s.messageCount
	s.mu.RUnlock()

	var messages []provider.Message
	fromLegacy := false

	switch format {
	case FormatSegmentV1:
		path := s.messagesJSONLPath()
		loaded, err := readAllMessagesJSONL(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("load segment messages: %w", err)
			}
			legacy, legacyErr := s.loadLegacyMessages()
			if legacyErr != nil {
				if os.IsNotExist(legacyErr) {
					if expected > 0 {
						return fmt.Errorf("load segment messages: %w", err)
					}
					messages = []provider.Message{}
				} else {
					return fmt.Errorf("load legacy messages: %w", legacyErr)
				}
			} else {
				messages = legacy
				fromLegacy = true
			}
		} else if len(loaded) == 0 {
			// Mid-migrate or empty segment: prefer legacy body if present.
			legacy, legacyErr := s.loadLegacyMessages()
			if legacyErr != nil {
				if os.IsNotExist(legacyErr) {
					if expected > 0 {
						return fmt.Errorf("load segment messages: empty transcript for %d persisted messages", expected)
					}
					messages = loaded
				} else {
					return fmt.Errorf("load legacy messages: %w", legacyErr)
				}
			} else if len(legacy) > 0 {
				messages = legacy
				fromLegacy = true
			} else {
				messages = loaded
			}
		} else {
			messages = loaded
		}
	default:
		loaded, _, err := s.loadLegacyMessagesWithMeta()
		if err != nil {
			if os.IsNotExist(err) {
				if expected > 0 {
					return fmt.Errorf("load legacy messages: %w", err)
				}
				messages = []provider.Message{}
			} else {
				return fmt.Errorf("load legacy messages: %w", err)
			}
		} else {
			messages = loaded
		}
	}

	if len(messages) < expected {
		return fmt.Errorf("load session history: found %d messages, expected at least %d persisted messages", len(messages), expected)
	}
	s.mu.Lock()
	// Metadata is initialized when the Manager creates the stub. Reading the
	// transcript must not roll back setters applied before the first lazy load.
	s.Messages = messages
	if s.Messages == nil {
		s.Messages = make([]provider.Message, 0)
	}
	s.messageCount = len(s.Messages)
	s.persistedCount = len(s.Messages)
	if fromLegacy {
		s.persistedCount = 0
		s.needsFullRewrite = true
	}
	s.messagesLoaded = true
	s.lastAccess = time.Now()
	s.invalidatePageCacheLocked()
	s.mu.Unlock()
	return nil
}

const (
	defaultSessionEvictIdle = 2 * time.Minute
	defaultSessionEvictMax  = 8
)

// Unload drops a fully loaded transcript when it is already persisted.
// In-memory-only sessions (never saved, or pending rewrite) stay resident.
func (s *Session) Unload() bool {
	if s == nil {
		return false
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	return s.unloadLocked()
}

func (s *Session) unloadLocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.messagesLoaded {
		return false
	}
	if s.needsFullRewrite || s.persistedCount != len(s.Messages) {
		return false
	}
	s.Messages = nil
	s.messagesLoaded = false
	s.invalidatePageCacheLocked()
	return true
}

func (s *Session) touchAccessLocked() {
	s.lastAccess = time.Now()
}

func (s *Session) idleSinceLocked(now time.Time) time.Duration {
	if s.lastAccess.IsZero() {
		return 0
	}
	return now.Sub(s.lastAccess)
}

func (s *Session) loadLegacyMessages() ([]provider.Message, error) {
	messages, _, err := s.loadLegacyMessagesWithMeta()
	return messages, err
}

func (s *Session) loadLegacyMessagesWithMeta() ([]provider.Message, sessionData, error) {
	path := filepath.Join(s.dir, s.ID+".md")
	var messages []provider.Message
	sd, _, err := readSessionFile(path, func(msg provider.Message) error {
		messages = append(messages, msg)
		return nil
	})
	if err != nil {
		return nil, sessionData{}, err
	}
	if messages == nil {
		messages = []provider.Message{}
	}
	return messages, sd, nil
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
		case "pinned":
			if err := decoder.Decode(&data.Pinned); err != nil {
				return sessionData{}, 0, err
			}
		case "project":
			if err := decoder.Decode(&data.Project); err != nil {
				return sessionData{}, 0, err
			}
		case "agent_role":
			if err := decoder.Decode(&data.AgentRole); err != nil {
				return sessionData{}, 0, err
			}
		case "agent_system_prompt":
			if err := decoder.Decode(&data.AgentSystemPrompt); err != nil {
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
	s.manager = m
	s.messagesLoaded = true // 新会话消息已在内存
	s.messageCount = 0
	s.lastAccess = time.Now()
	m.sessions[id] = s
	m.evictLocked(s.ID)
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
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if ok && s != nil {
		s.mu.Lock()
		s.touchAccessLocked()
		s.mu.Unlock()
	}
	return s, ok
}

// Ensure 获取指定 ID 的会话；若不存在则创建一个同 ID 新会话。
func (m *Manager) Ensure(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	id = m.nextSessionIDLocked(id)
	if s, ok := m.sessions[id]; ok {
		s.mu.Lock()
		s.touchAccessLocked()
		s.mu.Unlock()
		return s
	}

	s := NewSession(id, m.dir)
	s.manager = m
	s.messagesLoaded = true
	s.messageCount = 0
	s.lastAccess = time.Now()
	m.sessions[id] = s
	m.evictLocked(s.ID)
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
		format := s.format
		if format == "" {
			format = FormatLegacyMD
		}
		id, title, agentRole, pinned, project, count := s.ID, s.Title, s.AgentRole, s.Pinned, s.Project, s.messageCountLocked()
		created, updated, byteSize := s.CreatedAt, s.UpdatedAt, s.storageBytes
		recap, recapUpdatedAt := s.recap, cloneRecapTime(s.recapUpdatedAt)
		s.mu.RUnlock()
		infos = append(infos, SessionInfo{
			ID:             id,
			Title:          title,
			Recap:          recap,
			RecapUpdatedAt: recapUpdatedAt,
			AgentRole:      agentRole,
			Pinned:         pinned,
			Project:        project,
			MessageCount:   count,
			ByteSize:       byteSize,
			Format:         format,
			CreatedAt:      created,
			UpdatedAt:      updated,
		})
	}

	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Pinned != infos[j].Pinned {
			return infos[i].Pinned
		}
		return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
	})

	return infos
}

// Search 搜索包含关键词的会话
func sessionContentMatches(s *Session, lowerQuery string) bool {
	if s == nil || lowerQuery == "" {
		return false
	}
	s.mu.RLock()
	format := s.format
	dir := s.dir
	id := s.ID
	s.mu.RUnlock()
	switch format {
	case FormatSegmentV1:
		path := filepath.Join(dir, id, messagesJSONLName)
		return fileContainsFold(path, lowerQuery)
	default:
		return fileContainsFold(filepath.Join(dir, id+".md"), lowerQuery)
	}
}

func fileContainsFold(path, lowerQuery string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 8*1024*1024)
	for sc.Scan() {
		if strings.Contains(strings.ToLower(sc.Text()), lowerQuery) {
			return true
		}
	}
	return false
}

func (m *Manager) Search(query string) []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []SessionInfo
	lowerQuery := strings.ToLower(query)

	for _, s := range m.sessions {
		s.mu.RLock()
		info := SessionInfo{
			ID:             s.ID,
			Title:          s.Title,
			Recap:          s.recap,
			RecapUpdatedAt: cloneRecapTime(s.recapUpdatedAt),
			AgentRole:      s.AgentRole,
			Pinned:         s.Pinned,
			Project:        s.Project,
			MessageCount:   s.messageCountLocked(),
			CreatedAt:      s.CreatedAt,
			UpdatedAt:      s.UpdatedAt,
		}
		loaded := s.messagesLoaded
		messages := append([]provider.Message(nil), s.Messages...)
		matched := strings.Contains(strings.ToLower(s.Title), lowerQuery) ||
			strings.Contains(strings.ToLower(s.Project), lowerQuery) ||
			strings.Contains(strings.ToLower(s.ID), lowerQuery)
		s.mu.RUnlock()
		if !matched {
			if loaded {
				for _, msg := range messages {
					if strings.Contains(strings.ToLower(msg.Content), lowerQuery) {
						matched = true
						break
					}
				}
			} else if sessionContentMatches(s, lowerQuery) {
				matched = true
			}
		}
		if matched {
			results = append(results, info)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Pinned != results[j].Pinned {
			return results[i].Pinned
		}
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})

	return results
}

// SaveMetadata writes title, pin, and project without rewriting the transcript.
func (s *Session) SaveMetadata() error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	return s.saveMetadataLocked()
}

// Delete 删除会话
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.checkLiveLocked(); err != nil {
		return err
	}

	// 删除磁盘文件
	if err := os.RemoveAll(filepath.Join(s.dir, s.ID)); err != nil {
		return fmt.Errorf("delete session data: %w", err)
	}
	path := filepath.Join(s.dir, s.ID+".md")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete legacy session: %w", err)
	}
	if err := os.Remove(sessionMetadataPath(s.dir, s.ID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete session metadata: %w", err)
	}
	s.mu.Lock()
	s.deleted = true
	s.storageBytes = 0
	s.invalidatePageCacheLocked()
	s.mu.Unlock()

	delete(m.sessions, id)
	// The files are already gone. A stale index row is removed on the next
	// startup scan, so an index failure does not report a failed deletion.
	if m.index != nil {
		_ = m.index.Delete(id)
	}
	return nil
}

// SaveAll 保存所有会话到磁盘
func (m *Manager) SaveAll() error {
	var errs []error
	for _, s := range m.List() {
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

// ResidentStats reports how many transcripts are currently materialized.
func (m *Manager) ResidentStats() (loaded int, bytes int64) {
	if m == nil {
		return 0, 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		if s == nil {
			continue
		}
		s.mu.RLock()
		if s.messagesLoaded {
			loaded++
			for _, msg := range s.Messages {
				bytes += int64(len(msg.Content))
			}
		}
		s.mu.RUnlock()
	}
	return loaded, bytes
}

func (m *Manager) evictLocked(keepID string) {
	if m == nil {
		return
	}
	maxLoaded := m.evictMax
	if maxLoaded <= 0 {
		maxLoaded = defaultSessionEvictMax
	}
	ttl := m.evictTTL
	if ttl <= 0 {
		ttl = defaultSessionEvictIdle
	}
	now := time.Now()
	type candidate struct {
		id    string
		idle  time.Duration
		bytes int
	}
	loaded := make([]candidate, 0)
	for id, s := range m.sessions {
		if s == nil || id == keepID {
			continue
		}
		s.mu.RLock()
		ok := s.messagesLoaded && !s.needsFullRewrite && s.persistedCount == len(s.Messages)
		idle := s.idleSinceLocked(now)
		n := len(s.Messages)
		s.mu.RUnlock()
		if !ok {
			continue
		}
		loaded = append(loaded, candidate{id: id, idle: idle, bytes: n})
	}
	if len(loaded) < maxLoaded {
		return
	}
	sort.Slice(loaded, func(i, j int) bool {
		if loaded[i].idle == loaded[j].idle {
			return loaded[i].bytes > loaded[j].bytes
		}
		return loaded[i].idle > loaded[j].idle
	})
	overflow := len(loaded) - maxLoaded + 1
	for i := 0; i < len(loaded) && overflow > 0; i++ {
		if loaded[i].idle < ttl && i >= overflow {
			continue
		}
		s := m.sessions[loaded[i].id]
		if s == nil {
			continue
		}
		if s.tryUnload() {
			overflow--
		}
	}
}

func (s *Session) tryUnload() bool {
	if s == nil {
		return false
	}
	if !s.saveMu.TryLock() {
		return false
	}
	defer s.saveMu.Unlock()
	return s.unloadLocked()
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
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.checkLiveLocked() != nil {
		return
	}
	s.mu.RLock()
	knownSize := s.storageBytesValid
	s.mu.RUnlock()
	if !knownSize {
		s.refreshStorageBytesLocked()
	}
	s.manager = m
	m.sessions[s.ID] = s
	m.evictLocked(s.ID)
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
