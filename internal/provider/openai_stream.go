package provider

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
)

var jsonAPI = jsoniter.ConfigCompatibleWithStandardLibrary

const defaultOpenAIUserAgent = "luckyharness"

func maskedKeySuffix(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "..."
}

// openaiChatRequest 是发送给 OpenAI API 的请求体
type openaiChatRequest struct {
	Model               string          `json:"model"`
	Messages            []openaiMessage `json:"messages"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         float64         `json:"temperature,omitempty"`
	Stream              bool            `json:"stream"`
	Tools               []openaiTool    `json:"tools,omitempty"`
	ToolChoice          any             `json:"tool_choice,omitempty"`
}

// openaiMessage 是 OpenAI API 的消息格式
type openaiMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content,omitempty"`
	ToolCalls  []openaiToolCallResp `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"` // v0.16.0: tool 消息的 call ID
	Name       string               `json:"name,omitempty"`         // v0.16.0: tool 消息的函数名
}

// openaiToolCallResp 是 OpenAI 响应中的工具调用格式
type openaiToolCallResp struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openaiTool 是 OpenAI function calling 的工具定义
type openaiTool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

// toolFunction 是工具的函数定义
type toolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// openaiChatResponse 是 OpenAI API 的响应体
type openaiChatResponse struct {
	ID      string         `json:"id"`
	Choices []openaiChoice `json:"choices"`
	Usage   *openaiUsage   `json:"usage,omitempty"`
}

type openaiChoice struct {
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
	Delta        *openaiDelta  `json:"delta,omitempty"`
	FinishReason string        `json:"finish_reason"`
}

type openaiDelta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []deltaToolCall `json:"tool_calls,omitempty"`
}

type deltaToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// openaiSSEEvent 是 SSE 事件
type openaiSSEEvent struct {
	Data string
}

// openAIHTTPClient 使用独立 transport，避免 http.DefaultTransport 在某些代理链路上复用连接导致 TLS 记录损坏。
var openAIHTTPClient = &http.Client{
	Transport: newOpenAITransport(),
}

func newOpenAITransport() http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{
			ForceAttemptHTTP2: true,
		}
	}
	t := base.Clone()
	// 保持 HTTP/2 可用：部分 OpenAI-compatible 网关在强制 HTTP/1.1 时会直接返回 HTTP/2 帧，
	// 触发 malformed HTTP response。连接稳定性问题由 doOpenAIRequest 的重试与断开空闲连接兜底。
	t.ForceAttemptHTTP2 = true
	if t.MaxIdleConnsPerHost < 8 {
		t.MaxIdleConnsPerHost = 8
	}
	return t
}

type openAIRetrySettings struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

func resolveOpenAIRetrySettings(cfg Config) openAIRetrySettings {
	// 默认对传输层抖动做轻量兜底重试；如果显式启用 retry，则使用配置覆盖。
	out := openAIRetrySettings{
		MaxAttempts:  2,
		InitialDelay: 300 * time.Millisecond,
		MaxDelay:     2 * time.Second,
	}

	if cfg.Retry.Enabled {
		if cfg.Retry.MaxAttempts > 0 {
			out.MaxAttempts = cfg.Retry.MaxAttempts
		}
		if cfg.Retry.InitialDelayMs > 0 {
			out.InitialDelay = time.Duration(cfg.Retry.InitialDelayMs) * time.Millisecond
		}
		if cfg.Retry.MaxDelayMs > 0 {
			out.MaxDelay = time.Duration(cfg.Retry.MaxDelayMs) * time.Millisecond
		}
	}

	if out.MaxAttempts < 1 {
		out.MaxAttempts = 1
	}
	if out.InitialDelay <= 0 {
		out.InitialDelay = 300 * time.Millisecond
	}
	if out.MaxDelay < out.InitialDelay {
		out.MaxDelay = out.InitialDelay
	}

	return out
}

