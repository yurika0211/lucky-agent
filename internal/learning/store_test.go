package learning

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinCoursesValidate(t *testing.T) {
	courses := BuiltinCourses()
	if len(courses) == 0 {
		t.Fatal("expected builtin courses")
	}
	for _, course := range courses {
		if err := course.Validate(); err != nil {
			t.Fatalf("course %s should validate: %v", course.ID, err)
		}
	}
}

func TestProgressStoreStartCourse(t *testing.T) {
	course, ok := FindCourse("lh-agent")
	if !ok {
		t.Fatal("expected course")
	}
	store := NewProgressStore(t.TempDir())

	cp, err := store.StartCourse(course)
	if err != nil {
		t.Fatalf("StartCourse: %v", err)
	}
	if cp.CourseID != course.ID {
		t.Fatalf("expected course id %q, got %q", course.ID, cp.CourseID)
	}
	if cp.CurrentModule != course.Modules[0].ID {
		t.Fatalf("expected first module active, got %q", cp.CurrentModule)
	}
	if cp.Modules[cp.CurrentModule].Status != "active" {
		t.Fatalf("expected active module, got %#v", cp.Modules[cp.CurrentModule])
	}
	if !strings.HasSuffix(store.Path(), filepath.Join("learning", progressFileName)) {
		t.Fatalf("unexpected progress path %q", store.Path())
	}
}

func TestProgressStoreSubmitEvidenceAdvancesModule(t *testing.T) {
	course, ok := FindCourse("lh-agent-systems")
	if !ok {
		t.Fatal("expected course")
	}
	store := NewProgressStore(t.TempDir())
	if _, err := store.StartCourse(course); err != nil {
		t.Fatalf("StartCourse: %v", err)
	}

	cp, mp, err := store.SubmitEvidence(course, "go test ./internal/gateway/telegram passed", true)
	if err != nil {
		t.Fatalf("SubmitEvidence: %v", err)
	}
	if mp.Status != "completed" {
		t.Fatalf("expected completed module, got %q", mp.Status)
	}
	if len(mp.Evidence) != 1 {
		t.Fatalf("expected evidence, got %#v", mp.Evidence)
	}
	if cp.CurrentModule != course.Modules[1].ID {
		t.Fatalf("expected next module %q, got %q", course.Modules[1].ID, cp.CurrentModule)
	}
	done, total := CourseCompletion(course, cp)
	if done != 1 || total != len(course.Modules) {
		t.Fatalf("unexpected completion %d/%d", done, total)
	}
}
