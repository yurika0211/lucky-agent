package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOpenAIEmbedder_EmbedBatch(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/v1/embeddings" {
				t.Fatalf("expected /v1/embeddings, got %s", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("unexpected Authorization header: %q", got)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("unexpected Content-Type: %q", got)
			}

			var req embeddingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if req.Model != "text-embedding-ada-002" {
				t.Fatalf("unexpected model: %q", req.Model)
			}
			if len(req.Input) != 2 || req.Input[0] != "hello" || req.Input[1] != "world" {
				t.Fatalf("unexpected input: %#v", req.Input)
			}

			body, err := json.Marshal(embeddingResponse{
				Data: []embeddingData{
					{Index: 0, Embedding: []float64{0.1, 0.2, 0.3}, Object: "embedding"},
					{Index: 1, Embedding: []float64{0.4, 0.5, 0.6}, Object: "embedding"},
				},
				Model:  "text-embedding-ada-002",
				Object: "list",
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(body)),
			}, nil
		}),
	}

	t.Setenv("EMBEDDING_MODEL_NAME", "text-embedding-ada-002")
	t.Setenv("EMBEDDING_MODEL_KEY", "test-key")
	t.Setenv("EMBEDDING_MODEL_URL", "https://example.test/v1")

	emb := NewOpenAIEmbedder(OpenAIEmbedderConfig{})
	emb.client = client
	vecs, err := emb.EmbedBatch(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("EmbedBatch returned error: %v", err)
	}

	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if !reflect.DeepEqual(vecs[0], []float64{0.1, 0.2, 0.3}) {
		t.Fatalf("unexpected vecs[0]: %#v", vecs[0])
	}
	if !reflect.DeepEqual(vecs[1], []float64{0.4, 0.5, 0.6}) {
		t.Fatalf("unexpected vecs[1]: %#v", vecs[1])
	}
}

func TestOpenAIEmbedder_EmbedBatch_RetriesOn429(t *testing.T) {
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				}, nil
			}

			body, err := json.Marshal(embeddingResponse{
				Data: []embeddingData{
					{Index: 0, Embedding: []float64{0.7, 0.8}, Object: "embedding"},
				},
				Model:  "text-embedding-ada-002",
				Object: "list",
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(body)),
			}, nil
		}),
	}

	t.Setenv("EMBEDDING_MODEL_NAME", "text-embedding-ada-002")
	t.Setenv("EMBEDDING_MODEL_KEY", "test-key")
	t.Setenv("EMBEDDING_MODEL_URL", "https://example.test/v1")
	t.Setenv("EMBEDDING_MODEL_MAX_RETRIES", "2")

	emb := NewOpenAIEmbedder(OpenAIEmbedderConfig{})
	emb.client = client
	vecs, err := emb.EmbedBatch(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("EmbedBatch returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if len(vecs) != 1 || !reflect.DeepEqual(vecs[0], []float64{0.7, 0.8}) {
		t.Fatalf("unexpected vectors: %#v", vecs)
	}
}

func TestOpenAIEmbedder_EmbedBatch_RetriesOnTransportTimeout(t *testing.T) {
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return nil, fmt.Errorf("context deadline exceeded")
			}

			body, err := json.Marshal(embeddingResponse{
				Data: []embeddingData{
					{Index: 0, Embedding: []float64{0.9}, Object: "embedding"},
				},
				Model:  "text-embedding-ada-002",
				Object: "list",
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(body)),
			}, nil
		}),
	}

	t.Setenv("EMBEDDING_MODEL_NAME", "text-embedding-ada-002")
	t.Setenv("EMBEDDING_MODEL_KEY", "test-key")
	t.Setenv("EMBEDDING_MODEL_URL", "https://example.test/v1")
	t.Setenv("EMBEDDING_MODEL_MAX_RETRIES", "2")

	emb := NewOpenAIEmbedder(OpenAIEmbedderConfig{})
	emb.client = client
	vecs, err := emb.EmbedBatch(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("EmbedBatch returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if len(vecs) != 1 || !reflect.DeepEqual(vecs[0], []float64{0.9}) {
		t.Fatalf("unexpected vectors: %#v", vecs)
	}
}

func TestOpenAIEmbedder_EmbedBatch_RetriesOnEOF(t *testing.T) {
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return nil, fmt.Errorf("EOF")
			}

			body, err := json.Marshal(embeddingResponse{
				Data: []embeddingData{
					{Index: 0, Embedding: []float64{1.1, 1.2}, Object: "embedding"},
				},
				Model:  "text-embedding-ada-002",
				Object: "list",
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(body)),
			}, nil
		}),
	}

	t.Setenv("EMBEDDING_MODEL_NAME", "text-embedding-ada-002")
	t.Setenv("EMBEDDING_MODEL_KEY", "test-key")
	t.Setenv("EMBEDDING_MODEL_URL", "https://example.test/v1")
	t.Setenv("EMBEDDING_MODEL_MAX_RETRIES", "2")

	emb := NewOpenAIEmbedder(OpenAIEmbedderConfig{})
	emb.client = client
	vecs, err := emb.EmbedBatch(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("EmbedBatch returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if len(vecs) != 1 || !reflect.DeepEqual(vecs[0], []float64{1.1, 1.2}) {
		t.Fatalf("unexpected vectors: %#v", vecs)
	}
}

func TestOpenAIEmbedder_EmbedBatch_IncludesErrorBodyOn403(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"quota exceeded","type":"forbidden"}}`)),
			}, nil
		}),
	}

	t.Setenv("EMBEDDING_MODEL_NAME", "text-embedding-ada-002")
	t.Setenv("EMBEDDING_MODEL_KEY", "test-key")
	t.Setenv("EMBEDDING_MODEL_URL", "https://example.test/v1")

	emb := NewOpenAIEmbedder(OpenAIEmbedderConfig{})
	emb.client = client
	_, err := emb.EmbedBatch(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}
