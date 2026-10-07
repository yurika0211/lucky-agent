package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/utils"
)

const (
	// DefaultBlobThreshold is the content size that triggers external blob storage.
	DefaultBlobThreshold = 48 * 1024
	// DefaultBlobPreviewChars is how many runes stay inline as Content preview.
	DefaultBlobPreviewChars = 4096
)

// blobThreshold returns the inline content limit before externalizing.
func blobThreshold() int {
	return DefaultBlobThreshold
}

func blobPreviewChars() int {
	return DefaultBlobPreviewChars
}

func (s *Session) sessionRootDir() string {
	if s == nil {
		return ""
	}
	return filepath.Join(s.dir, s.ID)
}

func (s *Session) blobsDir() string {
	return filepath.Join(s.sessionRootDir(), "blobs")
}

func (s *Session) blobFilePath(hash string) string {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return ""
	}
	return filepath.Join(s.blobsDir(), hash)
}

// externalizeLargeContent rewrites msg.Content to a short preview and stores
// the full body under sessions/<id>/blobs/<sha256>. Small messages are unchanged.
func (s *Session) externalizeLargeContent(msg *provider.Message) error {
	if s == nil || msg == nil {
		return nil
	}
	if strings.TrimSpace(msg.BlobHash) != "" {
		return nil
	}
	content := msg.Content
	if len(content) < blobThreshold() {
		return nil
	}
	// Compact boundary markers must stay fully inline for parseability.
	if IsCompactBoundary(*msg) {
		return nil
	}

	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(s.blobsDir(), 0700); err != nil {
		return fmt.Errorf("create blobs dir: %w", err)
	}
	path := s.blobFilePath(hash)
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat blob: %w", err)
		}
		if err := utils.WriteFileAtomic(path, []byte(content), 0600); err != nil {
			return fmt.Errorf("write blob: %w", err)
		}
	}

	preview := truncateRunes(content, blobPreviewChars())
	name := strings.TrimSpace(msg.Name)
	if name == "" {
		name = msg.Role
	}
	marker := fmt.Sprintf(
		"\n\n[content externalized: blob_hash=%s bytes=%d name=%s; full body under session blobs/]",
		hash, len(content), name,
	)
	msg.Content = preview + marker
	msg.BlobHash = hash
	msg.BlobBytes = len(content)
	return nil
}

// externalizeMessages applies blob externalization to every large message.
func (s *Session) externalizeMessages(messages []provider.Message) error {
	for i := range messages {
		if err := s.externalizeLargeContent(&messages[i]); err != nil {
			return err
		}
	}
	return nil
}

// ExternalizeLargeMessages rewrites in-memory oversized contents to blob
// previews. Safe to call before compact or save on legacy giant sessions.
func (s *Session) ExternalizeLargeMessages() error {
	if s == nil {
		return nil
	}
	if err := s.loadMessages(); err != nil {
		return err
	}
	s.mu.Lock()
	messages := s.Messages
	s.mu.Unlock()
	if err := s.externalizeMessages(messages); err != nil {
		return err
	}
	s.mu.Lock()
	s.Messages = messages
	s.needsFullRewrite = true
	s.invalidatePageCacheLocked()
	s.mu.Unlock()
	return nil
}

// ReadBlob returns the full body for a blob hash, or an error if missing.
func (s *Session) ReadBlob(hash string) ([]byte, error) {
	path := s.blobFilePath(hash)
	if path == "" {
		return nil, fmt.Errorf("empty blob hash")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ResolveMessageContent returns full content when a blob exists, else Content.
func (s *Session) ResolveMessageContent(msg provider.Message) (string, error) {
	hash := strings.TrimSpace(msg.BlobHash)
	if hash == "" {
		return msg.Content, nil
	}
	data, err := s.ReadBlob(hash)
	if err != nil {
		return msg.Content, err
	}
	return string(data), nil
}

func truncateRunes(s string, maxChars int) string {
	if maxChars <= 0 || s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxChars {
		return s
	}
	var b strings.Builder
	b.Grow(maxChars * 4)
	n := 0
	for _, r := range s {
		if n >= maxChars {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
