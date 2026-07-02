package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	taskstore "github.com/yurika0211/luckyagent/internal/task"
)

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
	default:
		s.sendError(w, "not found", http.StatusNotFound, "")
	}
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
