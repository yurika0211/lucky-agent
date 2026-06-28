package learning

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const progressFileName = "progress.json"

// ProgressStore persists learning progress under the LuckyAgent home.
type ProgressStore struct {
	dir  string
	path string
}

// Progress is the full persisted learning state.
type Progress struct {
	ActiveCourseID string                    `json:"active_course_id,omitempty"`
	Courses        map[string]CourseProgress `json:"courses,omitempty"`
	UpdatedAt      time.Time                 `json:"updated_at,omitempty"`
}

// CourseProgress tracks one learner's progress in a course.
type CourseProgress struct {
	CourseID      string                    `json:"course_id"`
	CurrentModule string                    `json:"current_module"`
	StartedAt     time.Time                 `json:"started_at"`
	UpdatedAt     time.Time                 `json:"updated_at"`
	CompletedAt   *time.Time                `json:"completed_at,omitempty"`
	Modules       map[string]ModuleProgress `json:"modules,omitempty"`
}

// ModuleProgress tracks lab attempts and completion state.
type ModuleProgress struct {
	ModuleID    string     `json:"module_id"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	Evidence    []Evidence `json:"evidence,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Score       float64    `json:"score,omitempty"`
	Review      string     `json:"review,omitempty"`
}

// Evidence is a learner-submitted proof item for a lab.
type Evidence struct {
	Text     string    `json:"text"`
	AddedAt  time.Time `json:"added_at"`
	Accepted bool      `json:"accepted"`
	Reviewer string    `json:"reviewer,omitempty"`
}

// NewProgressStore creates a store at homeDir/learning/progress.json.
func NewProgressStore(homeDir string) *ProgressStore {
	dir := filepath.Join(homeDir, "learning")
	return &ProgressStore{dir: dir, path: filepath.Join(dir, progressFileName)}
}

func (s *ProgressStore) Path() string {
	return s.path
}

func (s *ProgressStore) Load() (Progress, error) {
	if s == nil {
		return Progress{}, fmt.Errorf("learning store is nil")
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Progress{Courses: map[string]CourseProgress{}}, nil
	}
	if err != nil {
		return Progress{}, fmt.Errorf("read learning progress: %w", err)
	}
	var p Progress
	if err := json.Unmarshal(data, &p); err != nil {
		return Progress{}, fmt.Errorf("parse learning progress: %w", err)
	}
	if p.Courses == nil {
		p.Courses = map[string]CourseProgress{}
	}
	return p, nil
}

func (s *ProgressStore) Save(p Progress) error {
	if s == nil {
		return fmt.Errorf("learning store is nil")
	}
	if p.Courses == nil {
		p.Courses = map[string]CourseProgress{}
	}
	p.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create learning dir: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("encode learning progress: %w", err)
	}
	if err := os.WriteFile(s.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write learning progress: %w", err)
	}
	return nil
}

// StartCourse starts or resumes a course and makes it active.
func (s *ProgressStore) StartCourse(course Course) (CourseProgress, error) {
	if err := course.Validate(); err != nil {
		return CourseProgress{}, err
	}
	p, err := s.Load()
	if err != nil {
		return CourseProgress{}, err
	}
	now := time.Now().UTC()
	cp, ok := p.Courses[course.ID]
	if !ok {
		first := course.Modules[0]
		cp = CourseProgress{
			CourseID:      course.ID,
			CurrentModule: first.ID,
			StartedAt:     now,
			UpdatedAt:     now,
			Modules: map[string]ModuleProgress{
				first.ID: {
					ModuleID:  first.ID,
					Status:    "active",
					StartedAt: now,
					UpdatedAt: now,
				},
			},
		}
	} else {
		if cp.Modules == nil {
			cp.Modules = map[string]ModuleProgress{}
		}
		if cp.CurrentModule == "" && len(course.Modules) > 0 {
			cp.CurrentModule = course.Modules[0].ID
		}
		if cp.CurrentModule != "" {
			mp := cp.Modules[cp.CurrentModule]
			if mp.ModuleID == "" {
				mp.ModuleID = cp.CurrentModule
			}
			if mp.Status == "" {
				mp.Status = "active"
			}
			if mp.StartedAt.IsZero() {
				mp.StartedAt = now
			}
			mp.UpdatedAt = now
			cp.Modules[cp.CurrentModule] = mp
		}
		cp.UpdatedAt = now
	}
	p.ActiveCourseID = course.ID
	p.Courses[course.ID] = cp
	if err := s.Save(p); err != nil {
		return CourseProgress{}, err
	}
	return cp, nil
}

