package cost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// CT-1: PriceTable Tests
// ---------------------------------------------------------------------------

func TestPriceTableDefaults(t *testing.T) {
	pt := NewPriceTable()
	entries := pt.List()
	if len(entries) == 0 {
		t.Error("expected default price entries")
	}
}

func TestPriceTableGet(t *testing.T) {
	pt := NewPriceTable()
	entry, ok := pt.Get("openai", "gpt-5.4-mini")
	if !ok {
		t.Error("expected to find gpt-5.4-mini pricing")
	}
	if entry.PromptPrice != 0.0025 {
		t.Errorf("expected prompt price 0.0025, got %f", entry.PromptPrice)
	}
}

func TestPriceTableGetNotFound(t *testing.T) {
	pt := NewPriceTable()
	_, ok := pt.Get("unknown", "model")
	if ok {
		t.Error("expected not found for unknown model")
	}
}

func TestPriceTableSet(t *testing.T) {
	pt := NewPriceTable()
	pt.Set(PriceEntry{Provider: "custom", Model: "my-model", PromptPrice: 0.01, CompletionPrice: 0.05})
	entry, ok := pt.Get("custom", "my-model")
	if !ok {
		t.Error("expected to find custom pricing")
	}
	if entry.PromptPrice != 0.01 {
		t.Errorf("expected 0.01, got %f", entry.PromptPrice)
	}
}

func TestCalculateCost(t *testing.T) {
	pt := NewPriceTable()
	// gpt-5.4-mini: prompt $0.0025/1K, completion $0.01/1K
	cost := pt.CalculateCost("openai", "gpt-5.4-mini", 1000, 500)
	expected := 1.0*0.0025 + 0.5*0.01 // 0.0025 + 0.005 = 0.0075
	if fmt.Sprintf("%.6f", cost) != fmt.Sprintf("%.6f", expected) {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}

func TestCalculateCostUnknownModel(t *testing.T) {
	pt := NewPriceTable()
	cost := pt.CalculateCost("unknown", "model", 1000, 1000)
	// Fallback: $0.002/1K tokens
	expected := 2000 * 0.002 / 1000
	if fmt.Sprintf("%.6f", cost) != fmt.Sprintf("%.6f", expected) {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}

func TestCalculateCostFreeModel(t *testing.T) {
	pt := NewPriceTable()
	cost := pt.CalculateCost("ollama", "llama3", 10000, 5000)
	if cost != 0 {
		t.Errorf("expected 0 for free model, got %f", cost)
	}
}

// ---------------------------------------------------------------------------
// CT-2: CostStore Tests
// ---------------------------------------------------------------------------

func TestCostStoreRecord(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)

	rec := store.RecordCall("call-1", "openai", "gpt-5.4-mini", "sess-1", 1000, 500)
	if rec.ID != "call-1" {
		t.Errorf("expected ID call-1, got %s", rec.ID)
	}
	if rec.TotalTokens != 1500 {
		t.Errorf("expected 1500 total tokens, got %d", rec.TotalTokens)
	}
	if rec.CostUSD <= 0 {
		t.Error("expected non-zero cost")
	}
}

func TestCostStoreSummary(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)
	store.RecordCall("c2", "openai", "gpt-5.4-mini", "s1", 2000, 1000)
	store.RecordCall("c3", "anthropic", "claude-3.5-sonnet", "s2", 500, 250)

	summary := store.Summary(SummaryOptions{Period: "all"})
	if summary.TotalCalls != 3 {
		t.Errorf("expected 3 calls, got %d", summary.TotalCalls)
	}
	if summary.TotalTokens != 5250 {
		t.Errorf("expected 5250 tokens, got %d", summary.TotalTokens)
	}
	if summary.TotalCostUSD <= 0 {
		t.Error("expected non-zero total cost")
	}
}

func TestCostStoreSummaryByProvider(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)
	store.RecordCall("c2", "anthropic", "claude-3.5-sonnet", "s2", 500, 250)

	summary := store.Summary(SummaryOptions{Provider: "openai", Period: "all"})
	if summary.TotalCalls != 1 {
		t.Errorf("expected 1 call, got %d", summary.TotalCalls)
	}
}

func TestCostStoreSummaryByModel(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)
	store.RecordCall("c2", "openai", "gpt-5.4-mini-mini", "s2", 500, 250)

	summary := store.Summary(SummaryOptions{Model: "gpt-5.4-mini", Period: "all"})
	if summary.TotalCalls != 1 {
		t.Errorf("expected 1 call, got %d", summary.TotalCalls)
	}
}

func TestCostStoreSummaryBySession(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)
	store.RecordCall("c2", "openai", "gpt-5.4-mini", "s2", 500, 250)

	summary := store.Summary(SummaryOptions{SessionID: "s1", Period: "all"})
	if summary.TotalCalls != 1 {
		t.Errorf("expected 1 call, got %d", summary.TotalCalls)
	}
}

