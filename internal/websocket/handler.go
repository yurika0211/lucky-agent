package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/logger"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type agentRuntime interface {
	Chat(ctx context.Context, userInput string) (string, error)
	ChatWithSession(ctx context.Context, sessionID, userInput string) (string, error)
	ChatWithSessionStream(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error)
	ChatWithSessionInput(ctx context.Context, sessionID string, input agent.UserTurnInput) (string, error)
	ChatWithSessionStreamInput(ctx context.Context, sessionID string, input agent.UserTurnInput) (<-chan agent.ChatEvent, error)
	Sessions() *session.Manager
	Tools() *tool.Registry
}

type runtimeConfigProvider interface {
	Config() *config.Manager
}

type progressFeedbackRuntime interface {
	ProgressFeedbackWithPrompt(ctx context.Context, userInput string, round int, observations []string, presentationPrompt string) (string, error)
}

type queuedRun struct {
	id       string
	parentID string
	data     ChatData
	resume   bool
	client   *Client
}

type sessionRunner struct {
	queue     []*queuedRun
	active    *queuedRun
	cancel    context.CancelFunc
	cancelled bool
}

// AgentHandler 将 WebSocket 消息桥接到 Agent Loop
type AgentHandler struct {
	agent     agentRuntime
	pending   map[string]context.CancelFunc // sessionID → cancel
	done      map[string]chan struct{}      // sessionID → handler goroutine completion
	runners   map[string]*sessionRunner
	store     *runStore
	eventSink func(string, *Message)
	mu        sync.Mutex
}

// NewAgentHandler 创建 Agent 消息处理器
func NewAgentHandler(a agentRuntime) *AgentHandler {
	storeRoot := ""
	if provider, ok := a.(runtimeConfigProvider); ok && provider.Config() != nil {
		storeRoot = filepath.Join(provider.Config().HomeDir(), "runtime", "websocket")
	}
	return &AgentHandler{
		agent:   a,
		pending: make(map[string]context.CancelFunc),
		done:    make(map[string]chan struct{}),
		runners: make(map[string]*sessionRunner),
		store:   newRunStore(storeRoot),
	}
}

// SetEventSink makes agent output independent from the client that submitted it.
// The HTTP server wires this to Hub.SendToSession so reconnecting clients receive
// the same durable stream.
func (h *AgentHandler) SetEventSink(sink func(string, *Message)) {
	h.mu.Lock()
	h.eventSink = sink
	h.mu.Unlock()
}

// Start restores queued and interrupted runs after the runtime process starts.
func (h *AgentHandler) Start() {
	for _, persisted := range h.store.restoreable() {
		data := ChatData{
			Message:     persisted.Message,
			Stream:      persisted.Stream,
			MaxIter:     persisted.MaxIter,
			Attachments: persisted.Attachments,
		}
		run := &queuedRun{id: persisted.ID, parentID: persisted.ParentID, data: data}
		if persisted.TaskID != "" {
			run.data.Message = "继续前台任务 " + persisted.TaskID
			run.data.Attachments = nil
			run.resume = true
		}
		h.mu.Lock()
		runner := h.runners[persisted.SessionID]
		if runner == nil {
			runner = &sessionRunner{}
			h.runners[persisted.SessionID] = runner
		}
		runner.queue = append(runner.queue, run)
		h.mu.Unlock()
	}
	for sessionID := range h.runners {
		h.startNext(sessionID)
	}
}

// HandleMessage 处理来自 WebSocket 客户端的消息
func (h *AgentHandler) HandleMessage(client *Client, msg *Message) {
	switch msg.Type {
	case TypeChat:
		h.handleChat(client, msg)
	case TypeCancel:
		h.handleCancel(client, msg)
	case TypeReconnect:
		h.HandleReconnect(client, msg)
	case TypeStreamAck:
		// 流式确认，暂不处理
		logger.Debug("stream ack received", "client_id", client.ID, "msg_id", msg.ID)
	default:
		logger.Warn("unknown message type", "type", msg.Type, "client_id", client.ID)
	}
}

