package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	delegateruntime "github.com/yurika0211/luckyagent-orchestrator/delegate"
	"github.com/yurika0211/lucky-agent/agent/intentclassify"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/memory"
)

// skillComparisonToolName is deliberately a host tool. The tools module only
// supplies ranked candidates; the host owns the safety gate, execution and
// evidence comparison because it owns the Agent loop and memory store.
const skillComparisonToolName = "skill_compare"

// skillComparisonContextMarker keeps a comparison child on the ordinary tool
// set. An untyped delegate otherwise becomes the read-only explore role.
const skillComparisonContextMarker = "\x00skill_comparison"

// repeatSafety is the host decision for a dual run. The published tool still
// accepts safe_to_repeat from the caller; when RepeatSafety is set, that
// decision replaces the description-word list.
type skillRepeatSafetyFunc func(description string) (safe bool, reason string)

var skillRepeatSafety skillRepeatSafetyFunc

// skillComparisonMeter records cost and time produced while a candidate runs.
// The delegate status response does not always carry those numbers.
type skillComparisonMeter struct {
	mu      sync.Mutex
	calls   map[string]int
	elapsed map[string]int64
}

func (m *skillComparisonMeter) record(taskID string, calls int, elapsedMS int64) {
	if m == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls == nil {
		m.calls = map[string]int{}
		m.elapsed = map[string]int64{}
	}
	if calls > 0 {
		m.calls[taskID] = calls
	}
	if elapsedMS > 0 {
		m.elapsed[taskID] = elapsedMS
	}
}

func (m *skillComparisonMeter) get(taskID string) (int, int64) {
	if m == nil {
		return 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[taskID], m.elapsed[taskID]
}

type skillComparisonCandidate struct {
	Skill           string   `json:"skill"`
	TaskID          string   `json:"task_id"`
	Status          string   `json:"status"`
	Complete        bool     `json:"complete"`
	CompletionNote  string   `json:"completion_note,omitempty"`
	ToolCalls       int      `json:"tool_calls"`
	ElapsedMS       int64    `json:"elapsed_ms"`
	CostUnits       int      `json:"cost_units,omitempty"`
	Score           float64  `json:"score,omitempty"`
	ResultSummary   string   `json:"result_summary,omitempty"`
	Result          string   `json:"result,omitempty"`
	CostAvailable   bool     `json:"cost_available"`
	EvidenceSources []string `json:"evidence_sources,omitempty"`
}

type skillComparisonResponse struct {
	ComparisonID   string                     `json:"comparison_id"`
	Status         string                     `json:"status"`
	Safe           bool                       `json:"safe_to_repeat"`
	Candidates     []skillComparisonCandidate `json:"candidates"`
	Winner         string                     `json:"winner,omitempty"`
	Reason         string                     `json:"reason"`
	MemorySaved    bool                       `json:"memory_saved"`
	ComparisonCost int                        `json:"comparison_cost_units"`
	TaskCost       int                        `json:"task_cost_units,omitempty"`
}

// installSkillComparisonTool exposes the only entry point that may start a
// safe dual run. It is installed once during normal Agent assembly; the guard
// also makes tests and embedders that assemble tools manually idempotent.
func (a *Agent) installSkillComparisonTool() {
	if a == nil || a.tools == nil {
		return
	}
	if _, ok := a.tools.Get(skillComparisonToolName); ok {
		return
	}
	a.tools.Register(&tool.Tool{
		Name:        skillComparisonToolName,
		Description: "For an explicitly repeat-safe, idempotent task with no learned skill preference, run the two named candidate skills in isolated child tasks and choose only a completed result with the lower cost × time score.",
		Category:    tool.CatDelegate,
		Source:      "builtin",
		Permission:  tool.PermAuto,
		Enabled:     true,
		Parameters: map[string]tool.Param{
			"description":    {Type: "string", Description: "The same task goal to give both candidates. Do not include credentials.", Required: true},
			"skill_a":        {Type: "string", Description: "First ranked candidate skill name.", Required: true},
			"skill_b":        {Type: "string", Description: "Second ranked candidate skill name.", Required: true},
			"safe_to_repeat": {Type: "boolean", Description: "Must be true only when repeating the task has no external side effect.", Required: true},
			"timeout":        {Type: "number", Description: "Shared child timeout in seconds (default 120).", Required: false, Default: 120},
		},
		ContextDetailedHandler: func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
			out, err := a.compareSkills(exec, args)
			return tool.ToolCallResult{Output: out}, err
		},
	})
}

