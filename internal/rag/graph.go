package rag

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// KnowledgeNode 表示知识图谱中的一个节点（实体或概念）
type KnowledgeNode struct {
	ID          string            // 唯一标识
	Type        string            // person, organization, concept, location, event
	Name        string            // 实体名称
	Aliases     []string          // 别名
	Description string            // 描述
	Importance  float64           // 重要性 0.0~1.0
	AccessCount int               // 被访问次数
	CreatedAt   time.Time         // 创建时间
	AccessedAt  time.Time         // 最后访问时间
	SourceChunks []string         // 来源的文档块 ID
	EmbeddingID string            // 可选：实体的向量 ID
	Tags        []string          // 标签
}

// Weight 计算节点权重（类似 memory.Entry.Weight）
func (n *KnowledgeNode) Weight(now time.Time) float64 {
	return n.Importance * n.recencyFactor(now) * n.accessBoost()
}

func (n *KnowledgeNode) recencyFactor(now time.Time) float64 {
	halflife := 24.0 * 30 // 30 天半衰期
	age := now.Sub(n.CreatedAt).Hours()
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, age/halflife)
}

func (n *KnowledgeNode) accessBoost() float64 {
	if n.AccessCount <= 0 {
		return 1
	}
	return 1 + min(math.Log1p(float64(n.AccessCount))*0.12, 0.75)
}

// KnowledgeEdge 表示实体之间的关系
type KnowledgeEdge struct {
	ID        string    // 唯一标识
	SourceID  string    // 源节点 ID
	TargetID  string    // 目标节点 ID
	RelType   string    // works_at, located_in, part_of, related_to
	Weight    float64   // 关系强度 0.0~1.0
	Context   string    // 关系的上下文说明
	Evidence  []string  // 支撑这个关系的文档块 ID
	CreatedAt time.Time // 创建时间
}

// KnowledgeGraph 知识图谱（复用 memory.GraphIndex 思路）
type KnowledgeGraph struct {
	mu sync.RWMutex

	Nodes map[string]*KnowledgeNode // ID -> Node
	Edges map[string]*KnowledgeEdge // ID -> Edge

	// 索引（类似 memory.GraphIndex）
	Forward   map[string][]string // nodeID -> outgoing edge IDs
	Backward  map[string][]string // nodeID -> incoming edge IDs
	TypeIndex map[string][]string // type -> node IDs
	NameIndex map[string][]string // normalized name -> node IDs
	TagIndex  map[string][]string // tag -> node IDs

	// 与向量存储的桥接
	ChunkNodes map[string][]string // chunkID -> related node IDs
}

// NewKnowledgeGraph 创建新的知识图谱
func NewKnowledgeGraph() *KnowledgeGraph {
	return &KnowledgeGraph{
		Nodes:      make(map[string]*KnowledgeNode),
		Edges:      make(map[string]*KnowledgeEdge),
		Forward:    make(map[string][]string),
		Backward:   make(map[string][]string),
		TypeIndex:  make(map[string][]string),
		NameIndex:  make(map[string][]string),
		TagIndex:   make(map[string][]string),
		ChunkNodes: make(map[string][]string),
	}
}

// AddNode 添加节点到图谱
func (kg *KnowledgeGraph) AddNode(node *KnowledgeNode) error {
	if node == nil || node.ID == "" {
		return fmt.Errorf("invalid node: nil or empty ID")
	}

	kg.mu.Lock()
	defer kg.mu.Unlock()

	// 检查是否已存在
	if existing, exists := kg.Nodes[node.ID]; exists {
		// 合并信息
		existing.AccessCount++
		existing.AccessedAt = time.Now()
		if node.Description != "" {
			existing.Description = node.Description
		}
		existing.SourceChunks = append(existing.SourceChunks, node.SourceChunks...)
		return nil
	}

	kg.Nodes[node.ID] = node

	// 更新索引
	normalizedName := normalizeString(node.Name)
	kg.NameIndex[normalizedName] = append(kg.NameIndex[normalizedName], node.ID)

	if node.Type != "" {
		kg.TypeIndex[node.Type] = append(kg.TypeIndex[node.Type], node.ID)
	}

	for _, tag := range node.Tags {
		normalizedTag := normalizeString(tag)
		kg.TagIndex[normalizedTag] = append(kg.TagIndex[normalizedTag], node.ID)
	}

	for _, alias := range node.Aliases {
		normalizedAlias := normalizeString(alias)
		kg.NameIndex[normalizedAlias] = append(kg.NameIndex[normalizedAlias], node.ID)
	}

	// 更新 chunk 到 node 的映射
	for _, chunkID := range node.SourceChunks {
		kg.ChunkNodes[chunkID] = append(kg.ChunkNodes[chunkID], node.ID)
	}

	return nil
}