// HandleReconnect replays events after the supplied cursor. Without a usable
// cursor it only replays events belonging to runs that are still active.
func (h *AgentHandler) HandleReconnect(client *Client, msg *Message) {
	var data ReconnectData
	if len(msg.Data) > 0 && msg.ParseData(&data) != nil {
		data.LastMessageID = ""
	}
	for _, event := range h.store.replayForReconnect(client.SessionID, strings.TrimSpace(data.LastMessageID)) {
		client.TrySend(event)
	}
	for _, run := range h.store.activeRuns(client.SessionID) {
		state := "queued"
		message := "message queued"
		if run.State == "running" {
			state = "running"
			message = "agent is running"
		}
		status, _ := NewMessage(TypeStatus, client.SessionID, StatusData{State: state, Message: message})
		status.ParentID = run.ParentID
		status.RunID = run.ID
		status.ID = ""
		status.EventID = ""
		client.TrySend(status)
	}
	status, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State:   "connected",
		Message: "reconnected",
	})
	status.ID = ""
	status.EventID = ""
	client.TrySend(status)
	logger.Info("client reconnecting", "client_id", client.ID, "last_msg", data.LastMessageID)
}

// handleCancel 取消指定 session 的进行中请求。客户端 Stop 必须走这条路径，
// 只关 WebSocket 不会停止已经跑起来的 agent turn。
func (h *AgentHandler) handleCancel(client *Client, msg *Message) {
	var data CancelData
	if len(msg.Data) > 0 {
		if err := msg.ParseData(&data); err != nil {
			errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
				Code:    "INVALID_DATA",
				Message: fmt.Sprintf("invalid cancel data: %v", err),
			})
			client.TrySend(errMsg)
			return
		}
	}
	sessionID := strings.TrimSpace(data.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(msg.SessionID)
	}
	if sessionID == "" && client != nil {
		sessionID = client.SessionID
	}
	if sessionID == "" {
		errMsg, _ := NewMessage(TypeError, "", ErrorData{
			Code:    "INVALID_DATA",
			Message: "cancel requires a session id",
		})
		if client != nil {
			client.TrySend(errMsg)
		}
		return
	}
	cancelled := h.cancelSession(sessionID)
	if len(cancelled) == 0 {
		status, _ := NewMessage(TypeStatus, sessionID, StatusData{
			State:   "idle",
			Message: "cancelled",
		})
		if msg != nil {
			status.ParentID = msg.ID
		}
		h.emit(client, sessionID, "", status)
	} else {
		for _, run := range cancelled {
			status, _ := NewMessage(TypeStatus, sessionID, StatusData{
				State:   "idle",
				Message: "cancelled",
			})
			status.ParentID = run.parentID
			status.RunID = run.id
			h.emit(client, sessionID, run.id, status)
		}
	}
	logger.Info("session cancelled", "session", sessionID)
}

// handleChat 处理聊天消息
func (h *AgentHandler) handleChat(client *Client, msg *Message) {
	var data ChatData
	if err := msg.ParseData(&data); err != nil {
		errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
			Code:    "INVALID_DATA",
			Message: fmt.Sprintf("invalid chat data: %v", err),
		})
		client.TrySend(errMsg)
		return
	}

	run := &queuedRun{
		id:       msg.ID,
		parentID: msg.ID,
		data:     data,
		client:   client,
	}
	if run.id == "" {
		run.id = generateID()
		run.parentID = run.id
	}
	persisted := persistedRun{
		ID:          run.id,
		SessionID:   client.SessionID,
		ParentID:    run.parentID,
		Message:     data.Message,
		Stream:      data.Stream,
		MaxIter:     data.MaxIter,
		Attachments: data.Attachments,
		State:       "queued",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := h.store.upsertRun(persisted); err != nil {
		logger.Error("persist websocket run failed", "session", client.SessionID, "error", err)
	}
	h.mu.Lock()
	runner := h.runners[client.SessionID]
	if runner == nil {
		runner = &sessionRunner{}
		h.runners[client.SessionID] = runner
	}
	wasBusy := runner.active != nil || len(runner.queue) > 0
	runner.queue = append(runner.queue, run)
	h.mu.Unlock()
	queued, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State:   map[bool]string{true: "queued", false: "thinking"}[wasBusy],
		Message: map[bool]string{true: "message queued", false: "processing your message"}[wasBusy],
	})
	queued.ParentID = run.parentID
	queued.RunID = run.id
	h.emit(client, client.SessionID, run.id, queued)
	if !wasBusy {
		h.startNext(client.SessionID)
	}
}

