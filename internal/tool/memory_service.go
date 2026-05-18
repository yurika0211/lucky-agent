package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/utils"
)

// MemoryToolService implements remember/recall handlers in the tool layer.
type MemoryToolService struct {
	store *memory.Store
}

// NewMemoryToolService creates a tool-layer memory service.
func NewMemoryToolService(store *memory.Store) *MemoryToolService {
	return &MemoryToolService{store: store}
}

func (s *MemoryToolService) HandleRemember(args map[string]any) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("memory store not initialized")
	}
	content, _ := args["content"].(string)
	category, _ := args["category"].(string)
	if content == "" {
		return "", fmt.Errorf("content is required")
	}
	if category == "" {
		category = inferMemoryCategory(content)
	}
	tier := memory.TierMedium
	importance := 0.5
	longTerm, _ := args["long_term"].(bool)
	if longTerm {
		tier = memory.TierLong
		importance = 0.9
	}
	if rawTier, _ := args["tier"].(string); rawTier != "" {
		tier = parseMemoryToolTier(rawTier)
		if _, ok := args["importance"]; !ok {
			importance = defaultImportanceForTier(tier)
		}
	}
	if rawImportance, ok := numberArg(args["importance"]); ok {
		importance = clamp01(rawImportance)
	}
	tags := stringSliceArg(args["tags"])
	links := stringSliceArg(args["links"])
	aliases := stringSliceArg(args["aliases"])
	opts := memory.SaveOptions{
		Tags:       tags,
		Links:      links,
		Aliases:    aliases,
		Status:     stringArg(args["status"]),
		StateKey:   stringArg(args["state_key"]),
		StateValue: stringArg(args["state_value"]),
		Supersedes: stringSliceArg(args["supersedes"]),
	}
	if confidence, ok := numberArg(args["confidence"]); ok {
		opts.Confidence = clamp01(confidence)
	}
	if validFrom, ok, err := timeArg(args["valid_from"]); err != nil {
		return "", err
	} else if ok {
		opts.ValidFrom = validFrom
	}
	if validUntil, ok, err := timeArg(args["valid_until"]); err != nil {
		return "", err
	} else if ok {
		opts.ValidUntil = &validUntil
	}

	if err := s.store.SaveWithOptions(content, category, tier, importance, opts); err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ 已保存为%s记忆 [%s]: %s", memoryTierLabel(tier), category, utils.Truncate(content, 80)), nil
}

func (s *MemoryToolService) HandleRecall(args map[string]any) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("memory store not initialized")
	}
	query, _ := args["query"].(string)
	if query == "" {
		recent := s.store.Recent(5)
		if len(recent) == 0 {
			return "没有找到记忆", nil
		}
		var sb strings.Builder
		sb.WriteString("最近的记忆：\n")
		for _, e := range recent {
			sb.WriteString(fmt.Sprintf("- [%s/%s] %s\n", e.Category, e.Tier.String(), utils.Truncate(e.Content, 80)))
		}
		return sb.String(), nil
	}
	results := s.store.Search(query)
	if len(results) == 0 {
		return fmt.Sprintf("没有找到关于「%s」的记忆", query), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条关于「%s」的记忆：\n", len(results), query))
	limit := 10
	if len(results) < limit {
		limit = len(results)
	}
	for i := 0; i < limit; i++ {
		e := results[i]
		ref := ""
		if e.Path != "" {
			ref = " @" + e.Path
			if e.BlockID != "" {
				ref += "#" + e.BlockID
			}
		}
		graph := ""
		if len(e.Links) > 0 {
			graph = " links=" + strings.Join(limitStrings(e.Links, 4), ",")
		}
		sb.WriteString(fmt.Sprintf("- [%s/%s%s%s] %s\n", e.Category, e.Tier.String(), graph, ref, utils.Truncate(e.Content, 100)))
	}
	return sb.String(), nil
}

func parseMemoryToolTier(raw string) memory.Tier {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short", "短期":
		return memory.TierShort
	case "long", "长期":
		return memory.TierLong
	default:
		return memory.TierMedium
	}
}

func defaultImportanceForTier(t memory.Tier) float64 {
	switch t {
	case memory.TierShort:
		return 0.25
	case memory.TierLong:
		return 0.9
	default:
		return 0.5
	}
}

func memoryTierLabel(t memory.Tier) string {
	switch t {
	case memory.TierShort:
		return "短期"
	case memory.TierLong:
		return "长期"
	default:
		return "中期"
	}
}

func numberArg(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		parsed, err := n.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func stringArg(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func timeArg(v any) (time.Time, bool, error) {
	raw := stringArg(v)
	if raw == "" {
		return time.Time{}, false, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed, true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("invalid time %q: use RFC3339 or YYYY-MM-DD", raw)
}

func stringSliceArg(v any) []string {
	switch raw := v.(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		fields := strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == '，' || r == '\n' || r == ';' || r == '；'
		})
		out := make([]string, 0, len(fields))
		for _, field := range fields {
			if strings.TrimSpace(field) != "" {
				out = append(out, strings.TrimSpace(field))
			}
		}
		return out
	default:
		return nil
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func inferMemoryCategory(input string) string {
	lower := strings.ToLower(input)

	for _, kw := range []string{"喜欢", "偏好", "prefer", "like", "想要", "习惯", "讨厌", "hate", "dislike"} {
		if strings.Contains(lower, kw) {
			return "preference"
		}
	}
	for _, kw := range []string{"项目", "project", "代码", "code", "bug", "部署", "deploy", "仓库", "repo", "pr", "merge"} {
		if strings.Contains(lower, kw) {
			return "project"
		}
	}
	for _, kw := range []string{"过敏", "花粉", "诊断", "健康", "生病", "allergy", "pollen", "health", "diagnosed"} {
		if strings.Contains(lower, kw) {
			return "health"
		}
	}
	for _, kw := range []string{"必须", "应该", "需要查询", "工具", "tool", "rule", "workflow", "路由"} {
		if strings.Contains(lower, kw) {
			return "rule"
		}
	}
	for _, kw := range []string{"城市", "地点", "位置", "住在", "location", "city"} {
		if strings.Contains(lower, kw) {
			return "location"
		}
	}
	for _, kw := range []string{"什么是", "怎么", "如何", "为什么", "what is", "how to", "why", "解释", "explain", "调研", "研究"} {
		if strings.Contains(lower, kw) {
			return "knowledge"
		}
	}
	for _, kw := range []string{"我叫", "我是", "我的名字", "my name", "i am", "住", "学校", "公司"} {
		if strings.Contains(lower, kw) {
			return "identity"
		}
	}
	return "conversation"
}
