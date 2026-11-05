package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yurika0211/luckyagent-runtime/utils"
)

// ErrSessionDeleted means a handle no longer owns a live, writable session.
// Reusing the same ID through Manager.Ensure creates a new live handle.
var ErrSessionDeleted = errors.New("session was deleted")

// checkLiveLocked requires saveMu. It is shared by persistence and structural
// operations so deletion cannot race a later write from an existing handle.
func (s *Session) checkLiveLocked() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.deleted {
		return fmt.Errorf("%w: %s", ErrSessionDeleted, s.ID)
	}
	return nil
}

// refreshStorageBytesLocked requires saveMu and publishes a single cached size.
// Call after operations that create/remove transcript or blob files; list
// requests only read the cache and never traverse the storage tree.
func (s *Session) refreshStorageBytesLocked() {
	s.mu.RLock()
	format, dir, id := s.format, s.dir, s.ID
	s.mu.RUnlock()
	var size int64
	if format == FormatSegmentV1 {
		size = segmentStorageBytes(filepath.Join(dir, id))
	} else {
		size = fileByteSize(filepath.Join(dir, id+".md"))
	}
	s.mu.Lock()
	s.storageBytes = size
	s.storageBytesValid = true
	s.mu.Unlock()
}

func (s *Session) cachedStorageBytes() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.storageBytes
}

// saveMetadataLocked requires saveMu. Count describes the durable transcript,
// not messages that have only been appended in memory since the last Save.
func (s *Session) saveMetadataLocked() error {
	if err := s.checkLiveLocked(); err != nil {
		return err
	}
	s.mu.RLock()
	knownSize := s.storageBytesValid
	s.mu.RUnlock()
	if !knownSize {
		s.refreshStorageBytesLocked()
	}
	s.mu.RLock()
	format := s.format
	if format == "" {
		format = FormatLegacyMD
	}
	meta := sessionMetadata{
		ID: s.ID, Title: s.Title, AgentRole: s.AgentRole,
		Recap: s.recap, RecapUpdatedAt: s.recapUpdatedAt,
		AgentSystemPrompt: s.AgentSystemPrompt, Pinned: s.Pinned, Project: s.Project,
		MessageCount: s.persistedCount, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		ByteSize: s.storageBytes, Format: format,
		ShellContext: ShellContext{Cwd: s.ShellContext.Cwd, Env: cloneEnv(s.ShellContext.Env)},
	}
	dir := s.dir
	s.mu.RUnlock()
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal session metadata: %w", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	path := sessionMetadataPath(dir, meta.ID)
	if err := utils.WriteFileAtomic(path, data, 0600); err != nil {
		return fmt.Errorf("write session metadata: %w", err)
	}
	s.publishIndexLocked(meta, path)
	return nil
}

// publishIndexLocked requires saveMu. The sidecar remains the recovery source;
// a failed index update leaves the next startup to rebuild the row.
func (s *Session) publishIndexLocked(meta sessionMetadata, source string) {
	if s.manager == nil || s.manager.index == nil {
		return
	}
	size, mod, ok := sourceFingerprint(source)
	if !ok {
		return
	}
	s.mu.RLock()
	tailOffset, tailStart, fileSize := s.tailOffset, s.tailStart, s.tailSize
	s.mu.RUnlock()
	_ = s.manager.index.Upsert(indexedSession{
		Meta:       meta,
		SourceSize: size,
		SourceTime: mod,
		TailOffset: tailOffset,
		TailStart:  tailStart,
		FileSize:   fileSize,
	})
}
