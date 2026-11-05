package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Syntax compaction folds structure that line scoring keeps whole: repeated
// JSON objects, long arrays, YAML maps, HTML subtrees, and function bodies.
// It runs after the shape rewrite and before the line compressor.
//
// No model call. No tree-sitter. Parsers are the standard library plus the
// YAML decoder already required by this module. Unrecognized plaintext is
// returned unchanged and does not build a tree.
//
// Failure stays open: a parse error, an empty tree, a result that is not
// shorter, or a second pass that still shrinks, returns the text that entered
// this layer.

const (
	syntaxSkipSmall      = "too-small"
	syntaxSkipLarge      = "too-large"
	syntaxSkipError      = "tool-error"
	syntaxSkipUnknown    = "unrecognised"
	syntaxSkipParse      = "parse-fail"
	syntaxSkipEmpty      = "empty"
	syntaxSkipNotShorter = "not-shorter"
	syntaxSkipNotStable  = "not-idempotent"

	syntaxArrayHead = 3
	syntaxArrayTail = 2
	syntaxArrayKeep = syntaxArrayHead + syntaxArrayTail
	syntaxMaxDepth  = 24
	syntaxKeySample = 8
)

const (
	syntaxJSON   = "json"
	syntaxJSONL  = "jsonl"
	syntaxYAML   = "yaml"
	syntaxHTML   = "html"
	syntaxSource = "source"
)

type syntaxRewriteResult struct {
	Text       string
	Filter     string
	Skip       string
	BytesIn    int
	BytesOut   int
	Idempotent bool
}

