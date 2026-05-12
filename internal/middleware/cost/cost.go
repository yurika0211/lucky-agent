// Package cost provides API cost tracking and budget management for LuckyHarness.
// It records per-call token usage and cost, aggregates by provider/model/session,
// and supports budget thresholds with alert callbacks.
package cost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// CT-1: CostRecord + PriceTable
// ---------------------------------------------------------------------------

// CostRecord represents a single API call cost entry.
type CostRecord struct {
	ID               string            `json:"id"`
	Timestamp        time.Time         `json:"timestamp"`
	Provider         string            `json:"provider"`
	Model            string            `json:"model"`
	SessionID        string            `json:"sessionId,omitempty"`
	PromptTokens     int               `json:"promptTokens"`
	CompletionTokens int               `json:"completionTokens"`
	TotalTokens      int               `json:"totalTokens"`
	CostUSD          float64           `json:"costUsd"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// PriceEntry defines the pricing for a model.
type PriceEntry struct {
	Provider        string  `json:"provider" yaml:"provider"`
	Model           string  `json:"model" yaml:"model"`
	PromptPrice     float64 `json:"promptPrice" yaml:"promptPrice"`         // $/1K prompt tokens
	CompletionPrice float64 `json:"completionPrice" yaml:"completionPrice"` // $/1K completion tokens
}

// PriceTable maps "provider/model" to pricing.
type PriceTable struct {
	mu     sync.RWMutex
	prices map[string]PriceEntry // key: "provider/model"
}

// NewPriceTable creates a price table with default pricing.
func NewPriceTable() *PriceTable {
	pt := &PriceTable{
		prices: make(map[string]PriceEntry),
	}
	pt.loadDefaults()
	return pt
}

// loadDefaults sets common model pricing.
func (pt *PriceTable) loadDefaults() {
	defaults := []PriceEntry{
		{Provider: "openai", Model: "gpt-5.4-mini", PromptPrice: 0.0025, CompletionPrice: 0.01},
		{Provider: "openai", Model: "gpt-5.4-mini", PromptPrice: 0.00015, CompletionPrice: 0.0006},
		{Provider: "openai", Model: "gpt-5.4-mini", PromptPrice: 0.01, CompletionPrice: 0.03},
		{Provider: "openai", Model: "gpt-5.4-mini", PromptPrice: 0.0005, CompletionPrice: 0.0015},
		{Provider: "anthropic", Model: "claude-3.5-sonnet", PromptPrice: 0.003, CompletionPrice: 0.015},
		{Provider: "anthropic", Model: "claude-3-opus", PromptPrice: 0.015, CompletionPrice: 0.075},
		{Provider: "anthropic", Model: "claude-3-haiku", PromptPrice: 0.00025, CompletionPrice: 0.00125},
		{Provider: "ollama", Model: "llama3", PromptPrice: 0, CompletionPrice: 0},
		{Provider: "ollama", Model: "mistral", PromptPrice: 0, CompletionPrice: 0},
		{Provider: "icompify", Model: "glm-5.1", PromptPrice: 0.001, CompletionPrice: 0.002},
	}
	for _, d := range defaults {
		pt.prices[d.Provider+"/"+d.Model] = d
	}
}

// Set sets a price entry.
func (pt *PriceTable) Set(entry PriceEntry) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.prices[entry.Provider+"/"+entry.Model] = entry
}

// Get retrieves a price entry.
func (pt *PriceTable) Get(provider, model string) (PriceEntry, bool) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	e, ok := pt.prices[provider+"/"+model]
	return e, ok
}

// List returns all price entries.
func (pt *PriceTable) List() []PriceEntry {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	entries := make([]PriceEntry, 0, len(pt.prices))
	for _, e := range pt.prices {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Provider+"/"+entries[i].Model < entries[j].Provider+"/"+entries[j].Model
	})
	return entries
}

// CalculateCost computes the cost for a given token usage.
func (pt *PriceTable) CalculateCost(provider, model string, promptTokens, completionTokens int) float64 {
	entry, ok := pt.Get(provider, model)
	if !ok {
		// Unknown model: estimate $0.002/1K tokens as fallback
		return float64(promptTokens+completionTokens) * 0.002 / 1000
	}
	cost := float64(promptTokens)*entry.PromptPrice/1000 +
		float64(completionTokens)*entry.CompletionPrice/1000
	return cost
}

// ---------------------------------------------------------------------------
// CT-2: CostStore
// ---------------------------------------------------------------------------

// CostSummary is an aggregated cost summary.
type CostSummary struct {
	Provider         string  `json:"provider,omitempty"`
	Model            string  `json:"model,omitempty"`
	SessionID        string  `json:"sessionId,omitempty"`
	Period           string  `json:"period,omitempty"` // "today", "week", "month", "all"
	TotalCalls       int     `json:"totalCalls"`
	TotalTokens      int     `json:"totalTokens"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	TotalCostUSD     float64 `json:"totalCostUsd"`
}

