package rag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIndexQueue(t *testing.T) {
	q := NewIndexQueue()

	// Test empty queue
	if q.Len() != 0 {
		t.Errorf("expected empty queue, got %d", q.Len())
	}
	if q.Pop() != nil {
		t.Error("expected nil from empty queue")
	}

	// Add jobs
	job1 := q.Add("/path/1.md", ChangeAdded, 5)
	if job1 == nil {
		t.Fatal("expected job, got nil")
	}
	if job1.Priority != 5 {
		t.Errorf("expected priority 5, got %d", job1.Priority)
	}

	q.Add("/path/2.md", ChangeModified, 10)
	q.Add("/path/3.md", ChangeDeleted, 7)

	// Test length
	if q.Len() != 3 {
		t.Errorf("expected 3 jobs, got %d", q.Len())
	}

	// Pop should return highest priority first
	popped := q.Pop()
	if popped.Path != "/path/2.md" {
		t.Errorf("expected /path/2.md (priority 10), got %s", popped.Path)
	}

	// Test update existing job
	q.Add("/path/1.md", ChangeModified, 15) // upgrade priority
	popped = q.Pop()
	if popped.Path != "/path/1.md" {
		t.Errorf("expected /path/1.md (upgraded priority), got %s", popped.Path)
	}

	// Test remove
	q.Add("/path/4.md", ChangeAdded, 3)
	if !q.Remove("/path/4.md") {
		t.Error("expected remove to succeed")
	}
	if q.Remove("/path/4.md") {
		t.Error("expected remove to fail for non-existent")
	}

	// Test clear
	q.Add("/path/5.md", ChangeAdded, 1)
	q.Clear()
	if q.Len() != 0 {
		t.Errorf("expected empty after clear, got %d", q.Len())
	}
}

func TestChangeDetector(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "test1.md")
	file2 := filepath.Join(tmpDir, "test2.txt")
	file3 := filepath.Join(tmpDir, "test3.go")

	os.WriteFile(file1, []byte("# Test 1\nContent 1"), 0644)
	os.WriteFile(file2, []byte("Test 2 content"), 0644)
	os.WriteFile(file3, []byte("package main"), 0644)

	cd := NewChangeDetector()

	// Test ComputeHash
	hash1, err := cd.ComputeHash(file1)
	if err != nil {
		t.Fatalf("compute hash: %v", err)
	}
	if hash1 == "" {
		t.Error("expected non-empty hash")
	}

	// Test Snapshot
	err = cd.Snapshot(tmpDir, []string{".md", ".txt"})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Verify hashes stored
	if h, ok := cd.GetHash(file1); !ok || h != hash1 {
		t.Error("expected hash to be stored")
	}
	if _, ok := cd.GetHash(file3); ok {
		t.Error("expected .go file to be excluded")
	}

	// Modify file1
	time.Sleep(10 * time.Millisecond) // ensure different timestamp
	os.WriteFile(file1, []byte("# Test 1 Modified\nNew content"), 0644)

	// Add new file
	file4 := filepath.Join(tmpDir, "test4.md")
	os.WriteFile(file4, []byte("New file"), 0644)

	// Delete file2
	os.Remove(file2)

	// Detect changes
	changes := cd.DetectChanges(tmpDir, []string{".md", ".txt"})

	// Should detect 3 changes: modified file1, added file4, deleted file2
	changeMap := make(map[string]ChangeType)
	for _, c := range changes {
		changeMap[c.Path] = c.Type
	}

	if changeMap[file1] != ChangeModified {
		t.Errorf("expected file1 to be modified, got %v", changeMap[file1])
	}
	if changeMap[file4] != ChangeAdded {
		t.Errorf("expected file4 to be added, got %v", changeMap[file4])
	}
	if changeMap[file2] != ChangeDeleted {
		t.Errorf("expected file2 to be deleted, got %v", changeMap[file2])
	}
}

