package memoryaestus_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/yurika0211/aestus/memory"
	"github.com/yurika0211/luckyagent-runtime/memoryaestus"
	"github.com/yurika0211/luckyagent-tools/memory"
)

func TestAdapterConcurrentGetAndRepeatedWrite(t *testing.T) {
	store, err := memoryaestus.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	req := memory.RememberRequest{Content: "Project runtime record", Category: "project", Tier: memory.TierLong, Importance: .9,
		Options: memory.SaveOptions{RoutePolicies: []memory.RoutePolicy{{ID: "inspect", Match: memory.RoutePolicyMatch{QueryAll: []memory.RouteTermGroup{{Any: []string{"runtime"}}}}, RequiredTools: []memory.RouteToolRequirement{{Name: "read"}}}}}}
	first, err := store.Remember(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	errs := make(chan error, 1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			if _, err := store.Remember(context.Background(), req); err != nil {
				errs <- err
				return
			}
		}
	}()
	for i := 0; i < 1000; i++ {
		entry, err := store.Get(first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Content != req.Content || entry.AccessedAt.IsZero() || entry.RoutePolicies[0].Match.QueryAll[0].Any[0] != "runtime" {
			t.Error("incomplete projected snapshot")
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestAdapterOpensExistingVaultAndPersistsStateReplacement(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sdk.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	old, err := legacy.SaveWithOptionsResult("Project directory is /old/project", "project", sdk.TierLong, .9,
		sdk.SaveOptions{StateKey: "project.directory"})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := memoryaestus.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := memory.Write(context.Background(), store, memory.RememberRequest{
		Content: "Project directory is /new/project", Category: "project", Tier: memory.TierLong, Importance: .9,
		Options: memory.SaveOptions{StateKey: "project.directory", StateValue: "/new/project", Confidence: .9}, Mode: "upsert_state",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID == old.ID || len(result.Superseded) != 1 || result.Superseded[0] != old.ID {
		t.Fatalf("replacement lost IDs: old=%+v new=%+v", old, result)
	}
	previous, err := store.Get(old.ID)
	if err != nil || previous.Path != old.Path || previous.Status != "superseded" {
		t.Fatalf("old note = %+v, %v", previous, err)
	}
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(result.Path)))
	if err != nil || !strings.Contains(string(body), "state_key: project.directory") || !strings.Contains(string(body), result.ID) {
		t.Fatalf("Markdown contract lost: %q, %v", body, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sdk.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := reopened.Get(result.ID)
	if err != nil || entry.StateValue != "/new/project" || len(reopened.ActiveStateIDs("project.directory")) != 1 {
		t.Fatalf("SDK could not read adapter write: %+v, %v", entry, err)
	}
}

func TestAdapterKeepsLocatedEvidenceAndPolicyArgumentsIndependent(t *testing.T) {
	store, err := memoryaestus.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := memory.RoutePolicy{ID: "lookup", Match: memory.RoutePolicyMatch{QueryAny: []string{"launch"}},
		RequiredTools: []memory.RouteToolRequirement{{Name: "read", Calls: []memory.RouteToolCall{{Arguments: map[string]any{"options": map[string]any{"key": "original"}}}}}}}
	target, err := store.SaveWithOptionsResult("# Credentials\nRead the current credential file.\n^credential-block\n\n# Other\nUnrelated material.", "project", memory.TierLong, .9,
		memory.SaveOptions{Aliases: []string{"Runtime Config"}, Tags: []string{"original"}, RoutePolicies: []memory.RoutePolicy{policy}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("launch instructions: [["+target.ID+"#Credentials#^credential-block|configuration]]", "fact"); err != nil {
		t.Fatal(err)
	}
	route := store.RouteWithOptions("launch instructions", memory.RouteOptions{})
	excerpts := route.Excerpts[target.ID]
	if len(excerpts) == 0 || excerpts[0].Block != "credential-block" || excerpts[0].Heading != "Credentials" ||
		!strings.Contains(excerpts[0].Context, "credential file") || strings.Contains(excerpts[0].Context, "Unrelated") {
		t.Fatalf("located evidence lost: %+v", route)
	}
	entry, err := store.Get(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry.Tags[0] = "changed"
	entry.RoutePolicies[0].RequiredTools[0].Calls[0].Arguments["options"].(map[string]any)["key"] = "changed"
	again, err := store.Get(target.ID)
	if err != nil || again.Tags[0] != "original" || again.RoutePolicies[0].RequiredTools[0].Calls[0].Arguments["options"].(map[string]any)["key"] != "original" {
		t.Fatalf("caller mutated SDK-owned evidence: %+v, %v", again, err)
	}
}

func TestAdapterSessionStateAndSummariesStayIndependent(t *testing.T) {
	sessions := memoryaestus.NewSessionShortTermStore(2)
	for i := 0; i < 5; i++ {
		sessions.Add("a", "user", "alpha preference")
		sessions.Add("b", "user", "beta preference")
	}
	if strings.Contains(sessions.Summary("a"), "beta") || strings.Contains(sessions.Summary("b"), "alpha") {
		t.Fatal("session summaries crossed scopes")
	}
	if context := sessions.GetContext("a"); len(context) == 0 || context[0].Role != "system" {
		t.Fatalf("neutral context = %+v", context)
	}
	sessions.Clear("a")
	if _, ok := sessions.Get("a"); ok {
		t.Fatal("cleared session still exists")
	}
	if sessions.SessionCount() != 1 {
		t.Fatalf("session count = %d", sessions.SessionCount())
	}
	mid, err := memoryaestus.NewMidTermStore(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	summary := memoryaestus.GenerateSessionSummary("session-summary", "user", []memory.ConversationTurn{{Role: "user", Content: "Decided to use Aestus for memory."}})
	if err := mid.SaveSessionSummary(summary); err != nil {
		t.Fatal(err)
	}
	if saved, ok := mid.Get("session-summary"); !ok || saved.SessionID != "session-summary" || saved.RawSummary == "" {
		t.Fatalf("summary lost: %+v", saved)
	}
}

func TestAdapterForwardsTidalFeedbackAndClosesOwnedResources(t *testing.T) {
	store, err := memoryaestus.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tidal, err := memoryaestus.OpenTidalStore(filepath.Join(t.TempDir(), "tidal.db"))
	if err != nil {
		t.Fatal(err)
	}
	reranker, err := memoryaestus.NewPersistentTidalMemoryReranker(memoryaestus.DefaultTidalRerankerConfig(), tidal)
	if err != nil {
		t.Fatal(err)
	}
	store.SetActivationReranker(reranker)
	if err := store.Save("Aestus memory integration", "project"); err != nil {
		t.Fatal(err)
	}
	entries := store.Search("Aestus memory")
	store.RecordActivationFeedback("Aestus memory", entries, "context_selected", 1, time.Now())
	stats, err := reranker.StoreStats()
	if err != nil || stats.QueryEvents == 0 || stats.FeedbackEvents == 0 {
		t.Fatalf("Tidal hooks lost: %+v, %v", stats, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tidal.Stats(); err == nil {
		t.Fatal("owned Tidal DB remained open")
	}
}
