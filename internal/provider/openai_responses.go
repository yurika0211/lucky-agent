package provider

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

const (
	openAIProtocolChatCompletions = "chat_completions"
	openAIProtocolResponses       = "responses"
)

func normalizedOpenAIProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "chat", "chat-completion", "chat-completions", "chat_completion", "chat_completions":
		return openAIProtocolChatCompletions
	case "response", "responses":
		return openAIProtocolResponses
	default:
		return strings.ToLower(strings.TrimSpace(protocol))
	}
}

func usesResponsesAPI(protocol string) bool {
	return normalizedOpenAIProtocol(protocol) == openAIProtocolResponses
}

func validateOpenAIProtocol(protocol string) error {
	switch normalizedOpenAIProtocol(protocol) {
	case openAIProtocolChatCompletions, openAIProtocolResponses:
		return nil
	default:
		return fmt.Errorf("unsupported OpenAI protocol %q: use chat_completions or responses", protocol)
	}
}

type responsesRequest struct {
	Model           string          `json:"model"`
	Input           []any           `json:"input"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Temperature     float64         `json:"temperature,omitempty"`
	Stream          bool            `json:"stream"`
	Tools           []responsesTool `json:"tools,omitempty"`
	ToolChoice      any             `json:"tool_choice,omitempty"`
}

type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type responsesAPIResponse struct {
	ID         string                `json:"id"`
	Model      string                `json:"model"`
	Status     string                `json:"status"`
	Output     []responsesOutputItem `json:"output"`
	OutputText string                `json:"output_text"`
	Usage      *responsesUsage       `json:"usage,omitempty"`
	Error      *responsesAPIError    `json:"error,omitempty"`
}

type responsesOutputItem struct {
	Type      string                 `json:"type"`
	ID        string                 `json:"id"`
	Role      string                 `json:"role"`
	CallID    string                 `json:"call_id"`
	Name      string                 `json:"name"`
	Arguments string                 `json:"arguments"`
	Content   []responsesContentPart `json:"content"`
	Summary   []responsesContentPart `json:"summary"`
}

type responsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	InputDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details,omitempty"`
}

type responsesAPIError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

type responsesStreamEvent struct {
	Type        string                `json:"type"`
	Delta       string                `json:"delta"`
	OutputIndex int                   `json:"output_index"`
	Item        responsesOutputItem   `json:"item"`
	Response    *responsesAPIResponse `json:"response,omitempty"`
	Error       *responsesAPIError    `json:"error,omitempty"`
}

func buildResponsesRequest(cfg Config, messages []Message, opts CallOptions, stream bool) (responsesRequest, error) {
	input, err := toResponsesInput(messages)
	if err != nil {
		return responsesRequest{}, err
	}

	req := responsesRequest{
		Model:           cfg.LlmProvider.Model,
		Input:           input,
		MaxOutputTokens: cfg.Limits.MaxTokens,
		Temperature:     cfg.LlmProvider.Temperature,
		Stream:          stream,
	}
	if len(opts.Tools) > 0 {
		req.Tools = toResponsesTools(opts.Tools)
		if opts.ToolChoice != nil {
			req.ToolChoice = toResponsesToolChoice(opts.ToolChoice)
		} else {
			req.ToolChoice = "auto"
		}
	}
	return req, nil
}

func toResponsesInput(messages []Message) ([]any, error) {
	input := make([]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "tool":
			callID := strings.TrimSpace(msg.ToolCallID)
			if callID == "" {
				continue
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  msg.Content,
			})
		case "assistant":
			if content := responsesAssistantText(msg); content != "" {
				input = append(input, map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{{
						"type": "output_text",
						"text": content,
					}},
				})
			}
			for _, call := range msg.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					callID = GenerateCallID()
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      call.Name,
					"arguments": call.Arguments,
				})
			}
		default:
			content, err := toResponsesInputContent(msg)
			if err != nil {
				return nil, err
			}
			input = append(input, map[string]any{
				"type":    "message",
				"role":    msg.Role,
				"content": content,
			})
		}
	}
	return input, nil
}

func responsesAssistantText(msg Message) string {
	if len(msg.ContentParts) == 0 {
		return msg.Content
	}
	var text strings.Builder
	for _, part := range msg.ContentParts {
		if part.Type == "text" {
			text.WriteString(part.Text)
		}
	}
	if text.Len() == 0 {
		return msg.Content
	}
	return text.String()
}

