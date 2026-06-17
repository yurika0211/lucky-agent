package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/logger"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/telemetry"
	"github.com/yurika0211/luckyharness/internal/tool"
	"github.com/yurika0211/luckyharness/internal/utils"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var shellCommandSeparator = regexp.MustCompile(`\s*(?:;|&&|\|\|)\s*`)

// LoopState 代表 Agent Loop 的状态
type LoopState int

const (
	StateReason  LoopState = iota // 推理：分析用户意图，决定下一步
	StateAct                      // 行动：调用工具或生成回复
	StateObserve                  // 观察：处理工具结果，决定是否继续
	StateDone                     // 完成：输出最终结果
)

/*
String 返回 LoopState 的可读名称。
*/
func (s LoopState) String() string {
	switch s {
	case StateReason:
		return "Reason"
	case StateAct:
		return "Act"
	case StateObserve:
		return "Observe"
	case StateDone:
		return "Done"
	default:
		return "Unknown"
	}
}

// LoopConfig 是 Agent Loop 的配置
/*
LoopConfig 定义一次 Agent Loop 执行的关键参数。
*/
type LoopConfig struct {
	MaxIterations          int           // 最大循环次数
	Timeout                time.Duration // 单次循环超时
	AutoApprove            bool          // 自动批准工具调用 (--yolo)
	RepeatToolCallLimit    int           // 相同工具签名重复上限
	ToolOnlyIterationLimit int           // 连续纯工具轮次上限
	DuplicateFetchLimit    int           // 同一 URL 抓取上限
	DisabledTools          []string      // 本轮对模型隐藏的工具名
	Ephemeral              bool          // 临时后台执行，不写会话外持久化上下文
}

// DefaultLoopConfig 返回默认 Loop 配置
func DefaultLoopConfig() LoopConfig {
	return LoopConfig{
		MaxIterations:          10,
		Timeout:                60 * time.Second,
		AutoApprove:            false,
		RepeatToolCallLimit:    3,
		ToolOnlyIterationLimit: 3,
		DuplicateFetchLimit:    1,
	}
}

// maxAllowedIterations 是 MaxIterations 的硬上限
const maxAllowedIterations = 300

const (
	maxEmptyResponseRetries      = 2
	maxLengthContinuationRetries = 3
	searchSynthesisThreshold     = 2
	emptyResponseRecoveryPrompt  = "Your last response was empty. Please provide a direct, complete answer to my previous request. Avoid tool calls unless required."
	lengthRecoveryPrompt         = "Continue exactly from where you stopped. Do not repeat previous content."
	searchSynthesisPrompt        = "You now have enough search evidence from previous tool results. Synthesize a direct, source-aware answer now. Do not call any more tools unless a critical factual gap remains unresolved."
	emptyFinalResponseMessage    = "I couldn't produce a complete answer this round. Please retry."
	lengthTruncatedNotice        = "\n\n[Output may be truncated after multiple continuation attempts.]"
)

// sanitizeLoopConfig 校验并修正 LoopConfig 的安全边界
func sanitizeLoopConfig(cfg *LoopConfig) {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 10
	}
	if cfg.MaxIterations > maxAllowedIterations {
		cfg.MaxIterations = maxAllowedIterations
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Timeout > 10*time.Minute {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.RepeatToolCallLimit <= 0 {
		cfg.RepeatToolCallLimit = 3
	}
	if cfg.ToolOnlyIterationLimit <= 0 {
		cfg.ToolOnlyIterationLimit = 3
	}
	if cfg.DuplicateFetchLimit <= 0 {
		cfg.DuplicateFetchLimit = 1
	}
	cfg.DisabledTools = normalizeToolNameList(cfg.DisabledTools)
}

func normalizeToolNameList(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	return normalized
}

/*
appendContinuation 将一段续写内容追加到累计回复中。
*/
func appendContinuation(dst *strings.Builder, part string) {
	if strings.TrimSpace(part) == "" {
		return
	}
	dst.WriteString(part)
}

/*
canonicalToolArguments 将工具参数 JSON 规范化为稳定字符串。
*/
func canonicalToolArguments(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return ""
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}

	canonical, err := json.Marshal(decoded)
	if err != nil {
		return trimmed
	}
	return string(canonical)
}

/*
toolCallSignature 为工具调用生成用于比较的签名。
*/
func toolCallSignature(name, arguments string) string {
	return name + "|" + canonicalToolArguments(arguments)
}

