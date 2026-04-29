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
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
	System    string             `json:"system,omitempty"`
	Stream    bool               `json:"stream"`
}

// anthropicMessage 是 Anthropic API 的消息格式
type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse 是 Anthropic API 的响应体
type anthropicResponse struct {
	ID         string           `json:"id"`
	Type       string           `json:"type"`
	Role       string           `json:"role"`
	Content    []anthropicBlock `json:"content"`
	Model      string           `json:"model"`
	Usage      anthropicUsage   `json:"usage"`
	StopReason string           `json:"stop_reason"`
}

type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicStreamEvent 是 Anthropic SSE 事件
type anthropicStreamEvent struct {
	Type         string             `json:"type"`
	Index        int                `json:"index,omitempty"`
	ContentBlock *anthropicBlock    `json:"content_block,omitempty"`
	Delta        *anthropicDelta    `json:"delta,omitempty"`
	Message      *anthropicResponse `json:"message,omitempty"`
}

type anthropicDelta struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

func (p *AnthropicProvider) Chat(ctx context.Context, messages []Message) (*Response, error) {
	// 分离 system 消息
	var systemPrompt string
	var apiMsgs []anthropicMessage
	for _, m := range messages {
		if m.Role == "system" {
			systemPrompt += m.Content + "\n"
			continue
		}
		apiMsgs = append(apiMsgs, anthropicMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	reqBody := anthropicRequest{
		Model:     p.cfg.LlmProvider.Model,
		MaxTokens: p.cfg.Limits.MaxTokens,
		Messages:  apiMsgs,
		System:    strings.TrimSpace(systemPrompt),
		Stream:    false,
	}
	if reqBody.MaxTokens == 0 {
		reqBody.MaxTokens = 4096
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", p.cfg.LlmProvider.BaseURL+"/v1/messages", bytes.NewReader(body))
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
		FinishReason: chatResp.StopReason,
		TokensUsed:   chatResp.Usage.InputTokens + chatResp.Usage.OutputTokens,
	}

	// 提取文本内容
	for _, block := range chatResp.Content {
		if block.Type == "text" {
			result.Content += block.Text
		}
	}

	return result, nil
}

func (p *AnthropicProvider) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	var systemPrompt string
	var apiMsgs []anthropicMessage
	for _, m := range messages {
		if m.Role == "system" {
			systemPrompt += m.Content + "\n"
			continue
		}
		apiMsgs = append(apiMsgs, anthropicMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	reqBody := anthropicRequest{
		Model:     p.cfg.LlmProvider.Model,
		MaxTokens: p.cfg.Limits.MaxTokens,
		Messages:  apiMsgs,
		System:    strings.TrimSpace(systemPrompt),
		Stream:    true,
	}
	if reqBody.MaxTokens == 0 {
		reqBody.MaxTokens = 4096
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", p.cfg.LlmProvider.BaseURL+"/v1/messages", bytes.NewReader(body))
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
			case "content_block_delta":
				if event.Delta != nil && event.Delta.Text != "" {
					ch <- StreamChunk{
						Content: event.Delta.Text,
						Model:   p.cfg.LlmProvider.Model,
					}
				}
			case "message_stop":
				ch <- StreamChunk{Done: true, FinishReason: "stop", Model: p.cfg.LlmProvider.Model}
				return
			case "message_delta":
				if event.Delta != nil && event.Delta.StopReason != "" {
					ch <- StreamChunk{Done: true, FinishReason: event.Delta.StopReason, Model: p.cfg.LlmProvider.Model}
					return
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

// Ensure AnthropicProvider implements Provider
var _ Provider = (*AnthropicProvider)(nil)
