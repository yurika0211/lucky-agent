package main

type syntheticCase struct {
	id               string
	scenario         string
	taskType         string
	prompt           string
	goldMode         string
	needsCritic      bool
	allowsBackground bool
	forbiddenModes   []string
	riskBudget       float64
	difficulty       float64
	intentTerms      []string
	subtasks         []subtaskSpec
}

func syntheticExpansionTasks() []benchTask {
	specs := []syntheticCase{
		{id: "S1-005", scenario: "single", taskType: "api_lookup", prompt: "只定位 lh chat 的配置读取路径，不要拆分执行。", goldMode: "single", forbiddenModes: []string{"parallel", "pipeline", "autonomy_queue"}, riskBudget: 0.25, difficulty: 0.36, intentTerms: []string{"repo", "config", "single"}, subtasks: []subtaskSpec{simpleSubtask("locate-config", "repo", "Locate config read path", []string{"repo", "code", "search"}, 950, 420, 0.25)}},
		{id: "S1-006", scenario: "single", taskType: "policy_answer", prompt: "解释为什么 runtime dry-run 阶段不能自动开子代理。", goldMode: "single", forbiddenModes: []string{"parallel", "debate", "autonomy_queue"}, riskBudget: 0.22, difficulty: 0.34, intentTerms: []string{"policy", "explain", "dry-run"}, subtasks: []subtaskSpec{simpleSubtask("explain-policy", "docs", "Explain dry-run policy", []string{"docs", "summary", "policy"}, 850, 390, 0.20)}},
		{id: "S1-007", scenario: "single", taskType: "metric_question", prompt: "给出 EdgeNLL 的直觉说明和一个数字例子。", goldMode: "single", forbiddenModes: []string{"parallel", "pipeline", "autonomy_queue"}, riskBudget: 0.24, difficulty: 0.42, intentTerms: []string{"metric", "nll", "example"}, subtasks: []subtaskSpec{simpleSubtask("derive-nll", "data", "Explain EdgeNLL example", []string{"data", "metric", "analysis"}, 1000, 480, 0.22)}},
		{id: "S1-008", scenario: "single", taskType: "status_check", prompt: "只检查多 agent benchmark 的 case 数量并回复数字。", goldMode: "single", forbiddenModes: []string{"parallel", "pipeline", "debate", "autonomy_queue"}, riskBudget: 0.18, difficulty: 0.28, intentTerms: []string{"benchmark", "count", "status"}, subtasks: []subtaskSpec{simpleSubtask("count-cases", "test", "Count benchmark cases", []string{"test", "benchmark", "metric"}, 700, 300, 0.18)}},
		{id: "S1-009", scenario: "single", taskType: "doc_edit", prompt: "把 README 里一个小错别字改掉，不需要子代理。", goldMode: "single", forbiddenModes: []string{"parallel", "pipeline", "debate"}, riskBudget: 0.20, difficulty: 0.30, intentTerms: []string{"docs", "edit", "single"}, subtasks: []subtaskSpec{simpleSubtask("fix-typo", "docs", "Fix README typo", []string{"docs", "writing"}, 760, 320, 0.20)}},
		{id: "S1-010", scenario: "single", taskType: "formula_check", prompt: "检查 Lyapunov decrease 的公式符号是否写反。", goldMode: "single", forbiddenModes: []string{"parallel", "autonomy_queue"}, riskBudget: 0.24, difficulty: 0.46, intentTerms: []string{"lyapunov", "formula", "check"}, subtasks: []subtaskSpec{simpleSubtask("check-sign", "data", "Check Lyapunov sign", []string{"data", "metric", "analysis"}, 1080, 500, 0.25)}},

		{id: "P2-005", scenario: "parallel", taskType: "module_audit", prompt: "并行审计 CLI、websocket、autonomy 三个模块，然后合并风险清单。", goldMode: "parallel", needsCritic: true, riskBudget: 1.05, difficulty: 0.78, intentTerms: []string{"cli", "websocket", "autonomy", "audit"}, subtasks: parallelAuditSubtasks("cli", "websocket", "autonomy", "risk-merge")},
		{id: "P2-006", scenario: "parallel", taskType: "source_research", prompt: "三个子代理分别研究 arXiv、现有 benchmark、Hermes 行为，再输出模型假设表。", goldMode: "parallel", riskBudget: 1.0, difficulty: 0.76, intentTerms: []string{"arxiv", "benchmark", "hermes", "research"}, subtasks: []subtaskSpec{
			simpleSubtask("arxiv", "research", "Research papers", []string{"research", "source", "synthesis"}, 1750, 720, 0.35),
			simpleSubtask("benchmark", "test", "Inspect benchmark", []string{"test", "benchmark", "metric"}, 1550, 650, 0.35),
			simpleSubtask("hermes", "repo", "Inspect Hermes parity notes", []string{"repo", "code", "search"}, 1450, 620, 0.35),
			simpleSubtask("hypothesis-table", "integrator", "Merge model assumptions", []string{"integration", "aggregation", "planning"}, 900, 430, 0.25),
		}},
		{id: "P2-007", scenario: "parallel", taskType: "ui_backend_gap", prompt: "前端和后端分别检查多 agent trace 展示和写入，再由文档代理汇总缺口。", goldMode: "parallel", riskBudget: 0.95, difficulty: 0.72, intentTerms: []string{"frontend", "backend", "trace", "docs"}, subtasks: []subtaskSpec{
			simpleSubtask("frontend-trace", "frontend", "Audit trace UI", []string{"frontend", "ui", "typescript"}, 1650, 640, 0.40),
			simpleSubtask("backend-trace", "backend", "Audit trace writer", []string{"backend", "go", "api"}, 1650, 660, 0.45),
			simpleSubtask("docs-gap", "docs", "Summarize trace docs gap", []string{"docs", "report", "summary"}, 950, 420, 0.25),
			simpleSubtask("merge-gap", "integrator", "Merge trace gap list", []string{"aggregation", "planning"}, 850, 390, 0.25),
		}},
		{id: "P2-008", scenario: "parallel", taskType: "quality_matrix", prompt: "并行计算速度、成本、质量、风险四组指标，最后合成质量矩阵。", goldMode: "parallel", riskBudget: 0.9, difficulty: 0.74, intentTerms: []string{"speed", "cost", "quality", "risk"}, subtasks: []subtaskSpec{
			simpleSubtask("speed", "data", "Measure speed", []string{"data", "metric", "analysis"}, 1350, 560, 0.30),
			simpleSubtask("cost", "data", "Measure cost", []string{"data", "metric", "analysis"}, 1350, 560, 0.30),
			simpleSubtask("quality", "test", "Measure quality", []string{"test", "benchmark", "validation"}, 1450, 620, 0.40),
			simpleSubtask("risk", "security", "Measure risk", []string{"security", "risk", "review"}, 1300, 560, 0.50),
			simpleSubtask("matrix", "integrator", "Merge quality matrix", []string{"aggregation", "summary"}, 900, 420, 0.25),
		}},
		{id: "P2-009", scenario: "parallel", taskType: "migration_inventory", prompt: "并行盘点 memory、tools、sessions、permissions 四类迁移对象。", goldMode: "parallel", riskBudget: 1.0, difficulty: 0.77, intentTerms: []string{"memory", "tools", "sessions", "permissions"}, subtasks: []subtaskSpec{
			simpleSubtask("memory", "memory", "Inventory memory migration", []string{"memory", "knowledge", "index"}, 1500, 620, 0.35),
			simpleSubtask("tools", "repo", "Inventory tool migration", []string{"repo", "code", "search"}, 1500, 620, 0.35),
			simpleSubtask("sessions", "backend", "Inventory session migration", []string{"backend", "go", "service"}, 1600, 660, 0.45),
			simpleSubtask("permissions", "security", "Inventory permissions migration", []string{"security", "policy", "risk"}, 1400, 580, 0.55),
			simpleSubtask("inventory", "integrator", "Merge migration inventory", []string{"integration", "aggregation"}, 900, 430, 0.30),
		}},
		{id: "P2-010", scenario: "parallel", taskType: "release_parallel_check", prompt: "发布前让测试、日志、文档、安全四路并行检查并合并 blocking list。", goldMode: "parallel", needsCritic: true, riskBudget: 1.15, difficulty: 0.82, intentTerms: []string{"release", "test", "logs", "docs", "security"}, subtasks: []subtaskSpec{
			simpleSubtask("test-check", "test", "Run release tests", []string{"test", "ci", "validation"}, 1700, 680, 0.45),
			simpleSubtask("log-check", "ops", "Inspect release logs", []string{"ops", "logs", "runtime"}, 1450, 560, 0.45),
			simpleSubtask("doc-check", "docs", "Inspect release docs", []string{"docs", "report"}, 1000, 420, 0.25),
			simpleSubtask("security-check", "security", "Inspect release security", []string{"security", "risk", "policy"}, 1450, 600, 0.60),
			simpleSubtask("blocking-list", "critic", "Merge blocking list", []string{"critic", "review", "aggregation"}, 1050, 480, 0.45),
		}},
		{id: "P2-011", scenario: "parallel", taskType: "compare_baselines", prompt: "并行跑 baseline、dependency-aware、math-full 三组离线结果并比较。", goldMode: "parallel", riskBudget: 1.05, difficulty: 0.80, intentTerms: []string{"baseline", "dependency-aware", "math-full", "compare"}, subtasks: []subtaskSpec{
			simpleSubtask("run-baseline", "test", "Run baseline", []string{"test", "benchmark"}, 1650, 650, 0.40),
			simpleSubtask("run-dependency", "test", "Run dependency-aware", []string{"test", "benchmark"}, 1650, 650, 0.40),
			simpleSubtask("run-math", "data", "Run math-full", []string{"data", "experiment", "metric"}, 1800, 720, 0.45),
			simpleSubtask("compare", "integrator", "Compare baseline variants", []string{"aggregation", "summary", "planning"}, 950, 430, 0.30),
		}},

		{id: "L3-004", scenario: "pipeline", taskType: "trace_schema", prompt: "先定义 trace schema，再改 JSONL 输出，再补测试，最后更新 README。", goldMode: "pipeline", riskBudget: 1.25, difficulty: 0.84, intentTerms: []string{"trace", "jsonl", "test", "readme"}, subtasks: pipelineSubtasks("schema", "emit", "test", "docs")},
		{id: "L3-005", scenario: "pipeline", taskType: "calibration_metric", prompt: "先收集预测概率，再分桶计算 ECE，再写 summary 字段，最后验证阈值。", goldMode: "pipeline", riskBudget: 1.2, difficulty: 0.86, intentTerms: []string{"probability", "ece", "summary", "threshold"}, subtasks: []subtaskSpec{
			simpleSubtask("collect-probs", "data", "Collect predicted probabilities", []string{"data", "metric", "experiment"}, 1300, 540, 0.35),
			dependentSubtask("bucket-ece", "data", "Compute calibration ECE", []string{"data", "metric", "analysis"}, []string{"collect-probs"}, 1600, 680, 0.55),
			dependentSubtask("summary-field", "backend", "Add summary fields", []string{"backend", "go", "code"}, []string{"bucket-ece"}, 1550, 660, 0.70),
			dependentSubtask("validate", "test", "Validate calibration threshold", []string{"test", "validation", "benchmark"}, []string{"summary-field"}, 1400, 560, 0.45),
		}},
		{id: "L3-006", scenario: "pipeline", taskType: "runtime_patch", prompt: "先检查 planner 接口，再增加 dry-run trace 写入，再跑单元测试，再输出开发记录。", goldMode: "pipeline", needsCritic: true, riskBudget: 1.35, difficulty: 0.87, intentTerms: []string{"planner", "dry-run", "trace", "test"}, subtasks: pipelineSubtasks("inspect", "patch", "unit-test", "dev-log")},
		{id: "L3-007", scenario: "pipeline", taskType: "rollback_plan", prompt: "先设计小流量启用条件，再加入回滚检查，再验证失败路径，最后形成 SOP。", goldMode: "pipeline", needsCritic: true, riskBudget: 1.45, difficulty: 0.89, intentTerms: []string{"canary", "rollback", "failure", "sop"}, subtasks: []subtaskSpec{
			simpleSubtask("conditions", "integrator", "Design canary conditions", []string{"integration", "planning", "orchestration"}, 1400, 580, 0.45),
			dependentSubtask("rollback", "security", "Add rollback checks", []string{"security", "risk", "policy"}, []string{"conditions"}, 1500, 620, 0.70),
			dependentSubtask("failure-path", "test", "Validate failure path", []string{"test", "validation", "ci"}, []string{"rollback"}, 1500, 620, 0.60),
			dependentSubtask("sop", "docs", "Write rollout SOP", []string{"docs", "report", "summary"}, []string{"failure-path"}, 950, 420, 0.30),
		}},
		{id: "L3-008", scenario: "pipeline", taskType: "session_replay", prompt: "先抽取历史会话，再匿名化，再标注 mode，再跑 replay 评估。", goldMode: "pipeline", needsCritic: true, riskBudget: 1.4, difficulty: 0.88, intentTerms: []string{"history", "session", "label", "replay"}, subtasks: []subtaskSpec{
			simpleSubtask("extract", "memory", "Extract historical sessions", []string{"memory", "recall", "index"}, 1600, 650, 0.45),
			dependentSubtask("anonymize", "security", "Anonymize session data", []string{"security", "policy", "risk"}, []string{"extract"}, 1400, 580, 0.65),
			dependentSubtask("label", "data", "Label orchestration mode", []string{"data", "analysis", "metric"}, []string{"anonymize"}, 1700, 720, 0.55),
			dependentSubtask("replay", "test", "Run replay evaluation", []string{"test", "benchmark", "validation"}, []string{"label"}, 1800, 720, 0.50),
		}},
		{id: "L3-009", scenario: "pipeline", taskType: "edge_model_train", prompt: "先生成训练集，再拟合边概率模型，再校准概率，再保存模型卡。", goldMode: "pipeline", riskBudget: 1.3, difficulty: 0.86, intentTerms: []string{"training", "edge", "calibration", "model-card"}, subtasks: []subtaskSpec{
			simpleSubtask("dataset", "data", "Build edge model dataset", []string{"data", "experiment", "metric"}, 1550, 640, 0.40),
			dependentSubtask("fit", "data", "Fit edge probability model", []string{"data", "analysis", "metric"}, []string{"dataset"}, 1800, 760, 0.55),
			dependentSubtask("calibrate", "test", "Calibrate probability model", []string{"test", "validation", "benchmark"}, []string{"fit"}, 1550, 640, 0.45),
			dependentSubtask("model-card", "docs", "Write model card", []string{"docs", "report", "summary"}, []string{"calibrate"}, 900, 420, 0.25),
		}},
		{id: "L3-010", scenario: "pipeline", taskType: "safe_commit", prompt: "先检查工作树，再运行目标测试，再生成报告，再只提交相关文件。", goldMode: "pipeline", riskBudget: 1.35, difficulty: 0.83, intentTerms: []string{"git", "test", "report", "commit"}, subtasks: []subtaskSpec{
			simpleSubtask("status", "repo", "Check worktree", []string{"repo", "git", "search"}, 850, 350, 0.35),
			dependentSubtask("target-test", "test", "Run target tests", []string{"test", "validation", "ci"}, []string{"status"}, 1550, 620, 0.50),
			dependentSubtask("report", "docs", "Generate report", []string{"docs", "report", "summary"}, []string{"target-test"}, 1000, 440, 0.30),
			dependentSubtask("commit", "repo", "Commit scoped files", []string{"repo", "git"}, []string{"report"}, 1000, 420, 0.90),
		}},

		{id: "D4-004", scenario: "debate", taskType: "spawn_policy", prompt: "让安全、性能、产品三个视角辩论什么时候应该自动召唤子代理。", goldMode: "debate", needsCritic: true, riskBudget: 1.0, difficulty: 0.78, intentTerms: []string{"spawn", "policy", "safety", "performance"}, subtasks: debateSubtasks("safety", "performance", "product", "judge")},
		{id: "D4-005", scenario: "debate", taskType: "lyapunov_vs_dijkstra", prompt: "让数学代理和工程代理辩论 Lyapunov 能不能替代 Dijkstra，并由 critic 裁决。", goldMode: "debate", needsCritic: true, riskBudget: 0.95, difficulty: 0.76, intentTerms: []string{"lyapunov", "dijkstra", "debate"}, subtasks: []subtaskSpec{
			simpleSubtask("math-position", "data", "Argue mathematical position", []string{"data", "metric", "analysis"}, 1200, 600, 0.30),
			simpleSubtask("engineering-position", "backend", "Argue engineering position", []string{"backend", "service", "planning"}, 1200, 600, 0.35),
			simpleSubtask("critic", "critic", "Judge replacement claim", []string{"critic", "review", "consensus"}, 950, 480, 0.35),
		}},
		{id: "D4-006", scenario: "debate", taskType: "budget_tradeoff", prompt: "围绕高成本 verifier 是否默认开启进行正反辩论，并输出阈值建议。", goldMode: "debate", needsCritic: true, riskBudget: 1.0, difficulty: 0.80, intentTerms: []string{"verifier", "cost", "threshold", "debate"}, subtasks: debateSubtasks("pro-verifier", "against-verifier", "threshold", "judge")},
		{id: "D4-007", scenario: "debate", taskType: "ood_policy", prompt: "辩论分布外任务应该保守单 agent、pipeline，还是进入人工验收。", goldMode: "debate", needsCritic: true, riskBudget: 1.1, difficulty: 0.84, intentTerms: []string{"ood", "single", "pipeline", "human-review"}, subtasks: []subtaskSpec{
			simpleSubtask("single-policy", "security", "Argue conservative single path", []string{"security", "risk", "policy"}, 1150, 540, 0.45),
			simpleSubtask("pipeline-policy", "integrator", "Argue pipeline path", []string{"integration", "planning", "orchestration"}, 1150, 540, 0.45),
			simpleSubtask("human-review", "critic", "Argue human review path", []string{"critic", "review", "consensus"}, 1150, 540, 0.50),
			simpleSubtask("decision", "critic", "Judge OOD policy", []string{"critic", "consensus", "policy"}, 900, 460, 0.45),
		}},
		{id: "D4-008", scenario: "debate", taskType: "agent_topology", prompt: "让 planner、executor、debugger、verifier 四个角色辩论最佳 agent 拓扑。", goldMode: "debate", needsCritic: true, riskBudget: 1.05, difficulty: 0.82, intentTerms: []string{"planner", "executor", "debugger", "verifier"}, subtasks: []subtaskSpec{
			simpleSubtask("planner", "integrator", "Planner topology view", []string{"integration", "planning", "orchestration"}, 1100, 520, 0.35),
			simpleSubtask("executor", "backend", "Executor topology view", []string{"backend", "service", "go"}, 1100, 520, 0.35),
			simpleSubtask("debugger", "test", "Debugger topology view", []string{"test", "validation", "benchmark"}, 1100, 520, 0.35),
			simpleSubtask("verifier", "critic", "Verifier topology view", []string{"critic", "review", "risk"}, 1100, 520, 0.40),
			simpleSubtask("vote", "critic", "Vote topology", []string{"critic", "consensus", "aggregation"}, 850, 430, 0.35),
		}},
		{id: "D4-009", scenario: "debate", taskType: "metric_weights", prompt: "围绕 MultiAgentScore 是否应该提高风险权重进行多方辩论。", goldMode: "debate", needsCritic: true, riskBudget: 0.95, difficulty: 0.74, intentTerms: []string{"score", "risk", "weights", "debate"}, subtasks: debateSubtasks("metric", "risk", "product", "critic")},

		{id: "A5-004", scenario: "autonomy", taskType: "nightly_bench", prompt: "把 60 case benchmark 放进后台 nightly worker，完成后发摘要。", goldMode: "autonomy_queue", allowsBackground: true, riskBudget: 1.25, difficulty: 0.76, intentTerms: []string{"nightly", "worker", "benchmark", "summary"}, subtasks: autonomySubtasks("enqueue-nightly", "run-sixty", "persist-summary", "notify")},
		{id: "A5-005", scenario: "autonomy", taskType: "watchdog", prompt: "后台持续观察多 agent dry-run trace，一旦 ECE 超阈值就通知。", goldMode: "autonomy_queue", allowsBackground: true, riskBudget: 1.3, difficulty: 0.78, intentTerms: []string{"watchdog", "dry-run", "ece", "notify"}, subtasks: autonomySubtasks("enqueue-watchdog", "scan-traces", "check-ece", "notify")},
		{id: "A5-006", scenario: "autonomy", taskType: "long_replay", prompt: "把历史会话 replay 放到异步队列，保存每轮路径 regret。", goldMode: "autonomy_queue", allowsBackground: true, riskBudget: 1.35, difficulty: 0.82, intentTerms: []string{"history", "replay", "queue", "regret"}, subtasks: autonomySubtasks("enqueue-replay", "run-replay", "save-regret", "summarize")},
		{id: "A5-007", scenario: "autonomy", taskType: "trap_background", prompt: "只解释后台 worker 的风险，不要真的创建后台任务。", goldMode: "single", forbiddenModes: []string{"autonomy_queue", "parallel", "pipeline"}, riskBudget: 0.22, difficulty: 0.34, intentTerms: []string{"worker", "risk", "explain"}, subtasks: []subtaskSpec{simpleSubtask("explain-worker-risk", "docs", "Explain worker risk", []string{"docs", "summary", "risk"}, 820, 360, 0.20)}},
		{id: "A5-008", scenario: "autonomy", taskType: "slow_research", prompt: "这个调研可能跑很久，放到后台队列并在完成后写报告。", goldMode: "autonomy_queue", allowsBackground: true, riskBudget: 1.15, difficulty: 0.72, intentTerms: []string{"slow", "research", "queue", "report"}, subtasks: autonomySubtasks("enqueue-research", "long-research", "draft-report", "notify")},
		{id: "A5-009", scenario: "autonomy", taskType: "maintenance", prompt: "启动一个后台维护任务，定期清理过期 benchmark trace。", goldMode: "autonomy_queue", allowsBackground: true, riskBudget: 1.0, difficulty: 0.68, intentTerms: []string{"maintenance", "cleanup", "trace", "worker"}, subtasks: autonomySubtasks("enqueue-cleanup", "scan-old-traces", "delete-expired", "report")},

		{id: "H6-006", scenario: "heavy", taskType: "full_replay_lab", prompt: "构建完整 replay lab：抽取历史会话、标注、训练边模型、跑 math-full、校准、生成论文式实验报告。", goldMode: "pipeline", needsCritic: true, riskBudget: 2.7, difficulty: 0.97, intentTerms: []string{"replay", "label", "train", "calibrate", "report"}, subtasks: heavyPipelineSubtasks("extract-history", "label-dataset", "train-edge-model", "run-math-full", "calibrate", "write-paper")},
		{id: "H6-007", scenario: "heavy", taskType: "runtime_dry_run_rollout", prompt: "上线 runtime dry-run：接入 planner、生成候选图、写 trace、保留单 agent 执行、统计预测命中、准备回滚。", goldMode: "pipeline", needsCritic: true, riskBudget: 2.5, difficulty: 0.95, intentTerms: []string{"runtime", "dry-run", "planner", "trace", "rollback"}, subtasks: heavyPipelineSubtasks("planner-hook", "candidate-graph", "trace-writer", "single-agent-fallback", "stats", "rollback")},
		{id: "H6-008", scenario: "heavy", taskType: "multi_repo_hermes_bridge", prompt: "跨 CLI、GUI、Telegram、dashboard 四端复现 Hermes 风格 bridge，分别实现并最终统一验收。", goldMode: "parallel", needsCritic: true, riskBudget: 2.3, difficulty: 0.93, intentTerms: []string{"cli", "gui", "telegram", "dashboard", "bridge"}, subtasks: []subtaskSpec{
			simpleSubtask("cli-bridge", "backend", "Implement CLI bridge", []string{"backend", "go", "service"}, 2500, 980, 0.75),
			simpleSubtask("gui-bridge", "frontend", "Implement GUI bridge", []string{"frontend", "ui", "typescript"}, 2500, 980, 0.70),
			simpleSubtask("telegram-bridge", "ops", "Implement Telegram bridge", []string{"ops", "runtime", "logs"}, 2300, 900, 0.75),
			simpleSubtask("dashboard-bridge", "repo", "Implement dashboard bridge", []string{"repo", "code", "search"}, 2200, 900, 0.65),
			simpleSubtask("acceptance", "test", "Run unified acceptance", []string{"test", "validation", "benchmark"}, 2300, 900, 0.70),
			simpleSubtask("review", "critic", "Review bridge parity", []string{"critic", "review", "aggregation"}, 1500, 680, 0.55),
		}},
		{id: "H6-009", scenario: "heavy", taskType: "safety_red_team", prompt: "让多角色围绕自动子代理召唤做红队辩论，覆盖权限、成本、循环、数据泄漏和验收失败。", goldMode: "debate", needsCritic: true, riskBudget: 2.0, difficulty: 0.91, intentTerms: []string{"red-team", "permission", "cost", "loop", "leak"}, subtasks: []subtaskSpec{
			simpleSubtask("permission", "security", "Red-team permissions", []string{"security", "policy", "risk"}, 1600, 760, 0.75),
			simpleSubtask("cost", "data", "Red-team cost runaway", []string{"data", "metric", "analysis"}, 1600, 760, 0.60),
			simpleSubtask("loop", "ops", "Red-team orchestration loops", []string{"ops", "runtime", "logs"}, 1600, 760, 0.70),
			simpleSubtask("leak", "security", "Red-team data leakage", []string{"security", "threat", "review"}, 1600, 760, 0.80),
			simpleSubtask("verdict", "critic", "Judge red-team findings", []string{"critic", "consensus", "review"}, 1300, 620, 0.55),
		}},
		{id: "H6-010", scenario: "heavy", taskType: "background_model_training", prompt: "把边概率模型训练、校准、回放验证和模型卡生成放进后台队列，失败后通知 verifier。", goldMode: "autonomy_queue", allowsBackground: true, needsCritic: true, riskBudget: 2.1, difficulty: 0.89, intentTerms: []string{"training", "calibration", "replay", "model-card", "verifier"}, subtasks: []subtaskSpec{
			simpleSubtask("enqueue-training", "ops", "Enqueue edge model training", []string{"ops", "runtime"}, 950, 380, 0.60),
			dependentSubtask("train-model", "data", "Train edge probability model", []string{"data", "analysis", "experiment"}, []string{"enqueue-training"}, 3000, 1120, 0.75),
			dependentSubtask("calibrate-model", "test", "Calibrate trained model", []string{"test", "benchmark", "validation"}, []string{"train-model"}, 2400, 900, 0.60),
			dependentSubtask("replay-validate", "test", "Replay validation", []string{"test", "validation", "ci"}, []string{"calibrate-model"}, 2300, 880, 0.55),
			dependentSubtask("model-card", "docs", "Generate model card", []string{"docs", "report", "summary"}, []string{"replay-validate"}, 1300, 560, 0.35),
			dependentSubtask("notify-verifier", "critic", "Notify verifier on failures", []string{"critic", "review", "risk"}, []string{"model-card"}, 1050, 500, 0.50),
		}},
		{id: "H6-011", scenario: "heavy", taskType: "heavy_single_guard", prompt: "只阅读这份超长 Hermes 迁移报告并指出三个最大风险，不要拆子代理，不要启动后台任务。", goldMode: "single", forbiddenModes: []string{"parallel", "pipeline", "debate", "autonomy_queue"}, needsCritic: true, riskBudget: 0.55, difficulty: 0.90, intentTerms: []string{"hermes", "report", "risk", "single"}, subtasks: []subtaskSpec{
			simpleSubtask("read-heavy-report", "security", "Review long Hermes migration report", []string{"security", "risk", "review", "summary"}, 2200, 920, 0.45),
		}},
	}

	out := make([]benchTask, 0, len(specs))
	for _, spec := range specs {
		out = append(out, benchTask{
			ID:                   spec.id,
			Scenario:             spec.scenario,
			TaskType:             spec.taskType,
			Prompt:               spec.prompt,
			GoldMode:             spec.goldMode,
			Subtasks:             spec.subtasks,
			IntentTerms:          spec.intentTerms,
			ForbiddenModes:       spec.forbiddenModes,
			NeedsCritic:          spec.needsCritic,
			AllowsBackground:     spec.allowsBackground,
			RiskBudget:           spec.riskBudget,
			Difficulty:           spec.difficulty,
			RequiredCapabilities: nil,
		})
	}
	return out
}