func (a *Agent) compareSkills(exec tool.ExecutionContext, args map[string]any) (string, error) {
	description := strings.TrimSpace(skillCompareStringArg(args, "description"))
	skillA := strings.TrimSpace(skillCompareStringArg(args, "skill_a"))
	skillB := strings.TrimSpace(skillCompareStringArg(args, "skill_b"))
	if description == "" || skillA == "" || skillB == "" {
		return "", fmt.Errorf("description, skill_a and skill_b are required")
	}
	if strings.EqualFold(skillA, skillB) {
		return "", fmt.Errorf("skill_a and skill_b must be different")
	}
	if !boolArg(args, "safe_to_repeat") {
		return "", fmt.Errorf("refusing dual run: safe_to_repeat must be true")
	}
	if reason := skillComparisonSafetyReason(description); reason != "" {
		return "", fmt.Errorf("refusing dual run: %s", reason)
	}
	if preferred, prefErr := a.preferredSkillFromMemory(description, a.snapshotSkills()); prefErr != nil {
		return "", fmt.Errorf("refusing dual run: %w", prefErr)
	} else if preferred != nil {
		return "", fmt.Errorf("refusing dual run: an explicit learned skill preference already exists")
	}
	if !a.isRankedSkillCandidate(description, skillA) || !a.isRankedSkillCandidate(description, skillB) {
		return "", fmt.Errorf("refusing dual run: both skills must be ranked candidates for this request")
	}
	if a.delegate == nil {
		return "", fmt.Errorf("refusing dual run: delegation is unavailable")
	}
	meter := &skillComparisonMeter{}
	a.comparisonMeter = meter

	ctx := exec.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	timeout := comparisonTimeout(args, a.delegate.Config().Timeout)
	delegate := tool.DelegateTaskTool(a.delegate)
	start := func(skill string) (delegateruntime.StartResponse, error) {
		contextText := skillComparisonContextMarker + "\n" + fmt.Sprintf("This is one isolated candidate of a safe skill comparison. Use only the %q skill. Do not send, write, modify, deploy, purchase, schedule, or otherwise cause an external side effect. If the goal would require a side effect, stop and report that the candidate is unsafe or incomplete. Return concrete completion evidence and any files/tests actually inspected.", skill)
		result, err := delegate.ContextDetailedHandler(tool.ExecutionContext{
			Context: ctx, SessionID: exec.SessionID, Source: exec.Source, UserID: exec.UserID,
			UserRequest: description, AutoApprove: true, Sandbox: exec.Sandbox,
		}, map[string]any{
			"description":  fmt.Sprintf("Use skill %s to complete this repeat-safe task: %s", skill, description),
			"context":      contextText,
			"timeout":      int(timeout.Seconds()),
			"mode":         "single",
			"max_children": 1,
		})
		if err != nil {
			return delegateruntime.StartResponse{}, err
		}
		var started delegateruntime.StartResponse
		if err := json.Unmarshal([]byte(result.Output), &started); err != nil {
			return delegateruntime.StartResponse{}, fmt.Errorf("decode delegated %s result: %w", skill, err)
		}
		if strings.TrimSpace(started.TaskID) == "" {
			return delegateruntime.StartResponse{}, fmt.Errorf("delegated %s task returned no task_id", skill)
		}
		return started, nil
	}
	first, err := start(skillA)
	if err != nil {
		return "", err
	}
	second, err := start(skillB)
	if err != nil {
		_ = a.delegate.CancelTask(first.TaskID, "skill comparison second candidate could not start")
		return "", err
	}
	waitJSON, err := a.delegate.HandleWaitForTasksForSession(ctx, map[string]any{
		"task_ids": []any{first.TaskID, second.TaskID},
		"timeout":  timeout.Seconds(), "poll_interval_ms": 100, "include_result": true,
	}, strings.TrimSpace(exec.SessionID))
	if err != nil {
		return "", err
	}
	var waited delegateruntime.WaitResponse
	if err := json.Unmarshal([]byte(waitJSON), &waited); err != nil {
		return "", fmt.Errorf("decode skill comparison: %w", err)
	}
	byID := make(map[string]delegateruntime.StatusResponse, len(waited.Tasks))
	for _, status := range waited.Tasks {
		byID[status.TaskID] = status
	}
	candidates := []skillComparisonCandidate{
		comparisonCandidate(skillA, a.measuredDelegateStatus(byID[first.TaskID])),
		comparisonCandidate(skillB, a.measuredDelegateStatus(byID[second.TaskID])),
	}
	response := skillComparisonResponse{
		ComparisonID: comparisonID(description, skillA, skillB),
		Safe:         true, Candidates: candidates, MemorySaved: false,
		ComparisonCost: comparisonOverhead(candidates),
	}
	complete := make([]int, 0, 2)
	for i := range candidates {
		if candidates[i].Complete && candidates[i].CostAvailable {
			complete = append(complete, i)
		}
	}
	switch len(complete) {
	case 0:
		response.Status = "no_winner"
		response.Reason = "Neither candidate supplied both completion evidence and a measurable cost; no winner was selected or saved."
	case 1:
		winner := complete[0]
		response.Status, response.Winner = "selected", candidates[winner].Skill
		response.Reason = fmt.Sprintf("Only %q completed with measurable evidence; incomplete, partial, failed, or unmeasured results cannot win.", candidates[winner].Skill)
		response.MemorySaved = a.saveSkillComparisonPreference(description, candidates[winner], candidates[1-winner])
	case 2:
		winner := complete[0]
		if candidates[1].Score < candidates[0].Score {
			winner = 1
		}
		response.Status, response.Winner = "selected", candidates[winner].Skill
		response.Reason = fmt.Sprintf("Selected the completed candidate with the lower score = cost × time (%s).", scoreComparison(candidates[0], candidates[1]))
		response.MemorySaved = a.saveSkillComparisonPreference(description, candidates[winner], candidates[1-winner])
	}
	return marshalSkillComparison(response)
}

