package memory

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// --- 中期记忆：会话级持久化 ---
//
// 核心思路（参考 yurika0408.icu/post/301）：
// 1. 每次会话结束时生成结构化摘要（SessionSummary）
// 2. 新会话开始时，用当前查询检索最相关的历史会话摘要注入上下文
// 3. 按时间衰减排序，3个月前的调试会话降级
// 4. 摘要用结构化模板约束输出

// SessionSummary 会话摘要结构体
type SessionSummary struct {
	SessionID     string    `json:"session_id"`
	UserID        string    `json:"user_id"`
	CreatedAt     time.Time `json:"created_at"`
	Topics        []string  `json:"topics"`
	KeyDecisions  []string  `json:"key_decisions"`
	OpenQuestions []string  `json:"open_questions"`
	CodeContext   string    `json:"code_context"`
	RawSummary    string    `json:"raw_summary"`
}

// MidTermStore 中期记忆存储
type MidTermStore struct {
	mu           sync.RWMutex
	summaries    map[string]*SessionSummary // key: session ID
	paths        map[string]string          // key: session ID, value: relative note path
	dir          string
	maxSummaries int // 最大摘要数量（默认 100）
}

// NewMidTermStore 创建中期记忆存储
func NewMidTermStore(dir string, maxSummaries int) (*MidTermStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create midterm dir: %w", err)
	}
	if maxSummaries <= 0 {
		maxSummaries = 100
	}
	s := &MidTermStore{
		summaries:    make(map[string]*SessionSummary),
		paths:        make(map[string]string),
		dir:          dir,
		maxSummaries: maxSummaries,
	}
	if err := s.load(); err != nil {
		// 加载失败不阻塞，使用空 store
		fmt.Printf("[midterm] warning: failed to load: %v\n", err)
	}
	return s, nil
}

// SaveSessionSummary 保存会话摘要
func (s *MidTermStore) SaveSessionSummary(summary *SessionSummary) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if summary.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	normalizeSessionSummaryForSave(summary)
	if strings.TrimSpace(summary.RawSummary) == "" &&
		len(summary.Topics) == 0 &&
		len(summary.KeyDecisions) == 0 &&
		len(summary.OpenQuestions) == 0 &&
		strings.TrimSpace(summary.CodeContext) == "" {
		return nil
	}

	// 如果已存在同 ID，合并
	if existing, ok := s.summaries[summary.SessionID]; ok {
		s.mergeSummary(existing, summary)
		normalizeSessionSummaryForSave(existing)
	} else {
		s.summaries[summary.SessionID] = summary
	}

	// 超过最大数量时，删除最旧的
	if len(s.summaries) > s.maxSummaries {
		s.evictOldest()
	}

	return s.persist()
}

// mergeSummary 合并摘要（保留更丰富的信息）
func (s *MidTermStore) mergeSummary(existing, newSummary *SessionSummary) {
	// 合并 topics
	existing.Topics = mergeStringSlices(existing.Topics, newSummary.Topics)
	// 合并 key_decisions
	existing.KeyDecisions = mergeStringSlices(existing.KeyDecisions, newSummary.KeyDecisions)
	// 合并 open_questions
	existing.OpenQuestions = mergeStringSlices(existing.OpenQuestions, newSummary.OpenQuestions)
	// 更新 code_context（取更长的）
	if len(newSummary.CodeContext) > len(existing.CodeContext) {
		existing.CodeContext = newSummary.CodeContext
	}
	// 更新 raw_summary（取更新的）
	if newSummary.CreatedAt.After(existing.CreatedAt) {
		existing.RawSummary = newSummary.RawSummary
		existing.CreatedAt = newSummary.CreatedAt
	}
}

// mergeStringSlices 合并两个字符串切片，去重
func mergeStringSlices(a, b []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// SearchSummaries 关键词+时间衰减混合检索
func (s *MidTermStore) SearchSummaries(query string, topK int) []SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if topK <= 0 {
		topK = 3
	}

	now := time.Now()
	queryLower := strings.ToLower(query)
	queryWords := strings.Fields(queryLower)

	type scoredSummary struct {
		summary SessionSummary
		score   float64
	}

	var scored []scoredSummary

	for _, sm := range s.summaries {
		score := s.computeSearchScore(sm, queryLower, queryWords, now)
		if score > 0 {
			scored = append(scored, scoredSummary{summary: *sm, score: score})
		}
	}

	// 按综合分降序排序
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// 返回 topK
	if len(scored) < topK {
		topK = len(scored)
	}

	results := make([]SessionSummary, topK)
	for i := 0; i < topK; i++ {
		results[i] = scored[i].summary
	}

	return results
}

