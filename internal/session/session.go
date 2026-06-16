package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/utils"
)

// Session 代表一次对话会话
type Session struct {
	mu        sync.RWMutex
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
		ID:        id,
		Messages:  make([]provider.Message, 0),
		CreatedAt: now,
		UpdatedAt: now,
		dir:       dir,
	}
}

// AddProviderMessage 添加完整 provider 消息（保留 tool_calls / tool_call_id 等结构化字段）
func (s *Session) AddProviderMessage(msg provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	b.WriteString("# LuckyHarness Session\n\n")
	b.WriteString("自动生成，请勿手动编辑 JSON 块。\n\n")
	b.WriteString("```json\n")
	b.Write(jsonData)
	b.WriteString("\n```\n")

	path := filepath.Join(dir, id+".md")
	return os.WriteFile(path, []byte(b.String()), 0600)
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
		data, err := os.ReadFile(path)
		if err != nil {
			continue // 跳过无法读取的文件
		}

		raw := extractJSONCodeFence(string(data))
		var sd sessionData
		if err := json.Unmarshal([]byte(raw), &sd); err != nil {
			continue // 跳过无法解析的文件
		}

		s := &Session{
			ID:             sd.ID,
			Title:          sd.Title,
			Messages:       nil, // 不加载消息，按需加载
			CreatedAt:      sd.CreatedAt,
			UpdatedAt:      sd.UpdatedAt,
			dir:            m.dir,
			ShellContext:   sd.ShellContext,
			messagesLoaded: false,
			messageCount:   len(sd.Messages),
		}
		m.sessions[s.ID] = s
	}

	return nil
}

// loadMessages 懒加载 session 的完整消息
func (s *Session) loadMessages() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.messagesLoaded {
		return nil
	}

	path := filepath.Join(s.dir, s.ID+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		// 文件不存在时用空消息
		s.Messages = make([]provider.Message, 0)
		s.messagesLoaded = true
		return nil
	}

	raw := extractJSONCodeFence(string(data))
	var sd sessionData
	if err := json.Unmarshal([]byte(raw), &sd); err != nil {
		s.Messages = make([]provider.Message, 0)
		s.messagesLoaded = true
		return nil
	}

	s.Messages = sd.Messages
	if s.Messages == nil {
		s.Messages = make([]provider.Message, 0)
	}
	s.messagesLoaded = true
	return nil
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
	os.Remove(path) // 忽略错误，文件可能不存在

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