// measuredDelegateStatus copies cost and elapsed time from the delegate task
// the child execution recorded. Wait responses can omit both.
func (a *Agent) measuredDelegateStatus(status delegateruntime.StatusResponse) delegateruntime.StatusResponse {
	if a == nil || strings.TrimSpace(status.TaskID) == "" {
		return status
	}
	if a.delegate != nil {
		raw, err := a.delegate.HandleStatus(map[string]any{"task_id": status.TaskID, "include_result": true})
		if err == nil {
			var measured delegateruntime.StatusResponse
			if json.Unmarshal([]byte(raw), &measured) == nil {
				if measured.ToolCalls > 0 {
					status.ToolCalls = measured.ToolCalls
				}
				if measured.ElapsedMS > 0 {
					status.ElapsedMS = measured.ElapsedMS
				}
				if strings.TrimSpace(measured.Error) != "" && strings.TrimSpace(status.Error) == "" {
					status.Error = measured.Error
				}
				if strings.TrimSpace(measured.Result) != "" && strings.TrimSpace(status.Result) == "" {
					status.Result = measured.Result
				}
			}
		}
	}
	if a.comparisonMeter != nil {
		calls, elapsed := a.comparisonMeter.get(status.TaskID)
		if status.ToolCalls == 0 && calls > 0 {
			status.ToolCalls = calls
		}
		if status.ElapsedMS == 0 && elapsed > 0 {
			status.ElapsedMS = elapsed
		}
	}
	return status
}

// RecordSkillComparisonCost stores the cost and time a candidate execution
// produced. compareSkills reads it when the delegate status omits both.
func (a *Agent) RecordSkillComparisonCost(taskID string, toolCalls int, elapsed time.Duration) {
	if a == nil {
		return
	}
	if a.comparisonMeter == nil {
		a.comparisonMeter = &skillComparisonMeter{}
	}
	a.comparisonMeter.record(taskID, toolCalls, elapsed.Milliseconds())
}

func comparisonCandidate(skill string, status delegateruntime.StatusResponse) skillComparisonCandidate {
	c := skillComparisonCandidate{Skill: skill, TaskID: status.TaskID, Status: status.Status, ToolCalls: status.ToolCalls, ElapsedMS: status.ElapsedMS, ResultSummary: strings.TrimSpace(status.ResultSummary), Result: strings.TrimSpace(status.Result), CostAvailable: status.ElapsedMS > 0 && status.ToolCalls > 0}
	c.Complete, c.CompletionNote, c.EvidenceSources = skillResultComplete(status)
	if c.CostAvailable {
		c.CostUnits = status.ToolCalls
		c.Score = float64(c.CostUnits) * float64(c.ElapsedMS)
	}
	return c
}



func skillResultComplete(status delegateruntime.StatusResponse) (bool, string, []string) {
	if !strings.EqualFold(strings.TrimSpace(status.Status), "completed") {
		return false, "task did not reach completed status", nil
	}
	result := strings.ToLower(strings.TrimSpace(status.Result + "\n" + status.ResultSummary))
	if result == "" {
		return false, "completed task returned no result evidence", nil
	}
	for _, word := range []string{"partial", "incomplete", "not completed", "failed", "blocked", "unsafe"} {
		if strings.Contains(result, word) {
			return false, "result explicitly reports an incomplete or unsafe outcome", nil
		}
	}
	evidence := []string{"task status: completed", "non-empty result summary"}
	if status.ToolCalls > 0 {
		evidence = append(evidence, fmt.Sprintf("%d tool calls recorded", status.ToolCalls))
	}
	return true, "completed with result evidence", evidence
}