// SubmitEvidence records learner evidence and marks the module completed.
func (s *ProgressStore) SubmitEvidence(course Course, evidenceText string, accept bool) (CourseProgress, ModuleProgress, error) {
	if err := course.Validate(); err != nil {
		return CourseProgress{}, ModuleProgress{}, err
	}
	evidenceText = strings.TrimSpace(evidenceText)
	if evidenceText == "" {
		return CourseProgress{}, ModuleProgress{}, fmt.Errorf("evidence is required")
	}

	p, err := s.Load()
	if err != nil {
		return CourseProgress{}, ModuleProgress{}, err
	}
	cp, ok := p.Courses[course.ID]
	if !ok {
		cp, err = s.StartCourse(course)
		if err != nil {
			return CourseProgress{}, ModuleProgress{}, err
		}
		p, err = s.Load()
		if err != nil {
			return CourseProgress{}, ModuleProgress{}, err
		}
		cp = p.Courses[course.ID]
	}
	if cp.Modules == nil {
		cp.Modules = map[string]ModuleProgress{}
	}
	now := time.Now().UTC()
	moduleID := cp.CurrentModule
	if moduleID == "" {
		moduleID = course.Modules[0].ID
		cp.CurrentModule = moduleID
	}
	mp := cp.Modules[moduleID]
	if mp.ModuleID == "" {
		mp.ModuleID = moduleID
	}
	if mp.StartedAt.IsZero() {
		mp.StartedAt = now
	}
	mp.Attempts++
	mp.Evidence = append(mp.Evidence, Evidence{
		Text:     evidenceText,
		AddedAt:  now,
		Accepted: accept,
		Reviewer: "lh learn",
	})
	mp.UpdatedAt = now
	if accept {
		mp.Status = "completed"
		mp.Score = 1
		mp.Review = "Accepted by evidence submission. Run a human or agent review for richer feedback."
		completedAt := now
		mp.CompletedAt = &completedAt
		cp.Modules[moduleID] = mp
		cp = advanceCourse(course, cp, now)
	} else if mp.Status == "" {
		mp.Status = "active"
		cp.Modules[moduleID] = mp
	} else {
		cp.Modules[moduleID] = mp
	}
	cp.UpdatedAt = now
	p.ActiveCourseID = course.ID
	p.Courses[course.ID] = cp
	if err := s.Save(p); err != nil {
		return CourseProgress{}, ModuleProgress{}, err
	}
	return cp, mp, nil
}

func advanceCourse(course Course, cp CourseProgress, now time.Time) CourseProgress {
	idx := course.IndexOfModule(cp.CurrentModule)
	if idx < 0 {
		cp.CurrentModule = course.Modules[0].ID
		return cp
	}
	nextIdx := idx + 1
	if nextIdx >= len(course.Modules) {
		completedAt := now
		cp.CompletedAt = &completedAt
		return cp
	}
	next := course.Modules[nextIdx]
	cp.CurrentModule = next.ID
	if cp.Modules == nil {
		cp.Modules = map[string]ModuleProgress{}
	}
	if _, ok := cp.Modules[next.ID]; !ok {
		cp.Modules[next.ID] = ModuleProgress{
			ModuleID:  next.ID,
			Status:    "active",
			StartedAt: now,
			UpdatedAt: now,
		}
	}
	return cp
}

// CourseCompletion returns completed and total module counts.
func CourseCompletion(course Course, cp CourseProgress) (int, int) {
	total := len(course.Modules)
	done := 0
	for _, m := range course.Modules {
		if cp.Modules[m.ID].Status == "completed" {
			done++
		}
	}
	return done, total
}
