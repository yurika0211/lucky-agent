package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yurika0211/lucky-agent/agent/artifact"
	"github.com/yurika0211/lucky-agent/agent/textcall"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/sandbox"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/turn"
	"github.com/yurika0211/luckyagent-runtime/utils"
)

// ChatEvent 是流式对话事件，包含思考过程和内容
/*
ChatEvent 描述面向上层流式 UI 的聊天事件。
*/
type ChatEvent struct {
	Type        ChatEventType
	Content     string
	TaskID      string // durable foreground task identifier, when available
	Name        string // 工具名（Type=EventToolCall 时）
	Args        string // 工具参数
	Result      string // 工具结果
	Round       int    // 所属轮次（Type=ChatEventReasoningContent 时使用）
	Observation *ObservationEvent
	Approval    *ApprovalEvent
	Compact     *CompactProgress     // session compact progress (Type=ChatEventCompact)
	Usage       *provider.TokenUsage // aggregate provider usage for ChatEventDone
	CreatedAt   *time.Time           // server timestamp of the final assistant message
	Err         error
}

// ObservationEvent is safe-to-serialize metadata for a computer frame. The
// local file path is intentionally omitted from the public event payload.
type ObservationEvent struct {
	FrameID string `json:"frame_id,omitempty"`
	// FilePath is available to in-process gateway consumers (for example
	// Telegram) to upload the frame. It is intentionally omitted from public
	// JSON/SSE payloads because local filesystem paths are not API resources.
	FilePath     string  `json:"-"`
	MimeType     string  `json:"mime_type,omitempty"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	ScaleFactor  float64 `json:"scale_factor,omitempty"`
	DisplayID    string  `json:"display_id,omitempty"`
	ActiveWindow string  `json:"active_window,omitempty"`
}

type ApprovalEvent struct {
	RequestID string            `json:"request_id,omitempty"`
	Tool      string            `json:"tool,omitempty"`
	Action    string            `json:"action,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	FrameID   string            `json:"frame_id,omitempty"`
	Kind      string            `json:"kind,omitempty"`
	Prompt    string            `json:"prompt,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	Secure    bool              `json:"secure,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// ChatEventType 事件类型
type ChatEventType int

const (
	ChatEventThinking         ChatEventType = iota // 🧠 思考中
	ChatEventToolCall                              // 🔧 工具调用
	ChatEventToolResult                            // 📋 工具结果
	ChatEventContent                               // 📝 内容片段
	ChatEventDone                                  // ✅ 完成
	ChatEventError                                 // ❌ 错误
	ChatEventObservation                           // 🖼️ computer observation
	ChatEventApprovalRequired                      // 🔐 approval required
	ChatEventReasoningContent                      // 🧠 真实推理摘要内容
	ChatEventCompact                               // 📦 session context compact progress
)

// StreamMode 流式输出模式
type StreamMode string

const (
	// StreamModeNative 真流式：直接使用 provider 的 ChatStream，逐 chunk 推送
	StreamModeNative StreamMode = "native"
	// StreamModeSimulated 模拟流式：先非流式获取完整响应，再按句子边界逐段推送
	StreamModeSimulated StreamMode = "simulated"
)

// DefaultStreamMode 默认流式模式
const DefaultStreamMode = StreamModeNative

// getStreamMode 获取当前流式模式配置
/*
getStreamMode 返回当前 Agent 使用的流式输出模式。
*/
func (a *Agent) getStreamMode() StreamMode {
	if a.cfg == nil {
		return DefaultStreamMode
	}
	cfg := a.cfg.Get()
	mode := StreamMode(cfg.StreamMode)
	if mode != StreamModeNative && mode != StreamModeSimulated {
		return DefaultStreamMode
	}
	return mode
}

/*
streamConvergenceState 保存流式对话在多轮推理中的收敛与去重状态。
*/
type streamConvergenceState struct {
	ephemeral bool
	// provider is captured at stream-turn start and reused by every recursive
	// iteration. It must remain immutable for the lifetime of the stream.
	provider                       providerSnapshot
	autoApprove                    bool
	source                         string
	emptyResponseRetries           int
	toolCallRepeatCount            map[string]int
	toolCallLastResult             map[string]string
	failureRepeatCount             map[string]int
	toolURLRepeatCount             map[string]int
	toolURLLastResult              map[string]string
	toolExecutionGuard             *toolExecutionGuard
	consecutiveToolOnlyIters       int
	consecutiveComputerObserveOnly int
	successfulSearchEvidence       int
	detailedSearchEvidence         int
	forceSearchSynthesis           bool
	repeatToolCallLimit            int
	toolOnlyIterationLimit         int
	duplicateFetchLimit            int
	disabledTools                  []string
	memoryGate                     *memoryToolGate
	citationToolCalls              []toolCallLog
	iterationTimeout               time.Duration
	artifactGuard                  *artifact.Guard
	pendingDelegateTaskIDs         map[string]struct{}
	usage                          *provider.TokenUsage
	runID                          string
}

func (s *streamConvergenceState) addUsage(details *provider.UsageDetails, model string) {
	if s == nil || details == nil {
		return
	}
	if s.usage == nil {
		s.usage = &provider.TokenUsage{}
	}
	s.usage.AddDetails(details, model)
}

func (s *streamConvergenceState) addResponseUsage(resp *provider.Response) {
	if s == nil || resp == nil || (resp.Usage == nil && resp.TokensUsed <= 0) {
		return
	}
	if s.usage == nil {
		s.usage = &provider.TokenUsage{}
	}
	s.usage.AddResponse(resp)
}

func (s *streamConvergenceState) hasPendingDelegateTasks() bool {
	return s != nil && len(s.pendingDelegateTaskIDs) > 0
}

func (s *streamConvergenceState) pendingDelegateWaitMessage() (string, bool) {
	if !s.hasPendingDelegateTasks() {
		return "", false
	}
	ids := make([]string, 0, len(s.pendingDelegateTaskIDs))
	for taskID := range s.pendingDelegateTaskIDs {
		ids = append(ids, taskID)
	}
	slices.Sort(ids)
	encoded, _ := json.Marshal(ids)
	return "Delegated tasks have not been collected yet. Call wait_for_tasks with task_ids=" + string(encoded) + " and include_result=true. Do not finalize until it returns all_terminal=true; then synthesize the returned results.", true
}

func (s *streamConvergenceState) recordDelegateTaskToolResult(name, arguments, result string) {
	if s == nil {
		return
	}
	var payload struct {
		TaskID      string `json:"task_id"`
		AllTerminal bool   `json:"all_terminal"`
		Tasks       []struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return
	}
	action := ""
	if strings.EqualFold(strings.TrimSpace(name), "subagent") {
		var args map[string]any
		_ = json.Unmarshal([]byte(arguments), &args)
		action, _ = args["action"].(string)
		action = strings.ToLower(strings.TrimSpace(action))
	}
	switch action {
	case "delegate":
		if taskID := strings.TrimSpace(payload.TaskID); taskID != "" {
			if s.pendingDelegateTaskIDs == nil {
				s.pendingDelegateTaskIDs = make(map[string]struct{})
			}
			s.pendingDelegateTaskIDs[taskID] = struct{}{}
		}
	case "wait":
		if !payload.AllTerminal {
			return
		}
		for _, task := range payload.Tasks {
			if taskID := strings.TrimSpace(task.TaskID); taskID != "" && isTerminalDelegateTaskStatus(task.Status) {
				delete(s.pendingDelegateTaskIDs, taskID)
			}
		}
	}
}

func subagentAction(call provider.ToolCall, action string) bool {
	if !strings.EqualFold(strings.TrimSpace(call.Name), "subagent") {
		return false
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return false
	}
	got, _ := args["action"].(string)
	return strings.EqualFold(strings.TrimSpace(got), action)
}

func imageReadCall(call provider.ToolCall) bool {
	if strings.EqualFold(strings.TrimSpace(call.Name), "image_read") {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(call.Name), "image") {
		return false
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return false
	}
	got, _ := args["action"].(string)
	return strings.EqualFold(strings.TrimSpace(got), "read")
}

func isTerminalDelegateTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func streamIterationContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

/*
toolCallSig 生成工具调用签名，用于重复检测。
*/
func (s *streamConvergenceState) toolCallSig(name, arguments string) string {
	return toolCallSignature(name, arguments)
}

// trackComputerObservationLoop stops repeated screenshot-only rounds in the
// streaming loop. The first observation is useful; the next one without an
// action is treated as no progress.
// trackComputerObservationLoop stops repeated screenshot-only rounds in the
// streaming loop. limit comes from tools.computer_use.max_consecutive_observe_only.
func (s *streamConvergenceState) trackComputerObservationLoop(toolCalls []provider.ToolCall, limit int) bool {
	if s == nil {
		return false
	}
	if computerObserveOnlyBatch(toolCalls) {
		s.consecutiveComputerObserveOnly++
	} else {
		s.consecutiveComputerObserveOnly = 0
	}
	return s.consecutiveComputerObserveOnly >= computerObserveOnlyLimit(limit)
}

/*
trackToolCallPattern 跟踪工具调用模式，并判断是否进入重复循环。
*/
func (s *streamConvergenceState) trackToolCallPattern(toolCalls []provider.ToolCall, assistantContent string) (bool, []string) {
	if s.toolCallRepeatCount == nil {
		s.toolCallRepeatCount = make(map[string]int)
	}
	if s.repeatToolCallLimit <= 0 {
		s.repeatToolCallLimit = 3
	}
	if s.toolOnlyIterationLimit <= 0 {
		s.toolOnlyIterationLimit = 3
	}
	trimmed := strings.TrimSpace(assistantContent)
	hasWaitForTasks := false
	for _, tc := range toolCalls {
		if strings.EqualFold(tc.Name, "wait_for_tasks") {
			hasWaitForTasks = true
			break
		}
	}
	if trimmed == "" && !hasWaitForTasks {
		s.consecutiveToolOnlyIters++
	} else {
		s.consecutiveToolOnlyIters = 0
	}

	repeatedSigs := make([]string, 0, len(toolCalls))
	allRepeated := true
	failureStopped := false
	for _, tc := range toolCalls {
		sig := s.toolCallSig(tc.Name, tc.Arguments)
		repeatedSigs = append(repeatedSigs, sig)
		if subagentAction(tc, "wait") {
			// A timed-out wait may need another wait call. Let the outer
			// iteration budget, rather than duplicate-call protection, bound it.
			allRepeated = false
			continue
		}
		if s.failureRepeated(tc.Name, tc.Arguments) {
			failureStopped = true
			continue
		}
		s.toolCallRepeatCount[sig]++
		if key := normalizedToolTarget(tc.Name, tc.Arguments); key != "" {
			if s.toolURLRepeatCount == nil {
				s.toolURLRepeatCount = make(map[string]int)
			}
			s.toolURLRepeatCount[key]++
		}
		if s.toolCallRepeatCount[sig] >= s.repeatToolCallLimit {
			continue
		}
		allRepeated = false
	}
	if failureStopped && (trimmed == "" || s.consecutiveToolOnlyIters >= s.toolOnlyIterationLimit) {
		return true, repeatedSigs
	}

	if allRepeated && (trimmed == "" || s.consecutiveToolOnlyIters >= s.toolOnlyIterationLimit) {
		return true, repeatedSigs
	}
	return false, nil
}

/*
rememberToolCallResult 记录一次工具调用的结果，供循环保护、摘要和最终引用使用。
*/
func (s *streamConvergenceState) rememberToolCallResult(name, arguments, result string, duration time.Duration) {
	if s.toolCallRepeatCount == nil {
		s.toolCallRepeatCount = make(map[string]int)
	}
	if s.toolCallLastResult == nil {
		s.toolCallLastResult = make(map[string]string)
	}
	sig := s.toolCallSig(name, arguments)
	if previous, seen := s.toolCallLastResult[sig]; !seen || previous != result {
		s.toolCallRepeatCount[sig] = 0
	}
	s.toolCallLastResult[sig] = result
	if key := normalizedToolTarget(name, arguments); key != "" {
		if s.toolURLLastResult == nil {
			s.toolURLLastResult = make(map[string]string)
		}
		s.toolURLLastResult[key] = result
	}
	s.noteFailureRepeat(name, result)
	s.citationToolCalls = append(s.citationToolCalls, toolCallLog{
		Name:      name,
		Arguments: arguments,
		Result:    result,
		Duration:  duration,
	})
	if s.artifactGuard != nil {
		s.artifactGuard.RecordToolResult(name, arguments, result)
	}
	s.recordDelegateTaskToolResult(name, arguments, result)
}

func (a *Agent) continueAfterPendingDelegateTasks(
	ctx context.Context,
	events chan<- ChatEvent,
	messages []provider.Message,
	callOpts provider.CallOptions,
	sess *session.Session,
	turnInput UserTurnInput,
	round int,
	remaining int,
	state *streamConvergenceState,
	assistantContent string,
	reasoningContent string,
) bool {
	if state == nil {
		return false
	}
	message, blocked := state.pendingDelegateWaitMessage()
	if !blocked {
		return false
	}
	if remaining <= 1 {
		a.finalizeStreamWithState(events, sess, turnInput, message, state)
		return true
	}
	messages = append(messages, provider.Message{
		Role:             "assistant",
		Content:          assistantContent,
		ReasoningContent: reasoningContent,
	})
	messages = append(messages, provider.Message{Role: "user", Content: message})
	messages = a.fitContextWindow(messages)
	nextRound := round + 1
	events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
	a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
	return true
}

/*
repeatedToolLoopMessage 构造“重复工具调用已中止”的用户可见提示文本。
*/
func (s *streamConvergenceState) repeatedToolLoopMessage(repeatedSigs []string) string {
	var b strings.Builder
	b.WriteString("Detected repeated tool-call loop and stopped early to avoid timeout.\n")
	b.WriteString("Latest tool outputs:\n")
	seen := make(map[string]struct{}, len(repeatedSigs))
	for _, sig := range repeatedSigs {
		if _, ok := seen[sig]; ok {
			continue
		}
		seen[sig] = struct{}{}
		parts := strings.SplitN(sig, "|", 2)
		name := parts[0]
		out := "(no cached output)"
		if s.toolCallLastResult != nil {
			if v := strings.TrimSpace(s.toolCallLastResult[sig]); v != "" {
				out = v
			}
		}
		if len(out) > 240 {
			out = out[:240] + "...(truncated)"
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n", name, out))
	}
	return strings.TrimSpace(b.String())
}

func (a *Agent) ChatWithSessionStream(ctx context.Context, sessionID string, userInput string) (<-chan ChatEvent, error) {
	return a.ChatWithSessionStreamInput(ctx, sessionID, TextUserTurnInput(userInput))
}

// ChatWithSessionStreamWithLoopConfig streams chat events using an explicit loop configuration.
func (a *Agent) ChatWithSessionStreamWithLoopConfig(ctx context.Context, sessionID string, userInput string, loopCfg LoopConfig) (<-chan ChatEvent, error) {
	return a.ChatWithSessionStreamInputWithLoopConfig(ctx, sessionID, TextUserTurnInput(userInput), loopCfg)
}

func (a *Agent) ChatWithSessionStreamInput(ctx context.Context, sessionID string, input UserTurnInput) (<-chan ChatEvent, error) {
	loopCfg := DefaultLoopConfig()
	cfg := a.cfg.Get()
	ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
	loopCfg.Source = input.Scope.Platform
	if strings.TrimSpace(loopCfg.Source) == "" {
		loopCfg.Source = "cli"
	}
	return a.ChatWithSessionStreamInputWithLoopConfig(ctx, sessionID, input, loopCfg)
}

// ChatWithSessionStreamTurn is the stream counterpart to ChatWithSessionTurn.
func (a *Agent) ChatWithSessionStreamTurn(ctx context.Context, sessionID string, input turn.Input) (<-chan ChatEvent, error) {
	return a.ChatWithSessionStreamInput(ctx, sessionID, UserTurnInputFromTurn(input))
}

// ChatWithSessionStreamTurnEvents is the neutral-event counterpart to
// ChatWithSessionStreamTurn. The legacy method remains available for in-process
// callers while gateway adapters can consume turn.ChatEvent directly.
func (a *Agent) ChatWithSessionStreamTurnEvents(ctx context.Context, sessionID string, input turn.Input) (<-chan turn.ChatEvent, error) {
	events, err := a.ChatWithSessionStreamTurn(ctx, sessionID, input)
	if err != nil {
		return nil, err
	}
	return ToTurnChatEventStream(ctx, events), nil
}

func (a *Agent) ChatWithSessionStreamInputWithLoopConfig(ctx context.Context, sessionID string, input UserTurnInput, loopCfg LoopConfig) (<-chan ChatEvent, error) {
	sess, ok := a.sessions.Get(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	return a.ChatWithSessionStreamForSession(ctx, sess, input, loopCfg)
}

// ChatWithSessionStreamForSession retains the caller's original session handle
// across scheduling. Reusing its ID after deletion cannot redirect this turn to
// a replacement conversation.
func (a *Agent) ChatWithSessionStreamForSession(ctx context.Context, sess *session.Session, input UserTurnInput, loopCfg LoopConfig) (<-chan ChatEvent, error) {
	if sess == nil {
		return nil, fmt.Errorf("session is nil")
	}
	sessionID := sess.ID
	ctx, finish, err := a.beginSessionCall(ctx, sess)
	if err != nil {
		return nil, err
	}
	input = a.expandPluginCommand(input)
	routingText := input.RoutingText
	turnProvider := a.providerSnapshotForTurn(routingText)
	if !turnProvider.valid() {
		finish()
		return nil, fmt.Errorf("provider not initialized")
	}
	snapshot, snapshotErr := a.sandboxSnapshot(sess)
	if snapshotErr != nil {
		finish()
		return nil, fmt.Errorf("prepare %s sandbox: %w", a.SandboxMode(), snapshotErr)
	}
	loopCfg.Sandbox = snapshot
	loopCfg.SessionID = strings.TrimSpace(sess.ID)
	ctx = sandbox.WithSnapshot(ctx, snapshot)

	events := make(chan ChatEvent, 64)

	go func() {
		defer close(events)
		defer finish()
		defer snapshot.Close()

		sanitizeLoopConfig(&loopCfg)
		if strings.TrimSpace(loopCfg.RunID) == "" {
			loopCfg.RunID = fmt.Sprintf("run-%d", time.Now().UnixNano())
		}
		a.applySandboxToolPolicy(&loopCfg, snapshot)
		// Stream path always exposes emit so auto-compact progress reaches clients.
		loopCfg.emit = func(eventCtx context.Context, event ChatEvent) { sendForegroundEvent(eventCtx, events, event) }
		loopCfg.eventContext = ctx
		if a.useForeground(sess, loopCfg) {
			result, err := a.runForeground(ctx, sess, input, loopCfg, turnProvider)
			if err != nil {
				sendForegroundEvent(ctx, events, ChatEvent{Type: ChatEventError, Err: err})
			} else {
				a.finishForegroundMemory(sess, input, result)
				sendForegroundEvent(ctx, events, ChatEvent{Type: ChatEventDone, Content: result.Response, Usage: result.Usage, CreatedAt: result.CreatedAt})
			}
			return
		}
		a.applyIntentToolGating(&loopCfg, routingText)
		a.applyVisionToolPolicy(&loopCfg, turnProvider)
		ctx = withHITLEmitter(ctx, func(event ChatEvent) {
			sendForegroundEvent(ctx, events, event)
		})
		ctx = withPluginCallPolicy(ctx, loopCfg, input.Scope, routingText)
		logger.Info("agent stream loop started",
			"session_id", sessionID,
			"provider", turnProvider.name(),
			"model", turnProvider.model,
			"stream_mode", a.getStreamMode(),
			"max_iterations", loopCfg.MaxIterations,
			"timeout_ms", loopCfg.Timeout.Milliseconds(),
			"auto_approve", loopCfg.AutoApprove,
			"input_len", len(routingText),
		)
		logger.Debug("agent stream loop input",
			"session_id", sessionID,
			"input", routingText,
			"scope_platform", input.Scope.Platform,
			"scope_chat_id", input.Scope.ChatID,
			"scope_chat_type", input.Scope.ChatType,
			"scope_sender_id", input.Scope.SenderID,
			"attachments", len(input.Attachments),
			"disabled_tools", strings.Join(loopCfg.DisabledTools, ","),
		)

		buildOpts := defaultContextBuildOptions()
		buildOpts.DisabledTools = append([]string(nil), loopCfg.DisabledTools...)
		a.maybeAutoCompactSessionWithProvider(ctx, sess, routingText, loopCfg.Ephemeral, turnProvider, compactProgressEmitter(loopCfg))
		messages := a.buildContextMessagesForInputWithProvider(ctx, sess, input, buildOpts, turnProvider)
		if err := sess.AddProviderMessageWithError(input.Message); err != nil {
			sendForegroundEvent(ctx, events, ChatEvent{Type: ChatEventError, Err: sessionError(err)})
			return
		}
		callOpts := a.buildLoopCallOptions(routingText, loopCfg)

		state := &streamConvergenceState{
			ephemeral:              loopCfg.Ephemeral,
			provider:               turnProvider,
			autoApprove:            loopCfg.AutoApprove,
			source:                 loopCfg.Source,
			repeatToolCallLimit:    loopCfg.RepeatToolCallLimit,
			toolOnlyIterationLimit: loopCfg.ToolOnlyIterationLimit,
			duplicateFetchLimit:    loopCfg.DuplicateFetchLimit,
			disabledTools:          append([]string(nil), loopCfg.DisabledTools...),
			memoryGate:             a.buildMemoryToolGate(routingText, input.Scope, loopCfg.DisabledTools),
			toolExecutionGuard:     configuredTurnToolGuard(routingText, loopCfg),
			iterationTimeout:       loopCfg.Timeout,
			artifactGuard:          artifact.NewGuard(routingText),
			runID:                  loopCfg.RunID,
		}
		logger.Debug("agent stream context prepared",
			"session_id", sessionID,
			"messages", len(messages),
			"tools", len(callOpts.Tools),
			"tool_choice", fmt.Sprint(callOpts.ToolChoice),
		)

		// 🧠 思考阶段（第一轮）
		events <- ChatEvent{Type: ChatEventThinking, Content: "Thinking... (round 1)"}

		mode := a.getStreamMode()
		if mode == StreamModeNative {
			// === 真流式路径 ===
			a.streamNative(ctx, events, messages, callOpts, sess, input, 1, loopCfg.MaxIterations, state)
			return
		}

		// === 模拟流式路径 ===
		a.streamSimulated(ctx, events, messages, callOpts, sess, input, 1, loopCfg.MaxIterations, state)
	}()

	return events, nil
}

// streamNative 真流式：直接使用 provider 的 ChatStream，逐 chunk 推送
// tool_calls 通过流式增量拼接处理
/*
streamNative 使用 provider 原生流式接口执行一轮或多轮对话。
*/
func (a *Agent) streamNative(ctx context.Context, events chan<- ChatEvent, messages []provider.Message, callOpts provider.CallOptions, sess *session.Session, turnInput UserTurnInput, round int, remaining int, state *streamConvergenceState) {
	timeout := time.Duration(0)
	if state != nil {
		timeout = state.iterationTimeout
	}
	a.streamNativeAttempt(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state, timeout, true)
}

func (a *Agent) streamNativeAttempt(ctx context.Context, events chan<- ChatEvent, messages []provider.Message, callOpts provider.CallOptions, sess *session.Session, turnInput UserTurnInput, round int, remaining int, state *streamConvergenceState, iterationTimeout time.Duration, allowSoftRetry bool) {
	if state == nil {
		state = &streamConvergenceState{}
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	if remaining <= 0 {
		if message, blocked := state.pendingDelegateWaitMessage(); blocked {
			a.finalizeStreamWithState(events, sess, turnInput, message, state)
			return
		}
		if state.memoryGate != nil && state.memoryGate.shouldBlockFinal() {
			a.finalizeStreamWithState(events, sess, turnInput, state.memoryGate.incompleteMessage(), state)
			return
		}
		events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
		return
	}

	effectiveCallOpts := prepareLoopCallOptions(messages, callOpts, state.forceSearchSynthesis)
	logger.Debug("agent stream native iteration started",
		"session_id", sessionID,
		"round", round,
		"remaining", remaining,
		"messages", len(messages),
		"tools", len(effectiveCallOpts.Tools),
		"tool_choice", fmt.Sprint(effectiveCallOpts.ToolChoice),
		"force_search_synthesis", state.forceSearchSynthesis,
	)
	iterCtx, cancel := streamIterationContext(ctx, iterationTimeout)
	defer cancel()
	retryOnce := func(err error, safeToRetry bool) bool {
		if !allowSoftRetry || !safeToRetry {
			return false
		}
		retryTimeout, retry := agentIterationSoftRetryTimeout(ctx, err, iterationTimeout)
		if !retry {
			return false
		}
		cancel()
		sendForegroundEvent(ctx, events, ChatEvent{Type: ChatEventThinking, Content: "本轮模型请求超时，正在短时重试一次。", Round: round})
		a.streamNativeAttempt(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state, retryTimeout, false)
		return true
	}
	ch, err := a.streamLoopIteration(iterCtx, messages, callOpts, state.forceSearchSynthesis, state.provider)
	if err != nil {
		logger.Warn("agent stream native iteration failed",
			"session_id", sessionID,
			"round", round,
			"error", err,
		)
		if retryOnce(err, true) {
			return
		}
		if a.finalizeStreamAfterInterruption(ctx, events, messages, callOpts, sess, turnInput, state, err, "") {
			return
		}
		events <- ChatEvent{Type: ChatEventError, Err: err}
		return
	}

	var content strings.Builder
	var reasoning strings.Builder
	emittedContentBytes := 0
	streamFinishReason := ""
	streamTerminal := false
	var iterationUsage *provider.UsageDetails
	recordIterationUsage := func() {
		if iterationUsage != nil {
			state.addUsage(iterationUsage, state.provider.model)
			iterationUsage = nil
		}
	}
	// 流式 tool_calls 增量拼接
	var toolCallsAcc []streamToolCallAcc // 按 index 累积

	for chunk := range ch {
		if chunk.Usage != nil {
			iterationUsage = provider.MergeUsageDetails(iterationUsage, chunk.Usage)
		}
		if chunk.Err != nil {
			recordIterationUsage()
			streamErr := fmt.Errorf("provider stream interrupted: %w", chunk.Err)
			logger.Warn("agent stream native provider stream interrupted",
				"session_id", sessionID,
				"round", round,
				"error", streamErr,
			)
			if retryOnce(streamErr, emittedContentBytes == 0) {
				return
			}
			if a.finalizeStreamAfterInterruption(ctx, events, messages, callOpts, sess, turnInput, state, streamErr, content.String()) {
				return
			}
			events <- ChatEvent{Type: ChatEventError, Err: streamErr}
			return
		}
		if chunk.FinishReason != "" {
			streamFinishReason = chunk.FinishReason
		}
		if chunk.Content != "" {
			content.WriteString(chunk.Content)
			if state.memoryGate != nil && state.memoryGate.shouldBlockFinal() {
				continue
			}
			if state.hasPendingDelegateTasks() {
				continue
			}
			full := content.String()
			pending := full[emittedContentBytes:]
			if textcall.ShouldHoldStream(full) || textcall.ShouldHoldStream(pending) {
				continue
			}
			if len(full) > emittedContentBytes {
				events <- ChatEvent{Type: ChatEventContent, Content: pending}
				emittedContentBytes = len(full)
			}
		}
		if chunk.ReasoningContent != "" {
			reasoning.WriteString(chunk.ReasoningContent)
		}
		// 处理流式 tool_calls 增量
		if len(chunk.ToolCallDeltas) > 0 {
			for _, dtc := range chunk.ToolCallDeltas {
				// 确保 slice 足够长
				for len(toolCallsAcc) <= dtc.Index {
					toolCallsAcc = append(toolCallsAcc, streamToolCallAcc{})
				}
				acc := &toolCallsAcc[dtc.Index]
				if dtc.ID != "" {
					acc.id = dtc.ID
				}
				if dtc.Name != "" {
					acc.name = dtc.Name
				}
				if dtc.Arguments != "" {
					acc.arguments += dtc.Arguments
				}
			}
		}
		if chunk.Done {
			streamTerminal = true
			break
		}
	}
	if !streamTerminal {
		recordIterationUsage()
		streamErr := iterCtx.Err()
		if streamErr == nil {
			streamErr = fmt.Errorf("provider stream interrupted: closed without a terminal event")
		}
		logger.Warn("agent stream native provider stream interrupted",
			"session_id", sessionID,
			"round", round,
			"error", streamErr,
		)
		if retryOnce(streamErr, emittedContentBytes == 0) {
			return
		}
		if a.finalizeStreamAfterInterruption(ctx, events, messages, callOpts, sess, turnInput, state, streamErr, content.String()) {
			return
		}
		events <- ChatEvent{Type: ChatEventError, Err: streamErr}
		return
	}
	if err := iterCtx.Err(); err != nil {
		recordIterationUsage()
		if a.finalizeStreamAfterInterruption(ctx, events, messages, callOpts, sess, turnInput, state, err, content.String()) {
			return
		}
	}
	recordIterationUsage()

	response := content.String()
	assistantContent, textToolCalls := textcall.Extract(response)
	hadTextToolCalls := len(textToolCalls) > 0
	textToolCalls = textcall.FilterDisabled(textToolCalls, state.disabledTools)
	logger.Debug("agent stream native provider response",
		"session_id", sessionID,
		"round", round,
		"finish_reason", streamFinishReason,
		"content_bytes", len(response),
		"reasoning_bytes", reasoning.Len(),
		"stream_tool_call_deltas", len(toolCallsAcc),
		"text_tool_calls", len(textToolCalls),
	)
	if reasoning.Len() > 0 {
		events <- ChatEvent{Type: ChatEventReasoningContent, Content: reasoning.String(), Round: round}
	}

	// 如果有累积的 tool_calls，处理它们
	if len(toolCallsAcc) > 0 || len(textToolCalls) > 0 {
		state.emptyResponseRetries = 0
		toolCalls := make([]provider.ToolCall, 0, len(toolCallsAcc)+len(textToolCalls))
		for _, acc := range toolCallsAcc {
			if acc.name != "" {
				// v0.55.1: 如果 ID 为空，生成唯一 call_id
				id := acc.id
				if id == "" {
					id = provider.GenerateCallID()
				}
				toolCalls = append(toolCalls, provider.ToolCall{
					ID:        id,
					Name:      acc.name,
					Arguments: acc.arguments,
				})
			}
		}
		toolCalls = append(toolCalls, textToolCalls...)

		if len(toolCalls) > 0 {
			if state.trackComputerObservationLoop(toolCalls, a.cfg.Get().Tools.ComputerUse.MaxConsecutiveObserveOnly) {
				a.finalizeStreamWithState(events, sess, turnInput, computerObservationLoopMessage, state)
				return
			}
			logger.Debug("agent stream native tool calls assembled",
				"session_id", sessionID,
				"round", round,
				"tool_calls", formatToolCallsForLog(toolCalls),
				"tool_names", strings.Join(toolCallNamesForLog(toolCalls), ","),
			)
			if shouldStop, repeatedSigs := state.trackToolCallPattern(toolCalls, assistantContent); shouldStop {
				if a.continueAfterStreamMemoryGate(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state) {
					return
				}
				a.finalizeStreamWithState(events, sess, turnInput, state.repeatedToolLoopMessage(repeatedSigs), state)
				return
			}

			// 将 assistant 消息加入历史
			messages = append(messages, provider.Message{
				Role:             "assistant",
				Content:          assistantContent,
				ReasoningContent: reasoning.String(),
				ToolCalls:        toolCalls,
			})
			if sess != nil {
				if !appendStreamSessionMessage(events, sess, provider.Message{
					Role:             "assistant",
					Content:          assistantContent,
					ReasoningContent: reasoning.String(),
					ToolCalls:        toolCalls,
				}) {
					return
				}
			}

			emitChatToolCallEvents(events, toolCalls)
			executed := a.executeToolCallsOrderedGuardedContext(
				ctx,
				toolCalls,
				state.autoApprove,
				sess,
				state.toolURLRepeatCount,
				state.toolURLLastResult,
				state.duplicateFetchLimit,
				true,
				state.toolExecutionGuard,
				state.source,
				turnInput.RoutingText,
			)

			for _, execResult := range executed {
				if state.memoryGate != nil {
					state.memoryGate.markExecuted(execResult.ToolCall.Name, execResult.Result)
				}
				emitChatToolResultEvent(events, execResult.ToolCall.Name, execResult.Result, execResult.Metadata)
				emitChatObservationEvents(events, execResult)
				messages = append(messages, observedToolMessage(state.runID, execResult, buildContextToolResult(execResult.ToolCall.Name, execResult.Result, &state.successfulSearchEvidence, &state.detailedSearchEvidence)))
				if sess != nil {
					if !appendStreamSessionMessage(events, sess, observedToolMessage(state.runID, execResult, execResult.Result)) {
						return
					}
				}
				state.rememberToolCallResult(execResult.ToolCall.Name, execResult.ToolCall.Arguments, execResult.Result, execResult.Duration)
			}
			if sess != nil {
				if err := sess.Save(); err != nil {
					events <- ChatEvent{Type: ChatEventError, Err: sessionError(err)}
					return
				}
			}

			// 裁剪上下文，继续下一轮；保留最新 computer frame transiently。
			messages = appendLatestComputerObservation(messages, executed)
			messages = appendComputerActDirective(messages, toolCalls)
			messages = a.fitContextWindow(messages)
			messages = maybeAppendSearchSynthesisMessage(messages, &state.forceSearchSynthesis, state.successfulSearchEvidence, state.consecutiveToolOnlyIters)
			if a.continueAfterStreamMemoryGate(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state) {
				return
			}
			if remaining <= 1 {
				if message, blocked := state.pendingDelegateWaitMessage(); blocked {
					a.finalizeStreamWithState(events, sess, turnInput, message, state)
					return
				}
				events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
				return
			}
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}

			// 递归进入下一轮（用非流式，因为 tool_calls 后通常需要完整响应）
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
			return
		}
	}

	// 没有工具调用，纯文本回复（已在流式中逐 chunk 推送了）
	if hadTextToolCalls {
		response = assistantContent
		emittedContentBytes = 0
	}
	if a.continueAfterStreamMemoryGate(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state) {
		return
	}
	if a.continueAfterPendingDelegateTasks(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state, response, reasoning.String()) {
		return
	}
	if len(response) > emittedContentBytes {
		events <- ChatEvent{Type: ChatEventContent, Content: response[emittedContentBytes:]}
	}
	clean := strings.TrimSpace(response)

	// 空回复恢复
	if clean == "" {
		if state.emptyResponseRetries < maxEmptyResponseRetries && remaining > 1 {
			state.emptyResponseRetries++
			messages = append(messages, provider.Message{Role: "assistant", Content: response, ReasoningContent: reasoning.String()})
			messages = append(messages, provider.Message{Role: "user", Content: emptyResponseRecoveryPrompt})
			messages = a.fitContextWindow(messages)
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
			return
		}
		a.finalizeStreamWithState(events, sess, turnInput, emptyFinalResponseMessage, state)
		return
	}
	state.emptyResponseRetries = 0

	finalResponse := response
	finalReasoning := reasoning.String()
	if msg, blocked := state.artifactGuard.BlockMessage(finalResponse); blocked {
		if remaining > 1 {
			messages = append(messages, provider.Message{Role: "assistant", Content: response, ReasoningContent: reasoning.String()})
			messages = append(messages, provider.Message{Role: "user", Content: msg})
			messages = a.fitContextWindow(messages)
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
			return
		}
		finalResponse = msg
		finalReasoning = ""
	}
	a.finalizeStreamWithState(events, sess, turnInput, finalResponse, state, finalReasoning)
}

// streamSimulated 模拟流式：先非流式获取完整响应，再按句子边界逐段推送
/*
streamSimulated 先获取完整响应，再按块模拟流式输出。
*/
func (a *Agent) streamSimulated(ctx context.Context, events chan<- ChatEvent, messages []provider.Message, callOpts provider.CallOptions, sess *session.Session, turnInput UserTurnInput, round int, remaining int, state *streamConvergenceState) {
	if state == nil {
		state = &streamConvergenceState{}
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	callOpts = a.appendEphemeralToolSchemas(callOpts, sessionID)
	policy := LoopConfig{DisabledTools: state.disabledTools}
	if guard := state.toolExecutionGuard; guard != nil {
		policy.RestrictTools = guard.restrictTools
		for name := range guard.allowedTools {
			policy.AllowedTools = append(policy.AllowedTools, name)
		}
	}
	callOpts = applyToolAllowlist(callOpts, policy)
	if remaining <= 0 {
		if message, blocked := state.pendingDelegateWaitMessage(); blocked {
			a.finalizeStreamWithState(events, sess, turnInput, message, state)
			return
		}
		if state.memoryGate != nil && state.memoryGate.shouldBlockFinal() {
			a.finalizeStreamWithState(events, sess, turnInput, state.memoryGate.incompleteMessage(), state)
			return
		}
		events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
		return
	}

	effectiveCallOpts := prepareLoopCallOptions(messages, callOpts, state.forceSearchSynthesis)
	logger.Debug("agent stream simulated iteration started",
		"session_id", sessionID,
		"round", round,
		"remaining", remaining,
		"messages", len(messages),
		"tools", len(effectiveCallOpts.Tools),
		"tool_choice", fmt.Sprint(effectiveCallOpts.ToolChoice),
		"force_search_synthesis", state.forceSearchSynthesis,
	)
	resp, err, _ := a.chatLoopIterationWithSoftRetry(
		ctx,
		state.iterationTimeout,
		messages,
		callOpts,
		state.forceSearchSynthesis,
		state.provider,
		func() {
			sendForegroundEvent(ctx, events, ChatEvent{Type: ChatEventThinking, Content: "本轮模型请求超时，正在短时重试一次。", Round: round})
		},
	)
	if err != nil {
		logger.Warn("agent stream simulated iteration failed",
			"session_id", sessionID,
			"round", round,
			"error", err,
		)
		if a.finalizeStreamAfterInterruption(ctx, events, messages, callOpts, sess, turnInput, state, err, "") {
			return
		}
		events <- ChatEvent{Type: ChatEventError, Err: err}
		return
	}
	state.addResponseUsage(resp)
	textcall.ApplyToResponse(resp, state.disabledTools)
	logger.Debug("agent stream simulated provider response",
		"session_id", sessionID,
		"round", round,
		"model", resp.Model,
		"finish_reason", resp.FinishReason,
		"tokens_used", resp.TokensUsed,
		"content_bytes", len(resp.Content),
		"reasoning_bytes", len(resp.ReasoningContent),
		"tool_calls", len(resp.ToolCalls),
		"tool_names", strings.Join(toolCallNamesForLog(resp.ToolCalls), ","),
	)
	if len(resp.ReasoningContent) > 0 {
		events <- ChatEvent{Type: ChatEventReasoningContent, Content: resp.ReasoningContent, Round: round}
	}

	// 有工具调用 → 展示过程 → 执行 → 继续循环
	if len(resp.ToolCalls) > 0 {
		state.emptyResponseRetries = 0
		if state.trackComputerObservationLoop(resp.ToolCalls, a.cfg.Get().Tools.ComputerUse.MaxConsecutiveObserveOnly) {
			a.finalizeStreamWithState(events, sess, turnInput, computerObservationLoopMessage, state)
			return
		}
		if shouldStop, repeatedSigs := state.trackToolCallPattern(resp.ToolCalls, resp.Content); shouldStop {
			if a.continueAfterStreamMemoryGate(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state) {
				return
			}
			a.finalizeStreamWithState(events, sess, turnInput, state.repeatedToolLoopMessage(repeatedSigs), state)
			return
		}
		messages = append(messages, provider.Message{
			Role:             "assistant",
			Content:          resp.Content,
			ReasoningContent: resp.ReasoningContent,
			ToolCalls:        resp.ToolCalls,
		})
		if sess != nil {
			if !appendStreamSessionMessage(events, sess, provider.Message{
				Role:             "assistant",
				Content:          resp.Content,
				ReasoningContent: resp.ReasoningContent,
				ToolCalls:        resp.ToolCalls,
			}) {
				return
			}
		}

		emitChatToolCallEvents(events, resp.ToolCalls)
		executed := a.executeToolCallsOrderedGuardedContext(
			ctx,
			resp.ToolCalls,
			state.autoApprove,
			sess,
			state.toolURLRepeatCount,
			state.toolURLLastResult,
			state.duplicateFetchLimit,
			true,
			state.toolExecutionGuard,
			state.source,
			turnInput.RoutingText,
		)

		for _, execResult := range executed {
			if state.memoryGate != nil {
				state.memoryGate.markExecuted(execResult.ToolCall.Name, execResult.Result)
			}
			emitChatToolResultEvent(events, execResult.ToolCall.Name, execResult.Result, execResult.Metadata)
			emitChatObservationEvents(events, execResult)
			messages = append(messages, observedToolMessage(state.runID, execResult, buildContextToolResult(execResult.ToolCall.Name, execResult.Result, &state.successfulSearchEvidence, &state.detailedSearchEvidence)))
			if sess != nil {
				if !appendStreamSessionMessage(events, sess, observedToolMessage(state.runID, execResult, execResult.Result)) {
					return
				}
			}
			state.rememberToolCallResult(execResult.ToolCall.Name, execResult.ToolCall.Arguments, execResult.Result, execResult.Duration)
		}
		if sess != nil {
			if err := sess.Save(); err != nil {
				events <- ChatEvent{Type: ChatEventError, Err: sessionError(err)}
				return
			}
		}

		// 裁剪上下文，递归继续；保留最新 computer frame transiently。
		messages = appendLatestComputerObservation(messages, executed)
		messages = appendComputerActDirective(messages, resp.ToolCalls)
		messages = a.fitContextWindow(messages)
		messages = maybeAppendSearchSynthesisMessage(messages, &state.forceSearchSynthesis, state.successfulSearchEvidence, state.consecutiveToolOnlyIters)
		if a.continueAfterStreamMemoryGate(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state) {
			return
		}
		if remaining <= 1 {
			if message, blocked := state.pendingDelegateWaitMessage(); blocked {
				a.finalizeStreamWithState(events, sess, turnInput, message, state)
				return
			}
			events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
			return
		}
		nextRound := round + 1
		events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
		a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
		return
	}

	// 纯文本回复，模拟流式推送
	response := resp.Content
	clean := strings.TrimSpace(response)
	if a.continueAfterStreamMemoryGate(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state) {
		return
	}
	if a.continueAfterPendingDelegateTasks(ctx, events, messages, callOpts, sess, turnInput, round, remaining, state, response, resp.ReasoningContent) {
		return
	}

	// 空回复恢复
	if clean == "" {
		if state.emptyResponseRetries < maxEmptyResponseRetries && remaining > 1 {
			state.emptyResponseRetries++
			messages = append(messages, provider.Message{Role: "assistant", Content: response, ReasoningContent: resp.ReasoningContent})
			messages = append(messages, provider.Message{Role: "user", Content: emptyResponseRecoveryPrompt})
			messages = a.fitContextWindow(messages)
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
			return
		}
		a.finalizeStreamWithState(events, sess, turnInput, emptyFinalResponseMessage, state)
		return
	}
	state.emptyResponseRetries = 0

	finalResponse := response
	finalReasoning := resp.ReasoningContent
	if msg, blocked := state.artifactGuard.BlockMessage(finalResponse); blocked {
		if remaining > 1 {
			messages = append(messages, provider.Message{Role: "assistant", Content: response, ReasoningContent: resp.ReasoningContent})
			messages = append(messages, provider.Message{Role: "user", Content: msg})
			messages = a.fitContextWindow(messages)
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
			return
		}
		finalResponse = msg
		finalReasoning = ""
	}
	chunks := splitIntoChunks(finalResponse, 60)
	for _, chunk := range chunks {
		events <- ChatEvent{Type: ChatEventContent, Content: chunk}
		time.Sleep(50 * time.Millisecond)
	}
	a.finalizeStreamWithState(events, sess, turnInput, finalResponse, state, finalReasoning)
}

// finalizeStream 流式对话收尾：保存会话、记忆、RAG 索引
func (a *Agent) finalizeStream(events chan<- ChatEvent, sess *session.Session, turnInput UserTurnInput, response string, citationLogs ...[]toolCallLog) {
	a.finalizeStreamWithUsage(events, sess, turnInput, response, "", nil, citationLogs...)
}

func (a *Agent) finalizeStreamWithReasoning(events chan<- ChatEvent, sess *session.Session, turnInput UserTurnInput, response string, reasoningContent string, citationLogs ...[]toolCallLog) {
	a.finalizeStreamWithUsage(events, sess, turnInput, response, reasoningContent, nil, citationLogs...)
}

func (a *Agent) finalizeStreamWithUsage(events chan<- ChatEvent, sess *session.Session, turnInput UserTurnInput, response string, reasoningContent string, usage *provider.TokenUsage, citationLogs ...[]toolCallLog) {
	a.finalizeStreamWithRecap(events, sess, turnInput, response, reasoningContent, usage, true, citationLogs...)
}

func (a *Agent) finalizeStreamWithRecap(events chan<- ChatEvent, sess *session.Session, turnInput UserTurnInput, response string, reasoningContent string, usage *provider.TokenUsage, generateRecap bool, citationLogs ...[]toolCallLog) {
	turnInput = turnInput.Normalize()
	routingText := turnInput.RoutingText
	if strings.TrimSpace(response) == "" {
		response = emptyFinalResponseMessage
	}
	response = utils.SanitizeToolProtocolOutput(response)
	var logs []toolCallLog
	if len(citationLogs) > 0 {
		logs = citationLogs[0]
	}
	response = a.appendNaturalCitationsIfEnabled(response, logs)
	response = a.appendRunningTaskNotice(response)
	createdAt := time.Now().UTC()
	if sess != nil {
		msg := provider.Message{Role: "assistant", Content: response, ReasoningContent: reasoningContent}
		msg.CreatedAt = &createdAt
		if usage != nil && usage.TotalTokens > 0 {
			usageCopy := *usage
			msg.Usage = &usageCopy
		}
		if err := sess.AddProviderMessageWithError(msg); err != nil {
			events <- ChatEvent{Type: ChatEventError, Err: sessionError(err)}
			return
		}
		if err := sess.Save(); err != nil {
			events <- ChatEvent{Type: ChatEventError, Err: sessionError(err)}
			return
		}
	}

	maintenance := a.recordMemoryTurn(sess)
	if generateRecap {
		a.queueSessionRecap(sess)
	}
	a.saveConversationMemoryFromSession(sess, turnInput, response)
	a.applyMemoryMaintenance(maintenance)

	if a.ragManager != nil && autoIndexFinalAnswersEnabled() {
		a.indexConversationTurn(routingText, response)
	}

	a.metrics.RecordChatRequest()
	var usageCopy *provider.TokenUsage
	if usage != nil && usage.TotalTokens > 0 {
		copy := *usage
		usageCopy = &copy
	}
	events <- ChatEvent{Type: ChatEventDone, Content: response, Usage: usageCopy, CreatedAt: &createdAt}
}

func (a *Agent) finalizeStreamWithState(events chan<- ChatEvent, sess *session.Session, turnInput UserTurnInput, response string, state *streamConvergenceState, reasoning ...string) {
	if state == nil {
		a.finalizeStream(events, sess, turnInput, response)
		return
	}
	reasoningContent := ""
	if len(reasoning) > 0 {
		reasoningContent = reasoning[0]
	}
	a.finalizeStreamWithRecap(events, sess, turnInput, response, reasoningContent, state.usage, !state.ephemeral, state.citationToolCalls)
}

// finalizeStreamAfterInterruption gives the model one final, tool-free chance
// to synthesize a useful answer from the completed tool results and any partial
// output collected before a reasoning/provider failure. User cancellation is
// terminal and never triggers this recovery request.
func (a *Agent) finalizeStreamAfterInterruption(
	ctx context.Context,
	events chan<- ChatEvent,
	messages []provider.Message,
	callOpts provider.CallOptions,
	sess *session.Session,
	turnInput UserTurnInput,
	state *streamConvergenceState,
	err error,
	partial string,
) bool {
	if errors.Is(context.Cause(ctx), errSessionCallCancelled) {
		events <- ChatEvent{Type: ChatEventError, Err: context.Canceled}
		return true
	}
	if state == nil {
		return a.finalizeStreamInterruption(events, sess, turnInput, nil, err, partial)
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return a.finalizeStreamInterruption(events, sess, turnInput, state, ctx.Err(), partial)
	}

	synthesisMessages := append([]provider.Message(nil), messages...)
	partial = strings.TrimSpace(partial)
	if partial != "" {
		synthesisMessages = append(synthesisMessages, provider.Message{
			Role:    "assistant",
			Content: utils.TrimToRunes(partial, 6000),
		})
	}

	var prompt strings.Builder
	prompt.WriteString("The previous reasoning or provider phase was interrupted before a clean final response.\n")
	prompt.WriteString("Generate the best user-facing final answer now using only the recorded conversation, completed tool results, and partial output above.\n")
	prompt.WriteString("State clearly what is verified, what failed or remains uncertain, and what the user should do next. Do not claim an action succeeded unless the recorded evidence supports it. Do not call tools.\n")
	prompt.WriteString("Interruption reason: ")
	prompt.WriteString(strings.TrimSpace(fmt.Sprint(err)))
	if observations := interruptionToolObservations(state); len(observations) > 0 {
		prompt.WriteString("\nRecorded tool observations:\n")
		for _, observation := range observations {
			prompt.WriteString("- ")
			prompt.WriteString(observation)
			prompt.WriteByte('\n')
		}
	}
	if state != nil && state.hasPendingDelegateTasks() {
		prompt.WriteString("\nDelegated work is still pending; do not present it as completed.")
	}
	synthesisMessages = append(synthesisMessages, provider.Message{Role: "user", Content: prompt.String()})

	synthesisCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, synthErr := a.chatLoopIteration(synthesisCtx, synthesisMessages, callOpts, true, state.provider)
	if synthErr == nil && resp != nil && len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Content) != "" {
		state.addResponseUsage(resp)
		a.finalizeStreamWithState(events, sess, turnInput, resp.Content, state, resp.ReasoningContent)
		return true
	}

	if a.finalizeStreamInterruption(events, sess, turnInput, state, err, partial) {
		return true
	}
	return false
}

// finalizeStreamInterruption turns expected cancellation and timeout failures
// into a terminal user-facing response without starting another model request.
func (a *Agent) finalizeStreamInterruption(events chan<- ChatEvent, sess *session.Session, turnInput UserTurnInput, state *streamConvergenceState, err error, partial string) bool {
	if !isStreamInterruption(err) {
		return false
	}
	a.finalizeStreamWithState(events, sess, turnInput, streamInterruptionMessage(err, state, partial), state)
	return true
}

func isStreamInterruption(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "deadline exceeded") || strings.Contains(message, "timed out") || strings.Contains(message, "timeout")
}

func streamInterruptionMessage(err error, state *streamConvergenceState, partial string) string {
	var message strings.Builder
	if errors.Is(err, context.Canceled) {
		message.WriteString("This request was canceled before a complete final answer was generated.")
	} else {
		message.WriteString("This request timed out before a complete final answer was generated.")
	}

	partial = strings.TrimSpace(partial)
	if partial != "" {
		message.WriteString("\n\nPartial model output (may be incomplete):\n")
		message.WriteString(utils.TrimToRunes(partial, 1200))
	}

	if observations := interruptionToolObservations(state); len(observations) > 0 {
		message.WriteString("\n\nRecorded tool observations:\n")
		for _, observation := range observations {
			message.WriteString("- ")
			message.WriteString(observation)
			message.WriteByte('\n')
		}
	}
	if state != nil && state.hasPendingDelegateTasks() {
		message.WriteString("\nDelegated work may still be running; its completion was not confirmed before the interruption.")
	}
	message.WriteString("\n\nNext: retry or ask me to continue from these recorded results.")
	return strings.TrimSpace(message.String())
}

func interruptionToolObservations(state *streamConvergenceState) []string {
	if state == nil || len(state.toolCallLastResult) == 0 {
		return nil
	}
	keys := make([]string, 0, len(state.toolCallLastResult))
	for key := range state.toolCallLastResult {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	observations := make([]string, 0, len(keys))
	for _, key := range keys {
		result := strings.TrimSpace(state.toolCallLastResult[key])
		if result == "" {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(key, "|", 2)[0])
		if name == "" {
			name = "tool"
		}
		observations = append(observations, name+": "+utils.TrimToRunes(utils.SanitizeToolProtocolOutput(result), 500))
	}
	return observations
}

// streamToolCallAcc 流式 tool_calls 增量累积器
/*
streamToolCallAcc 用于在原生流式模式下累积单个工具调用的增量字段。
*/
type streamToolCallAcc struct {
	id        string
	name      string
	arguments string
}

// splitIntoChunks 将文本按指定长度分割成块，优先在句子边界分割
func splitIntoChunks(text string, chunkSize int) []string {
	if len(text) <= chunkSize {
		return []string{text}
	}

	var chunks []string
	runes := []rune(text)

	for len(runes) > 0 {
		if len(runes) <= chunkSize {
			chunks = append(chunks, string(runes))
			break
		}

		// 在 chunkSize 附近找句子边界
		splitAt := chunkSize
		for i := chunkSize; i > chunkSize/2 && i < len(runes); i-- {
			r := runes[i]
			if r == '\n' || r == '。' || r == '.' || r == '！' || r == '?' || r == '；' || r == ';' {
				splitAt = i + 1
				break
			}
		}

		chunks = append(chunks, string(runes[:splitAt]))
		runes = runes[splitAt:]
	}

	return chunks
}
