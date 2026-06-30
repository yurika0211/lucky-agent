package proactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Sampler collects low-risk local signals. It deliberately avoids invasive
// OS activity capture in the first slice.
type Sampler struct {
	Now        func() time.Time
	WorkingDir string
}

func NewSampler(workingDir string) Sampler {
	return Sampler{WorkingDir: strings.TrimSpace(workingDir)}
}

func (s Sampler) Sample(ctx context.Context) ([]Signal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	signals := []Signal{
		{
			ID:        signalID("time", now, 1),
			Channel:   "time_of_day",
			Value:     float64(now.Hour()*60+now.Minute()) / 1440.0,
			Label:     timeSegment(now),
			Metadata:  map[string]string{"hour": fmt.Sprintf("%02d", now.Hour()), "minute": fmt.Sprintf("%02d", now.Minute())},
			CreatedAt: now,
		},
		{
			ID:        signalID("weekday", now, 2),
			Channel:   "day_of_week",
			Value:     float64(now.Weekday()),
			Label:     strings.ToLower(now.Weekday().String()),
			Metadata:  map[string]string{"is_weekend": fmt.Sprintf("%t", now.Weekday() == time.Saturday || now.Weekday() == time.Sunday)},
			CreatedAt: now,
		},
	}

	workspaceSignal, ok, err := s.workspaceSignal(now)
	if err != nil {
		return nil, err
	}
	if ok {
		signals = append(signals, workspaceSignal)
	}
	return signals, nil
}

func (s Sampler) workspaceSignal(now time.Time) (Signal, bool, error) {
	dir := s.WorkingDir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return Signal{}, false, fmt.Errorf("get working dir: %w", err)
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Signal{}, false, fmt.Errorf("resolve working dir: %w", err)
	}
	kind := detectWorkspaceKind(abs)
	if kind == "" {
		return Signal{}, false, nil
	}
	return Signal{
		ID:        signalID("workspace", now, 3),
		Channel:   "workspace_context",
		Value:     1,
		Label:     kind,
		Metadata:  map[string]string{"path": abs, "name": filepath.Base(abs)},
		CreatedAt: now,
	}, true, nil
}

func timeSegment(t time.Time) string {
	h := t.Hour()
	switch {
	case h >= 0 && h < 5:
		return "deep_night"
	case h >= 5 && h < 9:
		return "morning_ramp"
	case h >= 9 && h < 12:
		return "morning_work"
	case h >= 12 && h < 14:
		return "midday"
	case h >= 14 && h < 18:
		return "afternoon_work"
	case h >= 18 && h < 23:
		return "evening"
	default:
		return "late_night"
	}
}

func detectWorkspaceKind(dir string) string {
	checks := []struct {
		path string
		kind string
	}{
		{"go.mod", "go_repo"},
		{"package.json", "node_repo"},
		{".git", "git_repo"},
	}
	for _, check := range checks {
		if _, err := os.Stat(filepath.Join(dir, check.path)); err == nil {
			return check.kind
		}
	}
	return ""
}

func signalID(prefix string, t time.Time, seq int) string {
	return fmt.Sprintf("%s-%d-%02d", prefix, t.UnixNano(), seq)
}
