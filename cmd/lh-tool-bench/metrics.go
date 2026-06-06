package main

import (
	"math"
	"sort"
	"strings"
)

type taskMetrics struct {
	NeedPredictionCorrect  bool
	RequiredToolCount      int
	RequiredOperationCount int
	CalledToolCount        int
	CalledOperationCount   int
	ToolHitCount           int
	OperationHitCount      int
	ForbiddenCallCount     int
	RedundantCalls         int
	ToolRecall             float64
	ToolPrecision          float64
	OperationRecall        float64
	OperationPrecision     float64
	RedundantRate          float64
	RouteRisk              float64
	ExpectedRouteRisk      float64
	ToolTokenCost          int
	ToolResultTokens       int
	IrrelevantTokens       int
	ToolResultNoise        float64
	ToolAlignment          float64
	InfoGain               float64
	InfoEfficiency         float64
	ExpectedUtility        float64
	SuccessProbability     float64
	ToolTuneScore          float64
	Clean                  bool
	Success                bool
}

func evaluateTask(cfg benchConfig, task benchTask, result strategyResult, tools map[string]toolSpec, ops map[string]operationSpec) taskMetrics {
	calledTools := callToolNames(result.Calls)
	calledOps := callOperationNames(result.Calls)
	requiredTools := uniqueStrings(task.RequiredTools)
	requiredOps := uniqueStrings(task.RequiredOperations)
	forbiddenTools := uniqueStrings(task.ForbiddenTools)
	forbiddenOps := uniqueStrings(task.ForbiddenOperations)

	toolHits := countIntersection(calledTools, requiredTools)
	opHits := countIntersection(calledOps, requiredOps)
	forbiddenCalls := countIntersection(calledTools, forbiddenTools) + countIntersection(calledOps, forbiddenOps)
	redundantCalls := countRedundantCalls(task, calledOps, ops)
	routeRisk := routeRiskScore(task, result.Calls, ops)
	expectedRisk := expectedRouteRisk(task, result, tools, ops)
	descTokens := visibleToolDescTokens(result.VisibleTools, tools)
	resultTokens, irrelevantTokens := resultTokenStats(result.Calls, result.Packed, ops)
	tokenCost := descTokens + resultTokens
	alignment := toolAlignment(task.IntentTerms, result.Calls, ops)
	infoGain := informationGain(task, calledOps, opHits, redundantCalls, forbiddenCalls)
	infoCost := float64(tokenCost)/1000.0 + routeRisk
	if infoCost <= 0 {
		infoCost = 1
	}
	infoEfficiency := infoGain / infoCost
	utility := expectedUtility(task, result.NeedToolProb, infoGain, tokenCost, routeRisk, noiseRatio(irrelevantTokens, resultTokens))

	metrics := taskMetrics{
		NeedPredictionCorrect:  (result.NeedToolProb >= cfg.NeedThreshold) == task.NeedTool,
		RequiredToolCount:      len(requiredTools),
		RequiredOperationCount: len(requiredOps),
		CalledToolCount:        len(uniqueStrings(calledTools)),
		CalledOperationCount:   len(calledOps),
		ToolHitCount:           toolHits,
		OperationHitCount:      opHits,
		ForbiddenCallCount:     forbiddenCalls,
		RedundantCalls:         redundantCalls,
		ToolRecall:             ratioOrPerfect(toolHits, len(requiredTools)),
		ToolPrecision:          ratioOrPerfect(toolHits, len(uniqueStrings(calledTools))),
		OperationRecall:        ratioOrPerfect(opHits, len(requiredOps)),
		OperationPrecision:     ratioOrPerfect(opHits, len(calledOps)),
		RouteRisk:              routeRisk,
		ExpectedRouteRisk:      expectedRisk,
		ToolTokenCost:          tokenCost,
		ToolResultTokens:       resultTokens,
		IrrelevantTokens:       irrelevantTokens,
		ToolResultNoise:        noiseRatio(irrelevantTokens, resultTokens),
		ToolAlignment:          alignment,
		InfoGain:               infoGain,
		InfoEfficiency:         infoEfficiency,
		ExpectedUtility:        utility,
	}
	metrics.RedundantRate = ratioOrZero(redundantCalls, len(calledOps))
	metrics.SuccessProbability = successProbability(metrics)
	metrics.Success = metrics.SuccessProbability >= cfg.SuccessThreshold
	metrics.Clean = metrics.ForbiddenCallCount == 0 && metrics.RouteRisk <= task.RiskBudget && metrics.RedundantRate <= cfg.MaxRedundantRate
	metrics.ToolTuneScore = toolTuneScore(metrics)
	return metrics
}

