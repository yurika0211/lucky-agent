package multimodal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOpenAIMediaProviderResponsesSetsUserAgent(t *testing.T) {
	provider, err := NewOpenAIMediaProvider(OpenAIMediaConfig{
		APIKey: "sk-test",
	})
	if err != nil {
		t.Fatalf("NewOpenAIMediaProvider returned error: %v", err)
	}

	var gotUserAgent string
	provider.client = &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			gotUserAgent = req.Header.Get("User-Agent")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"output_text":"ok"}`)),
				Request:    req,
			}, nil
		}),
	}

	_, err = provider.Analyze(context.Background(), &Input{
		ID:       "img-1",
		Modality: ModalityImage,
		MimeType: "image/png",
		Data:     []byte("png"),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	if gotUserAgent != defaultOpenAIUserAgent {
		t.Fatalf("user agent = %q, want %q", gotUserAgent, defaultOpenAIUserAgent)
	}
}

func TestOpenAIMediaProviderAnalyzeImageFallsBackToChatCompletions(t *testing.T) {
	provider, err := NewOpenAIMediaProvider(OpenAIMediaConfig{
		APIKey:         "sk-test",
		ResponsesModel: "vision-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAIMediaProvider returned error: %v", err)
	}

	var paths []string
	var chatBody string
	provider.client = &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			paths = append(paths, req.URL.Path)
			body, _ := io.ReadAll(req.Body)
			if req.URL.Path == "/v1/chat/completions" {
				chatBody = string(body)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"chart summary"}}]}`)),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"responses unavailable"}`)),
				Request:    req,
			}, nil
		}),
	}

	result, err := provider.Analyze(context.Background(), &Input{
		ID:       "img-1",
		Modality: ModalityImage,
		MimeType: "image/png",
		Data:     []byte("png"),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	if strings.Join(paths, ",") != "/v1/responses,/v1/chat/completions" {
		t.Fatalf("paths = %v, want responses then chat completions", paths)
	}
	if !strings.Contains(chatBody, `"model":"vision-model"`) {
		t.Fatalf("chat completion body missing model: %s", chatBody)
	}
	if !strings.Contains(chatBody, `"type":"image_url"`) {
		t.Fatalf("chat completion body missing image_url part: %s", chatBody)
	}
	if result.Text != "chart summary" {
		t.Fatalf("text = %q, want chart summary", result.Text)
	}
	if result.Metadata["source"] != "openai-chat-completions" {
		t.Fatalf("source = %q, want openai-chat-completions", result.Metadata["source"])
	}
	if result.Metadata["fallback_from"] != "openai-responses" {
		t.Fatalf("fallback_from = %q, want openai-responses", result.Metadata["fallback_from"])
	}
}

func TestOpenAIMediaProviderAnalyzeDocumentUsesResponsesDataURL(t *testing.T) {
	provider, err := NewOpenAIMediaProvider(OpenAIMediaConfig{
		APIKey:         "sk-test",
		ResponsesModel: "vision-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAIMediaProvider returned error: %v", err)
	}

	var gotPath string
	var gotBody struct {
		Input []struct {
			Content []map[string]any `json:"content"`
		} `json:"input"`
	}
	provider.client = &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			body, _ := io.ReadAll(req.Body)
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Fatalf("decode request body: %v\n%s", err, body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"output_text":"document summary"}`)),
				Request:    req,
			}, nil
		}),
	}

	result, err := provider.Analyze(context.Background(), &Input{
		ID:       "doc-1",
		Modality: ModalityDocument,
		MimeType: "application/pdf",
		Data:     []byte("%PDF-1.4"),
		Metadata: map[string]string{
			"filename": "report.pdf",
		},
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	if gotPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", gotPath)
	}
	if result.Text != "document summary" {
		t.Fatalf("text = %q, want document summary", result.Text)
	}
	if len(gotBody.Input) != 1 || len(gotBody.Input[0].Content) != 2 {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
	filePart := gotBody.Input[0].Content[1]
	if filePart["type"] != "input_file" || filePart["filename"] != "report.pdf" {
		t.Fatalf("unexpected file part metadata: %+v", filePart)
	}
	wantPrefix := "data:application/pdf;base64,"
	fileData, _ := filePart["file_data"].(string)
	if !strings.HasPrefix(fileData, wantPrefix) {
		t.Fatalf("file_data = %q, want prefix %q", fileData, wantPrefix)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(fileData, wantPrefix))
	if err != nil {
		t.Fatalf("decode file_data payload: %v", err)
	}
	if string(raw) != "%PDF-1.4" {
		t.Fatalf("decoded file_data = %q", raw)
	}
}

func TestOpenAIMediaProviderTranscriptionSetsUserAgent(t *testing.T) {
	provider, err := NewOpenAIMediaProvider(OpenAIMediaConfig{
		APIKey: "sk-test",
	})
	if err != nil {
		t.Fatalf("NewOpenAIMediaProvider returned error: %v", err)
	}

	var gotUserAgent string
	provider.client = &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			gotUserAgent = req.Header.Get("User-Agent")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"text":"ok"}`)),
				Request:    req,
			}, nil
		}),
	}

	_, err = provider.Analyze(context.Background(), &Input{
		ID:       "audio-1",
		Modality: ModalityAudio,
		MimeType: "audio/mp3",
		Data:     []byte("audio"),
	})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	if gotUserAgent != defaultOpenAIUserAgent {
		t.Fatalf("user agent = %q, want %q", gotUserAgent, defaultOpenAIUserAgent)
	}
}

func TestOpenAIMediaProviderGenerateImageUsesImagesAPI(t *testing.T) {
	provider, err := NewOpenAIMediaProvider(OpenAIMediaConfig{
		APIKey: "sk-test",
	})
	if err != nil {
		t.Fatalf("NewOpenAIMediaProvider returned error: %v", err)
	}

	var gotUserAgent, gotPath, gotContentType string
	var gotBody string
	provider.client = &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			gotUserAgent = req.Header.Get("User-Agent")
			gotPath = req.URL.Path
			gotContentType = req.Header.Get("Content-Type")
			body, _ := io.ReadAll(req.Body)
			gotBody = string(body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"created":1710000000,"data":[{"b64_json":"` +
					base64.StdEncoding.EncodeToString([]byte("png-bytes")) + `","revised_prompt":"refined"}]}`)),
				Request: req,
			}, nil
		}),
	}

	result, err := provider.GenerateImage(context.Background(), ImageGenerationRequest{
		Prompt:       "a red lantern in the rain",
		OutputFormat: "png",
	})
	if err != nil {
		t.Fatalf("GenerateImage returned error: %v", err)
	}

	if gotUserAgent != defaultOpenAIUserAgent {
		t.Fatalf("user agent = %q, want %q", gotUserAgent, defaultOpenAIUserAgent)
	}
	if gotPath != "/v1/images/generations" {
		t.Fatalf("path = %q, want /v1/images/generations", gotPath)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Fatalf("content type = %q, want application/json", gotContentType)
	}
	if !strings.Contains(gotBody, `"prompt":"a red lantern in the rain"`) {
		t.Fatalf("request body missing prompt: %s", gotBody)
	}
	if result.Model != defaultOpenAIImageGenerationModel {
		t.Fatalf("model = %q, want %q", result.Model, defaultOpenAIImageGenerationModel)
	}
	if len(result.Images) != 1 || string(result.Images[0].Data) != "png-bytes" {
		t.Fatalf("unexpected image payload: %+v", result.Images)
	}
}