// CostStore stores and queries cost records.
type CostStore struct {
	mu       sync.RWMutex
	records  []CostRecord
	prices   *PriceTable
	filePath string // persistence path
}

// NewCostStore creates a new cost store.
func NewCostStore(prices *PriceTable) *CostStore {
	return &CostStore{
		records: make([]CostRecord, 0),
		prices:  prices,
	}
}

// SetFilePath sets the JSON persistence path.
func (s *CostStore) SetFilePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filePath = path
}

// Record records a new cost entry.
func (s *CostStore) Record(rec CostRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Auto-calculate cost if not set
	if rec.CostUSD == 0 && rec.TotalTokens > 0 {
		rec.CostUSD = s.prices.CalculateCost(rec.Provider, rec.Model, rec.PromptTokens, rec.CompletionTokens)
	}

	s.records = append(s.records, rec)

	// Persist if path is set
	if s.filePath != "" {
		s.persistLocked()
	}

	return nil
}

// RecordCall is a convenience method to record a call.
func (s *CostStore) RecordCall(id, provider, model, sessionID string, promptTokens, completionTokens int) CostRecord {
	total := promptTokens + completionTokens
	cost := s.prices.CalculateCost(provider, model, promptTokens, completionTokens)
	rec := CostRecord{
		ID:               id,
		Timestamp:        time.Now(),
		Provider:         provider,
		Model:            model,
		SessionID:        sessionID,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      total,
		CostUSD:          cost,
	}
	_ = s.Record(rec)
	return rec
}

// Summary returns aggregated cost summary with optional filters.
func (s *CostStore) Summary(opts SummaryOptions) CostSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	summary := CostSummary{
		Provider:  opts.Provider,
		Model:     opts.Model,
		SessionID: opts.SessionID,
		Period:    opts.Period,
	}

	for _, r := range s.records {
		if !s.matchRecord(r, opts) {
			continue
		}
		summary.TotalCalls++
		summary.TotalTokens += r.TotalTokens
		summary.PromptTokens += r.PromptTokens
		summary.CompletionTokens += r.CompletionTokens
		summary.TotalCostUSD += r.CostUSD
	}

	return summary
}

// ByProvider returns cost summaries grouped by provider.
func (s *CostStore) ByProvider(period string) map[string]CostSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]CostSummary)
	for _, r := range s.records {
		if !matchPeriod(r.Timestamp, period) {
			continue
		}
		sum, ok := result[r.Provider]
		if !ok {
			sum = CostSummary{Provider: r.Provider, Period: period}
		}
		sum.TotalCalls++
		sum.TotalTokens += r.TotalTokens
		sum.PromptTokens += r.PromptTokens
		sum.CompletionTokens += r.CompletionTokens
		sum.TotalCostUSD += r.CostUSD
		result[r.Provider] = sum
	}
	return result
}

// ByModel returns cost summaries grouped by model.
func (s *CostStore) ByModel(period string) map[string]CostSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]CostSummary)
	for _, r := range s.records {
		if !matchPeriod(r.Timestamp, period) {
			continue
		}
		key := r.Provider + "/" + r.Model
		sum, ok := result[key]
		if !ok {
			sum = CostSummary{Provider: r.Provider, Model: r.Model, Period: period}
		}
		sum.TotalCalls++
		sum.TotalTokens += r.TotalTokens
		sum.PromptTokens += r.PromptTokens
		sum.CompletionTokens += r.CompletionTokens
		sum.TotalCostUSD += r.CostUSD
		result[key] = sum
	}
	return result
}

