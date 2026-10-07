package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
)

type modelSwitchRequest struct {
	Kind     string `json:"kind"`
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
}

type modelProfileRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.agent == nil || s.agent.Config() == nil {
		s.sendError(w, "agent runtime unavailable", http.StatusServiceUnavailable, "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.sendJSON(w, http.StatusOK, config.RedactSecrets(s.agent.Config().Get()))
	case http.MethodPut:
		defer r.Body.Close()
		var submitted config.Config
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
		if err := decoder.Decode(&submitted); err != nil {
			s.sendError(w, "invalid configuration payload", http.StatusBadRequest, "")
			return
		}
		current := s.agent.Config().Get()
		config.PreserveRedactedSecrets(current, &submitted)
		if err := s.agent.ValidateRuntimeConfig(&submitted); err != nil {
			s.sendError(w, "configuration validation failed", http.StatusBadRequest, "review provider, model, and protocol settings")
			return
		}
		if err := s.agent.Config().Replace(&submitted); err != nil {
			s.sendError(w, "configuration save failed", http.StatusInternalServerError, "")
			return
		}
		if err := s.agent.ApplyRuntimeConfig(s.agent.Config().Get()); err != nil {
			_ = s.agent.Config().Replace(current)
			_ = s.agent.ApplyRuntimeConfig(current)
			s.sendError(w, "configuration could not be applied; previous configuration restored", http.StatusBadRequest, "")
			return
		}
		s.sendJSON(w, http.StatusOK, config.RedactSecrets(s.agent.Config().Get()))
	default:
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
	}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	if s.agent == nil {
		s.sendError(w, "agent runtime unavailable", http.StatusServiceUnavailable, "")
		return
	}
	var kind *config.ModelKind
	if raw := strings.TrimSpace(r.URL.Query().Get("kind")); raw != "" && !strings.EqualFold(raw, "all") {
		parsed, err := config.ParseModelKind(raw)
		if err != nil {
			s.sendError(w, err.Error(), http.StatusBadRequest, "")
			return
		}
		kind = &parsed
	}
	providerName := strings.TrimSpace(r.URL.Query().Get("provider"))
	refresh := modelRefreshRequested(r)
	var models []agent.ModelRef
	var discoveryError string
	var source string
	if refresh {
		refreshed, err := s.agent.RefreshChatModels(r.Context())
		if err != nil {
			discoveryError = err.Error()
			models = s.agent.ListModels(kind)
			source = "catalog"
		} else {
			models = mergeRefreshedChatModels(s.agent.ListModels(kind), append(refreshed, s.agent.CompactModelsFromChatDiscovery(refreshed)...), kind)
			source = "provider"
		}
	} else {
		models = s.agent.ListModels(kind)
		source = "catalog"
	}
	if providerName != "" {
		filtered := models[:0]
		for _, model := range models {
			if strings.EqualFold(model.Provider, providerName) {
				filtered = append(filtered, model)
			}
		}
		models = filtered
	}
	payload := map[string]any{"models": models, "count": len(models), "source": source}
	if discoveryError != "" {
		payload["error"] = discoveryError
	}
	s.sendJSON(w, http.StatusOK, payload)
}

func mergeRefreshedChatModels(catalog []agent.ModelRef, discovered []agent.ModelRef, kind *config.ModelKind) []agent.ModelRef {
	if kind != nil && *kind != config.ModelKindChat {
		return catalog
	}
	kept := make([]agent.ModelRef, 0, len(catalog)+len(discovered))
	for _, model := range catalog {
		if model.Kind == config.ModelKindChat || model.Kind == config.ModelKindCompact {
			continue
		}
		kept = append(kept, model)
	}
	kept = append(kept, discovered...)
	return kept
}

func modelRefreshRequested(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("refresh"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func (s *Server) handleModelSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	if s.agent == nil {
		s.sendError(w, "agent runtime unavailable", http.StatusServiceUnavailable, "")
		return
	}
	defer r.Body.Close()
	var request modelSwitchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
		s.sendError(w, "invalid model switch payload", http.StatusBadRequest, "")
		return
	}
	kind, err := config.ParseModelKind(request.Kind)
	if err != nil {
		s.sendError(w, err.Error(), http.StatusBadRequest, "")
		return
	}
	if err := s.agent.SwitchModelKind(kind, request.Model, agent.SwitchModelOptions{Persist: true, Provider: request.Provider}); err != nil {
		s.sendError(w, "model switch failed", http.StatusBadRequest, err.Error())
		return
	}
	current, _ := s.agent.CurrentModel(kind)
	s.sendJSON(w, http.StatusOK, current)
}

func (s *Server) handleModelProfiles(w http.ResponseWriter, r *http.Request) {
	if s.agent == nil || s.agent.Config() == nil {
		s.sendError(w, "agent runtime unavailable", http.StatusServiceUnavailable, "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		profiles := s.agent.Config().Get().Models.Profiles
		s.sendJSON(w, http.StatusOK, map[string]any{"profiles": profiles, "count": len(profiles)})
	case http.MethodPost:
		defer r.Body.Close()
		var request modelProfileRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
			s.sendError(w, "invalid model profile payload", http.StatusBadRequest, "")
			return
		}
		if err := s.agent.ApplyModelProfile(request.Name); err != nil {
			s.sendError(w, "model profile switch failed", http.StatusBadRequest, fmt.Sprint(err))
			return
		}
		s.sendJSON(w, http.StatusOK, map[string]any{"active": s.agent.Config().Get().Models.Active, "profile": strings.TrimSpace(request.Name)})
	default:
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
	}
}
