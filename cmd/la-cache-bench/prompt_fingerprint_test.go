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

func TestComparePromptPrefixStopsAtFirstChangedMessage(t *testing.T) {
	previous := promptSnapshot{
		EstimatedTokens: 30,
		Messages:        [][]byte{[]byte(`{"role":"system"}`), []byte(`{"role":"user","content":"one"}`)},
		MessageTokens:   []int{20, 10},
	}
	current := promptSnapshot{
		EstimatedTokens: 40,
		Messages:        [][]byte{[]byte(`{"role":"system"}`), []byte(`{"role":"user","content":"two"}`), []byte(`{"role":"assistant"}`)},
		MessageTokens:   []int{20, 10, 10},
	}

	got := comparePromptPrefix(previous, current)
	if got.Messages != 1 || got.Tokens != 20 || got.Ratio != 0.5 {
		t.Fatalf("unexpected stable prefix: %+v", got)
	}
}

func TestReadPromptSnapshotCanonicalizesMessageObjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call.request.json")
	data := `{"messages":[{"content":"core prompt","role":"system"},{"role":"user","content":"hello"}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	got := readPromptSnapshot(path)
	if got.Hash == "" || got.EstimatedTokens <= 0 || len(got.Messages) != 2 {
		t.Fatalf("unexpected prompt snapshot: %+v", got)
	}
	if string(got.Messages[0]) != `{"content":"core prompt","role":"system"}` {
		t.Fatalf("expected canonical message JSON, got %s", got.Messages[0])
	}
}
