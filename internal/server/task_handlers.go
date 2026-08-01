package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	taskstore "github.com/yurika0211/luckyagent/internal/task"
)

type taskFeedbackRequest struct {
	Status          taskstore.Status       `json:"status"`
	Verified        bool                   `json:"verified"`
	Verifier        string                 `json:"verifier"`
	UserFeedback    string                 `json:"user_feedback"`
	Score           float64                `json:"score"`
	Cost            taskstore.CostSnapshot `json:"cost"`
	RecommendedNext string                 `json:"recommended_next"`
}

type taskCancelRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	store := s.agent.TaskStore()
	if store == nil {
		s.sendError(w, "task store not initialized", http.StatusServiceUnavailable, "")
		return
	}
	filter := taskstore.ListFilter{
		Source:   taskstore.Source(strings.TrimSpace(r.URL.Query().Get("source"))),
		Status:   taskstore.Status(strings.TrimSpace(r.URL.Query().Get("status"))),
		ParentID: strings.TrimSpace(r.URL.Query().Get("parent_id")),
		Limit:    boundedTasksLimit(r.URL.Query().Get("limit")),
	}
	records, err := store.List(filter)
	if err != nil {
		s.sendError(w, "list tasks failed", http.StatusInternalServerError, err.Error())
		return
	}
	s.sendJSON(w, http.StatusOK, map[string]any{
		"tasks": records,
		"count": len(records),
	})
}

func (s *Server) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	store := s.agent.TaskStore()
	if store == nil {
		s.sendError(w, "task store not initialized", http.StatusServiceUnavailable, "")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/"), "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		s.sendError(w, "task id is required", http.StatusBadRequest, "")
		return
	}
	taskID := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		record, ok, err := store.Get(taskID)
		if err != nil {
			s.sendError(w, "get task failed", http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			s.sendError(w, "task not found", http.StatusNotFound, taskID)
			return
		}
		s.sendJSON(w, http.StatusOK, record)
		return
	}

	switch parts[1] {
	case "events":
		if r.Method != http.MethodGet {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		events, err := store.Events(taskID)
		if err != nil {
			s.sendError(w, "get task events failed", http.StatusInternalServerError, err.Error())
			return
		}
		s.sendJSON(w, http.StatusOK, map[string]any{
			"task_id": taskID,
			"events":  events,
			"count":   len(events),
		})
	case "trace":
		if r.Method != http.MethodGet {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		trace, ok, err := store.PlannerTrace(taskID)
		if err != nil {
			s.sendError(w, "get task trace failed", http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			s.sendError(w, "task trace not found", http.StatusNotFound, taskID)
			return
		}
		var payload any
		if err := json.Unmarshal(trace, &payload); err != nil {
			s.sendError(w, "task trace is invalid", http.StatusInternalServerError, err.Error())
			return
		}
		s.sendJSON(w, http.StatusOK, payload)
	case "observation":
		if r.Method != http.MethodGet {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		record, ok, err := store.Get(taskID)
		if err != nil {
			s.sendError(w, "get task failed", http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			s.sendError(w, "task not found", http.StatusNotFound, taskID)
			return
		}
		events, err := store.Events(taskID)
		if err != nil {
			s.sendError(w, "get task events failed", http.StatusInternalServerError, err.Error())
			return
		}
		s.sendJSON(w, http.StatusOK, taskstore.ReduceObservation(record, events))
	case "feedback":
		if r.Method != http.MethodPost {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		s.handleTaskFeedback(w, r, store, taskID)
	case "cancel":
		if r.Method != http.MethodPost {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		s.handleTaskCancel(w, r, store, taskID)
	default:
		s.sendError(w, "not found", http.StatusNotFound, "")
	}
}

func (s *Server) handleTaskFeedback(w http.ResponseWriter, r *http.Request, store taskstore.Store, taskID string) {
	var req taskFeedbackRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			s.sendError(w, "invalid feedback body", http.StatusBadRequest, err.Error())
			return
		}
	}
	record, ok, err := store.Get(taskID)
	if err != nil {
		s.sendError(w, "get task failed", http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		s.sendError(w, "task not found", http.StatusNotFound, taskID)
		return
	}
	record.Outcome = taskstore.Outcome{
		Status:          firstStatus(req.Status, record.Status),
		Verified:        req.Verified,
		Verifier:        strings.TrimSpace(req.Verifier),
		UserFeedback:    strings.TrimSpace(req.UserFeedback),
		Score:           req.Score,
		Cost:            req.Cost,
		RecommendedNext: strings.TrimSpace(req.RecommendedNext),
	}
	if err := store.Update(record); err != nil {
		s.sendError(w, "record task feedback failed", http.StatusInternalServerError, err.Error())
		return
	}
	if err := store.AppendEvent(taskstore.Event{
		Type:    taskstore.EventOutcomeRecorded,
		TaskID:  record.ID,
		Time:    time.Now(),
		Status:  record.Outcome.Status,
		Message: record.Outcome.UserFeedback,
		Cost:    record.Outcome.Cost,
		Metadata: map[string]string{
			"verified":         fmt.Sprintf("%t", record.Outcome.Verified),
			"verifier":         record.Outcome.Verifier,
			"score":            fmt.Sprintf("%.4f", record.Outcome.Score),
			"recommended_next": record.Outcome.RecommendedNext,
		},
	}); err != nil {
		s.sendError(w, "append task feedback event failed", http.StatusInternalServerError, err.Error())
		return
	}
	s.sendJSON(w, http.StatusOK, record)
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request, store taskstore.Store, taskID string) {
	var req taskCancelRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			s.sendError(w, "invalid cancel body", http.StatusBadRequest, err.Error())
			return
		}
	}
	record, ok, err := store.Get(taskID)
	if err != nil {
		s.sendError(w, "get task failed", http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		s.sendError(w, "task not found", http.StatusNotFound, taskID)
		return
	}
	switch record.Status {
	case taskstore.StatusCompleted, taskstore.StatusFailed, taskstore.StatusCancelled:
		s.sendError(w, "task cannot be cancelled from current status", http.StatusConflict, string(record.Status))
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "cancelled by user"
	}
	if s.delegateManager != nil && record.Source == taskstore.SourceHTTP {
		_ = s.delegateManager.CancelTask(record.ID)
	}
	record.Status = taskstore.StatusCancelled
	now := time.Now()
	record.CompletedAt = now
	record.Outcome.Status = taskstore.StatusCancelled
	record.Outcome.UserFeedback = reason
	if err := store.Update(record); err != nil {
		s.sendError(w, "cancel task failed", http.StatusInternalServerError, err.Error())
		return
	}
	if err := store.AppendEvent(taskstore.Event{
		Type:    taskstore.EventCancelled,
		TaskID:  record.ID,
		Time:    now,
		Status:  taskstore.StatusCancelled,
		Message: reason,
	}); err != nil {
		s.sendError(w, "append task cancellation event failed", http.StatusInternalServerError, err.Error())
		return
	}
	s.sendJSON(w, http.StatusOK, record)
}

func firstStatus(primary, fallback taskstore.Status) taskstore.Status {
	if primary != "" {
		return primary
	}
	return fallback
}

func boundedTasksLimit(raw string) int {
	limit := 50
	if strings.TrimSpace(raw) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			limit = n
		}
	}
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}