func shouldRetryTransportError(err error, cfg Config) bool {
	if err == nil {
		return false
	}
	// 外层 context 被取消/超时，通常代表调用方主动结束，不应继续重试。
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		// 如果配置了 retry 规则，尊重 retry_on_timeout；否则默认对网络超时重试。
		if cfg.Retry.Enabled {
			return cfg.Retry.RetryOnTimeout
		}
		return true
	}

	s := strings.ToLower(err.Error())
	if strings.Contains(s, "tls: bad record mac") {
		return true
	}
	if strings.Contains(s, "local error: tls") {
		return true
	}
	if strings.Contains(s, "connection reset") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "http2: client connection lost") ||
		strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "unexpected eof") {
		return true
	}
	if strings.Contains(s, "timeout") {
		if cfg.Retry.Enabled {
			return cfg.Retry.RetryOnTimeout
		}
		return true
	}
	return false
}

func retryDelay(settings openAIRetrySettings, attempt int) time.Duration {
	delay := settings.InitialDelay
	// attempt=1 对应第一次重试延迟；attempt>1 指数退避。
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= settings.MaxDelay {
			return settings.MaxDelay
		}
	}
	if delay > settings.MaxDelay {
		return settings.MaxDelay
	}
	return delay
}

func applyOpenAIRequestHeaders(req *http.Request, apiKey string, extraHeaders map[string]string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", defaultOpenAIUserAgent)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
}

func doOpenAIRequest(ctx context.Context, cfg Config, body []byte) (*http.Response, error) {
	settings := resolveOpenAIRetrySettings(cfg)
	url := strings.TrimRight(cfg.LlmProvider.BaseURL, "/") + "/chat/completions"
	log.Printf("[provider] openai request: model=%s url=%s stream_retry_attempts=%d api_key=%s", cfg.LlmProvider.Model, url, settings.MaxAttempts, maskedKeySuffix(cfg.LlmProvider.APIKey))

	var lastErr error
	for attempt := 1; attempt <= settings.MaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		applyOpenAIRequestHeaders(req, cfg.LlmProvider.APIKey, cfg.ExtraHeaders)

		// 重试时强制新连接，避免复用潜在损坏的 TLS/HTTP2 长连接。
		if attempt > 1 {
			req.Close = true
			if tr, ok := openAIHTTPClient.Transport.(*http.Transport); ok {
				tr.CloseIdleConnections()
			}
		}

		resp, err := openAIHTTPClient.Do(req)
		if err == nil {
			log.Printf("[provider] openai response: model=%s url=%s status=%d", cfg.LlmProvider.Model, url, resp.StatusCode)
			return resp, nil
		}

		lastErr = err
		log.Printf("[provider] openai transport error: model=%s url=%s attempt=%d err=%v", cfg.LlmProvider.Model, url, attempt, err)
		if attempt == settings.MaxAttempts || !shouldRetryTransportError(err, cfg) {
			return nil, fmt.Errorf("send request: %w", err)
		}

		delay := retryDelay(settings, attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, fmt.Errorf("send request: %w", ctx.Err())
		case <-timer.C:
		}
	}

	return nil, fmt.Errorf("send request: %w", lastErr)
}

