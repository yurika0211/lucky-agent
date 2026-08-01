package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yurika0211/luckyagent/internal/embedder"
	"github.com/yurika0211/luckyagent/internal/rag"
)

func TestRAGToolServiceIndexDryRunPlanSkipsSensitiveFiles(t *testing.T) {
	mgr := rag.NewRAGManager(embedder.NewMockEmbedder(8), rag.DefaultRAGConfig())
	svc := NewRAGToolService(mgr)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("# Doc\n\nalpha"), 0o600); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("API_KEY=secret"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	out, err := svc.HandleIndex(map[string]any{
		"path":    dir,
		"dry_run": true,
		"format":  "json",
	})
	if err != nil {
		t.Fatalf("HandleIndex dry_run: %v", err)
	}
	var plan struct {
		DryRun     bool `json:"dry_run"`
		TotalFiles int  `json:"total_files"`
		Skipped    []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("unmarshal plan: %v\n%s", err, out)
	}
	if !plan.DryRun || plan.TotalFiles != 1 || len(plan.Skipped) == 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if stats := mgr.Stats(); stats.DocumentCount != 0 {
		t.Fatalf("dry_run should not index documents, got %+v", stats)
	}
}

func TestRAGToolServiceIndexJSONResult(t *testing.T) {
	mgr := rag.NewRAGManager(embedder.NewMockEmbedder(8), rag.DefaultRAGConfig())
	svc := NewRAGToolService(mgr)
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Doc\n\nalpha beta"), 0o600); err != nil {
		t.Fatalf("write doc: %v", err)
	}

	out, err := svc.HandleIndex(map[string]any{
		"path":   path,
		"format": "json",
	})
	if err != nil {
		t.Fatalf("HandleIndex: %v", err)
	}
	var payload struct {
		Indexed   bool `json:"indexed"`
		Documents int  `json:"documents"`
		Chunks    int  `json:"chunks"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal result: %v\n%s", err, out)
	}
	if !payload.Indexed || payload.Documents != 1 || payload.Chunks == 0 {
		t.Fatalf("unexpected index result: %+v", payload)
	}
}

func TestRAGToolServiceIndexRejectsBounds(t *testing.T) {
	mgr := rag.NewRAGManager(embedder.NewMockEmbedder(8), rag.DefaultRAGConfig())
	svc := NewRAGToolService(mgr)

	if _, err := svc.HandleIndex(map[string]any{
		"path":      t.TempDir(),
		"max_files": defaultRAGIndexMaxFiles + 1,
	}); err == nil {
		t.Fatal("expected max_files bound error")
	}
}
