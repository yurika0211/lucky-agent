package server

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/provider"
)

type artifactRoot struct {
	prefix string
	path   string
}

type sessionHistoryMessage struct {
	provider.Message
	Attachments []gateway.Attachment `json:"attachments,omitempty"`
}

type historyToolAttachmentPayload struct {
	Paths      []string `json:"paths"`
	Path       string   `json:"path"`
	OutputPath string   `json:"output_path"`
	FilePath   string   `json:"file_path"`
}

func (s *Server) historyMessages(messages []provider.Message) []sessionHistoryMessage {
	result := make([]sessionHistoryMessage, 0, len(messages))
	for _, message := range messages {
		item := sessionHistoryMessage{Message: message}
		fallbackType := gateway.AttachmentDocument
		switch strings.ToLower(strings.TrimSpace(message.Name)) {
		case "image_generate":
			fallbackType = gateway.AttachmentImage
		case "text_to_speech":
			fallbackType = gateway.AttachmentAudio
		}

		var payload historyToolAttachmentPayload
		if json.Unmarshal([]byte(message.Content), &payload) == nil {
			paths := append([]string{}, payload.Paths...)
			paths = append(paths, payload.Path, payload.OutputPath, payload.FilePath)
			for _, path := range paths {
				if attachment, ok := s.attachmentForArtifact(path, fallbackType); ok {
					item.Attachments = appendUniqueHistoryAttachments(item.Attachments, attachment)
				}
			}
		}
		response, responseAttachments := s.attachmentsFromResponse(message.Content)
		item.Content = response
		for _, attachment := range responseAttachments {
			item.Attachments = appendUniqueHistoryAttachments(item.Attachments, attachment)
		}
		for _, part := range message.ContentParts {
			if part.Image == nil {
				continue
			}
			if attachment, ok := s.attachmentForArtifact(part.Image.FilePath, gateway.AttachmentImage); ok {
				item.Attachments = appendUniqueHistoryAttachments(item.Attachments, attachment)
			}
		}
		result = append(result, item)
	}
	return result
}

func (s *Server) attachmentsFromResponse(raw string) (string, []gateway.Attachment) {
	references := append(agent.MediaReferences(raw), agent.ArtifactReferences(raw)...)
	if len(references) == 0 {
		return raw, nil
	}
	resolved := make([]string, 0, len(references))
	attachments := make([]gateway.Attachment, 0, len(references))
	for _, reference := range references {
		if strings.HasPrefix(strings.ToLower(reference), "http://") || strings.HasPrefix(strings.ToLower(reference), "https://") {
			parsed, err := url.Parse(reference)
			if err != nil || parsed.Host == "" {
				continue
			}
			name := filepath.Base(parsed.Path)
			if name == "." || name == "/" || name == "" {
				name = "attachment"
			}
			mimeType := mime.TypeByExtension(filepath.Ext(name))
			attachments = appendUniqueHistoryAttachments(attachments, gateway.Attachment{
				Type:     attachmentTypeForMIME(mimeType, gateway.AttachmentDocument),
				FileURL:  parsed.String(),
				FileName: name,
				MimeType: mimeType,
			})
			resolved = append(resolved, reference)
			continue
		}
		if attachment, ok := s.attachmentForArtifact(reference, gateway.AttachmentDocument); ok {
			attachments = appendUniqueHistoryAttachments(attachments, attachment)
			resolved = append(resolved, reference)
		}
	}
	return agent.StripMediaReferences(raw, resolved), attachments
}

func attachmentTypeForMIME(mimeType string, fallback gateway.AttachmentType) gateway.AttachmentType {
	switch {
	case strings.HasPrefix(strings.ToLower(mimeType), "image/"):
		return gateway.AttachmentImage
	case strings.HasPrefix(strings.ToLower(mimeType), "audio/"):
		return gateway.AttachmentAudio
	case strings.HasPrefix(strings.ToLower(mimeType), "video/"):
		return gateway.AttachmentVideo
	default:
		return fallback
	}
}

func appendUniqueHistoryAttachments(attachments []gateway.Attachment, candidate gateway.Attachment) []gateway.Attachment {
	for _, existing := range attachments {
		if existing.FileURL == candidate.FileURL {
			return attachments
		}
	}
	return append(attachments, candidate)
}

func (s *Server) artifactRoots() []artifactRoot {
	home := ""
	if s.agent != nil && s.agent.Config() != nil {
		home = strings.TrimSpace(s.agent.Config().HomeDir())
	}
	if home == "" {
		return nil
	}
	return []artifactRoot{
		{prefix: "workspace", path: filepath.Join(home, "workspace")},
		{prefix: "uploads", path: filepath.Join(home, "uploads")},
	}
}

func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	relative := strings.TrimSpace(r.URL.Query().Get("path"))
	if relative == "" {
		s.sendError(w, "artifact path is required", http.StatusBadRequest, "")
		return
	}
	_, target, err := s.resolveArtifactPath(relative)
	if err != nil {
		s.sendError(w, "invalid artifact path", http.StatusForbidden, err.Error())
		return
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		s.sendError(w, "artifact not found", http.StatusNotFound, "")
		return
	}

	w.Header().Set("Cache-Control", "private, max-age=3600")
	if contentType := mime.TypeByExtension(filepath.Ext(target)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(target)))
	http.ServeFile(w, r, target)
}

func (s *Server) resolveArtifactPath(relative string) (artifactRoot, string, error) {
	relative = filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	for _, root := range s.artifactRoots() {
		prefix := root.prefix + "/"
		if relative == root.prefix || !strings.HasPrefix(relative, prefix) {
			continue
		}
		child := strings.TrimPrefix(relative, prefix)
		if child == "" || filepath.IsAbs(child) || child == "." || strings.HasPrefix(child, "../") || strings.Contains(child, "/../") {
			return artifactRoot{}, "", fmt.Errorf("path escapes artifact root")
		}
		rootPath, err := filepath.Abs(root.path)
		if err != nil {
			return artifactRoot{}, "", err
		}
		target, err := filepath.Abs(filepath.Join(rootPath, filepath.FromSlash(child)))
		if err != nil {
			return artifactRoot{}, "", err
		}
		if target != rootPath && !strings.HasPrefix(target, rootPath+string(filepath.Separator)) {
			return artifactRoot{}, "", fmt.Errorf("path escapes artifact root")
		}
		return root, target, nil
	}
	return artifactRoot{}, "", fmt.Errorf("unsupported artifact root")
}

func (s *Server) artifactURLForPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	for _, root := range s.artifactRoots() {
		rootPath, err := filepath.Abs(root.path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootPath, abs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return "/api/v1/artifacts?path=" + url.QueryEscape(root.prefix+"/"+filepath.ToSlash(rel))
	}
	return ""
}

func (s *Server) attachmentForArtifact(path string, fallbackType gateway.AttachmentType) (gateway.Attachment, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return gateway.Attachment{}, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return gateway.Attachment{}, false
	}
	fileURL := s.artifactURLForPath(path)
	if fileURL == "" {
		return gateway.Attachment{}, false
	}
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	typeValue := fallbackType
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		typeValue = gateway.AttachmentImage
	case strings.HasPrefix(mimeType, "audio/"):
		typeValue = gateway.AttachmentAudio
	case strings.HasPrefix(mimeType, "video/"):
		typeValue = gateway.AttachmentVideo
	}
	return gateway.Attachment{
		Type:     typeValue,
		FileURL:  fileURL,
		FilePath: path,
		FileName: filepath.Base(path),
		MimeType: mimeType,
		FileSize: info.Size(),
	}, true
}
