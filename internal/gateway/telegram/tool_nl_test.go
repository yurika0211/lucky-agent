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
		assert.Contains(t, got, "我先陪你把这条思路慢慢捋顺喔：")
	})

	t.Run("tool call narrative", func(t *testing.T) {
		got := humanizeToolCallProgress(2, "file_read", `{"path":"tasks/QUEUE.md"}`)
		assert.Contains(t, got, "我先陪你翻一下文件")
		assert.Contains(t, got, "tasks/QUEUE.md")
	})

	t.Run("skill narrative", func(t *testing.T) {
		got := humanizeToolCallProgress(1, "skill_run", `{"skill_name":"deep-research"}`)
		assert.Contains(t, got, "技能")
		assert.Contains(t, got, "deep-research")
	})

	t.Run("tool result narrative", func(t *testing.T) {
		got := humanizeToolResultProgress(3, "web_search", "ok")
		assert.Contains(t, got, "搜索结果")
	})
}
