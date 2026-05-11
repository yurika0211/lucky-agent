package multimodal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultOpenAIUserAgent = "luckyharness"

type OpenAIMediaConfig struct {
	APIKey             string
	APIBase            string
	ResponsesModel     string
	TranscriptionModel string
}

type OpenAIMediaProvider struct {
	mu                 sync.RWMutex
	apiKey             string
	apiBase            string
	responsesModel     string
	transcriptionModel string
	client             *http.Client
	analyzed           int
}

func NewOpenAIMediaProvider(cfg OpenAIMediaConfig) (*OpenAIMediaProvider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("openai media provider requires api key")
	}
	if strings.TrimSpace(cfg.APIBase) == "" {
		cfg.APIBase = "https://api.openai.com/v1"
	}
	cfg.APIBase = strings.TrimRight(cfg.APIBase, "/")
	if cfg.ResponsesModel == "" {
		cfg.ResponsesModel = "gpt-4o"
	}
	if cfg.TranscriptionModel == "" {
		cfg.TranscriptionModel = "whisper-1"
	}

	return &OpenAIMediaProvider{
		apiKey:             cfg.APIKey,
		apiBase:            cfg.APIBase,
		responsesModel:     cfg.ResponsesModel,
		transcriptionModel: cfg.TranscriptionModel,
		client: &http.Client{
			Timeout: 90 * time.Second,
		},
	}, nil
}

func (o *OpenAIMediaProvider) Name() string {
	return "openai-media"
}

func (o *OpenAIMediaProvider) SupportedModalities() []Modality {
	return []Modality{ModalityImage, ModalityAudio, ModalityDocument}
}

func (o *OpenAIMediaProvider) Analyze(ctx context.Context, input *Input) (*AnalysisResult, error) {
	o.mu.Lock()
	o.analyzed++
	o.mu.Unlock()

	switch input.Modality {
	case ModalityImage:
		return o.analyzeWithResponses(ctx, input, "Describe this image for an AI assistant. Extract visible text, summarize the scene, and keep the result concise but informative.")
	case ModalityDocument:
		return o.analyzeWithResponses(ctx, input, "Read this document and extract the most important information for an AI assistant. Summarize the document, preserve critical facts, and quote key text snippets only when necessary.")
	case ModalityAudio:
		return o.transcribeAudio(ctx, input)
	default:
		return nil, fmt.Errorf("unsupported modality for openai media provider: %q", input.Modality)
	}
}

func (o *OpenAIMediaProvider) AnalyzeStream(ctx context.Context, input *Input) (<-chan StreamChunk, error) {
	result, err := o.Analyze(ctx, input)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- StreamChunk{Text: result.Text, Done: false}
		ch <- StreamChunk{Text: "", Done: true}
	}()

	return ch, nil
}

func (o *OpenAIMediaProvider) Validate() error {
	if strings.TrimSpace(o.apiKey) == "" {
		return fmt.Errorf("openai media provider requires api key")
	}
	if strings.TrimSpace(o.apiBase) == "" {
		return fmt.Errorf("openai media provider requires api base")
	}
	return nil
}

func applyOpenAIMediaHeaders(req *http.Request, apiKey, contentType string) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", defaultOpenAIUserAgent)
}

func (o *OpenAIMediaProvider) analyzeWithResponses(ctx context.Context, input *Input, prompt string) (*AnalysisResult, error) {
	contentItem, err := o.buildResponsesContentItem(input)
	if err != nil {
		return nil, err
	}

	reqBody := map[string]any{
		"model": o.responsesModel,
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": prompt,
					},
					contentItem,
				},
			},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal responses request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.apiBase+"/responses", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create responses request: %w", err)
	}
	applyOpenAIMediaHeaders(req, o.apiKey, "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send responses request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read responses response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("responses api error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	text := extractResponsesOutputText(body)
	if text == "" {
		return nil, fmt.Errorf("responses api returned empty output")
	}

	return &AnalysisResult{
		InputID:    input.ID,
		Modality:   input.Modality,
		Text:       text,
		Summary:    truncateString(text, 240),
		Labels:     []string{string(input.Modality), "openai"},
		Confidence: 0.85,
		Metadata: map[string]string{
			"model":  o.responsesModel,
			"source": "openai-responses",
		},
	}, nil
}

