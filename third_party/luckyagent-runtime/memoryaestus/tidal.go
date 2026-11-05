package memoryaestus

import (
	"time"

	sdk "github.com/yurika0211/aestus/memory"
	"github.com/yurika0211/luckyagent-tools/memory"
	"github.com/yurika0211/luckyagent-tools/memory/diagnostic"
)

type TidalStore struct{ inner *sdk.TidalStore }

func OpenTidalStore(path string) (*TidalStore, error) {
	inner, err := sdk.OpenTidalStore(path)
	if err != nil {
		return nil, err
	}
	return &TidalStore{inner: inner}, nil
}

func (s *TidalStore) Close() error { return s.inner.Close() }
func (s *TidalStore) Stats() (diagnostic.TidalStoreStats, error) {
	stats, err := s.inner.Stats()
	return fromSDKTidalStoreStats(stats), err
}

func (s *TidalStore) LoadKernels() ([]diagnostic.TidalKernelSnapshot, error) {
	kernels, err := s.inner.LoadKernels()
	return mapSlice(kernels, fromSDKTidalKernelSnapshot), err
}

type TidalMemoryReranker struct{ inner *sdk.TidalReranker }

func (s *Store) TidalStats() (diagnostic.TidalStoreStats, bool, error) {
	bridge, ok := s.inner.ActivationReranker().(sdkReranker)
	if !ok {
		return diagnostic.TidalStoreStats{}, false, nil
	}
	reporter, ok := bridge.host.(interface {
		StoreStats() (diagnostic.TidalStoreStats, error)
	})
	if !ok {
		return diagnostic.TidalStoreStats{}, false, nil
	}
	stats, err := reporter.StoreStats()
	return stats, true, err
}

func DefaultTidalRerankerConfig() diagnostic.TidalRerankerConfig {
	return fromSDKTidalRerankerConfig(sdk.DefaultTidalRerankerConfig())
}

func NewTidalMemoryReranker(config diagnostic.TidalRerankerConfig) *TidalMemoryReranker {
	return &TidalMemoryReranker{inner: sdk.NewTidalMemoryReranker(toSDKTidalRerankerConfig(config))}
}

func NewPersistentTidalMemoryReranker(config diagnostic.TidalRerankerConfig, store *TidalStore) (*TidalMemoryReranker, error) {
	var innerStore *sdk.TidalStore
	if store != nil {
		innerStore = store.inner
	}
	inner, err := sdk.NewPersistentTidalMemoryReranker(toSDKTidalRerankerConfig(config), innerStore)
	if err != nil {
		return nil, err
	}
	return &TidalMemoryReranker{inner: inner}, nil
}

func (r *TidalMemoryReranker) RerankMemoryActivations(query string, scores []memory.ActivationScore, now time.Time) []memory.ActivationScore {
	return mapSlice(r.inner.RerankMemoryActivations(query, mapSlice(scores, toSDKActivationScore), now), fromSDKActivationScore)
}
func (r *TidalMemoryReranker) ObserveActivationFeedback(feedback memory.ActivationFeedback) {
	r.inner.ObserveActivationFeedback(toSDKActivationFeedback(feedback))
}
func (r *TidalMemoryReranker) ObserveFeedback(feedback memory.TidalFeedback) {
	r.inner.ObserveFeedback(toSDKTidalFeedback(feedback))
}
func (r *TidalMemoryReranker) RecordMemoryActivation(query string, scores []memory.ActivationScore, now time.Time) {
	r.inner.RecordMemoryActivation(query, mapSlice(scores, toSDKActivationScore), now)
}
func (r *TidalMemoryReranker) KernelSnapshots() []diagnostic.TidalKernelSnapshot {
	return mapSlice(r.inner.KernelSnapshots(), fromSDKTidalKernelSnapshot)
}
func (r *TidalMemoryReranker) ApplyKernelSnapshots(snapshots []diagnostic.TidalKernelSnapshot) {
	r.inner.ApplyKernelSnapshots(mapSlice(snapshots, toSDKTidalKernelSnapshot))
}
func (r *TidalMemoryReranker) StoreStats() (diagnostic.TidalStoreStats, error) {
	stats, err := r.inner.StoreStats()
	return fromSDKTidalStoreStats(stats), err
}
func (r *TidalMemoryReranker) Close() error { return r.inner.Close() }

// Bridge optional reranker hooks without exposing upstream activation types.
type sdkReranker struct{ host memory.ActivationReranker }

func (r sdkReranker) RerankMemoryActivations(query string, scores []sdk.ActivationScore, now time.Time) []sdk.ActivationScore {
	return mapSlice(r.host.RerankMemoryActivations(query, mapSlice(scores, fromSDKActivationScore), now), toSDKActivationScore)
}
func (r sdkReranker) RecordMemoryActivation(query string, scores []sdk.ActivationScore, now time.Time) {
	if recorder, ok := r.host.(memory.ActivationEventRecorder); ok {
		recorder.RecordMemoryActivation(query, mapSlice(scores, fromSDKActivationScore), now)
	}
}
func (r sdkReranker) ObserveActivationFeedback(feedback sdk.ActivationFeedback) {
	if observer, ok := r.host.(memory.ActivationFeedbackObserver); ok {
		observer.ObserveActivationFeedback(fromSDKActivationFeedback(feedback))
	}
}
func (r sdkReranker) Close() error {
	if closer, ok := r.host.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
func (s *Store) SetActivationReranker(reranker memory.ActivationReranker) {
	if reranker == nil {
		s.inner.SetActivationReranker(nil)
		return
	}
	s.inner.SetActivationReranker(sdkReranker{host: reranker})
}
func (s *Store) ActivationReranker() memory.ActivationReranker {
	if bridge, ok := s.inner.ActivationReranker().(sdkReranker); ok {
		return bridge.host
	}
	return nil
}
