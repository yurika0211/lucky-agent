package feishu

import (
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

const feishuAutoLinkLabelLimit = 72

var (
	feishuMarkdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM))
	feishuBareURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)
)

type feishuPostContent struct {
	ZhCN feishuPostLocale `json:"zh_cn"`
}

type feishuPostLocale struct {
	Content [][]feishuPostElement `json:"content"`
}

type feishuPostElement struct {
	Tag   string   `json:"tag"`
	Text  string   `json:"text,omitempty"`
	Href  string   `json:"href,omitempty"`
	Style []string `json:"style,omitempty"`
}

// newFeishuPostContent converts Markdown links and bare HTTP(S) URLs to
// native Feishu link elements. Returning false deliberately preserves the
// legacy text payload for messages without usable links.
func newFeishuPostContent(message string) (feishuPostContent, bool) {
	return newFeishuPostContentWithMode(message, false)
}

func newFeishuPostContentWithMode(message string, force bool) (feishuPostContent, bool) {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n"))
	if message == "" {
		return feishuPostContent{}, false
	}

	source := []byte(message)
	root := feishuMarkdownParser.Parser().Parse(text.NewReader(source))
	renderer := &feishuPostRenderer{source: source}
	renderer.renderDocument(root)
	if (!force && !renderer.hasFormatting) || len(renderer.rows) == 0 {
		return feishuPostContent{}, false
	}
	return feishuPostContent{ZhCN: feishuPostLocale{Content: renderer.rows}}, true
}

type feishuPostRenderer struct {
	source        []byte
	rows          [][]feishuPostElement
	current       []feishuPostElement
	hasLink       bool
	hasFormatting bool
	style         []string
}

func (r *feishuPostRenderer) renderDocument(doc ast.Node) {
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		r.renderBlock(child, "")
	}
	r.finishRow()
}

func (r *feishuPostRenderer) renderBlock(node ast.Node, prefix string) {
	switch n := node.(type) {
	case *ast.Heading:
		r.hasFormatting = true
		r.startRow(prefix)
		r.withStyle("bold", func() { r.renderInlineChildren(n) })
		r.finishRow()
	case *ast.Paragraph, *ast.TextBlock:
		r.startRow(prefix)
		r.renderInlineChildren(n)
		r.finishRow()
	case *ast.List:
		r.hasFormatting = true
		index := n.Start
		if index <= 0 {
			index = 1
		}
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			item, ok := child.(*ast.ListItem)
			if !ok {
				continue
			}
			marker := prefix + "- "
			if n.IsOrdered() {
				marker = prefix + strconv.Itoa(index) + ". "
				index++
			}
			r.renderListItem(item, marker)
		}
	case *ast.Blockquote:
		r.hasFormatting = true
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			r.renderBlock(child, prefix+"> ")
		}
	case *ast.FencedCodeBlock:
		r.hasFormatting = true
		r.writeCodeBlock(prefix, r.blockText(n.Lines()))
	case *ast.CodeBlock:
		r.hasFormatting = true
		r.writeCodeBlock(prefix, r.blockText(n.Lines()))
	case *ast.ThematicBreak:
		r.hasFormatting = true
		r.startRow(prefix)
		r.writeText("---")
		r.finishRow()
	case *extast.Table:
		r.hasFormatting = true
		r.renderTable(n, prefix)
	default:
		if node.FirstChild() == nil {
			return
		}
		r.startRow(prefix)
		r.renderInlineChildren(node)
		r.finishRow()
	}
}

func (r *feishuPostRenderer) renderListItem(item *ast.ListItem, marker string) {
	firstLine := true
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		switch child := child.(type) {
		case *ast.Paragraph, *ast.TextBlock:
			prefix := "  "
			if firstLine {
				prefix = marker
				firstLine = false
			}
			r.startRow(prefix)
			r.renderInlineChildren(child)
			r.finishRow()
		case *ast.List:
			r.renderBlock(child, "  ")
		default:
			if firstLine {
				r.startRow(marker)
				firstLine = false
			}
			r.renderInlineChildren(child)
			r.finishRow()
		}
	}
}

