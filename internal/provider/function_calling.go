package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// GenerateCallID 生成唯一的 call_id，用于工具调用
// 格式："call_" + 16 字符随机字符串
func GenerateCallID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// 极端情况下随机数生成失败，使用时间戳
		return "call_" + hex.EncodeToString([]byte("fallback"))
	}
	return "call_" + hex.EncodeToString(b)
}

// CallOptions 是 API 调用的额外选项（用于 function calling）
type CallOptions struct {
	Tools        []map[string]any // OpenAI function calling 工具定义
	ToolChoice   any              // "auto" | "none" | {"type":"function","function":{"name":"xxx"}}
	MaxToolCalls int              // 单次响应最大工具调用数（0 = 不限制）
}

// ChatWithOptions 发送消息并获取完整响应（支持 function calling）
type ChatWithOptionsFunc func(ctx context.Context, messages []Message, opts CallOptions) (*Response, error)

// ChatStreamWithOptions 发送消息并获取流式响应（支持 function calling）
type ChatStreamWithOptionsFunc func(ctx context.Context, messages []Message, opts CallOptions) (<-chan StreamChunk, error)

// FunctionCallingProvider 扩展 Provider 接口，支持 function calling
type FunctionCallingProvider interface {
	Provider
	// ChatWithOptions 发送消息（支持 function calling 选项）
	ChatWithOptions(ctx context.Context, messages []Message, opts CallOptions) (*Response, error)
	// ChatStreamWithOptions 发送消息流式（支持 function calling 选项）
	ChatStreamWithOptions(ctx context.Context, messages []Message, opts CallOptions) (<-chan StreamChunk, error)
}

// DefaultCallOptions 返回默认调用选项（使用配置默认值）
func DefaultCallOptions(cfg Config) CallOptions {
	maxToolCalls := cfg.Limits.MaxToolCalls
	if maxToolCalls <= 0 {
		maxToolCalls = 5 // 默认值
	}
	return CallOptions{
		ToolChoice:   "auto",
		MaxToolCalls: maxToolCalls,
	}
}

type pendingToolRound struct {
	start    int
	expected map[string]struct{}
	returned map[string]struct{}
}

func newPendingToolRound(start int, calls []ToolCall) *pendingToolRound {
	expected := make(map[string]struct{}, len(calls))
	for _, tc := range calls {
		id := strings.TrimSpace(tc.ID)
		if id == "" {
			continue
		}
		expected[id] = struct{}{}
	}
	return &pendingToolRound{
		start:    start,
		expected: expected,
		returned: make(map[string]struct{}, len(expected)),
	}
}

func (r *pendingToolRound) complete() bool {
	if r == nil {
		return true
	}
	if len(r.expected) == 0 {
		return false
	}
	return len(r.returned) == len(r.expected)
}

func (r *pendingToolRound) accepts(callID string) bool {
	if r == nil {
		return false
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return false
	}
	if _, ok := r.expected[callID]; !ok {
		return false
	}
	if _, ok := r.returned[callID]; ok {
		return false
	}
	return true
}

func (r *pendingToolRound) markReturned(callID string) {
	if r == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	r.returned[callID] = struct{}{}
}

// normalizeToolProtocolMessages enforces a strict Chat Completions tool-calling state machine:
// assistant(tool_calls) -> tool(tool_call_id) ... -> next non-tool message.
// Incomplete rounds and orphan tool messages are dropped instead of being forwarded upstream.
func normalizeToolProtocolMessages(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	var round *pendingToolRound

	dropRound := func() {
		if round == nil {
			return
		}
		out = out[:round.start]
		round = nil
	}

	closeRoundIfNeeded := func() {
		if round == nil {
			return
		}
		if round.complete() {
			round = nil
			return
		}
		dropRound()
	}

	for _, msg := range messages {
		if msg.Role != "tool" {
			closeRoundIfNeeded()
		}

		if len(msg.ToolCalls) > 0 {
			if round != nil {
				dropRound()
			}
			round = newPendingToolRound(len(out), msg.ToolCalls)
			out = append(out, msg)
			continue
		}

		if msg.Role == "tool" {
			if round == nil || !round.accepts(msg.ToolCallID) {
				continue
			}
			out = append(out, msg)
			round.markReturned(msg.ToolCallID)
			continue
		}

		out = append(out, msg)
	}

	if round != nil && !round.complete() {
		dropRound()
	}

	return out
}
