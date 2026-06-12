package telegram

import (
	"strings"
	"testing"
)

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
	want := "<b>代码</b>\n<pre><code>print(&#34;hello&#34;)</code></pre>"
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}

func TestFormatTelegramRichTextCodeBlockWithoutLanguage(t *testing.T) {
	input := "```text\nhello\n```"
	got := formatTelegramRichText(input)
	want := "<pre><code>hello</code></pre>"
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}

func TestFormatTelegramRichTextPreservesCodeClassHTML(t *testing.T) {
	input := `<pre><code class="language-python">print("hi")</code></pre>`
	got := formatTelegramRichText(input)
	want := `<pre><code>print(&#34;hi&#34;)</code></pre>`
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}

func TestFormatTelegramRichTextStripsPreLanguageHTML(t *testing.T) {
	input := `<pre language="c"><code>int main() {}</code></pre>`
	got := formatTelegramRichText(input)
	want := `<pre><code>int main() {}</code></pre>`
	if got != want {
		t.Fatalf("formatTelegramRichText() = %q, want %q", got, want)
	}
}

func TestFormatTelegramRichTextClosesDanglingFenceBeforeReferences(t *testing.T) {
	input := "讲解：\n```asm\nmov %rax, %rbx\n\nReferences:\n[1] Local file. README.md"
	got := formatTelegramRichText(input)
	if !strings.Contains(got, "</code></pre>\nReferences:\n[1] Local file. README.md") {
		t.Fatalf("expected references to render outside code block, got:\n%s", got)
	}
}

func TestFormatTelegramRichTextRecoversScreenshotStyleNestedCodeBlocks(t *testing.T) {
	input := "🧱 1. 寄存器模型 — CPU 的临时变量\n```asm\nmov %rsi, %rax\n\n⚡ 2. 常用的指令 — 数据搬运 mov\n```asm\nmov $5, %rax\n```\n\nReferences:\n[1] Local directory listing."
	got := formatTelegramRichText(input)

	if strings.Contains(got, "<code>mov %rsi, %rax\n\n⚡ 2. 常用的指令") {
		t.Fatalf("expected second prose heading to be outside first code block, got:\n%s", got)
	}
	if !strings.Contains(got, "</code></pre>\n⚡ 2. 常用的指令") {
		t.Fatalf("expected first code block to close before second heading, got:\n%s", got)
	}
	if !strings.Contains(got, "</code></pre>\nReferences:") {
		t.Fatalf("expected references to be outside code block, got:\n%s", got)
	}
}

func TestFormatTelegramRichTextKeepsScreenshotProseAfterCodeFenceOutsidePre(t *testing.T) {
	input := "看一个典型的 C 函数怎么编译的：\n" +
		"```c\n" +
		"void func() {\n" +
		"    int x;    // 局部变量\n" +
		"}\n\n" +
		"编译后大概长这样：\n\n" +
		"```asm\n" +
		"func:\n" +
		"    push    rbp\n" +
		"    mov     rbp, rsp\n" +
		"    sub     rsp, 16\n\n" +
		"## 💡 为什么是 `-4` 而不是 `-1`？\n\n" +
		"因为 **int 占 4 个字节**！偏移 4 字节正是为了给 `int` 变量腾出完整空间：\n\n" +
		"| 类型 | 大小 | 典型偏移 |\n" +
		"|------|------|----------|\n" +
		"| `char` | 1B | `rbp-1` |\n" +
		"| **`int`** | **4B** | **`rbp-4`** |\n\n" +
		"## 🎯 一句话总结\n\n" +
		"**`[rbp-4]` = 当前函数栈帧中，从栈底往下第 1 个局部变量的位置**"

	got := formatTelegramRichText(input)
	if strings.Contains(got, "<code>void func()") && strings.Contains(got, "## 💡") && strings.Contains(got, "一句话总结</code></pre>") {
		t.Fatalf("expected prose after code fences to render outside code block, got:\n%s", got)
	}
	for _, outside := range []string{
		"编译后大概长这样：",
		"<b>💡 为什么是 <code>-4</code> 而不是 <code>-1</code>？</b>",
		"| 类型 | 大小 | 典型偏移 |",
		"<b>🎯 一句话总结</b>",
	} {
		if !strings.Contains(got, outside) {
			t.Fatalf("expected %q outside code block, got:\n%s", outside, got)
		}
	}
	if strings.Contains(got, "<code>编译后大概长这样：") ||
		strings.Contains(got, "<code>| 类型 |") ||
		strings.Contains(got, "一句话总结</code></pre>") {
		t.Fatalf("expected prose/table/summary outside code block, got:\n%s", got)
	}
	if strings.Count(got, "<pre>") != strings.Count(got, "</pre>") {
		t.Fatalf("unbalanced pre tags in:\n%s", got)
	}
	if strings.Count(got, "<code>") != strings.Count(got, "</code>") {
		t.Fatalf("unbalanced code tags in:\n%s", got)
	}
}