var (
	syntaxShebang = regexp.MustCompile(`^#![^\n]*(?:python|node|deno|bun)\b`)
	syntaxGoFunc  = regexp.MustCompile(`(?m)^func(?:\s+\([^)]*\))?\s+[A-Za-z_][A-Za-z0-9_]*\s*\(`)
	syntaxPyDef   = regexp.MustCompile(`(?m)^(?:async\s+def|def|class)\s+[A-Za-z_]`)
	syntaxJSFunc  = regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?(?:function\s+[A-Za-z_$]|class\s+[A-Za-z_$]|const\s+[A-Za-z_$][A-Za-z0-9_$]*\s*=\s*(?:async\s*)?\()`)
	syntaxLineNo  = regexp.MustCompile(`(?:^|\s)(\d+)\|`)
)

// compactToolResultSyntax folds a recognised tree. toolError, short text, and
// huge dumps pass through with the original bytes.
func compactToolResultSyntax(text string, toolError bool) syntaxRewriteResult {
	in := len(text)
	out := syntaxRewriteResult{Text: text, BytesIn: in, BytesOut: in}
	if toolError || strings.HasPrefix(strings.TrimSpace(text), "[Tool Error:") {
		out.Skip = syntaxSkipError
		return out
	}
	if in < shapeMinBytes {
		out.Skip = syntaxSkipSmall
		return out
	}
	if in > shapeMaxBytes {
		out.Skip = syntaxSkipLarge
		return out
	}
	filter := detectSyntaxShape(text)
	if filter == "" {
		out.Skip = syntaxSkipUnknown
		return out
	}
	rewritten, ok := applySyntaxShape(filter, text)
	if !ok {
		out.Skip = syntaxSkipParse
		return out
	}
	if strings.TrimSpace(rewritten) == "" {
		out.Skip = syntaxSkipEmpty
		return out
	}
	if len(rewritten) >= in {
		out.Skip = syntaxSkipNotShorter
		return out
	}
	again, okAgain := applySyntaxShape(detectSyntaxShape(rewritten), rewritten)
	if okAgain && len(again) < len(rewritten) {
		out.Skip = syntaxSkipNotStable
		return out
	}
	out.Text = rewritten
	out.Filter = filter
	out.BytesOut = len(rewritten)
	out.Idempotent = true
	return out
}

func detectSyntaxShape(text string) string {
	trim := strings.TrimSpace(text)
	if trim == "" || strings.HasPrefix(trim, "[syntax ") {
		return ""
	}
	if strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[") {
		if json.Valid([]byte(trim)) {
			return syntaxJSON
		}
	}
	if looksLikeJSONL(trim) {
		return syntaxJSONL
	}
	if looksLikeHTML(trim) {
		return syntaxHTML
	}
	if looksLikeSource(text) {
		return syntaxSource
	}
	if looksLikeYAML(trim) {
		return syntaxYAML
	}
	return ""
}

func applySyntaxShape(filter, text string) (string, bool) {
	switch filter {
	case syntaxJSON:
		return foldJSON(strings.TrimSpace(text))
	case syntaxJSONL:
		return foldJSONL(text)
	case syntaxYAML:
		return foldYAML(text)
	case syntaxHTML:
		return foldMarkup(text)
	case syntaxSource:
		return foldSource(text)
	default:
		return text, false
	}
}

func looksLikeJSONL(text string) bool {
	lines := nonEmptyLines(text)
	if len(lines) < 3 {
		return false
	}
	objects := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") || !json.Valid([]byte(line)) {
			return false
		}
		objects++
	}
	return objects >= 3
}

func looksLikeHTML(text string) bool {
	trim := strings.TrimSpace(text)
	lower := strings.ToLower(trim)
	if strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html") || strings.HasPrefix(lower, "<?xml") {
		return true
	}
	if !strings.HasPrefix(trim, "<") || !strings.Contains(trim, "</") {
		return false
	}
	return strings.Count(lower, "<div")+strings.Count(lower, "<span")+strings.Count(lower, "<p")+strings.Count(lower, "<li") >= 4
}

func looksLikeSource(text string) bool {
	if syntaxShebang.MatchString(text) {
		return true
	}
	if syntaxGoFunc.MatchString(text) && strings.Contains(text, "{\n") {
		return true
	}
	if syntaxPyDef.MatchString(text) && strings.Contains(text, ":\n") {
		return true
	}
	if syntaxJSFunc.MatchString(text) && strings.Contains(text, "{\n") {
		return true
	}
	return false
}

func looksLikeYAML(text string) bool {
	trim := strings.TrimSpace(text)
	if strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[") || strings.HasPrefix(trim, "<") {
		return false
	}
	if !strings.Contains(trim, ":\n") && !strings.Contains(trim, ": ") {
		return false
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(trim), &node); err != nil {
		return false
	}
	return node.Kind != 0
}

func foldJSON(text string) (string, bool) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", false
	}
	folded := foldJSONValue(value, 0)
	raw, err := json.Marshal(folded)
	if err != nil || len(raw) == 0 {
		return "", false
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return string(raw), true
	}
	return "[syntax json]\n" + pretty.String(), true
}

func foldJSONL(text string) (string, bool) {
	var rows []any
	for _, line := range nonEmptyLines(text) {
		var value any
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &value); err != nil {
			return "", false
		}
		rows = append(rows, value)
	}
	if len(rows) == 0 {
		return "", false
	}
	folded := foldJSONValue(rows, 0)
	raw, err := json.Marshal(folded)
	if err != nil {
		return "", false
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return "", false
	}
	return fmt.Sprintf("[syntax jsonl]\n%d lines\n%s", len(rows), pretty.String()), true
}

func foldJSONValue(value any, depth int) any {
	if depth > syntaxMaxDepth {
		return map[string]any{"_truncated": true}
	}
	switch typed := value.(type) {
	case []any:
		return foldJSONArray(typed, depth)
	case map[string]any:
		return foldJSONObject(typed, depth)
	default:
		return typed
	}
}

func foldJSONArray(items []any, depth int) any {
	if len(items) <= syntaxArrayKeep {
		out := make([]any, len(items))
		for i, item := range items {
			out[i] = foldJSONValue(item, depth+1)
		}
		return out
	}
	folded := make([]any, 0, syntaxArrayKeep+1)
	seen := map[string]int{}
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			seen[jsonObjectKey(object)]++
		}
		folded = append(folded, foldJSONValue(item, depth+1))
	}
	head := append([]any{}, folded[:syntaxArrayHead]...)
	tail := append([]any{}, folded[len(folded)-syntaxArrayTail:]...)
	omitted := len(items) - syntaxArrayKeep
	note := fmt.Sprintf("+%d more", omitted)
	if repeated := repeatedKeyNote(seen); repeated != "" {
		note = fmt.Sprintf("+%d more; repeated %s", omitted, repeated)
	}
	out := append(head, note)
	out = append(out, tail...)
	return out
}

func foldJSONObject(object map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(object))
	for key, value := range object {
		out[key] = foldJSONValue(value, depth+1)
	}
	return out
}

func jsonObjectKey(object map[string]any) string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	if len(keys) > syntaxKeySample {
		keys = keys[:syntaxKeySample]
	}
	return strings.Join(keys, ",")
}

func repeatedKeyNote(seen map[string]int) string {
	best := ""
	count := 1
	for key, n := range seen {
		if n > count {
			best = key
			count = n
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf("%s x%d", best, count)
}

func foldYAML(text string) (string, bool) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(text), &node); err != nil || node.Kind == 0 {
		return "", false
	}
	foldYAMLNode(&node, 0)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return "", false
	}
	_ = enc.Close()
	folded := strings.TrimSpace(buf.String())
	if folded == "" {
		return "", false
	}
	return "[syntax yaml]\n" + folded, true
}

func foldYAMLNode(node *yaml.Node, depth int) {
	if node == nil || depth > syntaxMaxDepth {
		return
	}
	if node.Kind == yaml.DocumentNode {
		for _, child := range node.Content {
			foldYAMLNode(child, depth)
		}
		return
	}
	if node.Kind == yaml.MappingNode {
		for i := 1; i < len(node.Content); i += 2 {
			foldYAMLNode(node.Content[i], depth+1)
		}
		return
	}
	if node.Kind != yaml.SequenceNode || len(node.Content) <= syntaxArrayKeep {
		for _, child := range node.Content {
			foldYAMLNode(child, depth+1)
		}
		return
	}
	head := append([]*yaml.Node{}, node.Content[:syntaxArrayHead]...)
	tail := append([]*yaml.Node{}, node.Content[len(node.Content)-syntaxArrayTail:]...)
	omitted := len(node.Content) - syntaxArrayKeep
	note := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprintf("+%d more", omitted)}
	node.Content = append(head, note)
	node.Content = append(node.Content, tail...)
	for _, child := range node.Content {
		foldYAMLNode(child, depth+1)
	}
}

func foldMarkup(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	seen := map[string]int{}
	dropped := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			dropped++
			continue
		}
		key := markupLineKey(trimmed)
		if key != "" {
			seen[key]++
			if seen[key] > 2 {
				dropped++
				continue
			}
		}
		out = append(out, trimmed)
	}
	if dropped == 0 {
		return "", false
	}
	out = append(out, fmt.Sprintf("<!-- +%d markup lines folded -->", dropped))
	return "[syntax html]\n" + strings.Join(out, "\n"), true
}

func markupLineKey(line string) string {
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > 180 {
		line = line[:180]
	}
	if !strings.Contains(line, "<") {
		return ""
	}
	return line
}

func foldSource(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) < 8 {
		return "", false
	}
	kind := sourceKind(text)
	out := make([]string, 0, 64)
	dropped := 0
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !isSourceHeader(kind, line) {
			if strings.TrimSpace(line) != "" {
				out = append(out, line)
			}
			i++
			continue
		}
		out = append(out, line)
		keep := keepLine(&out)
		if kind == "python" {
			i, dropped = foldPythonBody(lines, i, keep, dropped)
			continue
		}
		i, dropped = foldBraceBody(lines, i, keep, dropped)
	}
	if dropped == 0 {
		return "", false
	}
	out = append(out, fmt.Sprintf("// +%d body lines folded", dropped))
	return "[syntax " + kind + "]\n" + strings.Join(out, "\n"), true
}

type outKeep func(line string)

func keepLine(dst *[]string) outKeep {
	return func(line string) {
		*dst = append(*dst, line)
	}
}

func sourceKind(text string) string {
	switch {
	case syntaxGoFunc.MatchString(text):
		return "go"
	case syntaxPyDef.MatchString(text):
		return "python"
	default:
		return "js"
	}
}

func isSourceHeader(kind, line string) bool {
	trim := strings.TrimSpace(line)
	switch kind {
	case "go":
		return strings.HasPrefix(trim, "func ")
	case "python":
		return strings.HasPrefix(trim, "def ") || strings.HasPrefix(trim, "async def ") || strings.HasPrefix(trim, "class ")
	default:
		return strings.HasPrefix(trim, "function ") || strings.HasPrefix(trim, "export function ") || strings.HasPrefix(trim, "class ") || strings.HasPrefix(trim, "export class ") || strings.Contains(trim, "=> {") || strings.HasSuffix(trim, "{") && strings.Contains(trim, "(")
	}
}

func foldBraceBody(lines []string, header int, keep outKeep, dropped int) (int, int) {
	depth := strings.Count(lines[header], "{") - strings.Count(lines[header], "}")
	i := header + 1
	bodyStart := i
	for i < len(lines) && depth > 0 {
		depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
		i++
	}
	body := lines[bodyStart:i]
	if i > bodyStart && strings.TrimSpace(lines[i-1]) == "}" {
		body = lines[bodyStart : i-1]
	}
	kept, removed := foldStatementLines(body)
	for _, line := range kept {
		keep(line)
	}
	if i > bodyStart && strings.TrimSpace(lines[i-1]) == "}" {
		keep(lines[i-1])
	}
	return i, dropped + removed
}

func foldPythonBody(lines []string, header int, keep outKeep, dropped int) (int, int) {
	base := indentWidth(lines[header])
	i := header + 1
	var body []string
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			body = append(body, line)
			i++
			continue
		}
		if indentWidth(line) <= base {
			break
		}
		body = append(body, line)
		i++
	}
	kept, removed := foldStatementLines(body)
	for _, line := range kept {
		keep(line)
	}
	return i, dropped + removed
}

func foldStatementLines(body []string) ([]string, int) {
	seen := map[string]int{}
	out := make([]string, 0, 8)
	dropped := 0
	for _, line := range body {
		trim := strings.TrimSpace(line)
		if trim == "" || trim == "{" || trim == "}" {
			dropped++
			continue
		}
		key := normalizeStatement(trim)
		seen[key]++
		if seen[key] > 1 {
			dropped++
			continue
		}
		out = append(out, line)
	}
	return out, dropped
}

func normalizeStatement(line string) string {
	fields := strings.Fields(line)
	text := strings.Join(fields, " ")
	var b strings.Builder
	b.Grow(len(text))
	digits := false
	for _, r := range text {
		if r >= '0' && r <= '9' {
			if !digits {
				b.WriteByte('#')
				digits = true
			}
			continue
		}
		digits = false
		b.WriteRune(r)
	}
	return b.String()
}

func indentWidth(line string) int {
	n := 0
	for _, r := range line {
		if r == ' ' {
			n++
			continue
		}
		if r == '\t' {
			n += 4
			continue
		}
		break
	}
	return n
}
