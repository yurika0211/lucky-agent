package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/logger"
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
	"github.com/yurika0211/luckyagent/internal/utils"
)

/*
contextBuildOptions 定义上下文构建时的行为开关。
*/
type contextBuildOptions struct {
	IncludeRAG     bool
	IncludeHistory bool
	HistoryRecent  int
	HistoryMiddle  int
	DisabledTools  []string
}

/*
defaultContextBuildOptions 返回默认的上下文构建选项。
*/
func defaultContextBuildOptions() contextBuildOptions {
	return contextBuildOptions{
		IncludeRAG:     true,
		IncludeHistory: true,
		HistoryRecent:  6,
		HistoryMiddle:  12,
	}
}

/*
contextBudget 描述不同上下文类别的 token 预算。
*/
type contextBudget struct {
	System     int
	Memory     int
	RAG        int
	History    int
	ToolResult int
}

/*
contextPlanner 负责根据预算组装系统提示、记忆、RAG 与历史消息。
*/
type contextPlanner struct {
	agent   *Agent
	est     *contextx.TokenEstimator
	budget  contextBudget
	options contextBuildOptions
}

/*
newContextPlanner 创建一个新的上下文规划器。
*/
func newContextPlanner(a *Agent, options contextBuildOptions) *contextPlanner {
	cfg := contextx.DefaultWindowConfig()
	if a != nil && a.contextWin != nil {
		cfg = a.contextWin.Config()
	}
	available := cfg.MaxTokens - cfg.ReservedTokens
	if available <= 0 {
		available = cfg.MaxTokens / 2
	}
	if available <= 0 {
		available = 2048
	}

	budget := contextBudget{
		System:     int(float64(available) * 0.15),
		Memory:     int(float64(available) * 0.10),
		RAG:        int(float64(available) * 0.20),
		History:    int(float64(available) * 0.25),
		ToolResult: int(float64(available) * 0.30),
	}

	if budget.System < 256 {
		budget.System = 256
	}
	if budget.Memory < 128 {
		budget.Memory = 128
	}
	if budget.RAG < 256 {
		budget.RAG = 256
	}
	if budget.History < 256 {
		budget.History = 256
	}
	if budget.ToolResult < 256 {
		budget.ToolResult = 256
	}

	return &contextPlanner{
		agent:   a,
		est:     resolveTokenEstimator(a, cfg.MaxTokens),
		budget:  budget,
		options: options,
	}
}

/*
Build 根据当前请求、会话和预算生成最终上下文消息序列。
*/
func (p *contextPlanner) Build(ctx context.Context, sess *session.Session, userInput string) []provider.Message {
	return p.BuildInput(ctx, sess, TextUserTurnInput(userInput))
}

/*
BuildInput 根据结构化用户输入、会话和预算生成最终上下文消息序列。
*/
func (p *contextPlanner) BuildInput(ctx context.Context, sess *session.Session, input UserTurnInput) []provider.Message {
	input = input.Normalize()
	routingText := input.RoutingText
	if p.agent != nil {
		p.agent.runContextMemoryHygieneHook()
	}
	hasStructuredParts := len(input.Message.ContentParts) > 0
	if hasStructuredParts && !p.supportsImageContentParts() {
		input.Message = stripContentParts(input.Message)
	}
	allowCache := !hasStructuredParts

	if allowCache {
		if key, ok := p.cacheKey(sess, routingText); ok && p.agent != nil && p.agent.contextCache != nil {
			if cached, entry, hit := p.agent.contextCache.Get(key); hit {
				p.logContextReport("cache_hit", key, entry)
				return cached
			}
		}
	}

	messages := make([]provider.Message, 0, 8)

	// 构建系统提示词
	systemPrompt := ""
	if p.agent != nil {
		systemPrompt = p.agent.buildSystemPromptWithOptions(sess, systemPromptOptions{
			DisabledTools: p.options.DisabledTools,
		})
	}
	systemParts := []string{p.fitTextToBudget(aOrEmpty(systemPrompt), p.budget.System)}
	if p.agent == nil || p.agent.provider == nil {
		if tools := p.buildToolCatalog(p.options.DisabledTools); tools != "" {
			systemParts = append(systemParts, tools)
		}
	} else if _, ok := p.agent.provider.(provider.FunctionCallingProvider); !ok {
		if tools := p.buildToolCatalog(p.options.DisabledTools); tools != "" {
			systemParts = append(systemParts, tools)
		}
	}
	systemContent := strings.TrimSpace(strings.Join(utils.FilterNonEmptyTrimmed(systemParts), "\n\n"))
	if systemContent != "" {
		messages = append(messages, provider.Message{Role: "system", Content: systemContent})
	}
	// 拼接skill的内容
	if p.agent != nil {
		if skillHint := strings.TrimSpace(p.agent.buildSkillRouteSystemHintWithOptions(routingText, skillRouteOptions{
			DisabledTools: p.options.DisabledTools,
		})); skillHint != "" {
			messages = append(messages, provider.Message{Role: "system", Content: skillHint})
		}
	}

	// 拼接rag知识库的内容
	messages = append(messages, p.buildMemoryMessages(routingText, input.Scope)...)
	if p.options.IncludeRAG {
		if ragMsg := p.buildRAGMessage(ctx, routingText); ragMsg.Content != "" {
			messages = append(messages, ragMsg)
		}
	}
	if p.options.IncludeHistory && sess != nil {
		messages = append(messages, p.buildHistoryMessages(sess, routingText)...)
	}

	if p.agent == nil {
		return append(messages, p.buildAttachmentMessages(ctx, input)...)
	}

	// 拼接附件解析完成后的内容
	attachmentMsgs := p.buildAttachmentMessages(ctx, input)
	provisional := append(append([]provider.Message(nil), messages...), attachmentMsgs...)
	provisional = append(provisional, provider.Message{
		Role:    "user",
		Content: routingText,
	})
	provisional = p.agent.fitContextWindow(provisional)
	if n := len(provisional); n > 0 {
		last := provisional[n-1]
		if last.Role == "user" && strings.TrimSpace(last.Content) == routingText {
			provisional = provisional[:n-1]
		}
	}
	messages = append(provisional, input.Message)

	report := p.buildContextReport(messages)
	if allowCache {
		if key, ok := p.cacheKey(sess, routingText); ok && p.agent.contextCache != nil {
			p.agent.contextCache.Set(key, contextCacheEntry{
				messages:     messages,
				totalTokens:  report.totalTokens,
				bucketTokens: report.bucketTokens,
			})
			p.logContextReport("cache_store", key, contextCacheEntry{
				messages:     messages,
				totalTokens:  report.totalTokens,
				bucketTokens: report.bucketTokens,
			})
		}
	}
	return messages
}

