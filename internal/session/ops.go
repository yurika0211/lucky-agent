package session

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/utils"
)

// MigrateResult describes one session migrate attempt.
type MigrateResult struct {
	ID           string `json:"id"`
	FromFormat   string `json:"from_format"`
	ToFormat     string `json:"to_format"`
	MessageCount int    `json:"message_count"`
	ByteSize     int64  `json:"byte_size"`
	BlobCount    int    `json:"blob_count,omitempty"`
	Skipped      bool   `json:"skipped,omitempty"`
	Error        string `json:"error,omitempty"`
}

// GCResult describes blob garbage collection for one session.
type GCResult struct {
	ID            string `json:"id"`
	Removed       int    `json:"removed"`
	FreedBytes    int64  `json:"freed_bytes"`
	Referenced    int    `json:"referenced"`
	Error         string `json:"error,omitempty"`
}

// RollResult describes an in-place session roll that keeps the same session id.
type RollResult struct {
	Rolled       bool   `json:"rolled"`
	ArchivePath  string `json:"archive_path,omitempty"`
	Dropped      int    `json:"dropped"`
	Retained     int    `json:"retained"`
	MessageCount int    `json:"message_count"`
}

// ExportOptions controls Markdown export content.
type ExportOptions struct {
	IncludeTools bool
	IncludeSystem bool
	MaxToolChars int
}

// ByteSize returns on-disk size for the session storage.
func (s *Session) ByteSize() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	format := s.format
	id := s.ID
	dir := s.dir
	s.mu.RUnlock()
	switch format {
	case FormatSegmentV1:
		return segmentStorageBytes(filepath.Join(dir, id))
	default:
		return fileByteSize(filepath.Join(dir, id+".md"))
	}
}

// MigrateToSegment forces externalization + segment_v1 rewrite.
func (s *Session) MigrateToSegment() (MigrateResult, error) {
	result := MigrateResult{ID: s.ID}
	if s == nil {
		return result, fmt.Errorf("session is nil")
	}
	s.mu.RLock()
	result.FromFormat = s.format
	if result.FromFormat == "" {
		result.FromFormat = FormatLegacyMD
	}
	s.mu.RUnlock()

	if err := s.loadMessages(); err != nil {
		result.Error = err.Error()
		return result, err
	}
	if err := s.ExternalizeLargeMessages(); err != nil {
		result.Error = err.Error()
		return result, err
	}

	s.mu.Lock()
	already := s.format == FormatSegmentV1 && !s.needsFullRewrite
	s.format = FormatSegmentV1
	s.needsFullRewrite = true
	s.persistedCount = 0
	s.mu.Unlock()

	if already {
		// Still rewrite to pick up any newly externalized blobs and drop legacy md.
	}
	if err := s.Save(); err != nil {
		result.Error = err.Error()
		return result, err
	}

	s.mu.RLock()
	result.ToFormat = s.format
	result.MessageCount = s.messageCountLocked()
	s.mu.RUnlock()
	result.ByteSize = s.ByteSize()
	result.BlobCount = countBlobs(s.blobsDir())
	if result.FromFormat == FormatSegmentV1 && result.ToFormat == FormatSegmentV1 {
		// Not a no-op: migrate still externalizes and rewrites.
	}
	return result, nil
}

// NeedsRoll reports whether the session exceeds configured soft limits.
func (s *Session) NeedsRoll(maxMessages int, maxBytes int64) bool {
	if s == nil {
		return false
	}
	if maxMessages <= 0 && maxBytes <= 0 {
		return false
	}
	count := s.MessageCount()
	if maxMessages > 0 && count >= maxMessages {
		return true
	}
	if maxBytes > 0 && s.ByteSize() >= maxBytes {
		return true
	}
	return false
}

