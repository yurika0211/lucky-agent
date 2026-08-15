package telegram

import (
	"bytes"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	xhtml "golang.org/x/net/html"
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

type telegramHTMLTag struct {
	name  string
	open  string
	close string
}

// formatTelegramRichText renders markdown-ish LLM output into the subset of
// Telegram HTML that is both supported and predictable across clients.
func formatTelegramRichText(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.TrimSpace(repairTelegramMarkdownFences(input))
	if input == "" {
		return ""
	}

	source := []byte(input)
	root := mdParser.Parser().Parse(text.NewReader(source))
	r := &telegramHTMLRenderer{source: source}
	return strings.TrimSpace(r.render(root))
}

func repairTelegramMarkdownFences(input string) string {
	if input == "" || (!strings.Contains(input, "```") && !strings.Contains(input, "~~~")) {
		return input
	}

	var b strings.Builder
	inFence := false
	closeMarker := ""

	for _, line := range strings.SplitAfter(input, "\n") {
		lineMarker, hasFence := telegramMarkdownFenceMarker(line)
		if inFence && !hasFence && isTelegramFenceRecoveryBoundary(line) {
			writeTelegramFenceClose(&b, closeMarker)
			inFence = false
			closeMarker = ""
		}
		if inFence && hasFence && !isTelegramMarkdownFenceCloser(line, closeMarker) {
			writeTelegramFenceClose(&b, closeMarker)
			inFence = false
			closeMarker = ""
		}

		b.WriteString(line)
		if !hasFence {
			continue
		}
		if !inFence {
			inFence = true
			closeMarker = strings.Repeat(lineMarker[:1], len(lineMarker))
			continue
		}
		if isTelegramMarkdownFenceCloser(line, closeMarker) {
			inFence = false
			closeMarker = ""
		}
	}
	if inFence && closeMarker != "" {
		writeTelegramFenceClose(&b, closeMarker)
	}
	return b.String()
}

func writeTelegramFenceClose(b *strings.Builder, marker string) {
	if marker == "" {
		return
	}
	if b.Len() > 0 {
		current := b.String()
		if !strings.HasSuffix(current, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString(marker)
	b.WriteString("\n")
}

func isTelegramFenceRecoveryBoundary(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if lower == "references:" || trimmed == "参考说明：" {
		return true
	}
	if strings.HasPrefix(trimmed, "# ") ||
		strings.HasPrefix(trimmed, "## ") ||
		strings.HasPrefix(trimmed, "### ") ||
		strings.HasPrefix(trimmed, "#### ") {
		return true
	}
	if looksLikeCJKProseLeadIn(trimmed) {
		return true
	}
	return looksLikeNumberedProseHeading(trimmed)
}

func looksLikeCJKProseLeadIn(trimmed string) bool {
	if !containsCJK(trimmed) {
		return false
	}
	if !strings.HasSuffix(trimmed, ":") && !strings.HasSuffix(trimmed, "：") {
		return false
	}
	for _, prefix := range []string{"//", "#", ";", "/*", "*", "--", "<!--"} {
		if strings.HasPrefix(trimmed, prefix) {
			return false
		}
	}
	return true
}

func looksLikeNumberedProseHeading(trimmed string) bool {
	dot := strings.Index(trimmed, ". ")
	if dot <= 0 || dot > 16 {
		return false
	}
	hasDigit := false
	for _, r := range trimmed[:dot] {
		if r >= '0' && r <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return false
	}
	return strings.Contains(trimmed, "—") || strings.Contains(trimmed, "：") || containsCJK(trimmed)
}

func containsCJK(s string) bool {
	for _, r := range s {
		if (r >= '\u4e00' && r <= '\u9fff') ||
			(r >= '\u3040' && r <= '\u30ff') ||
			(r >= '\u3400' && r <= '\u4dbf') {
			return true
		}
	}
	return false
}

func telegramMarkdownFenceMarker(line string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimRight(line, "\n"))
	if len(trimmed) < 3 {
		return "", false
	}
	if strings.HasPrefix(trimmed, "```") {
		return strings.Repeat("`", countLeadingTelegramFenceRunes(trimmed, '`')), true
	}
	if strings.HasPrefix(trimmed, "~~~") {
		return strings.Repeat("~", countLeadingTelegramFenceRunes(trimmed, '~')), true
	}
	return "", false
}

func countLeadingTelegramFenceRunes(s string, target rune) int {
	count := 0
	for _, r := range s {
		if r != target {
			break
		}
		count++
	}
	return count
}

func isTelegramMarkdownFenceCloser(line string, opener string) bool {
	if opener == "" {
		return false
	}
	trimmed := strings.TrimSpace(strings.TrimRight(line, "\n"))
	if len(trimmed) < len(opener) {
		return false
	}
	for _, r := range trimmed {
		if r != rune(opener[0]) {
			return false
		}
	}
	return true
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

func sanitizeTelegramHTML(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}

	tokenizer := xhtml.NewTokenizer(strings.NewReader(input))
	var output strings.Builder
	stack := make([]telegramHTMLTag, 0, 8)

	for {
		tokenType := tokenizer.Next()
		if tokenType == xhtml.ErrorToken {
			break
		}

		raw := string(tokenizer.Raw())
		token := tokenizer.Token()
		switch tokenType {
		case xhtml.TextToken:
			output.WriteString(html.EscapeString(token.Data))
		case xhtml.StartTagToken:
			tag, ok := normalizeTelegramHTMLTag(token)
			if !ok {
				output.WriteString(html.EscapeString(raw))
				continue
			}
			output.WriteString(tag.open)
			stack = append(stack, tag)
		case xhtml.EndTagToken:
			tag, ok := normalizeTelegramHTMLEndTag(token.Data)
			if !ok {
				output.WriteString(html.EscapeString(raw))
				continue
			}
			match := -1
			for index := len(stack) - 1; index >= 0; index-- {
				if stack[index].name == tag.name {
					match = index
					break
				}
			}
			if match < 0 {
				output.WriteString(html.EscapeString(raw))
				continue
			}
			for index := len(stack) - 1; index >= match; index-- {
				output.WriteString(stack[index].close)
			}
			stack = stack[:match]
		default:
			output.WriteString(html.EscapeString(raw))
		}
	}

	for index := len(stack) - 1; index >= 0; index-- {
		output.WriteString(stack[index].close)
	}
	return strings.TrimSpace(output.String())
}

func normalizeTelegramHTMLTag(token xhtml.Token) (telegramHTMLTag, bool) {
	name := strings.ToLower(token.Data)
	switch name {
	case "b", "strong":
		return telegramHTMLTag{name: "b", open: "<b>", close: "</b>"}, true
	case "i", "em":
		return telegramHTMLTag{name: "i", open: "<i>", close: "</i>"}, true
	case "u", "ins":
		return telegramHTMLTag{name: "u", open: "<u>", close: "</u>"}, true
	case "s", "strike", "del":
		return telegramHTMLTag{name: "s", open: "<s>", close: "</s>"}, true
	case "code":
		return telegramHTMLTag{name: "code", open: "<code>", close: "</code>"}, true
	case "pre":
		return telegramHTMLTag{name: "pre", open: "<pre>", close: "</pre>"}, true
	case "tg-spoiler":
		return telegramHTMLTag{name: "tg-spoiler", open: "<tg-spoiler>", close: "</tg-spoiler>"}, true
	case "span":
		if telegramHTMLAttribute(token, "class") == "tg-spoiler" {
			return telegramHTMLTag{name: "tg-spoiler", open: "<tg-spoiler>", close: "</tg-spoiler>"}, true
		}
	case "blockquote":
		if telegramHTMLHasAttribute(token, "expandable") {
			return telegramHTMLTag{name: "blockquote", open: "<blockquote expandable>", close: "</blockquote>"}, true
		}
		return telegramHTMLTag{name: "blockquote", open: "<blockquote>", close: "</blockquote>"}, true
	case "a":
		href := telegramHTMLAttribute(token, "href")
		if isTelegramSafeURL(href) {
			return telegramHTMLTag{name: "a", open: `<a href="` + html.EscapeString(href) + `">`, close: "</a>"}, true
		}
	}
	return telegramHTMLTag{}, false
}

func normalizeTelegramHTMLEndTag(name string) (telegramHTMLTag, bool) {
	switch strings.ToLower(name) {
	case "b", "strong":
		return telegramHTMLTag{name: "b", close: "</b>"}, true
	case "i", "em":
		return telegramHTMLTag{name: "i", close: "</i>"}, true
	case "u", "ins":
		return telegramHTMLTag{name: "u", close: "</u>"}, true
	case "s", "strike", "del":
		return telegramHTMLTag{name: "s", close: "</s>"}, true
	case "code":
		return telegramHTMLTag{name: "code", close: "</code>"}, true
	case "pre":
		return telegramHTMLTag{name: "pre", close: "</pre>"}, true
	case "tg-spoiler", "span":
		return telegramHTMLTag{name: "tg-spoiler", close: "</tg-spoiler>"}, true
	case "blockquote":
		return telegramHTMLTag{name: "blockquote", close: "</blockquote>"}, true
	case "a":
		return telegramHTMLTag{name: "a", close: "</a>"}, true
	default:
		return telegramHTMLTag{}, false
	}
}

func telegramHTMLAttribute(token xhtml.Token, key string) string {
	for _, attr := range token.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func telegramHTMLHasAttribute(token xhtml.Token, key string) bool {
	for _, attr := range token.Attr {
		if strings.EqualFold(attr.Key, key) {
			return true
		}
	}
	return false
}

func isTelegramSafeURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "tg", "mailto":
		return true
	default:
		return false
	}
}

func splitTelegramHTMLChunks(message string, maxLen int) []string {
	if message == "" {
		return []string{""}
	}
	if maxLen <= 0 || maxLen > 4096 {
		maxLen = 4096
	}
	if len(message) <= maxLen {
		return []string{message}
	}

	var chunks []string
	var current strings.Builder
	stack := make([]telegramHTMLTag, 0, 8)

	closingLength := func() int {
		length := 0
		for _, tag := range stack {
			length += len(tag.close)
		}
		return length
	}
	reopen := func() {
		for _, tag := range stack {
			current.WriteString(tag.open)
		}
	}
	flush := func(reopenTags bool) {
		if current.Len() == 0 {
			return
		}
		for index := len(stack) - 1; index >= 0; index-- {
			current.WriteString(stack[index].close)
		}
		chunks = append(chunks, current.String())
		current.Reset()
		if reopenTags {
			reopen()
		}
	}

	for remaining := message; remaining != ""; {
		if remaining[0] == '<' {
			end := strings.IndexByte(remaining, '>')
			if end >= 0 {
				rawTag := remaining[:end+1]
				if strings.HasPrefix(rawTag, "</") {
					if len(rawTag) <= maxLen-current.Len() {
						current.WriteString(rawTag)
						if len(stack) > 0 {
							stack = stack[:len(stack)-1]
						}
						remaining = remaining[end+1:]
						continue
					}
					flush(true)
					continue
				}

				name := telegramHTMLTagName(rawTag)
				close := telegramHTMLCloseTag(name)
				if close != "" && current.Len()+len(rawTag)+closingLength()+len(close) > maxLen && current.Len() > 0 {
					flush(true)
					continue
				}
				current.WriteString(rawTag)
				stack = append(stack, telegramHTMLTag{name: name, open: rawTag, close: close})
				remaining = remaining[end+1:]
				continue
			}
		}

		nextTag := strings.IndexByte(remaining, '<')
		text := remaining
		if nextTag >= 0 {
			text = remaining[:nextTag]
		}
		textLength := len(text)
		for text != "" {
			available := maxLen - current.Len() - closingLength()
			if available <= 0 {
				flush(true)
				continue
			}
			part := telegramHTMLTextPrefix(text, available)
			if part == "" {
				for _, r := range text {
					part = string(r)
					break
				}
			}
			if len(part) < len(text) {
				part = telegramPreferredHTMLBreak(part)
			}
			current.WriteString(part)
			text = text[len(part):]
			if text != "" {
				flush(true)
			}
		}
		remaining = remaining[textLength:]
	}

	flush(false)
	return chunks
}

func telegramHTMLTagName(tag string) string {
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">"))
	name = strings.TrimPrefix(name, "/")
	if index := strings.IndexAny(name, " \t\r\n"); index >= 0 {
		name = name[:index]
	}
	return strings.ToLower(name)
}

func telegramHTMLCloseTag(name string) string {
	switch name {
	case "b", "i", "u", "s", "code", "pre", "tg-spoiler", "blockquote", "a":
		return "</" + name + ">"
	default:
		return ""
	}
}

func telegramHTMLTextPrefix(text string, maxLen int) string {
	part := truncateTelegramRunes(text, maxLen)
	if ampersand := strings.LastIndex(part, "&"); ampersand >= 0 && !strings.Contains(part[ampersand:], ";") {
		part = part[:ampersand]
	}
	return part
}

func telegramPreferredHTMLBreak(text string) string {
	for _, marker := range []string{"\n\n", "\n", "。", "！", "？", ". ", "; ", "；"} {
		if index := strings.LastIndex(text, marker); index >= 0 {
			return text[:index+len(marker)]
		}
	}
	for index := len(text); index > 0; {
		lastRune, size := utf8.DecodeLastRuneInString(text[:index])
		index -= size
		if unicode.IsSpace(lastRune) {
			return text[:index+size]
		}
	}
	return text
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