func toResponsesInputContent(msg Message) (any, error) {
	if len(msg.ContentParts) == 0 {
		return msg.Content, nil
	}

	parts := make([]map[string]any, 0, len(msg.ContentParts))
	for _, part := range msg.ContentParts {
		switch part.Type {
		case "text":
			text := strings.TrimSpace(part.Text)
			if text != "" {
				parts = append(parts, map[string]any{"type": "input_text", "text": text})
			}
		case "image":
			if msg.Role != "user" {
				return nil, fmt.Errorf("image content is only supported for user messages")
			}
			if part.Image == nil {
				return nil, fmt.Errorf("image content part is missing image payload")
			}
			imageURL, err := resolveOpenAIImageURL(part.Image)
			if err != nil {
				return nil, err
			}
			detail := strings.TrimSpace(part.Image.Detail)
			if detail == "" {
				detail = "auto"
			}
			parts = append(parts, map[string]any{
				"type":      "input_image",
				"image_url": imageURL,
				"detail":    detail,
			})
		default:
			return nil, fmt.Errorf("unsupported content part type %q", part.Type)
		}
	}
	if len(parts) == 0 {
		return msg.Content, nil
	}
	return parts, nil
}

func toResponsesTools(tools []map[string]any) []responsesTool {
	result := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		function := newToolFunction(fn)
		if function.Name == "" {
			continue
		}
		result = append(result, responsesTool{
			Type:        "function",
			Name:        function.Name,
			Description: function.Description,
			Parameters:  function.Parameters,
		})
	}
	return result
}

func toResponsesToolChoice(choice any) any {
	choiceMap, ok := choice.(map[string]any)
	if !ok {
		return choice
	}
	function, ok := choiceMap["function"].(map[string]any)
	if !ok {
		return choice
	}
	name, _ := function["name"].(string)
	if strings.TrimSpace(name) == "" {
		return choice
	}
	return map[string]any{"type": "function", "name": name}
}

func callOpenAIResponses(ctx context.Context, cfg Config, messages []Message, opts CallOptions) (*Response, error) {
	normalizedMessages := normalizeToolProtocolMessages(messages)
	if len(normalizedMessages) != len(messages) {
		log.Printf("[provider] normalized tool protocol messages (responses): before=%d after=%d", len(messages), len(normalizedMessages))
	}
	reqBody, err := buildResponsesRequest(cfg, normalizedMessages, opts, false)
	if err != nil {
		return nil, fmt.Errorf("convert responses request: %w", err)
	}
	body, err := jsonAPI.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal responses request: %w", err)
	}
	capture := newUpstreamCapture("responses_non_stream", cfg, body)

	resp, err := doOpenAIRequestTo(ctx, cfg, body, "/responses")
	if err != nil {
		capture.writeError("do_request", err)
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		capture.writeError("read_response", err)
		return nil, fmt.Errorf("read responses response: %w", err)
	}
	capture.writeResponseMeta(resp.StatusCode, resp.Header)
	capture.writeResponseBody(respBody)
	if resp.StatusCode != 200 {
		log.Printf("[provider] responses non-200: model=%s url=%s status=%d body=%s", cfg.LlmProvider.Model, strings.TrimRight(cfg.LlmProvider.BaseURL, "/")+"/responses", resp.StatusCode, strings.TrimSpace(string(respBody)))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var response responsesAPIResponse
	if err := jsonAPI.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("parse responses response: %w", err)
	}
	if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
		return nil, fmt.Errorf("responses API error: %s", response.Error.Message)
	}
	return response.toProviderResponse(cfg.LlmProvider.Model), nil
}

func (response responsesAPIResponse) toProviderResponse(configuredModel string) *Response {
	content, reasoning, toolCalls := collectResponsesOutput(response)
	result := &Response{
		Content:          content,
		ReasoningContent: reasoning,
		ToolCalls:        toolCalls,
		FinishReason:     responseFinishReason(response, len(toolCalls) > 0),
		Model:            configuredModel,
		Usage:            convertResponsesUsage(response.Usage),
	}
	if result.Usage != nil {
		result.TokensUsed = result.Usage.TotalTokens
		logOpenAIUsage("responses_non_stream", configuredModel, result.Usage)
	}
	return result
}

