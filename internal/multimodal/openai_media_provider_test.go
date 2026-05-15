package multimodal

import (
	"context"
	"encoding/base64"
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
