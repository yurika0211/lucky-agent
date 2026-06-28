package rag

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	embedderpkg "github.com/yurika0211/luckyagent/internal/embedder"
)

const defaultEmbeddingBatchSize = 16

// Chunk represents a segment of a document.
type Chunk struct {
	ID       string
	Content  string
	Metadata map[string]string
}

// Document is an indexed document with its chunks.
type Document struct {
	ID        string
	Path      string
	Title     string
	Chunks    []string // chunk IDs
	IndexedAt time.Time
	Metadata  map[string]string
}

// IndexStats holds statistics about the index.
type IndexStats struct {
	DocumentCount int
	ChunkCount    int
	TotalTokens   int // estimated
	LastIndexed   time.Time
	Sources       map[string]int // source -> count
}

// Indexer processes documents into chunks and stores them in the vector store.
type Indexer struct {
	store    VectorStoreBackend
	embedder embedderpkg.Embedder
	sqlite   *SQLiteStore // v0.20.0: optional SQLite backend for document persistence

	mu        sync.RWMutex
	documents map[string]*Document // docID -> Document
	chunks    map[string]*Chunk    // chunkID -> Chunk
	stats     IndexStats
}

func NewIndexer(store VectorStoreBackend, embedder embedderpkg.Embedder) *Indexer {
	indexer := &Indexer{
		store:     store,
		embedder:  embedder,
		documents: make(map[string]*Document),
		chunks:    make(map[string]*Chunk),
		stats: IndexStats{
			Sources: make(map[string]int),
		},
	}
	// If store is SQLite, set up persistence
	if sqlStore, ok := store.(*SQLiteStore); ok {
		indexer.sqlite = sqlStore
	}
	return indexer
}

// NewIndexerWithBackend creates an indexer with a VectorStoreBackend (alias for NewIndexer).
func NewIndexerWithBackend(store VectorStoreBackend, embedder embedderpkg.Embedder) *Indexer {
	return NewIndexer(store, embedder)
}

// IndexFile indexes a single file (Markdown or TXT).
func (idx *Indexer) IndexFile(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}

	content := string(data)
	title := extractTitle(content, path)

	return idx.IndexText(path, title, content)
}

// IndexText indexes raw text content with a given source path and title.
func (idx *Indexer) IndexText(source, title, content string) (*Document, error) {
	docID := docID(source)
	createdChunkIDs := make([]string, 0, 16)

	// Remove old chunks if re-indexing
	idx.mu.Lock()
	if oldDoc, exists := idx.documents[docID]; exists {
		for _, cid := range oldDoc.Chunks {
			idx.store.Delete(cid)
			delete(idx.chunks, cid)
		}
	}
	idx.mu.Unlock()

	// Split into chunks
	rawChunks := splitChunks(content, 512, 64)

	doc := &Document{
		ID:        docID,
		Path:      source,
		Title:     title,
		Chunks:    make([]string, 0, len(rawChunks)),
		IndexedAt: time.Now(),
		Metadata: map[string]string{
			"source": source,
			"title":  title,
		},
	}

	batchSize := embeddingBatchSizeFromEnv()
	for start := 0; start < len(rawChunks); start += batchSize {
		end := start + batchSize
		if end > len(rawChunks) {
			end = len(rawChunks)
		}

		batch := rawChunks[start:end]
		vecs, err := idx.embedder.EmbedBatch(context.Background(), batch)
		if err != nil {
			idx.rollbackChunks(createdChunkIDs)
			chunkID := fmt.Sprintf("%s#%d", docID, start)
			return nil, fmt.Errorf("embed chunk %s: %w", chunkID, err)
		}
		if len(vecs) != len(batch) {
			idx.rollbackChunks(createdChunkIDs)
			chunkID := fmt.Sprintf("%s#%d", docID, start)
			return nil, fmt.Errorf("embed chunk %s: returned %d vectors for %d texts", chunkID, len(vecs), len(batch))
		}

		for offset, chunkText := range batch {
			i := start + offset
			chunkID := fmt.Sprintf("%s#%d", docID, i)
			metadata := map[string]string{
				"source":  source,
				"title":   title,
				"chunk_i": fmt.Sprintf("%d", i),
				"doc_id":  docID,
			}

			if err := idx.store.Upsert(chunkID, vecs[offset], metadata); err != nil {
				idx.rollbackChunks(createdChunkIDs)
				return nil, fmt.Errorf("store chunk %s: %w", chunkID, err)
			}

			chunk := &Chunk{
				ID:       chunkID,
				Content:  chunkText,
				Metadata: metadata,
			}

			idx.mu.Lock()
			idx.chunks[chunkID] = chunk
			doc.Chunks = append(doc.Chunks, chunkID)
			idx.mu.Unlock()
			createdChunkIDs = append(createdChunkIDs, chunkID)
		}
	}

	idx.mu.Lock()
	idx.documents[docID] = doc
	idx.stats.DocumentCount = len(idx.documents)
	idx.stats.ChunkCount = len(idx.chunks)
	idx.stats.LastIndexed = doc.IndexedAt
	idx.stats.Sources[source] = len(rawChunks)
	chunkSnapshot := make(map[string]*Chunk, len(idx.chunks))
	for id, chunk := range idx.chunks {
		chunkSnapshot[id] = chunk
	}
	idx.mu.Unlock()

	// v0.20.0: Persist to SQLite if available
	if idx.sqlite != nil {
		if err := idx.sqlite.SaveDocument(doc, chunkSnapshot); err != nil {
			idx.rollbackChunks(createdChunkIDs)
			return nil, fmt.Errorf("save document %s: %w", doc.ID, err)
		}
	}

	return doc, nil
}

