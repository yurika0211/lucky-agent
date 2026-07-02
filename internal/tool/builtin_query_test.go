package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCSVQueryStreamingProjectionFiltersAndMeta(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "users.csv")
	content := strings.Join([]string{
		"name,role,email,score",
		"Ada,admin,ada@example.com,98",
		"Bob,user,bob@example.net,75",
		"Chen,admin,chen@example.com,88",
		"Drew,user,,64",
		"",
	}, "\n")
	if err := os.WriteFile(csvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	result, err := r.Call("csv_query", map[string]any{
		"path":    csvPath,
		"columns": []any{"name", "email"},
		"filters": []any{
			map[string]any{"column": "role", "op": "eq", "value": "admin"},
			map[string]any{"column": "email", "op": "suffix", "value": "@example.com"},
			map[string]any{"column": "score", "op": "gte", "value": "90"},
		},
		"include_meta": true,
	})
	if err != nil {
		t.Fatalf("csv_query filters: %v", err)
	}
	if !strings.Contains(result, `"name": "Ada"`) || strings.Contains(result, `"name": "Chen"`) {
		t.Fatalf("unexpected filtered result: %q", result)
	}
	if strings.Contains(result, `"role"`) || !strings.Contains(result, `"matched_rows": 1`) {
		t.Fatalf("unexpected projection/meta result: %q", result)
	}
}

func TestCSVQueryDelimiterAndLegacyContains(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	tsvPath := filepath.Join(tmpDir, "users.tsv")
	content := "name\trole\temail\nAda\tadmin\tada@example.com\nBob\tuser\tbob@example.net\n"
	if err := os.WriteFile(tsvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write tsv: %v", err)
	}

	result, err := r.Call("csv_query", map[string]any{
		"path":      tsvPath,
		"delimiter": `\t`,
		"column":    "email",
		"contains":  "example.net",
	})
	if err != nil {
		t.Fatalf("csv_query tsv: %v", err)
	}
	if !strings.Contains(result, `"name": "Bob"`) || strings.Contains(result, `"name": "Ada"`) {
		t.Fatalf("unexpected tsv result: %q", result)
	}
}