func (h *AgentHandler) startNext(sessionID string) {
	h.mu.Lock()
	runner := h.runners[sessionID]
	if runner == nil || runner.active != nil || len(runner.queue) == 0 {
		h.mu.Unlock()
		return
	}
	run := runner.queue[0]
	runner.queue = runner.queue[1:]
	runner.active = run
	ctx, cancel := context.WithCancel(context.Background())
	runner.cancel = cancel
	runner.cancelled = false
	h.pending[sessionID] = cancel
	if h.done[sessionID] == nil {
		h.done[sessionID] = make(chan struct{})
	}
	h.mu.Unlock()
	_ = h.store.updateRun(sessionID, run.id, func(record *persistedRun) { record.State = "running" })
	go func() {
		var err error
		client := run.client
		if client == nil {
			client = &Client{SessionID: sessionID, Send: make(chan *Message, 1)}
		}
		if run.data.Stream {
			err = h.streamChatRun(ctx, client, run.data, run.parentID, run.id)
		} else {
			err = h.syncChatRun(ctx, client, run.data, run.parentID, run.id)
		}
		h.finishRun(sessionID, run, err)
	}()
}

func (h *AgentHandler) finishRun(sessionID string, run *queuedRun, err error) {
	h.mu.Lock()
	runner := h.runners[sessionID]
	if runner == nil || runner.active != run {
		h.mu.Unlock()
		return
	}
	wasCancelled := runner.cancelled
	runner.active = nil
	runner.cancel = nil
	runner.cancelled = false
	delete(h.pending, sessionID)
	if len(runner.queue) == 0 {
		if done := h.done[sessionID]; done != nil {
			close(done)
			delete(h.done, sessionID)
		}
	}
	h.mu.Unlock()
	state := "completed"
	if wasCancelled || errors.Is(err, context.Canceled) {
		state = "cancelled"
	} else if err != nil {
		state = "error"
	}
	_ = h.store.updateRun(sessionID, run.id, func(record *persistedRun) { record.State = state })
	h.startNext(sessionID)
}

func (h *AgentHandler) emit(client *Client, sessionID, runID string, msg *Message) {
	if msg == nil {
		return
	}
	msg.SessionID = sessionID
	if runID != "" {
		msg.RunID = runID
	}
	if err := h.store.appendEvent(sessionID, runID, msg); err != nil {
		logger.Error("persist websocket event failed", "session", sessionID, "error", err)
	}
	h.mu.Lock()
	sink := h.eventSink
	h.mu.Unlock()
	if sink != nil {
		sink(sessionID, msg)
		return
	}
	if client != nil {
		client.TrySend(msg)
	}
}

// syncChat 同步聊天（等待完整响应）
func (h *AgentHandler) syncChat(ctx context.Context, client *Client, data ChatData, parentID string) {
	_ = h.syncChatRun(ctx, client, data, parentID, "")
}

func (h *AgentHandler) syncChatRun(ctx context.Context, client *Client, data ChatData, parentID, runID string) error {
	// 发送 executing 状态
	status, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State:   "executing",
		Message: "agent is running",
	})
	status.ParentID = parentID
	h.emit(client, client.SessionID, runID, status)

	sessionID := h.ensureSession(client.SessionID)
	turn := agent.MultimodalUserTurnInput(data.Message, data.Attachments)
	result, err := h.agent.ChatWithSessionInput(ctx, sessionID, turn)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
			Code:    "AGENT_ERROR",
			Message: err.Error(),
		})
		errMsg.ParentID = parentID
		h.emit(client, client.SessionID, runID, errMsg)
		return err
	}
	var createdAt *time.Time
	var usage *provider.TokenUsage
	if sessions := h.agent.Sessions(); sessions != nil {
		if sess, ok := sessions.Get(sessionID); ok {
			messages := sess.GetMessages()
			if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
				createdAt = messages[len(messages)-1].CreatedAt
				usage = messages[len(messages)-1].Usage
			}
		}
	}

	// 发送完整响应
	response, responseAttachments := h.attachmentsFromResponse(result)
	endMsg, _ := NewMessage(TypeStreamEnd, client.SessionID, StreamEndData{
		FullResponse: response,
		Iterations:   1,
		CreatedAt:    createdAt,
		Usage:        usage,
		Attachments:  appendUniqueAttachments(h.attachmentsFromToolResult("", result), responseAttachments...),
	})
	endMsg.ParentID = parentID
	h.emit(client, client.SessionID, runID, endMsg)

	// 发送 idle 状态
	idle, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State: "idle",
	})
	idle.ParentID = parentID
	h.emit(client, client.SessionID, runID, idle)
	return nil
}

