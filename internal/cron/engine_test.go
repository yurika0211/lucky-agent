package cron

import (
	"fmt"
	"testing"
	"time"
)

// --- Schedule Tests ---

func TestIntervalSchedule(t *testing.T) {
	s := IntervalSchedule{Interval: 5 * time.Minute}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	next := s.Next(now)
	expected := now.Add(5 * time.Minute)
	if next != expected {
		t.Errorf("IntervalSchedule.Next = %v, want %v", next, expected)
	}
}

func TestDailySchedule(t *testing.T) {
	s := DailySchedule{Hour: 9, Minute: 30}

	// 同一天之前
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	next := s.Next(now)
	expected := time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("DailySchedule.Next = %v, want %v", next, expected)
	}

	// 同一天之后 → 下一天
	now = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	next = s.Next(now)
	expected = time.Date(2026, 1, 2, 9, 30, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("DailySchedule.Next = %v, want %v", next, expected)
	}

	// 恰好同一时刻 → 下一天
	now = time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)
	next = s.Next(now)
	expected = time.Date(2026, 1, 2, 9, 30, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("DailySchedule.Next = %v, want %v", next, expected)
	}
}

func TestOnceSchedule(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := OnceSchedule{At: at}

	// 之前
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next := s.Next(now)
	if next != at {
		t.Errorf("OnceSchedule.Next = %v, want %v", next, at)
	}

	// 之后
	now = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	next = s.Next(now)
	if !next.IsZero() {
		t.Errorf("OnceSchedule.Next after expiry = %v, want zero", next)
	}
}

func TestCronSchedule(t *testing.T) {
	// 每小时第30分: 30 * * * *
	s := CronSchedule{
		Minute:  []int{30},
		Hour:    []int{},
		Day:     []int{},
		Month:   []int{},
		Weekday: []int{},
	}

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	next := s.Next(now)
	expected := time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("CronSchedule.Next = %v, want %v", next, expected)
	}
}

func TestCronScheduleWeekday(t *testing.T) {
	// 每周一 9:00: 0 9 * * 1
	s := CronSchedule{
		Minute:  []int{0},
		Hour:    []int{9},
		Day:     []int{},
		Month:   []int{},
		Weekday: []int{1}, // Monday
	}

	// 2026-01-01 is Thursday
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	next := s.Next(now)
	// Next Monday is 2026-01-05
	expected := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("CronSchedule weekday = %v, want %v", next, expected)
	}
}

// --- ParseCronExpr Tests ---

func TestParseCronExpr(t *testing.T) {
	tests := []struct {
		expr    string
		wantErr bool
		minute  []int
		hour    []int
	}{
		{"* * * * *", false, []int{}, []int{}},
		{"30 * * * *", false, []int{30}, []int{}},
		{"0 9 * * *", false, []int{0}, []int{9}},
		{"*/15 * * * *", false, []int{0, 15, 30, 45}, []int{}},
		{"0 9-17 * * *", false, []int{0}, []int{9, 10, 11, 12, 13, 14, 15, 16, 17}},
		{"0 9,12,18 * * *", false, []int{0}, []int{9, 12, 18}},
		{"0 9 * * 1-5", false, []int{0}, []int{9}},
		{"* * * *", true, []int{}, []int{}},    // only 4 fields
		{"60 * * * *", true, []int{}, []int{}}, // invalid minute
		{"0 25 * * *", true, []int{}, []int{}}, // invalid hour
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			s, err := ParseCronExpr(tt.expr)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseCronExpr(%q) expected error, got nil", tt.expr)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseCronExpr(%q) unexpected error: %v", tt.expr, err)
				return
			}
			if len(s.Minute) != len(tt.minute) {
				t.Errorf("minute = %v, want %v", s.Minute, tt.minute)
			}
			if len(s.Hour) != len(tt.hour) {
				t.Errorf("hour = %v, want %v", s.Hour, tt.hour)
			}
		})
	}
}

// --- Engine Tests ---

