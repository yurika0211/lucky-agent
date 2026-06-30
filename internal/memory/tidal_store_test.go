package memory

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestTidalStoreMigratesLegacySchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tidal_memory.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	legacyStmts := []string{
		`CREATE TABLE query_events (
			id TEXT PRIMARY KEY,
			query TEXT NOT NULL,
			query_terms TEXT,
			intent_tags TEXT,
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE response_kernels (
			key TEXT PRIMARY KEY,
			weights TEXT NOT NULL,
			counts TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)`,
	}
	for _, stmt := range legacyStmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	store, err := OpenTidalStore(dbPath)
	if err != nil {
		t.Fatalf("OpenTidalStore() error = %v", err)
	}
	defer store.Close()

	for _, check := range []struct {
		table  string
		column string
	}{
		{"query_events", "session_id"},
		{"response_kernels", "feature"},
		{"response_kernels", "bins"},
	} {
		if !tidalTestColumnExists(t, store.db, check.table, check.column) {
			t.Fatalf("expected migrated column %s.%s", check.table, check.column)
		}
	}

	if err := store.SaveKernels([]TidalKernelSnapshot{{
		Key:      "tag:graph-rag",
		Feature:  "tag",
		BinEdges: DefaultTidalRerankerConfig().Bins,
		Weights:  []float64{1},
		Counts:   []int{1},
	}}); err != nil {
		t.Fatalf("SaveKernels() after migration error = %v", err)
	}
}

func tidalTestColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("schema rows: %v", err)
	}
	return false
}
