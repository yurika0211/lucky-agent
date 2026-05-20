package websocket

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// MessageType WebSocket 消息类型
type MessageType string

const (
	// 客户端 → 服务端
	TypeChat      MessageType = "chat"       // 聊天消息
	TypeStreamAck MessageType = "stream_ack" // 流式确认
	TypePing      MessageType = "ping"       // 心跳 ping
	TypeReconnect MessageType = "reconnect"  // 断线重连

	// 服务端 → 客户端
	TypeStreamChunk MessageType = "stream_chunk" // 流式输出块
	TypeStreamEnd   MessageType = "stream_end"   // 流式输出结束
	TypeReasoning   MessageType = "reasoning"    // 推理/思考摘要
	TypeToolCall    MessageType = "tool_call"    // 工具调用通知
	TypeToolResult  MessageType = "tool_result"  // 工具调用结果
	TypeStatus      MessageType = "status"       // 状态更新
	TypeError       MessageType = "error"        // 错误消息
	TypePong        MessageType = "pong"         // 心跳 pong
)

// Message WebSocket 消息协议
type Message struct {
	Type      MessageType     `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	ID        string          `json:"id,omitempty"`        // 消息唯一 ID
	ParentID  string          `json:"parent_id,omitempty"` // 回复的消息 ID
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// ChatData 聊天消息数据
type ChatData struct {
	Message   string `json:"message"`
	Stream    bool   `json:"stream,omitempty"`
	MaxIter   int    `json:"max_iterations,omitempty"`
	ProfileID string `json:"profile_id,omitempty"`
}

// StreamChunkData 流式输出块数据
type StreamChunkData struct {
	Content string `json:"content"`
	Done    bool   `json:"done"`
}

// StreamEndData 流式输出结束数据
type StreamEndData struct {
	FullResponse string `json:"full_response"`
	Iterations   int    `json:"iterations"`
}

// ReasoningData 推理摘要数据
type ReasoningData struct {
	Summary string `json:"summary"`
	Round   int    `json:"round,omitempty"`
	Stage   string `json:"stage,omitempty"` // "start" | "continue" | "update"
}

// ToolCallData 工具调用通知数据
type ToolCallData struct {
	Name       string                 `json:"name"`
	Params     map[string]interface{} `json:"params,omitempty"`
	Args       string                 `json:"args,omitempty"`
	Display    string                 `json:"display,omitempty"`
	Phase      string                 `json:"phase"` // "start" | "progress" | "end"
	Round      int                    `json:"round,omitempty"`
	GroupID    string                 `json:"group_id,omitempty"`
	StepID     string                 `json:"step_id,omitempty"`
	Visibility string                 `json:"visibility,omitempty"` // "visible" | "compact" | "hidden"
}

// ToolResultData 工具调用结果数据
type ToolResultData struct {
	Name       string `json:"name"`
	Success    bool   `json:"success"`
	Output     string `json:"output"`
	Display    string `json:"display,omitempty"`
	Round      int    `json:"round,omitempty"`
	GroupID    string `json:"group_id,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	Visibility string `json:"visibility,omitempty"` // "visible" | "compact" | "hidden"
}

// StatusData 状态更新数据
type StatusData struct {
	State   string `json:"state"` // "thinking" | "executing" | "idle" | "error"
	Message string `json:"message,omitempty"`
}

// ErrorData 错误消息数据
type ErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ReconnectData 断线重连数据
type ReconnectData struct {
	LastMessageID string `json:"last_message_id"`
}

// NewMessage 创建新消息
func NewMessage(msgType MessageType, sessionID string, data interface{}) (*Message, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &Message{
		Type:      msgType,
		SessionID: sessionID,
		ID:        generateID(),
		Timestamp: time.Now().UTC(),
		Data:      raw,
	}, nil
}

// ParseMessage 解析 WebSocket 消息
func ParseMessage(raw []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// ParseData 解析消息中的 Data 字段
func (m *Message) ParseData(v interface{}) error {
	return json.Unmarshal(m.Data, v)
}

// ID 生成器
var idCounter int64
var idMu sync.Mutex

func generateID() string {
	idMu.Lock()
	defer idMu.Unlock()
	idCounter++
	return fmt.Sprintf("ws-%d-%d", time.Now().UnixNano(), idCounter)
}
