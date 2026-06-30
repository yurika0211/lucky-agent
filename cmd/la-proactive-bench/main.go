package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yurika0211/luckyagent/internal/proactive"
)

func main() {
	events := flag.Int("events", 10000, "number of synthetic proactive cycles")
	rounds := flag.Int("rounds", 3, "benchmark rounds")
	persist := flag.Bool("persist", false, "persist benchmark events to SQLite")
	flag.Parse()

	if *events <= 0 {
		fmt.Fprintln(os.Stderr, "events must be > 0")
		os.Exit(2)
	}
	if *rounds <= 0 {
		fmt.Fprintln(os.Stderr, "rounds must be > 0")
		os.Exit(2)
	}

	var store *proactive.Store
	var err error
	if *persist {
		dbPath := filepath.Join(os.TempDir(), fmt.Sprintf("la-proactive-bench-%d.db", time.Now().UnixNano()))
		store, err = proactive.OpenStore(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open store: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()
		defer os.Remove(dbPath)
		defer os.Remove(dbPath + "-wal")
		defer os.Remove(dbPath + "-shm")
	}

	total := time.Duration(0)
	for round := 1; round <= *rounds; round++ {
		elapsed, decisions, err := runRound(context.Background(), *events, store)
		if err != nil {
			fmt.Fprintf(os.Stderr, "round %d failed: %v\n", round, err)
			os.Exit(1)
		}
		total += elapsed
		fmt.Printf("round=%d events=%d decisions=%d elapsed_ms=%d per_event_us=%.2f\n",
			round,
			*events,
			decisions,
			elapsed.Milliseconds(),
			float64(elapsed.Microseconds())/float64(*events),
		)
	}
	avg := total / time.Duration(*rounds)
	fmt.Printf("avg events=%d rounds=%d persist=%t elapsed_ms=%d per_event_us=%.2f\n",
		*events,
		*rounds,
		*persist,
		avg.Milliseconds(),
		float64(avg.Microseconds())/float64(*events),
	)
}

func runRound(ctx context.Context, events int, store *proactive.Store) (time.Duration, int, error) {
	estimator := proactive.NewEstimator()
	gate := proactive.NewGate(proactive.Config{Enabled: true, DryRun: true, ConfidenceThreshold: 0.60, Horizon: 5 * time.Minute})
	calibrator := proactive.NewFeedbackCalibrator(store)
	start := time.Now()
	decisions := 0
	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < events; i++ {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		now := base.Add(time.Duration(i) * time.Minute)
		signals := syntheticSignals(now, i)
		estimate := estimator.Estimate(signals, gate.Config.Horizon)
		estimate = calibrator.Calibrate(estimate)
		decision, err := gate.Decide(ctx, signals, estimate)
		if err != nil {
			return 0, 0, err
		}
		decisions += len(decision.Actions)
		if store != nil {
			if err := store.RecordSignals(signals); err != nil {
				return 0, 0, err
			}
			if err := store.RecordEstimate(estimate); err != nil {
				return 0, 0, err
			}
			if err := store.RecordActions(decision.Actions); err != nil {
				return 0, 0, err
			}
		}
	}
	return time.Since(start), decisions, nil
}

func syntheticSignals(now time.Time, i int) []proactive.Signal {
	workspace := "go_repo"
	if i%5 == 0 {
		workspace = "node_repo"
	}
	return []proactive.Signal{
		{ID: fmt.Sprintf("time-%d", i), Channel: "time_of_day", Value: float64(now.Hour()*60+now.Minute()) / 1440.0, Label: syntheticTimeSegment(now), CreatedAt: now},
		{ID: fmt.Sprintf("weekday-%d", i), Channel: "day_of_week", Value: float64(now.Weekday()), Label: now.Weekday().String(), CreatedAt: now},
		{ID: fmt.Sprintf("workspace-%d", i), Channel: "workspace_context", Value: 1, Label: workspace, CreatedAt: now},
	}
}

func syntheticTimeSegment(t time.Time) string {
	h := t.Hour()
	switch {
	case h >= 9 && h < 12:
		return "morning_work"
	case h >= 14 && h < 18:
		return "afternoon_work"
	case h >= 18 && h < 23:
		return "evening"
	case h < 5:
		return "deep_night"
	default:
		return "midday"
	}
}