func (r *feishuPostRenderer) renderTable(table *extast.Table, prefix string) {
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		r.startRow(prefix)
		for cell, index := row.FirstChild(), 0; cell != nil; cell, index = cell.NextSibling(), index+1 {
			if index > 0 {
				r.writeText(" | ")
			}
			r.renderInlineChildren(cell)
		}
		r.finishRow()
	}
}

func (r *feishuPostRenderer) renderInlineChildren(node ast.Node) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		r.renderInline(child)
	}
}

func (r *feishuPostRenderer) renderInline(node ast.Node) {
	switch n := node.(type) {
	case *ast.Text:
		r.writeTextWithURLs(string(n.Segment.Value(r.source)))
		if n.SoftLineBreak() || n.HardLineBreak() {
			r.finishRow()
		}
	case *ast.String:
		r.writeText(string(n.Value))
	case *ast.CodeSpan:
		r.hasFormatting = true
		r.writeText("`" + string(n.Text(r.source)) + "`")
	case *ast.Link:
		r.hasFormatting = true
		r.writeMarkdownLink(string(n.Destination), r.inlineText(n))
	case *ast.AutoLink:
		r.hasFormatting = true
		r.writeAutoLink(string(n.URL(r.source)))
	case *ast.Emphasis:
		r.hasFormatting = true
		style := "italic"
		if n.Level == 2 {
			style = "bold"
		}
		r.withStyle(style, func() { r.renderInlineChildren(n) })
	case *extast.Strikethrough:
		r.hasFormatting = true
		r.withStyle("lineThrough", func() { r.renderInlineChildren(n) })
	case *ast.Image:
		r.writeText(r.inlineText(n))
	case *ast.RawHTML:
		r.writeText(r.rawHTMLText(n))
	default:
		r.renderInlineChildren(node)
	}
}

func (r *feishuPostRenderer) writeMarkdownLink(destination, label string) {
	if !isFeishuSafeURL(destination) {
		r.writeText(label)
		if strings.TrimSpace(destination) != "" {
			r.writeText(" (" + destination + ")")
		}
		return
	}
	label = strings.TrimSpace(label)
	if label == "" || sameURL(label, destination) {
		label = compactFeishuURLLabel(destination)
	}
	r.writeLink(label, destination)
}

func (r *feishuPostRenderer) writeAutoLink(destination string) {
	if !isFeishuSafeURL(destination) {
		r.writeText(destination)
		return
	}
	r.writeLink(compactFeishuURLLabel(destination), destination)
}

func (r *feishuPostRenderer) writeLink(label, destination string) {
	if strings.TrimSpace(label) == "" {
		label = compactFeishuURLLabel(destination)
	}
	r.current = append(r.current, feishuPostElement{Tag: "a", Text: label, Href: destination})
	r.hasLink = true
	r.hasFormatting = true
}

func (r *feishuPostRenderer) writeBlockText(prefix, value string) {
	for index, line := range strings.Split(value, "\n") {
		linePrefix := prefix
		if index > 0 {
			linePrefix = ""
		}
		r.startRow(linePrefix)
		r.writeText(line)
		r.finishRow()
	}
}

func (r *feishuPostRenderer) writeCodeBlock(prefix, value string) {
	for index, line := range strings.Split(value, "\n") {
		linePrefix := prefix + "│ "
		if index > 0 {
			linePrefix = "│ "
		}
		r.startRow(linePrefix)
		r.writeText(line)
		r.finishRow()
	}
}

func (r *feishuPostRenderer) startRow(prefix string) {
	r.finishRow()
	if prefix != "" {
		r.writeText(prefix)
	}
}

func (r *feishuPostRenderer) finishRow() {
	if len(r.current) == 0 || !hasVisiblePostContent(r.current) {
		r.current = nil
		return
	}
	r.rows = append(r.rows, r.current)
	r.current = nil
}

func (r *feishuPostRenderer) writeText(value string) {
	r.writeStyledText(value, r.style)
}

func (r *feishuPostRenderer) writeStyledText(value string, style []string) {
	if value == "" {
		return
	}
	last := len(r.current) - 1
	if last >= 0 && r.current[last].Tag == "text" && samePostStyle(r.current[last].Style, style) {
		r.current[last].Text += value
		return
	}
	r.current = append(r.current, feishuPostElement{Tag: "text", Text: value, Style: append([]string(nil), style...)})
}