// Roll archives older messages and keeps only the latest retainTurns user turns
// in the live session. The session id is unchanged so gateways stay attached.
func (s *Session) Roll(retainTurns int) (RollResult, error) {
	result := RollResult{}
	if s == nil {
		return result, fmt.Errorf("session is nil")
	}
	if retainTurns <= 0 {
		retainTurns = 12
	}
	if err := s.loadMessages(); err != nil {
		return result, err
	}
	if err := s.ExternalizeLargeMessages(); err != nil {
		return result, err
	}

	s.mu.Lock()
	messages := append([]provider.Message(nil), s.Messages...)
	s.mu.Unlock()
	if len(messages) == 0 {
		return result, nil
	}

	cut := retainTurnStart(messages, retainTurns)
	if cut <= 0 {
		return result, nil
	}

	if err := s.ensureSegmentDirs(); err != nil {
		return result, err
	}
	archiveDir := filepath.Join(s.sessionRootDir(), "archive")
	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		return result, fmt.Errorf("create archive dir: %w", err)
	}
	archiveName := fmt.Sprintf("roll-%d.jsonl", time.Now().UnixNano())
	archivePath := filepath.Join(archiveDir, archiveName)
	if err := writeMessagesJSONLFile(archivePath, messages[:cut]); err != nil {
		return result, err
	}

	marker := provider.Message{
		Role: "system",
		Name: "session_roll",
		Content: fmt.Sprintf(
			"[Session rolled] Archived %d earlier messages to %s. Continuing with the latest %d user turns. Full history remains on disk under archive/.",
			cut, archiveName, retainTurns,
		),
	}
	now := time.Now().UTC()
	marker.CreatedAt = &now
	kept := append([]provider.Message{marker}, messages[cut:]...)

	s.mu.Lock()
	s.Messages = kept
	s.messageCount = len(kept)
	s.format = FormatSegmentV1
	s.needsFullRewrite = true
	s.persistedCount = 0
	s.invalidatePageCacheLocked()
	s.UpdatedAt = time.Now()
	s.mu.Unlock()

	if err := s.Save(); err != nil {
		return result, err
	}
	result.Rolled = true
	result.ArchivePath = archivePath
	result.Dropped = cut
	result.Retained = len(kept)
	result.MessageCount = len(kept)
	return result, nil
}

func retainTurnStart(messages []provider.Message, retainTurns int) int {
	if retainTurns <= 0 || len(messages) == 0 {
		return 0
	}
	userIdx := make([]int, 0, 32)
	for i, msg := range messages {
		if msg.Role == "user" {
			userIdx = append(userIdx, i)
		}
	}
	if len(userIdx) <= retainTurns {
		return 0
	}
	return userIdx[len(userIdx)-retainTurns]
}

func writeMessagesJSONLFile(path string, messages []provider.Message) error {
	var b strings.Builder
	for i := range messages {
		line, err := json.Marshal(messages[i])
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return utils.WriteFileAtomic(path, []byte(b.String()), 0600)
}

// GCBlobs deletes unreferenced blob files under the session blobs directory.
func (s *Session) GCBlobs() (GCResult, error) {
	result := GCResult{ID: s.ID}
	if s == nil {
		return result, fmt.Errorf("session is nil")
	}
	if err := s.loadMessages(); err != nil {
		result.Error = err.Error()
		return result, err
	}
	s.mu.RLock()
	messages := append([]provider.Message(nil), s.Messages...)
	s.mu.RUnlock()

	referenced := make(map[string]struct{})
	for _, msg := range messages {
		hash := strings.TrimSpace(msg.BlobHash)
		if hash != "" {
			referenced[hash] = struct{}{}
		}
	}
	// Also scan archive rolls for references.
	archiveDir := filepath.Join(s.sessionRootDir(), "archive")
	if entries, err := os.ReadDir(archiveDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			msgs, err := readAllMessagesJSONL(filepath.Join(archiveDir, entry.Name()))
			if err != nil {
				continue
			}
			for _, msg := range msgs {
				if hash := strings.TrimSpace(msg.BlobHash); hash != "" {
					referenced[hash] = struct{}{}
				}
			}
		}
	}
	result.Referenced = len(referenced)

	blobs := s.blobsDir()
	entries, err := os.ReadDir(blobs)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		result.Error = err.Error()
		return result, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, ok := referenced[name]; ok {
			continue
		}
		path := filepath.Join(blobs, name)
		info, statErr := entry.Info()
		if removeErr := os.Remove(path); removeErr != nil {
			continue
		}
		result.Removed++
		if statErr == nil {
			result.FreedBytes += info.Size()
		}
	}
	return result, nil
}