func TestEngineAddJob(t *testing.T) {
	e := NewEngine()
	err := e.AddJob("test", "Test Job", "desc", IntervalSchedule{Interval: 1 * time.Hour}, func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if e.JobCount() != 1 {
		t.Errorf("JobCount = %d, want 1", e.JobCount())
	}

	// Duplicate
	err = e.AddJob("test", "Test Job", "desc", IntervalSchedule{Interval: 1 * time.Hour}, func() error {
		return nil
	})
	if err == nil {
		t.Error("AddJob duplicate should fail")
	}
}

func TestEngineRemoveJob(t *testing.T) {
	e := NewEngine()
	e.AddJob("test", "Test Job", "desc", IntervalSchedule{Interval: 1 * time.Hour}, func() error {
		return nil
	})

	err := e.RemoveJob("test")
	if err != nil {
		t.Fatalf("RemoveJob failed: %v", err)
	}
	if e.JobCount() != 0 {
		t.Errorf("JobCount = %d, want 0", e.JobCount())
	}

	// Not found
	err = e.RemoveJob("nonexistent")
	if err == nil {
		t.Error("RemoveJob nonexistent should fail")
	}
}

func TestEnginePauseResume(t *testing.T) {
	e := NewEngine()
	e.AddJob("test", "Test Job", "desc", IntervalSchedule{Interval: 1 * time.Hour}, func() error {
		return nil
	})

	if err := e.PauseJob("test"); err != nil {
		t.Fatalf("PauseJob failed: %v", err)
	}
	job, ok := e.GetJob("test")
	if !ok || job.Status != StatusPaused {
		t.Errorf("status = %v, want paused", job.Status)
	}

	if err := e.ResumeJob("test"); err != nil {
		t.Fatalf("ResumeJob failed: %v", err)
	}
	job, ok = e.GetJob("test")
	if !ok || job.Status != StatusIdle {
		t.Errorf("status = %v, want idle", job.Status)
	}
}

func TestEngineListJobs(t *testing.T) {
	e := NewEngine()
	e.AddJob("j1", "Job 1", "", IntervalSchedule{Interval: 1 * time.Hour}, func() error { return nil })
	e.AddJob("j2", "Job 2", "", IntervalSchedule{Interval: 2 * time.Hour}, func() error { return nil })

	jobs := e.ListJobs()
	if len(jobs) != 2 {
		t.Errorf("ListJobs count = %d, want 2", len(jobs))
	}
}

func TestEngineEventHandler(t *testing.T) {
	e := NewEngine()
	var events []Event
	e.SetEventHandler(func(ev Event) {
		events = append(events, ev)
	})

	e.AddJob("test", "Test Job", "desc", IntervalSchedule{Interval: 1 * time.Hour}, func() error {
		return nil
	})

	if len(events) != 1 || events[0].Type != EventJobAdded {
		t.Errorf("events = %v, want job_added", events)
	}

	e.RemoveJob("test")
	if len(events) != 2 || events[1].Type != EventJobRemoved {
		t.Errorf("events = %v, want job_removed", events)
	}
}

func TestEngineStartStop(t *testing.T) {
	e := NewEngine()
	if e.IsRunning() {
		t.Error("engine should not be running initially")
	}

	e.Start()
	if !e.IsRunning() {
		t.Error("engine should be running after Start")
	}

	// Double start should be safe
	e.Start()

	e.Stop()
	if e.IsRunning() {
		t.Error("engine should not be running after Stop")
	}

	// Double stop should be safe
	e.Stop()
}

func TestEngineJobExecution(t *testing.T) {
	e := NewEngine()
	var executed int

	schedule := &immediateSchedule{}
	e.AddJob("test", "Test Job", "desc", schedule, func() error {
		executed++
		return nil
	})

	e.Start()
	defer e.Stop()

	// 手动触发 tick
	e.tick(time.Now())

	if executed == 0 {
		t.Error("job should have been executed")
	}
}

func TestEngineJobFailure(t *testing.T) {
	e := NewEngine()
	var events []Event
	e.SetEventHandler(func(ev Event) {
		events = append(events, ev)
	})

	schedule := &immediateSchedule{}
	e.AddJob("fail", "Fail Job", "desc", schedule, func() error {
		return fmt.Errorf("test error")
	})

	e.tick(time.Now())

	// 检查是否有失败事件
	found := false
	for _, ev := range events {
		if ev.Type == EventJobFailed {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected JobFailed event")
	}

	job, ok := e.GetJob("fail")
	if !ok {
		t.Fatal("job not found")
	}
	if job.ErrorCount == 0 {
		t.Error("expected error count > 0")
	}
}

// immediateSchedule 立即触发的调度（用于测试）
type immediateSchedule struct{}

func (s *immediateSchedule) Next(from time.Time) time.Time {
	return from.Add(-1 * time.Second) // 过去时间，tick 时立即执行
}

func (s *immediateSchedule) String() string {
	return "immediate"
}

// --- Natural Language Parsing Tests ---

func TestParseNaturalLanguage(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"每天9点", false},
		{"每小时", false},
		{"每六个小时", false},
		{"每30分钟", false},
		{"每周一9点", false},
		{"无效输入", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := ParseNaturalLanguage(tt.input)
			if tt.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
