package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/session"
)

func TestInspectContextIncludesHistoryAndHypotheticalUser(t *testing.T) {
	a := &Agent{contextWin: contextx.NewContextWindow(contextx.DefaultWindowConfig())}
	sess := session.NewSession("inspect-history", t.TempDir())
	sess.AddMessage("user", "earlier question about the planner")
	sess.AddMessage("assistant", "earlier answer")
	before := sess.MessageCount()

	result := a.InspectContext(context.Background(), ContextInspectRequest{
		Session: sess,
		Message: "继续改上下文接口",
	})

	if sess.MessageCount() != before {
		t.Fatalf("inspect changed session messages: before=%d after=%d", before, sess.MessageCount())
	}
	if result.Usage.SessionID != sess.ID {
		t.Fatalf("session id = %q", result.Usage.SessionID)
	}
	if result.Usage.Estimate != "local" {
		t.Fatalf("estimate = %q", result.Usage.Estimate)
	}
	if result.Usage.Buckets["history"].Tokens <= 0 {
		t.Fatalf("history bucket = %+v", result.Usage.Buckets["history"])
	}
	if result.Usage.Buckets["user"].Budget != 0 {
		t.Fatalf("user budget = %d", result.Usage.Buckets["user"].Budget)
	}
	if len(result.Sections) == 0 || result.Sections[len(result.Sections)-1].Role != "user" {
		t.Fatalf("last section = %+v", result.Sections)
	}
	last := result.Sections[len(result.Sections)-1]
	if last.Label != "user" || !strings.Contains(last.Preview, "继续改上下文接口") {
		t.Fatalf("last user section = %+v", last)
	}
	historySeen := false
	for _, section := range result.Sections {
		if section.Bucket == "history" {
			historySeen = true
		}
		if section.Content != "" {
			t.Fatalf("content leaked without include_messages: %+v", section)
		}
	}
	if !historySeen {
		t.Fatal("expected a history section before the user message")
	}
	if result.Usage.TotalTokens <= 0 || result.Usage.AvailableTokens <= 0 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if result.Usage.HeadroomTokens != result.Usage.AvailableTokens-result.Usage.TotalTokens {
		t.Fatalf("headroom = %d usage = %+v", result.Usage.HeadroomTokens, result.Usage)
	}
}

func TestInspectContextEmptyMessageDoesNotAppendUser(t *testing.T) {
	a := &Agent{}
	sess := session.NewSession("inspect-empty", t.TempDir())
	sess.AddMessage("user", "stored user turn")

	result := a.InspectContext(context.Background(), ContextInspectRequest{Session: sess})
	for _, section := range result.Sections {
		if section.Role == "user" && section.Label == "user" && strings.Contains(section.Preview, "multimodal") {
			t.Fatalf("empty inspect appended placeholder user: %+v", section)
		}
	}
	if result.Usage.Buckets["history"].Messages == 0 && result.Usage.Buckets["user"].Messages == 0 {
		t.Fatalf("stored history missing: %+v", result.Usage.Buckets)
	}
}

func TestInspectContextIncludeMessagesCopiesContent(t *testing.T) {
	a := &Agent{}
	result := a.InspectContext(context.Background(), ContextInspectRequest{
		Message:         "show me the full section",
		IncludeMessages: true,
	})
	if len(result.Sections) == 0 {
		t.Fatal("expected sections")
	}
	last := result.Sections[len(result.Sections)-1]
	if last.Content != "show me the full section" {
		t.Fatalf("content = %q", last.Content)
	}
}

func TestInspectContextTwiceDoesNotChangeMemory(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{memory: store}
	before := a.memory.Count()
	req := ContextInspectRequest{Message: "remember this query"}
	a.InspectContext(context.Background(), req)
	a.InspectContext(context.Background(), req)
	if got := a.memory.Count(); got != before {
		t.Fatalf("memory count changed: before=%d after=%d", before, got)
	}
}
