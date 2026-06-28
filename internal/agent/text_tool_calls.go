package agent

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"

	"github.com/yurika0211/luckyagent/internal/provider"
)

var (
	textToolCallsBlockRe = regexp.MustCompile(`(?is)<\s*(?:\|\|DSML\|\||｜｜DSML｜｜)?tool_calls\s*>(.*?)</\s*(?:\|\|DSML\|\||｜｜DSML｜｜)?tool_calls\s*>`)
	textToolInvokeRe     = regexp.MustCompile(`(?is)<\s*(?:\|\|DSML\|\||｜｜DSML｜｜)?invoke\b([^>]*)>(.*?)</\s*(?:\|\|DSML\|\||｜｜DSML｜｜)?invoke\s*>`)
	textToolParamRe      = regexp.MustCompile(`(?is)<\s*(?:\|\|DSML\|\||｜｜DSML｜｜)?parameter\b([^>]*)>(.*?)</\s*(?:\|\|DSML\|\||｜｜DSML｜｜)?parameter\s*>`)
	textToolAttrRe       = regexp.MustCompile(`(?is)\b([a-zA-Z_][a-zA-Z0-9_-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

func applyTextToolCallsToResponse(resp *provider.Response, disabledTools []string) bool {
	if resp == nil || strings.TrimSpace(resp.Content) == "" {
		return false
	}
	cleaned, calls := extractTextToolCalls(resp.Content)
	if len(calls) == 0 {
		return false
	}

	resp.Content = cleaned
	calls = filterProviderToolCalls(calls, disabledTools)
	if len(calls) > 0 {
		resp.ToolCalls = append(resp.ToolCalls, calls...)
	}
	return true
}

func extractTextToolCalls(content string) (string, []provider.ToolCall) {
	if !strings.Contains(content, "tool_calls") {
		return content, nil
	}

	matches := textToolCallsBlockRe.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return content, nil
	}

	var cleaned strings.Builder
	var calls []provider.ToolCall
	last := 0
	removedAny := false
	for _, match := range matches {
		if len(match) < 4 || match[2] < 0 || match[3] < 0 {
			continue
		}
		blockCalls := parseTextToolCallBlock(content[match[2]:match[3]])
		if len(blockCalls) == 0 {
			continue
		}

		cleaned.WriteString(content[last:match[0]])
		calls = append(calls, blockCalls...)
		last = match[1]
		removedAny = true
	}

	if !removedAny {
		return content, nil
	}
	cleaned.WriteString(content[last:])
	return strings.TrimSpace(cleaned.String()), calls
}

func parseTextToolCallBlock(block string) []provider.ToolCall {
	matches := textToolInvokeRe.FindAllStringSubmatchIndex(block, -1)
	if len(matches) == 0 {
		return nil
	}

	calls := make([]provider.ToolCall, 0, len(matches))
	for _, match := range matches {
		if len(match) < 6 || match[2] < 0 || match[3] < 0 || match[4] < 0 || match[5] < 0 {
			continue
		}
		attrs := parseTextToolAttrs(block[match[2]:match[3]])
		name := strings.TrimSpace(attrs["name"])
		if name == "" {
			continue
		}

		args := parseTextToolParams(block[match[4]:match[5]])
		rawArgs, err := json.Marshal(args)
		if err != nil {
			continue
		}
		calls = append(calls, provider.ToolCall{
			ID:        provider.GenerateCallID(),
			Name:      name,
			Arguments: string(rawArgs),
		})
	}
	return calls
}

func parseTextToolParams(body string) map[string]any {
	args := make(map[string]any)
	matches := textToolParamRe.FindAllStringSubmatchIndex(body, -1)
	for _, match := range matches {
		if len(match) < 6 || match[2] < 0 || match[3] < 0 || match[4] < 0 || match[5] < 0 {
			continue
		}
		attrs := parseTextToolAttrs(body[match[2]:match[3]])
		name := strings.TrimSpace(attrs["name"])
		if name == "" {
			continue
		}
		rawValue := html.UnescapeString(strings.TrimSpace(body[match[4]:match[5]]))
		args[name] = parseTextToolParamValue(rawValue, attrs["string"])
	}
	return args
}

func parseTextToolAttrs(raw string) map[string]string {
	out := make(map[string]string)
	for _, match := range textToolAttrRe.FindAllStringSubmatch(raw, -1) {
		if len(match) >= 4 {
			value := match[2]
			if value == "" {
				value = match[3]
			}
			out[strings.ToLower(strings.TrimSpace(match[1]))] = html.UnescapeString(value)
		}
	}
	return out
}

func parseTextToolParamValue(rawValue string, stringAttr string) any {
	if stringAttr == "" || strings.EqualFold(strings.TrimSpace(stringAttr), "true") {
		return rawValue
	}

	var value any
	if err := json.Unmarshal([]byte(rawValue), &value); err == nil {
		return value
	}
	return rawValue
}

func filterProviderToolCalls(calls []provider.ToolCall, disabledTools []string) []provider.ToolCall {
	if len(calls) == 0 || len(disabledTools) == 0 {
		return calls
	}
	disabled := make(map[string]struct{}, len(disabledTools))
	for _, name := range disabledTools {
		name = strings.TrimSpace(name)
		if name != "" {
			disabled[name] = struct{}{}
		}
	}
	if len(disabled) == 0 {
		return calls
	}

	filtered := make([]provider.ToolCall, 0, len(calls))
	for _, call := range calls {
		if _, blocked := disabled[strings.TrimSpace(call.Name)]; blocked {
			continue
		}
		filtered = append(filtered, call)
	}
	return filtered
}

func shouldHoldPotentialTextToolCallStream(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(trimmed, "<｜｜DSML｜｜") ||
		strings.Contains(lower, "<||dsml||") ||
		strings.Contains(lower, "<tool_calls") ||
		strings.Contains(lower, "</tool_calls") {
		return true
	}
	if strings.HasPrefix(trimmed, "<") && len(trimmed) < 96 {
		return true
	}
	return false
}
