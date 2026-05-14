package telegram

import (
	"bytes"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var (
	allowedHTMLTagRe         = regexp.MustCompile(`(?i)</?(?:b|strong|i|em|u|ins|s|strike|del|tg-spoiler|blockquote)\s*>|<pre(?:\s+language="[a-zA-Z0-9_+-]+")?\s*>|</pre>|<code(?:\s+class="language-[a-zA-Z0-9_+-]+")?\s*>|</code>|<span\s+class="tg-spoiler"\s*>|</span>|<blockquote(?:\s+expandable)?\s*>`)
	telegramCodeClassTagRe   = regexp.MustCompile(`(?i)<code\s+class="language-[a-zA-Z0-9_+-]+"\s*>`)
	telegramPreLanguageTagRe = regexp.MustCompile(`(?i)<pre\s+language="[a-zA-Z0-9_+-]+"\s*>`)
	mdParser                 = goldmark.New()
)

type telegramHTMLBlock struct {
	token string
	html  string
}

// formatTelegramRichText renders markdown-ish LLM output into the subset of
// Telegram HTML that is both supported and predictable across clients.
func formatTelegramRichText(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	source := []byte(input)
	root := mdParser.Parser().Parse(text.NewReader(source))
	r := &telegramHTMLRenderer{source: source}
	return strings.TrimSpace(r.render(root))
}

type telegramHTMLRenderer struct {
	source []byte
}

func (r *telegramHTMLRenderer) render(node ast.Node) string {
	switch n := node.(type) {
	case *ast.Document:
		return r.renderDocument(n)
	case *ast.Heading:
		return r.renderHeading(n)
	case *ast.Paragraph:
		return r.renderParagraph(n)
	case *ast.TextBlock:
		return r.renderContainerChildren(n, "")
	case *ast.Blockquote:
		return r.renderBlockquote(n)
	case *ast.List:
		return r.renderList(n)
	case *ast.ListItem:
		return r.renderListItem(n)
	case *ast.FencedCodeBlock:
		return r.renderFencedCodeBlock(n)
	case *ast.CodeBlock:
		return r.renderCodeBlock(n)
	case *ast.CodeSpan:
		return "<code>" + html.EscapeString(string(n.Text(r.source))) + "</code>"
	case *ast.Emphasis:
		return r.renderEmphasis(n)
	case *ast.Text:
		return r.renderText(n)
	case *ast.String:
		return html.EscapeString(string(n.Value))
	case *ast.ThematicBreak:
		return "---"
	case *ast.Link:
		return r.renderLink(n)
	case *ast.AutoLink:
		return r.renderAutoLink(n)
	case *ast.RawHTML:
		return r.renderRawHTML(n)
	case *ast.HTMLBlock:
		return r.renderHTMLBlock(n)
	default:
		return r.renderContainerChildren(node, "")
	}
}

func (r *telegramHTMLRenderer) renderDocument(doc *ast.Document) string {
	parts := make([]string, 0, doc.ChildCount())
	nodes := make([]ast.Node, 0, doc.ChildCount())
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		part := strings.TrimSpace(r.render(child))
		if part != "" {
			parts = append(parts, part)
			nodes = append(nodes, child)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteString(documentSeparator(nodes[i-1], nodes[i]))
		}
		b.WriteString(part)
	}
	return b.String()
}

func (r *telegramHTMLRenderer) renderHeading(n *ast.Heading) string {
	content := strings.TrimSpace(r.renderInlineChildren(n))
	if content == "" {
		return ""
	}
	return "<b>" + content + "</b>"
}

func (r *telegramHTMLRenderer) renderParagraph(n *ast.Paragraph) string {
	return strings.TrimSpace(r.renderInlineChildren(n))
}

func (r *telegramHTMLRenderer) renderBlockquote(n *ast.Blockquote) string {
	content := strings.TrimSpace(r.renderContainerChildren(n, "\n"))
	if content == "" {
		return ""
	}
	return "<blockquote>" + content + "</blockquote>"
}

func (r *telegramHTMLRenderer) renderList(n *ast.List) string {
	lines := make([]string, 0, n.ChildCount())
	index := n.Start
	if index <= 0 {
		index = 1
	}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		item, ok := child.(*ast.ListItem)
		if !ok {
			continue
		}
		body := strings.TrimSpace(r.renderListItem(item))
		if body == "" {
			continue
		}
		prefix := "• "
		if n.IsOrdered() {
			prefix = strconv.Itoa(index) + ". "
			index++
		}
		body = strings.ReplaceAll(body, "\n", "\n   ")
		lines = append(lines, prefix+body)
	}
	return strings.Join(lines, "\n")
}