func comparisonPreferenceText(description string, winner, loser skillComparisonCandidate) string {
	return fmt.Sprintf("System comparison preference: skill %q beat skill %q on comparison %s; lower score = cost × time (%s vs %s). This is system comparison evidence, not a user-stated preference.", winner.Skill, loser.Skill, comparisonID(description, winner.Skill, loser.Skill), formatScore(winner.Score), formatScore(loser.Score))
}

func (a *Agent) saveSkillComparisonPreference(description string, winner, loser skillComparisonCandidate) bool {
	if a == nil || a.memory == nil || winner.Skill == "" || loser.Skill == "" {
		return false
	}
	content := comparisonPreferenceText(description, winner, loser)
	_, err := a.WriteMemory(context.Background(), memory.RememberRequest{
		Content: content, Category: "preference", Tier: memory.TierLong, Importance: 0.72,
		Options: memory.SaveOptions{
			Tags:   []string{"system_comparison", "skill_preference", "source:comparison"},
			Status: "active", Confidence: 0.72,
		},
	})
	return err == nil
}

func skillComparisonSafetyReason(description string) string {
	checker := skillRepeatSafety
	if checker == nil {
		checker = defaultSkillRepeatSafety
	}
	safe, reason := checker(description)
	if safe {
		return ""
	}
	if strings.TrimSpace(reason) == "" {
		reason = "the existing safety check says this task must not be repeated"
	}
	return reason
}

// defaultSkillRepeatSafety asks the existing turn classifier whether the task
// would mutate, send, schedule, or otherwise leave this process. Those labels
// already gate tools. A second skill run is refused for the same labels.
func defaultSkillRepeatSafety(description string) (bool, string) {
	classified := intentclassify.Classify(description)
	if !classified.Recognized() {
		return true, ""
	}
	blocking := []intentclassify.Label{
		intentclassify.LabelEdit,
		intentclassify.LabelHTTP,
		intentclassify.LabelCron,
		intentclassify.LabelAutonomy,
		intentclassify.LabelHeartbeat,
		intentclassify.LabelComputer,
		intentclassify.LabelDelegate,
		intentclassify.LabelSubagent,
		intentclassify.LabelRemember,
		intentclassify.LabelRAGIndex,
	}
	for _, label := range blocking {
		if classified.Has(label) && !classified.Has(intentclassify.LabelNoExecute) && !classified.Has(intentclassify.LabelReadOnlyMutation) && !classified.Has(intentclassify.LabelNoMemoryWrite) {
			return false, "existing intent classifier marks this task as not repeat-safe (" + string(label) + ")"
		}
	}
	return true, ""
}

func (a *Agent) isRankedSkillCandidate(description, name string) bool {
	for _, candidate := range a.skillRouteCandidates(description) {
		if candidate != nil && strings.EqualFold(strings.TrimSpace(candidate.skill.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func comparisonTimeout(args map[string]any, fallback time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = 120 * time.Second
	}
	seconds := fallback.Seconds()
	switch raw := args["timeout"].(type) {
	case float64:
		if raw > 0 {
			seconds = raw
		}
	case int:
		if raw > 0 {
			seconds = float64(raw)
		}
	case json.Number:
		if value, err := raw.Float64(); err == nil && value > 0 {
			seconds = value
		}
	}
	if seconds < 5 {
		seconds = 5
	}
	if seconds > 1800 {
		seconds = 1800
	}
	return time.Duration(seconds * float64(time.Second))
}

func comparisonID(description, a, b string) string {
	payload := []byte(strings.Join([]string{description, a, b}, "\x00"))
	digest := sha256.Sum256(payload)
	return "skill-compare-" + hex.EncodeToString(digest[:8])
}

func scoreComparison(a, b skillComparisonCandidate) string {
	return fmt.Sprintf("%s=%s, %s=%s", a.Skill, formatScore(a.Score), b.Skill, formatScore(b.Score))
}

func formatScore(score float64) string { return fmt.Sprintf("%.0f", score) }

// comparisonOverhead is the extra work of running the comparison itself.
// It is not added to either candidate's task score.
func comparisonOverhead(candidates []skillComparisonCandidate) int {
	total := 0
	for _, candidate := range candidates {
		if candidate.ToolCalls > 0 {
			total += candidate.ToolCalls
		}
	}
	if total == 0 {
		return 0
	}
	return total
}

func marshalSkillComparison(response skillComparisonResponse) (string, error) {
	// Stable candidate order makes the audit record reproducible and prevents a
	// completion race from changing which candidate is shown first.
	sort.SliceStable(response.Candidates, func(i, j int) bool { return response.Candidates[i].Skill < response.Candidates[j].Skill })
	encoded, err := json.Marshal(response)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func skillCompareStringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, _ := args[key].(string)
	return value
}

func boolArg(args map[string]any, key string) bool {
	value, ok := args[key].(bool)
	return ok && value
}