// computeSearchScore 计算搜索得分 = 关键词匹配 × 时间衰减
func (s *MidTermStore) computeSearchScore(sm *SessionSummary, queryLower string, queryWords []string, now time.Time) float64 {
	matchScore := 0.0

	// 搜索 raw_summary
	rawLower := strings.ToLower(sm.RawSummary)
	if strings.Contains(rawLower, queryLower) {
		matchScore += 2.0
	}
	for _, w := range queryWords {
		if strings.Contains(rawLower, w) {
			matchScore += 0.5
		}
	}

	// 搜索 topics
	for _, topic := range sm.Topics {
		topicLower := strings.ToLower(topic)
		if strings.Contains(topicLower, queryLower) {
			matchScore += 1.5
		}
		for _, w := range queryWords {
			if strings.Contains(topicLower, w) {
				matchScore += 0.3
			}
		}
	}

	// 搜索 key_decisions
	for _, d := range sm.KeyDecisions {
		dLower := strings.ToLower(d)
		if strings.Contains(dLower, queryLower) {
			matchScore += 1.0
		}
		for _, w := range queryWords {
			if strings.Contains(dLower, w) {
				matchScore += 0.2
			}
		}
	}

	// 搜索 code_context
	codeLower := strings.ToLower(sm.CodeContext)
	if strings.Contains(codeLower, queryLower) {
		matchScore += 1.0
	}
	for _, w := range queryWords {
		if strings.Contains(codeLower, w) {
			matchScore += 0.2
		}
	}

	// 搜索 open_questions
	for _, q := range sm.OpenQuestions {
		qLower := strings.ToLower(q)
		if strings.Contains(qLower, queryLower) {
			matchScore += 0.8
		}
		for _, w := range queryWords {
			if strings.Contains(qLower, w) {
				matchScore += 0.2
			}
		}
	}

	if matchScore == 0 {
		return 0
	}

	// 时间衰减：90天半衰期
	ageHours := now.Sub(sm.CreatedAt).Hours()
	halflifeHours := 24.0 * 90 // 90 天
	decay := math.Pow(0.5, ageHours/halflifeHours)

	return matchScore * decay
}

// ExpireOldSummaries 过期清理：删除超过指定时间的摘要
func (s *MidTermStore) ExpireOldSummaries(olderThan time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-olderThan)
	var toDelete []string

	for id, sm := range s.summaries {
		if sm.CreatedAt.Before(cutoff) {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		s.removeSummaryFileLocked(id)
		delete(s.summaries, id)
	}

	if len(toDelete) > 0 {
		s.persist()
	}

	return len(toDelete)
}

// Get 获取指定会话的摘要
func (s *MidTermStore) Get(sessionID string) (*SessionSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sm, ok := s.summaries[sessionID]
	if !ok {
		return nil, false
	}
	cp := *sm
	return &cp, true
}

// Count 返回摘要数量
func (s *MidTermStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.summaries)
}

// ListAll 返回所有摘要（按创建时间降序）
func (s *MidTermStore) ListAll() []SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]SessionSummary, 0, len(s.summaries))
	for _, sm := range s.summaries {
		result = append(result, *sm)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	return result
}

// Delete 删除指定会话的摘要
func (s *MidTermStore) Delete(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.summaries[sessionID]; !ok {
		return fmt.Errorf("summary not found: %s", sessionID)
	}

	s.removeSummaryFileLocked(sessionID)
	delete(s.summaries, sessionID)
	return s.persist()
}

