# LuckyHarness Graph RAG Design

## Goal

LuckyHarness already has a working vector RAG layer in `internal/rag`: documents are chunked, embedded, stored in a vector backend, and retrieved by semantic similarity. Graph RAG should extend this layer with document structure, entity links, code symbols, wikilinks, and multi-hop evidence paths.

The first implementation should live in a new package, `internal/graphrag`, and should not replace or heavily rewrite `internal/rag`.

## Current Baseline

The current RAG stack is vector-first:

- `RAGManager` owns an indexer, retriever, embedder, and vector store.
- `SQLiteStore` persists `vectors`, `documents`, `chunks`, and `store_meta`.
- `Search` embeds the query, returns top chunks by vector similarity, and builds context from those chunks.
- Tooling exposes `rag_search` and `rag_index`.

This works for direct semantic matches, but it cannot follow relationships such as:

- a chunk mentions an entity that is explained elsewhere,
- a code symbol appears in multiple files,
- an Obsidian-style wikilink connects notes,
- neighboring chunks are needed to understand a retrieved chunk,
- multiple weak signals form a stronger answer through graph paths.

## Package Boundary

Add:

```text
internal/graphrag/
```

The new package owns:

- graph node and edge models,
- graph persistence,
- graph indexing from existing RAG documents and chunks,
- graph expansion from vector retrieval seeds,
- hybrid ranking and context assembly,
- evidence path formatting.

It should not own:

- embedding provider selection,
- document chunking,
- core vector search,
- existing RAG CLI/API behavior,
- memory vault semantics.

The intended dependency direction is:

```text
internal/rag        -> no dependency on graphrag
internal/graphrag   -> may import internal/rag
agent/tool/server   -> may use both
```

This keeps the current vector RAG stable while Graph RAG matures.

## Data Model

### Nodes

```go
type NodeKind string

const (
    NodeDocument NodeKind = "document"
    NodeChunk    NodeKind = "chunk"
    NodeEntity   NodeKind = "entity"
    NodeTopic    NodeKind = "topic"
    NodeSymbol   NodeKind = "symbol"
)

type Node struct {
    ID        string
    Kind      NodeKind
    Label     string
    SourceID  string
    DocID     string
    ChunkID   string
    Metadata  map[string]string
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

Initial node types:

- `document`: one node per indexed document.
- `chunk`: one node per RAG chunk.
- `entity`: extracted names, concepts, products, systems, or people.
- `topic`: headings, tags, or major concepts.
- `symbol`: code symbols such as Go packages, functions, types, and methods.

### Edges

```go
type EdgeKind string

const (
    EdgeContains  EdgeKind = "contains"
    EdgeMentions  EdgeKind = "mentions"
    EdgeRelatedTo EdgeKind = "related_to"
    EdgeNext      EdgeKind = "next"
    EdgeCites     EdgeKind = "cites"
    EdgeSameAs    EdgeKind = "same_as"
)