/**
 * 将附件解析完之后的结果放到上下文当中
 */
func (p *contextPlanner) buildAttachmentMessages(ctx context.Context, input UserTurnInput) []provider.Message {
	if len(input.Attachments) == 0 {
		return nil
	}

	messages := make([]provider.Message, 0, 2)
	if manifest := attachmentEvidenceManifest(input.Attachments); manifest != "" {
		messages = append(messages, provider.Message{
			Role:    "system",
			Content: manifest,
		})
	}
	if p.agent != nil {
		if summary, err := p.agent.AnalyzeAttachments(ctx, input.Attachments); err == nil && strings.TrimSpace(summary) != "" {
			messages = append(messages, provider.Message{
				Role:    "system",
				Content: summary,
			})
		}
	}
	if len(input.Message.ContentParts) > 0 && p.supportsImageContentParts() {
		messages = append(messages, provider.Message{
			Role:         "user",
			Content:      input.RoutingText,
			ContentParts: input.Message.ContentParts,
		})
	}
	return messages
}

func attachmentEvidenceManifest(attachments []gateway.Attachment) string {
	if len(attachments) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Current Turn Attachments]\n")
	b.WriteString("Use only the attachments listed in this block for the current user request. Do not substitute files from chat history, memory, or the workspace when an attachment is missing or unreadable.\n")
	for i, att := range attachments {
		label := strings.TrimSpace(string(att.Type))
		if label == "" {
			label = "attachment"
		}
		name := strings.TrimSpace(att.FileName)
		if name == "" {
			name = strings.TrimSpace(att.FileID)
		}
		if name == "" {
			name = "unnamed"
		}
		fmt.Fprintf(&b, "%d. type=%s name=%s", i+1, label, name)
		if strings.TrimSpace(att.FilePath) != "" {
			fmt.Fprintf(&b, " file_path=%s", strings.TrimSpace(att.FilePath))
		}
		if strings.TrimSpace(att.FileURL) != "" {
			fmt.Fprintf(&b, " url=%s", strings.TrimSpace(att.FileURL))
		}
		if strings.TrimSpace(att.FileID) != "" {
			fmt.Fprintf(&b, " file_id=%s", strings.TrimSpace(att.FileID))
		}
		if strings.TrimSpace(att.MimeType) != "" {
			fmt.Fprintf(&b, " mime=%s", strings.TrimSpace(att.MimeType))
		}
		if att.FileSize > 0 {
			fmt.Fprintf(&b, " size=%d", att.FileSize)
		}
		if strings.TrimSpace(att.FilePath) == "" && strings.TrimSpace(att.FileURL) == "" && len(att.Data) == 0 {
			b.WriteString(" status=unavailable")
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

/**
 * 支持图片内容的部分，查看模型是否支持图片内容
 */
func (p *contextPlanner) supportsImageContentParts() bool {
	if p == nil || p.agent == nil {
		return false
	}

	// 优先检查配置中的 vision 字段
	if p.agent.cfg != nil {
		cfg := p.agent.cfg.Get()
		if cfg.LlmProvider.Vision {
			return true
		}
	}

	// 回退到 catalog 精确匹配
	if p.agent.catalog == nil {
		return false
	}

	model := strings.TrimSpace(p.agent.activeModel)
	if model == "" && p.agent.cfg != nil {
		model = strings.TrimSpace(p.agent.cfg.Get().Model)
	}
	if model == "" {
		return false
	}

	info, err := p.agent.catalog.Get(model)
	if err == nil && info != nil {
		for _, capability := range info.Capabilities {
			if strings.EqualFold(strings.TrimSpace(capability), "vision") {
				return true
			}
		}
	}

	return false
}

/**
 * 清空消息的内容
 */
func stripContentParts(msg provider.Message) provider.Message {
	msg.ContentParts = nil
	return msg
}

/*
contextReport 汇总一次上下文构建的 token 与消息分布。
*/
type contextReport struct {
	totalTokens  int
	bucketTokens map[string]int
	bucketCounts map[string]int
}

/*
buildContextReport 统计上下文中各类别消息的 token 占用情况。
*/
func (p *contextPlanner) buildContextReport(messages []provider.Message) contextReport {
	report := contextReport{
		bucketTokens: map[string]int{
			"system":      0,
			"memory":      0,
			"rag":         0,
			"history":     0,
			"tool_result": 0,
			"user":        0,
		},
		bucketCounts: map[string]int{
			"system":      0,
			"memory":      0,
			"rag":         0,
			"history":     0,
			"tool_result": 0,
			"user":        0,
		},
	}

	for _, msg := range messages {
		tokens := p.est.Estimate(msg.Content) + 4
		report.totalTokens += tokens
		name := classifyContextBucket(msg)
		report.bucketTokens[name] += tokens
		report.bucketCounts[name]++
	}
	return report
}

/*
logContextReport 在开启调试时输出上下文规划报告。
*/
func (p *contextPlanner) logContextReport(mode string, key uint64, entry contextCacheEntry) {
	if p.agent == nil || p.agent.cfg == nil {
		return
	}
	if !p.agent.cfg.Get().Agent.ContextDebug {
		return
	}

	logger.Info("context planner report",
		"mode", mode,
		"cache_key", key,
		"messages", len(entry.messages),
		"cached_tokens_total", entry.totalTokens,
		"cached_system_tokens", entry.bucketTokens["system"],
		"cached_memory_tokens", entry.bucketTokens["memory"],
		"cached_rag_tokens", entry.bucketTokens["rag"],
		"cached_history_tokens", entry.bucketTokens["history"],
		"cached_tool_tokens", entry.bucketTokens["tool_result"],
		"cached_user_tokens", entry.bucketTokens["user"],
	)
}

/*
classifyContextBucket 将消息归类到上下文统计桶中。
*/
func classifyContextBucket(msg provider.Message) string {
	if msg.Role == "user" {
		return "user"
	}
	if msg.Role == "tool" {
		return "tool_result"
	}
	if msg.Role != "system" {
		return "history"
	}
	switch {
	case strings.HasPrefix(msg.Content, "[Core Memory"),
		strings.HasPrefix(msg.Content, "[Working Memory"),
		strings.HasPrefix(msg.Content, "[Session History"),
		strings.HasPrefix(msg.Content, "[Recent Context"):
		return "memory"
	case strings.HasPrefix(msg.Content, "## Retrieved Knowledge"),
		strings.HasPrefix(msg.Content, "[Retrieved Knowledge"):
		return "rag"
	case strings.HasPrefix(msg.Content, "[Conversation Summary"),
		strings.HasPrefix(msg.Content, "[Conversation Themes"):
		return "history"
	default:
		return "system"
	}
}

/*
cacheKey 为一次上下文构建请求生成缓存键。
*/
func (p *contextPlanner) cacheKey(sess *session.Session, userInput string) (uint64, bool) {
	if p.agent == nil {
		return 0, false
	}

	payload := map[string]any{
		"user_input": userInput,
		"options":    p.options,
		"budget":     p.budget,
	}

	if p.agent.soul != nil {
		payload["system_prompt"] = p.agent.soul.SystemPrompt()
	}
	if p.agent.provider != nil {
		_, fc := p.agent.provider.(provider.FunctionCallingProvider)
		payload["function_calling"] = fc
	}
	if p.agent.memory != nil {
		payload["recent_memory"] = p.agent.memory.Recent(8)
	}
	if p.agent.shortTerm != nil {
		payload["short_summary"] = p.agent.shortTerm.Summary()
	}
	if p.agent.midTerm != nil && strings.TrimSpace(userInput) != "" {
		payload["midterm"] = p.agent.midTerm.SearchSummaries(userInput, 2)
	}
	if p.agent.ragManager != nil {
		stats := p.agent.ragManager.Stats()
		payload["rag_doc_count"] = stats.DocumentCount
	}
	if sess != nil {
		payload["session_id"] = sess.ID
		payload["session_title"] = sess.Title
		payload["session_message_count"] = sess.MessageCount()
		payload["session_last_message_sig"] = sessionLastMessageSignature(sess)
	}

	return makeContextCacheKey(payload), true
}

func sessionLastMessageSignature(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	last := sess.LastMessage()
	if last == nil {
		return ""
	}
	payload := map[string]any{
		"role":              last.Role,
		"content":           last.Content,
		"reasoning_content": last.ReasoningContent,
		"tool_call_id":      last.ToolCallID,
		"name":              last.Name,
		"tool_calls":        last.ToolCalls,
	}
	return fmt.Sprintf("%x", makeContextCacheKey(payload))
}

/*
resolveTokenEstimator 解析可用的 token 估算器。
*/
func resolveTokenEstimator(a *Agent, maxTokens int) *contextx.TokenEstimator {
	if a != nil && a.contextEst != nil {
		a.contextEst.SetModelContextWindow(maxTokens)
		return a.contextEst
	}
	return contextx.NewTokenEstimator(maxTokens)
}

/*
buildToolCatalog 构造供非 function-calling 模型参考的工具目录文本。
*/
func (p *contextPlanner) buildToolCatalog(disabled []string) string {
	if p.agent == nil || p.agent.tools == nil {
		return ""
	}
	tools := p.agent.Tools().ListModelVisible()
	disabledSet := makeToolNameSet(disabled)
	if len(tools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Available Tools]\n")
	for _, t := range tools {
		if t == nil || toolNameInSet(disabledSet, t.Name) {
			continue
		}
		permLabel := "🟢"
		if t.Permission == tool.PermApprove {
			permLabel = "🟡"
		}
		b.WriteString(fmt.Sprintf("- %s %s: %s\n", permLabel, t.Name, t.Description))
	}
	content := strings.TrimSpace(b.String())
	if content == "[Available Tools]" {
		return ""
	}
	return p.fitTextToBudget(content, utils.MaxInt(96, p.budget.System/4))
}

/*
buildMemoryMessages 构造与当前查询相关的记忆上下文消息。
*/
func (p *contextPlanner) buildMemoryMessages(query string, scope TurnScope) []provider.Message {
	var messages []provider.Message

	if core := p.buildCoreMemoryMessage(query, scope); core.Content != "" {
		messages = append(messages, core)
	}
	if relevant := p.buildRelevantMemoryMessage(query, scope); relevant.Content != "" {
		messages = append(messages, relevant)
	}
	if midterm := p.buildMidtermSummaryMessage(query); midterm.Content != "" {
		messages = append(messages, midterm)
	}
	if short := p.buildShortTermSummaryMessage(); short.Content != "" {
		messages = append(messages, short)
	}

	return messages
}

/*
buildCoreMemoryMessage 构造长期核心记忆消息。
*/
func (p *contextPlanner) buildCoreMemoryMessage(query string, scope TurnScope) provider.Message {
	if p.agent == nil || p.agent.memory == nil {
		return provider.Message{}
	}
	longs := p.agent.memory.ByTier(memory.TierLong)
	longs = filterMemoryEntriesForScope(longs, scope)
	if len(longs) == 0 {
		return provider.Message{}
	}
	selected := make([]string, 0, 3)
	queryLower := strings.ToLower(query)
	for _, e := range longs {
		if queryLower == "" || strings.Contains(strings.ToLower(e.Content), queryLower) || len(selected) == 0 {
			selected = append(selected, "- "+e.Content)
		}
		if len(selected) >= 3 {
			break
		}
	}
	if len(selected) == 0 {
		return provider.Message{}
	}
	content := "[Core Memory]\n" + strings.Join(selected, "\n")
	return provider.Message{Role: "system", Content: p.fitTextToBudget(content, utils.MaxInt(96, p.budget.Memory/3))}
}

/*
buildRelevantMemoryMessage 构造与当前问题相关的普通记忆消息。
*/
func (p *contextPlanner) buildRelevantMemoryMessage(query string, scope TurnScope) provider.Message {
	if p.agent == nil || p.agent.memory == nil {
		return provider.Message{}
	}
	if strings.TrimSpace(query) == "" {
		return provider.Message{}
	}
	route := p.agent.memory.Route(query)
	results := route.Entries
	if len(results) == 0 {
		return provider.Message{}
	}
	results = filterRecallContextEntries(results)
	results = filterMemoryEntriesForScope(results, scope)
	if len(results) == 0 {
		return provider.Message{}
	}
	route.Entries = results
	route.EvidenceRefs = evidenceRefsForContext(results, 6)
	route.TemporalNotes = nil
	route.SupersededRefs = nil
	route.ConflictRefs = nil
	route.ExpiredRefs = nil
	route.FutureRefs = nil
	results = prioritizeMemoryForContext(results)
	header := "[Working Memory — Retrieved Evidence]\nThese active memories were retrieved from the LuckyAgent Obsidian-compatible Markdown memory vault"
	if vault := p.agent.memoryVaultPath(); vault != "" {
		header += " at " + vault
	}
	header += ". Treat them as prior evidence, not as the current task itself. Prefer the latest user message and explicit session history when they conflict with retrieved memory. If a memory says real-time data or external checks are needed and it still matches the current user request, use available tools before the final answer or state exactly what could not be checked."
	bodyBudget := utils.MaxInt(768, p.budget.Memory*3-p.est.Estimate(header)-16)
	body := p.buildTypedMemoryBody(route, results, bodyBudget)
	content := header
	if body != "" {
		content += "\n\n" + body
	}
	return provider.Message{Role: "system", Content: content}
}

func (p *contextPlanner) buildTypedMemoryBody(route memory.RouteAnalysis, results []memory.Entry, tokenBudget int) string {
	if tokenBudget <= 0 {
		return ""
	}
	var sections []string
	if len(route.RequiredTools) > 0 {
		sections = append(sections, "[Required Tools]\n- "+strings.Join(route.RequiredTools, "\n- "))
	}
	if len(route.Constraints) > 0 {
		sections = append(sections, "[Answer Constraints]\n- "+strings.Join(limitStrings(route.Constraints, 8), "\n- "))
	}
	if len(route.SuggestedSearches) > 0 {
		sections = append(sections, "[Suggested web_search queries]\n- "+strings.Join(limitStrings(route.SuggestedSearches, 4), "\n- "))
	}
	if lines := temporalWarningLines(route); len(lines) > 0 {
		sections = append(sections, "[Temporal Warnings]\n"+strings.Join(lines, "\n"))
	}
	if len(route.EvidenceRefs) > 0 {
		refs := strings.Join(limitStrings(route.EvidenceRefs, 8), "\n- ")
		sections = append(sections, "[Evidence Refs]\nMemory refs:\n- "+refs)
	}
	if lines := typedMemoryFactLines(results, 6); len(lines) > 0 {
		sections = append(sections, "[Must Use Facts]\n"+strings.Join(lines, "\n"))
	}
	if lines := memoryRouteLines(route); len(lines) > 0 {
		lines = limitStrings(lines, 3)
		sections = append(sections, "[Memory Router]\n"+strings.Join(lines, "\n"))
	}
	return p.fitTextToBudget(strings.Join(sections, "\n\n"), tokenBudget)
}

func typedMemoryFactLines(entries []memory.Entry, limit int) []string {
	if limit <= 0 {
		return nil
	}
	lines := make([]string, 0, utils.MinInt(limit, len(entries)))
	for _, e := range entries {
		if strings.TrimSpace(e.Content) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- [%s/%s] %s%s", e.Category, e.Tier.String(), truncate(e.Content, 160), memoryRefHint(e)))
		if len(lines) >= limit {
			break
		}
	}
	return lines
}

func filterRecallContextEntries(entries []memory.Entry) []memory.Entry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]memory.Entry, 0, len(entries))
	for _, e := range entries {
		if isRawConversationShortMemory(e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func filterMemoryEntriesForScope(entries []memory.Entry, scope TurnScope) []memory.Entry {
	if len(entries) == 0 {
		return nil
	}
	scope = scope.Normalize()
	if !scope.HasSender() {
		return entries
	}
	out := make([]memory.Entry, 0, len(entries))
	for _, e := range entries {
		if memoryEntryVisibleInScope(e, scope) {
			out = append(out, e)
		}
	}
	return out
}

func memoryEntryVisibleInScope(e memory.Entry, scope TurnScope) bool {
	tags := normalizedTagSet(e.Tags)
	if len(tags) == 0 || !hasScopedMemoryTag(tags) {
		return true
	}
	if tags["scope:shared"] {
		if groupTag := scope.GroupTag(); groupTag != "" {
			return tags[groupTag]
		}
		return true
	}
	if tags[scope.UserTag()] || tags[scope.PrivateTag()] {
		return true
	}
	if groupTag := scope.GroupTag(); groupTag != "" && tags[groupTag] && !tags["scope:personal"] {
		return true
	}
	return false
}

func normalizedTagSet(tags []string) map[string]bool {
	out := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(tag)), "#")
		if tag != "" {
			out[tag] = true
		}
	}
	return out
}

