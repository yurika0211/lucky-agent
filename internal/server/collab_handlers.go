package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/yurika0211/luckyagent/internal/collab"
)

// handleAgentsList 列出所有注册的 Agent
func (s *Server) handleAgentsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agents := s.collabRegistry.List()

	resp := struct {
		Agents  []collab.AgentProfile `json:"agents"`
		Count   int                   `json:"count"`
		Online  int                   `json:"online"`
	}{}

	for _, a := range agents {
		resp.Agents = append(resp.Agents, *a)
		resp.Count++
		if a.Status == collab.StatusOnline {
			resp.Online++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAgentsGet 获取单个 Agent 信息
func (s *Server) handleAgentsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := r.URL.Query().Get("id")
	if agentID == "" {
		http.Error(w, "missing agent id", http.StatusBadRequest)
		return
	}

	profile, ok := s.collabRegistry.Get(agentID)
	if !ok {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(profile)
}

// handleAgentsRegister 注册新 Agent
func (s *Server) handleAgentsRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID           string            `json:"id"`
		Name         string            `json:"name"`
		Description  string            `json:"description"`
		Capabilities []string          `json:"capabilities"`
		Metadata     map[string]string `json:"metadata"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "agent id is required", http.StatusBadRequest)
		return
	}

	profile := &collab.AgentProfile{
		ID:           req.ID,
		Name:         req.Name,
		Description:  req.Description,
		Capabilities: req.Capabilities,
		Status:       collab.StatusOnline,
		Metadata:     req.Metadata,
	}

	if err := s.collabRegistry.Register(profile); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "registered",
		"agent_id": req.ID,
	})
}

// handleAgentsDeregister 注销 Agent
func (s *Server) handleAgentsDeregister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := r.URL.Query().Get("id")
	if agentID == "" {
		http.Error(w, "missing agent id", http.StatusBadRequest)
		return
	}

	if err := s.collabRegistry.Deregister(agentID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "deregistered",
		"agent_id": agentID,
	})
}

// handleAgentsDelegate 创建协作任务
func (s *Server) handleAgentsDelegate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Mode        collab.CollabMode `json:"mode"`
		Description string            `json:"description"`
		Input       string            `json:"input"`
		AgentIDs    []string          `json:"agent_ids"`
		Timeout     time.Duration     `json:"timeout"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.AgentIDs) == 0 {
		http.Error(w, "at least one agent_id is required", http.StatusBadRequest)
		return
	}

	if req.Timeout == 0 {
		req.Timeout = 60 * time.Second
	}

	task, err := s.delegateManager.Delegate(r.Context(), req.Mode, req.Description, req.Input, req.AgentIDs, req.Timeout)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 使用 GetTask 获取深拷贝，避免 data race（后台 goroutine 正在修改 task）
	taskCopy, _ := s.delegateManager.GetTask(task.ID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(taskCopy)
}

// handleAgentsTask 获取任务状态
func (s *Server) handleAgentsTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}

	task, ok := s.delegateManager.GetTask(taskID)
	if !ok {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// handleAgentsTasks 列出所有任务
func (s *Server) handleAgentsTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tasks := s.delegateManager.ListTasks()

	resp := struct {
		Tasks []*collab.CollabTask `json:"tasks"`
		Count int                  `json:"count"`
	}{
		Tasks: tasks,
		Count: len(tasks),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAgentsCancel 取消任务
func (s *Server) handleAgentsCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}

	if err := s.delegateManager.CancelTask(taskID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "cancelled",
		"task_id": taskID,
	})
}