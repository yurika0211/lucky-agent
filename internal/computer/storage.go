package computer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FrameStore persists screenshots with private permissions and bounded lifetime.
type FrameStore struct {
	root     string
	keep     int
	ttl      time.Duration
	maxBytes int
}

func NewFrameStore(root string, keep int, ttl time.Duration) (*FrameStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("computer: frame storage directory is required")
	}
	if keep < 1 {
		keep = 2
	}
	if ttl < 0 {
		ttl = 0
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("computer: create frame storage: %w", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		return nil, fmt.Errorf("computer: secure frame storage: %w", err)
	}
	return &FrameStore{root: root, keep: keep, ttl: ttl}, nil
}

func (s *FrameStore) SessionDir(sessionID string) string {
	return filepath.Join(s.root, safeSessionID(sessionID))
}

func (s *FrameStore) Root() string { return s.root }

func safeSessionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "anonymous"
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	clean := b.String()
	if clean == "." || clean == ".." || clean == "" {
		return "_"
	}
	return clean
}

func (s *FrameStore) Save(sessionID string, sequence uint64, obs Observation) (Observation, error) {
	dir := s.SessionDir(sessionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return obs, fmt.Errorf("computer: create session frame directory: %w", err)
	}
	_ = os.Chmod(dir, 0700)
	name := fmt.Sprintf("frame-%06d.png", sequence)
	dst := filepath.Join(dir, name)
	var src io.Reader
	var closeSrc io.Closer
	if len(obs.ImageData) > 0 {
		if s.maxBytes > 0 && len(obs.ImageData) > s.maxBytes {
			return obs, fmt.Errorf("computer: screenshot exceeds configured maximum %d bytes", s.maxBytes)
		}
		src = bytes.NewReader(obs.ImageData)
	} else if obs.FilePath != "" {
		f, err := os.Open(obs.FilePath)
		if err != nil {
			return obs, fmt.Errorf("computer: open backend frame: %w", err)
		}
		if s.maxBytes > 0 {
			if info, statErr := f.Stat(); statErr == nil && info.Size() > int64(s.maxBytes) {
				_ = f.Close()
				return obs, fmt.Errorf("computer: screenshot exceeds configured maximum %d bytes", s.maxBytes)
			}
		}
		src, closeSrc = f, f
	} else {
		return obs, errors.New("computer: backend returned no screenshot data")
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		if closeSrc != nil {
			_ = closeSrc.Close()
		}
		return obs, fmt.Errorf("computer: create frame: %w", err)
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), src)
	closeErr := f.Close()
	if closeSrc != nil {
		_ = closeSrc.Close()
	}
	if copyErr != nil {
		return obs, fmt.Errorf("computer: save frame: %w", copyErr)
	}
	if closeErr != nil {
		return obs, fmt.Errorf("computer: close frame: %w", closeErr)
	}
	obs.FilePath = dst
	obs.ImageData = nil
	obs.SHA256 = hex.EncodeToString(h.Sum(nil))
	if obs.MimeType == "" {
		obs.MimeType = "image/png"
	}
	if err := s.prune(sessionID); err != nil {
		return obs, err
	}
	return obs, nil
}

func (s *FrameStore) prune(sessionID string) error {
	entries, err := os.ReadDir(s.SessionDir(sessionID))
	if err != nil {
		return err
	}
	type fileInfo struct {
		path string
		mod  time.Time
	}
	files := make([]fileInfo, 0, len(entries))
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil {
			continue
		}
		path := filepath.Join(s.SessionDir(sessionID), e.Name())
		if s.ttl > 0 && now.Sub(info.ModTime()) >= s.ttl {
			_ = os.Remove(path)
			continue
		}
		files = append(files, fileInfo{path: path, mod: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for i := s.keep; i < len(files); i++ {
		_ = os.Remove(files[i].path)
	}
	return nil
}

func (s *FrameStore) RemoveSession(sessionID string) error {
	if err := os.RemoveAll(s.SessionDir(sessionID)); err != nil {
		return fmt.Errorf("computer: remove session frames: %w", err)
	}
	return nil
}

func (s *FrameStore) Cleanup() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := s.prune(e.Name()); err != nil {
			return err
		}
	}
	return nil
}
