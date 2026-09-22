package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/tool"
)

func TestImageReadPreservesBytesThroughProviderRequest(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	registry.Register(tool.ImageReadTool())
	result, err := registry.CallDetailed("image_read", map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	call := provider.ToolCall{ID: "read_1", Name: "image_read", Arguments: "{}"}
	messages := appendLatestComputerObservation([]provider.Message{
		{Role: "user", Content: "Read the image."},
		{Role: "assistant", ToolCalls: []provider.ToolCall{call}},
		{Role: "tool", ToolCallID: call.ID, Name: call.Name, Content: result.Output},
	}, []executedToolCall{{ToolCall: call, Observations: result.Observations}})
	messages = (&Agent{}).fitContextWindow(messages)
	wantURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	for _, protocol := range []string{"chat_completions", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			captured := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				captured <- body
				w.Header().Set("Content-Type", "application/json")
				if protocol == "responses" {
					_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"received"}]}]}`))
				} else {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"received"},"finish_reason":"stop"}]}`))
				}
			}))
			defer server.Close()
			p := provider.NewOpenAIProvider(provider.Config{LlmProvider: provider.LlmProvider{
				APIKey: "test-key", BaseURL: server.URL, Model: "vision-test", Protocol: protocol,
			}})
			if _, err := p.Chat(context.Background(), messages); err != nil {
				t.Fatal(err)
			}
			body := <-captured
			field := "messages"
			if protocol == "responses" {
				field = "input"
			}
			items := body[field].([]any)
			last := items[len(items)-1].(map[string]any)
			if last["role"] != "user" {
				t.Fatal("image must be sent as user input after the tool result")
			}
			parts := last["content"].([]any)
			if len(parts) != 1 {
				t.Fatalf("image part count = %d, want 1", len(parts))
			}
			part := parts[0].(map[string]any)
			var gotURL any
			if protocol == "responses" {
				gotURL = part["image_url"]
			} else {
				gotURL = part["image_url"].(map[string]any)["url"]
			}
			if gotURL != wantURL {
				t.Fatal("the provider changed or omitted the original image bytes")
			}
		})
	}
}