/*
ApplyAgentLoopConfig 将配置文件中的 AgentLoopConfig 应用到运行时 LoopConfig。
*/
func ApplyAgentLoopConfig(loopCfg *LoopConfig, cfg config.AgentLoopConfig) {
	if cfg.MaxIterations > 0 {
		loopCfg.MaxIterations = cfg.MaxIterations
	}
	if cfg.TimeoutSeconds > 0 {
		loopCfg.Timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	loopCfg.AutoApprove = cfg.AutoApprove
	if cfg.RepeatToolCallLimit > 0 {
		loopCfg.RepeatToolCallLimit = cfg.RepeatToolCallLimit
	}
	if cfg.ToolOnlyIterationLimit > 0 {
		loopCfg.ToolOnlyIterationLimit = cfg.ToolOnlyIterationLimit
	}
	if cfg.DuplicateFetchLimit > 0 {
		loopCfg.DuplicateFetchLimit = cfg.DuplicateFetchLimit
	}
}

// LoopResult 是 Agent Loop 的执行结果
/*
LoopResult 描述一次 Agent Loop 执行完成后的结果摘要。
*/
type LoopResult struct {
	Response   string        // 最终回复
	Iterations int           // 实际循环次数
	ToolCalls  []toolCallLog // 工具调用记录
	State      LoopState     // 结束状态
	TokensUsed int           // 总 token 消耗
}

/*
toolCallLog 记录单次工具调用的参数、结果和耗时。
*/
type toolCallLog struct {
	Name      string
	Arguments string
	Result    string
	Duration  time.Duration
}

// RunLoop 执行 Agent Loop
func (a *Agent) RunLoop(ctx context.Context, userInput string, loopCfg LoopConfig) (*LoopResult, error) {
	return a.RunLoopWithSessionInput(ctx, nil, TextUserTurnInput(userInput), loopCfg)
}

// RunLoopWithSession 执行 Agent Loop（带会话上下文）
func (a *Agent) RunLoopWithSession(ctx context.Context, sess *session.Session, userInput string, loopCfg LoopConfig) (result *LoopResult, err error) {
	return a.RunLoopWithSessionInput(ctx, sess, TextUserTurnInput(userInput), loopCfg)
}

// RunLoopWithSessionInput 执行 Agent Loop（带结构化多模态输入）。
func (a *Agent) RunLoopWithSessionInput(ctx context.Context, sess *session.Session, turnInput UserTurnInput, loopCfg LoopConfig) (result *LoopResult, err error) {
	turnInput = turnInput.Normalize()
	routingText := turnInput.RoutingText
	a.maybeRouteModel(routingText)

	// 安全边界校验
	sanitizeLoopConfig(&loopCfg)
	a.applyIntentToolGating(&loopCfg, routingText)

	if startErr := a.StartAutonomy(ctx); startErr != nil && a.autonomy != nil {
		return nil, fmt.Errorf("start autonomy: %w", startErr)
	}

	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	startAt := time.Now()
	ctx, span := telemetry.StartSpan(ctx, "agent.loop",
		trace.WithAttributes(
			attribute.String("agent.session_id", sessionID),
			attribute.Int("agent.max_iterations", loopCfg.MaxIterations),
		),
	)
	defer span.End()
	logger.Info("agent loop started",
		"session_id", sessionID,
		"max_iterations", loopCfg.MaxIterations,
		"timeout_ms", loopCfg.Timeout.Milliseconds(),
		"auto_approve", loopCfg.AutoApprove,
	)
	defer func() {
		state := StateDone.String()
		iterations := 0
		tokens := 0
		if result != nil {
			state = result.State.String()
			iterations = result.Iterations
			tokens = result.TokensUsed
		}

		fields := []any{
			"session_id", sessionID,
			"state", state,
			"iterations", iterations,
			"tokens_used", tokens,
			"duration_ms", time.Since(startAt).Milliseconds(),
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			fields = append(fields, "error", err)
			logger.Warn("agent loop finished with error", fields...)
			return
		}
		span.SetStatus(codes.Ok, "")
		span.SetAttributes(
			attribute.String("agent.state", state),
			attribute.Int("agent.iterations", iterations),
			attribute.Int("agent.tokens_used", tokens),
			attribute.Int64("agent.duration_ms", time.Since(startAt).Milliseconds()),
		)
		logger.Info("agent loop finished", fields...)
	}()

	result = &LoopResult{
		State: StateReason,
	}
	finalize := func(response string, reasoningContent string) {
		response = utils.SanitizeToolProtocolOutput(response)
		response = appendNaturalCitations(response, result.ToolCalls)
		result.Response = response
		result.State = StateDone

		// 会话中保留 provider 级消息顺序：user -> assistant(tool call) -> tool -> assistant(final)
		if sess != nil {
			sess.AddProviderMessage(provider.Message{Role: "assistant", Content: response, ReasoningContent: reasoningContent})
		}

		// Final answers are not indexed into RAG by default: indexed source
		// material should stay separate from model-generated conclusions.
		if !loopCfg.Ephemeral && a.ragManager != nil && autoIndexFinalAnswersEnabled() {
			a.indexConversationTurn(routingText, response)
		}

		if !loopCfg.Ephemeral {
			if saveErr := a.saveFinalAnswerDocument(sessionID, routingText, response); saveErr != nil {
				logger.Warn("save final answer document failed", "session_id", sessionID, "error", saveErr)
			}
		}

		// v0.24.1: 保存会话到磁盘
		if sess != nil {
			if saveErr := sess.Save(); saveErr != nil {
				logger.Warn("agent session save failed", "session_id", sessionID, "error", saveErr)
			}
		}
	}
	loopState := newLoopRuntimeState()
	loopState.toolExecutionGuard = newToolExecutionGuard(routingText)
	memoryGate := a.buildMemoryToolGate(routingText, loopCfg.DisabledTools)

	// 构建初始消息
	buildOpts := defaultContextBuildOptions()
	buildOpts.DisabledTools = append([]string(nil), loopCfg.DisabledTools...)
	messages := a.buildContextMessagesForInput(ctx, sess, turnInput, buildOpts)
	if sess != nil {
		sess.AddProviderMessage(turnInput.Message)
	}

	callOpts := a.buildLoopCallOptions(routingText, loopCfg)

	for i := 0; i < loopCfg.MaxIterations; i++ {
		result.Iterations = i + 1
		result.State = StateReason
		logger.Debug("agent loop iteration started",
			"session_id", sessionID,
			"iteration", i+1,
			"messages", len(messages),
		)

		// Reason: 调用 LLM（带 function calling 支持）
		loopCtx, cancel := context.WithTimeout(ctx, loopCfg.Timeout)
		resp, err := a.chatLoopIteration(loopCtx, messages, callOpts, loopState.forceSearchSynthesis)
		cancel()

		if err != nil {
			return result, fmt.Errorf("loop iteration %d: %w", i+1, err)
		}

		result.TokensUsed += resp.TokensUsed
		applyTextToolCallsToResponse(resp, loopCfg.DisabledTools)

		// 检查是否有工具调用
		if len(resp.ToolCalls) > 0 {
			var finalized bool
			var finalResponse string
			messages, finalized, finalResponse = a.processToolCallBatch(resp, loopCfg, result, sess, messages, loopState, memoryGate)
			if finalized {
				if updatedMessages, enforced := a.executeMemoryGateForLoop(memoryGate, loopCfg, result, sess, messages, loopState); enforced {
					messages = updatedMessages
					continue
				}
				finalize(finalResponse, "")
				return result, nil
			}
			if updatedMessages, enforced := a.executeMemoryGateForLoop(memoryGate, loopCfg, result, sess, messages, loopState); enforced {
				messages = updatedMessages
			}
			continue // 继续循环，让 LLM 处理工具结果
		}

		if updatedMessages, enforced := a.executeMemoryGateForLoop(memoryGate, loopCfg, result, sess, messages, loopState); enforced {
			messages = updatedMessages
			continue
		}

		var finalized bool
		var finalResponse string
		messages, finalized, finalResponse = a.processDirectResponse(resp, messages, loopState)
		if !finalized {
			continue
		}
		finalize(finalResponse, directReasoningContent(resp, loopState))
		return result, nil
	}

	if strings.TrimSpace(loopState.continuedResponse.String()) != "" {
		finalize(strings.TrimSpace(loopState.continuedResponse.String())+lengthTruncatedNotice, strings.TrimSpace(loopState.continuedReasoning.String()))
		return result, nil
	}

	if memoryGate != nil && (memoryGate.shouldBlockFinal() || len(memoryGate.attemptedTools()) > 0) {
		result.Response = memoryGate.incompleteMessage()
		result.State = StateDone
		if sess != nil {
			if saveErr := sess.Save(); saveErr != nil {
				logger.Warn("agent session save failed", "session_id", sessionID, "error", saveErr)
			}
		}
		return result, fmt.Errorf("memory gate did not produce final synthesis")
	}

	// 达到最大循环次数
	result.Response = "Max iterations reached, last response may be incomplete"
	result.State = StateDone

	// v0.24.1: 保存会话到磁盘
	if sess != nil {
		if saveErr := sess.Save(); saveErr != nil {
			logger.Warn("agent session save failed", "session_id", sessionID, "error", saveErr)
		}
	}

	return result, fmt.Errorf("max iterations (%d) reached", loopCfg.MaxIterations)
}

/*
shouldForceSearchSynthesis 判断是否应强制进入搜索结果综合阶段。
*/
func shouldForceSearchSynthesis(successfulSearchEvidenceCount, consecutiveToolOnlyIters int) bool {
	if successfulSearchEvidenceCount >= 3 && consecutiveToolOnlyIters >= 1 {
		return true
	}
	return successfulSearchEvidenceCount >= searchSynthesisThreshold && consecutiveToolOnlyIters >= 2
}

/*
loopRuntimeState 收纳 RunLoopWithSession 在单次执行过程中的临时状态。

这些字段都只服务于当前这一轮 agent loop，不属于 Agent、Session 或 Memory 的长期状态。
把它们集中到一个结构体里，能让主循环更容易阅读和后续拆分。
*/
type loopRuntimeState struct {
	toolCallRepeatCount      map[string]int
	toolCallLastResult       map[string]string
	toolURLRepeatCount       map[string]int
	toolURLLastResult        map[string]string
	toolExecutionGuard       *toolExecutionGuard
	consecutiveToolOnlyIters int
	emptyResponseRetries     int
	lengthRecoveryCount      int
	successfulSearchEvidence int
	detailedSearchEvidence   int
	forceSearchSynthesis     bool
	continuedResponse        strings.Builder
	continuedReasoning       strings.Builder
}

/*
newLoopRuntimeState 创建并初始化一份新的循环临时状态。
*/
func newLoopRuntimeState() *loopRuntimeState {
	return &loopRuntimeState{
		toolCallRepeatCount: make(map[string]int),
		toolCallLastResult:  make(map[string]string),
		toolURLRepeatCount:  make(map[string]int),
		toolURLLastResult:   make(map[string]string),
	}
}

/*
processDirectResponse 处理模型在当前轮次没有返回工具调用时的回复路径。

它负责：
1. 空回复恢复
2. 长度截断续写恢复
3. continuation 拼接
4. 决定是否直接结束本轮

返回值含义：
- updatedMessages: 可能附加了恢复提示的消息序列
- finalized: 是否已经得到最终响应
- finalResponse: 当 finalized=true 时的最终输出
*/
func (a *Agent) processDirectResponse(
	resp *provider.Response,
	messages []provider.Message,
	loopState *loopRuntimeState,
) (updatedMessages []provider.Message, finalized bool, finalResponse string) {
	raw := resp.Content
	clean := strings.TrimSpace(raw)

	if clean == "" {
		if loopState.emptyResponseRetries < maxEmptyResponseRetries {
			loopState.emptyResponseRetries++
			messages = append(messages, provider.Message{Role: "assistant", Content: raw, ReasoningContent: resp.ReasoningContent})
			messages = append(messages, provider.Message{Role: "user", Content: emptyResponseRecoveryPrompt})
			return messages, false, ""
		}
		if strings.TrimSpace(loopState.continuedResponse.String()) != "" {
			return messages, true, strings.TrimSpace(loopState.continuedResponse.String())
		}
		return messages, true, emptyFinalResponseMessage
	}
	loopState.emptyResponseRetries = 0

	if strings.EqualFold(resp.FinishReason, "length") {
		appendContinuation(&loopState.continuedResponse, raw)
		appendContinuation(&loopState.continuedReasoning, resp.ReasoningContent)
		if loopState.lengthRecoveryCount < maxLengthContinuationRetries {
			loopState.lengthRecoveryCount++
			messages = append(messages, provider.Message{Role: "assistant", Content: raw, ReasoningContent: resp.ReasoningContent})
			messages = append(messages, provider.Message{Role: "user", Content: lengthRecoveryPrompt})
			return messages, false, ""
		}
		partial := strings.TrimSpace(loopState.continuedResponse.String())
		if partial == "" {
			partial = clean
		}
		return messages, true, partial + lengthTruncatedNotice
	}
	loopState.lengthRecoveryCount = 0

	if strings.TrimSpace(loopState.continuedResponse.String()) != "" {
		appendContinuation(&loopState.continuedResponse, raw)
		appendContinuation(&loopState.continuedReasoning, resp.ReasoningContent)
		return messages, true, strings.TrimSpace(loopState.continuedResponse.String())
	}

	return messages, true, raw
}

func directReasoningContent(resp *provider.Response, loopState *loopRuntimeState) string {
	if loopState == nil || strings.TrimSpace(loopState.continuedReasoning.String()) == "" {
		if resp == nil {
			return ""
		}
		return resp.ReasoningContent
	}
	return strings.TrimSpace(loopState.continuedReasoning.String())
}

/*
processToolCallBatch 处理模型在单轮中返回的一整批 tool_calls。

它负责：
1. 重复工具调用检测
2. 记录 assistant/tool 消息到上下文与 session
3. 按并发安全策略执行工具
4. 按原始顺序收集并回写工具结果
5. 触发必要的搜索综合提示

返回值含义：
- updatedMessages: 工具结果回写后的消息序列
- finalized: 是否已经直接结束本轮并产出最终响应
- finalResponse: 当 finalized=true 时的最终响应文本
*/
func (a *Agent) processToolCallBatch(
	resp *provider.Response,
	loopCfg LoopConfig,
	result *LoopResult,
	sess *session.Session,
	messages []provider.Message,
	loopState *loopRuntimeState,
	memoryGate *memoryToolGate,
) (updatedMessages []provider.Message, finalized bool, finalResponse string) {
	logger.Info("agent loop tool call batch",
		"session_id", func() string {
			if sess != nil {
				return sess.ID
			}
			return ""
		}(),
		"count", len(resp.ToolCalls),
	)
	loopState.emptyResponseRetries = 0
	loopState.lengthRecoveryCount = 0
	result.State = StateAct
	if strings.TrimSpace(resp.Content) == "" {
		loopState.consecutiveToolOnlyIters++
	} else {
		loopState.consecutiveToolOnlyIters = 0
	}

	repeatedSigs := make([]string, 0, len(resp.ToolCalls))
	allRepeated := true
	for _, tc := range resp.ToolCalls {
		sig := toolCallSignature(tc.Name, tc.Arguments)
		repeatedSigs = append(repeatedSigs, sig)
		loopState.toolCallRepeatCount[sig]++
		if key := normalizedToolTarget(tc.Name, tc.Arguments); key != "" {
			loopState.toolURLRepeatCount[key]++
		}
		if loopState.toolCallRepeatCount[sig] < loopCfg.RepeatToolCallLimit {
			allRepeated = false
		}
	}
	if (allRepeated && strings.TrimSpace(resp.Content) == "") || loopState.consecutiveToolOnlyIters >= loopCfg.ToolOnlyIterationLimit {
		if !loopState.forceSearchSynthesis && loopState.successfulSearchEvidence > 0 {
			loopState.forceSearchSynthesis = true
			messages = append(messages, provider.Message{
				Role:    "user",
				Content: searchSynthesisPrompt,
			})
			return messages, false, ""
		}
		var b strings.Builder
		b.WriteString("Detected repeated tool-call loop and stopped early to avoid timeout.\n")
		b.WriteString("Latest tool outputs:\n")
		for _, sig := range repeatedSigs {
			parts := strings.SplitN(sig, "|", 2)
			name := parts[0]
			out := strings.TrimSpace(loopState.toolCallLastResult[sig])
			if out == "" {
				out = "(no cached output)"
			}
			if len(out) > 240 {
				out = out[:240] + "...(truncated)"
			}
			b.WriteString(fmt.Sprintf("- %s: %s\n", name, out))
		}
		return messages, true, strings.TrimSpace(b.String())
	}

	messages = append(messages, provider.Message{
		Role:             "assistant",
		Content:          resp.Content,
		ReasoningContent: resp.ReasoningContent,
		ToolCalls:        resp.ToolCalls,
	})
	if sess != nil {
		sess.AddProviderMessage(provider.Message{
			Role:             "assistant",
			Content:          resp.Content,
			ReasoningContent: resp.ReasoningContent,
			ToolCalls:        resp.ToolCalls,
		})
	}

	executed := a.executeToolCallsOrderedGuarded(
		resp.ToolCalls,
		loopCfg.AutoApprove,
		sess,
		loopState.toolURLRepeatCount,
		loopState.toolURLLastResult,
		loopCfg.DuplicateFetchLimit,
		false,
		loopState.toolExecutionGuard,
	)

	for _, execResult := range executed {
		if memoryGate != nil {
			memoryGate.markExecuted(execResult.ToolCall.Name, execResult.Result)
		}
		tcLog := toolCallLog{
			Name:      execResult.ToolCall.Name,
			Arguments: execResult.ToolCall.Arguments,
			Result:    execResult.Result,
			Duration:  execResult.Duration,
		}
		result.ToolCalls = append(result.ToolCalls, tcLog)

		contextToolMsg := provider.Message{
			Role:       "tool",
			Content:    buildContextToolResult(execResult.ToolCall.Name, execResult.Result, &loopState.successfulSearchEvidence, &loopState.detailedSearchEvidence),
			ToolCallID: execResult.ToolCall.ID,
			Name:       execResult.ToolCall.Name,
		}
		loopState.toolCallLastResult[toolCallSignature(tcLog.Name, tcLog.Arguments)] = tcLog.Result
		if key := normalizedToolTarget(tcLog.Name, tcLog.Arguments); key != "" {
			loopState.toolURLLastResult[key] = tcLog.Result
		}
		messages = append(messages, contextToolMsg)
		if sess != nil {
			sess.AddProviderMessage(contextToolMsg)
		}
	}

	messages = a.fitContextWindow(messages)
	result.State = StateObserve

	messages = maybeAppendSearchSynthesisMessage(messages, &loopState.forceSearchSynthesis, loopState.successfulSearchEvidence, loopState.consecutiveToolOnlyIters)

	return messages, false, ""
}

/*
isUsefulSearchEvidence 判断某个工具结果是否可作为有效搜索证据。
*/
func isUsefulSearchEvidence(toolName, result string) bool {
	if toolName != "web_search" && toolName != "web_fetch" && toolName != "opencli" {
		return false
	}
	out := strings.TrimSpace(result)
	if out == "" {
		return false
	}
	lower := strings.ToLower(out)
	if strings.HasPrefix(lower, "error:") {
		return false
	}
	if strings.Contains(lower, "no results found for") {
		return false
	}
	if strings.Contains(lower, "all search sources failed") {
		return false
	}
	return true
}

/*
normalizedToolTarget 提取并归一化工具调用的主要目标对象。
*/
func normalizedToolTarget(toolName, arguments string) string {
	if toolName != "web_fetch" && toolName != "opencli" {
		return ""
	}

	var args map[string]any
	if arguments == "" {
		return ""
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return ""
	}
	rawURL, _ := args["url"].(string)
	if rawURL == "" && toolName == "opencli" {
		rawURL = openCLIToolURLFromArgs(args)
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	return u.String()
}

func openCLIToolURLFromArgs(args map[string]any) string {
	rawArgs, ok := args["args"]
	if !ok {
		return ""
	}
	items, ok := rawArgs.([]any)
	if !ok {
		return ""
	}
	for i, item := range items {
		value, ok := item.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "--url=") {
			return strings.TrimSpace(strings.TrimPrefix(value, "--url="))
		}
		if value == "--url" && i+1 < len(items) {
			next, _ := items[i+1].(string)
			return strings.TrimSpace(next)
		}
	}
	return ""
}

/*
executeToolMaybeDedup 执行工具，并在需要时避免重复抓取相同目标。
*/
func (a *Agent) executeToolMaybeDedup(name, arguments string, autoApprove bool, sess *session.Session, toolURLRepeatCount map[string]int, toolURLLastResult map[string]string, duplicateFetchLimit int) (string, error) {
	if key := normalizedToolTarget(name, arguments); key != "" && toolURLRepeatCount[key] > duplicateFetchLimit {
		if cached := strings.TrimSpace(toolURLLastResult[key]); cached != "" {
			return fmt.Sprintf("Skipped duplicate %s for %s. Reuse previous fetched content.\n\n%s", name, key, cached), nil
		}
		return fmt.Sprintf("Skipped duplicate %s for %s. Reuse earlier fetched content.", name, key), nil
	}
	return a.executeToolWithSession(name, arguments, autoApprove, sess)
}

/*
compactToolResultForContext 将工具结果压缩成适合继续放入上下文的文本。
*/
func compactToolResultForContext(toolName, result string) string {
	out := strings.TrimSpace(result)
	if out == "" {
		return result
	}

	summary := summarizeToolResult(toolName, out)
	if summary != "" && toolName != "file_list" {
		out = fmt.Sprintf("[Tool Summary]\n- tool: %s\n- finding: %s\n\n%s", toolName, summary, out)
	}

	limit := 4000
	switch toolName {
	case "web_search":
		limit = 900
	case "web_fetch", "opencli":
		limit = 1800
	case "file_list":
		limit = 600
	}

	if len(out) <= limit {
		return out
	}
	return out[:limit] + "\n... (truncated for context)"
}

// extractRequiredToolNames 从用户输入中提取显式点名的工具（按出现顺序）。
/*
extractRequiredToolNames 从用户输入中提取被显式点名要求使用的工具名。
*/
func (a *Agent) extractRequiredToolNames(input string) []string {
	type hit struct {
		name string
		pos  int
	}

	var hits []hit
	for _, t := range a.tools.ListEnabled() {
		searchFrom := 0
		for {
			idx := strings.Index(input[searchFrom:], t.Name)
			if idx < 0 {
				break
			}
			pos := searchFrom + idx
			if isExplicitRequiredToolMention(input, pos, t.Name) {
				hits = append(hits, hit{name: t.Name, pos: pos})
			}
			searchFrom = pos + len(t.Name)
		}
	}
	if len(hits) == 0 {
		return nil
	}

	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].pos < hits[j].pos
	})

	seen := make(map[string]struct{}, len(hits))
	result := make([]string, 0, len(hits))
	for _, h := range hits {
		if _, ok := seen[h.name]; ok {
			continue
		}
		seen[h.name] = struct{}{}
		result = append(result, h.name)
	}
	return result
}

