package tool

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestHTTPRequestRejectsMutationBodyAndUnsafeHeaders(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	if _, err := r.Call("http_request", map[string]any{
		"url":    "https://93.184.216.34/api",
		"method": "POST",
	}); err == nil || !strings.Contains(err.Error(), "allow_mutation") {
		t.Fatalf("expected mutation gate error, got %v", err)
	}

	if _, err := r.Call("http_request", map[string]any{
		"url":            "https://93.184.216.34/api",
		"method":         "POST",
		"allow_mutation": true,
		"body":           strings.Repeat("x", maxHTTPRequestBodyBytes+1),
	}); err == nil || !strings.Contains(err.Error(), "request body") {
		t.Fatalf("expected body size error, got %v", err)
	}

	if _, err := r.Call("http_request", map[string]any{
		"url":          "https://93.184.216.34/api",
		"headers_json": `{"Host":"evil.example"}`,
	}); err == nil || !strings.Contains(err.Error(), "cannot be overridden") {
		t.Fatalf("expected unsafe header error, got %v", err)
	}
}

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

func TestSQLQueryReadOnlyMetadataAndBlob(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	dbPath := seedQuerySQLite(t)

	if _, err := r.Call("sql_query", map[string]any{"path": dbPath, "query": "SELECT name FROM users; DELETE FROM users"}); err == nil {
		t.Fatalf("expected multi-statement query to be rejected")
	}
	if _, err := r.Call("sql_query", map[string]any{"path": dbPath, "query": "PRAGMA query_only=OFF"}); err == nil {
		t.Fatalf("expected write-like pragma to be rejected")
	}

	result, err := r.Call("sql_query", map[string]any{
		"path":         dbPath,
		"query":        "SELECT name, avatar FROM users ORDER BY id",
		"limit":        1,
		"include_meta": true,
	})
	if err != nil {
		t.Fatalf("sql_query metadata: %v", err)
	}
	for _, want := range []string{`"columns"`, `"returned_rows": 1`, `"truncated": true`, `"type": "blob"`, `"base64"`} {
		if !strings.Contains(result, want) {
			t.Fatalf("sql_query output missing %s: %q", want, result)
		}
	}
}

func TestDBSchemaIncludesIndexesForeignKeysAndDefaults(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	dbPath := seedQuerySQLite(t)
	result, err := r.Call("db_schema", map[string]any{
		"path":        dbPath,
		"table":       "users",
		"include":     "columns,indexes,foreign_keys,triggers",
		"include_sql": true,
	})
	if err != nil {
		t.Fatalf("db_schema: %v", err)
	}
	for _, want := range []string{
		`"indexes"`,
		`"foreign_keys"`,
		`"default": null`,
		`"has_default": false`,
		`"name": "idx_users_name"`,
		`"table": "orgs"`,
		`"triggers"`,
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("db_schema output missing %s: %q", want, result)
		}
	}
}

func seedQuerySQLite(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sample.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE orgs (id INTEGER PRIMARY KEY, name TEXT);
CREATE TABLE users (
	id INTEGER PRIMARY KEY,
	org_id INTEGER REFERENCES orgs(id) ON DELETE CASCADE,
	name TEXT,
	role TEXT DEFAULT 'user',
	avatar BLOB
);
CREATE INDEX idx_users_name ON users(name);
CREATE TRIGGER users_ai AFTER INSERT ON users BEGIN UPDATE users SET role = role WHERE id = NEW.id; END;
INSERT INTO orgs(name) VALUES ('core');
INSERT INTO users(org_id, name, avatar) VALUES (1, 'Ada', x'fffefd'), (1, 'Bob', x'00ff');
`)
	if err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}
	return dbPath
}
