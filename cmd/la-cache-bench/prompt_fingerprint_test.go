package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSystemPromptText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call.request.json")
	data := `{
		"messages": [
			{"role":"system","content":"core prompt"},
			{"role":"user","content":"hello"},
			{"role":"system","content":[{"type":"text","text":"manual prompt"}]}
		]
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	got := readSystemPromptText(path)
	if got != "core prompt\n\nmanual prompt" {
		t.Fatalf("unexpected system prompt text: %q", got)
	}
}

func TestAggregatePromptFingerprint(t *testing.T) {
	dir := t.TempDir()
	prefix1 := filepath.Join(dir, "one")
	prefix2 := filepath.Join(dir, "two")
	if err := os.WriteFile(prefix1+".request.json", []byte(`{"messages":[{"role":"system","content":"alpha"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefix2+".request.json", []byte(`{"messages":[{"role":"system","content":"beta"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := aggregatePromptFingerprint([]string{prefix1, prefix2})
	if got.Hash == "" || len(got.Hash) != 16 {
		t.Fatalf("unexpected hash: %#v", got)
	}
	if got.Bytes == 0 || got.EstimatedTokens == 0 {
		t.Fatalf("expected non-zero size estimate: %#v", got)
	}
}
