package eval

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/memory"
)

func TestLongMemEvalRetrievalScoresMatchOfficialFormula(t *testing.T) {
	// eval_utils.evaluate_retrieval(["s2", "s1", "s3"], correct=["s1", "s3"], k=2):
	// recalled {s2, s1} misses s3, so recall_all is 0.
	// DCG = 0 + 1/log2(2) = 1. Ideal DCG for two relevant docs at k=2 is
	// 1 + 1/log2(2) = 2. NDCG is 0.5.
	recallAny, recallAll, ndcg := SessionRetrievalScores([]string{"s2", "s1", "s3"}, []string{"s1", "s3"}, 2)
	if recallAny != 1 {
		t.Fatalf("recall_any@2 = %v, want 1", recallAny)
	}
	if recallAll != 0 {
		t.Fatalf("recall_all@2 = %v, want 0", recallAll)
	}
	if ndcg < 0.499 || ndcg > 0.501 {
		t.Fatalf("ndcg_any@2 = %v, want 0.5", ndcg)
	}

	recallAny, recallAll, ndcg = SessionRetrievalScores([]string{"s1", "s3", "s2"}, []string{"s1", "s3"}, 2)
	if recallAny != 1 || recallAll != 1 || ndcg < 0.999 {
		t.Fatalf("perfect top-2: any=%v all=%v ndcg=%v", recallAny, recallAll, ndcg)
	}

	recallAny, _, _ = SessionRetrievalScores([]string{"other"}, []string{"s1", "s3"}, 5)
	if recallAny != 0 {
		t.Fatalf("recall_any with no hit = %v, want 0", recallAny)
	}
}

func TestLongMemEvalSampleRetrievesTheEvidenceSession(t *testing.T) {
	instance := LongMemEvalInstance{
		QuestionID:         "sample-user",
		QuestionType:       "single-session-user",
		Question:           "What running shoes did I buy?",
		QuestionDate:       "2024/06/02 (Sun) 10:00",
		HaystackSessionIDs: []string{"filler", "evidence"},
		HaystackDates:      []string{"2024/05/01 (Wed) 09:00", "2024/05/20 (Mon) 18:30"},
		HaystackSessions: [][]LMTurn{
			{{Role: "user", Content: "I watched a cooking show about pasta."}, {Role: "assistant", Content: "Which dish did you try?"}},
			{{Role: "user", Content: "I bought Brooks Ghost running shoes yesterday.", HasAnswer: true}, {Role: "assistant", Content: "Those are a neutral daily trainer."}},
		},
		AnswerSessionIDs: []string{"evidence"},
	}
	ranked, err := RetrieveLongMemEval(instance, SearchEvalOptions{TopK: 5, AsOf: time.Date(2024, 6, 2, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) == 0 || ranked[0] != "evidence" {
		t.Fatalf("ranked = %#v, want evidence first", ranked)
	}
	recallAny, recallAll, _ := SessionRetrievalScores(ranked, instance.AnswerSessionIDs, 5)
	if recallAny != 1 || recallAll != 1 {
		t.Fatalf("recall any=%v all=%v, want both 1", recallAny, recallAll)
	}
}

func TestLongMemEvalAbstentionIsNotScoredAsRetrieval(t *testing.T) {
	instances := []LongMemEvalInstance{{
		QuestionID:         "missing_abs",
		QuestionType:       "single-session-user",
		Question:           "What car did I lease?",
		HaystackSessionIDs: []string{"only"},
		HaystackSessions:   [][]LMTurn{{{Role: "user", Content: "I bake bread on Sundays."}}},
		AnswerSessionIDs:   nil,
	}}
	metrics, err := EvaluateLongMemEvalRetrieval(instances, SearchEvalOptions{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Questions != 0 || metrics.Skipped != 1 {
		t.Fatalf("metrics = %+v, want the abstention question skipped", metrics)
	}
}

var longMemEvalData = flag.String("longmemeval", "", "path to longmemeval_oracle.json, longmemeval_s_cleaned.json, or longmemeval_m_cleaned.json")
var longMemEvalLimit = flag.Int("longmemeval-limit", 0, "evaluate only the first N questions; 0 means the whole file")

func TestLongMemEvalDataset(t *testing.T) {
	path := strings.TrimSpace(*longMemEvalData)
	if path == "" {
		path = strings.TrimSpace(os.Getenv("LONGMEMEVAL_DATA"))
	}
	if path == "" {
		path = filepath.Join("data", "longmemeval_oracle.json")
	}
	path = resolveLongMemEvalPath(path)
	if _, err := os.Stat(path); err != nil {
		t.Skip("put longmemeval_oracle.json in eval/data/ or pass -longmemeval / LONGMEMEVAL_DATA")
	}

	instances, err := LoadLongMemEval(path)
	if err != nil {
		t.Fatal(err)
	}
	if *longMemEvalLimit > 0 && *longMemEvalLimit < len(instances) {
		instances = instances[:*longMemEvalLimit]
	}
	metrics, err := EvaluateLongMemEvalRetrieval(instances, SearchEvalOptions{
		TopK: 10,
		AsOf: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\nLongMemEval retrieval\n%s\n", encoded)
	if metrics.Questions == 0 {
		t.Fatal("no scored questions")
	}
}

// go test runs with eval/ as the working directory, while commands are often
// typed from the repository root. Accept either location.
func resolveLongMemEvalPath(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	trimmed := strings.TrimPrefix(filepath.ToSlash(path), "eval/")
	if trimmed != path {
		if _, err := os.Stat(trimmed); err == nil {
			return trimmed
		}
	}
	return path
}

func TestLongMemEvalSavedSessionKeepsTheSessionID(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveWithOptionsResult("user: I bought Brooks Ghost running shoes.", "conversation", memory.TierLong, 0.5, memory.SaveOptions{
		Tags:    []string{"session-7"},
		Aliases: []string{"session-7"},
	}); err != nil {
		t.Fatal(err)
	}
	results := store.SearchWithOptions("Brooks Ghost", memory.SearchOptions{Limit: 1, IncludeInactive: true, SkipAccessStats: true})
	if len(results) != 1 || sessionIDFromResult(results[0]) != "session-7" {
		t.Fatalf("results = %+v", results)
	}
}
