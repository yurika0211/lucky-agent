package collab

import (
	"fmt"
	"strings"
	"sync"
)

// AggregationStrategy 聚合策略
type AggregationStrategy string

const (
	// AggConcat 拼接所有结果
	AggConcat AggregationStrategy = "concat"
	// AggBest 选择最佳结果（按评分）
	AggBest AggregationStrategy = "best"
	// AggVote 投票选择
	AggVote AggregationStrategy = "vote"
	// AggMerge 合并去重
	AggMerge AggregationStrategy = "merge"
	// AggSummary 摘要合并
	AggSummary AggregationStrategy = "summary"
)

// AggregationResult 聚合结果
type AggregationResult struct {
	Strategy  AggregationStrategy `json:"strategy"`
	Output    string              `json:"output"`
	Count     int                 `json:"count"`                // 参与聚合的子结果数
	BestIndex int                 `json:"best_index,omitempty"` // 最佳结果索引（AggBest）
	Votes     map[string]int      `json:"votes,omitempty"`      // 投票结果（AggVote）
	Metadata  map[string]string   `json:"metadata,omitempty"`
}

// Aggregator 结果聚合器
type Aggregator struct {
	mu      sync.RWMutex
	scorers map[AggregationStrategy]ScorerFunc
}

// ScorerFunc 评分函数 — 返回 0~1 的分数
type ScorerFunc func(result string) float64

// NewAggregator 创建聚合器
func NewAggregator() *Aggregator {
	a := &Aggregator{
		scorers: make(map[AggregationStrategy]ScorerFunc),
	}

	// 默认评分器：按长度评分（越长越详细，分数越高，上限 1.0）
	a.scorers[AggBest] = func(result string) float64 {
		length := len(result)
		if length == 0 {
			return 0
		}
		// 500 字以上满分
		if length >= 500 {
			return 1.0
		}
		return float64(length) / 500.0
	}

	return a
}

// Aggregate 聚合子任务结果
func (a *Aggregator) Aggregate(strategy AggregationStrategy, results []string) *AggregationResult {
	if len(results) == 0 {
		return &AggregationResult{
			Strategy: strategy,
			Output:   "",
			Count:    0,
		}
	}

	// 过滤空结果
	nonEmpty := make([]string, 0, len(results))
	for _, r := range results {
		if r != "" {
			nonEmpty = append(nonEmpty, r)
		}
	}

	if len(nonEmpty) == 0 {
		return &AggregationResult{
			Strategy: strategy,
			Output:   "",
			Count:    0,
		}
	}

	switch strategy {
	case AggConcat:
		return a.aggregateConcat(nonEmpty)
	case AggBest:
		return a.aggregateBest(nonEmpty)
	case AggVote:
		return a.aggregateVote(nonEmpty)
	case AggMerge:
		return a.aggregateMerge(nonEmpty)
	case AggSummary:
		return a.aggregateSummary(nonEmpty)
	default:
		return a.aggregateConcat(nonEmpty)
	}
}

// aggregateConcat 拼接
func (a *Aggregator) aggregateConcat(results []string) *AggregationResult {
	return &AggregationResult{
		Strategy: AggConcat,
		Output:   strings.Join(results, "\n\n---\n\n"),
		Count:    len(results),
	}
}

// aggregateBest 选择最佳
func (a *Aggregator) aggregateBest(results []string) *AggregationResult {
	a.mu.RLock()
	scorer, ok := a.scorers[AggBest]
	a.mu.RUnlock()

	if !ok {
		// 降级到拼接
		return a.aggregateConcat(results)
	}

	bestIdx := 0
	bestScore := -1.0

	for i, r := range results {
		score := scorer(r)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	return &AggregationResult{
		Strategy:  AggBest,
		Output:    results[bestIdx],
		Count:     len(results),
		BestIndex: bestIdx,
		Metadata: map[string]string{
			"best_score": fmt.Sprintf("%.4f", bestScore),
		},
	}
}

// aggregateVote 投票 — 按结果相似度分组，最多票的胜出
func (a *Aggregator) aggregateVote(results []string) *AggregationResult {
	// 简化投票：按结果前 100 字符分组
	groups := make(map[string][]int) // prefix -> indices
	for i, r := range results {
		prefix := r
		if len(prefix) > 100 {
			prefix = prefix[:100]
		}
		groups[prefix] = append(groups[prefix], i)
	}

	// 找最大组
	bestPrefix := ""
	bestCount := 0
	for prefix, indices := range groups {
		if len(indices) > bestCount {
			bestCount = len(indices)
			bestPrefix = prefix
		}
	}

	// 构建投票结果
	votes := make(map[string]int)
	for _, indices := range groups {
		votes[fmt.Sprintf("group_%d", indices[0])] = len(indices)
	}

	// 使用最大组的第一个结果
	output := ""
	if indices, ok := groups[bestPrefix]; ok && len(indices) > 0 {
		output = results[indices[0]]
	}

	return &AggregationResult{
		Strategy: AggVote,
		Output:   output,
		Count:    len(results),
		Votes:    votes,
	}
}

// aggregateMerge 合并去重
func (a *Aggregator) aggregateMerge(results []string) *AggregationResult {
	seen := make(map[string]bool)
	var unique []string

	for _, r := range results {
		// 按行去重
		lines := strings.Split(r, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !seen[line] {
				seen[line] = true
			}
		}
	}

	// 重建去重后的结果
	for _, r := range results {
		lines := strings.Split(r, "\n")
		var merged []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && seen[trimmed] {
				merged = append(merged, line)
				delete(seen, trimmed) // 只保留第一次出现
			}
		}
		if len(merged) > 0 {
			unique = append(unique, strings.Join(merged, "\n"))
		}
	}

	return &AggregationResult{
		Strategy: AggMerge,
		Output:   strings.Join(unique, "\n"),
		Count:    len(results),
		Metadata: map[string]string{
			"unique_lines": fmt.Sprintf("%d", len(seen)+countNonEmpty(unique)),
		},
	}
}

// aggregateSummary 摘要合并 — 每个结果取前 N 字符
func (a *Aggregator) aggregateSummary(results []string) *AggregationResult {
	const maxPerResult = 200

	var parts []string
	for i, r := range results {
		summary := r
		if len(summary) > maxPerResult {
			summary = summary[:maxPerResult] + "..."
		}
		parts = append(parts, fmt.Sprintf("[Agent %d] %s", i+1, summary))
	}

	return &AggregationResult{
		Strategy: AggSummary,
		Output:   strings.Join(parts, "\n\n"),
		Count:    len(results),
	}
}

// countNonEmpty 计算非空行数
func countNonEmpty(lines []string) int {
	count := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			count++
		}
	}
	return count
}