// evictOldest 驱逐最旧的摘要（调用方需持有锁）
func (s *MidTermStore) evictOldest() {
	var oldestID string
	var oldestTime time.Time

	for id, sm := range s.summaries {
		if oldestID == "" || sm.CreatedAt.Before(oldestTime) {
			oldestID = id
			oldestTime = sm.CreatedAt
		}
	}

	if oldestID != "" {
		s.removeSummaryFileLocked(oldestID)
		delete(s.summaries, oldestID)
	}
}

func (s *MidTermStore) persist() error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create midterm dir: %w", err)
	}
	for _, sm := range s.summaries {
		normalizeSessionSummaryForSave(sm)
		if sm.CreatedAt.IsZero() {
			sm.CreatedAt = time.Now()
		}
		rel := s.paths[sm.SessionID]
		if rel == "" {
			rel = sessionNotePath(sm)
			s.paths[sm.SessionID] = rel
		}
		path := filepath.Join(s.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return fmt.Errorf("create session note dir: %w", err)
		}
		if err := os.WriteFile(path, []byte(renderSessionSummaryNote(sm)), 0600); err != nil {
			return fmt.Errorf("write session note %s: %w", path, err)
		}
	}
	return nil
}

func normalizeSessionSummaryForSave(sm *SessionSummary) {
	if sm == nil {
		return
	}
	sm.SessionID = strings.TrimSpace(sm.SessionID)
	sm.UserID = strings.TrimSpace(sm.UserID)
	sm.Topics = sanitizeSummarySlice(sm.Topics)
	sm.KeyDecisions = sanitizeSummarySlice(sm.KeyDecisions)
	sm.OpenQuestions = sanitizeSummarySlice(sm.OpenQuestions)
	sm.CodeContext = sanitizeDurableMemoryContent(sm.CodeContext)
	sm.RawSummary = sanitizeDurableMemoryContent(sm.RawSummary)
}

func sanitizeSummarySlice(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = sanitizeDurableMemoryContent(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return dedupSlice(out)
}

func (s *MidTermStore) load() error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create midterm dir: %w", err)
	}
	return filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		sm, ok, err := parseSessionSummaryNote(path, s.dir)
		if err != nil {
			return fmt.Errorf("parse session note %s: %w", path, err)
		}
		if !ok {
			return nil
		}
		s.summaries[sm.SessionID] = sm
		rel, err := filepath.Rel(s.dir, path)
		if err != nil {
			rel = path
		}
		s.paths[sm.SessionID] = filepath.ToSlash(rel)
		return nil
	})
}

type sessionSummaryFrontmatter struct {
	ID            string    `yaml:"id"`
	Type          string    `yaml:"type"`
	UserID        string    `yaml:"user_id,omitempty"`
	CreatedAt     time.Time `yaml:"created_at"`
	Topics        []string  `yaml:"topics,omitempty"`
	KeyDecisions  []string  `yaml:"key_decisions,omitempty"`
	OpenQuestions []string  `yaml:"open_questions,omitempty"`
	CodeContext   string    `yaml:"code_context,omitempty"`
	Status        string    `yaml:"status,omitempty"`
	Tags          []string  `yaml:"tags,omitempty"`
}

func sessionNotePath(sm *SessionSummary) string {
	created := sm.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	slug := slugify(strings.Join(sm.Topics, "-"))
	if slug == "" {
		slug = slugify(sm.SessionID)
	}
	if slug == "" {
		slug = "session"
	}
	return filepath.ToSlash(fmt.Sprintf("%s-%s-%s.md", created.Format("20060102-150405"), slug, sm.SessionID))
}

