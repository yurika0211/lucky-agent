package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/utils"
)

const (
	FormatLegacyMD    = "legacy_md"
	FormatSegmentV1   = "segment_v1"
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

// jsonlAppend records where an append landed. Offsets[i] is the file position
// of messages[i]. Size is the file length after the records and their newlines.
type jsonlAppend struct {
	Start   int64
	Size    int64
	Offsets []int64
}

// appendMessagesJSONL appends messages as one JSON object per line.
// On success Size is the file length after the append and Offsets holds each
// new record's starting byte. An empty append returns the current size.
func (s *Session) appendMessagesJSONL(messages []provider.Message) (result jsonlAppend, retErr error) {
	if len(messages) == 0 {
		info, err := os.Stat(s.messagesJSONLPath())
		if err != nil {
			return jsonlAppend{}, nil
		}
		return jsonlAppend{Start: info.Size(), Size: info.Size()}, nil
	}
	if err := s.ensureSegmentDirs(); err != nil {
		return jsonlAppend{}, err
	}
	path := s.messagesJSONLPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return jsonlAppend{}, fmt.Errorf("open messages jsonl: %w", err)
	}
	defer f.Close()
	if err := prepareJSONLAppend(f); err != nil {
		return jsonlAppend{}, err
	}
	info, err := f.Stat()
	if err != nil {
		return jsonlAppend{}, err
	}
	start := info.Size()
	result.Start = start
	result.Offsets = make([]int64, len(messages))
	// Save retries the pending in-memory suffix after a failure. Roll back any
	// complete records written by this attempt so that retry cannot duplicate it.
	defer func() {
		if retErr != nil {
			if err := f.Truncate(info.Size()); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("rollback jsonl append: %w", err))
			} else if err := f.Sync(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("sync jsonl rollback: %w", err))
			}
		}
	}()

	w := bufio.NewWriter(f)
	written := int64(0)
	for i := range messages {
		result.Offsets[i] = start + written
		line, err := json.Marshal(messages[i])
		if err != nil {
			return jsonlAppend{}, fmt.Errorf("marshal message: %w", err)
		}
		n, err := w.Write(line)
		written += int64(n)
		if err != nil {
			return jsonlAppend{}, err
		}
		if err := w.WriteByte('\n'); err != nil {
			return jsonlAppend{}, err
		}
		written++
	}
	if err := w.Flush(); err != nil {
		return jsonlAppend{}, err
	}
	if err := f.Sync(); err != nil {
		return jsonlAppend{}, err
	}
	result.Size = start + written
	return result, nil
}

// jsonlRewrite is a full transcript replacement and the byte position where
// the latest 500-message window starts inside it.
type jsonlRewrite struct {
	Size       int64
	TailOffset int64
	TailStart  int
}

