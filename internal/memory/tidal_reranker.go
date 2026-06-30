package memory

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// TidalRerankerConfig controls the conservative EWMA-based tidal reranker.
type TidalRerankerConfig struct {
	Beta         float64
	MaxBoost     float64
	LearningRate float64
	MinSamples   int
	Bins         []time.Duration
}

// DefaultTidalRerankerConfig returns a safe config that keeps learned scores
// secondary to the base activation score.
func DefaultTidalRerankerConfig() TidalRerankerConfig {
	return TidalRerankerConfig{
		Beta:         0.15,
		MaxBoost:     0.35,
		LearningRate: 0.20,
		MinSamples:   1,
		Bins: []time.Duration{
			10 * time.Minute,
			time.Hour,
			6 * time.Hour,
			24 * time.Hour,
			7 * 24 * time.Hour,
			30 * 24 * time.Hour,
			180 * 24 * time.Hour,
		},
	}
}

// TidalFeedback is weak supervision for a recalled memory.
type TidalFeedback struct {
	Query   string
	QueryID string
	Entry   Entry
	Signal  string
	Value   float64
	At      time.Time
	Keys    []string
}

// TidalKernelSnapshot is a read-only view of one learned response kernel.
type TidalKernelSnapshot struct {
	Key      string
	Feature  string
	BinEdges []time.Duration
	Weights  []float64
	Counts   []int
}

type tidalKernel struct {
	weights []float64
	counts  []int
}

// TidalMemoryReranker learns coarse response kernels over memory age buckets and
// applies them as a post-recall score adjustment.
type TidalMemoryReranker struct {
	mu      sync.RWMutex
	config  TidalRerankerConfig
	kernels map[string]*tidalKernel
	store   *TidalStore
}

// NewTidalMemoryReranker creates a disabled-by-default-safe reranker. With no
// feedback, RerankMemoryActivations returns the original scores unchanged.
func NewTidalMemoryReranker(config TidalRerankerConfig) *TidalMemoryReranker {
	config = normalizeTidalRerankerConfig(config)
	return &TidalMemoryReranker{
		config:  config,
		kernels: make(map[string]*tidalKernel),
	}
}

// NewPersistentTidalMemoryReranker creates a reranker backed by a SQLite tidal
// store and restores previously learned kernels.
func NewPersistentTidalMemoryReranker(config TidalRerankerConfig, store *TidalStore) (*TidalMemoryReranker, error) {
	r := NewTidalMemoryReranker(config)
	r.store = store
	if store == nil {
		return r, nil
	}
	snapshots, err := store.LoadKernels()
	if err != nil {
		return nil, err
	}
	r.ApplyKernelSnapshots(snapshots)
	return r, nil
}

func normalizeTidalRerankerConfig(config TidalRerankerConfig) TidalRerankerConfig {
	def := DefaultTidalRerankerConfig()
	if config.Beta <= 0 {
		config.Beta = def.Beta
	}
	if config.MaxBoost <= 0 {
		config.MaxBoost = def.MaxBoost
	}
	if config.LearningRate <= 0 || config.LearningRate > 1 {
		config.LearningRate = def.LearningRate
	}
	if config.MinSamples <= 0 {
		config.MinSamples = def.MinSamples
	}
	if len(config.Bins) == 0 {
		config.Bins = append([]time.Duration(nil), def.Bins...)
	} else {
		config.Bins = append([]time.Duration(nil), config.Bins...)
		sort.Slice(config.Bins, func(i, j int) bool {
			return config.Bins[i] < config.Bins[j]
		})
	}
	return config
}

// RerankMemoryActivations implements ActivationReranker.
func (r *TidalMemoryReranker) RerankMemoryActivations(query string, scores []ActivationScore, now time.Time) []ActivationScore {
	if r == nil || len(scores) == 0 {
		return scores
	}
	if now.IsZero() {
		now = time.Now()
	}

	out := make([]ActivationScore, len(scores))
	copy(out, scores)

	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range out {
		boost := r.boostLocked(query, out[i].Entry, now)
		if boost == 0 {
			continue
		}
		out[i].Components.TidalBoost = boost
		multiplier := 1 + r.config.Beta*boost
		if multiplier < 0.05 {
			multiplier = 0.05
		}
		out[i].Score *= multiplier
	}
	return out
}

// ObserveFeedback updates response kernels with weak feedback.
func (r *TidalMemoryReranker) ObserveFeedback(feedback TidalFeedback) {
	if r == nil {
		return
	}
	if feedback.At.IsZero() {
		feedback.At = time.Now()
	}
	value := clamp(feedback.Value, -1, 1)
	if value == 0 {
		return
	}
	feedback.Value = value

	r.mu.Lock()
	bin := r.binLocked(memoryDelay(feedback.Entry, feedback.At))
	keys := r.keysForFeedbackLocked(feedback)
	for _, key := range keys {
		kernel := r.kernelLocked(key)
		old := kernel.weights[bin]
		kernel.weights[bin] = old*(1-r.config.LearningRate) + value*r.config.LearningRate
		kernel.counts[bin]++
	}
	snapshots := r.kernelSnapshotsLocked()
	store := r.store
	r.mu.Unlock()

	if store != nil {
		store.RecordFeedback(feedback)
		_ = store.SaveKernels(snapshots)
	}
}