func simpleSubtask(id, role, title string, caps []string, workMS float64, tokens int, risk float64) subtaskSpec {
	return subtaskSpec{ID: id, Role: role, Title: title, Capabilities: caps, WorkMS: workMS, Tokens: tokens, Risk: risk}
}

func dependentSubtask(id, role, title string, caps []string, deps []string, workMS float64, tokens int, risk float64) subtaskSpec {
	sub := simpleSubtask(id, role, title, caps, workMS, tokens, risk)
	sub.DependsOn = deps
	return sub
}

func parallelAuditSubtasks(first, second, third, merge string) []subtaskSpec {
	return []subtaskSpec{
		simpleSubtask(first, "repo", "Audit "+first, []string{"repo", "code", "search"}, 1500, 620, 0.40),
		simpleSubtask(second, "backend", "Audit "+second, []string{"backend", "go", "service"}, 1600, 660, 0.45),
		simpleSubtask(third, "ops", "Audit "+third, []string{"ops", "runtime", "logs"}, 1550, 640, 0.45),
		simpleSubtask(merge, "integrator", "Merge "+merge, []string{"integration", "aggregation", "summary"}, 900, 420, 0.30),
	}
}

func pipelineSubtasks(first, second, third, fourth string) []subtaskSpec {
	return []subtaskSpec{
		simpleSubtask(first, "repo", "Pipeline step "+first, []string{"repo", "code", "search"}, 1150, 500, 0.30),
		dependentSubtask(second, "backend", "Pipeline step "+second, []string{"backend", "go", "code"}, []string{first}, 1650, 700, 0.70),
		dependentSubtask(third, "test", "Pipeline step "+third, []string{"test", "validation", "benchmark"}, []string{second}, 1450, 600, 0.50),
		dependentSubtask(fourth, "docs", "Pipeline step "+fourth, []string{"docs", "report", "summary"}, []string{third}, 900, 420, 0.25),
	}
}