// ExportMarkdown writes a human-readable conversation export.
func (s *Session) ExportMarkdown(w io.Writer, opts ExportOptions) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	if err := s.loadMessages(); err != nil {
		return err
	}
	if opts.MaxToolChars <= 0 {
		opts.MaxToolChars = 1200
	}
	s.mu.RLock()
	title := s.Title
	id := s.ID
	created := s.CreatedAt
	updated := s.UpdatedAt
	messages := append([]provider.Message(nil), s.Messages...)
	s.mu.RUnlock()

	var b strings.Builder
	b.WriteString("# Session Export\n\n")
	b.WriteString(fmt.Sprintf("- id: `%s`\n", id))
	if title != "" {
		b.WriteString(fmt.Sprintf("- title: %s\n", title))
	}
	b.WriteString(fmt.Sprintf("- created_at: %s\n", created.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- updated_at: %s\n", updated.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- messages: %d\n\n", len(messages)))
	b.WriteString("---\n\n")

	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		switch role {
		case "system":
			if !opts.IncludeSystem && !IsCompactBoundary(msg) {
				continue
			}
			if IsCompactBoundary(msg) {
				meta, ok := ParseCompactMetadata(msg)
				summary := msg.Content
				if ok && strings.TrimSpace(meta.Summary) != "" {
					summary = meta.Summary
				}
				b.WriteString("### Compact Summary\n\n")
				b.WriteString(strings.TrimSpace(summary))
				b.WriteString("\n\n")
				continue
			}
			if strings.TrimSpace(msg.Name) == "session_roll" {
				b.WriteString("### Session Roll\n\n")
				b.WriteString(strings.TrimSpace(msg.Content))
				b.WriteString("\n\n")
				continue
			}
			if !opts.IncludeSystem {
				continue
			}
			b.WriteString("### System\n\n")
			b.WriteString(strings.TrimSpace(msg.Content))
			b.WriteString("\n\n")
		case "user":
			b.WriteString("### User\n\n")
			b.WriteString(strings.TrimSpace(msg.Content))
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("### Assistant\n\n")
			content := strings.TrimSpace(msg.Content)
			if content != "" {
				b.WriteString(content)
				b.WriteString("\n\n")
			}
			if opts.IncludeTools && len(msg.ToolCalls) > 0 {
				b.WriteString("Tool calls:\n")
				for _, call := range msg.ToolCalls {
					b.WriteString(fmt.Sprintf("- `%s`\n", call.Name))
				}
				b.WriteString("\n")
			}
		case "tool":
			if !opts.IncludeTools {
				continue
			}
			name := strings.TrimSpace(msg.Name)
			if name == "" {
				name = "tool"
			}
			b.WriteString(fmt.Sprintf("### Tool `%s`\n\n", name))
			content := strings.TrimSpace(msg.Content)
			if msg.BlobHash != "" {
				b.WriteString(fmt.Sprintf("_externalized blob `%s` (%d bytes)_\n\n", msg.BlobHash, msg.BlobBytes))
			}
			if utf8.RuneCountInString(content) > opts.MaxToolChars {
				content = truncateRunes(content, opts.MaxToolChars) + "\n\n...[truncated for export]..."
			}
			b.WriteString(content)
			b.WriteString("\n\n")
		default:
			continue
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// MigrateAll migrates every managed session to segment_v1.
func (m *Manager) MigrateAll() []MigrateResult {
	if m == nil {
		return nil
	}
	sessions := m.List()
	results := make([]MigrateResult, 0, len(sessions))
	for _, sess := range sessions {
		res, err := sess.MigrateToSegment()
		if err != nil && res.Error == "" {
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results
}

// GCAllBlobs runs blob GC on every session.
func (m *Manager) GCAllBlobs() []GCResult {
	if m == nil {
		return nil
	}
	sessions := m.List()
	results := make([]GCResult, 0, len(sessions))
	for _, sess := range sessions {
		res, err := sess.GCBlobs()
		if err != nil && res.Error == "" {
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results
}

func countBlobs(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			n++
		}
	}
	return n
}
