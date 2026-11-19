package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

// Numbered rows compete for the small tool excerpt budget. The interesting
// line remains in the source but disappears from both rendered excerpts.
func compactEvidenceToolResult(fact string) provider.Message {
	var b strings.Builder
	for i := 0; i < 64; i++ {
		if i == 32 {
			b.WriteString(fact + "\n")
		}
		fmt.Fprintf(&b, "record elapsed=%04dms: ordinary synthetic observation\n", 2000+i)
	}
	return provider.Message{Role: "tool", Name: "terminal", Content: b.String()}
}

func TestCompactSourceEvidenceMapAndMerge(t *testing.T) {
	const alpha = "/example/alpha/manifest.txt"
	const bravo = "/example/bravo/manifest.txt"
	first := compactEvidenceToolResult(alpha)
	second := compactEvidenceToolResult(bravo)
	history := []provider.Message{
		{Role: "user", Content: "inspect the first manifest"}, first, first,
		{Role: "user", Content: "inspect the other manifest"}, second,
	}
	for _, output := range []provider.Message{first, second} {
		raw := compactlib.Transcript(toCompactMessages([]provider.Message{output}), compactTextOptions())
		if strings.Contains(raw, alpha) || strings.Contains(raw, bravo) {
			t.Fatal("fixture already exposed the middle of the tool output")
		}
	}

	est := contextx.NewTokenEstimator(4096)
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("context.compact_max_chunk_tokens", fmt.Sprint(estimateProviderMessages(est, history[:3]))); err != nil {
		t.Fatal(err)
	}
	// This provider omits both identifiers in its map responses, so a complete
	// merge prompt requires the host to recover both from the original source.
	cp := &countingCompactProvider{name: "fake"}
	a := &Agent{cfg: cfg, contextEst: est, provider: cp}
	_, source, err := a.generateMapReduceCompactSummary(context.Background(), history, providerSnapshot{provider: cp, model: "test"}, CompactSessionOptions{})
	if err != nil || source != "llm-mapreduce" || len(cp.prompts) != 3 {
		t.Fatalf("source=%q calls=%d error=%v", source, len(cp.prompts), err)
	}
	for _, prompt := range cp.prompts {
		var wantAlpha, wantBravo int
		switch {
		case strings.Contains(prompt, "Merge the segment summaries"):
			wantAlpha, wantBravo = 1, 1
		case strings.Contains(prompt, history[0].Content):
			wantAlpha = 1
		case strings.Contains(prompt, history[3].Content):
			wantBravo = 1
		default:
			t.Fatal("unrecognized compact request")
		}
		if strings.Count(prompt, alpha) != wantAlpha || strings.Count(prompt, bravo) != wantBravo {
			t.Fatalf("incorrect source evidence in a request expecting alpha=%d bravo=%d", wantAlpha, wantBravo)
		}
	}
}

func TestCompactSourceEvidenceLocalBeforeChat(t *testing.T) {
	safe := "/example/alpha/manifest.txt"
	secret := "error: Authorization: Bearer synthetic-only"
	tool := compactEvidenceToolResult(safe)
	history := []provider.Message{{Role: "user", Content: "x"}, tool}
	// One chunk fits exactly before the evidence is restored. Adding even the
	// shortest missing fact would exceed the transcript limit.
	base := compactlib.Transcript(toCompactMessages(history), compactTextOptions())
	history[0].Content += strings.Repeat("x", compactlib.TranscriptMaxRunes-1-utf8.RuneCountInString(base))
	for _, tt := range []struct {
		name     string
		messages []provider.Message
		maxChunk int
	}{
		{"sensitive excerpt", []provider.Message{{Role: "user", Content: "inspect the tool output"}, compactEvidenceToolResult(secret)}, 12000},
		{"transcript budget", history, 64000},
		{"evidence spans windows", []provider.Message{{Role: "user", Content: "error: " + strings.Repeat("word ", 35)}}, 24},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.NewManagerWithDir(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Set("context.compact_max_chunk_tokens", fmt.Sprint(tt.maxChunk)); err != nil {
				t.Fatal(err)
			}
			cp := &countingCompactProvider{name: "fake"}
			a := &Agent{cfg: cfg, provider: cp, contextEst: contextx.NewTokenEstimator(4096)}
			summary, source, err := a.generateMapReduceCompactSummary(context.Background(), tt.messages, providerSnapshot{provider: cp, model: "test"}, CompactSessionOptions{})
			if err != nil || source != "local" || cp.calls.Load() != 0 {
				t.Fatalf("source=%q remote calls=%d error=%v", source, cp.calls.Load(), err)
			}
			if v := validateCompactSummary(summary, tt.messages); !v.Valid {
				t.Fatalf("local summary failed validation: %s", v.Reason)
			}
		})
	}
}

func TestCompactSourceEvidenceMergeBudget(t *testing.T) {
	const fact = "/example/docs/state.md"
	plan := compactEvidencePlan{required: []string{fact}}
	body := strings.Repeat("x", compactlib.TranscriptMaxRunes)
	request := compactlib.ChatRequest{Kind: "merge", UserPrompt: compactlib.MergePrompt(body, "LuckyAgent"), TranscriptRunes: utf8.RuneCountInString(body)}
	if _, ok := plan.prepare(request); ok {
		t.Fatal("merge exceeded its body budget when restoring evidence")
	}
	body = fact + body[len(fact):]
	request.UserPrompt = compactlib.MergePrompt(body, "LuckyAgent")
	if got, ok := plan.prepare(request); !ok || got != request {
		t.Fatal("merge with an already present fact must fit the original budget")
	}
}