func (r *feishuPostRenderer) withStyle(style string, render func()) {
	previous := r.style
	r.style = append(append([]string(nil), previous...), style)
	render()
	r.style = previous
}

func samePostStyle(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// writeTextWithURLs fills the gap left by Goldmark Linkify: that extension
// only starts at a line head or a few ASCII delimiters, while Chinese replies
// commonly place a URL directly after punctuation such as "地址：".
func (r *feishuPostRenderer) writeTextWithURLs(value string) {
	matches := feishuBareURLPattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		r.writeText(value)
		return
	}

	start := 0
	for _, match := range matches {
		r.writeText(value[start:match[0]])
		destination, suffix := trimFeishuURLSuffix(value[match[0]:match[1]])
		if isFeishuSafeURL(destination) {
			r.writeLink(compactFeishuURLLabel(destination), destination)
		} else {
			r.writeText(destination)
		}
		r.writeText(suffix)
		start = match[1]
	}
	r.writeText(value[start:])
}

func trimFeishuURLSuffix(value string) (string, string) {
	runes := []rune(value)
	end := len(runes)
	for end > 0 {
		last := runes[end-1]
		switch last {
		case '.', ',', '!', '?', ':', ';', '\'', '"', '。', '，', '！', '？', '：', '；', '”', '’', '）', '】', '》':
			end--
		case ')':
			current := string(runes[:end])
			if strings.Count(current, ")") > strings.Count(current, "(") {
				end--
			} else {
				return string(runes[:end]), string(runes[end:])
			}
		case ']':
			current := string(runes[:end])
			if strings.Count(current, "]") > strings.Count(current, "[") {
				end--
			} else {
				return string(runes[:end]), string(runes[end:])
			}
		default:
			return string(runes[:end]), string(runes[end:])
		}
	}
	return "", value
}

func (r *feishuPostRenderer) inlineText(node ast.Node) string {
	var value strings.Builder
	var collect func(ast.Node)
	collect = func(current ast.Node) {
		switch n := current.(type) {
		case *ast.Text:
			value.Write(n.Segment.Value(r.source))
			if n.SoftLineBreak() || n.HardLineBreak() {
				value.WriteByte(' ')
			}
		case *ast.String:
			value.Write(n.Value)
		case *ast.CodeSpan:
			value.Write(n.Text(r.source))
		case *ast.AutoLink:
			value.Write(n.URL(r.source))
		default:
			for child := current.FirstChild(); child != nil; child = child.NextSibling() {
				collect(child)
			}
		}
	}
	collect(node)
	return strings.TrimSpace(value.String())
}

func (r *feishuPostRenderer) rawHTMLText(node *ast.RawHTML) string {
	var value strings.Builder
	for index := 0; index < node.Segments.Len(); index++ {
		segment := node.Segments.At(index)
		value.Write(segment.Value(r.source))
	}
	return value.String()
}

func (r *feishuPostRenderer) blockText(lines *text.Segments) string {
	var value bytes.Buffer
	for index := 0; index < lines.Len(); index++ {
		segment := lines.At(index)
		value.Write(segment.Value(r.source))
	}
	return strings.TrimSuffix(value.String(), "\n")
}

func hasVisiblePostContent(elements []feishuPostElement) bool {
	for _, element := range elements {
		if element.Tag == "a" || strings.TrimSpace(element.Text) != "" {
			return true
		}
	}
	return false
}

func isFeishuSafeURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func sameURL(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	return left == right || strings.EqualFold(left, right)
}

func compactFeishuURLLabel(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return shortenFeishuLabel(rawURL, feishuAutoLinkLabelLimit)
	}

	label := parsed.Host
	path := strings.Trim(parsed.Path, "/")
	if path != "" {
		label += "/" + path
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		label += "..."
	}
	return shortenFeishuLabel(label, feishuAutoLinkLabelLimit)
}

func shortenFeishuLabel(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	if limit == 1 {
		return "..."
	}
	return string([]rune(value)[:limit-1]) + "..."
}
