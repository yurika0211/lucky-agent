package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// EntityExtractor 实体和关系提取器（使用 LLM）
type EntityExtractor struct {
	llm LLMProvider
}

// LLMProvider 定义 LLM 提供者接口
type LLMProvider interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// NewEntityExtractor 创建实体提取器
func NewEntityExtractor(llm LLMProvider) *EntityExtractor {
	return &EntityExtractor{llm: llm}
}

// ExtractResult 提取结果
type ExtractResult struct {
	Entities  []ExtractedEntity
	Relations []ExtractedRelation
}

// ExtractedEntity 提取的实体
type ExtractedEntity struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases,omitempty"`
}

// ExtractedRelation 提取的关系
type ExtractedRelation struct {
	Source  string `json:"source"`
	Target  string `json:"target"`
	Type    string `json:"type"`
	Context string `json:"context"`
}

// ExtractEntitiesAndRelations 从文档块中提取实体和关系
func (e *EntityExtractor) ExtractEntitiesAndRelations(ctx context.Context, chunk *Chunk) (*ExtractResult, error) {
	if e.llm == nil {
		return nil, fmt.Errorf("LLM provider is nil")
	}
	if chunk == nil || chunk.Content == "" {
		return &ExtractResult{}, nil
	}

	// 限制输入长度
	content := chunk.Content
	if len(content) > 2000 {
		content = content[:2000]
	}

	prompt := buildExtractionPrompt(content)

	response, err := e.llm.Complete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	// 解析 JSON 响应
	result, err := parseExtractionResponse(response)
	if err != nil {
		return nil, fmt.Errorf("parse response failed: %w", err)
	}

	return result, nil
}

// ConvertToGraphNodes 将提取结果转换为图节点
func (e *EntityExtractor) ConvertToGraphNodes(result *ExtractResult, chunkID string) ([]*KnowledgeNode, []*KnowledgeEdge) {
	now := time.Now()

	// 创建节点映射（name -> node）
	nodeMap := make(map[string]*KnowledgeNode)
	nodes := make([]*KnowledgeNode, 0, len(result.Entities))

	for _, ent := range result.Entities {
		if ent.Name == "" {
			continue
		}

		node := &KnowledgeNode{
			ID:           generateNodeID(ent.Type, ent.Name),
			Type:         normalizeEntityType(ent.Type),
			Name:         ent.Name,
			Aliases:      ent.Aliases,
			Description:  ent.Description,
			Importance:   0.5, // 默认重要性
			AccessCount:  0,
			CreatedAt:    now,
			AccessedAt:   now,
			SourceChunks: []string{chunkID},
			Tags:         []string{},
		}

		nodes = append(nodes, node)
		nodeMap[ent.Name] = node
	}

	// 创建边
	edges := make([]*KnowledgeEdge, 0, len(result.Relations))
	for _, rel := range result.Relations {
		sourceNode := nodeMap[rel.Source]
		targetNode := nodeMap[rel.Target]

		if sourceNode == nil || targetNode == nil {
			continue // 跳过找不到节点的关系
		}

		edge := &KnowledgeEdge{
			ID:        generateEdgeID(sourceNode.ID, targetNode.ID, rel.Type),
			SourceID:  sourceNode.ID,
			TargetID:  targetNode.ID,
			RelType:   normalizeRelationType(rel.Type),
			Weight:    0.7, // 默认权重
			Context:   rel.Context,
			Evidence:  []string{chunkID},
			CreatedAt: now,
		}

		edges = append(edges, edge)
	}

	return nodes, edges
}

// buildExtractionPrompt 构建提取 prompt
func buildExtractionPrompt(content string) string {
	return fmt.Sprintf(`从以下文本中提取实体和关系。

实体类型包括：
- person: 人物
- organization: 组织、公司
- location: 地点、城市
- concept: 概念、技术
- event: 事件

关系类型包括：
- works_at: 在...工作
- located_in: 位于
- part_of: 是...的一部分
- related_to: 与...相关
- mention: 提到

文本：
%s

请以 JSON 格式返回，格式如下：
{
  "entities": [
    {"name": "实体名称", "type": "类型", "description": "简短描述", "aliases": ["别名1", "别名2"]}
  ],
  "relations": [
    {"source": "源实体名称", "target": "目标实体名称", "type": "关系类型", "context": "关系说明"}
  ]
}

只返回 JSON，不要其他解释。`, content)
}

// parseExtractionResponse 解析提取响应
func parseExtractionResponse(response string) (*ExtractResult, error) {
	// 清理响应（去掉可能的 markdown 代码块标记）
	response = strings.TrimSpace(response)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	var result ExtractResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w", err)
	}

	return &result, nil
}

// normalizeEntityType 标准化实体类型
func normalizeEntityType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "person", "人物", "人":
		return "person"
	case "organization", "org", "company", "组织", "公司", "机构":
		return "organization"
	case "location", "place", "city", "地点", "城市", "位置":
		return "location"
	case "concept", "technology", "tech", "概念", "技术":
		return "concept"
	case "event", "事件":
		return "event"
	default:
		return "concept" // 默认为概念
	}
}

// normalizeRelationType 标准化关系类型
func normalizeRelationType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "works_at", "work_at", "employed_by", "工作于", "就职于":
		return "works_at"
	case "located_in", "in", "at", "位于":
		return "located_in"
	case "part_of", "belong_to", "属于", "是...的一部分":
		return "part_of"
	case "related_to", "relate", "相关", "关联":
		return "related_to"
	case "mention", "mentions", "提到", "提及":
		return "mention"
	default:
		return "related_to" // 默认为相关
	}
}
