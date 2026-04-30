package tool

import (
	"fmt"
	"strings"

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
	longTerm, _ := args["long_term"].(bool)
	if longTerm {
		if err := s.store.SaveLongTerm(content, category); err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ 已保存为长期记忆 [%s]: %s", category, utils.Truncate(content, 80)), nil
	}
	if err := s.store.Save(content, category); err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ 已保存为中期记忆 [%s]: %s", category, utils.Truncate(content, 80)), nil
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
		sb.WriteString(fmt.Sprintf("- [%s/%s] %s\n", e.Category, e.Tier.String(), utils.Truncate(e.Content, 80)))
	}
	return sb.String(), nil
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
