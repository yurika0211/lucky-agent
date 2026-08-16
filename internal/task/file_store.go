package task

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	taskFileName         = "task.json"
	eventsFileName       = "events.jsonl"
	resultFileName       = "result.md"
	plannerTraceFileName = "planner_trace.json"
)

type FileStore struct {
	mu   sync.RWMutex
	root string
}

func NewFileStore(root string) (*FileStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("task store root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create task store: %w", err)
	}
	return &FileStore{root: root}, nil
}

func (s *FileStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *FileStore) Create(record Record) (Record, error) {
	if s == nil {
		return Record{}, fmt.Errorf("task store is nil")
	}
	record.ID = sanitizeID(record.ID)
	if record.ID == "" {
		record.ID = fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	if record.Status == "" {
		record.Status = StatusPending
	}
	if record.Mode == "" {
		record.Mode = ModeSingle
	}
	if record.Source == "" {
		record.Source = SourceTool
	}
	if record.Metadata == nil {
		record.Metadata = make(map[string]string)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.taskDir(record.ID)
	if _, err := os.Stat(filepath.Join(dir, taskFileName)); err == nil {
		return Record{}, fmt.Errorf("task %s already exists", record.ID)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Record{}, fmt.Errorf("create task dir: %w", err)
	}
	if err := writeJSONFile(filepath.Join(dir, taskFileName), record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *FileStore) Update(record Record) error {
	if s == nil {
		return fmt.Errorf("task store is nil")
	}
	record.ID = sanitizeID(record.ID)
	if record.ID == "" {
		return fmt.Errorf("task id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.taskDir(record.ID)); err != nil {
		return fmt.Errorf("task %s not found", record.ID)
	}
	return writeJSONFile(filepath.Join(s.taskDir(record.ID), taskFileName), record)
}

func (s *FileStore) Get(id string) (Record, bool, error) {
	if s == nil {
		return Record{}, false, fmt.Errorf("task store is nil")
	}
	id = sanitizeID(id)
	if id == "" {
		return Record{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var record Record
	err := readJSONFile(filepath.Join(s.taskDir(id), taskFileName), &record)
	if os.IsNotExist(err) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

func (s *FileStore) List(filter ListFilter) ([]Record, error) {
	if s == nil {
		return nil, fmt.Errorf("task store is nil")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var record Record
		if err := readJSONFile(filepath.Join(s.root, entry.Name(), taskFileName), &record); err != nil {
			continue
		}
		if filter.Source != "" && record.Source != filter.Source {
			continue
		}
		if filter.Status != "" && record.Status != filter.Status {
			continue
		}
		if filter.ParentID != "" && record.ParentID != filter.ParentID {
			continue
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	if filter.Limit > 0 && len(records) > filter.Limit {
		records = records[:filter.Limit]
	}
	return records, nil
}

func (s *FileStore) AppendEvent(event Event) error {
	if s == nil {
		return fmt.Errorf("task store is nil")
	}
	event.TaskID = sanitizeID(event.TaskID)
	if event.TaskID == "" {
		return fmt.Errorf("task id is required")
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.taskDir(event.TaskID), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.taskDir(event.TaskID), eventsFileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *FileStore) Events(taskID string) ([]Event, error) {
	if s == nil {
		return nil, fmt.Errorf("task store is nil")
	}
	taskID = sanitizeID(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, err := os.Open(filepath.Join(s.taskDir(taskID), eventsFileName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *FileStore) SaveResult(taskID, markdown string) error {
	return s.writeArtifact(taskID, resultFileName, []byte(markdown))
}

func (s *FileStore) Result(taskID string) (string, bool, error) {
	data, ok, err := s.readArtifact(taskID, resultFileName)
	if err != nil || !ok {
		return "", ok, err
	}
	return string(data), true, nil
}

func (s *FileStore) SavePlannerTrace(taskID string, trace any) error {
	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return err
	}
	return s.writeArtifact(taskID, plannerTraceFileName, data)
}

func (s *FileStore) PlannerTrace(taskID string) ([]byte, bool, error) {
	return s.readArtifact(taskID, plannerTraceFileName)
}

func (s *FileStore) Cleanup(policy RetentionPolicy) (CleanupResult, error) {
	if s == nil {
		return CleanupResult{}, fmt.Errorf("task store is nil")
	}
	if policy.MaxAge <= 0 && policy.KeepLast <= 0 {
		return CleanupResult{}, fmt.Errorf("cleanup policy requires max age or keep last")
	}
	statuses := policy.Statuses
	if len(statuses) == 0 {
		statuses = []Status{StatusCompleted, StatusFailed, StatusCancelled}
	}
	statusSet := make(map[Status]struct{}, len(statuses))
	for _, status := range statuses {
		statusSet[status] = struct{}{}
	}

	records, err := s.List(ListFilter{})
	if err != nil {
		return CleanupResult{}, err
	}
	candidates := make([]Record, 0, len(records))
	for _, record := range records {
		if _, ok := statusSet[record.Status]; ok {
			candidates = append(candidates, record)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return retentionTime(candidates[i]).After(retentionTime(candidates[j]))
	})

	keep := make(map[string]struct{})
	if policy.KeepLast > 0 {
		for i, record := range candidates {
			if i >= policy.KeepLast {
				break
			}
			keep[record.ID] = struct{}{}
		}
	}
	cutoff := time.Now().Add(-policy.MaxAge)
	var result CleanupResult
	for _, record := range candidates {
		if _, ok := keep[record.ID]; ok {
			result.KeptCount++
			continue
		}
		if policy.MaxAge > 0 && retentionTime(record).After(cutoff) {
			result.KeptCount++
			continue
		}
		s.mu.Lock()
		err := os.RemoveAll(s.taskDir(record.ID))
		s.mu.Unlock()
		if err != nil {
			return result, err
		}
		result.DeletedIDs = append(result.DeletedIDs, record.ID)
	}
	return result, nil
}

func (s *FileStore) writeArtifact(taskID, name string, data []byte) error {
	if s == nil {
		return fmt.Errorf("task store is nil")
	}
	taskID = sanitizeID(taskID)
	if taskID == "" {
		return fmt.Errorf("task id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.taskDir(taskID), 0o700); err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(s.taskDir(taskID), name), data, 0o600)
}

func (s *FileStore) readArtifact(taskID, name string) ([]byte, bool, error) {
	if s == nil {
		return nil, false, fmt.Errorf("task store is nil")
	}
	taskID = sanitizeID(taskID)
	if taskID == "" {
		return nil, false, fmt.Errorf("task id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(s.taskDir(taskID), name))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s *FileStore) taskDir(id string) string {
	return filepath.Join(s.root, sanitizeID(id))
}

func retentionTime(record Record) time.Time {
	if !record.CompletedAt.IsZero() {
		return record.CompletedAt
	}
	return record.CreatedAt
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o600)
}

// atomicWriteFile prevents readers from observing a truncated state file while
// an asynchronous task transitions status. Rename is atomic when both paths
// are inside the same task directory.
func atomicWriteFile(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func readJSONFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}