// streamChat 流式聊天（逐块推送）
func (h *AgentHandler) streamChat(ctx context.Context, client *Client, data ChatData, parentID string) {
	_ = h.streamChatRun(ctx, client, data, parentID, "")
}

func (h *AgentHandler) streamChatRun(ctx context.Context, client *Client, data ChatData, parentID, runID string) error {
	// 发送 executing 状态
	status, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State:   "executing",
		Message: "agent is streaming",
	})
	status.ParentID = parentID
	h.emit(client, client.SessionID, runID, status)

	sessionID := h.ensureSession(client.SessionID)
	turn := agent.MultimodalUserTurnInput(data.Message, data.Attachments)
	streamCh, err := h.agent.ChatWithSessionStreamInput(ctx, sessionID, turn)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
			Code:    "AGENT_ERROR",
			Message: err.Error(),
		})
		errMsg.ParentID = parentID
		h.emit(client, client.SessionID, runID, errMsg)
		return err
	}

	var fullResponse strings.Builder
	currentRound := 0
	toolSeq := 0
	var pendingSteps []toolStepState
	var turnAttachments []gateway.Attachment
	progressSummaryEnabled, progressSummaryPrompt := h.progressSummaryConfig()
	var roundObservations []string
	var progressHistory []string
	lastProgress := ""

	emitRoundProgress := func(round int) bool {
		if !progressSummaryEnabled || len(roundObservations) == 0 {
			return false
		}
		runtime, ok := h.agent.(progressFeedbackRuntime)
		if !ok {
			return false
		}
		observations := append([]string(nil), roundObservations...)
		for _, previous := range progressHistory {
			if previous = strings.TrimSpace(previous); previous != "" {
				observations = append([]string{"Previous user-facing update: " + previous}, observations...)
			}
		}
		summaryCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		summary, err := runtime.ProgressFeedbackWithPrompt(summaryCtx, data.Message, round, observations, progressSummaryPrompt)
		cancel()
		if err != nil {
			return false
		}
		summary = strings.TrimSpace(summary)
		if summary == "" || summary == lastProgress {
			return false
		}
		msg, _ := NewMessage(TypeReasoning, client.SessionID, ReasoningData{
			Summary: summary,
			Round:   round,
			Stage:   "progress",
		})
		msg.ParentID = parentID
		h.emit(client, client.SessionID, runID, msg)
		lastProgress = summary
		progressHistory = append(progressHistory, summary)
		return true
	}

	sendIdle := func() {
		idle, _ := NewMessage(TypeStatus, client.SessionID, StatusData{State: "idle"})
		idle.ParentID = parentID
		h.emit(client, client.SessionID, runID, idle)
	}

	sendError := func(err error) {
		if err == nil {
			err = errors.New("agent stream failed")
		}
		errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
			Code:    "AGENT_ERROR",
			Message: err.Error(),
		})
		errMsg.ParentID = parentID
		h.emit(client, client.SessionID, runID, errMsg)
		sendIdle()
	}

	for evt := range streamCh {
		switch evt.Type {
		case agent.ChatEventThinking:
			if evt.TaskID != "" && runID != "" {
				_ = h.store.updateRun(client.SessionID, runID, func(record *persistedRun) { record.TaskID = evt.TaskID })
			}
			reasoning, ok := reasoningDataForEvent(evt.Content)
			if !ok {
				continue
			}
			if reasoning.Round > currentRound {
				emittedSummary := false
				if currentRound > 0 {
					emittedSummary = emitRoundProgress(currentRound)
				}
				roundObservations = nil
				currentRound = reasoning.Round
				if emittedSummary {
					continue
				}
			}
			msg, _ := NewMessage(TypeReasoning, client.SessionID, reasoning)
			msg.ParentID = parentID
			h.emit(client, client.SessionID, runID, msg)

		case agent.ChatEventReasoningContent:
			if currentRound == 0 {
				currentRound = evt.Round
			}
			msg, _ := NewMessage(TypeReasoning, client.SessionID, ReasoningData{
				Content: evt.Content,
				Round:   evt.Round,
				Stage:   "content",
			})
			msg.ParentID = parentID
			h.emit(client, client.SessionID, runID, msg)

		case agent.ChatEventToolCall:
			if currentRound == 0 {
				currentRound = 1
			}
			toolSeq++
			groupID := fmt.Sprintf("round-%d", currentRound)
			step := toolStepState{
				Name:       evt.Name,
				GroupID:    groupID,
				StepID:     fmt.Sprintf("%s-tool-%d", groupID, toolSeq),
				Visibility: h.toolVisibility(evt.Name),
			}
			pendingSteps = append(pendingSteps, step)
			if progressSummaryEnabled {
				roundObservations = append(roundObservations, fmt.Sprintf("Tool call: %s", formatToolCallDisplay(evt.Name, evt.Args)))
			}
			msg, _ := NewMessage(TypeToolCall, client.SessionID, ToolCallData{
				Name:       evt.Name,
				Params:     parseToolParams(evt.Args),
				Args:       evt.Args,
				Display:    formatToolCallDisplay(evt.Name, evt.Args),
				Phase:      "start",
				Round:      currentRound,
				GroupID:    step.GroupID,
				StepID:     step.StepID,
				Visibility: step.Visibility,
			})
			msg.ParentID = parentID
			h.emit(client, client.SessionID, runID, msg)

		case agent.ChatEventToolResult:
			if currentRound == 0 {
				currentRound = 1
			}
			step := matchPendingToolStep(&pendingSteps, evt.Name, currentRound)
			attachments := h.attachmentsFromToolResult(evt.Name, evt.Result)
			turnAttachments = appendUniqueAttachments(turnAttachments, attachments...)
			if progressSummaryEnabled {
				result := strings.TrimSpace(evt.Result)
				if len(result) > 600 {
					result = result[:600] + "..."
				}
				roundObservations = append(roundObservations, fmt.Sprintf("Tool result (%s): %s", evt.Name, result))
			}
			msg, _ := NewMessage(TypeToolResult, client.SessionID, ToolResultData{
				Name:        evt.Name,
				Success:     !looksLikeToolError(evt.Result),
				Output:      evt.Result,
				Display:     formatToolResultDisplay(evt.Name, evt.Result),
				Round:       currentRound,
				GroupID:     step.GroupID,
				StepID:      step.StepID,
				Visibility:  step.Visibility,
				Attachments: attachments,
			})
			msg.ParentID = parentID
			h.emit(client, client.SessionID, runID, msg)

		case agent.ChatEventContent:
			fullResponse.WriteString(evt.Content)
			msg, _ := NewMessage(TypeStreamChunk, client.SessionID, StreamChunkData{
				Content: evt.Content,
				Done:    false,
			})
			msg.ParentID = parentID
			h.emit(client, client.SessionID, runID, msg)

		case agent.ChatEventDone:
			emitRoundProgress(max(currentRound, 1))
			if evt.Content != "" {
				fullResponse.Reset()
				fullResponse.WriteString(evt.Content)
			}
			response, responseAttachments := h.attachmentsFromResponse(fullResponse.String())
			turnAttachments = appendUniqueAttachments(turnAttachments, responseAttachments...)
			endMsg, _ := NewMessage(TypeStreamEnd, client.SessionID, StreamEndData{
				FullResponse: response,
				Iterations:   max(currentRound, 1),
				CreatedAt:    evt.CreatedAt,
				Usage:        evt.Usage,
				Attachments:  turnAttachments,
			})
			endMsg.ParentID = parentID
			h.emit(client, client.SessionID, runID, endMsg)
			sendIdle()
			return nil

		case agent.ChatEventError:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			sendError(evt.Err)
			return evt.Err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if fullResponse.Len() > 0 {
		response, responseAttachments := h.attachmentsFromResponse(fullResponse.String())
		turnAttachments = appendUniqueAttachments(turnAttachments, responseAttachments...)
		endMsg, _ := NewMessage(TypeStreamEnd, client.SessionID, StreamEndData{
			FullResponse: response,
			Iterations:   max(currentRound, 1),
			Attachments:  turnAttachments,
		})
		endMsg.ParentID = parentID
		h.emit(client, client.SessionID, runID, endMsg)
	}
	sendIdle()
	return nil
}

type toolAttachmentPayload struct {
	Paths       []string             `json:"paths"`
	Path        string               `json:"path"`
	OutputPath  string               `json:"output_path"`
	FilePath    string               `json:"file_path"`
	URL         string               `json:"url"`
	FileURL     string               `json:"file_url"`
	DownloadURL string               `json:"download_url"`
	URLs        []string             `json:"urls"`
	FileURLs    []string             `json:"file_urls"`
	Attachments []gateway.Attachment `json:"attachments"`
}

func (h *AgentHandler) attachmentsFromToolResult(toolName, raw string) []gateway.Attachment {
	var payload toolAttachmentPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}

	fallbackType := gateway.AttachmentDocument
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "image_generate":
		fallbackType = gateway.AttachmentImage
	case "text_to_speech":
		fallbackType = gateway.AttachmentAudio
	}

	attachments := make([]gateway.Attachment, 0, len(payload.Paths)+len(payload.Attachments)+len(payload.URLs)+len(payload.FileURLs)+4)
	for _, attachment := range payload.Attachments {
		if normalized, ok := normalizeRemoteAttachment(attachment, fallbackType); ok {
			attachments = appendUniqueAttachments(attachments, normalized)
			continue
		}
		if normalized, ok := h.attachmentForLocalPath(attachment.FilePath, fallbackType); ok {
			attachments = appendUniqueAttachments(attachments, normalized)
		}
	}
	for _, path := range append([]string{}, payload.Paths...) {
		if attachment, ok := h.attachmentForLocalPath(path, fallbackType); ok {
			attachments = appendUniqueAttachments(attachments, attachment)
		}
	}
	for _, path := range []string{payload.Path, payload.OutputPath, payload.FilePath} {
		if attachment, ok := h.attachmentForLocalPath(path, fallbackType); ok {
			attachments = appendUniqueAttachments(attachments, attachment)
		}
	}
	for _, rawURL := range append(append([]string{}, payload.URLs...), payload.FileURLs...) {
		if attachment, ok := remoteAttachment(rawURL, fallbackType); ok {
			attachments = appendUniqueAttachments(attachments, attachment)
		}
	}
	for _, rawURL := range []string{payload.URL, payload.FileURL, payload.DownloadURL} {
		if attachment, ok := remoteAttachment(rawURL, fallbackType); ok {
			attachments = appendUniqueAttachments(attachments, attachment)
		}
	}
	return attachments
}