func TestChangeTypeString(t *testing.T) {
	tests := []struct {
		ct       ChangeType
		expected string
	}{
		{ChangeNone, "none"},
		{ChangeAdded, "added"},
		{ChangeModified, "modified"},
		{ChangeDeleted, "deleted"},
	}

	for _, tt := range tests {
		if got := tt.ct.String(); got != tt.expected {
			t.Errorf("ChangeType(%d).String() = %s, want %s", tt.ct, got, tt.expected)
		}
	}
}

func TestStreamIndexerBasic(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "doc1.md")
	os.WriteFile(file1, []byte("# Document 1\nThis is content."), 0644)

	// Create mock embedder
	embedder := newMockEmbedder(128)

	// Create RAG manager
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	// Create stream indexer
	si := NewStreamIndexer(rag, StreamConfig{
		WatchDirs:  []string{tmpDir},
		Extensions: []string{".md"},
		Workers:    1,
	})

	// Test initial snapshot
	err := si.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Index the file
	doc, err := si.IndexPath(file1)
	if err != nil {
		t.Fatalf("index path: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document, got nil")
	}
	if doc.Title != "Document 1" {
		t.Errorf("expected title 'Document 1', got %s", doc.Title)
	}

	// Verify in RAG index
	stats := rag.Stats()
	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 document, got %d", stats.DocumentCount)
	}

	// Test remove
	removed := si.RemovePath(file1)
	if !removed {
		t.Error("expected remove to succeed")
	}

	stats = rag.Stats()
	if stats.DocumentCount != 0 {
		t.Errorf("expected 0 documents after remove, got %d", stats.DocumentCount)
	}
}

func TestStreamIndexerScan(t *testing.T) {
	tmpDir := t.TempDir()

	// Create initial files
	file1 := filepath.Join(tmpDir, "file1.md")
	os.WriteFile(file1, []byte("# File 1"), 0644)

	embedder := newMockEmbedder(128)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	si := NewStreamIndexer(rag, StreamConfig{
		WatchDirs:  []string{tmpDir},
		Extensions: []string{".md"},
		Workers:    0, // no background workers
	})

	// Take initial snapshot
	si.Snapshot()

	// Index initial file
	si.IndexPath(file1)

	// Add new file
	file2 := filepath.Join(tmpDir, "file2.md")
	os.WriteFile(file2, []byte("# File 2"), 0644)

	// Modify existing file
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(file1, []byte("# File 1 Modified"), 0644)

	// Scan for changes
	changes := si.Scan()

	changeMap := make(map[string]ChangeType)
	for _, c := range changes {
		changeMap[c.Path] = c.Type
	}

	if changeMap[file1] != ChangeModified {
		t.Errorf("expected file1 to be modified, got %v", changeMap[file1])
	}
	if changeMap[file2] != ChangeAdded {
		t.Errorf("expected file2 to be added, got %v", changeMap[file2])
	}

	// Verify queue has jobs
	if si.Queue().Len() != 2 {
		t.Errorf("expected 2 jobs in queue, got %d", si.Queue().Len())
	}
}

func TestStreamIndexerProcessBatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files
	for i := 0; i < 3; i++ {
		path := filepath.Join(tmpDir, "file%d.md")
		os.WriteFile(path, []byte("# File %d"), 0644)
	}

	embedder := newMockEmbedder(128)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	si := NewStreamIndexer(rag, StreamConfig{
		WatchDirs:  []string{tmpDir},
		Extensions: []string{".md"},
	})

	si.Snapshot()

	// Add files to queue manually
	si.Queue().Add(filepath.Join(tmpDir, "file0.md"), ChangeAdded, 5)
	si.Queue().Add(filepath.Join(tmpDir, "file1.md"), ChangeAdded, 5)
	si.Queue().Add(filepath.Join(tmpDir, "file2.md"), ChangeAdded, 5)

	// Process batch
	jobs, docs, errs := si.ProcessBatch(context.Background(), 2)

	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(jobs))
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 docs, got %d", len(docs))
	}

	// Check for errors (some may fail if files don't exist)
	for i, err := range errs {
		if err != nil {
			t.Logf("job %d error: %v", i, err)
		}
	}

	// Queue should have 1 remaining
	if si.Queue().Len() != 1 {
		t.Errorf("expected 1 job remaining, got %d", si.Queue().Len())
	}
}

