package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// --- Anthropic Provider ---

// AnthropicProvider 实现 Claude API 调用
type AnthropicProvider struct {
	cfg Config
}

func NewAnthropicProvider(cfg Config) Provider {
	if cfg.LlmProvider.BaseURL == "" {
		cfg.LlmProvider.BaseURL = "https://api.anthropic.com"
	}
	if cfg.LlmProvider.Model == "" {
		cfg.LlmProvider.Model = "claude-sonnet-4-20250514"
	}
	return &AnthropicProvider{cfg: cfg}
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) Validate() error {
	if p.cfg.LlmProvider.APIKey == "" {
		return fmt.Errorf("anthropic: api_key is required")
	}
	return nil
}

// anthropicRequest 是 Anthropic Messages API 的请求体
type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	Temperature float64            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	ToolChoice  any                `json:"tool_choice,omitempty"`
}

// anthropicMessage 是 Anthropic API 的消息格式
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	Source    any    `json:"source,omitempty"`
}

// anthropicResponse 是 Anthropic API 的响应体
type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	Model      string                  `json:"model"`
	Usage      anthropicUsage          `json:"usage"`
	StopReason string                  `json:"stop_reason"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicStreamEvent 是 Anthropic SSE 事件
type anthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Delta        *anthropicDelta        `json:"delta,omitempty"`
	Message      *anthropicResponse     `json:"message,omitempty"`
	Usage        *anthropicUsage        `json:"usage,omitempty"`
}

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

func (p *AnthropicProvider) Chat(ctx context.Context, messages []Message) (*Response, error) {
	return p.ChatWithOptions(ctx, messages, CallOptions{})
}

func (p *AnthropicProvider) ChatWithOptions(ctx context.Context, messages []Message, opts CallOptions) (*Response, error) {
	reqBody, err := buildAnthropicRequest(p.cfg, messages, opts, false)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, anthropicMessagesEndpoint(p.cfg.LlmProvider.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.cfg.LlmProvider.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: API error %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp anthropicResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("anthropic: parse response: %w", err)
	}

	result := &Response{
		Model:        chatResp.Model,
		FinishReason: normalizeAnthropicFinishReason(chatResp.StopReason),
		TokensUsed:   chatResp.Usage.InputTokens + chatResp.Usage.OutputTokens,
		Usage:        convertAnthropicUsage(&chatResp.Usage),
	}

	// 提取文本内容和 Anthropic tool_use，转回 LuckyHarness 的 OpenAI-style ToolCall。
	for _, block := range chatResp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			id := strings.TrimSpace(block.ID)
			if id == "" {
				id = GenerateCallID()
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        id,
				Name:      block.Name,
				Arguments: anthropicInputToArguments(block.Input),
			})
		}
	}

	return result, nil
}

func (p *AnthropicProvider) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	return p.ChatStreamWithOptions(ctx, messages, CallOptions{})
}

func (p *AnthropicProvider) ChatStreamWithOptions(ctx context.Context, messages []Message, opts CallOptions) (<-chan StreamChunk, error) {
	reqBody, err := buildAnthropicRequest(p.cfg, messages, opts, true)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, anthropicMessagesEndpoint(p.cfg.LlmProvider.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.cfg.LlmProvider.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic: API error %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		lastFinishReason := ""
		for scanner.Scan() {
			line := scanner.Text()

			// Anthropic SSE: "event: xxx" then "data: {...}"
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			var event anthropicStreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			switch event.Type {
			case "message_start":
				if event.Message != nil {
					if usage := convertAnthropicUsage(&event.Message.Usage); usage != nil {
						ch <- StreamChunk{Model: p.cfg.LlmProvider.Model, Usage: usage}
					}
				}
			case "content_block_start":
				if event.ContentBlock == nil {
					continue
				}
				switch event.ContentBlock.Type {
				case "text":
					if event.ContentBlock.Text != "" {
						ch <- StreamChunk{Content: event.ContentBlock.Text, Model: p.cfg.LlmProvider.Model}
					}
				case "tool_use":
					id := strings.TrimSpace(event.ContentBlock.ID)
					if id == "" {
						id = GenerateCallID()
					}
					ch <- StreamChunk{
						ToolCallDeltas: []StreamToolCallDelta{{
							Index: event.Index,
							ID:    id,
							Name:  event.ContentBlock.Name,
						}},
						Model: p.cfg.LlmProvider.Model,
					}
				}
			case "content_block_delta":
				if event.Delta == nil {
					continue
				}
				if event.Delta.Text != "" {
					ch <- StreamChunk{
						Content: event.Delta.Text,
						Model:   p.cfg.LlmProvider.Model,
					}
				}
				if event.Delta.PartialJSON != "" {
					ch <- StreamChunk{
						ToolCallDeltas: []StreamToolCallDelta{{
							Index:     event.Index,
							Arguments: event.Delta.PartialJSON,
						}},
						Model: p.cfg.LlmProvider.Model,
					}
				}
			case "message_stop":
				if lastFinishReason == "" {
					lastFinishReason = "stop"
				}
				ch <- StreamChunk{Done: true, FinishReason: lastFinishReason, Model: p.cfg.LlmProvider.Model}
				return
			case "message_delta":
				if usage := convertAnthropicUsage(event.Usage); usage != nil {
					ch <- StreamChunk{Model: p.cfg.LlmProvider.Model, Usage: usage}
				}
				if event.Delta != nil && event.Delta.StopReason != "" {
					lastFinishReason = normalizeAnthropicFinishReason(event.Delta.StopReason)
				}
			case "error":
				// Anthropic error event
				ch <- StreamChunk{Done: true, FinishReason: "error", Model: p.cfg.LlmProvider.Model}
				return
			}
		}
	}()

	return ch, nil
}

func buildAnthropicRequest(cfg Config, messages []Message, opts CallOptions, stream bool) (anthropicRequest, error) {
	normalizedMessages := normalizeToolProtocolMessages(messages)
	systemPrompt, apiMessages, err := toAnthropicMessages(normalizedMessages)
	if err != nil {
		return anthropicRequest{}, err
	}

	reqBody := anthropicRequest{
		Model:       cfg.LlmProvider.Model,
		MaxTokens:   cfg.Limits.MaxTokens,
		Messages:    apiMessages,
		System:      systemPrompt,
		Temperature: cfg.LlmProvider.Temperature,
		Stream:      stream,
	}
	if reqBody.MaxTokens == 0 {
		reqBody.MaxTokens = 4096
	}

	if len(opts.Tools) > 0 && !anthropicToolChoiceDisablesTools(opts.ToolChoice) {
		reqBody.Tools = toAnthropicTools(opts.Tools)
		if len(reqBody.Tools) > 0 {
			if choice := toAnthropicToolChoice(opts.ToolChoice); choice != nil {
				reqBody.ToolChoice = choice
			}
		}
	}

	return reqBody, nil
}

func toAnthropicMessages(messages []Message) (string, []anthropicMessage, error) {
	var systemPrompt strings.Builder
	result := make([]anthropicMessage, 0, len(messages))

	for _, m := range messages {
		switch strings.TrimSpace(m.Role) {
		case "system":
			content := strings.TrimSpace(m.Content)
			if content != "" {
				if systemPrompt.Len() > 0 {
					systemPrompt.WriteString("\n")
				}
				systemPrompt.WriteString(content)
			}
		case "assistant":
			content, err := toAnthropicAssistantContent(m)
			if err != nil {
				return "", nil, err
			}
			result = append(result, anthropicMessage{Role: "assistant", Content: content})
		case "tool":
			toolUseID := strings.TrimSpace(m.ToolCallID)
			if toolUseID == "" {
				continue
			}
			result = append(result, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{{
					Type:      "tool_result",
					ToolUseID: toolUseID,
					Content:   m.Content,
				}},
			})
		case "user":
			content, err := toAnthropicUserContent(m)
			if err != nil {
				return "", nil, err
			}
			result = append(result, anthropicMessage{Role: "user", Content: content})
		default:
			content := strings.TrimSpace(m.Content)
			if content == "" {
				continue
			}
			result = append(result, anthropicMessage{Role: "user", Content: content})
		}
	}

	return strings.TrimSpace(systemPrompt.String()), result, nil
}

func toAnthropicAssistantContent(m Message) (any, error) {
	if len(m.ToolCalls) == 0 {
		return m.Content, nil
	}

	blocks := make([]anthropicContentBlock, 0, len(m.ToolCalls)+1)
	if text := strings.TrimSpace(m.Content); text != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: text})
	}
	for _, tc := range m.ToolCalls {
		id := strings.TrimSpace(tc.ID)
		if id == "" {
			id = GenerateCallID()
		}
		blocks = append(blocks, anthropicContentBlock{
			Type:  "tool_use",
			ID:    id,
			Name:  tc.Name,
			Input: parseAnthropicToolInput(tc.Arguments),
		})
	}
	return blocks, nil
}

func toAnthropicUserContent(m Message) (any, error) {
	if len(m.ContentParts) == 0 {
		return m.Content, nil
	}

	parts := make([]anthropicContentBlock, 0, len(m.ContentParts))
	for _, part := range m.ContentParts {
		switch part.Type {
		case "text":
			text := strings.TrimSpace(part.Text)
			if text != "" {
				parts = append(parts, anthropicContentBlock{Type: "text", Text: text})
			}
		case "image":
			source, err := toAnthropicImageSource(part.Image)
			if err != nil {
				return nil, err
			}
			parts = append(parts, anthropicContentBlock{Type: "image", Source: source})
		default:
			return nil, fmt.Errorf("anthropic: unsupported content part type %q", part.Type)
		}
	}
	if len(parts) == 0 {
		return m.Content, nil
	}
	return parts, nil
}

func toAnthropicImageSource(img *ImagePart) (map[string]any, error) {
	imageURL, err := resolveOpenAIImageURL(img)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(imageURL, "data:") {
		header, data, ok := strings.Cut(imageURL, ",")
		if !ok {
			return nil, fmt.Errorf("anthropic: invalid data image URL")
		}
		mediaType := strings.TrimPrefix(header, "data:")
		mediaType = strings.TrimSuffix(mediaType, ";base64")
		if mediaType == "" {
			mediaType = "image/png"
		}
		return map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       data,
		}, nil
	}
	return map[string]any{
		"type": "url",
		"url":  imageURL,
	}, nil
}

func parseAnthropicToolInput(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return map[string]any{"arguments": raw}
	}
	if value == nil {
		return map[string]any{}
	}
	if obj, ok := value.(map[string]any); ok {
		return obj
	}
	return map[string]any{"value": value}
}

func anthropicInputToArguments(input any) string {
	if input == nil {
		return "{}"
	}
	data, err := json.Marshal(input)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return "{}"
	}
	return string(data)
}

func toAnthropicTools(tools []map[string]any) []anthropicTool {
	result := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		fn, _ := t["function"].(map[string]any)
		tf := newToolFunction(fn)
		if strings.TrimSpace(tf.Name) == "" {
			continue
		}
		schema := copyStringAnyMap(tf.Parameters)
		if len(schema) == 0 {
			schema = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		if _, ok := schema["type"]; !ok {
			schema["type"] = "object"
		}
		result = append(result, anthropicTool{
			Name:        tf.Name,
			Description: tf.Description,
			InputSchema: schema,
		})
	}
	return result
}

func copyStringAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func toAnthropicToolChoice(choice any) any {
	switch c := choice.(type) {
	case nil:
		return map[string]any{"type": "auto"}
	case string:
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "", "auto":
			return map[string]any{"type": "auto"}
		case "none":
			return nil
		case "required", "any":
			return map[string]any{"type": "any"}
		default:
			return map[string]any{"type": "auto"}
		}
	case map[string]any:
		typ, _ := c["type"].(string)
		switch strings.ToLower(strings.TrimSpace(typ)) {
		case "function":
			if fn, ok := c["function"].(map[string]any); ok {
				if name, ok := fn["name"].(string); ok && strings.TrimSpace(name) != "" {
					return map[string]any{"type": "tool", "name": strings.TrimSpace(name)}
				}
			}
		case "tool", "auto", "any":
			return c
		case "none":
			return nil
		}
	}
	return map[string]any{"type": "auto"}
}

func anthropicToolChoiceDisablesTools(choice any) bool {
	switch c := choice.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(c), "none")
	case map[string]any:
		typ, _ := c["type"].(string)
		return strings.EqualFold(strings.TrimSpace(typ), "none")
	default:
		return false
	}
}

func convertAnthropicUsage(usage *anthropicUsage) *UsageDetails {
	if usage == nil {
		return nil
	}
	total := usage.InputTokens + usage.OutputTokens
	if total == 0 {
		return nil
	}
	return &UsageDetails{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      total,
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
	}
}

func normalizeAnthropicFinishReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}

func anthropicMessagesEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

// Ensure AnthropicProvider implements Provider and FunctionCallingProvider.
var (
	_ Provider                = (*AnthropicProvider)(nil)
	_ FunctionCallingProvider = (*AnthropicProvider)(nil)
)
