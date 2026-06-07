package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yurika0211/luckyharness/internal/provider"
)

type replayFile struct {
	Cases []replayCase `json:"cases"`
}

type replayCase struct {
	ID                   string             `json:"id"`
	Scenario             string             `json:"scenario,omitempty"`
	Title                string             `json:"title,omitempty"`
	Prompt               string             `json:"prompt,omitempty"`
	Query                string             `json:"query,omitempty"`
	Messages             []provider.Message `json:"messages,omitempty"`
	GoldMode             string             `json:"gold_mode,omitempty"`
	ShouldSplit          *bool              `json:"should_split,omitempty"`
	NeedsVerifier        *bool              `json:"needs_verifier,omitempty"`
	NeedsCritic          *bool              `json:"needs_critic,omitempty"`
	AllowsBackground     *bool              `json:"allows_background,omitempty"`
	ActualSuccess        *bool              `json:"actual_success,omitempty"`
	SubtaskDependencies  []string           `json:"subtask_dependencies,omitempty"`
	RequiredCapabilities []string           `json:"required_capabilities,omitempty"`
	ForbiddenModes       []string           `json:"forbidden_modes,omitempty"`
}

type replaySessionData struct {
	ID       string             `json:"id"`
	Title    string             `json:"title"`
	Messages []provider.Message `json:"messages"`
}

