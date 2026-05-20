package multimodal

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGeminiImageProviderGenerateImageBearer(t *testing.T) {
	provider, err := NewGeminiImageProvider(GeminiImageConfig{
		APIKey:   "gem-key",
		APIBase:  "https://api.example.com/v1",
		AuthMode: "bearer",
	})
	if err != nil {
		t.Fatalf("NewGeminiImageProvider returned error: %v", err)
	}

	var gotPath, gotAuth string
	provider.client = &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			gotAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"ok"},{"text":"![image](data:image/png;base64,` +
					base64.StdEncoding.EncodeToString([]byte("png-bytes")) + `)"}]}}]}`)),
				Request: req,
			}, nil
		}),
	}

	result, err := provider.GenerateImage(context.Background(), ImageGenerationRequest{
		Prompt: "flower",
		Model:  "gemini-3.1-flash-image-preview",
	})
	if err != nil {
		t.Fatalf("GenerateImage returned error: %v", err)
	}
	if gotPath != "/v1/models/gemini-3.1-flash-image-preview:generateContent" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer gem-key" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if len(result.Images) != 1 || string(result.Images[0].Data) != "png-bytes" {
		t.Fatalf("unexpected image payload: %+v", result.Images)
	}
}

func TestGeminiImageProviderGenerateImageXGoogAPIKey(t *testing.T) {
	provider, err := NewGeminiImageProvider(GeminiImageConfig{
		APIKey:   "gem-key",
		APIBase:  "https://api.example.com/v1beta",
		AuthMode: "x-goog-api-key",
	})
	if err != nil {
		t.Fatalf("NewGeminiImageProvider returned error: %v", err)
	}

	var gotHeader string
	provider.client = &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			gotHeader = req.Header.Get("x-goog-api-key")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/jpeg","data":"` +
					base64.StdEncoding.EncodeToString([]byte("jpeg-bytes")) + `"}}]}}]}`)),
				Request: req,
			}, nil
		}),
	}

	result, err := provider.GenerateImage(context.Background(), ImageGenerationRequest{
		Prompt: "flower",
		Model:  "gemini-3.1-flash-image-preview",
	})
	if err != nil {
		t.Fatalf("GenerateImage returned error: %v", err)
	}
	if gotHeader != "gem-key" {
		t.Fatalf("x-goog-api-key = %q", gotHeader)
	}
	if len(result.Images) != 1 || string(result.Images[0].Data) != "jpeg-bytes" {
		t.Fatalf("unexpected image payload: %+v", result.Images)
	}
}
