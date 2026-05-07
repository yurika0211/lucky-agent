package telegram

import "testing"

func TestFormatTelegramRichTextPreservesSupportedHTML(t *testing.T) {
	input := "<b>我在。</b>\n<i>样式正常</i>\n<code>ok</code>"
	got := formatTelegramRichText(input)
	if got != input {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, input)
	}
}

func TestFormatTelegramRichTextEscapesUnsupportedHTML(t *testing.T) {
	input := `<script>alert("x")</script>`
	got := formatTelegramRichText(input)
	want := `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;`
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}

func TestFormatTelegramRichTextConvertsMarkdownishText(t *testing.T) {
	input := "**粗体** _斜体_ `代码`"
	got := formatTelegramRichText(input)
	want := "<b>粗体</b> <i>斜体</i> <code>代码</code>"
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}

func TestFormatTelegramRichTextFormatsHeadingsAndLists(t *testing.T) {
	input := "## 核心思路\n- 第一点\n- 第二点\n1. 先做这个"
	got := formatTelegramRichText(input)
	want := "<b>核心思路</b>\n• 第一点\n• 第二点\n1. 先做这个"
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}

func TestFormatTelegramRichTextKeepsCodeBlockAheadOfLayoutFormatting(t *testing.T) {
	input := "## 代码\n```python\nprint(\"hello\")\n```"
	got := formatTelegramRichText(input)
	want := "<b>代码</b>\n<pre><code class=\"language-python\">print(&#34;hello&#34;)</code></pre>"
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}

func TestFormatTelegramRichTextCodeBlockWithoutLanguage(t *testing.T) {
	input := "```text\nhello\n```"
	got := formatTelegramRichText(input)
	want := "<pre><code class=\"language-text\">hello</code></pre>"
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}

func TestFormatTelegramRichTextPreservesCodeClassHTML(t *testing.T) {
	input := `<pre><code class="language-python">print("hi")</code></pre>`
	got := formatTelegramRichText(input)
	want := `<pre><code class="language-python">print(&#34;hi&#34;)</code></pre>`
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}