func (o *OpenAIMediaProvider) transcribeAudio(ctx context.Context, input *Input) (*AnalysisResult, error) {
	data, err := o.resolveInputData(input)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("audio input requires downloaded data")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("model", o.transcriptionModel); err != nil {
		return nil, fmt.Errorf("write model field: %w", err)
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return nil, fmt.Errorf("write response format field: %w", err)
	}

	filename := input.Metadata["filename"]
	if filename == "" {
		filename = "audio" + extensionForMime(input.MimeType)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("create multipart file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("write audio file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.apiBase+"/audio/transcriptions", &body)
	if err != nil {
		return nil, fmt.Errorf("create transcription request: %w", err)
	}
	applyOpenAIMediaHeaders(req, o.apiKey, writer.FormDataContentType())

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send transcription request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read transcription response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("transcription api error %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	text := extractTranscriptionText(respBody)
	if text == "" {
		return nil, fmt.Errorf("transcription api returned empty text")
	}

	return &AnalysisResult{
		InputID:    input.ID,
		Modality:   input.Modality,
		Text:       text,
		Summary:    truncateString(text, 240),
		Labels:     []string{string(input.Modality), "transcription"},
		Confidence: 0.85,
		Metadata: map[string]string{
			"model":  o.transcriptionModel,
			"source": "openai-transcription",
		},
	}, nil
}

func (o *OpenAIMediaProvider) buildResponsesContentItem(input *Input) (map[string]any, error) {
	switch input.Modality {
	case ModalityImage:
		if input.URL != "" {
			return map[string]any{
				"type":      "input_image",
				"image_url": input.URL,
			}, nil
		}
		data, err := o.resolveInputData(input)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("image input requires url or data")
		}
		return map[string]any{
			"type":      "input_image",
			"image_url": fmt.Sprintf("data:%s;base64,%s", input.MimeType, base64.StdEncoding.EncodeToString(data)),
		}, nil

	case ModalityDocument:
		filename := input.Metadata["filename"]
		if filename == "" {
			filename = "document" + extensionForMime(input.MimeType)
		}
		if input.URL != "" && len(input.Data) == 0 {
			return map[string]any{
				"type":     "input_file",
				"file_url": input.URL,
				"filename": filename,
			}, nil
		}
		data, err := o.resolveInputData(input)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("document input requires url or data")
		}
		return map[string]any{
			"type":      "input_file",
			"file_data": base64.StdEncoding.EncodeToString(data),
			"filename":  filename,
		}, nil
	}
	return nil, fmt.Errorf("unsupported modality %q", input.Modality)
}

func (o *OpenAIMediaProvider) resolveInputData(input *Input) ([]byte, error) {
	if len(input.Data) > 0 {
		return input.Data, nil
	}
	if strings.TrimSpace(input.FilePath) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(input.FilePath)
	if err != nil {
		return nil, fmt.Errorf("read input file %q: %w", input.FilePath, err)
	}
	return data, nil
}

func extractResponsesOutputText(body []byte) string {
	var helper struct {
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(body, &helper); err == nil && strings.TrimSpace(helper.OutputText) != "" {
		return strings.TrimSpace(helper.OutputText)
	}

	var payload struct {
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	var parts []string
	for _, out := range payload.Output {
		for _, content := range out.Content {
			if strings.TrimSpace(content.Text) == "" {
				continue
			}
			if content.Type == "" || strings.Contains(content.Type, "text") {
				parts = append(parts, strings.TrimSpace(content.Text))
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func extractTranscriptionText(body []byte) string {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Text) != "" {
		return strings.TrimSpace(payload.Text)
	}
	return strings.TrimSpace(string(body))
}

func extensionForMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "application/pdf":
		return ".pdf"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	}
	if ext := filepath.Ext(mimeType); ext != "" {
		return ext
	}
	return ""
}