func isExplicitRequiredToolMention(input string, pos int, name string) bool {
	if pos < 0 || strings.TrimSpace(name) == "" {
		return false
	}
	if pos > len(input) {
		pos = len(input)
	}
	start := pos - 64
	if start < 0 {
		start = 0
	}
	end := pos + len(name) + 32
	if end > len(input) {
		end = len(input)
	}
	window := strings.ToLower(input[start:end])
	return strings.Contains(window, "必须调用") ||
		strings.Contains(window, "强制调用") ||
		strings.Contains(window, "调用 "+name) ||
		strings.Contains(window, "调用"+name) ||
		strings.Contains(window, "用 "+name) ||
		strings.Contains(window, "用"+name) ||
		strings.Contains(window, "use "+strings.ToLower(name)) ||
		strings.Contains(window, "call "+strings.ToLower(name)) ||
		strings.Contains(window, "tool "+strings.ToLower(name)) ||
		strings.Contains(window, "to="+strings.ToLower(name))
}

// fitContextWindow 裁剪消息列表到上下文窗口内
/*
fitContextWindow 将消息序列裁剪到上下文窗口预算内。
*/
func (a *Agent) fitContextWindow(messages []provider.Message) []provider.Message {
	if a == nil || a.contextWin == nil {
		return messages
	}
	for _, msg := range messages {
		if len(msg.ContentParts) > 0 {
			return messages
		}
	}
	contextMessages := a.toContextMessages(messages)
	fitted, trimResult := a.contextWin.Fit(contextMessages)
	if trimResult.Trimmed {
		messages = a.fromContextMessages(fitted)
	}
	return messages
}