func collectResponsesOutput(response responsesAPIResponse) (string, string, []ToolCall) {
	var content strings.Builder
	var reasoning strings.Builder
	toolCalls := make([]ToolCall, 0)

	for _, item := range response.Output {
		switch item.Type {
		case "function_call":
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				callID = GenerateCallID()
			}
			toolCalls = append(toolCalls, ToolCall{ID: callID, Name: item.Name, Arguments: item.Arguments})
		case "message":
			appendResponsesText(&content, item.Content)
		case "reasoning":
			appendResponsesText(&reasoning, item.Summary)
		}
	}
	if content.Len() == 0 {
		content.WriteString(response.OutputText)
	}
	return content.String(), reasoning.String(), toolCalls
}

func appendResponsesText(dst *strings.Builder, parts []responsesContentPart) {
	for _, part := range parts {
		if part.Type == "output_text" || part.Type == "summary_text" {
			dst.WriteString(part.Text)
		}
	}
}

func responseFinishReason(response responsesAPIResponse, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	switch strings.ToLower(strings.TrimSpace(response.Status)) {
	case "incomplete":
		return "length"
	case "failed", "cancelled", "canceled":
		return "error"
	default:
		return "stop"
	}
}

func convertResponsesUsage(usage *responsesUsage) *UsageDetails {
	if usage == nil {
		return nil
	}
	result := &UsageDetails{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
	}
	if usage.InputDetails != nil {
		result.CachedPromptTokens = usage.InputDetails.CachedTokens
	}
	return result
}

