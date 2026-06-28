package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yurika0211/luckyagent/internal/soul"
)

// handleSoulTemplates 处理 /api/v1/soul/templates 请求
// GET  - 列出所有模板（支持 ?language=zh 过滤）
// POST - 添加自定义模板
func (s *Server) handleSoulTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listSoulTemplates(w, r)
	case http.MethodPost:
		s.addSoulTemplate(w, r)
	default:
		s.sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleSoulTemplateByID 处理 /api/v1/soul/templates/{id} 请求
// GET    - 获取模板详情
// DELETE - 删除自定义模板
func (s *Server) handleSoulTemplateByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/soul/templates/")
	if id == "" {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "template ID required"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getSoulTemplate(w, r, id)
	case http.MethodDelete:
		s.deleteSoulTemplate(w, r, id)
	default:
		s.sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// listSoulTemplates 列出模板
func (s *Server) listSoulTemplates(w http.ResponseWriter, r *http.Request) {
	tm := s.agent.TemplateManager()

	language := r.URL.Query().Get("language")
	var templates []*soul.Template
	if language != "" {
		templates = tm.ListByLanguage(language)
	} else {
		templates = tm.ListTemplates()
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"templates": templates,
		"count":     len(templates),
	})
}

// addSoulTemplate 添加自定义模板
func (s *Server) addSoulTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		Language    string            `json:"language"`
		Description string            `json:"description"`
		Content     string            `json:"content"`
		Variables   map[string]string  `json:"variables,omitempty"`
		Tags        []string          `json:"tags,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if req.ID == "" {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "template ID is required"})
		return
	}
	if req.Content == "" {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "template content is required"})
		return
	}

	tmpl := &soul.Template{
		ID:          req.ID,
		Name:        req.Name,
		Language:    req.Language,
		Description: req.Description,
		Content:     req.Content,
		Variables:   req.Variables,
		Tags:        req.Tags,
	}

	tm := s.agent.TemplateManager()
	if err := tm.AddTemplate(tmpl); err != nil {
		s.sendJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	s.sendJSON(w, http.StatusCreated, map[string]interface{}{
		"message":  "template added",
		"template": tmpl,
	})
}

// getSoulTemplate 获取模板详情
func (s *Server) getSoulTemplate(w http.ResponseWriter, r *http.Request, id string) {
	tm := s.agent.TemplateManager()
	tmpl, err := tm.GetTemplate(id)
	if err != nil {
		s.sendJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	// 支持渲染模板（通过 ?var_key=val 查询参数）
	vars := make(map[string]string)
	for k, v := range r.URL.Query() {
		if strings.HasPrefix(k, "var_") {
			vars[strings.TrimPrefix(k, "var_")] = v[0]
		}
	}

	result := map[string]interface{}{
		"id":          tmpl.ID,
		"name":        tmpl.Name,
		"language":    tmpl.Language,
		"description": tmpl.Description,
		"content":     tmpl.Content,
		"tags":        tmpl.Tags,
	}

	if len(vars) > 0 {
		result["rendered"] = tmpl.Render(vars)
	}

	s.sendJSON(w, http.StatusOK, result)
}

// deleteSoulTemplate 删除自定义模板
func (s *Server) deleteSoulTemplate(w http.ResponseWriter, r *http.Request, id string) {
	tm := s.agent.TemplateManager()
	if err := tm.RemoveTemplate(id); err != nil {
		if strings.Contains(err.Error(), "builtin") {
			s.sendJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		} else {
			s.sendJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return
	}

	s.sendJSON(w, http.StatusOK, map[string]string{"message": "template deleted"})
}