// indexConversationTurn 将最终答案索引进 RAG（异步执行）
/*
indexConversationTurn 异步将本轮最终答案写入 RAG。
它使用每轮唯一的 source，避免固定 source 导致的新答案覆盖旧答案问题。
*/
func (a *Agent) indexConversationTurn(userInput, assistantResponse string) {
	go func() {
		source, title, content, ok := buildFinalAnswerRAGDocument(userInput, assistantResponse)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if _, err := a.ragManager.IndexText(source, title, content); err != nil {
			// 索引失败不影响主流程，静默忽略
			_ = ctx.Err()
		}
	}()
}

func autoIndexFinalAnswersEnabled() bool {
	value := strings.TrimSpace(os.Getenv("LH_RAG_INDEX_FINAL_ANSWERS"))
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

/*
buildFinalAnswerRAGDocument 将最终答案整理成适合写入 RAG 的文档。
*/
func buildFinalAnswerRAGDocument(userInput, assistantResponse string) (source, title, content string, ok bool) {
	assistantResponse = strings.TrimSpace(utils.SanitizeToolProtocolOutput(assistantResponse))
	userInput = strings.TrimSpace(userInput)
	if assistantResponse == "" {
		return "", "", "", false
	}

	title = userInput
	if title == "" {
		title = "Final Answer"
	}
	if len(title) > 80 {
		title = title[:80] + "..."
	}

	var b strings.Builder
	b.WriteString("Final Answer:\n")
	b.WriteString(assistantResponse)
	if userInput != "" {
		b.WriteString("\n\nUser Request:\n")
		b.WriteString(userInput)
	}
	content = b.String()

	h := fnv.New64a()
	_, _ = h.Write([]byte(userInput))
	_, _ = h.Write([]byte{'\n'})
	_, _ = h.Write([]byte(assistantResponse))
	source = fmt.Sprintf("conversation/final/%x", h.Sum64())
	return source, title, content, true
}

/*
saveFinalAnswerDocument 将本轮最终答案落盘为 Markdown 文档。
*/
func (a *Agent) saveFinalAnswerDocument(sessionID, userInput, assistantResponse string) error {
	if a == nil || a.cfg == nil {
		return nil
	}

	source, title, content, ok := buildFinalAnswerRAGDocument(userInput, assistantResponse)
	if !ok {
		return nil
	}

	dir := filepath.Join(a.cfg.HomeDir(), "knowledge", "final_answers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create final answer dir: %w", err)
	}

	filename := buildFinalAnswerFilename(source, time.Now())
	path := filepath.Join(dir, filename)

	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString("- Generated At: ")
	b.WriteString(time.Now().Format(time.RFC3339))
	b.WriteString("\n")
	if strings.TrimSpace(sessionID) != "" {
		b.WriteString("- Session ID: ")
		b.WriteString(sessionID)
		b.WriteString("\n")
	}
	b.WriteString("- Source: ")
	b.WriteString(source)
	b.WriteString("\n\n")
	b.WriteString("## Final Answer\n\n")
	b.WriteString(strings.TrimSpace(utils.SanitizeToolProtocolOutput(assistantResponse)))
	if strings.TrimSpace(userInput) != "" {
		b.WriteString("\n\n## User Request\n\n")
		b.WriteString(strings.TrimSpace(userInput))
	}
	b.WriteString("\n\n## RAG Content\n\n")
	b.WriteString(content)
	b.WriteString("\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write final answer doc: %w", err)
	}
	return nil
}

