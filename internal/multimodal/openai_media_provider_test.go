package multimodal

import (
	"context"
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
