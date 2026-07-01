package rag

import (
	"sort"
	"strings"
	"time"
)

// GraphActivationOptions 控制知识图谱的激活扩散
type GraphActivationOptions struct {
	MaxDepth        int                // 最大遍历深度（1-3跳）
	MaxGraphBoost   float64            // 图扩散的最大加成
	MaxSeeds        int                // 初始激活种子数
	RelationWeights map[string]float64 // 不同关系类型的权重
	IncludeChunks   bool               // 是否包含关联的文档块
	UpdateAccess    bool               // 是否更新访问统计
}

// DefaultGraphActivationOptions 返回默认的图激活选项
func DefaultGraphActivationOptions() GraphActivationOptions {
	return GraphActivationOptions{
		MaxDepth:      2,
		MaxGraphBoost: 0.6,
		MaxSeeds:      10,
		RelationWeights: map[string]float64{
			"works_at":    0.7,
			"located_in":  0.6,
			"part_of":     0.5,
			"related_to":  0.3,
			"mention":     0.2,
		},
		IncludeChunks: true,
		UpdateAccess:  true,
	}
}

// NodeActivationScore 节点激活分数
type NodeActivationScore struct {
	NodeID        string
	Node          *KnowledgeNode
	Score         float64
	Components    NodeActivationComponents
	Paths         []ActivationPath // 激活路径
	DirectScore   float64          // 直接匹配分数
	RelatedChunks []string         // 关联的文档块
}

// NodeActivationComponents 激活分数组成
type NodeActivationComponents struct {
	Lexical    float64 // 词法匹配
	TypeMatch  float64 // 类型匹配
	Aliases    float64 // 别名匹配
	Tags       float64 // 标签匹配
	Importance float64 // 重要性
	Recency    float64 // 时效性
	Access     float64 // 访问频率加成
	GraphBoost float64 // 图扩散加成
}

// MatchScore 返回直接匹配分数
func (c NodeActivationComponents) MatchScore() float64 {
	return c.Lexical + c.TypeMatch + c.Aliases + c.Tags
}

// ActivationPath 激活路径（记录图扩散的路径）
type ActivationPath struct {
	FromID  string  // 源节点 ID
	ToID    string  // 目标节点 ID
	Via     string  // 通过什么关系
	RelType string  // 关系类型
	Weight  float64 // 路径权重
}

