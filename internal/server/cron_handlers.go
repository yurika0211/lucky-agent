package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/yurika0211/luckyagent/internal/cron"
)

type cronJobResponse struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Schedule       string            `json:"schedule"`
	Status         string            `json:"status"`
	LastRun        *string           `json:"last_run,omitempty"`
	NextRun        *string           `json:"next_run,omitempty"`
	RunCount       int               `json:"run_count"`
	ErrorCount     int               `json:"error_count"`
	LastError      string            `json:"last_error,omitempty"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
	DeleteAfterRun bool              `json:"delete_after_run"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	if s.agent == nil || s.agent.CronEngine() == nil {
		s.sendError(w, "cron engine unavailable", http.StatusServiceUnavailable, "")
		return
	}

	jobs := s.agent.CronEngine().ListJobs()
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].ID < jobs[j].ID
	})
	items := make([]cronJobResponse, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, cronJobResponse{
			ID:             job.ID,
			Name:           job.Name,
			Description:    job.Description,
			Schedule:       cron.DescribeSchedule(job.Schedule),
			Status:         job.Status.String(),
			LastRun:        formatCronTime(job.LastRun),
			NextRun:        formatCronTime(job.NextRun),
			RunCount:       job.RunCount,
			ErrorCount:     job.ErrorCount,
			LastError:      job.LastError,
			CreatedAt:      job.CreatedAt.Format(time.RFC3339),
			UpdatedAt:      job.UpdatedAt.Format(time.RFC3339),
			DeleteAfterRun: job.DeleteAfterRun,
			Metadata:       job.Metadata,
		})
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"running": s.agent.CronEngine().IsRunning(),
		"count":   len(items),
		"jobs":    items,
	})
}

func formatCronTime(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := value.Format(time.RFC3339)
	return &formatted
}
