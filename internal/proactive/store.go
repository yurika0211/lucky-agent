package proactive

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

// Store persists sampled signals, state estimates, and dry-run gate decisions.
type Store struct {
	db *sql.DB
}

// OpenStore opens or creates a proactive SQLite database.
func OpenStore(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("proactive store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create proactive store dir: %w", err)
	}
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open proactive sqlite: %w", err)
	}
	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) init() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS proactive_signals (
			id TEXT PRIMARY KEY,
			channel TEXT NOT NULL,
			value REAL NOT NULL,
			label TEXT NOT NULL,
			metadata TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS proactive_state_estimates (
			id TEXT PRIMARY KEY,
			predicted_state TEXT NOT NULL,
			confidence REAL NOT NULL,
			noise_variance REAL NOT NULL,
			horizon_seconds INTEGER NOT NULL,
			reasons TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS proactive_dry_run_actions (
			id TEXT PRIMARY KEY,
			state_id TEXT NOT NULL,
			action TEXT NOT NULL,
			confidence REAL NOT NULL,
			allowed INTEGER NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("init proactive store: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) RecordSignals(signals []Signal) error {
	if s == nil || s.db == nil || len(signals) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin proactive signal insert: %w", err)
	}
	defer tx.Rollback()
	for _, signal := range signals {
		if signal.ID == "" {
			return fmt.Errorf("signal id is required")
		}
		metadata, err := json.Marshal(signal.Metadata)
		if err != nil {
			return fmt.Errorf("marshal signal metadata: %w", err)
		}
		_, err = tx.Exec(
			`INSERT OR REPLACE INTO proactive_signals(id, channel, value, label, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			signal.ID, signal.Channel, signal.Value, signal.Label, string(metadata), formatTime(signal.CreatedAt),
		)
		if err != nil {
			return fmt.Errorf("insert proactive signal: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit proactive signals: %w", err)
	}
	return nil
}

func (s *Store) RecordEstimate(estimate StateEstimate) error {
	if s == nil || s.db == nil {
		return nil
	}
	if estimate.ID == "" {
		return fmt.Errorf("estimate id is required")
	}
	reasons, err := json.Marshal(estimate.Reasons)
	if err != nil {
		return fmt.Errorf("marshal estimate reasons: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO proactive_state_estimates(id, predicted_state, confidence, noise_variance, horizon_seconds, reasons, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		estimate.ID,
		estimate.PredictedState,
		estimate.Confidence,
		estimate.NoiseVariance,
		int(estimate.Horizon.Seconds()),
		string(reasons),
		formatTime(estimate.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert proactive estimate: %w", err)
	}
	return nil
}

func (s *Store) RecordActions(actions []DryRunAction) error {
	if s == nil || s.db == nil || len(actions) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin proactive action insert: %w", err)
	}
	defer tx.Rollback()
	for _, action := range actions {
		if action.ID == "" {
			return fmt.Errorf("action id is required")
		}
		allowed := 0
		if action.Allowed {
			allowed = 1
		}
		_, err := tx.Exec(
			`INSERT OR REPLACE INTO proactive_dry_run_actions(id, state_id, action, confidence, allowed, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			action.ID, action.StateID, action.Action, action.Confidence, allowed, action.Reason, formatTime(action.CreatedAt),
		)
		if err != nil {
			return fmt.Errorf("insert proactive action: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit proactive actions: %w", err)
	}
	return nil
}

func (s *Store) Stats() (Stats, error) {
	if s == nil || s.db == nil {
		return Stats{}, nil
	}
	var stats Stats
	counts := []struct {
		table string
		dest  *int
	}{
		{"proactive_signals", &stats.Signals},
		{"proactive_state_estimates", &stats.Estimates},
		{"proactive_dry_run_actions", &stats.Actions},
	}
	for _, item := range counts {
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + item.table).Scan(item.dest); err != nil {
			return Stats{}, fmt.Errorf("count %s: %w", item.table, err)
		}
	}
	return stats, nil
}

func (s *Store) LatestEstimate() (StateEstimate, bool, error) {
	if s == nil || s.db == nil {
		return StateEstimate{}, false, nil
	}
	var estimate StateEstimate
	var horizonSeconds int
	var reasonsJSON string
	var createdAt string
	err := s.db.QueryRow(
		`SELECT id, predicted_state, confidence, noise_variance, horizon_seconds, reasons, created_at
		 FROM proactive_state_estimates ORDER BY created_at DESC LIMIT 1`,
	).Scan(&estimate.ID, &estimate.PredictedState, &estimate.Confidence, &estimate.NoiseVariance, &horizonSeconds, &reasonsJSON, &createdAt)
	if err == sql.ErrNoRows {
		return StateEstimate{}, false, nil
	}
	if err != nil {
		return StateEstimate{}, false, fmt.Errorf("load latest proactive estimate: %w", err)
	}
	if err := json.Unmarshal([]byte(reasonsJSON), &estimate.Reasons); err != nil {
		return StateEstimate{}, false, fmt.Errorf("decode proactive estimate reasons: %w", err)
	}
	estimate.Horizon = time.Duration(horizonSeconds) * time.Second
	estimate.CreatedAt = parseTime(createdAt)
	return estimate, true, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}