func TestStreamIndexerStats(t *testing.T) {
	tmpDir := t.TempDir()

	embedder := newMockEmbedder(128)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	si := NewStreamIndexer(rag, StreamConfig{
		WatchDirs:  []string{tmpDir},
		Extensions: []string{".md"},
		Workers:    2,
	})

	stats := si.Stats()

	if stats.Running {
		t.Error("expected not running initially")
	}
	if len(stats.WatchDirs) != 1 {
		t.Errorf("expected 1 watch dir, got %d", len(stats.WatchDirs))
	}
}

func TestStreamIndexerAddRemoveWatchDir(t *testing.T) {
	embedder := newMockEmbedder(128)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	si := NewStreamIndexer(rag, DefaultStreamConfig())

	// Add watch dir
	si.AddWatchDir("/path/to/dir1")
	si.AddWatchDir("/path/to/dir2")

	stats := si.Stats()
	if len(stats.WatchDirs) != 2 {
		t.Errorf("expected 2 watch dirs, got %d", len(stats.WatchDirs))
	}

	// Remove watch dir
	si.RemoveWatchDir("/path/to/dir1")

	stats = si.Stats()
	if len(stats.WatchDirs) != 1 {
		t.Errorf("expected 1 watch dir after remove, got %d", len(stats.WatchDirs))
	}
}

func TestStreamIndexerStartStop(t *testing.T) {
	embedder := newMockEmbedder(128)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	si := NewStreamIndexer(rag, StreamConfig{
		Workers: 1,
	})

	if si.IsRunning() {
		t.Error("expected not running initially")
	}

	// Start
	si.Start()
	if !si.IsRunning() {
		t.Error("expected running after Start()")
	}

	// Double start should be no-op
	si.Start()
	if !si.IsRunning() {
		t.Error("expected still running")
	}

	// Stop
	si.Stop()
	// Give time for goroutines to stop
	time.Sleep(100 * time.Millisecond)

	if si.IsRunning() {
		t.Error("expected not running after Stop()")
	}

	// Double stop should be no-op
	si.Stop()
}

func TestStreamIndexerOnChangeCallback(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "callback.md")
	os.WriteFile(file1, []byte("# Callback Test"), 0644)

	embedder := newMockEmbedder(128)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	si := NewStreamIndexer(rag, StreamConfig{
		WatchDirs:  []string{tmpDir},
		Extensions: []string{".md"},
	})

	var callbackCalled bool
	var receivedChange FileChange

	si.OnChange = func(change FileChange) {
		callbackCalled = true
		receivedChange = change
	}

	si.Snapshot()

	// Modify file
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(file1, []byte("# Callback Test Modified"), 0644)

	// Scan should trigger callback
	si.Scan()

	if !callbackCalled {
		t.Error("expected OnChange callback to be called")
	}
	if receivedChange.Type != ChangeModified {
		t.Errorf("expected ChangeModified, got %v", receivedChange.Type)
	}
}

func TestStreamIndexerOnIndexCallback(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "index_callback.md")
	os.WriteFile(file1, []byte("# Index Callback Test"), 0644)

	embedder := newMockEmbedder(128)
	rag := NewRAGManager(embedder, DefaultRAGConfig())

	si := NewStreamIndexer(rag, StreamConfig{
		WatchDirs:  []string{tmpDir},
		Extensions: []string{".md"},
	})

	si.Snapshot()

	var callbackCalled bool
	var receivedJob *IndexJob

	si.OnIndex = func(job *IndexJob, doc *Document, err error) {
		callbackCalled = true
		receivedJob = job
	}

	// Add job and process
	si.Queue().Add(file1, ChangeAdded, 5)
	si.ProcessOne(context.Background())

	if !callbackCalled {
		t.Error("expected OnIndex callback to be called")
	}
	if receivedJob == nil {
		t.Fatal("expected job in callback")
	}
	if receivedJob.Path != file1 {
		t.Errorf("expected path %s, got %s", file1, receivedJob.Path)
	}
}