func (idx *Indexer) rollbackChunks(chunkIDs []string) {
	if len(chunkIDs) == 0 {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, id := range chunkIDs {
		idx.store.Delete(id)
		delete(idx.chunks, id)
	}
}

func embeddingBatchSizeFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("EMBEDDING_BATCH_SIZE"))
	if raw == "" {
		return defaultEmbeddingBatchSize
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultEmbeddingBatchSize
	}
	return n
}

// IndexDirectory indexes all .md and .txt files in a directory (non-recursive).
func (idx *Indexer) IndexDirectory(dir string) ([]*Document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	var docs []*Document
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".md" && ext != ".txt" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		doc, err := idx.IndexFile(path)
		if err != nil {
			return docs, fmt.Errorf("index %s: %w", path, err)
		}
		docs = append(docs, doc)
	}

	return docs, nil
}

// GetDocument returns a document by ID.
func (idx *Indexer) GetDocument(docID string) (*Document, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	d, ok := idx.documents[docID]
	if !ok {
		return nil, false
	}
	cp := *d
	cp.Chunks = append([]string{}, d.Chunks...)
	cp.Metadata = copyMap(d.Metadata)
	return &cp, true
}

// GetChunk returns a chunk by ID.
func (idx *Indexer) GetChunk(chunkID string) (*Chunk, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	c, ok := idx.chunks[chunkID]
	if !ok {
		return nil, false
	}
	cp := *c
	cp.Metadata = copyMap(c.Metadata)
	return &cp, true
}

// RemoveDocument removes a document and all its chunks.
func (idx *Indexer) RemoveDocument(docID string) bool {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	doc, exists := idx.documents[docID]
	if !exists {
		return false
	}

	for _, cid := range doc.Chunks {
		idx.store.Delete(cid)
		delete(idx.chunks, cid)
	}

	delete(idx.documents, docID)
	idx.stats.DocumentCount = len(idx.documents)
	idx.stats.ChunkCount = len(idx.chunks)
	delete(idx.stats.Sources, doc.Path)

	// v0.20.0: Remove from SQLite if available
	if idx.sqlite != nil {
		if err := idx.sqlite.DeleteDocument(docID); err != nil {
			_ = err
		}
	}

	return true
}

// Stats returns current index statistics.
func (idx *Indexer) Stats() IndexStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	stats := idx.stats
	stats.Sources = make(map[string]int)
	for k, v := range idx.stats.Sources {
		stats.Sources[k] = v
	}
	return stats
}

// ListDocuments returns all document IDs.
func (idx *Indexer) ListDocuments() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	ids := make([]string, 0, len(idx.documents))
	for id := range idx.documents {
		ids = append(ids, id)
	}
	return ids
}

// --- helpers ---

func docID(source string) string {
	h := sha256.Sum256([]byte(source))
	return fmt.Sprintf("%x", h[:8])
}

func extractTitle(content, path string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimPrefix(trimmed, "# ")
		}
	}
	// Fallback to filename
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// splitChunks splits text into overlapping chunks.
// chunkSize is the target size in characters; overlap is the overlap size.
func splitChunks(text string, chunkSize, overlap int) []string {
	if chunkSize <= 0 {
		chunkSize = 512
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 4
	}

	// Split by paragraphs first, then by sentences, then by characters
	paragraphs := strings.Split(text, "\n\n")

	var chunks []string
	var current strings.Builder

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		if current.Len()+len(para)+2 > chunkSize && current.Len() > 0 {
			chunks = append(chunks, current.String())
			// Handle overlap: keep last portion
			if overlap > 0 {
				lastChunk := current.String()
				if len(lastChunk) > overlap {
					current.Reset()
					current.WriteString(lastChunk[len(lastChunk)-overlap:])
				} else {
					current.Reset()
				}
			} else {
				current.Reset()
			}
		}

		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	// If a single paragraph exceeds chunkSize, split by sentences
	var finalChunks []string
	for _, chunk := range chunks {
		if len(chunk) <= chunkSize {
			finalChunks = append(finalChunks, chunk)
			continue
		}
		// Split long chunks by sentence boundaries
		sentences := splitSentences(chunk)
		var sb strings.Builder
		for _, s := range sentences {
			if sb.Len()+len(s) > chunkSize && sb.Len() > 0 {
				finalChunks = append(finalChunks, sb.String())
				sb.Reset()
			}
			sb.WriteString(s)
		}
		if sb.Len() > 0 {
			finalChunks = append(finalChunks, sb.String())
		}
	}

	if len(finalChunks) == 0 && len(strings.TrimSpace(text)) > 0 {
		finalChunks = append(finalChunks, text)
	}

	return finalChunks
}

func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	for _, ch := range text {
		current.WriteRune(ch)
		if ch == '。' || ch == '！' || ch == '？' || ch == '.' || ch == '!' || ch == '?' {
			// Check if next char is space or end
			sentences = append(sentences, current.String())
			current.Reset()
		}
	}

	if current.Len() > 0 {
		sentences = append(sentences, current.String())
	}

	return sentences
}