func hasScopedMemoryTag(tags map[string]bool) bool {
	for tag := range tags {
		if strings.HasPrefix(tag, "scope:") {
			return true
		}
	}
	return false
}

func isRawConversationShortMemory(e memory.Entry) bool {
	if e.Tier != memory.TierShort {
		return false
	}
	content := strings.TrimSpace(e.Content)
	return strings.HasPrefix(content, "User:") || strings.HasPrefix(content, "Assistant:")
}

func memoryRefHint(e memory.Entry) string {
	if e.Path == "" && e.BlockID == "" {
		return ""
	}
	ref := e.Path
	if ref == "" {
		ref = e.ID
	}
	if e.BlockID != "" {
		ref += "#" + e.BlockID
	}
	return " (ref=" + ref + ")"
}

func evidenceRefsForContext(entries []memory.Entry, limit int) []string {
	if limit <= 0 {
		return nil
	}
	refs := make([]string, 0, utils.MinInt(limit, len(entries)))
	for _, e := range entries {
		ref := e.Path
		if ref == "" {
			ref = e.ID
		}
		if ref == "" {
			continue
		}
		if e.BlockID != "" {
			ref += "#" + e.BlockID
		}
		refs = append(refs, ref)
		if len(refs) >= limit {
			break
		}
	}
	return dedupStrings(refs)
}

func dedupStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func memoryRouteLines(route memory.RouteAnalysis) []string {
	var lines []string
	if len(route.RequiredTools) > 0 {
		lines = append(lines, "- Required tools before final answer: "+strings.Join(route.RequiredTools, ", "))
	}
	if len(route.RiskFlags) > 0 {
		lines = append(lines, "- Risk flags: "+strings.Join(route.RiskFlags, ", "))
	}
	if len(route.Constraints) > 0 {
		lines = append(lines, "- Answer constraints: "+strings.Join(limitStrings(route.Constraints, 5), " | "))
	}
	if len(route.SuggestedSearches) > 0 {
		lines = append(lines, "- Suggested web_search queries: "+strings.Join(limitStrings(route.SuggestedSearches, 4), " | "))
	}
	if len(route.Clarifications) > 0 {
		lines = append(lines, "- Clarify if needed: "+strings.Join(limitStrings(route.Clarifications, 3), " | "))
	}
	if len(route.TemporalNotes) > 0 {
		lines = append(lines, "- Temporal resolution: "+strings.Join(limitStrings(route.TemporalNotes, 3), " | "))
	}
	if len(route.EvidenceRefs) > 0 {
		lines = append(lines, "- Memory refs: "+strings.Join(limitStrings(route.EvidenceRefs, 5), " | "))
	}
	return lines
}