func (h *AgentHandler) attachmentsFromResponse(raw string) (string, []gateway.Attachment) {
	references := append(agent.MediaReferences(raw), agent.ArtifactReferences(raw)...)
	if len(references) == 0 {
		return raw, nil
	}

	resolved := make([]string, 0, len(references))
	attachments := make([]gateway.Attachment, 0, len(references))
	for _, reference := range references {
		var attachment gateway.Attachment
		var ok bool
		if strings.HasPrefix(strings.ToLower(reference), "http://") || strings.HasPrefix(strings.ToLower(reference), "https://") {
			attachment, ok = remoteAttachment(reference, gateway.AttachmentDocument)
		} else {
			attachment, ok = h.attachmentForLocalPath(reference, gateway.AttachmentDocument)
		}
		if !ok {
			continue
		}
		attachments = appendUniqueAttachments(attachments, attachment)
		resolved = append(resolved, reference)
	}
	return agent.StripMediaReferences(raw, resolved), attachments
}

func (h *AgentHandler) attachmentForLocalPath(path string, fallbackType gateway.AttachmentType) (gateway.Attachment, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return gateway.Attachment{}, false
	}
	provider, ok := h.agent.(runtimeConfigProvider)
	if !ok || provider.Config() == nil {
		return gateway.Attachment{}, false
	}
	home := strings.TrimSpace(provider.Config().HomeDir())
	if home == "" {
		return gateway.Attachment{}, false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return gateway.Attachment{}, false
	}
	for _, root := range []struct {
		prefix string
		path   string
	}{
		{prefix: "workspace", path: filepath.Join(home, "workspace")},
		{prefix: "uploads", path: filepath.Join(home, "uploads")},
	} {
		rootPath, err := filepath.Abs(root.path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootPath, absPath)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		info, err := os.Stat(absPath)
		if err != nil || !info.Mode().IsRegular() {
			return gateway.Attachment{}, false
		}
		mimeType := mime.TypeByExtension(filepath.Ext(absPath))
		attachmentType := attachmentTypeForMIME(mimeType, fallbackType)
		return gateway.Attachment{
			Type:     attachmentType,
			FileURL:  "/api/v1/artifacts?path=" + url.QueryEscape(root.prefix+"/"+filepath.ToSlash(rel)),
			FilePath: absPath,
			FileName: filepath.Base(absPath),
			MimeType: mimeType,
			FileSize: info.Size(),
		}, true
	}
	return gateway.Attachment{}, false
}