func TestCostStoreByProvider(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)
	store.RecordCall("c2", "anthropic", "claude-3.5-sonnet", "s2", 500, 250)

	byProvider := store.ByProvider("all")
	if len(byProvider) != 2 {
		t.Errorf("expected 2 providers, got %d", len(byProvider))
	}
	if byProvider["openai"].TotalCalls != 1 {
		t.Errorf("expected 1 openai call, got %d", byProvider["openai"].TotalCalls)
	}
}

func TestCostStoreByModel(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)
	store.RecordCall("c2", "openai", "gpt-5.4-mini-mini", "s2", 500, 250)

	byModel := store.ByModel("all")
	if len(byModel) != 2 {
		t.Errorf("expected 2 models, got %d", len(byModel))
	}
}

func TestCostStoreRecent(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)
	store.RecordCall("c2", "openai", "gpt-5.4-mini", "s2", 2000, 1000)
	store.RecordCall("c3", "openai", "gpt-5.4-mini", "s3", 500, 250)

	recent := store.Recent(2)
	if len(recent) != 2 {
		t.Errorf("expected 2 records, got %d", len(recent))
	}
	if recent[0].ID != "c2" {
		t.Errorf("expected c2, got %s", recent[0].ID)
	}
}

func TestCostStorePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "costs.json")

	pt := NewPriceTable()
	store := NewCostStore(pt)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)
	store.RecordCall("c2", "anthropic", "claude-3.5-sonnet", "s2", 500, 250)

	// Save
	if err := store.Save(path); err != nil {
		t.Fatalf("save error: %v", err)
	}

	// Load into new store
	store2 := NewCostStore(pt)
	if err := store2.Load(path); err != nil {
		t.Fatalf("load error: %v", err)
	}

	summary := store2.Summary(SummaryOptions{Period: "all"})
	if summary.TotalCalls != 2 {
		t.Errorf("expected 2 calls after load, got %d", summary.TotalCalls)
	}
}

func TestCostStoreAutoPersist(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "costs.json")

	pt := NewPriceTable()
	store := NewCostStore(pt)
	store.SetFilePath(path)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)

	// Verify file was written
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
	var records []CostRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record in file, got %d", len(records))
	}
}

func TestCostStoreSummaryToday(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)

	summary := store.Summary(SummaryOptions{Period: "today"})
	if summary.TotalCalls != 1 {
		t.Errorf("expected 1 call today, got %d", summary.TotalCalls)
	}
}

// ---------------------------------------------------------------------------
// CT-3: BudgetManager Tests
// ---------------------------------------------------------------------------

func TestBudgetManagerSetAndGet(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	cfg := BudgetConfig{Period: "daily", LimitUSD: 10.0}
	bm.SetBudget(cfg)

	got, ok := bm.GetBudget("daily")
	if !ok {
		t.Error("expected to find budget")
	}
	if got.LimitUSD != 10.0 {
		t.Errorf("expected 10.0, got %f", got.LimitUSD)
	}
}

func TestBudgetManagerDefaults(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	cfg := BudgetConfig{Period: "daily", LimitUSD: 10.0}
	bm.SetBudget(cfg)

	got, _ := bm.GetBudget("daily")
	if got.WarningPct != 80 {
		t.Errorf("expected default warning 80, got %f", got.WarningPct)
	}
	if got.CriticalPct != 100 {
		t.Errorf("expected default critical 100, got %f", got.CriticalPct)
	}
}

func TestBudgetManagerRemove(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	bm.SetBudget(BudgetConfig{Period: "daily", LimitUSD: 10.0})
	bm.RemoveBudget("daily")

	_, ok := bm.GetBudget("daily")
	if ok {
		t.Error("expected budget to be removed")
	}
}

func TestBudgetManagerListBudgets(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	bm.SetBudget(BudgetConfig{Period: "daily", LimitUSD: 10.0})
	bm.SetBudget(BudgetConfig{Period: "monthly", LimitUSD: 100.0})

	list := bm.ListBudgets()
	if len(list) != 2 {
		t.Errorf("expected 2 budgets, got %d", len(list))
	}
}