func renderSessionSummaryNote(sm *SessionSummary) string {
	tags := make([]string, 0, len(sm.Topics)+1)
	tags = append(tags, "memory/session")
	for _, topic := range sm.Topics {
		if strings.TrimSpace(topic) != "" {
			tags = append(tags, "topic/"+slugify(topic))
		}
	}
	fm := sessionSummaryFrontmatter{
		ID:            sm.SessionID,
		Type:          "session_summary",
		UserID:        sm.UserID,
		CreatedAt:     sm.CreatedAt,
		Topics:        sm.Topics,
		KeyDecisions:  sm.KeyDecisions,
		OpenQuestions: sm.OpenQuestions,
		CodeContext:   sm.CodeContext,
		Status:        "active",
		Tags:          dedupSlice(tags),
	}
	yml, _ := yaml.Marshal(fm)
	title := sm.SessionID
	if len(sm.Topics) > 0 {
		title = strings.Join(sm.Topics, ", ")
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yml)
	b.WriteString("---\n\n")
	b.WriteString("# Session " + title + "\n\n")
	b.WriteString("## Summary\n\n")
	b.WriteString(strings.TrimSpace(sm.RawSummary))
	b.WriteString("\n\n^session-" + blockIDForEntry(sm.SessionID) + "\n")
	if len(sm.KeyDecisions) > 0 {
		b.WriteString("\n## Key Decisions\n\n")
		for _, item := range sm.KeyDecisions {
			b.WriteString("- " + strings.TrimSpace(item) + "\n")
		}
	}
	if len(sm.OpenQuestions) > 0 {
		b.WriteString("\n## Open Questions\n\n")
		for _, item := range sm.OpenQuestions {
			b.WriteString("- " + strings.TrimSpace(item) + "\n")
		}
	}
	if sm.CodeContext != "" {
		b.WriteString("\n## Code Context\n\n")
		b.WriteString(sm.CodeContext)
		b.WriteString("\n")
	}
	return b.String()
}

func parseSessionSummaryNote(path, root string) (*SessionSummary, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	fmRaw, body, ok := splitFrontmatter(string(raw))
	if !ok {
		return nil, false, nil
	}
	var fm sessionSummaryFrontmatter
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return nil, false, err
	}
	if fm.Type != "session_summary" || fm.ID == "" {
		return nil, false, nil
	}
	sm := &SessionSummary{
		SessionID:     fm.ID,
		UserID:        fm.UserID,
		CreatedAt:     fm.CreatedAt,
		Topics:        fm.Topics,
		KeyDecisions:  fm.KeyDecisions,
		OpenQuestions: fm.OpenQuestions,
		CodeContext:   fm.CodeContext,
		RawSummary:    strings.TrimSpace(blockIDPattern.ReplaceAllString(extractMarkdownSection(body, "Summary"), "")),
	}
	if sm.CreatedAt.IsZero() {
		sm.CreatedAt = time.Now()
	}
	if sm.RawSummary == "" {
		sm.RawSummary = strings.TrimSpace(blockIDPattern.ReplaceAllString(bodyWithoutTitle(body), ""))
	}
	return sm, true, nil
}

func (s *MidTermStore) removeSummaryFileLocked(sessionID string) {
	rel := s.paths[sessionID]
	if rel == "" {
		return
	}
	_ = os.Remove(filepath.Join(s.dir, filepath.FromSlash(rel)))
	delete(s.paths, sessionID)
}

// GenerateSessionSummary 从对话消息生成结构化会话摘要
// 暂不接 LLM，用启发式规则提取
func GenerateSessionSummary(sessionID, userID string, messages []ConversationTurn) *SessionSummary {
	summary := &SessionSummary{
		SessionID:     sessionID,
		UserID:        userID,
		CreatedAt:     time.Now(),
		Topics:        extractTopics(messages),
		KeyDecisions:  extractKeyDecisions(messages),
		OpenQuestions: extractOpenQuestions(messages),
		CodeContext:   extractCodeContext(messages),
		RawSummary:    generateRawSummary(messages),
	}
	return summary
}

// extractTopics 从对话中提取讨论主题
func extractTopics(messages []ConversationTurn) []string {
	topicKeywords := map[string][]string{
		"debugging":     {"bug", "debug", "fix", "error", "crash", "调试", "修复", "报错"},
		"architecture":  {"design", "architecture", "structure", "refactor", "架构", "设计", "重构"},
		"deployment":    {"deploy", "release", "ci/cd", "docker", "k8s", "部署", "发布"},
		"performance":   {"performance", "optimize", "slow", "latency", "性能", "优化", "慢"},
		"testing":       {"test", "coverage", "unit test", "测试", "覆盖率"},
		"feature":       {"feature", "implement", "add", "新功能", "实现", "添加"},
		"configuration": {"config", "setting", "env", "配置", "设置"},
		"documentation": {"doc", "readme", "文档", "说明"},
		"security":      {"security", "auth", "token", "安全", "认证"},
		"database":      {"database", "sql", "query", "migration", "数据库", "查询"},
	}

	var topics []string
	topicSeen := make(map[string]bool)

	for _, msg := range messages {
		lower := strings.ToLower(msg.Content)
		for topic, keywords := range topicKeywords {
			if topicSeen[topic] {
				continue
			}
			for _, kw := range keywords {
				if strings.Contains(lower, kw) {
					topics = append(topics, topic)
					topicSeen[topic] = true
					break
				}
			}
		}
	}

	return topics
}