func remoteAttachment(rawURL string, fallbackType gateway.AttachmentType) (gateway.Attachment, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return gateway.Attachment{}, false
	}
	name := filepath.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		name = "attachment"
	}
	mimeType := mime.TypeByExtension(filepath.Ext(name))
	return gateway.Attachment{
		Type:     attachmentTypeForMIME(mimeType, fallbackType),
		FileURL:  parsed.String(),
		FileName: name,
		MimeType: mimeType,
	}, true
}

func normalizeRemoteAttachment(attachment gateway.Attachment, fallbackType gateway.AttachmentType) (gateway.Attachment, bool) {
	if strings.TrimSpace(attachment.FileURL) == "" {
		return gateway.Attachment{}, false
	}
	normalized, ok := remoteAttachment(attachment.FileURL, fallbackType)
	if !ok {
		return gateway.Attachment{}, false
	}
	normalized.Type = attachmentTypeForMIME(attachment.MimeType, attachment.Type)
	if normalized.Type == "" {
		normalized.Type = attachmentTypeForMIME(normalized.MimeType, fallbackType)
	}
	normalized.FileID = attachment.FileID
	normalized.FileName = firstNonEmptyAttachmentString(attachment.FileName, normalized.FileName)
	normalized.MimeType = firstNonEmptyAttachmentString(attachment.MimeType, normalized.MimeType)
	normalized.FileSize = attachment.FileSize
	normalized.Metadata = attachment.Metadata
	return normalized, true
}

