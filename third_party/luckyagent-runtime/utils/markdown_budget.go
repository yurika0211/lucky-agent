package utils

import (
	"sort"
	"strings"
)

/*
EstimateFunc 用于估算一段文本的大致 token 消耗。

调用方可以传入任意估算函数，例如 contextx.TokenEstimator.Estimate，
从而避免 utils 反向依赖具体的上下文或模型实现。
*/
type EstimateFunc func(string) int

/*
MarkdownBudgetOptions 控制 Markdown 压缩时的行为细节。

当前主要用于自定义“省略标记”，以便在不同场景下输出不同风格的截断提示。
*/
type MarkdownBudgetOptions struct {
	OmissionMarker string
}

/*
CompactMarkdownForPrompt 在给定 token 预算内，尽量保留最有价值的 Markdown 区块。

它不会像简单的头尾截断那样直接按字符裁剪，而是先按 Markdown 结构切分，
再优先保留标题、规则性更强的段落、代码块和示例区块，最后才退化为安全截断。
*/
func CompactMarkdownForPrompt(text string, tokenBudget int, estimate EstimateFunc, opts MarkdownBudgetOptions) string {
	text = strings.TrimSpace(text)
	if text == "" || tokenBudget <= 0 || estimate == nil {
		return ""
	}
	if estimate(text) <= tokenBudget {
		return text
	}

	marker := strings.TrimSpace(opts.OmissionMarker)
	if marker == "" {
		marker = "[... omitted ...]"
	}

	sections := splitMarkdownSections(text)
	if len(sections) == 0 {
		return fitTextToBudget(text, tokenBudget, estimate, marker)
	}

	type scoredSection struct {
		index int
		score int
		text  string
	}

	scored := make([]scoredSection, 0, len(sections))
	for i, sec := range sections {
		rendered := strings.TrimSpace(strings.Join(sec.blocks, "\n\n"))
		if rendered == "" {
			continue
		}
		scored = append(scored, scoredSection{
			index: i,
			score: scoreMarkdownSection(i, sec.heading, rendered),
			text:  rendered,
		})
	}
	if len(scored) == 0 {
		return fitTextToBudget(text, tokenBudget, estimate, marker)
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].index < scored[j].index
		}
		return scored[i].score > scored[j].score
	})

	selected := make(map[int]string, len(scored))
	for _, sec := range scored {
		selected[sec.index] = sec.text
		candidate := renderSelectedSections(sections, selected, marker)
		if estimate(candidate) > tokenBudget {
			delete(selected, sec.index)
		}
	}

	if len(selected) == 0 {
		return fitTextToBudget(scored[0].text, tokenBudget, estimate, marker)
	}

	out := renderSelectedSections(sections, selected, marker)
	if estimate(out) <= tokenBudget {
		return out
	}
	return fitTextToBudget(out, tokenBudget, estimate, marker)
}

type markdownSection struct {
	heading string
	blocks  []string
}

/*
splitMarkdownSections 按 Markdown 结构将文本切分为若干 section。

切分时会识别标题与代码块边界，尽量避免把代码块和普通段落混在一起，
从而为后续的打分与预算选择提供更稳定的输入。
*/
func splitMarkdownSections(text string) []markdownSection {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	sections := make([]markdownSection, 0, 16)
	current := markdownSection{}
	var block []string
	inCode := false

	flushBlock := func() {
		if len(block) == 0 {
			return
		}
		current.blocks = append(current.blocks, strings.TrimSpace(strings.Join(block, "\n")))
		block = nil
	}
	flushSection := func() {
		flushBlock()
		if strings.TrimSpace(current.heading) == "" && len(current.blocks) == 0 {
			return
		}
		sections = append(sections, current)
		current = markdownSection{}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			block = append(block, line)
			if !inCode {
				flushBlock()
			}
			continue
		}
		if !inCode && isMarkdownHeading(trimmed) {
			flushSection()
			current.heading = trimmed
			block = append(block, line)
			continue
		}
		if !inCode && trimmed == "" {
			flushBlock()
			continue
		}
		block = append(block, line)
	}
	flushSection()
	return sections
}

/*
isMarkdownHeading 判断一行文本是否是标准 Markdown 标题。

当前只识别 `#` 到 `######` 且后面跟空格的标题形式。
*/
func isMarkdownHeading(line string) bool {
	if line == "" || line[0] != '#' {
		return false
	}
	hashes := 0
	for hashes < len(line) && line[hashes] == '#' {
		hashes++
	}
	return hashes > 0 && hashes <= 6 && len(line) > hashes && line[hashes] == ' '
}

/*
scoreMarkdownSection 为单个 Markdown section 计算启发式得分。

打分会偏向以下内容：
1. 靠前的 section
2. 带标题的区块，尤其是高层级标题
3. 含有规则词、警告词、示例词的内容
4. 包含代码块或列表的区块
*/
func scoreMarkdownSection(index int, heading, body string) int {
	score := 10
	if index == 0 {
		score += 20
	}
	if heading != "" {
		score += 30
		switch {
		case strings.HasPrefix(heading, "# "):
			score += 18
		case strings.HasPrefix(heading, "## "):
			score += 12
		case strings.HasPrefix(heading, "### "):
			score += 8
		}
	}

	lower := strings.ToLower(body)
	for _, kw := range []string{
		"must", "should", "never", "always", "important", "warning",
		"required", "rule", "example", "note", "禁止", "必须", "不要", "应当", "注意",
	} {
		if strings.Contains(lower, kw) {
			score += 10
		}
	}
	if strings.Contains(body, "```") {
		score += 12
	}
	if strings.Contains(body, "- ") || strings.Contains(body, "* ") {
		score += 6
	}
	return score
}

/*
renderSelectedSections 根据原始顺序重新组装已选中的 section。

如果中间有区块被跳过，则插入省略标记，帮助调用方感知内容是“有选择地保留”，
而不是单纯拼接后的完整原文。
*/
func renderSelectedSections(all []markdownSection, selected map[int]string, marker string) string {
	if len(selected) == 0 {
		return ""
	}
	parts := make([]string, 0, len(selected)*2)
	omittedPending := false
	for i := range all {
		text, ok := selected[i]
		if !ok {
			omittedPending = true
			continue
		}
		if omittedPending && len(parts) > 0 {
			parts = append(parts, marker)
		}
		parts = append(parts, text)
		omittedPending = false
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

/*
fitTextToBudget 在结构化选择仍超预算时，对文本做最后一层安全截断。

它使用二分搜索找到满足预算的最大前缀，并在必要时补一个省略标记，
属于 CompactMarkdownForPrompt 的兜底路径。
*/
func fitTextToBudget(text string, tokenBudget int, estimate EstimateFunc, marker string) string {
	if strings.TrimSpace(text) == "" || tokenBudget <= 0 {
		return ""
	}
	if estimate(text) <= tokenBudget {
		return text
	}
	runes := []rune(text)
	lo, hi := 0, len(runes)
	best := ""
	for lo <= hi {
		mid := (lo + hi) / 2
		candidate := string(runes[:mid])
		if mid < len(runes) {
			candidate = strings.TrimSpace(candidate) + "\n\n" + marker
		}
		if estimate(candidate) <= tokenBudget {
			best = candidate
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return strings.TrimSpace(best)
}
