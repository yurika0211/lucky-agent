package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/cron"
)

func TestHandleCronListsJobs(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())
	engine := a.CronEngine()
	if engine == nil {
		t.Fatal("expected cron engine")
	}

	if err := engine.AddJob("job-a", "Alpha", "first", cron.DailySchedule{Hour: 9, Minute: 30}, func() error { return nil }); err != nil {
		t.Fatalf("add job-a: %v", err)
	}
	if err := engine.AddJob("job-b", "Beta", "second", cron.IntervalSchedule{Interval: time.Hour}, func() error { return nil }); err != nil {
		t.Fatalf("add job-b: %v", err)
	}
	if err := engine.PauseJob("job-b"); err != nil {
		t.Fatalf("pause job-b: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cron", nil)
	w := httptest.NewRecorder()
	s.handleCron(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}

	var response struct {
		Running bool `json:"running"`
		Count   int  `json:"count"`
		Jobs    []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Schedule string `json:"schedule"`
			Status   string `json:"status"`
		} `json:"jobs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Count != 2 || len(response.Jobs) != 2 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Jobs[0].ID != "job-a" || response.Jobs[1].ID != "job-b" {
		t.Fatalf("jobs not sorted by id: %+v", response.Jobs)
	}
	if response.Jobs[0].Schedule == "" || response.Jobs[1].Schedule == "" {
		t.Fatalf("expected schedule text: %+v", response.Jobs)
	}
	if response.Jobs[1].Status != "paused" {
		t.Fatalf("expected job-b paused, got %+v", response.Jobs[1])
	}

	methodNotAllowed := httptest.NewRecorder()
	s.handleCron(methodNotAllowed, httptest.NewRequest(http.MethodPost, "/api/v1/cron", nil))
	if methodNotAllowed.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status: got %d", methodNotAllowed.Code)
	}
}
