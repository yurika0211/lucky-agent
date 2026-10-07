package agent

import (
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
)

func TestDefaultContextBudgetMatchesHistoricalSplit(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.MaxTokens != 4096 {
		t.Fatalf("expected max_tokens 4096, got %d", cfg.MaxTokens)
	}
	reserved := reservedTokensFor(cfg)
	if reserved != 1024 {
		t.Fatalf("expected reserved 1024, got %d", reserved)
	}
	budget := computeContextBudget(cfg.MaxTokens, reserved, contextBudgetShareFor(nil))
	if budget.System != 460 || budget.Memory != 307 || budget.RAG != 614 || budget.History != 768 || budget.ToolResult != 921 {
		t.Fatalf("default split changed: %+v", budget)
	}
}

func TestCustomContextBudgetRatioChangesSplit(t *testing.T) {
	ratios := contextBudgetShareFor(nil)
	ratios.System = 0.50
	ratios.Memory = 0.10
	ratios.RAG = 0.10
	ratios.History = 0.20
	ratios.ToolResult = 0.10
	budget := computeContextBudget(4096, 1024, ratios)
	if budget.System != 1536 {
		t.Fatalf("expected system 1536, got %d", budget.System)
	}
	if budget.ToolResult != 307 {
		t.Fatalf("expected tool result 307, got %d", budget.ToolResult)
	}
}

func TestContextBudgetRatiosFollowConfig(t *testing.T) {
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	base := mgr.Get()
	custom := *base
	custom.ContextBudget = config.ContextBudgetConfig{
		SystemRatio: 0.40, MemoryRatio: 0.10, RAGRatio: 0.10,
		HistoryRatio: 0.20, ToolResultRatio: 0.20, ReservedRatio: 0.25,
		SystemFloor: 256, MemoryFloor: 128, RAGFloor: 256, HistoryFloor: 256, ToolResultFloor: 256,
	}
	mgr.SetConfig(&custom)
	got := contextBudgetShareFor(&Agent{cfg: mgr})
	if got.System < 0.39 || got.System > 0.41 {
		t.Fatalf("expected system ratio near 0.40, got %v", got.System)
	}
}