/*
buildFinalAnswerFilename 为最终答案文档生成稳定且可读的文件名。
*/
func buildFinalAnswerFilename(source string, now time.Time) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(source))
	return fmt.Sprintf("%s_%x.md", now.Format("20060102_150405"), h.Sum64())
}

// executeToolWithSession 执行工具调用（带 session，支持 shell 上下文持久化）
/*
executeToolWithSession 在带会话上下文的情况下执行一次工具调用。
*/
func (a *Agent) executeToolWithSession(name, arguments string, autoApprove bool, sess *session.Session) (output string, err error) {
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	startAt := time.Now()
	logger.Debug("tool execution started",
		"session_id", sessionID,
		"tool", name,
		"auto_approve", autoApprove,
	)
	defer func() {
		fields := []any{
			"session_id", sessionID,
			"tool", name,
			"duration_ms", time.Since(startAt).Milliseconds(),
		}
		if err != nil {
			fields = append(fields, "error", err)
			logger.Warn("tool execution failed", fields...)
			return
		}
		fields = append(fields, "output_bytes", len(output))
		logger.Info("tool execution completed", fields...)
	}()

	// 解析参数
	var args map[string]any
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			args = map[string]any{"raw": arguments}
		}
	}

	// 构建 shell 上下文
	var sc *tool.ShellContext
	if sess != nil {
		cwd := sess.GetCwd()
		env := sess.GetEnv()
		if cwd != "" || len(env) > 0 {
			sc = &tool.ShellContext{
				Cwd: cwd,
				Env: env,
			}
		}
	}

	// 通过 Gateway 执行
	var result *tool.GatewayResult
	if sc != nil {
		result, err = a.gateway.ExecuteWithShellContext(name, args, "", sc)
	} else {
		result, err = a.gateway.Execute(name, args, "")
	}
	if err != nil {
		return "", err
	}

	// terminal 执行后更新 session 的 cwd/env
	if sess != nil && name == "terminal" {
		a.updateShellContext(sess, arguments, result.Output)
	}

	output = result.Output
	if a.hooks.Enabled() {
		// PostToolUse: 允许 hook 在工具结果回上下文前改写/脱敏/截断。
		// source 暂传空（匹配全部来源），TODO 接入网关来源。
		output = a.hooks.RunPost(name, arguments, "", sessionID, output, nil)
	}
	return output, nil
}