// ActivateGraph 从查询激活相关的知识节点（核心算法）
// 移植自 memory.Activate
func (kg *KnowledgeGraph) ActivateGraph(query string, opts GraphActivationOptions) []NodeActivationScore {
	if kg == nil {
		return nil
	}

	kg.mu.RLock()
	defer kg.mu.RUnlock()

	queryLower := strings.ToLower(strings.TrimSpace(query))
	queryTerms := extractQueryTerms(queryLower)
	now := time.Now()

	// 1. 直接激活：匹配查询的节点
	scores := make(map[string]*NodeActivationScore)
	seeds := make([]string, 0, opts.MaxSeeds)

	for id, node := range kg.Nodes {
		components := matchNodeActivation(node, queryLower, queryTerms)
		matchScore := components.MatchScore()
		if matchScore <= 0 {
			continue
		}

		// 计算权重（类似 memory.Entry.Weight）
		components.Importance = node.Importance
		components.Recency = node.recencyFactor(now)
		components.Access = node.accessBoost()

		total := matchScore * node.Weight(now)
		scores[id] = &NodeActivationScore{
			NodeID:        id,
			Node:          node,
			Score:         total,
			Components:    components,
			DirectScore:   total,
			RelatedChunks: node.SourceChunks,
		}

		// 收集激活种子
		if len(seeds) < opts.MaxSeeds {
			seeds = insertNodeActivationSeed(seeds, id, scores, opts.MaxSeeds)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	// 2. 激活扩散：从种子节点向外扩散
	kg.spreadActivationLocked(scores, seeds, now, opts)

	// 3. 更新访问统计（如果需要）
	if opts.UpdateAccess {
		for _, score := range scores {
			if node := kg.Nodes[score.NodeID]; node != nil {
				node.AccessCount++
				node.AccessedAt = now
			}
		}
	}

	// 4. 排序并返回
	return sortNodeActivationScores(scores)
}

// spreadActivationLocked 激活扩散（核心算法）
// 移植自 memory.spreadActivationGraphLocked
func (kg *KnowledgeGraph) spreadActivationLocked(
	scores map[string]*NodeActivationScore,
	seeds []string,
	now time.Time,
	opts GraphActivationOptions,
) {
	visited := make(map[string]int) // nodeID -> depth

	// BFS 广度优先遍历
	queue := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		queue = append(queue, seed)
		visited[seed] = 0
	}

	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]

		currentDepth := visited[currentID]
		if currentDepth >= opts.MaxDepth {
			continue
		}

		sourceScore := scores[currentID]
		if sourceScore == nil {
			continue
		}

		// 遍历所有出边（前向激活）
		for _, edgeID := range kg.Forward[currentID] {
			edge := kg.Edges[edgeID]
			if edge == nil {
				continue
			}

			targetID := edge.TargetID
			targetNode := kg.Nodes[targetID]
			if targetNode == nil {
				continue
			}

			// 计算激活传播的强度
			// boost = 源节点分数 × 关系权重 × 边权重 × 目标节点权重
			relWeight := opts.RelationWeights[edge.RelType]
			if relWeight <= 0 {
				relWeight = 0.3 // 默认权重
			}
			boost := sourceScore.Score * relWeight * edge.Weight * targetNode.Weight(now)

			if boost <= 0 {
				continue
			}

			// 更新或创建目标节点的激活分数
			kg.addActivationBoostLocked(scores, sourceScore, targetID, targetNode, currentID, edge, boost, now, opts)

			// 将目标节点加入队列继续扩散
			if _, seen := visited[targetID]; !seen {
				visited[targetID] = currentDepth + 1
				queue = append(queue, targetID)
			}
		}

		// 遍历入边（反向激活，权重稍低）
		for _, edgeID := range kg.Backward[currentID] {
			edge := kg.Edges[edgeID]
			if edge == nil {
				continue
			}

			sourceID := edge.SourceID
			sourceNode := kg.Nodes[sourceID]
			if sourceNode == nil {
				continue
			}

			// 反向激活的权重稍低（0.5倍）
			relWeight := opts.RelationWeights[edge.RelType] * 0.5
			boost := sourceScore.Score * relWeight * edge.Weight * sourceNode.Weight(now)

			if boost <= 0 {
				continue
			}

			kg.addActivationBoostLocked(scores, sourceScore, sourceID, sourceNode, currentID, edge, boost, now, opts)

			if _, seen := visited[sourceID]; !seen {
				visited[sourceID] = currentDepth + 1
				queue = append(queue, sourceID)
			}
		}
	}
}

// addActivationBoostLocked 添加激活加成
func (kg *KnowledgeGraph) addActivationBoostLocked(
	scores map[string]*NodeActivationScore,
	sourceScore *NodeActivationScore,
	targetID string,
	targetNode *KnowledgeNode,
	sourceID string,
	edge *KnowledgeEdge,
	boost float64,
	now time.Time,
	opts GraphActivationOptions,
) {
	targetScore := scores[targetID]
	if targetScore == nil {
		targetScore = &NodeActivationScore{
			NodeID: targetID,
			Node:   targetNode,
			Components: NodeActivationComponents{
				Importance: targetNode.Importance,
				Recency:    targetNode.recencyFactor(now),
				Access:     targetNode.accessBoost(),
			},
			RelatedChunks: targetNode.SourceChunks,
		}
		scores[targetID] = targetScore
	}

	// 应用图扩散加成上限
	if opts.MaxGraphBoost > 0 {
		remaining := opts.MaxGraphBoost - targetScore.Components.GraphBoost
		if remaining <= 0 {
			return
		}
		if boost > remaining {
			boost = remaining
		}
	}

	targetScore.Score += boost
	targetScore.Components.GraphBoost += boost

	// 记录激活路径
	relWeight := opts.RelationWeights[edge.RelType]
	if relWeight <= 0 {
		relWeight = 0.3
	}
	targetScore.Paths = append(targetScore.Paths, ActivationPath{
		FromID:  sourceID,
		ToID:    targetID,
		Via:     edge.Context,
		RelType: edge.RelType,
		Weight:  relWeight,
	})
}