type Edge struct {
    ID        string
    FromID    string
    ToID      string
    Kind      EdgeKind
    Weight    float64
    Metadata  map[string]string
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

Initial edge types:

- `document -> chunk`: `contains`
- `chunk -> chunk`: `next`
- `chunk -> entity/topic/symbol`: `mentions`
- `entity/topic/symbol -> entity/topic/symbol`: `related_to`
- `entity -> entity`: `same_as`
- `chunk/document -> external reference`: `cites`

## Store Interface

```go
type Store interface {
    UpsertNode(ctx context.Context, node Node) error
    UpsertEdge(ctx context.Context, edge Edge) error
    GetNode(ctx context.Context, id string) (Node, bool, error)
    SearchNodes(ctx context.Context, query NodeQuery) ([]ScoredNode, error)
    Neighbors(ctx context.Context, id string, opts WalkOptions) ([]ScoredNode, error)
    DeleteDocument(ctx context.Context, docID string) error
    Stats(ctx context.Context) (Stats, error)
    Close() error
}
```

First backend:

```text
SQLiteGraphStore
```

Use SQLite because the existing RAG store already uses SQLite and document/chunk IDs can be reused without introducing another database.

## SQLite Schema

Graph tables should be independent from the current vector tables:

```sql
CREATE TABLE IF NOT EXISTS graph_nodes (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    source_id TEXT NOT NULL DEFAULT '',
    doc_id TEXT NOT NULL DEFAULT '',
    chunk_id TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_graph_nodes_kind ON graph_nodes(kind);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_doc_id ON graph_nodes(doc_id);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_chunk_id ON graph_nodes(chunk_id);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_label ON graph_nodes(label);

CREATE TABLE IF NOT EXISTS graph_edges (
    id TEXT PRIMARY KEY,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_graph_edges_from ON graph_edges(from_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_to ON graph_edges(to_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_kind ON graph_edges(kind);

CREATE TABLE IF NOT EXISTS graph_aliases (
    alias TEXT NOT NULL,
    node_id TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    PRIMARY KEY (alias, node_id)
);

CREATE INDEX IF NOT EXISTS idx_graph_aliases_alias ON graph_aliases(alias);
```

Later phases can add:

- `graph_communities`
- `graph_summaries`
- `graph_extraction_runs`

## Indexing Pipeline

Graph indexing should reuse `rag.Document` and `rag.Chunk` from the existing indexer.

Proposed pipeline:

1. Create or update a `document` node.
2. Create one `chunk` node for each existing RAG chunk.
3. Add `contains` edges from document to chunks.
4. Add `next` edges between neighboring chunks.
5. Extract deterministic graph signals:
   - Markdown headings as `topic`.
   - Obsidian wikilinks `[[target]]` as `entity` or `topic`.
   - Markdown links as `cites`.
   - File path segments as weak `topic` nodes.
   - Go symbols from `package`, `func`, `type`, `interface`, and method declarations.
6. Add `mentions` edges from chunks to extracted nodes.
7. Add `same_as` or alias records for normalized labels.

MVP extraction should be deterministic and local. LLM extraction can be added later behind an `Extractor` interface.

```go
type Extractor interface {
    Extract(ctx context.Context, doc *rag.Document, chunks []*rag.Chunk) (GraphDelta, error)
}
```

Suggested first extractors:

- `MarkdownExtractor`
- `WikiLinkExtractor`
- `GoSymbolExtractor`
- `PathTopicExtractor`

## Retrieval Pipeline

Graph RAG should begin with vector search, then expand.

```text
query
  -> vector RAG topK chunks
  -> seed graph nodes from chunk IDs
  -> graph walk
  -> candidate chunk expansion
  -> hybrid rerank
  -> context with evidence paths
```

### Search Modes

```go
type SearchMode string

const (
    SearchVectorOnly SearchMode = "vector"
    SearchGraphOnly  SearchMode = "graph"
    SearchHybrid     SearchMode = "hybrid"
)
```

MVP should implement `hybrid`.

### Walk Options

```go
type WalkOptions struct {
    MaxDepth       int
    MaxNodes       int
    AllowedEdges   []EdgeKind
    NodeKinds      []NodeKind
    MinEdgeWeight  float64
}
```

Default walk:

- depth: 2
- max nodes: 64
- allowed edges: `mentions`, `related_to`, `same_as`, `contains`, `next`
- neighbor chunks included only when they improve context coverage.

### Ranking

Hybrid score:

```text
score =
  vector_score * 0.55 +
  graph_score  * 0.30 +
  structure_score * 0.10 +
  freshness_score * 0.05
```

Graph score should consider:

- distance from seed,
- edge weight,
- node kind,
- number of independent paths,
- whether the node leads back to a chunk.

Document/chunk result should include evidence:

```go
type Result struct {
    ChunkID string
    DocID   string
    Content string
    Score   float64
    Paths   []Path
    Why     string
}
```

Example `Why`:

```text
Matched vector seed chunk, then expanded through entity "OpenAI Responses API" to a neighboring implementation note.
```

## Context Assembly

Graph RAG context should be explicit about why each passage is present.

Example format:

```text
## Retrieved Knowledge (Graph RAG)

1. Source: docs/ops/LUCKYHARNESS_API.md
   Score: 0.82
   Path: chunk:api-overview -> entity:Responses API -> chunk:tool-calls
   Content:
   ...

2. Source: internal/provider/openai_stream.go
   Score: 0.74
   Path: chunk:tool-calls -> symbol:parseStreamingToolCall
   Content:
   ...
```

This makes Graph RAG debuggable and helps the agent avoid treating graph-expanded material as magic context.

## Public API Shape

Start with a Go API:

```go
type Manager struct {
    rag   *rag.RAGManager
    store Store
}

func NewManager(ragMgr *rag.RAGManager, store Store, cfg Config) *Manager
func (m *Manager) IndexDocument(ctx context.Context, docID string) error
func (m *Manager) IndexAll(ctx context.Context) error
func (m *Manager) Search(ctx context.Context, query string, opts SearchOptions) ([]Result, error)
func (m *Manager) BuildContext(results []Result) string
```

Tool layer later:

- `graphrag_search`
- `graphrag_index`
- `graphrag_stats`

After validation, existing `rag_search` can accept:

```json
{
  "query": "deployment auth flow",
  "top_k": 5,
  "mode": "hybrid"
}
```

## Agent Integration

Initial integration should be opt-in:

- Config flag: `rag.graph.enabled`
- Tool: `graphrag_search`
- CLI: `/rag graph search <query>` or separate `/graphrag search <query>`

Only after behavior is stable should `context_planner.buildRAGMessage` use Graph RAG automatically.

Final intended context planner behavior:

1. Use memory for stable user/project facts.
2. Use Graph RAG for indexed documents with relationship-heavy questions.
3. Fall back to vector RAG when graph index is empty or disabled.
4. Use direct local files when current workspace state is the source of truth.

## Implementation Phases

### Phase 1: Package And Store

- Add `internal/graphrag`.
- Define `Node`, `Edge`, `Store`, `Manager`, and config structs.
- Implement `SQLiteGraphStore`.
- Add tests for node/edge CRUD, aliases, neighbors, and delete-by-document.

### Phase 2: Deterministic Indexing

- Add graph indexer that reads existing `rag.Document` and chunks.
- Add document, chunk, contains, and next edges.
- Add Markdown heading, wikilink, path topic, and Go symbol extractors.
- Add tests with small Markdown and Go files.

### Phase 3: Hybrid Retrieval

- Use existing vector RAG results as seeds.
- Walk graph from seed chunks.
- Collect related chunks.
- Rerank with vector and graph scores.
- Build context with evidence paths.

### Phase 4: Tool And CLI

- Add `graphrag_search`.
- Add `graphrag_index`.
- Add stats/debug output:
  - node count,
  - edge count,
  - top node kinds,
  - orphan chunks,
  - last graph index time.

### Phase 5: Advanced Extraction

- Add optional LLM extractor.
- Add community detection and summaries.
- Add conflict/duplicate entity merging.
- Add incremental reindexing based on file hash or chunk hash.

## Risks

- Graph noise can hurt relevance if entity extraction is too broad.
- SQLite graph traversal may become slow without careful indexes.
- Automatic Graph RAG in the agent can add irrelevant context if enabled too early.
- LLM extraction can introduce hallucinated entities unless stored with confidence and source spans.

## Recommended MVP Rule

Graph RAG should not try to be a smarter reader in v1. It should do one thing well:

> When vector search finds a useful chunk, Graph RAG should follow reliable local relationships to recover nearby structure, linked concepts, code symbols, and supporting chunks.

That gives LuckyHarness better recall without destabilizing the current RAG path.
