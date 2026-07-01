package swebench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Prediction is compatible with the SWE-bench evaluator prediction JSONL.
type Prediction struct {
	InstanceID      string `json:"instance_id"`
	ModelNameOrPath string `json:"model_name_or_path"`
	ModelPatch      string `json:"model_patch"`
}

// Record is LuckyAgent's per-instance benchmark trace.
type Record struct {
	Type          string            `json:"type"`
	Variant       string            `json:"variant"`
	InstanceID    string            `json:"instance_id"`
	Repo          string            `json:"repo"`
	BaseCommit    string            `json:"base_commit"`
	DryRun        bool              `json:"dry_run"`
	Worktree      string            `json:"worktree,omitempty"`
	SourcePath    string            `json:"source_path,omitempty"`
	Iterations    int               `json:"iterations,omitempty"`
	TokensUsed    int               `json:"tokens_used,omitempty"`
	ToolCalls     []ToolCallSummary `json:"tool_calls,omitempty"`
	PatchBytes    int               `json:"patch_bytes"`
	PatchEmpty    bool              `json:"patch_empty"`
	AgentResponse string            `json:"agent_response,omitempty"`
	StartedAt     string            `json:"started_at"`
	DurationMS    float64           `json:"duration_ms"`
	Error         string            `json:"error,omitempty"`
	Extra         map[string]any    `json:"extra,omitempty"`
}

// Summary is written at the end of a run result JSONL.
type Summary struct {
	Type          string  `json:"type"`
	Variant       string  `json:"variant"`
	Records       int     `json:"records"`
	Errors        int     `json:"errors"`
	EmptyPatches  int     `json:"empty_patches"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	Clean         bool    `json:"clean"`
}

// ToolCallSummary captures enough tool trace detail for benchmark analysis
// without embedding full tool outputs in the report.
type ToolCallSummary struct {
	Name       string  `json:"name"`
	Arguments  string  `json:"arguments,omitempty"`
	ResultSize int     `json:"result_size"`
	DurationMS float64 `json:"duration_ms"`
}

// WritePredictions writes evaluator-compatible JSONL.
func WritePredictions(path string, predictions []Prediction) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("prediction path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create prediction dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open predictions: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, pred := range predictions {
		if strings.TrimSpace(pred.InstanceID) == "" {
			return fmt.Errorf("prediction is missing instance_id")
		}
		if strings.TrimSpace(pred.ModelNameOrPath) == "" {
			return fmt.Errorf("prediction %s is missing model_name_or_path", pred.InstanceID)
		}
		if err := enc.Encode(pred); err != nil {
			return fmt.Errorf("write prediction %s: %w", pred.InstanceID, err)
		}
	}
	return nil
}

// WriteRecords writes LuckyAgent run records plus a summary JSONL record.
func WriteRecords(path string, records []Record, summary Summary) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("record path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create record dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open records: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, record := range records {
		if err := enc.Encode(record); err != nil {
			return fmt.Errorf("write record %s: %w", record.InstanceID, err)
		}
	}
	if err := enc.Encode(summary); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

// SummarizeRecords aggregates per-instance records.
func SummarizeRecords(variant string, records []Record) Summary {
	var errors, empty int
	var totalDuration float64
	for _, record := range records {
		if record.Error != "" {
			errors++
		}
		if record.PatchEmpty {
			empty++
		}
		totalDuration += record.DurationMS
	}
	avg := 0.0
	if len(records) > 0 {
		avg = totalDuration / float64(len(records))
	}
	return Summary{
		Type:          "summary",
		Variant:       variant,
		Records:       len(records),
		Errors:        errors,
		EmptyPatches:  empty,
		AvgDurationMS: avg,
		Clean:         errors == 0,
	}
}
