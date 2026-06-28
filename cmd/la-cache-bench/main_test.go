package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummarizeRecordsCleanliness(t *testing.T) {
	records := []benchRecord{
		{
			DurationMS:           100,
			ProviderCalls:        1,
			SystemPromptHash:     "abc",
			SystemPromptBytes:    120,
			SystemPromptTokens:   30,
			PromptTokens:         1000,
			CachedPromptTokens:   800,
			UncachedPromptTokens: 200,
			CompletionTokens:     20,
			TotalTokens:          1020,
		},
		{
			DurationMS:           200,
			ProviderCalls:        1,
			SystemPromptHash:     "abc",
			SystemPromptBytes:    120,
			SystemPromptTokens:   30,
			PromptTokens:         1000,
			CachedPromptTokens:   900,
			UncachedPromptTokens: 100,
			CompletionTokens:     20,
			TotalTokens:          1020,
		},
	}

	got := summarizeRecords("fixed", "same-session", records)
	if !got.Clean {
		t.Fatalf("expected clean summary: %#v", got)
	}
	if !got.SystemPromptStable || len(got.SystemPromptHashes) != 1 {
		t.Fatalf("unexpected prompt stability fields: %#v", got)
	}
	if got.CachedRatio != 0.85 {
		t.Fatalf("CachedRatio = %v", got.CachedRatio)
	}
}

func TestSummarizeRecordsDetectsDirtyRun(t *testing.T) {
	records := []benchRecord{
		{
			ProviderCalls:        2,
			CaptureErrors:        1,
			MissingUsageCalls:    1,
			ToolCalls:            1,
			ToolNames:            []string{"web_search"},
			SystemPromptHash:     "abc",
			PromptTokens:         100,
			CachedPromptTokens:   50,
			UncachedPromptTokens: 50,
			Error:                "timeout",
		},
		{
			ProviderCalls:        1,
			SystemPromptHash:     "def",
			PromptTokens:         100,
			CachedPromptTokens:   60,
			UncachedPromptTokens: 40,
		},
	}

	got := summarizeRecords("fixed", "same-session", records)
	if got.Clean {
		t.Fatalf("expected dirty summary: %#v", got)
	}
	if got.Errors != 1 || got.CaptureErrors != 1 || got.MissingUsageCalls != 1 || got.ToolCalls != 1 || got.ToolRounds != 1 {
		t.Fatalf("unexpected dirty counters: %#v", got)
	}
	if got.SystemPromptStable || len(got.SystemPromptHashes) != 2 {
		t.Fatalf("expected unstable prompt hashes: %#v", got)
	}
	if len(got.ToolNames) != 1 || got.ToolNames[0] != "web_search" {
		t.Fatalf("unexpected tool names: %#v", got.ToolNames)
	}
}

func TestNewBenchmarkConfigManagerCanUseIsolatedHome(t *testing.T) {
	mgr, cleanup, err := newBenchmarkConfigManager(benchConfig{IsolatedHome: true})
	if err != nil {
		t.Fatalf("newBenchmarkConfigManager: %v", err)
	}
	defer cleanup()

	home := mgr.HomeDir()
	if home == "" || !strings.Contains(filepath.Base(home), "lh-cache-bench-home-") {
		t.Fatalf("expected isolated benchmark home, got %q", home)
	}
	soulPath := filepath.Join(home, "memory", "prompts", "SOUL.md")
	if _, err := os.Stat(soulPath); err != nil {
		t.Fatalf("expected isolated SOUL.md: %v", err)
	}
	cfg := mgr.Get()
	if cfg.Provider == "" || cfg.Model == "" {
		t.Fatalf("expected copied/default provider config, got provider=%q model=%q", cfg.Provider, cfg.Model)
	}
	expectedSoulPath := filepath.Join(home, "memory", "prompts", "SOUL.md")
	if cfg.SoulPath != expectedSoulPath {
		t.Fatalf("expected isolated soul path %q, got %q", expectedSoulPath, cfg.SoulPath)
	}
	if _, err := os.Stat(filepath.Join(home, "cron_jobs.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no cron store in isolated home, stat err=%v", err)
	}
}
