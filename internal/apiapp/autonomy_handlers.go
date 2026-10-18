package server

import (
	"github.com/yurika0211/luckyagent-api/resources"
	"net/http"

	"github.com/yurika0211/luckyagent-orchestrator/autonomy"
)

func (s *Server) handleAutonomyDashboard(w http.ResponseWriter, r *http.Request) {
	s.resourceController().HandleAutonomyDashboard(w, r)
}
func (s *Server) handleAutonomyTaskByID(w http.ResponseWriter, r *http.Request) {
	s.resourceController().HandleAutonomyTaskByID(w, r)
}
func (s *Server) autonomyKit() *autonomy.AutonomyKit { return s.resourceController().AutonomyKit() }

type autonomyDashboardResponse = resources.AutonomyDashboardResponse
type autonomyTaskDetailResponse = resources.AutonomyTaskDetailResponse