func callToolNames(calls []toolCall) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.Name)
	}
	return out
}

func callOperationNames(calls []toolCall) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.Operation)
	}
	return out
}

func countRedundantCalls(task benchTask, calledOps []string, ops map[string]operationSpec) int {
	required := stringSet(task.RequiredOperations)
	forbidden := stringSet(task.ForbiddenOperations)
	seen := map[string]struct{}{}
	count := 0
	for _, name := range calledOps {
		if _, ok := seen[name]; ok {
			count++
			continue
		}
		seen[name] = struct{}{}
		if _, ok := required[name]; ok {
			continue
		}
		if _, ok := forbidden[name]; ok {
			count++
			continue
		}
		op, ok := ops[name]
		if !ok {
			count++
			continue
		}
		if cosineSimilarity(termVector(task.IntentTerms), termVector(op.Tags)) < 0.20 {
			count++
		}
	}
	return count
}

func routeRiskScore(task benchTask, calls []toolCall, ops map[string]operationSpec) float64 {
	required := stringSet(task.RequiredOperations)
	forbidden := stringSet(task.ForbiddenOperations)
	risk := 0.0
	for _, call := range calls {
		op, ok := ops[call.Operation]
		if !ok {
			continue
		}
		misuse := 0.0
		if _, ok := forbidden[call.Operation]; ok {
			misuse = 1
		} else if _, ok := required[call.Operation]; !ok {
			if op.Risk >= 4 {
				misuse = 1
			} else {
				misuse = 0.5
			}
		}
		risk += op.Risk * misuse
	}
	return risk
}

func expectedRouteRisk(task benchTask, result strategyResult, tools map[string]toolSpec, ops map[string]operationSpec) float64 {
	visible := stringSet(result.VisibleTools)
	required := stringSet(task.RequiredTools)
	forbidden := stringSet(task.ForbiddenTools)
	total := 0.0
	for name, spec := range tools {
		if _, ok := visible[name]; !ok {
			continue
		}
		callProb := result.NeedToolProb * 0.16
		if _, ok := required[name]; ok {
			callProb = result.NeedToolProb * 0.70
		}
		misuseProb := 0.10
		if _, ok := forbidden[name]; ok {
			misuseProb = 0.80
		} else if _, ok := required[name]; ok {
			misuseProb = 0.05
		} else if spec.Risk >= 4 {
			misuseProb = 0.25
		}
		total += callProb * spec.Risk * misuseProb
	}
	return total
}

func visibleToolDescTokens(visible []string, tools map[string]toolSpec) int {
	total := 0
	for _, name := range visible {
		if spec, ok := tools[name]; ok {
			total += spec.DescTokens
		}
	}
	return total
}

func resultTokenStats(calls []toolCall, packed bool, ops map[string]operationSpec) (int, int) {
	total := 0
	irrelevant := 0
	for _, call := range calls {
		op, ok := ops[call.Operation]
		if !ok {
			continue
		}
		rt := op.ResultTokens
		it := op.IrrelevantTokens
		if packed {
			useful := rt - it
			if useful < 0 {
				useful = 0
			}
			rt = int(math.Ceil(float64(useful)*1.15 + 60))
			if rt < 80 {
				rt = 80
			}
			it = int(math.Ceil(float64(it) * 0.20))
			if it > rt {
				it = rt
			}
		}
		total += rt
		irrelevant += it
	}
	return total, irrelevant
}

