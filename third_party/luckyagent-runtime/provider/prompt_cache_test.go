package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestPromptCacheCanonicalizationPreservesInputAndLiteralOrder(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{"z", "a"}, "properties": map[string]any{"child": map[string]any{"required": []string{"z", "a"}}}, "enum": []any{"z", "a"}, "default": map[string]any{"required": []any{"z", "a"}}, "oneOf": []any{map[string]any{"title": "z"}, map[string]any{"title": "a"}}}
	tools := []map[string]any{{"function": map[string]any{"name": "z", "parameters": schema}}, {"function": map[string]any{"name": "a"}}}
	before, _ := json.Marshal(tools)
	got := canonicalizeOpenAITools(tools)
	if openAIToolName(got[0]) != "a" {
		t.Fatal("tools not sorted")
	}
	params := got[1]["function"].(map[string]any)["parameters"].(map[string]any)
	if !reflect.DeepEqual(params["required"], []string{"a", "z"}) {
		t.Fatalf("required = %#v", params["required"])
	}
	child := params["properties"].(map[string]any)["child"].(map[string]any)
	if !reflect.DeepEqual(child["required"], []string{"a", "z"}) {
		t.Fatal("nested required not sorted")
	}
	for _, key := range []string{"enum", "default", "oneOf"} {
		if !reflect.DeepEqual(params[key], schema[key]) {
			t.Errorf("changed literal %s", key)
		}
	}
	after, _ := json.Marshal(tools)
	if string(before) != string(after) {
		t.Fatal("mutated input schema")
	}
}

func TestPromptCacheKeyOnAllOpenAIRequestPaths(t *testing.T) {
	original := openAIHTTPClient
	t.Cleanup(func() { openAIHTTPClient = original })
	for _, protocol := range []string{"chat_completions", "responses"} {
		for _, stream := range []bool{false, true} {
			for _, key := range []string{"", " project-a "} {
				t.Run(protocol+"/"+map[bool]string{false: "chat", true: "stream"}[stream]+"/"+key, func(t *testing.T) {
					called := false
					openAIHTTPClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
						called = true
						var body map[string]any
						if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						got, exists := body["prompt_cache_key"]
						if key == "" && exists {
							t.Fatal("default key sent")
						}
						if key != "" && got != "project-a" {
							t.Fatalf("key = %#v", got)
						}
						content := `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`
						if protocol == "responses" {
							content = `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`
						}
						if stream {
							content = "data: [DONE]\n\n"
							if protocol == "responses" {
								content = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
							}
						}
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(content)), Request: req}, nil
					})}
					cfg := Config{LlmProvider: LlmProvider{BaseURL: "https://unit.invalid/v1", APIKey: "test", Model: "gpt-4.1", Protocol: protocol, PromptCacheKey: key}}
					messages := []Message{{Role: "user", Content: "hello"}}
					if stream {
						ch, err := callOpenAIStream(context.Background(), cfg, messages, CallOptions{})
						if err != nil {
							t.Fatal(err)
						}
						for chunk := range ch {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
							}
						}
					} else {
						if _, err := callOpenAI(context.Background(), cfg, messages, CallOptions{}); err != nil {
							t.Fatal(err)
						}
					}
					if !called {
						t.Fatal("no request")
					}
				})
			}
		}
	}
}