func TestOpenAIMediaProviderGenerateImageEditUsesMultipart(t *testing.T) {
	provider, err := NewOpenAIMediaProvider(OpenAIMediaConfig{
		APIKey: "sk-test",
	})
	if err != nil {
		t.Fatalf("NewOpenAIMediaProvider returned error: %v", err)
	}

	var gotPath, gotContentType, gotBody string
	provider.client = &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			gotContentType = req.Header.Get("Content-Type")
			body, _ := io.ReadAll(req.Body)
			gotBody = string(body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"` +
					base64.StdEncoding.EncodeToString([]byte("edited-bytes")) + `"}]}`)),
				Request: req,
			}, nil
		}),
	}

	result, err := provider.GenerateImage(context.Background(), ImageGenerationRequest{
		Prompt:       "make it cinematic",
		OutputFormat: "jpeg",
		InputImages: []ImageInput{
			{Data: []byte("source-image"), MimeType: "image/png", Filename: "source.png"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateImage returned error: %v", err)
	}

	if gotPath != "/v1/images/edits" {
		t.Fatalf("path = %q, want /v1/images/edits", gotPath)
	}
	if !strings.HasPrefix(gotContentType, "multipart/form-data; boundary=") {
		t.Fatalf("content type = %q, want multipart/form-data", gotContentType)
	}
	if !strings.Contains(gotBody, `name="prompt"`) || !strings.Contains(gotBody, "make it cinematic") {
		t.Fatalf("multipart body missing prompt: %s", gotBody)
	}
	if !strings.Contains(gotBody, `name="image[]"; filename="source.png"`) && !strings.Contains(gotBody, `name="image"; filename="source.png"`) {
		t.Fatalf("multipart body missing image field: %s", gotBody)
	}
	if len(result.Images) != 1 || string(result.Images[0].Data) != "edited-bytes" {
		t.Fatalf("unexpected image payload: %+v", result.Images)
	}
}

func TestOpenAITTSProviderSynthesizeSpeechUsesAudioSpeechAPI(t *testing.T) {
	provider, err := NewOpenAITTSProvider(OpenAITTSConfig{
		APIKey: "sk-test",
	})
	if err != nil {
		t.Fatalf("NewOpenAITTSProvider returned error: %v", err)
	}

	var gotPath, gotUserAgent, gotContentType string
	var gotBody string
	provider.client = &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			gotUserAgent = req.Header.Get("User-Agent")
			gotContentType = req.Header.Get("Content-Type")
			body, _ := io.ReadAll(req.Body)
			gotBody = string(body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("audio-bytes")),
				Request:    req,
			}, nil
		}),
	}

	result, err := provider.SynthesizeSpeech(context.Background(), SpeechSynthesisRequest{
		Text:   "hello world",
		Model:  "gpt-4o-mini-tts",
		Voice:  "alloy",
		Format: "mp3",
	})
	if err != nil {
		t.Fatalf("SynthesizeSpeech returned error: %v", err)
	}
	if gotPath != "/v1/audio/speech" {
		t.Fatalf("path = %q, want /v1/audio/speech", gotPath)
	}
	if gotUserAgent != defaultOpenAIUserAgent {
		t.Fatalf("user agent = %q, want %q", gotUserAgent, defaultOpenAIUserAgent)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Fatalf("content type = %q", gotContentType)
	}
	if !strings.Contains(gotBody, `"input":"hello world"`) || !strings.Contains(gotBody, `"voice":"alloy"`) {
		t.Fatalf("request body missing fields: %s", gotBody)
	}
	if string(result.Audio) != "audio-bytes" || result.MimeType != "audio/mpeg" {
		t.Fatalf("unexpected speech payload: %+v", result)
	}
}