// updateShellContext 从 shell 执行结果中提取 cwd 和 env 变更
func (a *Agent) updateShellContext(sess *session.Session, command, output string) {
	_ = output

	currentCwd := strings.TrimSpace(sess.GetCwd())
	for _, segment := range splitShellCommands(command) {
		segment = strings.TrimSpace(segment)
		if segment == "" || strings.Contains(segment, "|") {
			continue
		}

		lower := strings.ToLower(segment)
		switch {
		case strings.HasPrefix(lower, "cd "):
			target := strings.TrimSpace(segment[len("cd "):])
			if target == "" {
				continue
			}
			resolved := resolveShellPath(currentCwd, target)
			if info, err := os.Stat(resolved); err == nil && info.IsDir() {
				sess.SetCwd(resolved)
				currentCwd = resolved
			}
		case strings.HasPrefix(lower, "export "):
			key, value, ok := parseShellExport(segment[len("export "):])
			if ok {
				sess.SetEnv(key, value)
			}
		case strings.HasPrefix(lower, "unset "):
			for _, key := range strings.Fields(segment[len("unset "):]) {
				key = strings.TrimSpace(key)
				if key != "" {
					sess.UnsetEnv(key)
				}
			}
		}
	}
}

/*
splitShellCommands 按常见 shell 连接符拆分命令片段。
*/
func splitShellCommands(command string) []string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil
	}
	return shellCommandSeparator.Split(trimmed, -1)
}

