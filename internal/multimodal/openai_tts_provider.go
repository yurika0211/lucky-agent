package multimodal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultOpenAITTSModel = "gpt-4o-mini-tts"
const defaultOpenAITTSVoice = "alloy"

type OpenAITTSConfig struct {
	APIKey   string
	APIBase  string
	AuthMode string
}

type OpenAITTSProvider struct {
	apiKey   string
	apiBase  string
	authMode string
	client   *http.Client
}

func NewOpenAITTSProvider(cfg OpenAITTSConfig) (*OpenAITTSProvider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("openai tts provider requires api key")
	}
	if strings.TrimSpace(cfg.APIBase) == "" {
		cfg.APIBase = "https://api.openai.com/v1"
	}
	authMode := strings.ToLower(strings.TrimSpace(cfg.AuthMode))
	if authMode == "" {
		authMode = "bearer"
	}
	return &OpenAITTSProvider{
		apiKey:   strings.TrimSpace(cfg.APIKey),
		apiBase:  strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/"),
		authMode: authMode,
		client: &http.Client{
			Timeout: 90 * time.Second,
		},
	}, nil
}

func (o *OpenAITTSProvider) Name() string {
	return "openai-tts"
}

func (o *OpenAITTSProvider) SynthesizeSpeech(ctx context.Context, req SpeechSynthesisRequest) (*SpeechSynthesisResult, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, fmt.Errorf("speech synthesis text is required")
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = defaultOpenAITTSModel
	}
	voice := strings.TrimSpace(req.Voice)
	if voice == "" {
		voice = defaultOpenAITTSVoice
	}
	format := normalizeSpeechFormat(req.Format)
	speed := req.Speed
	if speed <= 0 {
		speed = 1.0
	}

	reqBody := map[string]any{
		"model":           model,
		"input":           text,
		"voice":           voice,
		"response_format": format,
	}
	if speed != 1.0 {
		reqBody["speed"] = speed
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal tts request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.apiBase+"/audio/speech", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create tts request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", defaultOpenAIUserAgent)
	switch o.authMode {
	case "x-api-key":
		httpReq.Header.Set("x-api-key", o.apiKey)
	default:
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send tts request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read tts response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tts api error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return &SpeechSynthesisResult{
		Provider:  "openai-tts",
		Model:     model,
		Voice:     voice,
		Audio:     body,
		MimeType:  mimeTypeForSpeechFormat(format),
		CreatedAt: time.Now().UTC(),
		Metadata: map[string]string{
			"source": "openai-audio-speech",
			"format": format,
		},
	}, nil
}

func normalizeSpeechFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "mp3":
		return "mp3"
	case "wav":
		return "wav"
	case "opus":
		return "opus"
	case "aac":
		return "aac"
	case "flac":
		return "flac"
	case "pcm", "pcm16":
		return "pcm"
	default:
		return strings.ToLower(strings.TrimSpace(format))
	}
}

func mimeTypeForSpeechFormat(format string) string {
	switch normalizeSpeechFormat(format) {
	case "wav":
		return "audio/wav"
	case "opus":
		return "audio/opus"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "pcm":
		return "audio/pcm"
	default:
		return "audio/mpeg"
	}
}
