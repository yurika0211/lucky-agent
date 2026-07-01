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
		`CREATE TABLE IF NOT EXISTS proactive_estimate_signals (
			estimate_id TEXT NOT NULL,
			signal_id TEXT NOT NULL,
			PRIMARY KEY (estimate_id, signal_id)
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
		`CREATE TABLE IF NOT EXISTS proactive_action_executions (
			id TEXT PRIMARY KEY,
			action_id TEXT NOT NULL,
			state_id TEXT NOT NULL,
			action TEXT NOT NULL,
			status TEXT NOT NULL,
			reason TEXT NOT NULL,
			metadata TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS proactive_signal_weights (
			predicted_state TEXT NOT NULL,
			channel TEXT NOT NULL,
			label TEXT NOT NULL,
			weight REAL NOT NULL,
			samples INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (predicted_state, channel, label)
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

func (s *Store) RecordEstimateSignals(estimateID string, signals []Signal) error {
	if s == nil || s.db == nil || len(signals) == 0 {
		return nil
	}
	estimateID = strings.TrimSpace(estimateID)
	if estimateID == "" {
		return fmt.Errorf("estimate id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin proactive estimate signal insert: %w", err)
	}
	defer tx.Rollback()
	for _, signal := range signals {
		if strings.TrimSpace(signal.ID) == "" {
			continue
		}
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO proactive_estimate_signals(estimate_id, signal_id) VALUES (?, ?)`,
			estimateID,
			signal.ID,
		); err != nil {
			return fmt.Errorf("insert proactive estimate signal: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit proactive estimate signals: %w", err)
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

type KernelLearningConfig struct {
	Enabled      bool
	LearningRate float64
}

func (s *Store) LearnFromFeedback(event FeedbackEvent, cfg KernelLearningConfig) (int, error) {
	if s == nil || s.db == nil || !cfg.Enabled {
		return 0, nil
	}
	if cfg.LearningRate <= 0 || cfg.LearningRate > 1 {
		cfg.LearningRate = 0.08
	}
	signals, err := s.SignalsForEstimate(event.StateID)
	if err != nil {
		return 0, err
	}
	if len(signals) == 0 {
		return 0, nil
	}
	predictedState := strings.TrimSpace(event.PredictedState)
	if predictedState == "" {
		predictedState = strings.TrimSpace(event.ActualState)
	}
	if predictedState == "" {
		return 0, nil
	}
	target := -1.0
	if event.Value > 0 || strings.EqualFold(strings.TrimSpace(event.PredictedState), strings.TrimSpace(event.ActualState)) {
		target = 1.0
	}
	now := event.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin proactive kernel update: %w", err)
	}
	defer tx.Rollback()
	updates := 0
	for _, signal := range signals {
		channel := strings.TrimSpace(signal.Channel)
		label := strings.TrimSpace(signal.Label)
		if channel == "" || label == "" {
			continue
		}
		var weight float64
		var samples int
		err := tx.QueryRow(
			`SELECT weight, samples FROM proactive_signal_weights
			 WHERE predicted_state = ? AND channel = ? AND label = ?`,
			predictedState,
			channel,
			label,
		).Scan(&weight, &samples)
		if err != nil && err != sql.ErrNoRows {
			return 0, fmt.Errorf("load proactive signal weight: %w", err)
		}
		if err == sql.ErrNoRows {
			weight = 0
			samples = 0
		}
		weight = clamp(weight+cfg.LearningRate*(target-weight), -1, 1)
		samples++
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO proactive_signal_weights(predicted_state, channel, label, weight, samples, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			predictedState,
			channel,
			label,
			weight,
			samples,
			formatTime(now),
		); err != nil {
			return 0, fmt.Errorf("upsert proactive signal weight: %w", err)
		}
		updates++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit proactive kernel update: %w", err)
	}
	return updates, nil
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

func (s *Store) RecordActionExecutions(executions []ActionExecution) error {
	if s == nil || s.db == nil || len(executions) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin proactive action execution insert: %w", err)
	}
	defer tx.Rollback()
	for _, execution := range executions {
		if strings.TrimSpace(execution.ID) == "" {
			return fmt.Errorf("action execution id is required")
		}
		if execution.Metadata == nil {
			execution.Metadata = map[string]string{}
		}
		metadata, err := json.Marshal(execution.Metadata)
		if err != nil {
			return fmt.Errorf("marshal action execution metadata: %w", err)
		}
		_, err = tx.Exec(
			`INSERT OR REPLACE INTO proactive_action_executions(id, action_id, state_id, action, status, reason, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			execution.ID,
			execution.ActionID,
			execution.StateID,
			execution.Action,
			execution.Status,
			execution.Reason,
			string(metadata),
			formatTime(execution.CreatedAt),
		)
		if err != nil {
			return fmt.Errorf("insert proactive action execution: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit proactive action executions: %w", err)
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
		{"proactive_action_executions", &stats.Executions},
	}
	for _, item := range counts {
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + item.table).Scan(item.dest); err != nil {
			return Stats{}, fmt.Errorf("count %s: %w", item.table, err)
		}
	}
	return stats, nil
}

func (s *Store) RecentActionExecutions(limit int) ([]ActionExecution, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT id, action_id, state_id, action, status, reason, metadata, created_at
		 FROM proactive_action_executions
		 ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query recent proactive action executions: %w", err)
	}
	defer rows.Close()
	var executions []ActionExecution
	for rows.Next() {
		execution, err := scanActionExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("scan recent proactive action execution: %w", err)
		}
		executions = append(executions, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return executions, nil
}

func (s *Store) LatestSuccessfulActionExecution(action string) (ActionExecution, bool, error) {
	if s == nil || s.db == nil {
		return ActionExecution{}, false, nil
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return ActionExecution{}, false, fmt.Errorf("action is required")
	}
	row := s.db.QueryRow(
		`SELECT id, action_id, state_id, action, status, reason, metadata, created_at
		 FROM proactive_action_executions
		 WHERE action = ? AND status = ?
		 ORDER BY created_at DESC LIMIT 1`,
		action,
		ActionStatusSuccess,
	)
	execution, err := scanActionExecution(row)
	if err == sql.ErrNoRows {
		return ActionExecution{}, false, nil
	}
	if err != nil {
		return ActionExecution{}, false, fmt.Errorf("load latest proactive action execution: %w", err)
	}
	return execution, true, nil
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

func (s *Store) KernelStats() (KernelStats, error) {
	if s == nil || s.db == nil {
		return KernelStats{}, nil
	}
	var stats KernelStats
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(samples), 0) FROM proactive_signal_weights`).Scan(&stats.Weights, &stats.Samples); err != nil {
		return KernelStats{}, fmt.Errorf("query proactive kernel stats: %w", err)
	}
	return stats, nil
}

func (s *Store) SignalWeightsForState(predictedState string) (map[string]SignalWeight, error) {
	weights := map[string]SignalWeight{}
	if s == nil || s.db == nil {
		return weights, nil
	}
	predictedState = strings.TrimSpace(predictedState)
	if predictedState == "" {
		return weights, nil
	}
	rows, err := s.db.Query(
		`SELECT predicted_state, channel, label, weight, samples, updated_at
		 FROM proactive_signal_weights
		 WHERE predicted_state = ?`,
		predictedState,
	)
	if err != nil {
		return nil, fmt.Errorf("query proactive signal weights: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var weight SignalWeight
		var updatedAt string
		if err := rows.Scan(&weight.PredictedState, &weight.Channel, &weight.Label, &weight.Weight, &weight.Samples, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan proactive signal weight: %w", err)
		}
		weight.UpdatedAt = parseTime(updatedAt)
		weights[signalWeightKey(weight.Channel, weight.Label)] = weight
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return weights, nil
}

func (s *Store) RecentSignalWeights(limit int) ([]SignalWeight, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT predicted_state, channel, label, weight, samples, updated_at
		 FROM proactive_signal_weights
		 ORDER BY updated_at DESC, samples DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query recent proactive signal weights: %w", err)
	}
	defer rows.Close()
	var weights []SignalWeight
	for rows.Next() {
		var weight SignalWeight
		var updatedAt string
		if err := rows.Scan(&weight.PredictedState, &weight.Channel, &weight.Label, &weight.Weight, &weight.Samples, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan recent proactive signal weight: %w", err)
		}
		weight.UpdatedAt = parseTime(updatedAt)
		weights = append(weights, weight)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return weights, nil
}

func (s *Store) SignalsForEstimate(estimateID string) ([]Signal, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	estimateID = strings.TrimSpace(estimateID)
	if estimateID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT sig.id, sig.channel, sig.value, sig.label, sig.metadata, sig.created_at
		 FROM proactive_estimate_signals rel
		 JOIN proactive_signals sig ON sig.id = rel.signal_id
		 WHERE rel.estimate_id = ?
		 ORDER BY sig.created_at ASC, sig.id ASC`,
		estimateID,
	)
	if err != nil {
		return nil, fmt.Errorf("query proactive estimate signals: %w", err)
	}
	defer rows.Close()
	var signals []Signal
	for rows.Next() {
		signal, err := scanSignal(rows)
		if err != nil {
			return nil, fmt.Errorf("scan proactive estimate signal: %w", err)
		}
		signals = append(signals, signal)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return signals, nil
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

func scanActionExecution(scanner estimateScanner) (ActionExecution, error) {
	var execution ActionExecution
	var metadataJSON string
	var createdAt string
	if err := scanner.Scan(&execution.ID, &execution.ActionID, &execution.StateID, &execution.Action, &execution.Status, &execution.Reason, &metadataJSON, &createdAt); err != nil {
		return ActionExecution{}, err
	}
	if err := json.Unmarshal([]byte(metadataJSON), &execution.Metadata); err != nil {
		return ActionExecution{}, fmt.Errorf("decode action execution metadata: %w", err)
	}
	execution.CreatedAt = parseTime(createdAt)
	return execution, nil
}

func scanSignal(scanner estimateScanner) (Signal, error) {
	var signal Signal
	var metadataJSON string
	var createdAt string
	if err := scanner.Scan(&signal.ID, &signal.Channel, &signal.Value, &signal.Label, &metadataJSON, &createdAt); err != nil {
		return Signal{}, err
	}
	if err := json.Unmarshal([]byte(metadataJSON), &signal.Metadata); err != nil {
		return Signal{}, fmt.Errorf("decode proactive signal metadata: %w", err)
	}
	signal.CreatedAt = parseTime(createdAt)
	return signal, nil
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

func signalWeightKey(channel, label string) string {
	return strings.TrimSpace(channel) + "\x00" + strings.TrimSpace(label)
}