func TestBudgetManagerAlertWarning(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	bm.SetBudget(BudgetConfig{Period: "daily", LimitUSD: 1.0, WarningPct: 50})

	var receivedAlert BudgetAlert
	bm.OnAlert(func(alert BudgetAlert) {
		receivedAlert = alert
	})

	// Record enough to trigger warning (>50% of $1)
	// gpt-5.4-mini: 1000 prompt + 500 completion ≈ $0.0075
	// Need ~67 calls to hit 50%, or use a more expensive model
	// Let's use gpt-4-turbo: prompt $0.01/1K, completion $0.03/1K
	// 1000 prompt + 1000 completion = $0.01 + $0.03 = $0.04
	// 13 calls = $0.52 > 50% of $1
	for i := 0; i < 13; i++ {
		store.RecordCall(fmt.Sprintf("c%d", i), "openai", "gpt-4-turbo", "s1", 1000, 1000)
	}

	alerts := bm.Check()
	if len(alerts) == 0 {
		t.Error("expected warning alert")
	}
	if receivedAlert.Level != BudgetLevelWarning {
		t.Errorf("expected warning level, got %s", receivedAlert.Level)
	}
}

func TestBudgetManagerAlertCritical(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	bm.SetBudget(BudgetConfig{Period: "daily", LimitUSD: 0.5})

	var receivedAlert BudgetAlert
	bm.OnAlert(func(alert BudgetAlert) {
		receivedAlert = alert
	})

	// gpt-4-turbo: 1000 prompt + 1000 completion = $0.04
	// 13 calls = $0.52 > $0.5
	for i := 0; i < 13; i++ {
		store.RecordCall(fmt.Sprintf("c%d", i), "openai", "gpt-4-turbo", "s1", 1000, 1000)
	}

	alerts := bm.Check()
	if len(alerts) == 0 {
		t.Error("expected critical alert")
	}
	if receivedAlert.Level != BudgetLevelCritical {
		t.Errorf("expected critical level, got %s", receivedAlert.Level)
	}
}

func TestBudgetManagerNoDuplicateAlerts(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	bm.SetBudget(BudgetConfig{Period: "daily", LimitUSD: 0.01})

	alertCount := 0
	bm.OnAlert(func(alert BudgetAlert) {
		alertCount++
	})

	// Trigger critical
	store.RecordCall("c1", "openai", "gpt-4-turbo", "s1", 1000, 1000)
	bm.Check()
	bm.Check() // Second check should not fire again

	if alertCount != 1 {
		t.Errorf("expected 1 alert, got %d", alertCount)
	}
}

func TestBudgetManagerResetAlerts(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	bm.SetBudget(BudgetConfig{Period: "daily", LimitUSD: 0.01})

	alertCount := 0
	bm.OnAlert(func(alert BudgetAlert) {
		alertCount++
	})

	store.RecordCall("c1", "openai", "gpt-4-turbo", "s1", 1000, 1000)
	bm.Check()
	bm.ResetAlerts()
	bm.Check() // Should fire again after reset

	if alertCount != 2 {
		t.Errorf("expected 2 alerts after reset, got %d", alertCount)
	}
}

func TestBudgetManagerStatus(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	bm.SetBudget(BudgetConfig{Period: "daily", LimitUSD: 1.0})

	store.RecordCall("c1", "openai", "gpt-5.4-mini", "s1", 1000, 500)

	statuses := bm.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].SpentUSD <= 0 {
		t.Error("expected non-zero spent")
	}
	if statuses[0].Remaining <= 0 {
		t.Error("expected positive remaining")
	}
}

func TestBudgetManagerProviderFilter(t *testing.T) {
	pt := NewPriceTable()
	store := NewCostStore(pt)
	bm := NewBudgetManager(store)

	bm.SetBudget(BudgetConfig{Period: "daily", LimitUSD: 0.01, Provider: "openai"})

	alertCount := 0
	bm.OnAlert(func(alert BudgetAlert) {
		alertCount++
	})

	// Anthropic spending should not affect openai budget
	store.RecordCall("c1", "anthropic", "claude-3.5-sonnet", "s1", 10000, 5000)
	bm.Check()
	if alertCount != 0 {
		t.Error("anthropic spending should not trigger openai budget")
	}

	// OpenAI spending should trigger
	store.RecordCall("c2", "openai", "gpt-4-turbo", "s2", 1000, 1000)
	bm.Check()
	if alertCount == 0 {
		t.Error("expected alert for openai spending")
	}
}

func TestMatchPeriod(t *testing.T) {
	now := time.Now()

	if !matchPeriod(now, "today") {
		t.Error("now should match today")
	}
	if !matchPeriod(now, "week") {
		t.Error("now should match week")
	}
	if !matchPeriod(now, "month") {
		t.Error("now should match month")
	}
	if !matchPeriod(now, "all") {
		t.Error("now should match all")
	}
	if !matchPeriod(now, "") {
		t.Error("now should match empty period")
	}

	// Old timestamp
	old := now.AddDate(0, 0, -8) // 8 days ago
	if matchPeriod(old, "today") {
		t.Error("8 days ago should not match today")
	}
	if matchPeriod(old, "week") {
		t.Error("8 days ago should not match week")
	}
}