func attachmentTypeForMIME(mimeType string, fallback gateway.AttachmentType) gateway.AttachmentType {
	switch {
	case strings.HasPrefix(strings.ToLower(mimeType), "image/"):
		return gateway.AttachmentImage
	case strings.HasPrefix(strings.ToLower(mimeType), "audio/"):
		return gateway.AttachmentAudio
	case strings.HasPrefix(strings.ToLower(mimeType), "video/"):
		return gateway.AttachmentVideo
	default:
		return fallback
	}
}

func firstNonEmptyAttachmentString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func appendUniqueAttachments(attachments []gateway.Attachment, candidates ...gateway.Attachment) []gateway.Attachment {
	for _, candidate := range candidates {
		key := candidate.FileURL
		if key == "" {
			key = candidate.FilePath
		}
		if key == "" {
			continue
		}
		duplicate := false
		for _, existing := range attachments {
			existingKey := existing.FileURL
			if existingKey == "" {
				existingKey = existing.FilePath
			}
			if existingKey == key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			attachments = append(attachments, candidate)
		}
	}
	return attachments
}

// CancelSession 取消指定 session 的进行中请求
func (h *AgentHandler) CancelSession(sessionID string) {
	h.cancelSession(sessionID)
}

func (h *AgentHandler) cancelSession(sessionID string) []*queuedRun {
	h.mu.Lock()
	runner := h.runners[sessionID]
	var cancel context.CancelFunc
	var queued []*queuedRun
	var cancelled []*queuedRun
	if runner != nil {
		runner.cancelled = true
		if runner.active != nil {
			cancelled = append(cancelled, runner.active)
		}
		cancel = runner.cancel
		queued = append(queued, runner.queue...)
		runner.queue = nil
		if runner.active == nil {
			if done := h.done[sessionID]; done != nil {
				close(done)
				delete(h.done, sessionID)
			}
		}
	}
	if cancel == nil {
		cancel = h.pending[sessionID]
	}
	delete(h.pending, sessionID)
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, run := range queued {
		_ = h.store.updateRun(sessionID, run.id, func(record *persistedRun) { record.State = "cancelled" })
	}
	cancelled = append(cancelled, queued...)
	return cancelled
}

// PendingCount 返回进行中的请求数
func (h *AgentHandler) PendingCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pending)
}