func debateSubtasks(first, second, third, judge string) []subtaskSpec {
	return []subtaskSpec{
		simpleSubtask(first, "research", "Debate position "+first, []string{"research", "synthesis"}, 1100, 540, 0.35),
		simpleSubtask(second, "security", "Debate position "+second, []string{"security", "risk", "policy"}, 1100, 540, 0.45),
		simpleSubtask(third, "data", "Debate position "+third, []string{"data", "metric", "analysis"}, 1100, 540, 0.35),
		simpleSubtask(judge, "critic", "Judge debate "+judge, []string{"critic", "consensus", "review"}, 900, 440, 0.35),
	}
}

func autonomySubtasks(first, second, third, fourth string) []subtaskSpec {
	return []subtaskSpec{
		simpleSubtask(first, "ops", "Autonomy step "+first, []string{"ops", "runtime"}, 850, 350, 0.50),
		dependentSubtask(second, "test", "Autonomy step "+second, []string{"test", "benchmark", "validation"}, []string{first}, 2200, 820, 0.55),
		dependentSubtask(third, "data", "Autonomy step "+third, []string{"data", "metric", "experiment"}, []string{second}, 1500, 620, 0.40),
		dependentSubtask(fourth, "docs", "Autonomy step "+fourth, []string{"docs", "report", "summary"}, []string{third}, 900, 400, 0.25),
	}
}

func heavyPipelineSubtasks(first, second, third, fourth, fifth, sixth string) []subtaskSpec {
	return []subtaskSpec{
		simpleSubtask(first, "repo", "Heavy step "+first, []string{"repo", "code", "search"}, 1800, 760, 0.50),
		dependentSubtask(second, "data", "Heavy step "+second, []string{"data", "analysis", "metric"}, []string{first}, 2200, 900, 0.65),
		dependentSubtask(third, "backend", "Heavy step "+third, []string{"backend", "go", "service"}, []string{second}, 3000, 1180, 0.90),
		dependentSubtask(fourth, "test", "Heavy step "+fourth, []string{"test", "benchmark", "validation"}, []string{third}, 2600, 980, 0.70),
		dependentSubtask(fifth, "security", "Heavy step "+fifth, []string{"security", "risk", "review"}, []string{fourth}, 2000, 820, 0.70),
		dependentSubtask(sixth, "docs", "Heavy step "+sixth, []string{"docs", "report", "summary"}, []string{fifth}, 1400, 600, 0.35),
	}
}
