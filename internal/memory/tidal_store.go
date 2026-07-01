package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TidalStore persists tidal memory telemetry and learned response kernels.
type TidalStore struct {
	db *sql.DB
}

// TidalStoreStats summarizes persisted tidal memory data.
type TidalStoreStats struct {
	QueryEvents    int `json:"query_events"`
	RecallEvents   int `json:"recall_events"`
	FeedbackEvents int `json:"feedback_events"`
	Kernels        int `json:"kernels"`
}

// OpenTidalStore opens or creates a tidal memory SQLite database.
func OpenTidalStore(path string) (*TidalStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("tidal store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create tidal store dir: %w", err)
	}
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open tidal sqlite: %w", err)
	}
	store := &TidalStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *TidalStore) init() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS query_events (
			id TEXT PRIMARY KEY,
			session_id TEXT,
			query TEXT NOT NULL,
			query_terms TEXT,
			intent_tags TEXT,
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS recall_events (
			id TEXT PRIMARY KEY,
			query_id TEXT NOT NULL,
			memory_id TEXT NOT NULL,
			rank INTEGER NOT NULL,
			score REAL NOT NULL,
			source TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS feedback_events (
			id TEXT PRIMARY KEY,
			query_id TEXT NOT NULL,
			memory_id TEXT NOT NULL,
			signal TEXT NOT NULL,
			value REAL NOT NULL,
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS response_kernels (
			key TEXT PRIMARY KEY,
			feature TEXT NOT NULL,
			bins TEXT NOT NULL,
			weights TEXT NOT NULL,
			counts TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("init tidal store: %w", err)
		}
	}
	if err := s.ensureColumn("query_events", "session_id", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("response_kernels", "feature", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("response_kernels", "bins", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	return nil
}

func (s *TidalStore) ensureColumn(table, column, definition string) error {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return fmt.Errorf("inspect tidal store schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan tidal store schema: %w", err)
		}
		if strings.EqualFold(name, column) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition); err != nil {
		return fmt.Errorf("migrate tidal store schema: %w", err)
	}
	return nil
}

// Close closes the store.
func (s *TidalStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *TidalStore) RecordActivation(query string, scores []ActivationScore, now time.Time) {
	if s == nil || s.db == nil || len(scores) == 0 {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	queryID := fmt.Sprintf("q-%d", now.UnixNano())
	terms, _ := json.Marshal(extractQueryTerms(strings.ToLower(strings.TrimSpace(query))))
	intents, _ := json.Marshal(inferTidalIntentTags(query))
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO query_events(id, session_id, query, query_terms, intent_tags, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		queryID, "", query, string(terms), string(intents), now,
	); err != nil {
		return
	}
	for i, score := range scores {
		source := "direct"
		if score.Components.GraphBoost > 0 {
			source = "graph"
		}
		if score.Components.TidalBoost != 0 {
			source += "+tidal"
		}
		id := fmt.Sprintf("%s-r-%d", queryID, i+1)
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO recall_events(id, query_id, memory_id, rank, score, source, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, queryID, score.EntryID, i+1, score.Score, source, now,
		); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

func (s *TidalStore) RecordFeedback(feedback TidalFeedback) {
	if s == nil || s.db == nil {
		return
	}
	if feedback.At.IsZero() {
		feedback.At = time.Now()
	}
	signal := strings.TrimSpace(feedback.Signal)
	if signal == "" {
		signal = "feedback"
	}
	queryID := strings.TrimSpace(feedback.QueryID)
	if queryID == "" {
		var err error
		queryID, err = s.latestOrCreateQueryEvent(feedback.Query, feedback.At)
		if err != nil {
			return
		}
	}
	id := fmt.Sprintf("f-%d", feedback.At.UnixNano())
	_, _ = s.db.Exec(
		`INSERT OR REPLACE INTO feedback_events(id, query_id, memory_id, signal, value, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, queryID, feedback.Entry.ID, signal, feedback.Value, feedback.At,
	)
}

func (s *TidalStore) latestOrCreateQueryEvent(query string, at time.Time) (string, error) {
	query = strings.TrimSpace(query)
	if query != "" {
		var queryID string
		err := s.db.QueryRow(
			`SELECT id FROM query_events WHERE query = ? ORDER BY created_at DESC LIMIT 1`,
			query,
		).Scan(&queryID)
		if err == nil && queryID != "" {
			return queryID, nil
		}
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	}
	if at.IsZero() {
		at = time.Now()
	}
	queryID := fmt.Sprintf("q-feedback-%d", at.UnixNano())
	terms, _ := json.Marshal(extractQueryTerms(strings.ToLower(query)))
	intents, _ := json.Marshal(inferTidalIntentTags(query))
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO query_events(id, session_id, query, query_terms, intent_tags, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		queryID, "", query, string(terms), string(intents), at,
	)
	return queryID, err
}

func (s *TidalStore) SaveKernels(snapshots []TidalKernelSnapshot) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	for _, snapshot := range snapshots {
		bins, err := json.Marshal(snapshot.BinEdges)
		if err != nil {
			return err
		}
		weights, err := json.Marshal(snapshot.Weights)
		if err != nil {
			return err
		}
		counts, err := json.Marshal(snapshot.Counts)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO response_kernels(key, feature, bins, weights, counts, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			snapshot.Key, snapshot.Feature, string(bins), string(weights), string(counts), now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *TidalStore) LoadKernels() ([]TidalKernelSnapshot, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT key, feature, bins, weights, counts FROM response_kernels ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TidalKernelSnapshot
	for rows.Next() {
		var snapshot TidalKernelSnapshot
		var binsRaw, weightsRaw, countsRaw string
		if err := rows.Scan(&snapshot.Key, &snapshot.Feature, &binsRaw, &weightsRaw, &countsRaw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(binsRaw), &snapshot.BinEdges)
		if err := json.Unmarshal([]byte(weightsRaw), &snapshot.Weights); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(countsRaw), &snapshot.Counts); err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	return out, rows.Err()
}

func (s *TidalStore) Stats() (TidalStoreStats, error) {
	var stats TidalStoreStats
	if s == nil || s.db == nil {
		return stats, nil
	}
	count := func(table string) (int, error) {
		var n int
		err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
		return n, err
	}
	var err error
	if stats.QueryEvents, err = count("query_events"); err != nil {
		return stats, err
	}
	if stats.RecallEvents, err = count("recall_events"); err != nil {
		return stats, err
	}
	if stats.FeedbackEvents, err = count("feedback_events"); err != nil {
		return stats, err
	}
	if stats.Kernels, err = count("response_kernels"); err != nil {
		return stats, err
	}
	return stats, nil
}