// ObserveActivationFeedback implements ActivationFeedbackObserver.
func (r *TidalMemoryReranker) ObserveActivationFeedback(feedback ActivationFeedback) {
	if r == nil {
		return
	}
	r.ObserveFeedback(TidalFeedback{
		Query:   feedback.Query,
		QueryID: feedback.QueryID,
		Entry:   feedback.Entry,
		Signal:  feedback.Signal,
		Value:   feedback.Value,
		At:      feedback.At,
		Keys:    feedback.Keys,
	})
}

// KernelSnapshots returns learned kernels for diagnostics and tests.
func (r *TidalMemoryReranker) KernelSnapshots() []TidalKernelSnapshot {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.kernelSnapshotsLocked()
}

func (r *TidalMemoryReranker) kernelSnapshotsLocked() []TidalKernelSnapshot {
	out := make([]TidalKernelSnapshot, 0, len(r.kernels))
	for key, kernel := range r.kernels {
		out = append(out, TidalKernelSnapshot{
			Key:      key,
			Feature:  tidalFeatureName(key),
			BinEdges: append([]time.Duration(nil), r.config.Bins...),
			Weights:  append([]float64(nil), kernel.weights...),
			Counts:   append([]int(nil), kernel.counts...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

// ApplyKernelSnapshots restores learned kernels.
func (r *TidalMemoryReranker) ApplyKernelSnapshots(snapshots []TidalKernelSnapshot) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	size := len(r.config.Bins) + 1
	for _, snapshot := range snapshots {
		key := normalizeTidalKey(snapshot.Key)
		if key == "" {
			continue
		}
		kernel := &tidalKernel{
			weights: make([]float64, size),
			counts:  make([]int, size),
		}
		copy(kernel.weights, snapshot.Weights)
		copy(kernel.counts, snapshot.Counts)
		r.kernels[key] = kernel
	}
}

// RecordMemoryActivation implements ActivationEventRecorder.
func (r *TidalMemoryReranker) RecordMemoryActivation(query string, scores []ActivationScore, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	copied := make([]ActivationScore, len(scores))
	copy(copied, scores)
	r.store.RecordActivation(query, copied, now)
}

// StoreStats returns persisted telemetry counts when a backing store is present.
func (r *TidalMemoryReranker) StoreStats() (TidalStoreStats, error) {
	if r == nil || r.store == nil {
		return TidalStoreStats{}, nil
	}
	return r.store.Stats()
}

// Close closes the persistent backing store, when one is attached.
func (r *TidalMemoryReranker) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	store := r.store
	r.store = nil
	r.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.Close()
}

func (r *TidalMemoryReranker) boostLocked(query string, entry Entry, now time.Time) float64 {
	bin := r.binLocked(memoryDelay(entry, now))
	keys := r.keysForQueryEntryLocked(query, entry)
	if len(keys) == 0 {
		return 0
	}

	var sum float64
	var weightSum float64
	for _, key := range keys {
		kernel := r.kernels[key]
		if kernel == nil || bin >= len(kernel.weights) || kernel.counts[bin] < r.config.MinSamples {
			continue
		}
		weight := tidalKeyWeight(key)
		sum += kernel.weights[bin] * weight
		weightSum += weight
	}
	if weightSum == 0 {
		return 0
	}
	return clamp(sum/weightSum, -r.config.MaxBoost, r.config.MaxBoost)
}

func (r *TidalMemoryReranker) keysForFeedbackLocked(feedback TidalFeedback) []string {
	var keys []string
	if feedback.Value < 0 {
		keys = r.entryFeatureKeysLocked(feedback.Entry)
	} else {
		keys = r.keysForQueryEntryLocked(feedback.Query, feedback.Entry)
	}
	for _, key := range feedback.Keys {
		if normalized := normalizeTidalKey(key); normalized != "" {
			keys = append(keys, normalized)
		}
	}
	return dedupeStrings(keys)
}

func (r *TidalMemoryReranker) entryFeatureKeysLocked(entry Entry) []string {
	keys := []string{
		"tier:" + entry.Tier.String(),
	}
	if category := normalizeTidalValue(entry.Category); category != "" {
		keys = append(keys, "category:"+category)
	}
	for _, tag := range entry.Tags {
		if normalized := normalizeTidalValue(tag); normalized != "" {
			keys = append(keys, "tag:"+normalized)
		}
	}
	return dedupeStrings(keys)
}

func (r *TidalMemoryReranker) keysForQueryEntryLocked(query string, entry Entry) []string {
	intents := inferTidalIntentTags(query)
	keys := r.entryFeatureKeysLocked(entry)
	for _, intent := range intents {
		keys = append(keys, "intent:"+intent)
	}
	keys = append(keys, tidalIntentPairKeys(intents, entry)...)
	return dedupeStrings(keys)
}

func (r *TidalMemoryReranker) kernelLocked(key string) *tidalKernel {
	key = normalizeTidalKey(key)
	if key == "" {
		return nil
	}
	if kernel := r.kernels[key]; kernel != nil {
		return kernel
	}
	size := len(r.config.Bins) + 1
	kernel := &tidalKernel{
		weights: make([]float64, size),
		counts:  make([]int, size),
	}
	r.kernels[key] = kernel
	return kernel
}

func (r *TidalMemoryReranker) binLocked(delay time.Duration) int {
	if delay < 0 {
		delay = 0
	}
	for i, upper := range r.config.Bins {
		if delay <= upper {
			return i
		}
	}
	return len(r.config.Bins)
}

func memoryDelay(entry Entry, now time.Time) time.Duration {
	ref := entry.AccessedAt
	if ref.IsZero() || ref.Before(entry.CreatedAt) {
		ref = entry.CreatedAt
	}
	if ref.IsZero() {
		return 0
	}
	return now.Sub(ref)
}

func inferTidalIntentTags(query string) []string {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return nil
	}
	var intents []string
	add := func(intent string, needles ...string) {
		for _, needle := range needles {
			if strings.Contains(lower, needle) {
				intents = append(intents, intent)
				return
			}
		}
	}
	add("code", "code", "test", "bug", "fix", "compile", "代码", "测试", "修复", "编译")
	add("project", "project", "current work", "implementation", "项目", "当前工作", "实现")
	add("rag", "rag", "graph", "index", "retrieval", "索引", "检索", "图记忆")
	add("memory", "memory", "记忆", "recall", "召回")
	add("tool", "tool", "config", "api", "工具", "配置")
	add("config", "config", "setting", "settings", "配置", "设置")
	add("transcription", "transcription", "transcribe", "asr", "voice", "audio", "语音", "转录", "音频")
	add("family", "daughter", "family", "女儿", "孩子", "家人")
	add("health", "allergy", "health", "pollen", "过敏", "健康")
	add("weather", "weather", "air quality", "天气", "空气质量")
	return dedupeStrings(intents)
}

func tidalIntentPairKeys(intents []string, entry Entry) []string {
	pairs := tidalWhitelistedIntentPairs(intents)
	if len(pairs) == 0 {
		return nil
	}
	category := normalizeTidalValue(entry.Category)
	tags := make([]string, 0, len(entry.Tags))
	for _, tag := range entry.Tags {
		if normalized := normalizeTidalValue(tag); normalized != "" {
			tags = append(tags, normalized)
		}
	}
	var keys []string
	for _, pair := range pairs {
		keys = append(keys, "pair:"+pair)
		if category != "" {
			keys = append(keys, "pair_category:"+pair+"+"+category)
		}
		for _, tag := range tags {
			keys = append(keys, "pair_tag:"+pair+"+"+tag)
		}
	}
	return keys
}

func tidalWhitelistedIntentPairs(intents []string) []string {
	if len(intents) < 2 {
		return nil
	}
	seen := make(map[string]bool, len(intents))
	for _, intent := range intents {
		seen[intent] = true
	}
	allowed := [][2]string{
		{"code", "project"},
		{"family", "health"},
		{"config", "tool"},
		{"memory", "rag"},
		{"tool", "transcription"},
	}
	var pairs []string
	for _, pair := range allowed {
		left, right := pair[0], pair[1]
		if seen[left] && seen[right] {
			if left > right {
				left, right = right, left
			}
			pairs = append(pairs, left+"+"+right)
		}
	}
	return pairs
}

func tidalFeatureName(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if idx := strings.Index(key, ":"); idx > 0 {
		return key[:idx]
	}
	return key
}

func tidalKeyWeight(key string) float64 {
	feature := tidalFeatureName(key)
	switch feature {
	case "intent", "pair":
		return 0.35
	case "tier":
		return 0.50
	case "category", "tag", "pair_category", "pair_tag":
		return 1.0
	default:
		return 0.75
	}
}

func normalizeTidalKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	if !strings.Contains(key, ":") {
		return normalizeTidalValue(key)
	}
	parts := strings.SplitN(key, ":", 2)
	left := normalizeTidalValue(parts[0])
	right := normalizeTidalValue(parts[1])
	if left == "" || right == "" {
		return ""
	}
	return left + ":" + right
}

func normalizeTidalValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), "_")
	return value
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func clamp(value, minValue, maxValue float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
