package main

import "strings"

type agentSpec struct {
	ID           string
	Name         string
	Capabilities []string
	Quality      float64
	LatencyMS    float64
	TokenBias    int
	RiskBias     float64
}

type subtaskSpec struct {
	ID           string   `json:"id"`
	Role         string   `json:"role"`
	Title        string   `json:"title"`
	Capabilities []string `json:"capabilities"`
	DependsOn    []string `json:"depends_on,omitempty"`
	WorkMS       float64  `json:"work_ms"`
	Tokens       int      `json:"tokens"`
	Risk         float64  `json:"risk"`
}

type assignment struct {
	SubtaskID string   `json:"subtask_id"`
	AgentID   string   `json:"agent_id"`
	Mode      string   `json:"mode"`
	Role      string   `json:"role"`
	Matched   []string `json:"matched_capabilities,omitempty"`
	Score     float64  `json:"score"`
}

type benchTask struct {
	ID                   string
	Scenario             string
	TaskType             string
	Prompt               string
	GoldMode             string
	RequiredCapabilities []string
	Subtasks             []subtaskSpec
	IntentTerms          []string
	ForbiddenModes       []string
	NeedsCritic          bool
	AllowsBackground     bool
	RiskBudget           float64
	Difficulty           float64
}

func defaultAgents() map[string]agentSpec {
	specs := []agentSpec{
		{ID: "generalist", Name: "Generalist Agent", Capabilities: []string{"planning", "repo", "docs", "research", "summary"}, Quality: 0.66, LatencyMS: 920, TokenBias: 220, RiskBias: 0.20},
		{ID: "repo-agent", Name: "Repository Agent", Capabilities: []string{"repo", "code", "git", "search", "go"}, Quality: 0.84, LatencyMS: 780, TokenBias: 180, RiskBias: 0.10},
		{ID: "backend-agent", Name: "Backend Agent", Capabilities: []string{"backend", "go", "api", "db", "service"}, Quality: 0.86, LatencyMS: 860, TokenBias: 210, RiskBias: 0.12},
		{ID: "frontend-agent", Name: "Frontend Agent", Capabilities: []string{"frontend", "ui", "typescript", "react", "visual"}, Quality: 0.83, LatencyMS: 840, TokenBias: 210, RiskBias: 0.10},
		{ID: "test-agent", Name: "Test Agent", Capabilities: []string{"test", "benchmark", "ci", "coverage", "validation"}, Quality: 0.88, LatencyMS: 740, TokenBias: 170, RiskBias: 0.08},
		{ID: "docs-agent", Name: "Documentation Agent", Capabilities: []string{"docs", "writing", "report", "summary", "changelog"}, Quality: 0.82, LatencyMS: 680, TokenBias: 160, RiskBias: 0.06},
		{ID: "ops-agent", Name: "Ops Agent", Capabilities: []string{"ops", "runtime", "logs", "deploy", "shell"}, Quality: 0.80, LatencyMS: 900, TokenBias: 190, RiskBias: 0.18},
		{ID: "research-agent", Name: "Research Agent", Capabilities: []string{"research", "web", "source", "synthesis", "market"}, Quality: 0.81, LatencyMS: 980, TokenBias: 240, RiskBias: 0.10},
		{ID: "security-agent", Name: "Security Agent", Capabilities: []string{"security", "risk", "policy", "review", "threat"}, Quality: 0.87, LatencyMS: 820, TokenBias: 190, RiskBias: 0.04},
		{ID: "critic-agent", Name: "Critic Agent", Capabilities: []string{"critic", "review", "risk", "consensus", "aggregation"}, Quality: 0.85, LatencyMS: 760, TokenBias: 180, RiskBias: 0.03},
		{ID: "integrator-agent", Name: "Integrator Agent", Capabilities: []string{"integration", "aggregation", "planning", "summary", "orchestration"}, Quality: 0.84, LatencyMS: 790, TokenBias: 190, RiskBias: 0.07},
		{ID: "memory-agent", Name: "Memory Agent", Capabilities: []string{"memory", "rag", "recall", "index", "knowledge"}, Quality: 0.83, LatencyMS: 820, TokenBias: 210, RiskBias: 0.09},
		{ID: "data-agent", Name: "Data Agent", Capabilities: []string{"data", "sql", "analysis", "metric", "experiment"}, Quality: 0.85, LatencyMS: 830, TokenBias: 200, RiskBias: 0.08},
	}
	out := make(map[string]agentSpec, len(specs))
	for _, spec := range specs {
		out[spec.ID] = spec
	}
	return out
}

