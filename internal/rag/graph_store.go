package rag

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// GraphStore 提供知识图谱的 SQLite 持久化
type GraphStore struct {
	mu     sync.RWMutex
	db     *sql.DB
	closed bool
}

// NewGraphStore 创建新的图谱存储（使用现有的 SQLite 连接）
func NewGraphStore(db *sql.DB) (*GraphStore, error) {
	store := &GraphStore{
		db: db,
	}

	if err := store.initSchema(); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	return store, nil
}

// initSchema 创建知识图谱相关的表
func (gs *GraphStore) initSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS knowledge_nodes (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			aliases TEXT NOT NULL DEFAULT '[]',
			description TEXT NOT NULL DEFAULT '',
			importance REAL NOT NULL DEFAULT 0.5,
			access_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			accessed_at DATETIME NOT NULL,
			source_chunks TEXT NOT NULL DEFAULT '[]',
			embedding_id TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '[]'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_type ON knowledge_nodes(type)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_name ON knowledge_nodes(name COLLATE NOCASE)`,
		`CREATE TABLE IF NOT EXISTS knowledge_edges (
			id TEXT PRIMARY KEY,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			rel_type TEXT NOT NULL,
			weight REAL NOT NULL DEFAULT 0.5,
			context TEXT NOT NULL DEFAULT '',
			evidence TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL,
			FOREIGN KEY (source_id) REFERENCES knowledge_nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES knowledge_nodes(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_source ON knowledge_edges(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_target ON knowledge_edges(target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_type ON knowledge_edges(rel_type)`,
	}

	for _, q := range queries {
		if _, err := gs.db.Exec(q); err != nil {
			return fmt.Errorf("exec graph schema: %w", err)
		}
	}

	return nil
}

// SaveNode 保存或更新节点
func (gs *GraphStore) SaveNode(node *KnowledgeNode) error {
	if node == nil || node.ID == "" {
		return fmt.Errorf("invalid node: nil or empty ID")
	}

	gs.mu.Lock()
	defer gs.mu.Unlock()

	if gs.closed {
		return fmt.Errorf("graph store is closed")
	}

	aliasesJSON, _ := json.Marshal(node.Aliases)
	chunksJSON, _ := json.Marshal(node.SourceChunks)
	tagsJSON, _ := json.Marshal(node.Tags)

	query := `INSERT OR REPLACE INTO knowledge_nodes
		(id, type, name, aliases, description, importance, access_count,
		 created_at, accessed_at, source_chunks, embedding_id, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := gs.db.Exec(query,
		node.ID, node.Type, node.Name, string(aliasesJSON), node.Description,
		node.Importance, node.AccessCount,
		node.CreatedAt, node.AccessedAt,
		string(chunksJSON), node.EmbeddingID, string(tagsJSON),
	)

	if err != nil {
		return fmt.Errorf("save node: %w", err)
	}

	return nil
}

// SaveEdge 保存或更新边
func (gs *GraphStore) SaveEdge(edge *KnowledgeEdge) error {
	if edge == nil || edge.ID == "" {
		return fmt.Errorf("invalid edge: nil or empty ID")
	}

	gs.mu.Lock()
	defer gs.mu.Unlock()

	if gs.closed {
		return fmt.Errorf("graph store is closed")
	}

	evidenceJSON, _ := json.Marshal(edge.Evidence)

	query := `INSERT OR REPLACE INTO knowledge_edges
		(id, source_id, target_id, rel_type, weight, context, evidence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := gs.db.Exec(query,
		edge.ID, edge.SourceID, edge.TargetID, edge.RelType,
		edge.Weight, edge.Context, string(evidenceJSON), edge.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("save edge: %w", err)
	}

	return nil
}

// LoadNode 加载单个节点
func (gs *GraphStore) LoadNode(id string) (*KnowledgeNode, error) {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	if gs.closed {
		return nil, fmt.Errorf("graph store is closed")
	}

	query := `SELECT id, type, name, aliases, description, importance,
		access_count, created_at, accessed_at, source_chunks, embedding_id, tags
		FROM knowledge_nodes WHERE id = ?`

	row := gs.db.QueryRow(query, id)

	var node KnowledgeNode
	var aliasesJSON, chunksJSON, tagsJSON string

	err := row.Scan(
		&node.ID, &node.Type, &node.Name, &aliasesJSON, &node.Description,
		&node.Importance, &node.AccessCount,
		&node.CreatedAt, &node.AccessedAt,
		&chunksJSON, &node.EmbeddingID, &tagsJSON,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load node: %w", err)
	}

	json.Unmarshal([]byte(aliasesJSON), &node.Aliases)
	json.Unmarshal([]byte(chunksJSON), &node.SourceChunks)
	json.Unmarshal([]byte(tagsJSON), &node.Tags)

	return &node, nil
}

// LoadAllNodes 加载所有节点
func (gs *GraphStore) LoadAllNodes() ([]*KnowledgeNode, error) {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	if gs.closed {
		return nil, fmt.Errorf("graph store is closed")
	}

	query := `SELECT id, type, name, aliases, description, importance,
		access_count, created_at, accessed_at, source_chunks, embedding_id, tags
		FROM knowledge_nodes`

	rows, err := gs.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*KnowledgeNode

	for rows.Next() {
		var node KnowledgeNode
		var aliasesJSON, chunksJSON, tagsJSON string

		err := rows.Scan(
			&node.ID, &node.Type, &node.Name, &aliasesJSON, &node.Description,
			&node.Importance, &node.AccessCount,
			&node.CreatedAt, &node.AccessedAt,
			&chunksJSON, &node.EmbeddingID, &tagsJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}

		json.Unmarshal([]byte(aliasesJSON), &node.Aliases)
		json.Unmarshal([]byte(chunksJSON), &node.SourceChunks)
		json.Unmarshal([]byte(tagsJSON), &node.Tags)

		nodes = append(nodes, &node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}

	return nodes, nil
}

// LoadAllEdges 加载所有边
func (gs *GraphStore) LoadAllEdges() ([]*KnowledgeEdge, error) {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	if gs.closed {
		return nil, fmt.Errorf("graph store is closed")
	}

	query := `SELECT id, source_id, target_id, rel_type, weight, context, evidence, created_at
		FROM knowledge_edges`

	rows, err := gs.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	var edges []*KnowledgeEdge

	for rows.Next() {
		var edge KnowledgeEdge
		var evidenceJSON string

		err := rows.Scan(
			&edge.ID, &edge.SourceID, &edge.TargetID, &edge.RelType,
			&edge.Weight, &edge.Context, &evidenceJSON, &edge.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}

		json.Unmarshal([]byte(evidenceJSON), &edge.Evidence)

		edges = append(edges, &edge)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate edges: %w", err)
	}

	return edges, nil
}

// DeleteNode 删除节点（会级联删除相关的边）
func (gs *GraphStore) DeleteNode(id string) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	if gs.closed {
		return fmt.Errorf("graph store is closed")
	}

	_, err := gs.db.Exec("DELETE FROM knowledge_nodes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}

	return nil
}

// DeleteEdge 删除边
func (gs *GraphStore) DeleteEdge(id string) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	if gs.closed {
		return fmt.Errorf("graph store is closed")
	}

	_, err := gs.db.Exec("DELETE FROM knowledge_edges WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete edge: %w", err)
	}

	return nil
}

// UpdateNodeAccess 更新节点访问统计
func (gs *GraphStore) UpdateNodeAccess(id string) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	if gs.closed {
		return fmt.Errorf("graph store is closed")
	}

	query := `UPDATE knowledge_nodes
		SET access_count = access_count + 1, accessed_at = ?
		WHERE id = ?`

	_, err := gs.db.Exec(query, time.Now(), id)
	if err != nil {
		return fmt.Errorf("update node access: %w", err)
	}

	return nil
}

// Stats 返回图谱统计信息
func (gs *GraphStore) Stats() (GraphStats, error) {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	if gs.closed {
		return GraphStats{}, fmt.Errorf("graph store is closed")
	}

	var nodeCount, edgeCount int

	err := gs.db.QueryRow("SELECT COUNT(*) FROM knowledge_nodes").Scan(&nodeCount)
	if err != nil {
		return GraphStats{}, fmt.Errorf("count nodes: %w", err)
	}

	err = gs.db.QueryRow("SELECT COUNT(*) FROM knowledge_edges").Scan(&edgeCount)
	if err != nil {
		return GraphStats{}, fmt.Errorf("count edges: %w", err)
	}

	return GraphStats{
		NodeCount: nodeCount,
		EdgeCount: edgeCount,
	}, nil
}

// Clear 清空所有图谱数据
func (gs *GraphStore) Clear() error {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	if gs.closed {
		return fmt.Errorf("graph store is closed")
	}

	// 先删除边，再删除节点（尊重外键约束）
	if _, err := gs.db.Exec("DELETE FROM knowledge_edges"); err != nil {
		return fmt.Errorf("clear edges: %w", err)
	}

	if _, err := gs.db.Exec("DELETE FROM knowledge_nodes"); err != nil {
		return fmt.Errorf("clear nodes: %w", err)
	}

	return nil
}

// Close 关闭图谱存储（不关闭底层的 db 连接）
func (gs *GraphStore) Close() error {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	gs.closed = true
	return nil
}