// extractKeyDecisions 从对话中提取关键决策
func extractKeyDecisions(messages []ConversationTurn) []string {
	var decisions []string
	decisionPatterns := []string{
		"决定", "决定用", "选择了", "采用", "方案是",
		"decided to", "chose to", "will use", "going with", "the approach is",
		"we should", "let's use", "let's go with",
	}

	seen := make(map[string]bool)
	for _, msg := range messages {
		lower := strings.ToLower(msg.Content)
		for _, pattern := range decisionPatterns {
			if strings.Contains(lower, strings.ToLower(pattern)) {
				fragment := truncateField(msg.Content, 150)
				if !seen[fragment] {
					decisions = append(decisions, fragment)
					seen[fragment] = true
				}
				break
			}
		}
	}

	return decisions
}

// extractOpenQuestions 从对话中提取未解决的问题
func extractOpenQuestions(messages []ConversationTurn) []string {
	var questions []string
	questionPatterns := []string{
		"怎么", "如何", "为什么", "是否", "能不能",
		"how to", "how do", "why", "what if", "can we",
		"?", "？", "TODO", "FIXME", "HACK",
	}

	seen := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		lower := strings.ToLower(msg.Content)
		for _, pattern := range questionPatterns {
			if strings.Contains(lower, strings.ToLower(pattern)) {
				fragment := truncateField(msg.Content, 150)
				if !seen[fragment] {
					questions = append(questions, fragment)
					seen[fragment] = true
				}
				break
			}
		}
	}

	return questions
}

// extractCodeContext 从对话中提取代码/项目上下文
func extractCodeContext(messages []ConversationTurn) string {
	var codeParts []string
	codeIndicators := []string{
		"func ", "func(", "package ", "import ",
		"type ", "struct ", "interface ",
		"func (", "var ", "const ",
		"```", "git ", "go mod",
		".go", ".py", ".rs", ".ts", ".js",
		"internal/", "pkg/", "cmd/",
	}

	for _, msg := range messages {
		lower := strings.ToLower(msg.Content)
		for _, indicator := range codeIndicators {
			if strings.Contains(lower, strings.ToLower(indicator)) {
				codeParts = append(codeParts, truncateField(msg.Content, 200))
				break
			}
		}
		if len(codeParts) >= 3 {
			break
		}
	}

	if len(codeParts) == 0 {
		return ""
	}

	result := strings.Join(codeParts, " | ")
	return truncateField(result, 500)
}

// generateRawSummary 生成自然语言摘要
func generateRawSummary(messages []ConversationTurn) string {
	if len(messages) == 0 {
		return ""
	}

	var userParts []string
	var assistantParts []string

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			userParts = append(userParts, truncateField(msg.Content, 80))
		case "assistant":
			assistantParts = append(assistantParts, truncateField(msg.Content, 80))
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Session with %d user messages and %d assistant responses. ",
		len(userParts), len(assistantParts)))

	if len(userParts) > 0 {
		sb.WriteString("User asked about: ")
		limit := 3
		if len(userParts) < limit {
			limit = len(userParts)
		}
		for i := 0; i < limit; i++ {
			if i > 0 {
				sb.WriteString("; ")
			}
			sb.WriteString(userParts[i])
		}
		sb.WriteString(". ")
	}

	if len(assistantParts) > 0 {
		sb.WriteString("Key responses: ")
		limit := 2
		if len(assistantParts) < limit {
			limit = len(assistantParts)
		}
		for i := 0; i < limit; i++ {
			if i > 0 {
				sb.WriteString("; ")
			}
			sb.WriteString(assistantParts[i])
		}
	}

	result := sb.String()
	return truncateField(result, 800)
}
