package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadJSONCaptureUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call.response.body.txt")
	data := `{
		"usage": {
			"prompt_tokens": 1200,
			"completion_tokens": 34,
			"total_tokens": 1234,
			"prompt_tokens_details": {"cached_tokens": 800}
		}
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	usage, ok := readJSONCaptureUsage(path)
	if !ok {
		t.Fatal("expected JSON capture to be readable")
	}
	if !usage.HasUsage {
		t.Fatal("expected usage")
	}
	if usage.PromptTokens != 1200 || usage.CachedPromptTokens != 800 || usage.CompletionTokens != 34 || usage.TotalTokens != 1234 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestReadSSECaptureUsageUsesLastUsageChunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call.response.sse.txt")
	data := "data: {\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":1,\"total_tokens\":101,\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
		"data: {\"usage\":{\"prompt_tokens\":1200,\"completion_tokens\":34,\"total_tokens\":1234,\"input_tokens_details\":{\"cached_tokens\":900}}}\n\n" +
		"data: [DONE]\n\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	usage, ok := readSSECaptureUsage(path)
	if !ok {
		t.Fatal("expected SSE capture to be readable")
	}
	if !usage.HasUsage {
		t.Fatal("expected usage")
	}
	if usage.PromptTokens != 1200 || usage.CachedPromptTokens != 900 || usage.CompletionTokens != 34 || usage.TotalTokens != 1234 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestDiffCapturePrefixes(t *testing.T) {
	before := map[string]struct{}{
		"/tmp/a": {},
		"/tmp/b": {},
	}
	after := map[string]struct{}{
		"/tmp/a": {},
		"/tmp/b": {},
		"/tmp/c": {},
		"/tmp/d": {},
	}

	got := diffCapturePrefixes(before, after)
	if len(got) != 2 || got[0] != "/tmp/c" || got[1] != "/tmp/d" {
		t.Fatalf("unexpected diff: %#v", got)
	}
}

func TestAggregateCaptureUsage(t *testing.T) {
	dir := t.TempDir()
	prefix1 := filepath.Join(dir, "one")
	prefix2 := filepath.Join(dir, "two")
	if err := os.WriteFile(prefix1+".request.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefix2+".request.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefix1+".response.body.txt", []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cached_tokens":80}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefix2+".response.body.txt", []byte(`{"usage":{"prompt_tokens":200,"completion_tokens":20,"total_tokens":220,"prompt_tokens_details":{"cached_tokens":160}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	totals, files, missing := aggregateCaptureUsage([]string{prefix1, prefix2})
	if missing != 0 {
		t.Fatalf("missing = %d", missing)
	}
	if len(files) != 2 {
		t.Fatalf("files = %#v", files)
	}
	if totals.PromptTokens != 300 || totals.CachedPromptTokens != 240 || totals.CompletionTokens != 30 || totals.TotalTokens != 330 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
}
