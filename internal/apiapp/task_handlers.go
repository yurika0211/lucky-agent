package server

import (
	"github.com/yurika0211/luckyagent-api/resources"

	"net/http"

	taskstore "github.com/yurika0211/luckyagent-orchestrator/task"
)

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	s.resourceController().HandleTasks(w, r)
}
func (s *Server) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	s.resourceController().HandleTaskByID(w, r)
}
func (s *Server) handleTaskTree(w http.ResponseWriter, store taskstore.Store, taskID string) {
	s.resourceController().HandleTaskTree(w, store, taskID)
}
func (s *Server) handleTaskFeedback(w http.ResponseWriter, r *http.Request, store taskstore.Store, taskID string) {
	s.resourceController().HandleTaskFeedback(w, r, store, taskID)
}
func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request, store taskstore.Store, taskID string) {
	s.resourceController().HandleTaskCancel(w, r, store, taskID)
}

type taskTreeNode = resources.TaskTreeNode