// AddEdge 添加边到图谱
func (kg *KnowledgeGraph) AddEdge(edge *KnowledgeEdge) error {
	if edge == nil || edge.ID == "" {
		return fmt.Errorf("invalid edge: nil or empty ID")
	}
	if edge.SourceID == "" || edge.TargetID == "" {
		return fmt.Errorf("invalid edge: empty source or target ID")
	}

	kg.mu.Lock()
	defer kg.mu.Unlock()

	// 检查节点是否存在
	if _, exists := kg.Nodes[edge.SourceID]; !exists {
		return fmt.Errorf("source node %s not found", edge.SourceID)
	}
	if _, exists := kg.Nodes[edge.TargetID]; !exists {
		return fmt.Errorf("target node %s not found", edge.TargetID)
	}

	// 检查是否已存在相同的边
	if existing, exists := kg.Edges[edge.ID]; exists {
		// 合并证据
		existing.Evidence = append(existing.Evidence, edge.Evidence...)
		// 更新权重（取平均）
		existing.Weight = (existing.Weight + edge.Weight) / 2
		return nil
	}

	kg.Edges[edge.ID] = edge

	// 更新前向和后向索引
	kg.Forward[edge.SourceID] = append(kg.Forward[edge.SourceID], edge.ID)
	kg.Backward[edge.TargetID] = append(kg.Backward[edge.TargetID], edge.ID)

	return nil
}

// GetNode 获取节点
func (kg *KnowledgeGraph) GetNode(nodeID string) (*KnowledgeNode, bool) {
	kg.mu.RLock()
	defer kg.mu.RUnlock()
	node, exists := kg.Nodes[nodeID]
	return node, exists
}

// GetEdge 获取边
func (kg *KnowledgeGraph) GetEdge(edgeID string) (*KnowledgeEdge, bool) {
	kg.mu.RLock()
	defer kg.mu.RUnlock()
	edge, exists := kg.Edges[edgeID]
	return edge, exists
}

// FindNodeByName 通过名称查找节点
func (kg *KnowledgeGraph) FindNodeByName(name string) []*KnowledgeNode {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	normalizedName := normalizeString(name)
	nodeIDs := kg.NameIndex[normalizedName]

	nodes := make([]*KnowledgeNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		if node, exists := kg.Nodes[id]; exists {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// GetNodesByChunk 获取与指定 chunk 关联的所有节点
func (kg *KnowledgeGraph) GetNodesByChunk(chunkID string) []*KnowledgeNode {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	nodeIDs := kg.ChunkNodes[chunkID]
	nodes := make([]*KnowledgeNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		if node, exists := kg.Nodes[id]; exists {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// Stats 返回图谱统计信息
func (kg *KnowledgeGraph) Stats() GraphStats {
	kg.mu.RLock()
	defer kg.mu.RUnlock()

	return GraphStats{
		NodeCount: len(kg.Nodes),
		EdgeCount: len(kg.Edges),
	}
}

// GraphStats 图谱统计信息
type GraphStats struct {
	NodeCount int
	EdgeCount int
}

// generateNodeID 生成节点 ID
func generateNodeID(nodeType, name string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s", nodeType, name)))
	return fmt.Sprintf("node_%x", h[:8])
}

// generateEdgeID 生成边 ID
func generateEdgeID(sourceID, targetID, relType string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s->%s:%s", sourceID, targetID, relType)))
	return fmt.Sprintf("edge_%x", h[:8])
}

// normalizeString 标准化字符串（用于索引）
func normalizeString(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
