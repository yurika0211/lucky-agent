package telegram

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHumanizeToolCall(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args string
		want string
	}{
		{
			name: "web search",
			tool: "web_search",
			args: `{"query":"golang context cancel"}`,
			want: "正在联网搜索：",
		},
		{
			name: "shell cmd",
			tool: "shell",
			args: `{"cmd":"go test ./..."}`,
			want: "正在执行命令：",
		},
		{
			name: "file read",
			tool: "file_read",
			args: `{"path":"/tmp/demo.txt"}`,
			want: "正在读取文件：/tmp/demo.txt",
		},
		{
			name: "unknown fallback",
			tool: "custom_tool",
			args: `{"name":"demo-task"}`,
			want: "正在调用工具 custom_tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := humanizeToolCall(tt.tool, tt.args)
			assert.Contains(t, got, tt.want)
		})
	}
}

func TestHumanizeProgressNarrative(t *testing.T) {
	t.Run("thinking", func(t *testing.T) {
		got := humanizeThinkingProgress("先看下 tasks 目录状态")
		assert.Contains(t, got, "先整理一下当前思路：")
	})

	t.Run("internal thinking marker is hidden", func(t *testing.T) {
		got := humanizeThinkingProgress("Thinking... (round 2)")
		assert.Equal(t, "", got)
	})

	t.Run("tool call narrative", func(t *testing.T) {
		got := humanizeToolCallProgress(2, "file_read", `{"path":"tasks/QUEUE.md"}`)
		assert.Contains(t, got, "先查看文件")
		assert.Contains(t, got, "tasks/QUEUE.md")
	})

	t.Run("skill narrative", func(t *testing.T) {
		got := humanizeToolCallProgress(1, "skill_run", `{"skill_name":"deep-research"}`)
		assert.Contains(t, got, "处理链路")
		assert.NotContains(t, got, "deep-research")
	})

	t.Run("tool result narrative", func(t *testing.T) {
		got := humanizeToolResultProgress(3, "web_search", "ok")
		assert.Contains(t, got, "搜索结果已经拿到")
	})
}

func TestTelegramProgressCards(t *testing.T) {
	t.Run("thinking card", func(t *testing.T) {
		got := renderTelegramThinkingCard("先看下 tasks 目录状态")
		assert.Contains(t, got, "<b>💭 Reasoning Trace</b>")
		assert.Contains(t, got, "<blockquote expandable>")
		assert.Contains(t, got, "先看下 tasks 目录状态")
	})

	t.Run("summary card removes blank lines", func(t *testing.T) {
		got := renderTelegramSummaryCard("First line\n\nSecond line")
		assert.Contains(t, got, "First line\nSecond line")
		assert.NotContains(t, got, "First line\n\nSecond line")
	})

	t.Run("history card joins without blank lines", func(t *testing.T) {
		got := renderTelegramProgressHistoryCard([]string{"One", "Two"})
		assert.Contains(t, got, "One\nTwo")
		assert.NotContains(t, got, "One\n\nTwo")
	})

	t.Run("internal thinking marker is suppressed", func(t *testing.T) {
		got := renderTelegramThinkingCard("Thinking... (round 2)")
		assert.Equal(t, "", got)
	})

	t.Run("tool trace card", func(t *testing.T) {
		got := renderTelegramToolTraceCard([]telegramToolTraceStep{
			{Name: "web_search", Args: `{"query":"luckyharness telegram"}`, Result: "Results for: luckyharness telegram", Success: true},
			{Name: "file_read", Args: `{"path":"internal/gateway/telegram/handler.go"}`, Result: "package telegram", Success: true},
		})
		assert.Contains(t, got, "<b>🛠 Tool Trace</b>")
		assert.Contains(t, got, "<pre><code>")
		assert.Contains(t, got, "[1]")
		assert.Contains(t, got, "web_search")
		assert.Contains(t, got, "→")
		assert.Contains(t, got, "[2]")
		assert.Contains(t, got, "file_read")
		assert.Contains(t, got, "Done · 2 tools")
	})

	t.Run("tool trace keeps executable skill tools compact", func(t *testing.T) {
		got := renderTelegramToolTraceCard([]telegramToolTraceStep{
			{Name: "skill_obsidian_run", Args: `{"name":"vault"}`, Result: "ok", Success: true},
			{Name: "skill_read", Args: `{"name":"obsidian"}`, Result: "ok", Success: true},
		})
		assert.Contains(t, got, "skill_obsidian_run")
		assert.Contains(t, got, "skill_read")
		assert.Contains(t, got, "[1]")
		assert.Contains(t, got, "[2]")
		assert.Contains(t, got, "Done · 2 tools")
	})

	t.Run("tool trace omits agent orchestration tools", func(t *testing.T) {
		got := renderTelegramToolTraceCard([]telegramToolTraceStep{
			{Name: "delegate_task", Args: `{"task":"inspect repo"}`, Result: "ok", Success: true},
			{Name: "autonomy_worker_spawn", Args: `{"task":"nightly replay"}`, Result: "ok", Success: true},
			{Name: "web_search", Args: `{"query":"agent"}`, Result: "ok", Success: true},
		})
		assert.Contains(t, got, "Tool Trace")
		assert.Contains(t, got, "web_search")
		assert.NotContains(t, got, "delegate")
		assert.NotContains(t, got, "autonomy")
	})

	t.Run("agent trace shows orchestration tools", func(t *testing.T) {
		got := renderTelegramAgentTraceCard([]telegramToolTraceStep{
			{Name: "delegate_task", Args: `{"task":"inspect repo"}`, Result: "ok", Success: true},
			{Name: "autonomy_worker_spawn", Args: `{"task":"nightly replay"}`, Result: "ok", Success: true},
			{Name: "heartbeat_trigger", Args: `{"task":"tick"}`, Result: "error: timeout", Success: false},
			{Name: "web_search", Args: `{"query":"agent"}`, Result: "ok", Success: true},
		})
		assert.Contains(t, got, "<b>🧭 Agent Trace</b>")
		assert.Contains(t, got, "delegate")
		assert.Contains(t, got, "inspect repo")
		assert.Contains(t, got, "autonomy")
		assert.Contains(t, got, "nightly replay")
		assert.Contains(t, got, "heartbeat")
		assert.Contains(t, got, "⚠️")
		assert.Contains(t, got, "Done · 3 agent steps")
		assert.NotContains(t, got, "web_search")
	})
}