// callOpenAI 执行 OpenAI API 调用（非流式）
// 支持文本响应和工具调用解析
func callOpenAI(ctx context.Context, cfg Config, messages []Message, opts CallOptions) (*Response, error) {
	normalizedMessages := normalizeToolProtocolMessages(messages)
	if len(normalizedMessages) != len(messages) {
		log.Printf("[provider] normalized tool protocol messages: before=%d after=%d", len(messages), len(normalizedMessages))
	}

	// 部分模型/网关组合（如 gpt-5.4-mini）非流式会返回空 content 但计费已发生。
	// 这类模型优先走 stream 聚合，避免一次非流式空响应 + 二次 stream 重试。
	if shouldPreferStreamFirst(cfg.LlmProvider.Model) {
		streamResult, err := retryWithStream(ctx, cfg, normalizedMessages, opts)
		if err == nil && streamResult != nil && (streamResult.Content != "" || len(streamResult.ToolCalls) > 0) {
			log.Printf("[provider] stream-first OK (model=%s): content_len=%d, tool_calls=%d", cfg.LlmProvider.Model, len(streamResult.Content), len(streamResult.ToolCalls))
			return streamResult, nil
		}
		if err != nil {
			log.Printf("[provider] stream-first failed, fallback to non-stream (model=%s): %v", cfg.LlmProvider.Model, err)
		} else {
			log.Printf("[provider] stream-first empty, fallback to non-stream (model=%s)", cfg.LlmProvider.Model)
		}
	}

	reqBody := openaiChatRequest{
		Model:               cfg.LlmProvider.Model,
		Messages:            toOpenAIMessages(normalizedMessages),
		MaxTokens:           cfg.Limits.MaxTokens,
		MaxCompletionTokens: cfg.Limits.MaxTokens,
		Temperature:         cfg.LlmProvider.Temperature,
		Stream:              false,
	}

	// v0.16.0: 添加 function calling 工具定义
	if len(opts.Tools) > 0 {
		tools := make([]openaiTool, 0, len(opts.Tools))
		for _, t := range opts.Tools {
			fn, _ := t["function"].(map[string]any)
			tools = append(tools, openaiTool{
				Type:     "function",
				Function: newToolFunction(fn),
			})
		}
		reqBody.Tools = tools
		if opts.ToolChoice != nil {
			reqBody.ToolChoice = opts.ToolChoice
		} else {
			reqBody.ToolChoice = "auto"
		}
	}

	body, err := jsonAPI.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	capture := newUpstreamCapture("chat_completions_non_stream", cfg, body)

	resp, err := doOpenAIRequest(ctx, cfg, body)
	if err != nil {
		capture.writeError("do_request", err)
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		capture.writeError("read_response", err)
		return nil, fmt.Errorf("read response: %w", err)
	}
	capture.writeResponseMeta(resp.StatusCode, resp.Header)
	capture.writeResponseBody(respBody)

	if resp.StatusCode != http.StatusOK {
		log.Printf("[provider] openai non-200: model=%s url=%s status=%d body=%s", cfg.LlmProvider.Model, strings.TrimRight(cfg.LlmProvider.BaseURL, "/")+"/chat/completions", resp.StatusCode, strings.TrimSpace(string(respBody)))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp openaiChatResponse
	if err := jsonAPI.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := chatResp.Choices[0]
	result := &Response{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Model:        cfg.LlmProvider.Model,
	}
	if chatResp.Usage != nil {
		result.TokensUsed = chatResp.Usage.TotalTokens
	}

	// 解析工具调用
	if len(choice.Message.ToolCalls) > 0 {
		result.ToolCalls = make([]ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			id := tc.ID
			// v0.55.1: 如果 API 返回空 ID，生成唯一 call_id
			if id == "" {
				id = GenerateCallID()
			}
			result.ToolCalls[i] = ToolCall{
				ID:        id,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			}
		}
	}

	// v0.55.0: 非流式返回空 content 但有 completion_tokens 时，
	// 用流式重试（某些 API 代理如 api.boaiak.com 的 gpt-5.4-mini 非流式不返回 content）
	hasUsage := chatResp.Usage != nil && chatResp.Usage.CompletionTokens > 0
	if result.Content == "" && len(result.ToolCalls) == 0 && hasUsage {
		log.Printf("[provider] non-stream empty content with %d completion_tokens, retrying stream (model=%s)", chatResp.Usage.CompletionTokens, cfg.LlmProvider.APIKey)
		streamResult, err := retryWithStream(ctx, cfg, messages, opts)
		if err == nil && streamResult != nil && (streamResult.Content != "" || len(streamResult.ToolCalls) > 0) {
			log.Printf("[provider] stream retry OK: content_len=%d, tool_calls=%d", len(streamResult.Content), len(streamResult.ToolCalls))
			return streamResult, nil
		}
		if err != nil {
			log.Printf("[provider] stream retry failed: %v", err)
		} else {
			log.Printf("[provider] stream retry also empty: content_len=%d", len(streamResult.Content))
		}
	}

	return result, nil
}

func shouldPreferStreamFirst(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	// 当前实测最常见问题模型，后续可扩展为配置化策略。
	if strings.Contains(m, "gpt-5.4-mini") {
		return true
	}
	return false
}

// retryWithStream 非流式返回空 content 时，用流式重试获取完整响应
func retryWithStream(ctx context.Context, cfg Config, messages []Message, opts CallOptions) (*Response, error) {
	ch, err := callOpenAIStream(ctx, cfg, messages, opts)
	if err != nil {
		return nil, err
	}

	var content strings.Builder
	var toolCalls []ToolCall
	toolCallAcc := make(map[int]*deltaToolCall)

	for chunk := range ch {
		if chunk.Content != "" {
			content.WriteString(chunk.Content)
		}
		if len(chunk.ToolCallDeltas) > 0 {
			for _, dtc := range chunk.ToolCallDeltas {
				existing, ok := toolCallAcc[dtc.Index]
				if !ok {
					toolCallAcc[dtc.Index] = &deltaToolCall{
						Index: dtc.Index,
						ID:    dtc.ID,
						Type:  "function",
					}
					if dtc.Name != "" {
						toolCallAcc[dtc.Index].Function.Name = dtc.Name
					}
					if dtc.Arguments != "" {
						toolCallAcc[dtc.Index].Function.Arguments = dtc.Arguments
					}
				} else {
					if dtc.ID != "" {
						existing.ID = dtc.ID
					}
					if dtc.Name != "" {
						existing.Function.Name += dtc.Name
					}
					if dtc.Arguments != "" {
						existing.Function.Arguments += dtc.Arguments
					}
				}
			}
		}
		if chunk.Done {
			break
		}
	}

	// 组装 tool calls
	for i := 0; i < len(toolCallAcc); i++ {
		if tc, ok := toolCallAcc[i]; ok && tc.Function.Name != "" {
			id := tc.ID
			// v0.55.1: 如果流式响应中 ID 为空，生成唯一 call_id
			if id == "" {
				id = GenerateCallID()
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:        id,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	return &Response{
		Content:   content.String(),
		ToolCalls: toolCalls,
		Model:     cfg.LlmProvider.Model,
	}, nil
}

// callOpenAIStream 执行 OpenAI API 流式调用
// 支持文本内容和工具调用的流式解析
func callOpenAIStream(ctx context.Context, cfg Config, messages []Message, opts CallOptions) (<-chan StreamChunk, error) {
	normalizedMessages := normalizeToolProtocolMessages(messages)
	if len(normalizedMessages) != len(messages) {
		log.Printf("[provider] normalized tool protocol messages (stream): before=%d after=%d", len(messages), len(normalizedMessages))
	}

	reqBody := openaiChatRequest{
		Model:               cfg.LlmProvider.Model,
		Messages:            toOpenAIMessages(normalizedMessages),
		MaxTokens:           cfg.Limits.MaxTokens,
		MaxCompletionTokens: cfg.Limits.MaxTokens,
		Temperature:         cfg.LlmProvider.Temperature,
		Stream:              true,
	}

	// v0.16.0: 添加 function calling 工具定义
	if len(opts.Tools) > 0 {
		tools := make([]openaiTool, 0, len(opts.Tools))
		for _, t := range opts.Tools {
			fn, _ := t["function"].(map[string]any)
			tools = append(tools, openaiTool{
				Type:     "function",
				Function: newToolFunction(fn),
			})
		}
		reqBody.Tools = tools
		if opts.ToolChoice != nil {
			reqBody.ToolChoice = opts.ToolChoice
		} else {
			reqBody.ToolChoice = "auto"
		}
	}

	body, err := jsonAPI.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	capture := newUpstreamCapture("chat_completions_stream", cfg, body)

	resp, err := doOpenAIRequest(ctx, cfg, body)
	if err != nil {
		capture.writeError("do_request", err)
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		capture.writeResponseMeta(resp.StatusCode, resp.Header)
		capture.writeResponseBody(respBody)
		log.Printf("[provider] openai stream non-200: model=%s url=%s status=%d body=%s", cfg.LlmProvider.Model, strings.TrimRight(cfg.LlmProvider.BaseURL, "/")+"/chat/completions", resp.StatusCode, strings.TrimSpace(string(respBody)))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}
	capture.writeResponseMeta(resp.StatusCode, resp.Header)

	ch := make(chan StreamChunk, 128)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		bodyReader := io.Reader(resp.Body)
		var captureFile *os.File
		if capture != nil && capture.enabled {
			f, fileErr := os.OpenFile(capture.prefix+".response.sse.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if fileErr != nil {
				capture.writeError("open_sse_capture", fileErr)
			} else {
				captureFile = f
				bodyReader = io.TeeReader(resp.Body, f)
			}
		}
		if captureFile != nil {
			defer captureFile.Close()
		}

		scanner := bufio.NewScanner(bodyReader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		lastFinishReason := ""
		for scanner.Scan() {
			line := scanner.Text()

			// SSE 格式: "data: {...}"
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			// 流结束标记
			if data == "[DONE]" {
				ch <- StreamChunk{Done: true, FinishReason: lastFinishReason, Model: cfg.LlmProvider.Model}
				return
			}

			var chatResp openaiChatResponse
			if err := jsonAPI.Unmarshal([]byte(data), &chatResp); err != nil {
				continue
			}

			if len(chatResp.Choices) == 0 {
				continue
			}

			choice := chatResp.Choices[0]
			if choice.FinishReason != "" {
				lastFinishReason = choice.FinishReason
			}

			// 处理文本内容
			if choice.Delta != nil && choice.Delta.Content != "" {
				ch <- StreamChunk{
					Content: choice.Delta.Content,
					Model:   cfg.LlmProvider.Model,
				}
			}

			// 处理工具调用（流式增量）— v0.40.0: 结构化传递
			if choice.Delta != nil && len(choice.Delta.ToolCalls) > 0 {
				deltas := make([]StreamToolCallDelta, 0, len(choice.Delta.ToolCalls))
				for _, dtc := range choice.Delta.ToolCalls {
					deltas = append(deltas, StreamToolCallDelta{
						Index:     dtc.Index,
						ID:        dtc.ID,
						Name:      dtc.Function.Name,
						Arguments: dtc.Function.Arguments,
					})
				}
				ch <- StreamChunk{
					ToolCallDeltas: deltas,
					Model:          cfg.LlmProvider.Model,
				}
			}

			if choice.FinishReason == "stop" || choice.FinishReason == "length" {
				ch <- StreamChunk{Done: true, FinishReason: choice.FinishReason, Model: cfg.LlmProvider.Model}
				return
			}

			// 工具调用完成
			if choice.FinishReason == "tool_calls" {
				ch <- StreamChunk{Done: true, FinishReason: choice.FinishReason, Model: cfg.LlmProvider.Model}
				return
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			capture.writeError("scan_sse", scanErr)
		}
	}()

	return ch, nil
}

// toOpenAIMessages 将通用 Message 转换为 OpenAI 格式
func toOpenAIMessages(messages []Message) []openaiMessage {
	result := make([]openaiMessage, 0, len(messages))
	for _, m := range messages {
		// 兼容旧会话：tool 消息缺失 tool_call_id 时，不能以 role=tool 发送。
		// 否则部分 API 网关会返回 400（Invalid input[*].call_id）。
		if m.Role == "tool" && strings.TrimSpace(m.ToolCallID) == "" {
			content := strings.TrimSpace(m.Content)
			if content == "" {
				continue
			}
			if m.Name != "" && !strings.HasPrefix(content, "[Tool:") {
				content = fmt.Sprintf("[Tool: %s] %s", m.Name, content)
			}
			result = append(result, openaiMessage{
				Role:    "assistant",
				Content: content,
			})
			continue
		}

		msg := openaiMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		// v0.16.0: 处理 tool_calls（assistant 消息）
		if len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]openaiToolCallResp, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				msg.ToolCalls[i] = openaiToolCallResp{
					ID:   tc.ID,
					Type: "function",
				}
				msg.ToolCalls[i].Function.Name = tc.Name
				msg.ToolCalls[i].Function.Arguments = tc.Arguments
			}
		}
		result = append(result, msg)
	}
	return result
}

// toolFunction 从 map 创建 toolFunction 结构
func newToolFunction(fn map[string]any) toolFunction {
	tf := toolFunction{}
	if name, ok := fn["name"].(string); ok {
		tf.Name = name
	}
	if desc, ok := fn["description"].(string); ok {
		tf.Description = desc
	}
	if params, ok := fn["parameters"].(map[string]any); ok {
		tf.Parameters = params
	}
	return tf
}
