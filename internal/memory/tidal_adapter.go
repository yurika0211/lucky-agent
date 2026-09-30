package memory

import (
	"time"

	"github.com/yurika0211/luckyagent/internal/memory/tidal"
)

// TidalReranker adapts tidal.TidalMemoryReranker to ActivationReranker.
// Callers keep using memory.NewTidalMemoryReranker and the tidal types
// re-exported below; the tidal package does not import memory.
type TidalReranker struct {
	inner *tidal.TidalMemoryReranker
}

func newTidalReranker(inner *tidal.TidalMemoryReranker) *TidalReranker {
	return &TidalReranker{inner: inner}
}

func (r *TidalReranker) RerankMemoryActivations(query string, scores []ActivationScore, now time.Time) []ActivationScore {
	if r == nil || r.inner == nil || len(scores) == 0 {
		return scores
	}
	ranked := r.inner.RerankMemoryActivations(query, tidalScoresFromActivation(scores), now)
	if len(ranked) != len(scores) {
		return scores
	}
	out := make([]ActivationScore, len(scores))
	copy(out, scores)
	for i := range out {
		out[i].Score = ranked[i].Score
		out[i].Components.TidalBoost = ranked[i].Components.TidalBoost
	}
	return out
}

func (r *TidalReranker) RecordMemoryActivation(query string, scores []ActivationScore, now time.Time) {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.RecordMemoryActivation(query, tidalScoresFromActivation(scores), now)
}

func (r *TidalReranker) ObserveActivationFeedback(feedback ActivationFeedback) {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.ObserveActivationFeedback(tidal.Feedback{
		Query:   feedback.Query,
		QueryID: feedback.QueryID,
		Entry:   tidalNoteFromEntry(feedback.Entry),
		Signal:  feedback.Signal,
		Value:   feedback.Value,
		At:      feedback.At,
		Keys:    feedback.Keys,
	})
}

func (r *TidalReranker) ObserveFeedback(feedback TidalFeedback) {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.ObserveFeedback(tidal.TidalFeedback{
		Query:   feedback.Query,
		QueryID: feedback.QueryID,
		Entry:   tidalNoteFromEntry(feedback.Entry),
		Signal:  feedback.Signal,
		Value:   feedback.Value,
		At:      feedback.At,
		Keys:    feedback.Keys,
	})
}

func (r *TidalReranker) KernelSnapshots() []TidalKernelSnapshot {
	if r == nil || r.inner == nil {
		return nil
	}
	return kernelSnapshotsFromTidal(r.inner.KernelSnapshots())
}

func (r *TidalReranker) ApplyKernelSnapshots(snapshots []TidalKernelSnapshot) {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.ApplyKernelSnapshots(kernelSnapshotsToTidal(snapshots))
}

func (r *TidalReranker) StoreStats() (TidalStoreStats, error) {
	if r == nil || r.inner == nil {
		return TidalStoreStats{}, nil
	}
	stats, err := r.inner.StoreStats()
	return TidalStoreStats{
		QueryEvents:    stats.QueryEvents,
		RecallEvents:   stats.RecallEvents,
		FeedbackEvents: stats.FeedbackEvents,
		Kernels:        stats.Kernels,
	}, err
}

func (r *TidalReranker) Close() error {
	if r == nil || r.inner == nil {
		return nil
	}
	return r.inner.Close()
}

func tidalScoresFromActivation(scores []ActivationScore) []tidal.Score {
	out := make([]tidal.Score, len(scores))
	for i, score := range scores {
		out[i] = tidal.Score{
			EntryID: score.EntryID,
			Entry:   tidalNoteFromEntry(score.Entry),
			Score:   score.Score,
			Components: tidal.Components{
				GraphBoost: score.Components.GraphBoost,
				TidalBoost: score.Components.TidalBoost,
			},
		}
	}
	return out
}

func tidalNoteFromEntry(entry Entry) tidal.Note {
	return tidal.Note{
		ID:         entry.ID,
		Content:    entry.Content,
		Category:   entry.Category,
		Tier:       tidal.Tier(entry.Tier),
		Tags:       append([]string(nil), entry.Tags...),
		CreatedAt:  entry.CreatedAt,
		AccessedAt: entry.AccessedAt,
	}
}

func kernelSnapshotsFromTidal(in []tidal.TidalKernelSnapshot) []TidalKernelSnapshot {
	out := make([]TidalKernelSnapshot, len(in))
	for i, snapshot := range in {
		out[i] = TidalKernelSnapshot{
			Key:      snapshot.Key,
			Feature:  snapshot.Feature,
			BinEdges: snapshot.BinEdges,
			Weights:  snapshot.Weights,
			Counts:   snapshot.Counts,
		}
	}
	return out
}

func kernelSnapshotsToTidal(in []TidalKernelSnapshot) []tidal.TidalKernelSnapshot {
	out := make([]tidal.TidalKernelSnapshot, len(in))
	for i, snapshot := range in {
		out[i] = tidal.TidalKernelSnapshot{
			Key:      snapshot.Key,
			Feature:  snapshot.Feature,
			BinEdges: snapshot.BinEdges,
			Weights:  snapshot.Weights,
			Counts:   snapshot.Counts,
		}
	}
	return out
}