func defaultTasks() []benchTask {
	base := []benchTask{
		{
			ID: "S1-001", Scenario: "single", TaskType: "concept", Prompt: "解释多 agent 为什么不一定比单 agent 更好。",
			GoldMode: "single", IntentTerms: []string{"concept", "explain", "multiagent"}, ForbiddenModes: []string{"parallel", "pipeline", "debate", "autonomy_queue"},
			Subtasks:   []subtaskSpec{{ID: "explain", Role: "generalist", Title: "Explain trade-offs", Capabilities: []string{"summary", "planning"}, WorkMS: 900, Tokens: 420, Risk: 0.2}},
			RiskBudget: 0.2, Difficulty: 0.30,
		},
		{
			ID: "S1-002", Scenario: "single", TaskType: "repo_read", Prompt: "看一下现有 delegate_task 工具在哪里，不要拆子代理。",
			GoldMode: "single", IntentTerms: []string{"repo", "inspect", "delegate"}, ForbiddenModes: []string{"parallel", "pipeline", "autonomy_queue"},
			Subtasks:   []subtaskSpec{{ID: "repo-read", Role: "repo", Title: "Locate delegate tool", Capabilities: []string{"repo", "code", "search"}, WorkMS: 1200, Tokens: 520, Risk: 0.3}},
			RiskBudget: 0.3, Difficulty: 0.35,
		},
		{
			ID: "S1-003", Scenario: "single", TaskType: "doc", Prompt: "用中文概括一下 autonomy worker pool 的作用。",
			GoldMode: "single", IntentTerms: []string{"docs", "summary", "worker"}, ForbiddenModes: []string{"parallel", "debate"},
			Subtasks:   []subtaskSpec{{ID: "summarize", Role: "docs", Title: "Summarize worker pool", Capabilities: []string{"docs", "summary"}, WorkMS: 1000, Tokens: 460, Risk: 0.2}},
			RiskBudget: 0.2, Difficulty: 0.30,
		},
		{
			ID: "S1-004", Scenario: "single", TaskType: "math", Prompt: "推导一下协调开销归一化指标。",
			GoldMode: "single", IntentTerms: []string{"metric", "formula", "explain"}, ForbiddenModes: []string{"parallel", "pipeline", "autonomy_queue"},
			Subtasks:   []subtaskSpec{{ID: "derive", Role: "data", Title: "Derive metric formula", Capabilities: []string{"metric", "analysis"}, WorkMS: 1100, Tokens: 520, Risk: 0.2}},
			RiskBudget: 0.2, Difficulty: 0.45,
		},
		{
			ID: "P2-001", Scenario: "parallel", TaskType: "repo_change", Prompt: "分别检查 backend、frontend、docs 三块，然后汇总 luckyagent 多 agent 入口。",
			GoldMode: "parallel", IntentTerms: []string{"repo", "frontend", "backend", "docs", "summary"},
			Subtasks: []subtaskSpec{
				{ID: "backend", Role: "backend", Title: "Inspect backend agent entry", Capabilities: []string{"backend", "go", "api"}, WorkMS: 1900, Tokens: 720, Risk: 0.5},
				{ID: "frontend", Role: "frontend", Title: "Inspect UI integration", Capabilities: []string{"frontend", "ui", "typescript"}, WorkMS: 1700, Tokens: 680, Risk: 0.4},
				{ID: "docs", Role: "docs", Title: "Inspect docs and changelog", Capabilities: []string{"docs", "report"}, WorkMS: 1200, Tokens: 480, Risk: 0.2},
				{ID: "integrate", Role: "integrator", Title: "Merge findings", Capabilities: []string{"aggregation", "summary"}, WorkMS: 800, Tokens: 360, Risk: 0.2},
			},
			RiskBudget: 0.9, Difficulty: 0.70,
		},
		{
			ID: "P2-002", Scenario: "parallel", TaskType: "benchmark_design", Prompt: "让多个子代理并行研究记忆、工具、上下文打包三个 benchmark，最后给一个统一实验矩阵。",
			GoldMode: "parallel", IntentTerms: []string{"benchmark", "memory", "tool", "context", "experiment"},
			Subtasks: []subtaskSpec{
				{ID: "memory", Role: "memory", Title: "Inspect memory benchmark", Capabilities: []string{"memory", "rag", "knowledge"}, WorkMS: 1800, Tokens: 700, Risk: 0.3},
				{ID: "tool", Role: "test", Title: "Inspect tool benchmark", Capabilities: []string{"benchmark", "test", "metric"}, WorkMS: 1600, Tokens: 680, Risk: 0.3},
				{ID: "context", Role: "data", Title: "Inspect context packer benchmark", Capabilities: []string{"experiment", "metric", "analysis"}, WorkMS: 1700, Tokens: 690, Risk: 0.3},
				{ID: "matrix", Role: "integrator", Title: "Build unified experiment matrix", Capabilities: []string{"aggregation", "planning"}, WorkMS: 900, Tokens: 420, Risk: 0.2},
			},
			RiskBudget: 0.8, Difficulty: 0.75,
		},
		{
			ID: "P2-003", Scenario: "parallel", TaskType: "qa", Prompt: "并行检查测试覆盖、运行日志、文档缺口，输出发布前风险列表。",
			GoldMode: "parallel", IntentTerms: []string{"test", "logs", "docs", "risk"},
			Subtasks: []subtaskSpec{
				{ID: "tests", Role: "test", Title: "Check tests", Capabilities: []string{"test", "coverage", "ci"}, WorkMS: 1600, Tokens: 620, Risk: 0.4},
				{ID: "logs", Role: "ops", Title: "Check runtime logs", Capabilities: []string{"ops", "logs", "runtime"}, WorkMS: 1400, Tokens: 560, Risk: 0.4},
				{ID: "docs", Role: "docs", Title: "Check docs gap", Capabilities: []string{"docs", "report"}, WorkMS: 1000, Tokens: 430, Risk: 0.2},
				{ID: "risk", Role: "security", Title: "Compile risk list", Capabilities: []string{"risk", "review"}, WorkMS: 1000, Tokens: 460, Risk: 0.6},
			},
			NeedsCritic: true, RiskBudget: 1.1, Difficulty: 0.80,
		},
		{
			ID: "P2-004", Scenario: "parallel", TaskType: "research", Prompt: "三个子代理分别研究竞品、论文、现有代码，再汇总多 agent 优化路线。",
			GoldMode: "parallel", IntentTerms: []string{"research", "paper", "repo", "roadmap"},
			Subtasks: []subtaskSpec{
				{ID: "competitor", Role: "research", Title: "Research competitor patterns", Capabilities: []string{"research", "web", "synthesis"}, WorkMS: 1900, Tokens: 760, Risk: 0.4},
				{ID: "paper", Role: "research", Title: "Research paper patterns", Capabilities: []string{"research", "source", "synthesis"}, WorkMS: 2000, Tokens: 800, Risk: 0.4},
				{ID: "code", Role: "repo", Title: "Inspect local code", Capabilities: []string{"repo", "code", "search"}, WorkMS: 1500, Tokens: 620, Risk: 0.3},
				{ID: "roadmap", Role: "integrator", Title: "Merge roadmap", Capabilities: []string{"aggregation", "planning"}, WorkMS: 1000, Tokens: 500, Risk: 0.3},
			},
			RiskBudget: 1.0, Difficulty: 0.78,
		},
		{
			ID: "L3-001", Scenario: "pipeline", TaskType: "implementation", Prompt: "先读代码，再改 benchmark，再跑测试，最后汇总结果。",
			GoldMode: "pipeline", IntentTerms: []string{"repo", "edit", "test", "report"},
			Subtasks: []subtaskSpec{
				{ID: "read", Role: "repo", Title: "Read benchmark code", Capabilities: []string{"repo", "code", "search"}, WorkMS: 1200, Tokens: 560, Risk: 0.3},
				{ID: "edit", Role: "backend", Title: "Patch benchmark", Capabilities: []string{"backend", "go", "code"}, DependsOn: []string{"read"}, WorkMS: 1800, Tokens: 780, Risk: 0.8},
				{ID: "test", Role: "test", Title: "Run tests", Capabilities: []string{"test", "validation"}, DependsOn: []string{"edit"}, WorkMS: 1600, Tokens: 620, Risk: 0.5},
				{ID: "report", Role: "docs", Title: "Summarize result", Capabilities: []string{"docs", "report"}, DependsOn: []string{"test"}, WorkMS: 900, Tokens: 420, Risk: 0.2},
			},
			RiskBudget: 1.2, Difficulty: 0.85,
		},
		{
			ID: "L3-002", Scenario: "pipeline", TaskType: "release", Prompt: "做一次发布收口：检查脏文件、跑 baseline、生成报告、commit。",
			GoldMode: "pipeline", IntentTerms: []string{"git", "benchmark", "report", "commit"},
			Subtasks: []subtaskSpec{
				{ID: "status", Role: "repo", Title: "Check git status", Capabilities: []string{"repo", "git"}, WorkMS: 900, Tokens: 360, Risk: 0.3},
				{ID: "baseline", Role: "test", Title: "Run baseline", Capabilities: []string{"benchmark", "test", "validation"}, DependsOn: []string{"status"}, WorkMS: 1700, Tokens: 640, Risk: 0.4},
				{ID: "report", Role: "docs", Title: "Generate report", Capabilities: []string{"docs", "report"}, DependsOn: []string{"baseline"}, WorkMS: 1200, Tokens: 520, Risk: 0.3},
				{ID: "commit", Role: "repo", Title: "Commit scoped changes", Capabilities: []string{"git", "repo"}, DependsOn: []string{"report"}, WorkMS: 1100, Tokens: 460, Risk: 0.9},
			},
			NeedsCritic: true, RiskBudget: 1.5, Difficulty: 0.82,
		},
		{
			ID: "L3-003", Scenario: "pipeline", TaskType: "migration", Prompt: "先分析旧记忆系统，再设计迁移，再验证回滚路径。",
			GoldMode: "pipeline", IntentTerms: []string{"memory", "migration", "validation", "risk"},
			Subtasks: []subtaskSpec{
				{ID: "analyze", Role: "memory", Title: "Analyze old memory system", Capabilities: []string{"memory", "knowledge", "repo"}, WorkMS: 1600, Tokens: 640, Risk: 0.4},
				{ID: "design", Role: "backend", Title: "Design migration", Capabilities: []string{"backend", "go", "service"}, DependsOn: []string{"analyze"}, WorkMS: 2000, Tokens: 820, Risk: 0.8},
				{ID: "rollback", Role: "security", Title: "Validate rollback", Capabilities: []string{"risk", "review", "policy"}, DependsOn: []string{"design"}, WorkMS: 1200, Tokens: 520, Risk: 0.7},
			},
			NeedsCritic: true, RiskBudget: 1.2, Difficulty: 0.88,
		},
		{
			ID: "D4-001", Scenario: "debate", TaskType: "architecture", Prompt: "让三个 agent 辩论：多 agent 是用 delegate、autonomy，还是 collab API 做主入口。",
			GoldMode: "debate", IntentTerms: []string{"architecture", "debate", "delegate", "autonomy", "collab"},
			Subtasks: []subtaskSpec{
				{ID: "delegate", Role: "repo", Title: "Argue for delegate tool", Capabilities: []string{"repo", "code", "planning"}, WorkMS: 1300, Tokens: 620, Risk: 0.4},
				{ID: "autonomy", Role: "ops", Title: "Argue for autonomy workers", Capabilities: []string{"ops", "runtime", "planning"}, WorkMS: 1300, Tokens: 620, Risk: 0.5},
				{ID: "collab", Role: "backend", Title: "Argue for collab API", Capabilities: []string{"backend", "api", "service"}, WorkMS: 1300, Tokens: 620, Risk: 0.4},
				{ID: "vote", Role: "critic", Title: "Vote and critique", Capabilities: []string{"critic", "consensus", "review"}, WorkMS: 900, Tokens: 460, Risk: 0.4},
			},
			NeedsCritic: true, RiskBudget: 1.0, Difficulty: 0.82,
		},
		{
			ID: "D4-002", Scenario: "debate", TaskType: "risk_decision", Prompt: "对是否自动启用多 agent 做辩论，重点比较成本、风险、质量。",
			GoldMode: "debate", IntentTerms: []string{"decision", "risk", "cost", "quality"},
			Subtasks: []subtaskSpec{
				{ID: "pro", Role: "research", Title: "Argue for auto multi-agent", Capabilities: []string{"research", "synthesis"}, WorkMS: 1100, Tokens: 560, Risk: 0.3},
				{ID: "con", Role: "security", Title: "Argue against auto multi-agent", Capabilities: []string{"security", "risk"}, WorkMS: 1100, Tokens: 560, Risk: 0.5},
				{ID: "judge", Role: "critic", Title: "Judge final policy", Capabilities: []string{"critic", "consensus", "policy"}, WorkMS: 1000, Tokens: 520, Risk: 0.5},
			},
			NeedsCritic: true, RiskBudget: 1.0, Difficulty: 0.80,
		},
		{
			ID: "D4-003", Scenario: "debate", TaskType: "formula", Prompt: "让指标专家和工程专家辩论 MultiAgentScore 的权重怎么设。",
			GoldMode: "debate", IntentTerms: []string{"metric", "engineering", "weight", "debate"},
			Subtasks: []subtaskSpec{
				{ID: "metric", Role: "data", Title: "Metric perspective", Capabilities: []string{"metric", "analysis"}, WorkMS: 1200, Tokens: 600, Risk: 0.3},
				{ID: "engineering", Role: "backend", Title: "Engineering perspective", Capabilities: []string{"backend", "service"}, WorkMS: 1200, Tokens: 600, Risk: 0.3},
				{ID: "critic", Role: "critic", Title: "Critique weights", Capabilities: []string{"critic", "review"}, WorkMS: 900, Tokens: 450, Risk: 0.3},
			},
			NeedsCritic: true, RiskBudget: 0.8, Difficulty: 0.72,
		},
		{
			ID: "A5-001", Scenario: "autonomy", TaskType: "background", Prompt: "开一个后台 worker 持续跟踪 benchmark 结果，完成后汇总。",
			GoldMode: "autonomy_queue", IntentTerms: []string{"autonomy", "worker", "background", "benchmark"},
			AllowsBackground: true,
			Subtasks: []subtaskSpec{
				{ID: "queue", Role: "ops", Title: "Create queued background task", Capabilities: []string{"ops", "runtime"}, WorkMS: 900, Tokens: 380, Risk: 0.6},
				{ID: "run", Role: "test", Title: "Run benchmark in worker", Capabilities: []string{"benchmark", "test"}, DependsOn: []string{"queue"}, WorkMS: 2600, Tokens: 850, Risk: 0.6},
				{ID: "report", Role: "docs", Title: "Report worker output", Capabilities: []string{"docs", "report"}, DependsOn: []string{"run"}, WorkMS: 900, Tokens: 420, Risk: 0.3},
			},
			RiskBudget: 1.4, Difficulty: 0.76,
		},
		{
			ID: "A5-002", Scenario: "autonomy", TaskType: "monitor", Prompt: "把多 agent 实验设为异步队列任务，不阻塞当前会话。",
			GoldMode: "autonomy_queue", IntentTerms: []string{"autonomy", "queue", "async", "experiment"},
			AllowsBackground: true,
			Subtasks: []subtaskSpec{
				{ID: "enqueue", Role: "ops", Title: "Enqueue async experiment", Capabilities: []string{"ops", "runtime"}, WorkMS: 800, Tokens: 360, Risk: 0.5},
				{ID: "experiment", Role: "data", Title: "Run experiment", Capabilities: []string{"experiment", "analysis"}, DependsOn: []string{"enqueue"}, WorkMS: 2300, Tokens: 780, Risk: 0.5},
				{ID: "notify", Role: "docs", Title: "Summarize notification", Capabilities: []string{"summary", "report"}, DependsOn: []string{"experiment"}, WorkMS: 800, Tokens: 360, Risk: 0.2},
			},
			RiskBudget: 1.2, Difficulty: 0.70,
		},
		{
			ID: "A5-003", Scenario: "autonomy", TaskType: "trap", Prompt: "只查看 autonomy 状态，不要新增后台 worker。",
			GoldMode: "single", IntentTerms: []string{"autonomy", "status", "safety"}, ForbiddenModes: []string{"autonomy_queue", "parallel"},
			Subtasks:   []subtaskSpec{{ID: "status", Role: "ops", Title: "Inspect autonomy status", Capabilities: []string{"ops", "runtime"}, WorkMS: 800, Tokens: 320, Risk: 0.2}},
			RiskBudget: 0.2, Difficulty: 0.35,
		},
		{
			ID: "H6-001", Scenario: "heavy", TaskType: "hermes_reproduction", Prompt: "复现一个 Hermes agent：先逆向分析 Hermes Agent 的 planner、execute、debugger、verifier 行为，再在 LuckyHarness 中设计兼容入口，接着实现最小可运行链路，跑 GUI 与 CLI 验收，最后生成迁移报告。",
			GoldMode: "pipeline", IntentTerms: []string{"hermes", "agent", "planner", "execute", "debugger", "verifier", "gui", "cli", "reproduction"},
			Subtasks: []subtaskSpec{
				{ID: "research-hermes", Role: "research", Title: "Reverse engineer Hermes agent behavior", Capabilities: []string{"research", "source", "synthesis"}, WorkMS: 3200, Tokens: 1260, Risk: 0.6},
				{ID: "design-entry", Role: "integrator", Title: "Design LuckyHarness-compatible orchestration entry", Capabilities: []string{"integration", "planning", "orchestration"}, DependsOn: []string{"research-hermes"}, WorkMS: 2800, Tokens: 1180, Risk: 0.8},
				{ID: "backend-runtime", Role: "backend", Title: "Implement planner-executor runtime bridge", Capabilities: []string{"backend", "go", "api", "service"}, DependsOn: []string{"design-entry"}, WorkMS: 4200, Tokens: 1680, Risk: 1.0},
				{ID: "frontend-console", Role: "frontend", Title: "Expose Hermes-like console workflow", Capabilities: []string{"frontend", "ui", "typescript", "react"}, DependsOn: []string{"design-entry"}, WorkMS: 3600, Tokens: 1420, Risk: 0.8},
				{ID: "debugger-chain", Role: "test", Title: "Wire debugger and verifier gates", Capabilities: []string{"test", "benchmark", "validation"}, DependsOn: []string{"backend-runtime", "frontend-console"}, WorkMS: 3400, Tokens: 1320, Risk: 0.9},
				{ID: "ops-trace", Role: "ops", Title: "Capture runtime traces and failure recovery", Capabilities: []string{"ops", "runtime", "logs"}, DependsOn: []string{"debugger-chain"}, WorkMS: 2400, Tokens: 960, Risk: 0.7},
				{ID: "security-review", Role: "security", Title: "Review delegation and tool safety", Capabilities: []string{"security", "risk", "policy", "review"}, DependsOn: []string{"ops-trace"}, WorkMS: 2200, Tokens: 880, Risk: 0.8},
				{ID: "acceptance-report", Role: "docs", Title: "Write migration and acceptance report", Capabilities: []string{"docs", "report", "summary", "changelog"}, DependsOn: []string{"security-review"}, WorkMS: 1800, Tokens: 720, Risk: 0.3},
			},
			NeedsCritic: true, RiskBudget: 2.7, Difficulty: 0.98,
		},
		{
			ID: "H6-002", Scenario: "heavy", TaskType: "hermes_parity_audit", Prompt: "多个子代理并行审计 LuckyHarness 的 backend、frontend、Telegram、dashboard、docs、tests 六条线，找出和 Hermes Agent 操作台的差距，并由集成者汇总优先级。",
			GoldMode: "parallel", IntentTerms: []string{"hermes", "parity", "backend", "frontend", "telegram", "dashboard", "docs", "tests"},
			Subtasks: []subtaskSpec{
				{ID: "backend-audit", Role: "backend", Title: "Audit backend orchestration APIs", Capabilities: []string{"backend", "go", "api", "service"}, WorkMS: 2600, Tokens: 1040, Risk: 0.6},
				{ID: "frontend-audit", Role: "frontend", Title: "Audit GUI and TUI parity", Capabilities: []string{"frontend", "ui", "typescript", "react"}, WorkMS: 2600, Tokens: 1040, Risk: 0.5},
				{ID: "telegram-audit", Role: "ops", Title: "Audit Telegram runtime handoff", Capabilities: []string{"ops", "runtime", "logs"}, WorkMS: 2200, Tokens: 900, Risk: 0.6},
				{ID: "dashboard-audit", Role: "repo", Title: "Audit dashboard and websocket workflow", Capabilities: []string{"repo", "code", "search"}, WorkMS: 2200, Tokens: 900, Risk: 0.5},
				{ID: "docs-audit", Role: "docs", Title: "Audit docs and runbooks", Capabilities: []string{"docs", "report", "summary"}, WorkMS: 1600, Tokens: 620, Risk: 0.3},
				{ID: "tests-audit", Role: "test", Title: "Audit tests and benchmark gaps", Capabilities: []string{"test", "benchmark", "coverage", "validation"}, WorkMS: 2200, Tokens: 860, Risk: 0.5},
				{ID: "priority-matrix", Role: "integrator", Title: "Merge Hermes parity priority matrix", Capabilities: []string{"integration", "aggregation", "planning", "summary"}, WorkMS: 1500, Tokens: 680, Risk: 0.4},
			},
			NeedsCritic: true, RiskBudget: 2.0, Difficulty: 0.92,
		},
		{
			ID: "H6-003", Scenario: "heavy", TaskType: "production_recovery", Prompt: "做一次超重发布恢复演练：先冻结变更并检查脏文件，再跑多 agent baseline，对失败 case 做修复，随后执行回滚验证、权限审计、真实 CLI 验收，最后提交 scoped changes。",
			GoldMode: "pipeline", IntentTerms: []string{"release", "recovery", "baseline", "rollback", "audit", "cli", "commit"},
			Subtasks: []subtaskSpec{
				{ID: "freeze", Role: "repo", Title: "Freeze scope and inspect git status", Capabilities: []string{"repo", "git", "search"}, WorkMS: 1000, Tokens: 420, Risk: 0.4},
				{ID: "run-baseline", Role: "test", Title: "Run expanded multi-agent baseline", Capabilities: []string{"test", "benchmark", "validation"}, DependsOn: []string{"freeze"}, WorkMS: 2600, Tokens: 980, Risk: 0.6},
				{ID: "fix-failures", Role: "backend", Title: "Fix failing heavy benchmark cases", Capabilities: []string{"backend", "go", "code"}, DependsOn: []string{"run-baseline"}, WorkMS: 3600, Tokens: 1380, Risk: 1.0},
				{ID: "rollback-check", Role: "security", Title: "Validate rollback and safety policy", Capabilities: []string{"security", "risk", "policy", "review"}, DependsOn: []string{"fix-failures"}, WorkMS: 2200, Tokens: 820, Risk: 0.9},
				{ID: "cli-acceptance", Role: "test", Title: "Run real CLI acceptance", Capabilities: []string{"test", "validation", "ci"}, DependsOn: []string{"rollback-check"}, WorkMS: 2600, Tokens: 940, Risk: 0.7},
				{ID: "write-report", Role: "docs", Title: "Write recovery drill report", Capabilities: []string{"docs", "report", "summary"}, DependsOn: []string{"cli-acceptance"}, WorkMS: 1500, Tokens: 620, Risk: 0.3},
				{ID: "commit", Role: "repo", Title: "Commit scoped benchmark changes", Capabilities: []string{"repo", "git"}, DependsOn: []string{"write-report"}, WorkMS: 1200, Tokens: 460, Risk: 0.9},
			},
			NeedsCritic: true, RiskBudget: 2.6, Difficulty: 0.96,
		},
		{
			ID: "H6-004", Scenario: "heavy", TaskType: "architecture_debate", Prompt: "让 backend、frontend、ops、security、critic 五个 agent 辩论：复现 Hermes agent 应该走 delegate、workflow graph、autonomy queue 还是 dashboard bridge 作为主入口，并输出可执行决策。",
			GoldMode: "debate", IntentTerms: []string{"hermes", "architecture", "debate", "delegate", "workflow", "autonomy", "dashboard"},
			Subtasks: []subtaskSpec{
				{ID: "backend-position", Role: "backend", Title: "Argue for backend workflow graph", Capabilities: []string{"backend", "api", "service", "planning"}, WorkMS: 1800, Tokens: 820, Risk: 0.5},
				{ID: "frontend-position", Role: "frontend", Title: "Argue for dashboard bridge", Capabilities: []string{"frontend", "ui", "react", "visual"}, WorkMS: 1800, Tokens: 820, Risk: 0.4},
				{ID: "ops-position", Role: "ops", Title: "Argue for autonomy queue and traces", Capabilities: []string{"ops", "runtime", "logs", "deploy"}, WorkMS: 1800, Tokens: 820, Risk: 0.6},
				{ID: "security-position", Role: "security", Title: "Argue safety and permission constraints", Capabilities: []string{"security", "risk", "policy", "review"}, WorkMS: 1800, Tokens: 820, Risk: 0.7},
				{ID: "critic-decision", Role: "critic", Title: "Judge architecture decision", Capabilities: []string{"critic", "consensus", "review", "aggregation"}, WorkMS: 1400, Tokens: 660, Risk: 0.5},
			},
			NeedsCritic: true, RiskBudget: 1.8, Difficulty: 0.90,
		},
		{
			ID: "H6-005", Scenario: "heavy", TaskType: "async_hermes_replay", Prompt: "把复现 Hermes agent 的兼容性实验放到后台异步队列，不阻塞当前会话；worker 要持续跑 baseline、保存 trace、失败后通知 verifier 汇总。",
			GoldMode: "autonomy_queue", IntentTerms: []string{"hermes", "background", "async", "queue", "worker", "baseline", "trace", "verifier"},
			AllowsBackground: true,
			Subtasks: []subtaskSpec{
				{ID: "enqueue", Role: "ops", Title: "Enqueue Hermes replay experiment", Capabilities: []string{"ops", "runtime"}, WorkMS: 900, Tokens: 380, Risk: 0.6},
				{ID: "run-heavy-baseline", Role: "test", Title: "Run heavy baseline in worker", Capabilities: []string{"test", "benchmark", "validation"}, DependsOn: []string{"enqueue"}, WorkMS: 3200, Tokens: 1120, Risk: 0.7},
				{ID: "save-trace", Role: "data", Title: "Persist orchestration traces", Capabilities: []string{"data", "metric", "experiment"}, DependsOn: []string{"run-heavy-baseline"}, WorkMS: 1800, Tokens: 680, Risk: 0.4},
				{ID: "notify-verifier", Role: "critic", Title: "Notify verifier on failures", Capabilities: []string{"critic", "review", "risk"}, DependsOn: []string{"save-trace"}, WorkMS: 1200, Tokens: 520, Risk: 0.5},
				{ID: "summary", Role: "docs", Title: "Summarize async replay output", Capabilities: []string{"docs", "report", "summary"}, DependsOn: []string{"notify-verifier"}, WorkMS: 1200, Tokens: 480, Risk: 0.3},
			},
			NeedsCritic: true, RiskBudget: 2.0, Difficulty: 0.88,
		},
	}
	base = append(base, syntheticExpansionTasks()...)
	return normalizeTasks(base)
}

func normalizeTasks(tasks []benchTask) []benchTask {
	for i := range tasks {
		tasks[i].GoldMode = normalizeMode(tasks[i].GoldMode)
		tasks[i].RequiredCapabilities = uniqueStrings(append(tasks[i].RequiredCapabilities, capabilitiesFromSubtasks(tasks[i].Subtasks)...))
		if tasks[i].RiskBudget == 0 {
			tasks[i].RiskBudget = 1.0
		}
		if tasks[i].Difficulty == 0 {
			tasks[i].Difficulty = 0.5
		}
	}
	return tasks
}

func capabilitiesFromSubtasks(subtasks []subtaskSpec) []string {
	var out []string
	for _, sub := range subtasks {
		out = append(out, sub.Capabilities...)
	}
	return out
}

func normalizeMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	mode = strings.ReplaceAll(mode, "-", "_")
	switch mode {
	case "", "none", "solo", "single_agent":
		return "single"
	case "parallel":
		return "parallel"
	case "pipeline":
		return "pipeline"
	case "debate":
		return "debate"
	case "autonomy", "autonomy_queue", "background":
		return "autonomy_queue"
	default:
		return mode
	}
}