// rewriteMessagesJSONL replaces the entire jsonl file atomically and records
// the latest window without a second read of the file.
func (s *Session) rewriteMessagesJSONL(messages []provider.Message) (jsonlRewrite, error) {
	if err := s.ensureSegmentDirs(); err != nil {
		return jsonlRewrite{}, err
	}
	var b strings.Builder
	window := len(messages) - maxPageCacheMessages
	if window < 0 {
		window = 0
	}
	var result jsonlRewrite
	result.TailStart = window
	for i := range messages {
		if i == window {
			result.TailOffset = int64(b.Len())
		}
		line, err := json.Marshal(messages[i])
		if err != nil {
			return jsonlRewrite{}, fmt.Errorf("marshal message: %w", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	payload := []byte(b.String())
	if err := utils.WriteFileAtomic(s.messagesJSONLPath(), payload, 0600); err != nil {
		return jsonlRewrite{}, err
	}
	result.Size = int64(len(payload))
	return result, nil
}

const maxMessageRecordBytes = 8 * 1024 * 1024

// walkMessagesJSONL treats a newline as the commit boundary. Only an incomplete
// final record without a newline may be skipped after an interrupted append.
// Readers never change the file; the next successful append repairs that tail.
func walkMessagesJSONL(r io.Reader, offset int64, lineNo int, visit func(provider.Message, int64, int) error) error {
	return walkMessagesJSONLMode(r, offset, lineNo, true, visit)
}

func walkMessagesJSONLMode(r io.Reader, offset int64, lineNo int, allowIncompleteTail bool, visit func(provider.Message, int64, int) error) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	for {
		start := offset
		var line []byte
		var readErr error
		for {
			part, err := reader.ReadSlice('\n')
			if len(line)+len(part) > maxMessageRecordBytes {
				return fmt.Errorf("messages.jsonl line %d exceeds %d bytes", lineNo, maxMessageRecordBytes)
			}
			line = append(line, part...)
			offset += int64(len(part))
			if err != bufio.ErrBufferFull {
				readErr = err
				break
			}
		}
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("read messages.jsonl line %d: %w", lineNo, readErr)
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var msg provider.Message
			if err := json.Unmarshal(trimmed, &msg); err != nil {
				if allowIncompleteTail && readErr == io.EOF && incompleteJSONRecord(trimmed) {
					return nil
				}
				return fmt.Errorf("decode messages.jsonl line %d: %w", lineNo, err)
			}
			if err := visit(msg, start, lineNo); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		lineNo++
	}
}

func incompleteJSONRecord(data []byte) bool {
	var value json.RawMessage
	err := json.NewDecoder(bytes.NewReader(data)).Decode(&value)
	return errors.Is(err, io.ErrUnexpectedEOF)
}

// prepareJSONLAppend inspects only the last uncommitted line. A complete legacy
// record without a newline gets a separator; a partial record is truncated to
// the previous commit boundary before any new records are appended.
func prepareJSONLAppend(f *os.File) error {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	end := info.Size()
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, end-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	start := end
	var tail []byte
	for start > 0 {
		size := int64(64 * 1024)
		if size > start {
			size = start
		}
		start -= size
		chunk := make([]byte, size)
		if _, err := f.ReadAt(chunk, start); err != nil {
			return err
		}
		if newline := bytes.LastIndexByte(chunk, '\n'); newline >= 0 {
			start += int64(newline + 1)
			tail = append(chunk[newline+1:], tail...)
			break
		}
		tail = append(chunk, tail...)
		if len(tail) > maxMessageRecordBytes {
			return fmt.Errorf("messages.jsonl final record exceeds %d bytes", maxMessageRecordBytes)
		}
	}
	trimmed := bytes.TrimSpace(tail)
	if len(trimmed) > 0 {
		var msg provider.Message
		if err := json.Unmarshal(trimmed, &msg); err != nil {
			if !incompleteJSONRecord(trimmed) {
				return fmt.Errorf("decode messages.jsonl final record: %w", err)
			}
			if err := f.Truncate(start); err != nil {
				return fmt.Errorf("repair incomplete messages.jsonl tail: %w", err)
			}
			return f.Sync()
		}
	}
	_, err = f.Write([]byte{'\n'})
	return err
}

// readAllMessagesJSONL loads every committed message from the segment jsonl.
func readAllMessagesJSONL(path string) ([]provider.Message, error) {
	return readMessagesJSONLMode(path, true)
}

// Maintenance must not discard references from unreadable source records.
func readAllMessagesJSONLStrict(path string) ([]provider.Message, error) {
	return readMessagesJSONLMode(path, false)
}

func readMessagesJSONLMode(path string, allowIncompleteTail bool) ([]provider.Message, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	messages := make([]provider.Message, 0, 256)
	err = walkMessagesJSONLMode(file, 0, 1, allowIncompleteTail, func(msg provider.Message, _ int64, _ int) error {
		messages = append(messages, msg)
		return nil
	})
	if err != nil {
		return nil, err
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
	err = walkMessagesJSONL(file, 0, 1, func(msg provider.Message, _ int64, _ int) error {
		ring[seen%keep] = msg
		seen++
		return nil
	})
	if err != nil {
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
	return countMessagesJSONLMode(path, true)
}

// countReadableMessagesJSONL rejects a damaged record. Callers use it to tell
// a complete external replacement from a truncated committed message.
func countReadableMessagesJSONL(path string) (int, error) {
	return countMessagesJSONLMode(path, false)
}

func countMessagesJSONLMode(path string, allowIncompleteTail bool) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()
	count := 0
	err = walkMessagesJSONLMode(file, 0, 1, allowIncompleteTail, func(_ provider.Message, _ int64, _ int) error {
		count++
		return nil
	})
	return count, err
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
