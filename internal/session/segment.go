package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/utils"
)

const (
	FormatLegacyMD   = "legacy_md"
	FormatSegmentV1  = "segment_v1"
	messagesJSONLName = "messages.jsonl"
)

func (s *Session) messagesJSONLPath() string {
	return filepath.Join(s.sessionRootDir(), messagesJSONLName)
}

func (s *Session) ensureSegmentDirs() error {
	if err := os.MkdirAll(s.sessionRootDir(), 0700); err != nil {
		return err
	}
	return os.MkdirAll(s.blobsDir(), 0700)
}

// appendMessagesJSONL appends messages as one JSON object per line.
func (s *Session) appendMessagesJSONL(messages []provider.Message) error {
	if len(messages) == 0 {
		return nil
	}
	if err := s.ensureSegmentDirs(); err != nil {
		return err
	}
	path := s.messagesJSONLPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open messages jsonl: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for i := range messages {
		line, err := json.Marshal(messages[i])
		if err != nil {
			return fmt.Errorf("marshal message: %w", err)
		}
		if _, err := w.Write(line); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Sync()
}

// rewriteMessagesJSONL replaces the entire jsonl file atomically.
func (s *Session) rewriteMessagesJSONL(messages []provider.Message) error {
	if err := s.ensureSegmentDirs(); err != nil {
		return err
	}
	var b strings.Builder
	for i := range messages {
		line, err := json.Marshal(messages[i])
		if err != nil {
			return fmt.Errorf("marshal message: %w", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return utils.WriteFileAtomic(s.messagesJSONLPath(), []byte(b.String()), 0600)
}

// readAllMessagesJSONL loads every message from the segment jsonl.
func readAllMessagesJSONL(path string) ([]provider.Message, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	messages := make([]provider.Message, 0, 256)
	scanner := bufio.NewScanner(file)
	// Large tool previews can still be multi-KB; allow up to 8MB per line.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 8*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg provider.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return nil, fmt.Errorf("decode messages.jsonl line %d: %w", lineNo, err)
		}
		messages = append(messages, msg)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan messages.jsonl: %w", err)
	}
	return messages, nil
}

// readMessagesJSONLTail returns the last keep messages and total count without
// retaining the entire history in an intermediate slice beyond the ring.
func readMessagesJSONLTail(path string, keep int) ([]provider.Message, int, error) {
	if keep < 1 {
		keep = 1
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	ring := make([]provider.Message, keep)
	seen := 0
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 8*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg provider.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return nil, 0, fmt.Errorf("decode messages.jsonl line %d: %w", lineNo, err)
		}
		ring[seen%keep] = msg
		seen++
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	if seen == 0 {
		return []provider.Message{}, 0, nil
	}
	available := seen
	if available > keep {
		available = keep
	}
	tail := make([]provider.Message, available)
	start := seen - available
	for i := 0; i < available; i++ {
		tail[i] = ring[(start+i)%keep]
	}
	return tail, seen, nil
}

func countMessagesJSONL(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 8*1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		count++
	}
	return count, scanner.Err()
}

func fileByteSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func segmentStorageBytes(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		// Archive rolls are cold storage; exclude them from live footprint so
		// NeedsRoll does not re-trigger immediately after a successful roll.
		if info.IsDir() && filepath.Base(path) == "archive" && path != root {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}
