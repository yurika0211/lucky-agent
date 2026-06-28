package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yurika0211/luckyagent/internal/embedder"
)

// --- v0.21.0: Embedder API handlers ---

func (s *Server) handleEmbedderList(w http.ResponseWriter, r *http.Request) {
	reg := s.agent.EmbedderRegistry()
	if reg == nil {
		s.sendJSON(w, http.StatusOK, []embedder.EmbedderInfo{})
		return
	}
	s.sendJSON(w, http.StatusOK, reg.List())
}

func (s *Server) handleEmbedderGet(w http.ResponseWriter, r *http.Request, id string) {
	reg := s.agent.EmbedderRegistry()
	if reg == nil {
		s.sendError(w, "embedder registry not available", http.StatusNotFound, "")
		return
	}

	e, ok := reg.Get(id)
	if !ok {
		s.sendError(w, "embedder not found: "+id, http.StatusNotFound, "")
		return
	}

	info := embedder.EmbedderInfo{
		ID:        id,
		Name:      e.Name(),
		Model:     e.Model(),
		Dimension: e.Dimension(),
		Active:    reg.ActiveID() == id,
	}
	s.sendJSON(w, http.StatusOK, info)
}

type embedderSwitchRequest struct {
	ID string `json:"id"`
}

func (s *Server) handleEmbedderSwitch(w http.ResponseWriter, r *http.Request) {
	var req embedderSwitchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
		return
	}

	reg := s.agent.EmbedderRegistry()
	if reg == nil {
		s.sendError(w, "embedder registry not available", http.StatusNotFound, "")
		return
	}

	if !reg.Switch(req.ID) {
		s.sendError(w, "embedder not found: "+req.ID, http.StatusNotFound, "")
		return
	}

	e := reg.Active()
	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"active":    req.ID,
		"name":      e.Name(),
		"model":     e.Model(),
		"dimension": e.Dimension(),
	})
}

type embedderRegisterRequest struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`  // "mock", "openai", "ollama"
	Model     string `json:"model"`
	Dimension int    `json:"dimension,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
}

func (s *Server) handleEmbedderRegister(w http.ResponseWriter, r *http.Request) {
	var req embedderRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
		return
	}

	reg := s.agent.EmbedderRegistry()
	if reg == nil {
		s.sendError(w, "embedder registry not available", http.StatusInternalServerError, "")
		return
	}

	var e embedder.Embedder
	switch req.Provider {
	case "mock":
		e = embedder.NewMockEmbedderWithModel(req.Dimension, req.Model)
	case "openai":
		e = embedder.NewOpenAIEmbedder(embedder.OpenAIEmbedderConfig{
			APIKey:    req.APIKey,
			Model:     req.Model,
			BaseURL:   req.BaseURL,
			Dimension: req.Dimension,
		})
	case "ollama":
		e = embedder.NewOllamaEmbedder(embedder.OllamaEmbedderConfig{
			BaseURL:   req.BaseURL,
			Model:     req.Model,
			Dimension: req.Dimension,
		})
	default:
		s.sendError(w, "unknown provider: "+req.Provider, http.StatusBadRequest, "")
		return
	}

	if !reg.Register(req.ID, e) {
		s.sendError(w, "embedder already registered: "+req.ID, http.StatusConflict, "")
		return
	}

	s.sendJSON(w, http.StatusCreated, map[string]interface{}{
		"id":        req.ID,
		"name":      e.Name(),
		"model":     e.Model(),
		"dimension": e.Dimension(),
	})
}

type embedderTestRequest struct {
	Text string `json:"text"`
}

func (s *Server) handleEmbedderTest(w http.ResponseWriter, r *http.Request, id string) {
	var req embedderTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
		return
	}

	if req.Text == "" {
		req.Text = "Hello, world!"
	}

	reg := s.agent.EmbedderRegistry()
	if reg == nil {
		s.sendError(w, "embedder registry not available", http.StatusNotFound, "")
		return
	}

	e, ok := reg.Get(id)
	if !ok {
		s.sendError(w, "embedder not found: "+id, http.StatusNotFound, "")
		return
	}

	vec, err := e.Embed(r.Context(), req.Text)
	if err != nil {
		s.sendError(w, "embedding failed", http.StatusInternalServerError, err.Error())
		return
	}

	sampleLen := 5
	if len(vec) < sampleLen {
		sampleLen = len(vec)
	}
	sample := make([]float64, sampleLen)
	copy(sample, vec[:sampleLen])

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"id":        id,
		"text":      req.Text,
		"dimension": len(vec),
		"sample":    sample,
	})
}

// handleEmbedderRoutes dispatches /api/v1/embedders/{id} and /api/v1/embedders/{id}/test
func (s *Server) handleEmbedderRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	suffix := strings.TrimPrefix(path, "/api/v1/embedders/")
	if suffix == "" {
		s.handleEmbedderList(w, r)
		return
	}

	parts := strings.SplitN(suffix, "/", 2)
	id := parts[0]

	if len(parts) == 2 && parts[1] == "test" {
		s.handleEmbedderTest(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleEmbedderGet(w, r, id)
	default:
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
	}
}