// Recent returns the N most recent cost records.
func (s *CostStore) Recent(n int) []CostRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if n > len(s.records) {
		n = len(s.records)
	}
	result := make([]CostRecord, n)
	copy(result, s.records[len(s.records)-n:])
	return result
}

// Load loads cost records from a JSON file.
func (s *CostStore) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read file: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := json.Unmarshal(data, &s.records); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	s.filePath = path
	return nil
}

// Save persists cost records to a JSON file.
func (s *CostStore) Save(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	return os.WriteFile(path, data, 0o600)
}

// persistLocked saves to the configured file path (must hold lock).
func (s *CostStore) persistLocked() {
	if s.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.filePath, data, 0o600)
}

// SummaryOptions defines filters for cost summary queries.
type SummaryOptions struct {
	Provider  string
	Model     string
	SessionID string
	Period    string // "today", "week", "month", "all", ""
}

func (s *CostStore) matchRecord(r CostRecord, opts SummaryOptions) bool {
	if opts.Provider != "" && r.Provider != opts.Provider {
		return false
	}
	if opts.Model != "" && r.Model != opts.Model {
		return false
	}
	if opts.SessionID != "" && r.SessionID != opts.SessionID {
		return false
	}
	if !matchPeriod(r.Timestamp, opts.Period) {
		return false
	}
	return true
}

func matchPeriod(t time.Time, period string) bool {
	now := time.Now()
	switch period {
	case "", "all":
		return true
	case "today":
		return t.Format("2006-01-02") == now.Format("2006-01-02")
	case "week":
		weekAgo := now.AddDate(0, 0, -7)
		return t.After(weekAgo) || t.Equal(weekAgo)
	case "month":
		monthAgo := now.AddDate(0, -1, 0)
		return t.After(monthAgo) || t.Equal(monthAgo)
	default:
		return true
	}
}

// ---------------------------------------------------------------------------
// CT-3: BudgetManager
// ---------------------------------------------------------------------------

// BudgetLevel represents budget alert severity.
type BudgetLevel string

const (
	BudgetLevelWarning  BudgetLevel = "warning"  // 80% of budget
	BudgetLevelCritical BudgetLevel = "critical" // 100% of budget
)

// BudgetAlert is triggered when spending exceeds a threshold.
type BudgetAlert struct {
	Level      BudgetLevel `json:"level"`
	Period     string      `json:"period"`
	BudgetUSD  float64     `json:"budgetUsd"`
	SpentUSD   float64     `json:"spentUsd"`
	Percentage float64     `json:"percentage"`
	Timestamp  time.Time   `json:"timestamp"`
}

// AlertHandler is called when a budget alert fires.
type AlertHandler func(alert BudgetAlert)

// BudgetConfig defines a budget for a time period.
type BudgetConfig struct {
	Period      string  `json:"period" yaml:"period"`                         // "daily", "weekly", "monthly"
	LimitUSD    float64 `json:"limitUsd" yaml:"limitUsd"`                     // total budget
	WarningPct  float64 `json:"warningPct" yaml:"warningPct"`                 // warning threshold % (default 80)
	CriticalPct float64 `json:"criticalPct" yaml:"criticalPct"`               // critical threshold % (default 100)
	Provider    string  `json:"provider,omitempty" yaml:"provider,omitempty"` // optional provider filter
}

// BudgetManager manages budgets and alerts.
type BudgetManager struct {
	mu       sync.RWMutex
	configs  map[string]BudgetConfig // key: period or "provider/period"
	store    *CostStore
	handlers []AlertHandler
	fired    map[string]bool // track already-fired alerts to avoid duplicates
}

// NewBudgetManager creates a new budget manager.
func NewBudgetManager(store *CostStore) *BudgetManager {
	return &BudgetManager{
		configs: make(map[string]BudgetConfig),
		store:   store,
		fired:   make(map[string]bool),
	}
}

// SetBudget sets a budget configuration.
func (bm *BudgetManager) SetBudget(cfg BudgetConfig) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if cfg.WarningPct == 0 {
		cfg.WarningPct = 80
	}
	if cfg.CriticalPct == 0 {
		cfg.CriticalPct = 100
	}

	key := budgetKey(cfg)
	bm.configs[key] = cfg
	// Reset fired state for this budget
	delete(bm.fired, key+":warning")
	delete(bm.fired, key+":critical")
}

