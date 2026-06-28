package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/gateway/telegram"
	"github.com/yurika0211/luckyagent/internal/utils"
	"github.com/yurika0211/luckyagent/internal/workflow"
)

// ============================================================================
// v0.24.0: Workflow Engine Handlers
// ============================================================================

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	s.dispatchMethod(w, r, map[string]func(){
		http.MethodGet: func() {
			workflows := s.workflowEngine.ListWorkflows()
			s.sendJSON(w, http.StatusOK, map[string]interface{}{
				"workflows": workflows,
				"count":     len(workflows),
			})
		},
		http.MethodPost: func() {
			var req struct {
				Name        string           `json:"name"`
				Description string           `json:"description,omitempty"`
				Tasks       []*workflow.Task `json:"tasks"`
				Version     string           `json:"version,omitempty"`
			}
			if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
				s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
				return
			}

			wf := workflow.NewWorkflow(req.Name, req.Tasks)
			wf.Description = req.Description
			wf.Version = req.Version

			if err := s.workflowEngine.RegisterWorkflow(wf); err != nil {
				s.sendError(w, "invalid workflow", http.StatusBadRequest, err.Error())
				return
			}

			s.sendJSON(w, http.StatusCreated, wf)
		},
	})
}

func (s *Server) handleWorkflowByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/workflows/")
	if id == "" {
		s.sendError(w, "workflow ID required", http.StatusBadRequest, "")
		return
	}

	s.dispatchMethod(w, r, map[string]func(){
		http.MethodGet: func() {
			wf, ok := s.workflowEngine.GetWorkflow(id)
			if !ok {
				s.sendError(w, "workflow not found", http.StatusNotFound, "")
				return
			}
			s.sendJSON(w, http.StatusOK, wf)
		},
		http.MethodDelete: func() {
			if err := s.workflowEngine.DeleteWorkflow(id); err != nil {
				s.sendError(w, "failed to delete workflow", http.StatusInternalServerError, err.Error())
				return
			}
			s.sendJSON(w, http.StatusOK, map[string]string{"message": "workflow deleted"})
		},
	})
}

func (s *Server) handleWorkflowInstances(w http.ResponseWriter, r *http.Request) {
	s.dispatchMethod(w, r, map[string]func(){
		http.MethodGet: func() {
			instances := s.workflowEngine.ListInstances()
			s.sendJSON(w, http.StatusOK, map[string]interface{}{
				"instances": instances,
				"count":     len(instances),
			})
		},
		http.MethodPost: func() {
			var req struct {
				WorkflowID string `json:"workflowId"`
			}
			if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
				s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
				return
			}

			instance, err := s.workflowEngine.StartWorkflow(req.WorkflowID)
			if err != nil {
				s.sendError(w, "failed to start workflow", http.StatusBadRequest, err.Error())
				return
			}

			s.sendJSON(w, http.StatusCreated, instance)
		},
	})
}

func (s *Server) handleWorkflowInstanceByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/workflow-instances/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		s.sendError(w, "instance ID required", http.StatusBadRequest, "")
		return
	}

	id := parts[0]

	s.dispatchMethod(w, r, map[string]func(){
		http.MethodGet: func() {
			instance, ok := s.workflowEngine.GetInstance(id)
			if !ok {
				s.sendError(w, "instance not found", http.StatusNotFound, "")
				return
			}

			// Check if requesting results
			if len(parts) > 1 && parts[1] == "results" {
				s.sendJSON(w, http.StatusOK, map[string]interface{}{
					"instanceId": instance.ID,
					"status":     instance.GetStatus(),
					"results":    instance.Results,
				})
				return
			}

			s.sendJSON(w, http.StatusOK, instance)
		},
		http.MethodDelete: func() {
			if err := s.workflowEngine.CancelInstance(id); err != nil {
				s.sendError(w, "failed to cancel instance", http.StatusNotFound, err.Error())
				return
			}
			s.sendJSON(w, http.StatusOK, map[string]string{"message": "instance cancelled"})
		},
	})
}

// ===== v0.6.0: Messaging Gateway Handlers =====

func (s *Server) handleGatewaysList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	gm := s.agent.MsgGateway()
	statuses := gm.Status()

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"gateways": statuses,
		"count":    len(statuses),
	})
}

func (s *Server) handleGatewayTelegramStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	var req struct {
		Token        string   `json:"token"`
		AllowedChats []string `json:"allowed_chats,omitempty"`
		AdminIDs     []string `json:"admin_ids,omitempty"`
	}
	if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
		return
	}

	if req.Token == "" {
		s.sendError(w, "token is required", http.StatusBadRequest, "")
		return
	}

	gm := s.agent.MsgGateway()

	// Check if already registered
	if _, exists := gm.Get("telegram"); exists {
		s.sendError(w, "telegram gateway already registered", http.StatusConflict, "")
		return
	}

	tgAdapter := telegram.NewAdapter(telegram.Config{
		Token:        req.Token,
		AllowedChats: req.AllowedChats,
		AdminIDs:     req.AdminIDs,
	})
	handler := telegram.NewHandler(tgAdapter, s.agent)
	// 与 CLI 路径保持一致：持久化 chatID→sessionID 映射，重启后恢复会话
	handler.SetDataDir(filepath.Join(s.agent.Config().HomeDir(), "data", "telegram"))
	tgAdapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		return handler.HandleMessage(ctx, msg)
	})

	if err := gm.Register(tgAdapter); err != nil {
		s.sendError(w, "failed to register telegram gateway", http.StatusInternalServerError, err.Error())
		return
	}

	if err := gm.Start(r.Context(), "telegram"); err != nil {
		s.sendError(w, "failed to start telegram gateway", http.StatusInternalServerError, err.Error())
		return
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"message": "telegram gateway started",
		"running": true,
	})
}

func (s *Server) handleGatewayByName(w http.ResponseWriter, r *http.Request) {
	// Extract gateway name from path: /api/v1/gateways/{name}/...
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/gateways/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]

	gm := s.agent.MsgGateway()

	if name == "" {
		s.handleGatewaysList(w, r)
		return
	}

	switch {
	case len(parts) == 2 && parts[1] == "stop" && r.Method == http.MethodPost:
		if err := gm.Stop(name); err != nil {
			s.sendError(w, "failed to stop gateway", http.StatusNotFound, err.Error())
			return
		}
		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"message": fmt.Sprintf("gateway %s stopped", name),
			"running": false,
		})

	case len(parts) == 2 && parts[1] == "status" && r.Method == http.MethodGet:
		gw, exists := gm.Get(name)
		if !exists {
			s.sendError(w, "gateway not found", http.StatusNotFound, "")
			return
		}
		stats, _ := gm.Stats(name)
		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"name":    name,
			"running": gw.IsRunning(),
			"stats":   stats,
		})

	default:
		s.sendError(w, "not found", http.StatusNotFound, "")
	}
}

// formatDuration 格式化运行时间
func formatDuration(d time.Duration) string {
	return utils.FormatDurationCompact(d)
}