/*
resolveShellPath 根据当前工作目录解析 shell 中的路径参数。
*/
func resolveShellPath(baseCwd, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	if baseCwd != "" {
		return filepath.Clean(filepath.Join(baseCwd, target))
	}
	if abs, err := filepath.Abs(target); err == nil {
		return abs
	}
	return filepath.Clean(target)
}

/*
parseShellExport 解析 export 语句中的键值对。
*/
func parseShellExport(expr string) (string, string, bool) {
	key, value, ok := strings.Cut(strings.TrimSpace(expr), "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return "", "", false
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		value = unquoted
	}
	return key, value, true
}

// buildMessages 构建消息列表
/*
buildMessages 使用默认上下文选项构造一次普通对话的消息列表。
*/
func (a *Agent) buildMessages(userInput string) []provider.Message {
	return a.buildContextMessages(context.Background(), nil, userInput, defaultContextBuildOptions())
}

// isToolParallelSafe 检查工具是否可安全并发执行
/*
isToolParallelSafe 判断某个工具是否可以安全并发执行。
*/
func (a *Agent) isToolParallelSafe(toolName string) bool {
	t, ok := a.tools.Get(toolName)
	if !ok {
		return false // 未知工具保守处理
	}
	return t.ParallelSafe
}

// ParallelSummarizeThreshold 触发并行摘要的对话条数阈值
const ParallelSummarizeThreshold = 20

