package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ChangeType represents the type of file change detected.
type ChangeType int

const (
	ChangeNone ChangeType = iota
	ChangeAdded
	ChangeModified
	ChangeDeleted
)

func (ct ChangeType) String() string {
	switch ct {
	case ChangeAdded:
		return "added"
	case ChangeModified:
		return "modified"
	case ChangeDeleted:
		return "deleted"
	default:
		return "none"
	}
}

// FileChange represents a detected file change.
type FileChange struct {
	Path      string
	Type      ChangeType
	OldHash   string
	NewHash   string
	Timestamp time.Time
}

// IndexJob represents an indexing job in the queue.
type IndexJob struct {
	ID          string
	Path        string
	JobType     ChangeType
	Priority    int // higher = more urgent
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	Error       error
}

// IndexQueue manages pending indexing jobs.
type IndexQueue struct {
	mu    sync.RWMutex
	jobs  map[string]*IndexJob // path -> job
	order []*IndexJob          // sorted by priority
}

// NewIndexQueue creates a new index queue.
func NewIndexQueue() *IndexQueue {
	return &IndexQueue{
		jobs:  make(map[string]*IndexJob),
		order: make([]*IndexJob, 0),
	}
}

// Add adds a job to the queue. If a job for the path exists, it updates priority.
func (q *IndexQueue) Add(path string, jobType ChangeType, priority int) *IndexJob {
	q.mu.Lock()
	defer q.mu.Unlock()

	jobID := jobID(path)
	now := time.Now()

	if existing, ok := q.jobs[path]; ok {
		// Update existing job
		if priority > existing.Priority {
			existing.Priority = priority
			q.sortLocked() // re-sort when priority changes
		}
		existing.JobType = jobType
		return existing
	}

	job := &IndexJob{
		ID:        jobID,
		Path:      path,
		JobType:   jobType,
		Priority:  priority,
		CreatedAt: now,
	}

	q.jobs[path] = job
	q.order = append(q.order, job)
	q.sortLocked()

	return job
}

// Pop removes and returns the highest priority job.
func (q *IndexQueue) Pop() *IndexJob {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.order) == 0 {
		return nil
	}

	job := q.order[0]
	q.order = q.order[1:]
	delete(q.jobs, job.Path)

	return job
}

// Peek returns the highest priority job without removing it.
func (q *IndexQueue) Peek() *IndexJob {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if len(q.order) == 0 {
		return nil
	}
	return q.order[0]
}

// Len returns the number of pending jobs.
func (q *IndexQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.order)
}

// Clear removes all jobs from the queue.
func (q *IndexQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = make(map[string]*IndexJob)
	q.order = make([]*IndexJob, 0)
}

// List returns all pending jobs.
func (q *IndexQueue) List() []*IndexJob {
	q.mu.RLock()
	defer q.mu.RUnlock()

	jobs := make([]*IndexJob, len(q.order))
	copy(jobs, q.order)
	return jobs
}

// Remove removes a job by path.
func (q *IndexQueue) Remove(path string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.jobs[path]; !ok {
		return false
	}

	delete(q.jobs, path)

	// Rebuild order slice
	newOrder := make([]*IndexJob, 0, len(q.order))
	for _, job := range q.order {
		if job.Path != path {
			newOrder = append(newOrder, job)
		}
	}
	q.order = newOrder

	return true
}

func (q *IndexQueue) sortLocked() {
	// Simple insertion sort for small queues
	for i := 1; i < len(q.order); i++ {
		for j := i; j > 0 && q.order[j].Priority > q.order[j-1].Priority; j-- {
			q.order[j], q.order[j-1] = q.order[j-1], q.order[j]
		}
	}
}

// ChangeDetector detects file changes using hash comparison.
type ChangeDetector struct {
	mu       sync.RWMutex
	hashes   map[string]string // path -> hash
	fileInfo map[string]os.FileInfo
}

// NewChangeDetector creates a new change detector.
func NewChangeDetector() *ChangeDetector {
	return &ChangeDetector{
		hashes:   make(map[string]string),
		fileInfo: make(map[string]os.FileInfo),
	}
}

// ComputeHash computes SHA256 hash of a file.
func (cd *ChangeDetector) ComputeHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// Snapshot takes a snapshot of a directory's file hashes.
func (cd *ChangeDetector) Snapshot(dir string, extensions []string) error {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			return nil
		}

		// Check extension
		ext := strings.ToLower(filepath.Ext(path))
		matched := len(extensions) == 0
		for _, e := range extensions {
			if strings.ToLower(e) == ext {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}

		hash, err := cd.ComputeHash(path)
		if err != nil {
			return nil // skip unreadable files
		}

		cd.hashes[path] = hash
		cd.fileInfo[path] = info
		return nil
	})
}

