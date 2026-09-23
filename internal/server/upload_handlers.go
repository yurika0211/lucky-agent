package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

const (
	// maxUploadBytes bounds a single upload request.
	maxUploadBytes = 32 << 20
	// maxUploadMemory is what ParseMultipartForm keeps in RAM; the rest spills
	// to the OS temp dir.
	maxUploadMemory = 8 << 20
	uploadDirName   = "uploads"
)

// handleUploads stores files posted by a UI and returns attachment descriptors
// that the chat endpoints already understand.
//
// Uploads need their own endpoint because the chat WebSocket caps frames at
// 64 KiB, so file bytes cannot ride along with the message. The multimodal
// pipeline also reads documents (pdf/docx/pptx) from disk via FilePath, which
// means the bytes have to land in a real file either way.
func (s *Server) handleUploads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		s.sendError(w, "invalid upload", http.StatusBadRequest, err.Error())
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		files = r.MultipartForm.File["files"]
	}
	if len(files) == 0 {
		s.sendError(w, "no files in request", http.StatusBadRequest, `expected a multipart field named "file"`)
		return
	}

	dir, err := s.uploadDir()
	if err != nil {
		s.sendError(w, "prepare upload directory failed", http.StatusInternalServerError, err.Error())
		return
	}

	attachments := make([]gateway.Attachment, 0, len(files))
	for _, header := range files {
		att, err := storeUpload(dir, header)
		if err != nil {
			s.sendError(w, "store upload failed", http.StatusInternalServerError, err.Error())
			return
		}
		att.FileURL = s.artifactURLForPath(att.FilePath)
		attachments = append(attachments, att)
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"attachments": attachments,
		"count":       len(attachments),
	})
}

// uploadDir returns today's upload directory inside the runtime home, creating
// it if needed. Uploads are grouped by day so they are easy to prune.
func (s *Server) uploadDir() (string, error) {
	home := ""
	if s.agent != nil {
		if mgr := s.agent.Config(); mgr != nil {
			home = strings.TrimSpace(mgr.HomeDir())
		}
	}
	if home == "" {
		return "", fmt.Errorf("runtime home directory is not configured")
	}
	dir := filepath.Join(home, uploadDirName, time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func storeUpload(dir string, header *multipart.FileHeader) (gateway.Attachment, error) {
	original := sanitizeUploadName(header.Filename)

	src, err := header.Open()
	if err != nil {
		return gateway.Attachment{}, err
	}
	defer func() { _ = src.Close() }()

	// The stored name is generated, never taken from the client, so a crafted
	// filename cannot escape the upload directory or overwrite anything.
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return gateway.Attachment{}, err
	}
	path := filepath.Join(dir, hex.EncodeToString(token)+filepath.Ext(original))

	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return gateway.Attachment{}, err
	}
	written, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return gateway.Attachment{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return gateway.Attachment{}, closeErr
	}

	mimeType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if mimeType == "" || mimeType == "application/octet-stream" {
		if guessed := mime.TypeByExtension(filepath.Ext(original)); guessed != "" {
			mimeType = guessed
		}
	}

	return gateway.Attachment{
		Type:     attachmentTypeFor(mimeType, original),
		FileName: original,
		FilePath: path,
		MimeType: mimeType,
		FileSize: written,
	}, nil
}

// sanitizeUploadName keeps a readable label for the model and the UI while
// discarding any directory component the client may have sent.
func sanitizeUploadName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if name == "" || name == "/" {
		return "upload"
	}
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	return name
}

// attachmentTypeFor maps a MIME type onto the attachment kinds the multimodal
// pipeline dispatches on. Anything that is not audio/video/image is treated as
// a document, which is the branch that extracts text from pdf/docx/pptx.
func attachmentTypeFor(mimeType, filename string) gateway.AttachmentType {
	lower := strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(lower, "image/"):
		return gateway.AttachmentImage
	case strings.HasPrefix(lower, "audio/"):
		return gateway.AttachmentAudio
	case strings.HasPrefix(lower, "video/"):
		return gateway.AttachmentVideo
	}

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".heic", ".avif":
		return gateway.AttachmentImage
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac":
		return gateway.AttachmentAudio
	case ".mp4", ".mov", ".mkv", ".webm", ".avi":
		return gateway.AttachmentVideo
	}

	return gateway.AttachmentDocument
}