type replayLabelRecord struct {
	Type                 string   `json:"type"`
	ID                   string   `json:"id"`
	Scenario             string   `json:"scenario"`
	Title                string   `json:"title,omitempty"`
	Prompt               string   `json:"prompt"`
	GoldMode             string   `json:"gold_mode"`
	ShouldSplit          bool     `json:"should_split"`
	NeedsVerifier        bool     `json:"needs_verifier"`
	NeedsCritic          bool     `json:"needs_critic"`
	AllowsBackground     bool     `json:"allows_background"`
	ForbiddenModes       []string `json:"forbidden_modes,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	TaskType             string   `json:"task_type"`
	Confidence           float64  `json:"confidence"`
	LabelSource          string   `json:"label_source"`
	Evidence             []string `json:"evidence,omitempty"`
	ReviewRequired       bool     `json:"review_required"`
}

func runReplayLabelExport(cfg benchConfig) error {
	if strings.TrimSpace(cfg.ReplayPath) == "" {
		return fmt.Errorf("-replay is required when -replay-label-out is set")
	}
	cases, err := loadReplayCases(cfg.ReplayPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.ReplayLabelOut), 0o700); err != nil {
		return fmt.Errorf("create replay label output dir: %w", err)
	}
	out, err := os.OpenFile(cfg.ReplayLabelOut, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open replay label output: %w", err)
	}
	defer out.Close()
	enc := json.NewEncoder(out)
	for i, c := range cases {
		rec, err := replayCaseToLabelRecord(c, i)
		if err != nil {
			return err
		}
		if err := enc.Encode(rec); err != nil {
			return fmt.Errorf("write replay label: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "replay labels: %s (%d cases)\n", cfg.ReplayLabelOut, len(cases))
	return nil
}

func loadReplayTasks(path string) ([]benchTask, error) {
	cases, err := loadReplayCases(path)
	if err != nil {
		return nil, err
	}
	tasks := make([]benchTask, 0, len(cases))
	for i, c := range cases {
		task, err := replayCaseToTask(c, i)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return normalizeTasks(tasks), nil
}

func loadReplayCases(path string) ([]replayCase, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat replay path %s: %w", path, err)
	}
	var cases []replayCase
	if info.IsDir() {
		cases, err = loadReplayDir(path)
	} else {
		cases, err = loadReplayFile(path)
	}
	if err != nil {
		return nil, err
	}
	return cases, nil
}

func loadReplayDir(dir string) ([]replayCase, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".json", ".jsonl", ".md":
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk replay dir %s: %w", dir, err)
	}
	sort.Strings(paths)
	var out []replayCase
	for _, path := range paths {
		cases, err := loadReplayFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, cases...)
	}
	return out, nil
}

func loadReplayFile(path string) ([]replayCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read replay file %s: %w", path, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, fmt.Errorf("replay file %s is empty", path)
	}
	if strings.EqualFold(filepath.Ext(path), ".md") {
		text = extractJSONFence(text)
	}
	var file replayFile
	if err := json.Unmarshal([]byte(text), &file); err == nil && len(file.Cases) > 0 {
		return file.Cases, nil
	}
	var single replayCase
	if err := json.Unmarshal([]byte(text), &single); err == nil && replayCaseHasContent(single) {
		return []replayCase{single}, nil
	}
	var session replaySessionData
	if err := json.Unmarshal([]byte(text), &session); err == nil && len(session.Messages) > 0 {
		return []replayCase{{ID: session.ID, Title: session.Title, Messages: session.Messages}}, nil
	}
	var out []replayCase
	for lineNo, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c replayCase
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("decode replay file %s line %d: %w", path, lineNo+1, err)
		}
		if !replayCaseHasContent(c) {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("replay file %s has no cases", path)
	}
	return out, nil
}

func replayCaseHasContent(c replayCase) bool {
	return strings.TrimSpace(c.Prompt) != "" || strings.TrimSpace(c.Query) != "" || len(c.Messages) > 0
}

func replayCaseToTask(c replayCase, index int) (benchTask, error) {
	rec, err := replayCaseToLabelRecord(c, index)
	if err != nil {
		return benchTask{}, err
	}
	subtasks := replaySubtasksForMode(rec.GoldMode, rec.Prompt, rec.NeedsCritic, rec.AllowsBackground)
	difficulty := replayDifficulty(rec.Prompt, subtasks, rec.NeedsCritic)
	riskBudget := replayRiskBudget(rec.GoldMode, difficulty, rec.NeedsCritic)
	return benchTask{
		ID:                   rec.ID,
		Scenario:             rec.Scenario,
		TaskType:             rec.TaskType,
		Prompt:               rec.Prompt,
		GoldMode:             rec.GoldMode,
		RequiredCapabilities: rec.RequiredCapabilities,
		Subtasks:             subtasks,
		IntentTerms:          replayIntentTerms(rec.Prompt),
		ForbiddenModes:       rec.ForbiddenModes,
		NeedsCritic:          rec.NeedsCritic,
		AllowsBackground:     rec.AllowsBackground,
		RiskBudget:           riskBudget,
		Difficulty:           difficulty,
	}, nil
}

func replayCaseToLabelRecord(c replayCase, index int) (replayLabelRecord, error) {
	prompt := firstNonEmpty(c.Prompt, c.Query, lastUserMessage(c.Messages), c.Title)
	if strings.TrimSpace(prompt) == "" {
		return replayLabelRecord{}, fmt.Errorf("replay case %d has no prompt or user message", index+1)
	}
	id := strings.TrimSpace(c.ID)
	if id == "" {
		id = fmt.Sprintf("R7-%03d", index+1)
	}
	mode := normalizeMode(c.GoldMode)
	if mode == "" || mode == "single" && strings.TrimSpace(c.GoldMode) == "" {
		mode = inferReplayMode(c, prompt)
	}
	allowsBackground := boolValue(c.AllowsBackground) || mode == "autonomy_queue" || containsAny(prompt, "后台", "异步", "队列", "worker", "background", "async")
	needsCritic := boolValue(c.NeedsCritic) || boolValue(c.NeedsVerifier) || mode == "debate" || containsAny(prompt, "验收", "verifier", "debugger", "审计", "安全", "风险", "review")
	scenario := normalizeReplayScenario(c.Scenario)
	taskType := inferReplayTaskType(prompt, mode)
	forbidden := append([]string(nil), c.ForbiddenModes...)
	if mode == "single" && len(forbidden) == 0 {
		forbidden = []string{"parallel", "pipeline", "debate", "autonomy_queue"}
	}
	if mode != "autonomy_queue" && !allowsBackground {
		forbidden = append(forbidden, "autonomy_queue")
	}
	confidence, evidence := replayLabelConfidence(c, prompt, mode, needsCritic, allowsBackground)
	return replayLabelRecord{
		Type:                 "replay_label",
		ID:                   id,
		Scenario:             scenario,
		Title:                c.Title,
		TaskType:             taskType,
		Prompt:               prompt,
		GoldMode:             mode,
		RequiredCapabilities: c.RequiredCapabilities,
		ForbiddenModes:       uniqueStrings(forbidden),
		ShouldSplit:          mode != "single",
		NeedsCritic:          needsCritic,
		NeedsVerifier:        boolValue(c.NeedsVerifier) || needsCritic,
		AllowsBackground:     allowsBackground,
		Confidence:           confidence,
		LabelSource:          replayLabelSource(c),
		Evidence:             evidence,
		ReviewRequired:       confidence < 0.75 || mode == "debate" || taskType == "replay_hermes",
	}, nil
}

func inferReplayMode(c replayCase, prompt string) string {
	if c.ShouldSplit != nil && !*c.ShouldSplit {
		return "single"
	}
	if c.AllowsBackground != nil && *c.AllowsBackground || containsAny(prompt, "后台", "异步", "队列", "worker", "background", "async") {
		return "autonomy_queue"
	}
	if containsAny(prompt, "不要拆", "不用子代理", "只查看", "只检查", "只解释", "不需要子代理") {
		return "single"
	}
	if c.NeedsCritic != nil && *c.NeedsCritic || containsAny(prompt, "辩论", "正反", "裁决", "debate") {
		return "debate"
	}
	if len(c.SubtaskDependencies) > 0 || containsAny(prompt, "先", "再", "最后", "随后", "迁移", "上线", "回滚", "训练") {
		return "pipeline"
	}
	if c.ShouldSplit != nil && *c.ShouldSplit || containsAny(prompt, "并行", "分别", "多个", "子代理", "三路", "四路") {
		return "parallel"
	}
	return "single"
}

func replaySubtasksForMode(mode, prompt string, needsCritic, allowsBackground bool) []subtaskSpec {
	switch mode {
	case "single":
		return []subtaskSpec{simpleSubtask("answer", replayRoleForPrompt(prompt), "Answer historical task", replayCapsForPrompt(prompt), 1000, 460, replaySubtaskRisk(prompt))}
	case "parallel":
		return []subtaskSpec{
			simpleSubtask("repo-track", "repo", "Inspect repository track", []string{"repo", "code", "search"}, 1400, 560, 0.35),
			simpleSubtask("validation-track", "test", "Inspect validation track", []string{"test", "benchmark", "validation"}, 1450, 600, 0.40),
			simpleSubtask("docs-track", "docs", "Inspect reporting track", []string{"docs", "report", "summary"}, 950, 420, 0.25),
			simpleSubtask("merge", "integrator", "Merge replay findings", []string{"integration", "aggregation", "summary"}, 850, 380, 0.25),
		}
	case "pipeline":
		return []subtaskSpec{
			simpleSubtask("inspect", "repo", "Inspect historical scope", []string{"repo", "code", "search"}, 1100, 480, 0.35),
			dependentSubtask("implement", "backend", "Apply ordered change", []string{"backend", "go", "code"}, []string{"inspect"}, 1650, 680, 0.70),
			dependentSubtask("validate", "test", "Validate ordered change", []string{"test", "validation", "benchmark"}, []string{"implement"}, 1400, 560, 0.45),
			dependentSubtask("report", "docs", "Report replay result", []string{"docs", "report", "summary"}, []string{"validate"}, 900, 400, 0.25),
		}
	case "debate":
		return debateSubtasks("proposal-a", "proposal-b", "metric-view", "judge")
	case "autonomy_queue":
		return autonomySubtasks("enqueue", "run-background", "persist-trace", "notify")
	default:
		return []subtaskSpec{simpleSubtask("answer", replayRoleForPrompt(prompt), "Answer historical task", replayCapsForPrompt(prompt), 1000, 460, replaySubtaskRisk(prompt))}
	}
}

func replayRoleForPrompt(prompt string) string {
	switch {
	case containsAny(prompt, "测试", "benchmark", "验证", "ci"):
		return "test"
	case containsAny(prompt, "文档", "报告", "总结", "README"):
		return "docs"
	case containsAny(prompt, "安全", "权限", "风险", "审计"):
		return "security"
	case containsAny(prompt, "前端", "ui", "react", "dashboard"):
		return "frontend"
	case containsAny(prompt, "后端", "api", "go", "服务"):
		return "backend"
	default:
		return "repo"
	}
}

func replayCapsForPrompt(prompt string) []string {
	role := replayRoleForPrompt(prompt)
	switch role {
	case "test":
		return []string{"test", "benchmark", "validation"}
	case "docs":
		return []string{"docs", "report", "summary"}
	case "security":
		return []string{"security", "risk", "review"}
	case "frontend":
		return []string{"frontend", "ui", "typescript"}
	case "backend":
		return []string{"backend", "go", "service"}
	default:
		return []string{"repo", "code", "search"}
	}
}

func replaySubtaskRisk(prompt string) float64 {
	risk := 0.25
	if containsAny(prompt, "commit", "push", "删除", "权限", "安全", "上线", "发布", "回滚") {
		risk += 0.35
	}
	if containsAny(prompt, "只查看", "只解释", "不要拆") {
		risk -= 0.08
	}
	return clamp(risk, 0.15, 0.85)
}

func replayDifficulty(prompt string, subtasks []subtaskSpec, needsCritic bool) float64 {
	difficulty := 0.40 + 0.05*float64(len(subtasks))
	if len([]rune(prompt)) > 80 {
		difficulty += 0.10
	}
	if needsCritic {
		difficulty += 0.12
	}
	if containsAny(prompt, "Hermes", "多 agent", "multi agent", "runtime", "上线", "训练", "回放") {
		difficulty += 0.12
	}
	return clamp(difficulty, 0.25, 0.98)
}

func replayRiskBudget(mode string, difficulty float64, needsCritic bool) float64 {
	budget := 0.45 + difficulty
	if mode != "single" {
		budget += 0.35
	}
	if needsCritic {
		budget += 0.25
	}
	return clamp(budget, 0.25, 2.4)
}

func inferReplayTaskType(prompt, mode string) string {
	switch {
	case containsAny(prompt, "Hermes"):
		return "replay_hermes"
	case containsAny(prompt, "benchmark", "实验"):
		return "replay_benchmark"
	case containsAny(prompt, "commit", "push", "git"):
		return "replay_release"
	case containsAny(prompt, "文档", "报告", "总结"):
		return "replay_docs"
	default:
		return "replay_" + mode
	}
}

func normalizeReplayScenario(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "replay"
	}
	raw = strings.NewReplacer(" ", "_", "-", "_").Replace(raw)
	if strings.Trim(raw, "_") == "" {
		return "replay"
	}
	return raw
}

func replayIntentTerms(prompt string) []string {
	terms := []string{}
	for _, term := range []string{"hermes", "agent", "benchmark", "trace", "replay", "runtime", "dry-run", "测试", "验证", "报告", "后台", "异步", "辩论", "并行"} {
		if containsAny(prompt, term) {
			terms = append(terms, term)
		}
	}
	return terms
}

func replayLabelConfidence(c replayCase, prompt, mode string, needsCritic, allowsBackground bool) (float64, []string) {
	conf := 0.55
	var evidence []string
	if strings.TrimSpace(c.GoldMode) != "" {
		conf += 0.30
		evidence = append(evidence, "gold_mode provided")
	}
	if c.ShouldSplit != nil {
		conf += 0.08
		evidence = append(evidence, "should_split provided")
	}
	if c.NeedsVerifier != nil || c.NeedsCritic != nil {
		conf += 0.06
		evidence = append(evidence, "verifier/critic label provided")
	}
	if mode != "single" && containsAny(prompt, "先", "再", "最后", "并行", "分别", "辩论", "后台", "异步", "队列", "worker") {
		conf += 0.08
		evidence = append(evidence, "prompt contains orchestration cue")
	}
	if mode == "single" && containsAny(prompt, "不要拆", "只查看", "只检查", "只解释", "不需要子代理") {
		conf += 0.10
		evidence = append(evidence, "prompt contains single-agent guard")
	}
	if needsCritic {
		evidence = append(evidence, "verifier/critic required")
	}
	if allowsBackground {
		evidence = append(evidence, "background execution allowed")
	}
	return clamp(conf, 0.05, 0.99), uniqueStrings(evidence)
}

func replayLabelSource(c replayCase) string {
	if strings.TrimSpace(c.GoldMode) != "" || c.ShouldSplit != nil || c.NeedsVerifier != nil || c.NeedsCritic != nil || c.AllowsBackground != nil {
		return "provided+heuristic"
	}
	return "heuristic"
}

func lastUserMessage(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolValue(v *bool) bool {
	return v != nil && *v
}

func extractJSONFence(text string) string {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "```json") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return text
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "```" {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}
