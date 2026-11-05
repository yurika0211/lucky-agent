package utils

/*
ChunkFeature 描述一个可参与排序的上下文分块特征。

除 Tokens 外，其余分数字段都约定落在 [0,1] 区间内。
*/
type ChunkFeature struct {
	Relevance        float64
	Recency          float64
	RoleWeight       float64
	Importance       float64
	DuplicatePenalty float64
	Tokens           int
}

/*
ScoreWeights 定义各个特征在上下文评分中的权重占比。
*/
type ScoreWeights struct {
	Relevance        float64
	Recency          float64
	RoleWeight       float64
	Importance       float64
	DuplicatePenalty float64
}

/*
DefaultScoreWeights 返回上下文选择使用的默认权重配置。
*/
func DefaultScoreWeights() ScoreWeights {
	return ScoreWeights{
		Relevance:        0.45,
		Recency:          0.20,
		RoleWeight:       0.15,
		Importance:       0.20,
		DuplicatePenalty: 0.20,
	}
}

/*
ScoreChunk 计算单个上下文分块的加权得分。

重复惩罚项会从总分中扣除，最终结果被限制为不小于 0。
*/
func ScoreChunk(feature ChunkFeature, weights ScoreWeights) float64 {
	w := sanitizeWeights(weights)

	score := w.Relevance*clamp01(feature.Relevance) +
		w.Recency*clamp01(feature.Recency) +
		w.RoleWeight*clamp01(feature.RoleWeight) +
		w.Importance*clamp01(feature.Importance) -
		w.DuplicatePenalty*clamp01(feature.DuplicatePenalty)

	if score < 0 {
		return 0
	}
	return score
}

/*
ScorePerToken 返回按 token 成本归一化后的分数密度。

这个指标适合在上下文预算受限时用于比较不同分块的性价比。
*/
func ScorePerToken(feature ChunkFeature, weights ScoreWeights) float64 {
	tokens := feature.Tokens
	if tokens <= 0 {
		tokens = 1
	}
	return ScoreChunk(feature, weights) / float64(tokens)
}

/*
sanitizeWeights 对评分权重做归一化前的安全修正。

当传入零值配置时会回退到默认权重；所有负数权重也会被钳制为 0。
*/
func sanitizeWeights(w ScoreWeights) ScoreWeights {
	if w == (ScoreWeights{}) {
		return DefaultScoreWeights()
	}
	if w.Relevance < 0 {
		w.Relevance = 0
	}
	if w.Recency < 0 {
		w.Recency = 0
	}
	if w.RoleWeight < 0 {
		w.RoleWeight = 0
	}
	if w.Importance < 0 {
		w.Importance = 0
	}
	if w.DuplicatePenalty < 0 {
		w.DuplicatePenalty = 0
	}
	return w
}

/*
clamp01 将浮点值限制在 [0,1] 区间内。
*/
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