func temporalWarningLines(route memory.RouteAnalysis) []string {
	var lines []string
	if len(route.TemporalNotes) > 0 || len(route.SupersededRefs) > 0 || len(route.ConflictRefs) > 0 || len(route.ExpiredRefs) > 0 || len(route.FutureRefs) > 0 {
		lines = append(lines, "- Temporal resolution:")
	}
	for _, note := range limitStrings(route.TemporalNotes, 6) {
		lines = append(lines, "- "+note)
	}
	if len(route.SupersededRefs) > 0 {
		lines = append(lines, "- Superseded refs: "+strings.Join(limitStrings(route.SupersededRefs, 6), " | "))
	}
	if len(route.ConflictRefs) > 0 {
		lines = append(lines, "- Conflict refs: "+strings.Join(limitStrings(route.ConflictRefs, 6), " | "))
	}
	if len(route.ExpiredRefs) > 0 {
		lines = append(lines, "- Expired refs: "+strings.Join(limitStrings(route.ExpiredRefs, 6), " | "))
	}
	if len(route.FutureRefs) > 0 {
		lines = append(lines, "- Future refs: "+strings.Join(limitStrings(route.FutureRefs, 6), " | "))
	}
	return lines
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func prioritizeMemoryForContext(entries []memory.Entry) []memory.Entry {
	if len(entries) <= 1 {
		return entries
	}
	out := append([]memory.Entry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		return memoryContextRank(out[i]) > memoryContextRank(out[j])
	})
	return out
}

func memoryContextRank(e memory.Entry) int {
	rank := 0
	switch e.Tier {
	case memory.TierLong:
		rank += 300
	case memory.TierMedium:
		rank += 200
	case memory.TierShort:
		rank += 100
	}
	switch strings.ToLower(strings.TrimSpace(e.Category)) {
	case "health", "rule", "identity", "preference", "location", "project", "plan":
		rank += 40
	case "conversation":
		rank -= 25
	}
	if strings.HasPrefix(strings.TrimSpace(e.Content), "User:") || strings.HasPrefix(strings.TrimSpace(e.Content), "Assistant:") {
		rank -= 30
	}
	rank += int(e.Importance * 20)
	return rank
}

