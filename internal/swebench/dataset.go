package swebench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Instance is the subset of the SWE-bench record schema LuckyAgent needs to
// generate a patch. Gold fields are kept for reporting only and must not be
// included in prompts.
type Instance struct {
	InstanceID       string   `json:"instance_id"`
	Repo             string   `json:"repo"`
	BaseCommit       string   `json:"base_commit"`
	ProblemStatement string   `json:"problem_statement"`
	HintsText        string   `json:"hints_text,omitempty"`
	CreatedAt        string   `json:"created_at,omitempty"`
	Version          string   `json:"version,omitempty"`
	Patch            string   `json:"patch,omitempty"`
	TestPatch        string   `json:"test_patch,omitempty"`
	FailToPass       TestList `json:"FAIL_TO_PASS,omitempty"`
	PassToPass       TestList `json:"PASS_TO_PASS,omitempty"`
}

// TestList accepts the array form used by Hugging Face datasets and the JSON
// string form sometimes emitted by export scripts.
type TestList []string

func (l *TestList) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*l = nil
		return nil
	}
	var items []string
	if err := json.Unmarshal(data, &items); err == nil {
		*l = items
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		*l = nil
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &items); err == nil {
			*l = items
			return nil
		}
	}
	*l = splitTestListString(raw)
	return nil
}

func splitTestListString(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ','
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Validate checks the minimum fields required to run one benchmark instance.
func (i Instance) Validate() error {
	if strings.TrimSpace(i.InstanceID) == "" {
		return fmt.Errorf("missing instance_id")
	}
	if strings.TrimSpace(i.Repo) == "" {
		return fmt.Errorf("instance %s: missing repo", i.InstanceID)
	}
	if strings.TrimSpace(i.BaseCommit) == "" {
		return fmt.Errorf("instance %s: missing base_commit", i.InstanceID)
	}
	if strings.TrimSpace(i.ProblemStatement) == "" {
		return fmt.Errorf("instance %s: missing problem_statement", i.InstanceID)
	}
	return nil
}

// BuildPrompt returns the exact task prompt shown to the agent. It deliberately
// omits Patch, TestPatch, FAIL_TO_PASS, and PASS_TO_PASS.
func (i Instance) BuildPrompt() string {
	var b strings.Builder
	b.WriteString("You are running one SWE-bench software-engineering repair task.\n")
	b.WriteString("Work only inside the current repository checkout. Inspect the code, reproduce or reason about the issue, edit the necessary files, and run focused tests when practical.\n")
	b.WriteString("Do not use any gold patch, hidden test patch, or evaluation oracle. The benchmark runner will collect git diff after you finish.\n\n")
	b.WriteString("Instance ID: ")
	b.WriteString(strings.TrimSpace(i.InstanceID))
	b.WriteString("\nRepository: ")
	b.WriteString(strings.TrimSpace(i.Repo))
	b.WriteString("\nBase commit: ")
	b.WriteString(strings.TrimSpace(i.BaseCommit))
	b.WriteString("\n\nIssue:\n")
	b.WriteString(strings.TrimSpace(i.ProblemStatement))
	if hints := strings.TrimSpace(i.HintsText); hints != "" {
		b.WriteString("\n\nHints:\n")
		b.WriteString(hints)
	}
	b.WriteString("\n\nWhen you are done, give a short summary of changed files and tests run. The actual answer is the repository patch, not prose.")
	return b.String()
}

// LoadInstances reads SWE-bench JSONL or JSON-array files.
func LoadInstances(path string, limit int) ([]Instance, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("dataset path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	if limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("dataset is empty: %s", path)
	}

	var instances []Instance
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" && bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		if err := json.Unmarshal(data, &instances); err != nil {
			return nil, fmt.Errorf("decode json dataset: %w", err)
		}
	} else {
		decoded, err := decodeJSONL(data)
		if err != nil {
			return nil, err
		}
		instances = decoded
	}

	if limit > 0 && len(instances) > limit {
		instances = instances[:limit]
	}
	for idx, inst := range instances {
		if err := inst.Validate(); err != nil {
			return nil, fmt.Errorf("dataset record %d: %w", idx+1, err)
		}
	}
	return instances, nil
}

func decodeJSONL(data []byte) ([]Instance, error) {
	var instances []Instance
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var inst Instance
		if err := json.Unmarshal(line, &inst); err != nil {
			return nil, fmt.Errorf("decode jsonl line %d: %w", lineNo, err)
		}
		instances = append(instances, inst)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan jsonl dataset: %w", err)
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("dataset has no records")
	}
	return instances, nil
}

// SafeID returns a filesystem-safe identifier for instance and repo names.
func SafeID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}
