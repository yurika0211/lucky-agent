package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/logger"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/tool"
)

type agentRuntime interface {
	Chat(ctx context.Context, userInput string) (string, error)
	ChatWithSession(ctx context.Context, sessionID, userInput string) (string, error)
	ChatWithSessionStream(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error)
	Sessions() *session.Manager
	Tools() *tool.Registry
}

// AgentHandler 将 WebSocket 消息桥接到 Agent Loop
type AgentHandler struct {
	agent   agentRuntime
	pending map[string]context.CancelFunc // sessionID → cancel
	mu      sync.Mutex
}

// NewAgentHandler 创建 Agent 消息处理器
func NewAgentHandler(a agentRuntime) *AgentHandler {
	return &AgentHandler{
		agent:   a,
		pending: make(map[string]context.CancelFunc),
	}
}

// HandleMessage 处理来自 WebSocket 客户端的消息
func (h *AgentHandler) HandleMessage(client *Client, msg *Message) {
	switch msg.Type {
	case TypeChat:
		h.handleChat(client, msg)
	case TypeStreamAck:
		// 流式确认，暂不处理
		logger.Debug("stream ack received", "client_id", client.ID, "msg_id", msg.ID)
	default:
		logger.Warn("unknown message type", "type", msg.Type, "client_id", client.ID)
	}
}

// handleChat 处理聊天消息
func (h *AgentHandler) handleChat(client *Client, msg *Message) {
	var data ChatData
	if err := msg.ParseData(&data); err != nil {
		errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
			Code:    "INVALID_DATA",
			Message: fmt.Sprintf("invalid chat data: %v", err),
		})
		client.Send <- errMsg
		return
	}

	// 发送 thinking 状态
	status, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State:   "thinking",
		Message: "processing your message",
	})
	client.Send <- status

	// 取消该 session 之前的请求
	h.mu.Lock()
	if cancel, ok := h.pending[client.SessionID]; ok {
		cancel()
		delete(h.pending, client.SessionID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.pending[client.SessionID] = cancel
	h.mu.Unlock()

	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.pending, client.SessionID)
			h.mu.Unlock()
		}()

		if data.Stream {
			h.streamChat(ctx, client, data, msg.ID)
		} else {
			h.syncChat(ctx, client, data, msg.ID)
		}
	}()
}

// syncChat 同步聊天（等待完整响应）
func (h *AgentHandler) syncChat(ctx context.Context, client *Client, data ChatData, parentID string) {
	// 发送 executing 状态
	status, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State:   "executing",
		Message: "agent is running",
	})
	client.Send <- status

	sessionID := h.ensureSession(client.SessionID)
	result, err := h.agent.ChatWithSession(ctx, sessionID, data.Message)
	if err != nil {
		errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
			Code:    "AGENT_ERROR",
			Message: err.Error(),
		})
		errMsg.ParentID = parentID
		client.Send <- errMsg
		return
	}

	// 发送完整响应
	endMsg, _ := NewMessage(TypeStreamEnd, client.SessionID, StreamEndData{
		FullResponse: result,
		Iterations:   1,
	})
	endMsg.ParentID = parentID
	client.Send <- endMsg

	// 发送 idle 状态
	idle, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State: "idle",
	})
	client.Send <- idle
}

// streamChat 流式聊天（逐块推送）
func (h *AgentHandler) streamChat(ctx context.Context, client *Client, data ChatData, parentID string) {
	// 发送 executing 状态
	status, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State:   "executing",
		Message: "agent is streaming",
	})
	client.Send <- status

	sessionID := h.ensureSession(client.SessionID)
	streamCh, err := h.agent.ChatWithSessionStream(ctx, sessionID, data.Message)
	if err != nil {
		errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
			Code:    "AGENT_ERROR",
			Message: err.Error(),
		})
		errMsg.ParentID = parentID
		client.Send <- errMsg
		return
	}

	var fullResponse strings.Builder
	currentRound := 0
	toolSeq := 0
	var pendingSteps []toolStepState

	sendIdle := func() {
		idle, _ := NewMessage(TypeStatus, client.SessionID, StatusData{State: "idle"})
		idle.ParentID = parentID
		client.Send <- idle
	}

	sendError := func(err error) {
		errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
			Code:    "AGENT_ERROR",
			Message: err.Error(),
		})
		errMsg.ParentID = parentID
		client.Send <- errMsg
		sendIdle()
	}

	for evt := range streamCh {
		switch evt.Type {
		case agent.ChatEventThinking:
			reasoning, ok := reasoningDataForEvent(evt.Content)
			if !ok {
				continue
			}
			if reasoning.Round > currentRound {
				currentRound = reasoning.Round
			}
			msg, _ := NewMessage(TypeReasoning, client.SessionID, reasoning)
			msg.ParentID = parentID
			client.Send <- msg

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
			client.Send <- msg

		case agent.ChatEventToolResult:
			if currentRound == 0 {
				currentRound = 1
			}
			step := matchPendingToolStep(&pendingSteps, evt.Name, currentRound)
			msg, _ := NewMessage(TypeToolResult, client.SessionID, ToolResultData{
				Name:       evt.Name,
				Success:    !looksLikeToolError(evt.Result),
				Output:     evt.Result,
				Display:    formatToolResultDisplay(evt.Name, evt.Result),
				Round:      currentRound,
				GroupID:    step.GroupID,
				StepID:     step.StepID,
				Visibility: step.Visibility,
			})
			msg.ParentID = parentID
			client.Send <- msg

		case agent.ChatEventContent:
			fullResponse.WriteString(evt.Content)
			msg, _ := NewMessage(TypeStreamChunk, client.SessionID, StreamChunkData{
				Content: evt.Content,
				Done:    false,
			})
			msg.ParentID = parentID
			client.Send <- msg

		case agent.ChatEventDone:
			if fullResponse.Len() == 0 && evt.Content != "" {
				fullResponse.WriteString(evt.Content)
			}
			endMsg, _ := NewMessage(TypeStreamEnd, client.SessionID, StreamEndData{
				FullResponse: fullResponse.String(),
				Iterations:   max(currentRound, 1),
			})
			endMsg.ParentID = parentID
			client.Send <- endMsg
			sendIdle()
			return

		case agent.ChatEventError:
			sendError(evt.Err)
			return
		}
	}

	if fullResponse.Len() > 0 {
		endMsg, _ := NewMessage(TypeStreamEnd, client.SessionID, StreamEndData{
			FullResponse: fullResponse.String(),
			Iterations:   max(currentRound, 1),
		})
		endMsg.ParentID = parentID
		client.Send <- endMsg
	}
	sendIdle()
}

// CancelSession 取消指定 session 的进行中请求
func (h *AgentHandler) CancelSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cancel, ok := h.pending[sessionID]; ok {
		cancel()
		delete(h.pending, sessionID)
	}
}

// PendingCount 返回进行中的请求数
func (h *AgentHandler) PendingCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pending)
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