/*
buildMidtermSummaryMessage 构造中期会话摘要消息。
*/
func (p *contextPlanner) buildMidtermSummaryMessage(query string) provider.Message {
	if p.agent == nil || p.agent.midTerm == nil || strings.TrimSpace(query) == "" {
		return provider.Message{}
	}
	summaries := p.agent.midTerm.SearchSummaries(query, 2)
	if len(summaries) == 0 {
		return provider.Message{}
	}
	var b strings.Builder
	b.WriteString("[Session History — Mid-term]\n")
	b.WriteString("Compressed summary; non-authoritative. Prefer raw recent messages, tool outputs, or workspace state when they conflict.\n")
	for _, sm := range summaries {
		b.WriteString("- ")
		if len(sm.Topics) > 0 {
			b.WriteString("[" + strings.Join(sm.Topics, ", ") + "] ")
		}
		b.WriteString(truncate(sm.RawSummary, 180))
		b.WriteString("\n")
	}
	return provider.Message{Role: "system", Content: p.fitTextToBudget(b.String(), utils.MaxInt(96, p.budget.Memory/3))}
}

/*
buildShortTermSummaryMessage 构造短期会话摘要消息。
*/
func (p *contextPlanner) buildShortTermSummaryMessage() provider.Message {
	if p.agent == nil || p.agent.shortTerm == nil {
		return provider.Message{}
	}
	summary := strings.TrimSpace(p.agent.shortTerm.Summary())
	if summary == "" {
		return provider.Message{}
	}
	content := "[Recent Context]\n" + summary
	return provider.Message{Role: "system", Content: p.fitTextToBudget(content, utils.MaxInt(96, p.budget.Memory/3))}
}

/*
buildRAGMessage 构造检索增强知识消息。
*/
func (p *contextPlanner) buildRAGMessage(ctx context.Context, query string) provider.Message {
	if p.agent == nil || p.agent.ragManager == nil || strings.TrimSpace(query) == "" {
		return provider.Message{}
	}
	stats := p.agent.ragManager.Stats()
	if stats.DocumentCount == 0 {
		return provider.Message{}
	}
	ragCtx, _, err := p.agent.ragManager.SearchWithContext(ctx, query)
	if err != nil || ragCtx == "" {
		return provider.Message{}
	}
	return provider.Message{Role: "system", Content: p.fitTextToBudget(ragCtx, p.budget.RAG)}
}

/*
buildHistoryMessages 从会话历史中挑选适量消息纳入上下文。
*/
func (p *contextPlanner) buildHistoryMessages(sess *session.Session, query string) []provider.Message {
	all := sess.GetMessages()
	if len(all) == 0 {
		return nil
	}
	intentTerms := historyIntentTerms(query+" "+sess.Title, all)

	recentCount := p.options.HistoryRecent
	if recentCount <= 0 {
		recentCount = 6
	}
	if recentCount > len(all) {
		recentCount = len(all)
	}

	middleCount := p.options.HistoryMiddle
	if middleCount < 0 {
		middleCount = 0
	}

	recentStart := len(all) - recentCount
	if recentStart < 0 {
		recentStart = 0
	}

	var messages []provider.Message
	if recentStart > 0 {
		middleStart := recentStart - middleCount
		if middleStart < 0 {
			middleStart = 0
		}
		if middleStart > 0 {
			if themes := p.summarizeIntentAwareConversationRange(context.Background(), sess, all[:middleStart], "[Conversation Themes]", utils.MaxInt(96, p.budget.History/4), intentTerms); themes != "" {
				messages = append(messages, provider.Message{Role: "system", Content: themes})
			}
		}
		if middleStart < recentStart {
			if summary := p.summarizeIntentAwareConversationRange(context.Background(), sess, all[middleStart:recentStart], "[Conversation Summary]", utils.MaxInt(96, p.budget.History/3), intentTerms); summary != "" {
				messages = append(messages, provider.Message{Role: "system", Content: summary})
			}
		}
	}

	recentBudget := utils.MaxInt(128, p.budget.History/2)
	recentMessages := p.selectIntentAwareRecentHistory(all[recentStart:], intentTerms)
	used := 0
	for _, msg := range recentMessages {
		msg = p.compactHistoryMessage(msg)
		tokens := p.est.Estimate(msg.Content) + 4
		if msg.ReasoningContent != "" {
			tokens += p.est.Estimate(msg.ReasoningContent)
		}
		if used+tokens > recentBudget && len(messages) > 0 {
			continue
		}
		used += tokens
		messages = append(messages, msg)
	}

	return messages
}

func (p *contextPlanner) summarizeIntentAwareConversationRange(ctx context.Context, sess *session.Session, messages []provider.Message, header string, tokenBudget int, terms []string) string {
	filtered := filterIntentAwareHistory(messages, terms)
	if len(filtered) == 0 {
		return ""
	}
	return p.summarizeConversationRangeWithLLM(ctx, sess, filtered, header, tokenBudget)
}

func filterIntentAwareHistory(messages []provider.Message, terms []string) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	filtered := make([]provider.Message, 0, len(messages))
	for _, msg := range messages {
		if historyMessageRelevant(msg, terms) {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

func (p *contextPlanner) selectIntentAwareRecentHistory(messages []provider.Message, terms []string) []provider.Message {
	if len(messages) <= 2 {
		return filterIntentAwareHistory(messages, terms)
	}
	latestUserIdx := latestUserMessageIndex(messages)
	selected := make([]provider.Message, 0, len(messages))
	for i, msg := range messages {
		content := strings.ToLower(msg.Content)
		tailCandidate := i >= len(messages)-2 && !historyExplicitlyIrrelevant(content)
		latestUserTurn := latestUserIdx >= 0 && i >= latestUserIdx && !historyExplicitlyIrrelevant(content)
		if historyMessageRelevant(msg, terms) || tailCandidate || latestUserTurn {
			selected = append(selected, msg)
		}
	}
	if len(selected) == 0 {
		tail := messages[len(messages)-utils.MinInt(2, len(messages)):]
		return filterHistoryWithoutExplicitIrrelevance(tail)
	}
	return selected
}

func latestUserMessageIndex(messages []provider.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Content) != "" {
			return i
		}
	}
	return -1
}