func (r *telegramHTMLRenderer) renderListItem(n *ast.ListItem) string {
	parts := make([]string, 0, n.ChildCount())
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		part := strings.TrimSpace(r.render(child))
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "\n")
}

func (r *telegramHTMLRenderer) renderFencedCodeBlock(n *ast.FencedCodeBlock) string {
	return "<pre><code>" + html.EscapeString(r.blockText(n.Lines())) + "</code></pre>"
}

func (r *telegramHTMLRenderer) renderCodeBlock(n *ast.CodeBlock) string {
	return "<pre><code>" + html.EscapeString(r.blockText(n.Lines())) + "</code></pre>"
}

func (r *telegramHTMLRenderer) renderEmphasis(n *ast.Emphasis) string {
	content := r.renderInlineChildren(n)
	if content == "" {
		return ""
	}
	if n.Level >= 2 {
		return "<b>" + content + "</b>"
	}
	return "<i>" + content + "</i>"
}

func (r *telegramHTMLRenderer) renderText(n *ast.Text) string {
	value := html.EscapeString(string(n.Segment.Value(r.source)))
	if n.HardLineBreak() || n.SoftLineBreak() {
		return value + "\n"
	}
	return value
}

func (r *telegramHTMLRenderer) renderLink(n *ast.Link) string {
	label := strings.TrimSpace(r.renderInlineChildren(n))
	if label == "" {
		label = html.EscapeString(string(n.Destination))
	}
	return `<a href="` + html.EscapeString(string(n.Destination)) + `">` + label + `</a>`
}

func (r *telegramHTMLRenderer) renderAutoLink(n *ast.AutoLink) string {
	url := string(n.URL(r.source))
	label := html.EscapeString(url)
	return `<a href="` + html.EscapeString(url) + `">` + label + `</a>`
}

func (r *telegramHTMLRenderer) renderRawHTML(n *ast.RawHTML) string {
	var b strings.Builder
	for i := 0; i < n.Segments.Len(); i++ {
		seg := n.Segments.At(i)
		part := string((&seg).Value(r.source))
		b.WriteString(r.preserveAllowedHTML(part))
	}
	return b.String()
}

func (r *telegramHTMLRenderer) renderHTMLBlock(n *ast.HTMLBlock) string {
	return r.preserveAllowedHTML(r.blockText(n.Lines()))
}

func (r *telegramHTMLRenderer) renderInlineChildren(node ast.Node) string {
	var b strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		b.WriteString(r.render(child))
	}
	return strings.TrimSpace(b.String())
}

func (r *telegramHTMLRenderer) renderContainerChildren(node ast.Node, sep string) string {
	parts := make([]string, 0, node.ChildCount())
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		part := strings.TrimSpace(r.render(child))
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, sep)
}

func (r *telegramHTMLRenderer) blockText(lines *text.Segments) string {
	var buf bytes.Buffer
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		buf.Write(segment.Value(r.source))
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func (r *telegramHTMLRenderer) preserveAllowedHTML(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = telegramCodeClassTagRe.ReplaceAllString(raw, "<code>")
	raw = telegramPreLanguageTagRe.ReplaceAllString(raw, "<pre>")

	placeholders := make([]telegramHTMLBlock, 0, 8)
	idx := 0
	masked := allowedHTMLTagRe.ReplaceAllStringFunc(raw, func(match string) string {
		token := telegramHTMLToken("HTMLTAG", idx)
		idx++
		placeholders = append(placeholders, telegramHTMLBlock{token: token, html: match})
		return token
	})

	escaped := html.EscapeString(masked)
	for _, ph := range placeholders {
		escaped = strings.ReplaceAll(escaped, html.EscapeString(ph.token), ph.html)
	}
	return escaped
}

func telegramHTMLToken(kind string, idx int) string {
	return "TG_" + kind + "_TOKEN_" + strconv.Itoa(idx) + "_END"
}

func documentSeparator(prev, next ast.Node) string {
	if isCompactBlock(prev) || isCompactBlock(next) {
		return "\n"
	}
	return "\n\n"
}

func isCompactBlock(node ast.Node) bool {
	switch node.(type) {
	case *ast.Heading, *ast.List, *ast.FencedCodeBlock, *ast.CodeBlock, *ast.Blockquote:
		return true
	default:
		return false
	}
}

// escapeMarkdownV2 is kept for backward compatibility with existing tests and
// any helper callers that still need raw MarkdownV2 escaping.
func escapeMarkdownV2(text string) string {
	const spoilerToken = "__TG_SPOILER__"
	text = strings.ReplaceAll(text, "||", spoilerToken)
	special := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	for _, ch := range special {
		text = strings.ReplaceAll(text, ch, "\\"+ch)
	}
	text = strings.ReplaceAll(text, spoilerToken, "||")
	return text
}
