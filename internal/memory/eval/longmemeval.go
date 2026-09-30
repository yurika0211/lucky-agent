// LongMemEval retrieval adapter.
//
// The dataset is LongMemEval (ICLR 2025), MIT licensed:
// https://github.com/xiaowu0162/LongMemEval
// Cleaned release: https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned
//
// This adapter scores retrieval only. It does not call a model and does not
// grade the written answer. A history session is one memory note. A question
// counts as recall_all@k when every answer_session_id appears in the top k
// notes. ndcg_any@k follows src/retrieval/eval_utils.py in that repository.
package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yurika0211/luckyagent/internal/memory"
)

// LongMemEvalKs are the cutoffs printed by the official retrieval report.
var LongMemEvalKs = []int{5, 10}

// LongMemEvalInstance is one question plus the chat history it is hidden in.
type LongMemEvalInstance struct {
	QuestionID         string     `json:"question_id"`
	QuestionType       string     `json:"question_type"`
	Question           string     `json:"question"`
	Answer             any        `json:"answer"`
	QuestionDate       string     `json:"question_date"`
	HaystackSessionIDs []string   `json:"haystack_session_ids"`
	HaystackDates      []string   `json:"haystack_dates"`
	HaystackSessions   [][]LMTurn `json:"haystack_sessions"`
	AnswerSessionIDs   []string   `json:"answer_session_ids"`
}

// LMTurn is one user or assistant message. Evidence turns set HasAnswer.
type LMTurn struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	HasAnswer bool   `json:"has_answer,omitempty"`
}

// LongMemEvalMetrics is the session-level retrieval report.
// Abstention questions (question_id ending in _abs) are left out, matching
// print_retrieval_metrics.py.
//
// RecallAll is the official printed metric: every evidence session is inside
// the top k. RecallAny is the community comparison metric: at least one
// evidence session is inside the top k. Both come from eval_utils.py.
type LongMemEvalMetrics struct {
	Questions int                `json:"questions"`
	Skipped   int                `json:"skipped_abstention"`
	ByType    map[string]float64 `json:"recall_all_at_5_by_type"`
	ByTypeAny map[string]float64 `json:"recall_any_at_5_by_type"`
	RecallAll map[int]float64    `json:"recall_all"`
	RecallAny map[int]float64    `json:"recall_any"`
	NDCGAny   map[int]float64    `json:"ndcg_any"`
}

// LoadLongMemEval reads one released JSON file. The oracle file is about 15 MB.
// The S file is about 277 MB and the M file is about 2.7 GB.
func LoadLongMemEval(path string) ([]LongMemEvalInstance, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var instances []LongMemEvalInstance
	if err := json.NewDecoder(f).Decode(&instances); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return instances, nil
}

// EvaluateLongMemEvalRetrieval indexes every session as its own note, searches
// with the question text, and averages the official session-level metrics.
func EvaluateLongMemEvalRetrieval(instances []LongMemEvalInstance, opts SearchEvalOptions) (LongMemEvalMetrics, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}
	metrics := LongMemEvalMetrics{
		ByType:    map[string]float64{},
		ByTypeAny: map[string]float64{},
		RecallAll: map[int]float64{},
		RecallAny: map[int]float64{},
		NDCGAny:   map[int]float64{},
	}
	typeAll := map[string]int{}
	typeAny := map[string]int{}
	typeTotal := map[string]int{}
	recallAllHits := map[int]int{}
	recallAnyHits := map[int]int{}
	ndcgSum := map[int]float64{}

	for _, instance := range instances {
		if strings.HasSuffix(instance.QuestionID, "_abs") {
			metrics.Skipped++
			continue
		}
		ranked, err := RetrieveLongMemEval(instance, opts)
		if err != nil {
			return LongMemEvalMetrics{}, fmt.Errorf("%s: %w", instance.QuestionID, err)
		}
		metrics.Questions++
		typeTotal[instance.QuestionType]++
		for _, k := range LongMemEvalKs {
			recallAny, recallAll, ndcg := SessionRetrievalScores(ranked, instance.AnswerSessionIDs, k)
			recallAnyHits[k] += int(recallAny)
			recallAllHits[k] += int(recallAll)
			ndcgSum[k] += ndcg
			if k == 5 && recallAll == 1 {
				typeAll[instance.QuestionType]++
			}
			if k == 5 && recallAny == 1 {
				typeAny[instance.QuestionType]++
			}
		}
	}
	for _, k := range LongMemEvalKs {
		metrics.RecallAll[k] = ratio(recallAllHits[k], metrics.Questions)
		metrics.RecallAny[k] = ratio(recallAnyHits[k], metrics.Questions)
		metrics.NDCGAny[k] = ratioFloat(ndcgSum[k], metrics.Questions)
	}
	for questionType, total := range typeTotal {
		metrics.ByType[questionType] = ratio(typeAll[questionType], total)
		metrics.ByTypeAny[questionType] = ratio(typeAny[questionType], total)
	}
	return metrics, nil
}