// ParallelSummarize 并行摘要对话历史
// 当对话超过阈值时，将对话分成两半，用 goroutine 并行调用 LLM 摘要
// 合并结果替换原对话
/*
ParallelSummarize 对消息列表执行并行摘要压缩。
*/
func (a *Agent) ParallelSummarize(messages []provider.Message) ([]provider.Message, error) {
	if len(messages) <= ParallelSummarizeThreshold {
		return messages, nil // 未达阈值，不处理
	}

	// 保留 system 消息
	var systemMsgs []provider.Message
	var conversationMsgs []provider.Message
	for _, msg := range messages {
		if msg.Role == "system" {
			systemMsgs = append(systemMsgs, msg)
		} else {
			conversationMsgs = append(conversationMsgs, msg)
		}
	}

	if len(conversationMsgs) <= ParallelSummarizeThreshold {
		return messages, nil
	}

	// 将对话分成两半
	mid := len(conversationMsgs) / 2
	firstHalf := conversationMsgs[:mid]
	secondHalf := conversationMsgs[mid:]

	// 使用 channel 收集摘要结果
	type summarizeResult struct {
		summary string
		err     error
	}
	resultCh := make(chan summarizeResult, 2)

	// 定义摘要 prompt
	summarizePrompt := func(msgs []provider.Message, part string) string {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Please summarize the following conversation %s concisely in 2-3 sentences. ", part))
		sb.WriteString("Preserve key information, decisions, and action items. ")
		sb.WriteString("Output only the summary, no additional commentary.\n\n")
		sb.WriteString("Conversation:\n")
		for _, m := range msgs {
			role := m.Role
			if role == "assistant" {
				role = "Assistant"
			} else {
				role = "User"
			}
			sb.WriteString(fmt.Sprintf("%s: %s\n", role, m.Content))
		}
		return sb.String()
	}

	// 并发摘要两半对话
	ctx := context.Background()

	// 第一部分
	go func() {
		prompt := summarizePrompt(firstHalf, "(first part)")
		messages := []provider.Message{
			{Role: "system", Content: "You are a helpful assistant that summarizes conversations."},
			{Role: "user", Content: prompt},
		}

		resp, err := a.provider.Chat(ctx, messages)
		if err != nil {
			resultCh <- summarizeResult{summary: "", err: err}
			return
		}
		resultCh <- summarizeResult{summary: resp.Content, err: nil}
	}()

	// 第二部分
	go func() {
		prompt := summarizePrompt(secondHalf, "(second part)")
		messages := []provider.Message{
			{Role: "system", Content: "You are a helpful assistant that summarizes conversations."},
			{Role: "user", Content: prompt},
		}

		resp, err := a.provider.Chat(ctx, messages)
		if err != nil {
			resultCh <- summarizeResult{summary: "", err: err}
			return
		}
		resultCh <- summarizeResult{summary: resp.Content, err: nil}
	}()

	// 收集两个摘要结果
	var firstSummary, secondSummary string
	for i := 0; i < 2; i++ {
		result := <-resultCh
		if result.err != nil {
			// 如果摘要失败，返回原始消息
			return messages, result.err
		}
		if firstSummary == "" {
			firstSummary = result.summary
		} else {
			secondSummary = result.summary
		}
	}

	// 合并摘要
	var summaryContent strings.Builder
	summaryContent.WriteString("[Conversation Summary - Parallel Summarization]\n")
	summaryContent.WriteString(fmt.Sprintf("First Part Summary: %s\n", firstSummary))
	summaryContent.WriteString(fmt.Sprintf("Second Part Summary: %s\n", secondSummary))
	summaryContent.WriteString("\n[End Summary]\n")

	// 构建新的消息列表：system + 摘要 + 最近少量原始消息
	newMessages := make([]provider.Message, 0, len(systemMsgs)+1+5)

	// 添加 system 消息
	newMessages = append(newMessages, systemMsgs...)

	// 添加摘要消息
	newMessages = append(newMessages, provider.Message{
		Role:    "system",
		Content: summaryContent.String(),
	})

	// 保留最后 5 条原始对话作为上下文
	keepCount := 5
	if keepCount > len(conversationMsgs) {
		keepCount = len(conversationMsgs)
	}
	startIdx := len(conversationMsgs) - keepCount
	newMessages = append(newMessages, conversationMsgs[startIdx:]...)

	return newMessages, nil
}