func filterHistoryWithoutExplicitIrrelevance(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	filtered := make([]provider.Message, 0, len(messages))
	for _, msg := range messages {
		if !historyExplicitlyIrrelevant(strings.ToLower(msg.Content)) {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

func historyIntentTerms(seedText string, messages []provider.Message) []string {
	var terms []string
	terms = append(terms, extractHistoryTerms(seedText)...)
	for _, msg := range messages {
		content := strings.ToLower(msg.Content)
		if historyExplicitlyIrrelevant(content) {
			continue
		}
		if strings.Contains(content, "context packer") ||
			strings.Contains(content, "benchmark") ||
			strings.Contains(content, "cmr") ||
			strings.Contains(content, "quality") ||
			strings.Contains(content, "p95") {
			terms = append(terms, extractHistoryTerms(content)...)
		}
	}
	terms = append(terms, historyAliasTerms(seedText)...)
	return utils.DedupStringsLimit(terms, 32)
}

func extractHistoryTerms(text string) []string {
	var terms []string
	var latin strings.Builder
	var han []rune
	flushLatin := func() {
		if latin.Len() == 0 {
			return
		}
		token := strings.Trim(strings.ToLower(latin.String()), "-_")
		if len(token) >= 3 {
			terms = append(terms, token)
		}
		latin.Reset()
	}
	flushHan := func() {
		if len(han) == 0 {
			return
		}
		for n := 2; n <= 4; n++ {
			if len(han) < n {
				continue
			}
			for i := 0; i+n <= len(han); i++ {
				terms = append(terms, string(han[i:i+n]))
			}
		}
		han = han[:0]
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.Is(unicode.Han, r):
			flushLatin()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-':
			flushHan()
			latin.WriteRune(r)
		default:
			flushLatin()
			flushHan()
		}
	}
	flushLatin()
	flushHan()
	return terms
}

func historyAliasTerms(seed string) []string {
	seed = strings.ToLower(seed)
	var terms []string
	if strings.Contains(seed, "女儿") || strings.Contains(seed, "孩子") || strings.Contains(seed, "daughter") || strings.Contains(seed, "child") {
		terms = append(terms, "daughter", "child", "family")
	}
	if strings.Contains(seed, "过敏") || strings.Contains(seed, "花粉") || strings.Contains(seed, "pollen") || strings.Contains(seed, "allergy") {
		terms = append(terms, "pollen", "allergy", "pollen allergy")
	}
	if strings.Contains(seed, "户外") || strings.Contains(seed, "出门") || strings.Contains(seed, "公园") || strings.Contains(seed, "outdoor") || strings.Contains(seed, "park") {
		terms = append(terms, "outdoor", "park", "outdoor plan")
	}
	return terms
}

func historyMessageRelevant(msg provider.Message, terms []string) bool {
	content := strings.ToLower(msg.Content)
	if msg.Role == "tool" {
		return true
	}
	if historyExplicitlyIrrelevant(content) {
		return false
	}
	for _, must := range []string{
		"context packer",
		"benchmark",
		"acceptance",
		"cmr",
		"constraint",
		"quality",
		"p95",
		"token",
		"latest user direction",
		"active task",
		"do not switch back",
		"do not return to",
		"最后一次确认",
		"不要回到",
	} {
		if strings.Contains(content, must) {
			return true
		}
	}
	if len(terms) == 0 {
		return false
	}
	hits := 0
	for _, term := range terms {
		if term != "" && strings.Contains(content, term) {
			hits++
			if hits >= 2 {
				return true
			}
		}
	}
	return false
}

func historyExplicitlyIrrelevant(content string) bool {
	for _, marker := range []string{
		"no benchmark relevance",
		"without benchmark relevance",
		"not benchmark relevant",
		"no current task relevance",
		"without current task relevance",
		"unrelated note",
		"unrelated response",
		"unrelated text",
		"unrelated project state",
	} {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

func (p *contextPlanner) summarizeConversationRangeWithLLM(ctx context.Context, sess *session.Session, messages []provider.Message, header string, tokenBudget int) string {
	if summary := p.tryLLMConversationSummary(ctx, sess, messages, header, tokenBudget); summary != "" {
		return summary
	}
	return summarizeConversationRange(messages, header, p.est, tokenBudget)
}

func (p *contextPlanner) tryLLMConversationSummary(ctx context.Context, sess *session.Session, messages []provider.Message, header string, tokenBudget int) string {
	if p == nil || p.agent == nil || p.agent.provider == nil || len(messages) < 8 || tokenBudget <= 0 {
		return ""
	}
	historyTokens := 0
	for _, msg := range messages {
		historyTokens += p.est.Estimate(msg.Content) + 4
	}
	threshold := p.agent.cfg.Get().Context.CompressionThreshold
	if threshold <= 0 {
		threshold = 0.8
	}
	if float64(historyTokens) < float64(tokenBudget)*threshold {
		return ""
	}

	var transcript strings.Builder
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		role := strings.ToUpper(msg.Role)
		transcript.WriteString(role)
		if msg.Name != "" {
			transcript.WriteString("(" + msg.Name + ")")
		}
		transcript.WriteString(": ")
		transcript.WriteString(truncate(content, 240))
		transcript.WriteString("\n")
	}
	if strings.TrimSpace(transcript.String()) == "" {
		return ""
	}

	prompt := fmt.Sprintf(
		"Summarize the following prior conversation for future context compression.\n"+
			"Return concise plain text under these headings only:\n"+
			"User topics:\nAssistant progress:\nTool evidence:\nOpen questions:\n"+
			"Keep the summary factual, preserve decisions, file/module names, errors, and unresolved items.\n"+
			"Avoid markdown bullets deeper than one level. Do not add commentary outside the summary.\n\nConversation:\n%s",
		transcript.String(),
	)

	sumCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resp, err := p.agent.provider.Chat(sumCtx, []provider.Message{
		{Role: "system", Content: "You compress prior conversation into a compact working-memory summary for an autonomous coding assistant."},
		{Role: "user", Content: prompt},
	})
	if err != nil || resp == nil {
		return ""
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return ""
	}
	content = header + "\n" + content
	content = p.fitTextToBudget(content, tokenBudget)
	if strings.TrimSpace(content) == "" {
		return ""
	}

	p.persistCompressedSummary(sess, messages, content)
	return content
}

func (p *contextPlanner) persistCompressedSummary(sess *session.Session, messages []provider.Message, summary string) {
	if p == nil || p.agent == nil || strings.TrimSpace(summary) == "" {
		return
	}

	if p.agent.midTerm == nil {
		return
	}

	sessionID := "context-compression"
	title := ""
	if sess != nil {
		if strings.TrimSpace(sess.ID) != "" {
			sessionID = sess.ID
		}
		title = strings.TrimSpace(sess.Title)
	}

	turns := make([]memory.ConversationTurn, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		turns = append(turns, memory.ConversationTurn{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	sessionSummary := memory.GenerateSessionSummary(sessionID, "context-compression", turns)
	if sessionSummary == nil {
		return
	}
	sessionSummary.RawSummary = strings.TrimSpace(summary)
	if title != "" {
		sessionSummary.Topics = append([]string{title}, sessionSummary.Topics...)
	}
	sessionSummary.Topics = utils.DedupStringsLimit(sessionSummary.Topics, 6)
	_ = p.agent.midTerm.SaveSessionSummary(sessionSummary)
}

/*
compactHistoryMessage 对历史消息做必要的压缩与清洗。
*/
func (p *contextPlanner) compactHistoryMessage(msg provider.Message) provider.Message {
	if len(msg.ContentParts) > 0 {
		msg.ContentParts = nil
	}
	if msg.Role == "tool" {
		msg.Content = compactToolResultForContext(msg.Name, msg.Content)
		return msg
	}
	if len(msg.Content) > 800 {
		msg.Content = p.fitTextToBudget(msg.Content, 240)
	}
	return msg
}

/*
fitTextToBudget 将文本裁剪到指定 token 预算内。
*/
func (p *contextPlanner) fitTextToBudget(text string, tokenBudget int) string {
	text = strings.TrimSpace(text)
	if text == "" || tokenBudget <= 0 {
		return ""
	}
	if p.est.Estimate(text) <= tokenBudget {
		return text
	}
	runes := []rune(text)
	lo, hi := 0, len(runes)
	best := ""
	for lo <= hi {
		mid := (lo + hi) / 2
		candidate := string(runes[:mid])
		if mid < len(runes) {
			candidate = strings.TrimSpace(candidate) + "\n...[truncated]"
		}
		if p.est.Estimate(candidate) <= tokenBudget {
			best = candidate
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best == "" {
		return ""
	}
	return best
}

/*
summarizeConversationRange 将一段对话消息压缩为适合上下文注入的摘要文本。
*/
func summarizeConversationRange(messages []provider.Message, header string, est *contextx.TokenEstimator, tokenBudget int) string {
	if len(messages) == 0 || tokenBudget <= 0 {
		return ""
	}
	var userLines []string
	var assistantLines []string
	var toolLines []string
	for _, msg := range messages {
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		switch msg.Role {
		case "user":
			userLines = append(userLines, "- "+truncate(text, 100))
		case "assistant":
			assistantLines = append(assistantLines, "- "+truncate(text, 100))
		case "tool":
			summary := summarizeToolResult(msg.Name, text)
			if summary != "" {
				toolLines = append(toolLines, "- "+summary)
			}
		}
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	if len(userLines) > 0 {
		b.WriteString("User topics:\n")
		for _, line := range utils.DedupStringsLimit(userLines, 4) {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(assistantLines) > 0 {
		b.WriteString("Assistant progress:\n")
		for _, line := range utils.DedupStringsLimit(assistantLines, 4) {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(toolLines) > 0 {
		b.WriteString("Tool evidence:\n")
		for _, line := range utils.DedupStringsLimit(toolLines, 4) {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		return ""
	}
	if est.Estimate(out) <= tokenBudget {
		return out
	}
	runes := []rune(out)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := strings.TrimSpace(string(runes)) + "\n...[truncated]"
		if est.Estimate(candidate) <= tokenBudget {
			return candidate
		}
	}
	return ""
}

/*
summarizeToolResult 对工具结果做短摘要，便于纳入上下文窗口。
*/
func summarizeToolResult(toolName, result string) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	switch toolName {
	case "web_search":
		query := extractLineAfterPrefix(result, "Results for:")
		if query != "" {
			return fmt.Sprintf("Searched for %s and collected source candidates.", query)
		}
		if strings.Contains(strings.ToLower(result), "no results found") {
			return "Search returned no useful results."
		}
		return "Collected search results from external sources."
	case "web_fetch":
		if strings.Contains(strings.ToLower(result), "failed to fetch") {
			return "Tried to fetch a page body but the fetch failed."
		}
		title := extractLineAfterPrefix(result, "# ")
		if title != "" {
			return fmt.Sprintf("Fetched page content: %s.", title)
		}
		return "Fetched page content and extracted key details."
	case "opencli":
		if strings.Contains(strings.ToLower(result), "failed") {
			return "Tried to access external content via OpenCLI but it failed."
		}
		title := extractLineAfterPrefix(result, "# ")
		if title != "" {
			return fmt.Sprintf("Extracted page content via OpenCLI: %s.", title)
		}
		return "Accessed external content via OpenCLI."
	default:
		return truncate(result, 120)
	}
}

/*
extractLineAfterPrefix 提取给定前缀后面的首行文本。
*/
func extractLineAfterPrefix(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

/*
aOrEmpty 将空字符串标准化为空值，其余值原样返回。
*/
func aOrEmpty(s string) string {
	return strings.TrimSpace(s)
}