func callOpenAIResponsesStream(ctx context.Context, cfg Config, messages []Message, opts CallOptions) (<-chan StreamChunk, error) {
	normalizedMessages := normalizeToolProtocolMessages(messages)
	if len(normalizedMessages) != len(messages) {
		log.Printf("[provider] normalized tool protocol messages (responses stream): before=%d after=%d", len(messages), len(normalizedMessages))
	}
	reqBody, err := buildResponsesRequest(cfg, normalizedMessages, opts, true)
	if err != nil {
		return nil, fmt.Errorf("convert responses request: %w", err)
	}
	body, err := jsonAPI.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal responses request: %w", err)
	}
	capture := newUpstreamCapture("responses_stream", cfg, body)

	resp, err := doOpenAIRequestTo(ctx, cfg, body, "/responses")
	if err != nil {
		capture.writeError("do_request", err)
		return nil, err
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		capture.writeResponseMeta(resp.StatusCode, resp.Header)
		capture.writeResponseBody(respBody)
		log.Printf("[provider] responses stream non-200: model=%s url=%s status=%d body=%s", cfg.LlmProvider.Model, strings.TrimRight(cfg.LlmProvider.BaseURL, "/")+"/responses", resp.StatusCode, strings.TrimSpace(string(respBody)))
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
			file, fileErr := os.OpenFile(capture.prefix+".response.sse.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if fileErr != nil {
				capture.writeError("open_sse_capture", fileErr)
			} else {
				captureFile = file
				bodyReader = io.TeeReader(resp.Body, file)
			}
		}
		if captureFile != nil {
			defer captureFile.Close()
		}

		scanner := bufio.NewScanner(bodyReader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		sawContent := false
		sawReasoning := false
		sawToolCalls := false
		seenToolCalls := make(map[int]bool)
		toolArguments := make(map[int]string)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				finish := "stop"
				if sawToolCalls {
					finish = "tool_calls"
				}
				ch <- StreamChunk{Done: true, FinishReason: finish, Model: cfg.LlmProvider.Model}
				return
			}

			var event responsesStreamEvent
			if err := jsonAPI.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			switch event.Type {
			case "response.output_text.delta":
				if event.Delta != "" {
					sawContent = true
					ch <- StreamChunk{Content: event.Delta, Model: cfg.LlmProvider.Model}
				}
			case "response.reasoning_summary_text.delta":
				if event.Delta != "" {
					sawReasoning = true
					ch <- StreamChunk{ReasoningContent: event.Delta, Model: cfg.LlmProvider.Model}
				}
			case "response.output_item.added":
				if event.Item.Type == "function_call" {
					sawToolCalls = true
					seenToolCalls[event.OutputIndex] = true
					toolArguments[event.OutputIndex] = event.Item.Arguments
					ch <- StreamChunk{ToolCallDeltas: []StreamToolCallDelta{{
						Index:     event.OutputIndex,
						ID:        responseCallID(event.Item),
						Name:      event.Item.Name,
						Arguments: event.Item.Arguments,
					}}, Model: cfg.LlmProvider.Model}
				}
			case "response.function_call_arguments.delta":
				if event.Delta != "" {
					toolArguments[event.OutputIndex] += event.Delta
					ch <- StreamChunk{ToolCallDeltas: []StreamToolCallDelta{{
						Index:     event.OutputIndex,
						Arguments: event.Delta,
					}}, Model: cfg.LlmProvider.Model}
				}
			case "response.output_item.done":
				if event.Item.Type == "function_call" {
					sawToolCalls = true
					if !seenToolCalls[event.OutputIndex] {
						seenToolCalls[event.OutputIndex] = true
						toolArguments[event.OutputIndex] = event.Item.Arguments
						ch <- StreamChunk{ToolCallDeltas: []StreamToolCallDelta{{
							Index:     event.OutputIndex,
							ID:        responseCallID(event.Item),
							Name:      event.Item.Name,
							Arguments: event.Item.Arguments,
						}}, Model: cfg.LlmProvider.Model}
					} else if remaining := strings.TrimPrefix(event.Item.Arguments, toolArguments[event.OutputIndex]); remaining != event.Item.Arguments && remaining != "" {
						toolArguments[event.OutputIndex] += remaining
						ch <- StreamChunk{ToolCallDeltas: []StreamToolCallDelta{{Index: event.OutputIndex, Arguments: remaining}}, Model: cfg.LlmProvider.Model}
					}
				}
			case "response.completed":
				if event.Response != nil {
					usage := convertResponsesUsage(event.Response.Usage)
					if usage != nil {
						logOpenAIUsage("responses_stream", cfg.LlmProvider.Model, usage)
						ch <- StreamChunk{Model: cfg.LlmProvider.Model, Usage: usage}
					}
					fallbackContent, fallbackReasoning, _ := collectResponsesOutput(*event.Response)
					if !sawContent && fallbackContent != "" {
						ch <- StreamChunk{Content: fallbackContent, Model: cfg.LlmProvider.Model}
					}
					if !sawReasoning && fallbackReasoning != "" {
						ch <- StreamChunk{ReasoningContent: fallbackReasoning, Model: cfg.LlmProvider.Model}
					}
					for index, item := range event.Response.Output {
						if item.Type != "function_call" || seenToolCalls[index] {
							continue
						}
						callID := responseCallID(item)
						if callID == "" {
							callID = GenerateCallID()
						}
						sawToolCalls = true
						seenToolCalls[index] = true
						ch <- StreamChunk{ToolCallDeltas: []StreamToolCallDelta{{Index: index, ID: callID, Name: item.Name, Arguments: item.Arguments}}, Model: cfg.LlmProvider.Model}
					}
					finish := responseFinishReason(*event.Response, sawToolCalls)
					ch <- StreamChunk{Done: true, FinishReason: finish, Model: cfg.LlmProvider.Model}
					return
				}
				ch <- StreamChunk{Done: true, FinishReason: "stop", Model: cfg.LlmProvider.Model}
				return
			case "response.failed", "response.incomplete", "error":
				message := ""
				if event.Error != nil {
					message = event.Error.Message
				} else if event.Response != nil && event.Response.Error != nil {
					message = event.Response.Error.Message
				}
				log.Printf("[provider] responses stream event failed: model=%s type=%s error=%s", cfg.LlmProvider.Model, event.Type, message)
				finish := "error"
				if event.Type == "response.incomplete" {
					finish = "length"
				}
				ch <- StreamChunk{Done: true, FinishReason: finish, Model: cfg.LlmProvider.Model}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			capture.writeError("scan_sse", err)
		}
	}()

	return ch, nil
}

func responseCallID(item responsesOutputItem) string {
	if callID := strings.TrimSpace(item.CallID); callID != "" {
		return callID
	}
	return strings.TrimSpace(item.ID)
}