// matchNodeActivation 匹配节点激活（类似 memory.matchActivation）
func matchNodeActivation(node *KnowledgeNode, queryLower string, queryTerms []string) NodeActivationComponents {
	if node == nil {
		return NodeActivationComponents{}
	}

	nameLower := strings.ToLower(node.Name)
	descLower := strings.ToLower(node.Description)
	typeLower := strings.ToLower(node.Type)

	components := NodeActivationComponents{}

	// 1. 名称匹配
	if queryLower != "" && strings.Contains(nameLower, queryLower) {
		components.Lexical = 1.0
		if nameLower == queryLower {
			components.Lexical = 2.0 // 完全匹配加倍
		}
	}

	// 2. 描述匹配
	if queryLower != "" && strings.Contains(descLower, queryLower) {
		components.Lexical += 0.5
	}

	// 3. 类型匹配
	if queryLower != "" && strings.Contains(typeLower, queryLower) {
		components.TypeMatch = 0.5
	}

	// 4. 词项匹配
	termHits := 0
	for _, term := range queryTerms {
		if term == "" {
			continue
		}
		if strings.Contains(nameLower, term) {
			components.Lexical += 0.3
			termHits++
		} else if strings.Contains(descLower, term) {
			components.Lexical += 0.15
			termHits++
		} else if strings.Contains(typeLower, term) {
			components.TypeMatch += 0.12
			termHits++
		}
	}
	if termHits >= 2 {
		components.Lexical += 0.25 // 多词匹配加成
	}

	// 5. 别名匹配
	for _, alias := range node.Aliases {
		aliasLower := strings.ToLower(alias)
		if queryLower != "" && (strings.Contains(aliasLower, queryLower) || strings.Contains(queryLower, aliasLower)) {
			components.Aliases += 0.6
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(aliasLower, term) || strings.Contains(term, aliasLower) {
				components.Aliases += 0.2
				break
			}
		}
	}

	// 6. 标签匹配
	for _, tag := range node.Tags {
		tagLower := strings.ToLower(tag)
		if queryLower != "" && strings.Contains(tagLower, queryLower) {
			components.Tags += 0.4
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(tagLower, term) {
				components.Tags += 0.15
				break
			}
		}
	}

	return components
}

// sortNodeActivationScores 排序激活分数
func sortNodeActivationScores(scores map[string]*NodeActivationScore) []NodeActivationScore {
	result := make([]NodeActivationScore, 0, len(scores))
	for _, score := range scores {
		result = append(result, *score)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score == result[j].Score {
			return result[i].Node.CreatedAt.After(result[j].Node.CreatedAt)
		}
		return result[i].Score > result[j].Score
	})

	return result
}

// insertNodeActivationSeed 插入激活种子（保持有序）
func insertNodeActivationSeed(seeds []string, candidate string, scores map[string]*NodeActivationScore, limit int) []string {
	if candidate == "" || limit <= 0 {
		return seeds
	}

	insertAt := len(seeds)
	for i, seed := range seeds {
		if nodeActivationSeedBetter(candidate, seed, scores) {
			insertAt = i
			break
		}
	}

	if len(seeds) >= limit && insertAt >= limit {
		return seeds
	}

	seeds = append(seeds, "")
	copy(seeds[insertAt+1:], seeds[insertAt:])
	seeds[insertAt] = candidate

	if len(seeds) > limit {
		seeds = seeds[:limit]
	}

	return seeds
}

// nodeActivationSeedBetter 判断候选种子是否更好
func nodeActivationSeedBetter(leftID, rightID string, scores map[string]*NodeActivationScore) bool {
	left := scores[leftID]
	right := scores[rightID]
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	if left.DirectScore == right.DirectScore {
		return left.Node.CreatedAt.After(right.Node.CreatedAt)
	}
	return left.DirectScore > right.DirectScore
}

// extractQueryTerms 提取查询词项
func extractQueryTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}

	// 简单分词：按空格分割
	terms := strings.Fields(query)
	result := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if len(term) >= 2 { // 过滤太短的词
			result = append(result, term)
		}
	}
	return result
}