// GetBudget retrieves a budget configuration.
func (bm *BudgetManager) GetBudget(period string) (BudgetConfig, bool) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	cfg, ok := bm.configs[period]
	return cfg, ok
}

// ListBudgets returns all budget configurations.
func (bm *BudgetManager) ListBudgets() []BudgetConfig {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	configs := make([]BudgetConfig, 0, len(bm.configs))
	for _, cfg := range bm.configs {
		configs = append(configs, cfg)
	}
	return configs
}

// RemoveBudget removes a budget configuration.
func (bm *BudgetManager) RemoveBudget(period string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	delete(bm.configs, period)
}

// OnAlert registers an alert handler.
func (bm *BudgetManager) OnAlert(handler AlertHandler) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.handlers = append(bm.handlers, handler)
}

// Check evaluates all budgets and fires alerts if thresholds are exceeded.
func (bm *BudgetManager) Check() []BudgetAlert {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	var alerts []BudgetAlert

	for key, cfg := range bm.configs {
		periodMap := map[string]string{
			"daily":   "today",
			"weekly":  "week",
			"monthly": "month",
		}
		queryPeriod := periodMap[cfg.Period]
		if queryPeriod == "" {
			queryPeriod = cfg.Period
		}

		summary := bm.store.Summary(SummaryOptions{
			Provider: cfg.Provider,
			Period:   queryPeriod,
		})

		pct := 0.0
		if cfg.LimitUSD > 0 {
			pct = (summary.TotalCostUSD / cfg.LimitUSD) * 100
		}

		// Check critical
		if pct >= cfg.CriticalPct {
			alertKey := key + ":critical"
			if !bm.fired[alertKey] {
				alert := BudgetAlert{
					Level:      BudgetLevelCritical,
					Period:     cfg.Period,
					BudgetUSD:  cfg.LimitUSD,
					SpentUSD:   summary.TotalCostUSD,
					Percentage: pct,
					Timestamp:  time.Now(),
				}
				alerts = append(alerts, alert)
				bm.fired[alertKey] = true
				for _, h := range bm.handlers {
					h(alert)
				}
			}
		}

		// Check warning
		if pct >= cfg.WarningPct && pct < cfg.CriticalPct {
			alertKey := key + ":warning"
			if !bm.fired[alertKey] {
				alert := BudgetAlert{
					Level:      BudgetLevelWarning,
					Period:     cfg.Period,
					BudgetUSD:  cfg.LimitUSD,
					SpentUSD:   summary.TotalCostUSD,
					Percentage: pct,
					Timestamp:  time.Now(),
				}
				alerts = append(alerts, alert)
				bm.fired[alertKey] = true
				for _, h := range bm.handlers {
					h(alert)
				}
			}
		}
	}

	return alerts
}

// ResetAlerts resets fired alert state (e.g., at period boundaries).
func (bm *BudgetManager) ResetAlerts() {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.fired = make(map[string]bool)
}

// Status returns the current budget status for all configured budgets.
func (bm *BudgetManager) Status() []BudgetStatus {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	var statuses []BudgetStatus
	for _, cfg := range bm.configs {
		periodMap := map[string]string{
			"daily":   "today",
			"weekly":  "week",
			"monthly": "month",
		}
		queryPeriod := periodMap[cfg.Period]
		if queryPeriod == "" {
			queryPeriod = cfg.Period
		}

		summary := bm.store.Summary(SummaryOptions{
			Provider: cfg.Provider,
			Period:   queryPeriod,
		})

		pct := 0.0
		if cfg.LimitUSD > 0 {
			pct = (summary.TotalCostUSD / cfg.LimitUSD) * 100
		}

		status := BudgetStatus{
			Config:     cfg,
			SpentUSD:   summary.TotalCostUSD,
			Percentage: pct,
			Remaining:  cfg.LimitUSD - summary.TotalCostUSD,
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// BudgetStatus shows current spending against a budget.
type BudgetStatus struct {
	Config     BudgetConfig `json:"config"`
	SpentUSD   float64      `json:"spentUsd"`
	Percentage float64      `json:"percentage"`
	Remaining  float64      `json:"remaining"`
}

func budgetKey(cfg BudgetConfig) string {
	if cfg.Provider != "" {
		return cfg.Provider + "/" + cfg.Period
	}
	return cfg.Period
}
