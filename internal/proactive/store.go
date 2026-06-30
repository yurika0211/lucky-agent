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
		`CREATE TABLE IF NOT EXISTS proactive_feedback_events (
			id TEXT PRIMARY KEY,
			state_id TEXT NOT NULL,
			predicted_state TEXT NOT NULL,
			actual_state TEXT NOT NULL,
			value REAL NOT NULL,
			source TEXT NOT NULL,
			note TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS proactive_runtime_events (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			session_id TEXT NOT NULL,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			value REAL NOT NULL,
			metadata TEXT NOT NULL,
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

func (s *Store) RecordFeedback(event FeedbackEvent) error {
	if s == nil || s.db == nil {
		return nil
	}
	event.ActualState = strings.TrimSpace(event.ActualState)
	event.PredictedState = strings.TrimSpace(event.PredictedState)
	if event.ActualState == "" {
		return fmt.Errorf("actual state is required")
	}
	if event.PredictedState == "" {
		return fmt.Errorf("predicted state is required")
	}
	if event.Value == 0 {
		if strings.EqualFold(event.ActualState, event.PredictedState) {
			event.Value = 1
		} else {
			event.Value = -1
		}
	}
	if strings.TrimSpace(event.ID) == "" {
		if event.CreatedAt.IsZero() {
			event.CreatedAt = time.Now()
		}
		event.ID = fmt.Sprintf("feedback-%d", event.CreatedAt.UnixNano())
	}
	if strings.TrimSpace(event.Source) == "" {
		event.Source = "cli"
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO proactive_feedback_events(id, state_id, predicted_state, actual_state, value, source, note, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.StateID,
		event.PredictedState,
		event.ActualState,
		event.Value,
		event.Source,
		event.Note,
		formatTime(event.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert proactive feedback: %w", err)
	}
	return nil
}

func (s *Store) RecordRuntimeEvent(event RuntimeEvent) error {
	if s == nil || s.db == nil {
		return nil
	}
	event.Type = strings.TrimSpace(event.Type)
	if event.Type == "" {
		return fmt.Errorf("runtime event type is required")
	}
	if strings.TrimSpace(event.ID) == "" {
		if event.CreatedAt.IsZero() {
			event.CreatedAt = time.Now()
		}
		event.ID = fmt.Sprintf("runtime-%d", event.CreatedAt.UnixNano())
	}
	if strings.TrimSpace(event.Source) == "" {
		event.Source = "runtime"
	}
	if event.Metadata == nil {
		event.Metadata = map[string]string{}
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("marshal runtime event metadata: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO proactive_runtime_events(id, source, session_id, type, name, value, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.Source,
		event.SessionID,
		event.Type,
		event.Name,
		event.Value,
		string(metadata),
		formatTime(event.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert proactive runtime event: %w", err)
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
		{"proactive_feedback_events", &stats.FeedbackEvents},
		{"proactive_runtime_events", &stats.RuntimeEvents},
	}
	for _, item := range counts {
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + item.table).Scan(item.dest); err != nil {
			return Stats{}, fmt.Errorf("count %s: %w", item.table, err)
		}
	}
	return stats, nil
}

func (s *Store) RuntimeEventStats() (RuntimeEventStats, error) {
	if s == nil || s.db == nil {
		return RuntimeEventStats{ByType: map[string]int{}}, nil
	}
	stats := RuntimeEventStats{ByType: map[string]int{}}
	rows, err := s.db.Query(`SELECT type, COUNT(*) FROM proactive_runtime_events GROUP BY type ORDER BY type`)
	if err != nil {
		return RuntimeEventStats{}, fmt.Errorf("query proactive runtime event stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var count int
		if err := rows.Scan(&typ, &count); err != nil {
			return RuntimeEventStats{}, fmt.Errorf("scan proactive runtime event stats: %w", err)
		}
		stats.ByType[typ] = count
		stats.Events += count
	}
	if err := rows.Err(); err != nil {
		return RuntimeEventStats{}, err
	}
	return stats, nil
}

func (s *Store) RuntimeEventCountsSince(since time.Time) (map[string]int, error) {
	counts := map[string]int{}
	if s == nil || s.db == nil {
		return counts, nil
	}
	rows, err := s.db.Query(
		`SELECT type, COUNT(*)
		 FROM proactive_runtime_events
		 WHERE created_at >= ?
		 GROUP BY type`,
		formatTime(since),
	)
	if err != nil {
		return nil, fmt.Errorf("query proactive runtime event counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var count int
		if err := rows.Scan(&typ, &count); err != nil {
			return nil, fmt.Errorf("scan proactive runtime event counts: %w", err)
		}
		counts[typ] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

func (s *Store) RecentRuntimeEvents(limit int) ([]RuntimeEvent, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT id, source, session_id, type, name, value, metadata, created_at
		 FROM proactive_runtime_events
		 ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query recent proactive runtime events: %w", err)
	}
	defer rows.Close()
	var events []RuntimeEvent
	for rows.Next() {
		var event RuntimeEvent
		var metadataJSON string
		var createdAt string
		if err := rows.Scan(&event.ID, &event.Source, &event.SessionID, &event.Type, &event.Name, &event.Value, &metadataJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan recent proactive runtime event: %w", err)
		}
		if err := json.Unmarshal([]byte(metadataJSON), &event.Metadata); err != nil {
			return nil, fmt.Errorf("decode runtime event metadata: %w", err)
		}
		event.CreatedAt = parseTime(createdAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) FeedbackStats(limit int) (FeedbackStats, error) {
	return s.feedbackStats("", limit)
}

func (s *Store) FeedbackStatsForState(predictedState string, limit int) (FeedbackStats, error) {
	return s.feedbackStats(strings.TrimSpace(predictedState), limit)
}

func (s *Store) feedbackStats(predictedState string, limit int) (FeedbackStats, error) {
	if s == nil || s.db == nil {
		return FeedbackStats{}, nil
	}
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if predictedState == "" {
		rows, err = s.db.Query(
			`SELECT predicted_state, actual_state
		 FROM proactive_feedback_events
		 ORDER BY created_at DESC LIMIT ?`,
			limit,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT predicted_state, actual_state
		 FROM proactive_feedback_events
		 WHERE predicted_state = ?
		 ORDER BY created_at DESC LIMIT ?`,
			predictedState,
			limit,
		)
	}
	if err != nil {
		return FeedbackStats{}, fmt.Errorf("query proactive feedback stats: %w", err)
	}
	defer rows.Close()

	var stats FeedbackStats
	for rows.Next() {
		var predicted, actual string
		if err := rows.Scan(&predicted, &actual); err != nil {
			return FeedbackStats{}, fmt.Errorf("scan proactive feedback stats: %w", err)
		}
		stats.Events++
		if strings.EqualFold(strings.TrimSpace(predicted), strings.TrimSpace(actual)) {
			stats.Correct++
		}
	}
	if err := rows.Err(); err != nil {
		return FeedbackStats{}, err
	}
	if stats.Events > 0 {
		stats.Accuracy = float64(stats.Correct) / float64(stats.Events)
	}
	return stats, nil
}