// DetectChanges compares current state with snapshot and returns changes.
func (cd *ChangeDetector) DetectChanges(dir string, extensions []string) []FileChange {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	changes := make([]FileChange, 0)
	currentHashes := make(map[string]string)
	currentInfo := make(map[string]os.FileInfo)

	// Scan current state
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		matched := len(extensions) == 0
		for _, e := range extensions {
			if strings.ToLower(e) == ext {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}

		hash, err := cd.ComputeHash(path)
		if err != nil {
			return nil
		}

		currentHashes[path] = hash
		currentInfo[path] = info

		// Check if new or modified
		oldHash, existed := cd.hashes[path]
		now := time.Now()

		if !existed {
			changes = append(changes, FileChange{
				Path:      path,
				Type:      ChangeAdded,
				NewHash:   hash,
				Timestamp: now,
			})
		} else if oldHash != hash {
			changes = append(changes, FileChange{
				Path:      path,
				Type:      ChangeModified,
				OldHash:   oldHash,
				NewHash:   hash,
				Timestamp: now,
			})
		}

		return nil
	})

	// Check for deleted files
	for path, oldHash := range cd.hashes {
		if !pathWithinDir(path, dir) {
			continue
		}
		if _, exists := currentHashes[path]; !exists {
			changes = append(changes, FileChange{
				Path:      path,
				Type:      ChangeDeleted,
				OldHash:   oldHash,
				Timestamp: time.Now(),
			})
		}
	}

	// Only unchanged files advance immediately. Added and modified files are
	// committed by the indexer after a successful index operation.
	for path, hash := range currentHashes {
		if oldHash, exists := cd.hashes[path]; exists && oldHash == hash {
			cd.fileInfo[path] = currentInfo[path]
		}
	}

	return changes
}

func pathWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// GetHash returns the stored hash for a path.
func (cd *ChangeDetector) GetHash(path string) (string, bool) {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	h, ok := cd.hashes[path]
	return h, ok
}

// SetHash manually sets a hash for a path.
func (cd *ChangeDetector) SetHash(path, hash string) {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	cd.hashes[path] = hash
}

// RemoveHash removes a hash entry.
func (cd *ChangeDetector) RemoveHash(path string) {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	delete(cd.hashes, path)
	delete(cd.fileInfo, path)
}

// StreamIndexer provides incremental indexing with change detection.
type StreamIndexer struct {
	rag        *RAGManager
	detector   *ChangeDetector
	queue      *IndexQueue
	watchDirs  []string
	extensions []string

	mu      sync.RWMutex
	running bool
	stopCh  chan struct{}
	workers int

	// Callbacks
	OnChange func(change FileChange)
	OnIndex  func(job *IndexJob, doc *Document, err error)
}

// StreamConfig configures the stream indexer.
type StreamConfig struct {
	WatchDirs  []string
	Extensions []string
	Workers    int
}

// DefaultStreamConfig returns default configuration.
func DefaultStreamConfig() StreamConfig {
	return StreamConfig{
		Extensions: []string{".md", ".txt"},
		Workers:    2,
	}
}

// NewStreamIndexer creates a new stream indexer.
func NewStreamIndexer(rag *RAGManager, config StreamConfig) *StreamIndexer {
	if config.Workers <= 0 {
		config.Workers = 2
	}

	return &StreamIndexer{
		rag:        rag,
		detector:   NewChangeDetector(),
		queue:      NewIndexQueue(),
		watchDirs:  config.WatchDirs,
		extensions: config.Extensions,
		workers:    config.Workers,
		stopCh:     make(chan struct{}),
	}
}

// AddWatchDir adds a directory to watch.
func (si *StreamIndexer) AddWatchDir(dir string) {
	dir = filepath.Clean(dir)
	si.mu.Lock()
	defer si.mu.Unlock()
	for _, existing := range si.watchDirs {
		if filepath.Clean(existing) == dir {
			return
		}
	}
	si.watchDirs = append(si.watchDirs, dir)
}

// RemoveWatchDir removes a watched directory.
func (si *StreamIndexer) RemoveWatchDir(dir string) {
	dir = filepath.Clean(dir)
	si.mu.Lock()
	defer si.mu.Unlock()

	newDirs := make([]string, 0, len(si.watchDirs))
	for _, d := range si.watchDirs {
		if filepath.Clean(d) != dir {
			newDirs = append(newDirs, d)
		}
	}
	si.watchDirs = newDirs
}

// Snapshot takes initial snapshots of all watched directories.
func (si *StreamIndexer) Snapshot() error {
	si.mu.RLock()
	dirs := make([]string, len(si.watchDirs))
	copy(dirs, si.watchDirs)
	si.mu.RUnlock()

	for _, dir := range dirs {
		if err := si.detector.Snapshot(dir, si.extensions); err != nil {
			return fmt.Errorf("snapshot %s: %w", dir, err)
		}
	}
	return nil
}