// SearchEvalOptions controls how many notes are kept. AsOf keeps the long-term
// half-life from burying older sessions during a batch run.
type SearchEvalOptions struct {
	TopK int
	AsOf time.Time
}

// RetrieveLongMemEval stores one note per session and returns session ids in
// search rank order.
func RetrieveLongMemEval(instance LongMemEvalInstance, opts SearchEvalOptions) ([]string, error) {
	if len(instance.HaystackSessionIDs) != len(instance.HaystackSessions) {
		return nil, fmt.Errorf("session ids (%d) and sessions (%d) differ", len(instance.HaystackSessionIDs), len(instance.HaystackSessions))
	}
	dir, err := os.MkdirTemp("", "aestus-longmemeval-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	asOf := opts.AsOf
	if asOf.IsZero() {
		asOf = time.Now()
	}
	// One question has hundreds of sessions in the M set. Saving through
	// Store rewrites the whole vault on every note, so the notes are written
	// once and then opened normally.
	if err := writeLongMemEvalVault(dir, instance, asOf); err != nil {
		return nil, err
	}
	store, err := memory.NewStore(dir)
	if err != nil {
		return nil, err
	}
	results := store.SearchWithOptions(instance.Question, memory.SearchOptions{
		Limit:           opts.TopK,
		IncludeInactive: true,
		IncludeExpired:  true,
		AsOf:            asOf,
		SkipAccessStats: true,
	})
	ranked := make([]string, 0, len(results))
	for _, result := range results {
		ranked = append(ranked, sessionIDFromResult(result))
	}
	return ranked, nil
}

func writeLongMemEvalVault(dir string, instance LongMemEvalInstance, asOf time.Time) error {
	notes := filepath.Join(dir, "30_Sessions")
	if err := os.MkdirAll(notes, 0700); err != nil {
		return err
	}
	for i, sessionID := range instance.HaystackSessionIDs {
		content := formatLongMemEvalSession(instance, i)
		if strings.TrimSpace(content) == "" {
			content = sessionID
		}
		created := parseLongMemEvalDate(instance.sessionDate(i), asOf).UTC()
		front, err := yaml.Marshal(map[string]any{
			"id":           sessionID,
			"type":         "memory",
			"tier":         "long",
			"category":     "conversation",
			"importance":   0.5,
			"access_count": 0,
			"created_at":   created,
			"accessed_at":  created,
			"tags":         []string{sessionID},
			"aliases":      []string{sessionID},
			"status":       "active",
			"valid_from":   created,
			"block_id":     strings.ReplaceAll(sessionID, "_", "-"),
		})
		if err != nil {
			return err
		}
		note := "---\n" + string(front) + "---\n\n## Memory\n\n" + content + "\n"
		path := filepath.Join(notes, fmt.Sprintf("%04d.md", i))
		if err := os.WriteFile(path, []byte(note), 0600); err != nil {
			return fmt.Errorf("write %s: %w", sessionID, err)
		}
	}
	return nil
}

func (instance LongMemEvalInstance) sessionDate(index int) string {
	if index < 0 || index >= len(instance.HaystackDates) {
		return ""
	}
	return instance.HaystackDates[index]
}

func formatLongMemEvalSession(instance LongMemEvalInstance, index int) string {
	var b strings.Builder
	if date := instance.sessionDate(index); date != "" {
		fmt.Fprintf(&b, "Session date: %s\n", date)
	}
	for _, turn := range instance.HaystackSessions[index] {
		role := strings.TrimSpace(turn.Role)
		if role == "" {
			role = "user"
		}
		content := strings.TrimSpace(turn.Content)
		if content == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", role, content)
	}
	return strings.TrimSpace(b.String())
}

func sessionIDFromResult(result memory.SearchResult) string {
	for _, tag := range result.Entry.Tags {
		if strings.TrimSpace(tag) != "" {
			return tag
		}
	}
	for _, alias := range result.Entry.Aliases {
		if strings.TrimSpace(alias) != "" {
			return alias
		}
	}
	return ""
}

// SessionRetrievalScores implements evaluate_retrieval from
// src/retrieval/eval_utils.py. It returns recall_any@k, recall_all@k, and
// ndcg_any@k. recall_any is 1 when any evidence session is inside the top k.
// recall_all is 1 only when every evidence session is inside the top k.
func SessionRetrievalScores(ranked []string, answerIDs []string, k int) (float64, float64, float64) {
	if k < 0 {
		k = 0
	}
	recalled := map[string]bool{}
	limit := k
	if limit > len(ranked) {
		limit = len(ranked)
	}
	for _, id := range ranked[:limit] {
		recalled[id] = true
	}
	recallAny := 0.0
	recallAll := 0.0
	if len(answerIDs) > 0 {
		recallAll = 1
		for _, id := range answerIDs {
			if recalled[id] {
				recallAny = 1
				continue
			}
			recallAll = 0
		}
	}
	return recallAny, recallAll, ndcgAny(ranked, answerSet(answerIDs), k)
}

func answerSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// ndcgAny follows dcg and ndcg in eval_utils.py. With binary relevance the
// ideal ranking is min(k, relevant-count) ones followed by zeros.
func ndcgAny(ranked []string, relevant map[string]bool, k int) float64 {
	if k > len(ranked) {
		k = len(ranked)
	}
	actual := dcgBinary(ranked[:k], relevant)
	idealCount := len(relevant)
	if idealCount > k {
		idealCount = k
	}
	idealRank := make([]string, idealCount)
	ideal := map[string]bool{"": true}
	idealActual := dcgBinary(idealRank, ideal)
	if idealActual == 0 {
		return 0
	}
	return actual / idealActual
}

func dcgBinary(ranked []string, relevant map[string]bool) float64 {
	if len(ranked) == 0 {
		return 0
	}
	score := 0.0
	if relevant[ranked[0]] {
		score = 1
	}
	// Official dcg() divides relevances[i] by log2(i + 1), where i starts at 1.
	// The second position therefore uses log2(2), not log2(3).
	for i := 1; i < len(ranked); i++ {
		if relevant[ranked[i]] {
			score += 1 / math.Log2(float64(i+1))
		}
	}
	return score
}

func parseLongMemEvalDate(value string, fallback time.Time) time.Time {
	value = strings.TrimSpace(value)
	layouts := []string{
		"2006/01/02 (Mon) 15:04",
		"2006/01/02 (Monday) 15:04",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return fallback
}

func ratio(hit, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

func ratioFloat(sum float64, total int) float64 {
	if total == 0 {
		return 0
	}
	return sum / float64(total)
}
