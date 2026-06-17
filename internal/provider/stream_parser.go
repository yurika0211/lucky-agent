package provider

// StreamParser 解析流式 SSE 响应，检测工具调用
// 支持 OpenAI function calling 格式的流式工具调用
type StreamParser struct {
	// 累积的 delta tool calls
	toolCalls map[int]*deltaToolCall
	// 累积的文本内容
	content stringsBuilder
	// 是否检测到工具调用
	hasToolCalls bool
	// 当前模型
	model string
	// 是否完成
	done bool
}

type stringsBuilder struct {
	data []byte
}

func (sb *stringsBuilder) WriteString(s string) {
	sb.data = append(sb.data, []byte(s)...)
}

func (sb *stringsBuilder) String() string {
	return string(sb.data)
}

// NewStreamParser 创建流式解析器
func NewStreamParser() *StreamParser {
	return &StreamParser{
		toolCalls: make(map[int]*deltaToolCall),
	}
}

// Feed 向解析器输入一个 SSE chunk
// 返回 true 表示需要继续，false 表示流结束
func (sp *StreamParser) Feed(chunk StreamChunk) bool {
	sp.model = chunk.Model

	if chunk.Done {
		sp.done = true
		return false
	}

	return true
}

// FeedDelta 处理一个 OpenAI delta chunk
// 返回 true 表示需要继续
func (sp *StreamParser) FeedDelta(delta *openaiDelta) bool {
	// 累积文本内容
	if delta.Content != "" {
		sp.content.WriteString(delta.Content)
	}

	// 累积工具调用
	if len(delta.ToolCalls) > 0 {
		sp.hasToolCalls = true
		for _, dtc := range delta.ToolCalls {
			existing, ok := sp.toolCalls[dtc.Index]
			if !ok {
				// 新的工具调用
				sp.toolCalls[dtc.Index] = &deltaToolCall{
					Index: dtc.Index,
					ID:    dtc.ID,
					Type:  dtc.Type,
				}
				if dtc.Function.Name != "" {
					sp.toolCalls[dtc.Index].Function.Name = dtc.Function.Name
				}
				if dtc.Function.Arguments != "" {
					sp.toolCalls[dtc.Index].Function.Arguments = dtc.Function.Arguments
				}
			} else {
				// 追加到已有的工具调用
				if dtc.ID != "" {
					existing.ID = dtc.ID
				}
				if dtc.Type != "" {
					existing.Type = dtc.Type
				}
				if dtc.Function.Name != "" {
					existing.Function.Name += dtc.Function.Name
				}
				if dtc.Function.Arguments != "" {
					existing.Function.Arguments += dtc.Function.Arguments
				}
			}
		}
	}

	return true
}

// GetContent 返回累积的文本内容
func (sp *StreamParser) GetContent() string {
	return sp.content.String()
}

// GetToolCalls 返回解析出的工具调用列表
func (sp *StreamParser) GetToolCalls() []ToolCall {
	if !sp.hasToolCalls {
		return nil
	}

	calls := make([]ToolCall, 0, len(sp.toolCalls))
	for i := 0; i < len(sp.toolCalls); i++ {
		if dtc, ok := sp.toolCalls[i]; ok {
			id := dtc.ID
			// v0.55.1: 如果 ID 为空，生成唯一 call_id
			if id == "" {
				id = GenerateCallID()
			}
			calls = append(calls, ToolCall{
				ID:        id,
				Name:      dtc.Function.Name,
				Arguments: dtc.Function.Arguments,
			})
		}
	}
	return calls
}

// HasToolCalls 是否检测到工具调用
func (sp *StreamParser) HasToolCalls() bool {
	return sp.hasToolCalls
}

// IsDone 流是否结束
func (sp *StreamParser) IsDone() bool {
	return sp.done
}

// GetModel 返回模型名
func (sp *StreamParser) GetModel() string {
	return sp.model
}

// BuildResponse 从解析结果构建 Response
func (sp *StreamParser) BuildResponse() *Response {
	resp := &Response{
		Content:   sp.GetContent(),
		Model:     sp.model,
		ToolCalls: sp.GetToolCalls(),
	}
	if sp.done {
		resp.FinishReason = "stop"
	}
	if sp.hasToolCalls {
		resp.FinishReason = "tool_calls"
	}
	return resp
}