// WaitSession waits for the handler goroutine to exit after cancellation.
// It is useful to make resource cleanup deterministic for callers that own
// temporary runtime directories.
func (h *AgentHandler) WaitSession(sessionID string, timeout time.Duration) bool {
	h.mu.Lock()
	done := h.done[sessionID]
	h.mu.Unlock()
	if done == nil {
		return true
	}
	if timeout <= 0 {
		<-done
		return true
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

func (h *AgentHandler) ensureSession(sessionID string) string {
	if h.agent == nil || h.agent.Sessions() == nil {
		return sessionID
	}
	if strings.TrimSpace(sessionID) == "" {
		return h.agent.Sessions().New().ID
	}
	return h.agent.Sessions().Ensure(sessionID).ID
}

func reasoningDataForEvent(content string) (ReasoningData, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return ReasoningData{}, false
	}
	if round := extractRoundNumber(content); round > 0 {
		summary := "Analyzing the request"
		stage := "start"
		if round > 1 {
			summary = "Continuing reasoning after tool results"
			stage = "continue"
		}
		return ReasoningData{Summary: summary, Round: round, Stage: stage}, true
	}
	return ReasoningData{Summary: content, Stage: "update"}, true
}

func (h *AgentHandler) progressSummaryConfig() (bool, string) {
	provider, ok := h.agent.(runtimeConfigProvider)
	if !ok || provider.Config() == nil {
		return false, ""
	}
	cfg := provider.Config().Get()
	return cfg.Server.ProgressSummaryWithLLM, strings.TrimSpace(cfg.Server.ProgressSummaryPrompt)
}

func extractRoundNumber(thinking string) int {
	var round int
	if _, err := fmt.Sscanf(strings.TrimSpace(thinking), "Thinking... (round %d)", &round); err == nil && round > 0 {
		return round
	}
	return 0
}

func parseToolParams(raw string) map[string]interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil
	}
	return params
}

func formatToolCallDisplay(name, rawArgs string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unknown_tool"
	}
	if rawArgs == "" {
		return name
	}
	return fmt.Sprintf("%s %s", name, rawArgs)
}

func formatToolResultDisplay(name, result string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	trimmed := strings.TrimSpace(result)
	if len(trimmed) > 160 {
		trimmed = trimmed[:157] + "..."
	}
	if trimmed == "" {
		return fmt.Sprintf("%s completed", name)
	}
	return fmt.Sprintf("%s: %s", name, trimmed)
}

func looksLikeToolError(result string) bool {
	result = strings.TrimSpace(strings.ToLower(result))
	return strings.HasPrefix(result, "error:")
}

type toolStepState struct {
	Name       string
	GroupID    string
	StepID     string
	Visibility string
}

func matchPendingToolStep(pending *[]toolStepState, name string, round int) toolStepState {
	if pending == nil || len(*pending) == 0 {
		groupID := fmt.Sprintf("round-%d", max(round, 1))
		return toolStepState{
			Name:       name,
			GroupID:    groupID,
			StepID:     fmt.Sprintf("%s-tool-unknown", groupID),
			Visibility: "visible",
		}
	}

	steps := *pending
	idx := 0
	for i, step := range steps {
		if step.Name == name {
			idx = i
			break
		}
	}
	matched := steps[idx]
	*pending = append(steps[:idx], steps[idx+1:]...)
	return matched
}

func (h *AgentHandler) toolVisibility(name string) string {
	if h.agent == nil {
		return classifyToolVisibility(nil, name)
	}
	return classifyToolVisibility(h.agent.Tools(), name)
}

func classifyToolVisibility(reg *tool.Registry, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "visible"
	}

	if reg != nil {
		if meta, ok := reg.Get(name); ok && meta != nil {
			if meta.HiddenFromModel || meta.Category == tool.CatSkill || meta.Category == tool.CatDelegate {
				return "hidden"
			}
		}
	}

	lower := strings.ToLower(name)
	switch {
	case lower == "skill_read",
		lower == "remember",
		lower == "recall",
		lower == "rag_search",
		lower == "rag_index",
		strings.HasPrefix(lower, "cron"):
		return "compact"
	case strings.HasPrefix(lower, "skill_"),
		strings.HasPrefix(lower, "delegate_"),
		strings.HasPrefix(lower, "autonomy_"),
		strings.HasPrefix(lower, "heartbeat_"):
		return "hidden"
	default:
		return "visible"
	}
}
