package memory

import (
	"fmt"
	"strings"
	"testing"
)

func TestActivateReturnsExplainableComponents(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithOptions(
		"User prefers [[Telegram Gateway]] for message delivery.",
		"preference",
		TierLong,
		0.9,
		SaveOptions{
			Tags:    []string{"gateway"},
			Aliases: []string{"telegram channel"},
		},
	); err != nil {
		t.Fatalf("SaveWithOptions: %v", err)
	}

	scores := store.Activate("telegram channel", ActivationOptions{
		IncludeGraph:      true,
		UpdateAccessStats: false,
	})
	if len(scores) != 1 {
		t.Fatalf("expected 1 activation, got %#v", scores)
	}
	components := scores[0].Components
	if components.MatchScore() <= 0 {
		t.Fatalf("expected positive match score: %#v", components)
	}
	if components.Aliases <= 0 {
		t.Fatalf("expected alias component, got %#v", components)
	}
	if components.Links <= 0 {
		t.Fatalf("expected wikilink component, got %#v", components)
	}
	if components.Importance != 0.9 {
		t.Fatalf("expected importance component 0.9, got %f", components.Importance)
	}
	if components.Recency <= 0 || components.Access <= 0 {
		t.Fatalf("expected positive recency/access components, got %#v", components)
	}
}

func TestActivateRecordsGraphPaths(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithTierAndTags("Outdoor walks often include [[Daughter]].", "plan", TierMedium, 0.6, []string{"family"}); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := store.SaveWithTierAndTags("[[Daughter]] has [[Pollen Allergy]].", "health", TierLong, 0.95, []string{"health"}); err != nil {
		t.Fatalf("save health: %v", err)
	}

	scores := store.Activate("Outdoor walks", ActivationOptions{
		IncludeGraph:      true,
		UpdateAccessStats: false,
		Explain:           true,
	})
	if len(scores) < 2 {
		t.Fatalf("expected graph activation to add related memory, got %#v", scores)
	}
	for _, score := range scores {
		if strings.Contains(score.Entry.Content, "Pollen Allergy") {
			if score.Components.GraphBoost <= 0 {
				t.Fatalf("expected graph boost for propagated memory, got %#v", score.Components)
			}
			if len(score.Paths) == 0 {
				t.Fatalf("expected activation path for propagated memory")
			}
			return
		}
	}
	t.Fatalf("expected propagated allergy memory, got %#v", scores)
}

func TestSearchParallelUsesUnifiedActivation(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithTierAndTags("Outdoor walks often include [[Daughter]].", "plan", TierMedium, 0.6, []string{"family"}); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := store.SaveWithTierAndTags("[[Daughter]] has [[Pollen Allergy]].", "health", TierLong, 0.95, []string{"health"}); err != nil {
		t.Fatalf("save health: %v", err)
	}

	results := store.SearchParallel("Outdoor walks", 3)
	if len(results) < 2 {
		t.Fatalf("expected SearchParallel to include graph-propagated memory, got %#v", results)
	}
	for _, result := range results {
		if strings.Contains(result.Content, "Pollen Allergy") {
			return
		}
	}
	t.Fatalf("expected propagated allergy memory, got %#v", results)
}

func TestActivateLimitReturnsOrderedTopK(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cases := []struct {
		content    string
		importance float64
	}{
		{content: "ranked memory low", importance: 0.2},
		{content: "ranked memory high", importance: 0.9},
		{content: "ranked memory mid", importance: 0.6},
	}
	for _, tc := range cases {
		if err := store.SaveWithTierAndTags(tc.content, "ranking", TierLong, tc.importance, []string{"ranked"}); err != nil {
			t.Fatalf("save %q: %v", tc.content, err)
		}
	}

	scores := store.Activate("ranked memory", ActivationOptions{
		Limit:             2,
		IncludeGraph:      false,
		UpdateAccessStats: false,
	})
	if len(scores) != 2 {
		t.Fatalf("expected top 2 scores, got %#v", scores)
	}
	if !strings.Contains(scores[0].Entry.Content, "high") || !strings.Contains(scores[1].Entry.Content, "mid") {
		t.Fatalf("expected high then mid, got %#v", scores)
	}
	if scores[0].Score < scores[1].Score {
		t.Fatalf("expected descending scores, got %#v", scores)
	}
}

func TestActivateLimitsGraphSeedsToStrongestDirectMatches(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for i := 0; i < 8; i++ {
		if err := store.SaveWithTierAndTags(
			fmt.Sprintf("low priority benchmark note %d", i),
			"benchmark",
			TierShort,
			0.1,
			[]string{"benchmark"},
		); err != nil {
			t.Fatalf("save noise: %v", err)
		}
	}
	if err := store.SaveWithTierAndTags("priority benchmark entry links [[Critical Node]].", "benchmark", TierLong, 0.95, []string{"benchmark"}); err != nil {
		t.Fatalf("save priority: %v", err)
	}
	if err := store.SaveWithTierAndTags("[[Critical Node]] carries high-value routed evidence.", "evidence", TierLong, 0.95, []string{"critical"}); err != nil {
		t.Fatalf("save evidence: %v", err)
	}

	scores := store.Activate("benchmark", ActivationOptions{
		Limit:             4,
		IncludeGraph:      true,
		MaxGraphSeeds:     1,
		UpdateAccessStats: false,
	})
	for _, score := range scores {
		if strings.Contains(score.Entry.Content, "high-value routed evidence") {
			if score.Components.GraphBoost <= 0 {
				t.Fatalf("expected graph boost from strongest seed, got %#v", score.Components)
			}
			return
		}
	}
	t.Fatalf("expected strongest seed to propagate critical evidence, got %#v", scores)
}