// Scan detects changes and queues indexing jobs.
func (si *StreamIndexer) Scan() []FileChange {
	si.mu.RLock()
	dirs := make([]string, len(si.watchDirs))
	copy(dirs, si.watchDirs)
	si.mu.RUnlock()

	var allChanges []FileChange
	for _, dir := range dirs {
		changes := si.detector.DetectChanges(dir, si.extensions)
		allChanges = append(allChanges, changes...)

		// Queue jobs
		for _, change := range changes {
			priority := 5 // default priority
			if change.Type == ChangeDeleted {
				priority = 10 // delete first
			}

			si.queue.Add(change.Path, change.Type, priority)

			if si.OnChange != nil {
				si.OnChange(change)
			}
		}
	}

	return allChanges
}

// ProcessOne processes one job from the queue.
func (si *StreamIndexer) ProcessOne(ctx context.Context) (*IndexJob, *Document, error) {
	job := si.queue.Pop()
	if job == nil {
		return nil, nil, nil
	}

	job.StartedAt = time.Now()
	var doc *Document
	var err error

	switch job.JobType {
	case ChangeAdded, ChangeModified:
		doc, err = si.indexPathContext(ctx, job.Path)
	case ChangeDeleted:
		_ = si.RemovePath(job.Path)
		si.detector.RemoveHash(job.Path)
	}

	job.CompletedAt = time.Now()
	job.Error = err

	if si.OnIndex != nil {
		si.OnIndex(job, doc, err)
	}

	return job, doc, err
}

// ProcessBatch processes up to n jobs from the queue.
func (si *StreamIndexer) ProcessBatch(ctx context.Context, n int) ([]*IndexJob, []*Document, []error) {
	jobs := make([]*IndexJob, 0, n)
	docs := make([]*Document, 0, n)
	errs := make([]error, 0, n)

	for i := 0; i < n && si.queue.Len() > 0; i++ {
		job, doc, err := si.ProcessOne(ctx)
		if job == nil {
			break
		}
		jobs = append(jobs, job)
		docs = append(docs, doc)
		errs = append(errs, err)
	}

	return jobs, docs, errs
}

// Start starts background workers.
func (si *StreamIndexer) Start() {
	si.mu.Lock()
	if si.running {
		si.mu.Unlock()
		return
	}
	si.stopCh = make(chan struct{})
	stopCh := si.stopCh
	si.running = true
	si.mu.Unlock()

	for i := 0; i < si.workers; i++ {
		go si.worker(i, stopCh)
	}
}

// Stop stops all workers.
func (si *StreamIndexer) Stop() {
	si.mu.Lock()
	if !si.running {
		si.mu.Unlock()
		return
	}
	si.running = false
	close(si.stopCh)
	si.mu.Unlock()
}

// IsRunning returns whether the indexer is running.
func (si *StreamIndexer) IsRunning() bool {
	si.mu.RLock()
	defer si.mu.RUnlock()
	return si.running
}

// Queue returns the underlying queue.
func (si *StreamIndexer) Queue() *IndexQueue {
	return si.queue
}

// Detector returns the underlying change detector.
func (si *StreamIndexer) Detector() *ChangeDetector {
	return si.detector
}

func (si *StreamIndexer) worker(id int, stopCh <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			if id == 0 {
				si.Scan()
			}

			// Process pending jobs
			for si.queue.Len() > 0 {
				si.ProcessOne(context.Background())
			}
		}
	}
}

// IndexPath indexes a single path immediately.
func (si *StreamIndexer) IndexPath(path string) (*Document, error) {
	return si.indexPathContext(context.Background(), path)
}

func (si *StreamIndexer) indexPathContext(ctx context.Context, path string) (*Document, error) {
	beforeHash, err := si.detector.ComputeHash(path)
	if err != nil {
		return nil, fmt.Errorf("compute hash: %w", err)
	}

	doc, err := si.rag.IndexFileContext(ctx, path)
	if err != nil {
		return nil, err
	}
	afterHash, err := si.detector.ComputeHash(path)
	if err != nil {
		return nil, fmt.Errorf("verify indexed hash: %w", err)
	}
	if beforeHash != afterHash {
		return nil, fmt.Errorf("file changed while indexing: %s", path)
	}
	si.detector.SetHash(path, afterHash)
	return doc, nil
}

// RemovePath removes a path from the index.
func (si *StreamIndexer) RemovePath(path string) bool {
	removed := si.rag.RemoveDocument(docID(path))
	if removed {
		si.detector.RemoveHash(path)
	}
	return removed
}

// Stats returns stream indexer statistics.
type StreamStats struct {
	QueueLen     int
	WatchDirs    []string
	TrackedFiles int
	Running      bool
}

// Stats returns current statistics.
func (si *StreamIndexer) Stats() StreamStats {
	si.mu.RLock()
	defer si.mu.RUnlock()

	return StreamStats{
		QueueLen:     si.queue.Len(),
		WatchDirs:    append([]string(nil), si.watchDirs...),
		TrackedFiles: len(si.detector.hashes),
		Running:      si.running,
	}
}

func jobID(path string) string {
	h := sha256.Sum256([]byte(path))
	return fmt.Sprintf("job-%x", h[:8])
}
