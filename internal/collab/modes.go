package collab

// CollabMode 协作模式
type CollabMode string

const (
	// ModeAuto lets the runtime planner choose a collaboration mode.
	ModeAuto CollabMode = "auto"
	// ModePipeline 串行流水线 — 前一步输出作为后一步输入
	ModePipeline CollabMode = "pipeline"
	// ModeParallel 并行执行 — 所有子任务同时执行，结果聚合
	ModeParallel CollabMode = "parallel"
	// ModeDebate 辩论模式 — Agent 轮流发言，最后投票决定
	ModeDebate CollabMode = "debate"
)

// ModeConfig 协作模式配置
type ModeConfig struct {
	Mode             CollabMode          `json:"mode" yaml:"mode"`
	DebateRounds     int                 `json:"debate_rounds,omitempty" yaml:"debate_rounds,omitempty"`
	Aggregation      AggregationStrategy `json:"aggregation,omitempty" yaml:"aggregation,omitempty"`
	FailFast         bool                `json:"fail_fast,omitempty" yaml:"fail_fast,omitempty"`                 // Pipeline: 任一失败即终止
	MaxConcurrent    int                 `json:"max_concurrent,omitempty" yaml:"max_concurrent,omitempty"`       // Parallel: 最大并发数
	RequireConsensus bool                `json:"require_consensus,omitempty" yaml:"require_consensus,omitempty"` // Debate: 是否需要一致
}

// DefaultModeConfig 默认模式配置
func DefaultModeConfig(mode CollabMode) ModeConfig {
	cfg := ModeConfig{
		Mode:        mode,
		Aggregation: AggConcat,
		FailFast:    true,
	}

	switch mode {
	case ModePipeline:
		cfg.Aggregation = AggConcat
		cfg.FailFast = true
	case ModeParallel:
		cfg.Aggregation = AggBest
		cfg.MaxConcurrent = 5
	case ModeDebate:
		cfg.DebateRounds = 2
		cfg.Aggregation = AggVote
		cfg.RequireConsensus = false
	}

	return cfg
}

// Validate 验证模式配置
func (c ModeConfig) Validate() error {
	switch c.Mode {
	case ModeAuto, ModePipeline, ModeParallel, ModeDebate:
		// valid
	default:
		return ErrInvalidMode
	}

	if c.Mode == ModeDebate && c.DebateRounds < 1 {
		return ErrInvalidDebateRounds
	}

	if c.Mode == ModeParallel && c.MaxConcurrent < 1 {
		return ErrInvalidMaxConcurrent
	}

	return nil
}

// ModeDescription 返回模式描述
func ModeDescription(mode CollabMode) string {
	switch mode {
	case ModeAuto:
		return "Auto: Runtime planner chooses the collaboration mode"
	case ModePipeline:
		return "Pipeline: Agents execute sequentially, each receiving the previous agent's output"
	case ModeParallel:
		return "Parallel: Agents execute concurrently, results are aggregated"
	case ModeDebate:
		return "Debate: Agents take turns presenting positions, then vote on the best answer"
	default:
		return "Unknown collaboration mode"
	}
}

// AllModes 返回所有可用模式
func AllModes() []CollabMode {
	return []CollabMode{ModePipeline, ModeParallel, ModeDebate}
}

// ParseMode 解析模式字符串
func ParseMode(s string) (CollabMode, error) {
	switch s {
	case "", "auto", "Auto", "AUTO":
		return ModeAuto, nil
	case "pipeline", "Pipeline", "PIPELINE":
		return ModePipeline, nil
	case "parallel", "Parallel", "PARALLEL":
		return ModeParallel, nil
	case "debate", "Debate", "DEBATE":
		return ModeDebate, nil
	default:
		return "", ErrInvalidMode
	}
}

// ParseAggregationStrategy 解析聚合策略
func ParseAggregationStrategy(s string) (AggregationStrategy, error) {
	switch s {
	case "concat":
		return AggConcat, nil
	case "best":
		return AggBest, nil
	case "vote":
		return AggVote, nil
	case "merge":
		return AggMerge, nil
	case "summary":
		return AggSummary, nil
	default:
		return "", ErrInvalidAggregation
	}
}