func toolAlignment(intent []string, calls []toolCall, ops map[string]operationSpec) float64 {
	if len(calls) == 0 {
		if len(intent) == 0 {
			return 1
		}
		return 0
	}
	taskVec := termVector(intent)
	toolVec := map[string]float64{}
	for _, call := range calls {
		if op, ok := ops[call.Operation]; ok {
			for term, weight := range termVector(op.Tags) {
				toolVec[term] += weight
			}
		}
	}
	return cosineSimilarity(taskVec, toolVec)
}

func informationGain(task benchTask, calledOps []string, opHits, redundant, forbidden int) float64 {
	if !task.NeedTool {
		if len(calledOps) == 0 {
			return 0.20
		}
		return 0
	}
	required := len(task.RequiredOperations)
	if required == 0 {
		return 0.10
	}
	baseEntropy := math.Log2(float64(required) + 1)
	coverage := float64(opHits) / float64(required)
	gain := baseEntropy * coverage
	gain -= 0.20 * float64(redundant)
	gain -= 0.45 * float64(forbidden)
	if gain < 0 {
		return 0
	}
	return gain
}

func expectedUtility(task benchTask, needProb, infoGain float64, tokenCost int, routeRisk, noise float64) float64 {
	successValue := 1.0 + 0.20*task.Difficulty
	return needProb*infoGain*successValue - float64(tokenCost)/6000.0 - 0.12*routeRisk - 0.20*noise
}

func successProbability(m taskMetrics) float64 {
	score := -0.2
	score += 1.40 * m.OperationRecall
	score += 0.75 * m.OperationPrecision
	score += 0.65 * boolFloat(m.NeedPredictionCorrect)
	score += 0.45 * m.ToolAlignment
	score += 0.35 * math.Min(m.InfoEfficiency, 1.5)
	score -= 0.75 * m.RedundantRate
	score -= 0.22 * m.RouteRisk
	score -= 0.60 * m.ToolResultNoise
	score -= 0.90 * float64(m.ForbiddenCallCount)
	return 1 / (1 + math.Exp(-score))
}

func toolTuneScore(m taskMetrics) float64 {
	score := 0.0
	score += 0.25 * m.SuccessProbability
	score += 0.10 * boolFloat(m.NeedPredictionCorrect)
	score += 0.12 * m.ToolRecall
	score += 0.10 * m.ToolPrecision
	score += 0.10 * math.Min(m.InfoEfficiency, 1)
	score += 0.10 * m.ToolAlignment
	score -= 0.10 * clamp(m.ExpectedRouteRisk/5, 0, 1)
	score -= 0.06 * m.RedundantRate
	score -= 0.05 * m.ToolResultNoise
	score -= 0.02 * clamp(float64(m.ToolTokenCost)/5000, 0, 1)
	return clamp(score, 0, 1)
}

func termVector(terms []string) map[string]float64 {
	out := map[string]float64{}
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		out[term]++
	}
	return out
}

func cosineSimilarity(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	dot := 0.0
	var normA, normB float64
	for k, av := range a {
		normA += av * av
		if bv, ok := b[k]; ok {
			dot += av * bv
		}
	}
	for _, bv := range b {
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func noiseRatio(irrelevant, total int) float64 {
	return ratioOrZero(irrelevant, total)
}

func ratioOrPerfect(num, den int) float64 {
	if den == 0 {
		return 1
	}
	return float64(num) / float64(den)
}

func ratioOrZero(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func countIntersection(a, b []string) int {
	bs := stringSet(b)
	count := 0
	seen := map[string]struct{}{}
	for _, value := range a {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		if _, ok := bs[value]; ok {
			count++
		}
	}
	return count
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sortedToolKeys(m map[string]toolSpec) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sortedSetKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	runes := len([]rune(text))
	words := len(strings.Fields(text))
	estimate := words
	if cjkRunes(text) > runes/4 {
		estimate = runes / 2
	}
	if estimate < runes/5 {
		estimate = runes / 5
	}
	if estimate < 1 {
		estimate = 1
	}
	return estimate
}

func cjkRunes(text string) int {
	count := 0
	for _, r := range text {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) {
			count++
		}
	}
	return count
}

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