func (s *Store) LatestEstimate() (StateEstimate, bool, error) {
	if s == nil || s.db == nil {
		return StateEstimate{}, false, nil
	}
	row := s.db.QueryRow(
		`SELECT id, predicted_state, confidence, noise_variance, horizon_seconds, reasons, created_at
		 FROM proactive_state_estimates ORDER BY created_at DESC LIMIT 1`,
	)
	estimate, err := scanEstimate(row)
	if err == sql.ErrNoRows {
		return StateEstimate{}, false, nil
	}
	if err != nil {
		return StateEstimate{}, false, fmt.Errorf("load latest proactive estimate: %w", err)
	}
	return estimate, true, nil
}

func (s *Store) EstimateByID(id string) (StateEstimate, bool, error) {
	if s == nil || s.db == nil {
		return StateEstimate{}, false, nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return StateEstimate{}, false, fmt.Errorf("estimate id is required")
	}
	row := s.db.QueryRow(
		`SELECT id, predicted_state, confidence, noise_variance, horizon_seconds, reasons, created_at
		 FROM proactive_state_estimates WHERE id = ? LIMIT 1`,
		id,
	)
	estimate, err := scanEstimate(row)
	if err == sql.ErrNoRows {
		return StateEstimate{}, false, nil
	}
	if err != nil {
		return StateEstimate{}, false, fmt.Errorf("load proactive estimate: %w", err)
	}
	return estimate, true, nil
}

type estimateScanner interface {
	Scan(dest ...any) error
}

func scanEstimate(scanner estimateScanner) (StateEstimate, error) {
	var estimate StateEstimate
	var horizonSeconds int
	var reasonsJSON string
	var createdAt string
	if err := scanner.Scan(&estimate.ID, &estimate.PredictedState, &estimate.Confidence, &estimate.NoiseVariance, &horizonSeconds, &reasonsJSON, &createdAt); err != nil {
		return StateEstimate{}, err
	}
	if err := json.Unmarshal([]byte(reasonsJSON), &estimate.Reasons); err != nil {
		return StateEstimate{}, fmt.Errorf("decode proactive estimate reasons: %w", err)
	}
	estimate.Horizon = time.Duration(horizonSeconds) * time.Second
	estimate.CreatedAt = parseTime(createdAt)
	return estimate, nil
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
