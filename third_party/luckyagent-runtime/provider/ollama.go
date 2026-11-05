package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// --- Ollama Provider ---

// OllamaProvider 实现本地 Ollama API 调用
type OllamaProvider struct {
	cfg Config
}

func NewOllamaProvider(cfg Config) Provider {
	if cfg.LlmProvider.BaseURL == "" {
		cfg.LlmProvider.BaseURL = "http://localhost:11434"
	}
	if cfg.LlmProvider.Model == "" {
		cfg.LlmProvider.Model = "llama3"
	}
	return &OllamaProvider{cfg: cfg}
}

func (p *OllamaProvider) Name() string { return "ollama" }

func (p *OllamaProvider) Validate() error {
	// Ollama 本地运行，不需要 API key
	// 尝试连接验证
	resp, err := http.Get(p.cfg.LlmProvider.BaseURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("ollama: cannot connect to %s: %w", p.cfg.LlmProvider.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// ollamaChatRequest 是 Ollama chat API 的请求体
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

// ollamaChatResponse 是 Ollama chat API 的响应体
type ollamaChatResponse struct {
	Model           string        `json:"model"`
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	TotalDuration   int64         `json:"total_duration,omitempty"`
	EvalCount       int           `json:"eval_count,omitempty"`
	PromptEvalCount int           `json:"prompt_eval_count,omitempty"`
}

func (p *OllamaProvider) Chat(ctx context.Context, messages []Message) (*Response, error) {
	apiMsgs := toOllamaMessages(messages)

	reqBody := ollamaChatRequest{
		Model:    p.cfg.LlmProvider.Model,
		Messages: apiMsgs,
		Stream:   false,
	}
	if p.cfg.LlmProvider.Temperature > 0 || p.cfg.Limits.MaxTokens > 0 {
		reqBody.Options = &ollamaOptions{
			Temperature: p.cfg.LlmProvider.Temperature,
			NumPredict:  p.cfg.Limits.MaxTokens,
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", p.cfg.LlmProvider.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: API error %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("ollama: parse response: %w", err)
	}

	result := &Response{
		Content:      chatResp.Message.Content,
		Model:        chatResp.Model,
		FinishReason: "stop",
		TokensUsed:   chatResp.PromptEvalCount + chatResp.EvalCount,
	}

	return result, nil
}

func (p *OllamaProvider) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	apiMsgs := toOllamaMessages(messages)

	reqBody := ollamaChatRequest{
		Model:    p.cfg.LlmProvider.Model,
		Messages: apiMsgs,
		Stream:   true,
	}
	if p.cfg.LlmProvider.Temperature > 0 || p.cfg.Limits.MaxTokens > 0 {
		reqBody.Options = &ollamaOptions{
			Temperature: p.cfg.LlmProvider.Temperature,
			NumPredict:  p.cfg.Limits.MaxTokens,
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", p.cfg.LlmProvider.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("ollama: API error %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		// Ollama 流式: 每行一个 JSON 对象 (NDJSON)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var chatResp ollamaChatResponse
			if err := json.Unmarshal([]byte(line), &chatResp); err != nil {
				continue
			}

			if chatResp.Message.Content != "" {
				ch <- StreamChunk{
					Content: chatResp.Message.Content,
					Model:   chatResp.Model,
				}
			}

			if chatResp.Done {
				ch <- StreamChunk{Done: true, FinishReason: "stop", Model: chatResp.Model}
				return
			}
		}
	}()

	return ch, nil
}

// toOllamaMessages 将通用 Message 转换为 Ollama 格式
func toOllamaMessages(messages []Message) []ollamaMessage {
	result := make([]ollamaMessage, 0, len(messages))
	for _, m := range messages {
		result = append(result, ollamaMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return result
}

// Ensure OllamaProvider implements Provider
var _ Provider = (*OllamaProvider)(nil)
