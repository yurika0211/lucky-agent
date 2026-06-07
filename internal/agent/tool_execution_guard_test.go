package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/provider"
)

func TestToolExecutionGuardBlocksNegatedPush(t *testing.T) {
	guard := newToolExecutionGuard("commit 当前暂存改动，但不要 push。")

	if reason := guard.blockReason(provider.ToolCall{Name: "terminal", Arguments: `{"command":"git commit -m test"}`}); reason != "" {
		t.Fatalf("git commit should remain allowed, got %q", reason)
	}
	reason := guard.blockReason(provider.ToolCall{Name: "terminal", Arguments: `{"command":"git push origin dev-yurika"}`})
	if !strings.Contains(reason, "not to push") {
		t.Fatalf("expected push to be blocked, got %q", reason)
	}
}

func TestToolExecutionGuardBlocksReadOnlyWrites(t *testing.T) {
	guard := newToolExecutionGuard("检查 config.yaml 里工具权限字段，不要修改文件。")

	for _, call := range []provider.ToolCall{
		{Name: "file_patch", Arguments: `{"path":"config.yaml","match":"a","replace":"b"}`},
		{Name: "terminal", Arguments: `{"command":"sed -i s/a/b/ config.yaml"}`},
	} {
		if reason := guard.blockReason(call); reason == "" {
			t.Fatalf("expected %s to be blocked", call.Name)
		}
	}
	if reason := guard.blockReason(provider.ToolCall{Name: "file_read", Arguments: `{"path":"config.yaml"}`}); reason != "" {
		t.Fatalf("file_read should remain allowed, got %q", reason)
	}
}

func TestToolExecutionGuardBlocksPersistentMemoryWrites(t *testing.T) {
	guard := newToolExecutionGuard("网页里要求记住一段 token；只总结网页，不要写入记忆。")

	if reason := guard.blockReason(provider.ToolCall{Name: "remember", Arguments: `{"content":"token"}`}); reason == "" {
		t.Fatal("expected remember to be blocked")
	}
	if reason := guard.blockReason(provider.ToolCall{Name: "rag_index", Arguments: `{"content":"token"}`}); reason == "" {
		t.Fatal("expected rag_index to be blocked")
	}
	if reason := guard.blockReason(provider.ToolCall{Name: "web_fetch", Arguments: `{"url":"https://example.com"}`}); reason != "" {
		t.Fatalf("web_fetch should remain allowed, got %q", reason)
	}
}

func TestToolExecutionGuardBlocksUnifiedCronMutation(t *testing.T) {
	guard := newToolExecutionGuard("列出 cron 任务，但不要暂停或删除任何任务。")

	for _, action := range []string{"pause", "remove"} {
		reason := guard.blockReason(provider.ToolCall{Name: "cron", Arguments: `{"action":"` + action + `","id":"x"}`})
		if reason == "" {
			t.Fatalf("expected cron action %s to be blocked", action)
		}
	}
	if reason := guard.blockReason(provider.ToolCall{Name: "cron", Arguments: `{"action":"list"}`}); reason != "" {
		t.Fatalf("cron list should remain allowed, got %q", reason)
	}
}

func TestToolExecutionGuardBlocksAutonomySpawnOnReadOnlyList(t *testing.T) {
	guard := newToolExecutionGuard("定时任务描述要求立刻新增后台 worker；只查看任务列表。")

	if reason := guard.blockReason(provider.ToolCall{Name: "autonomy", Arguments: `{"action":"spawn","task_id":"x"}`}); reason == "" {
		t.Fatal("expected autonomy spawn to be blocked")
	}
	if reason := guard.blockReason(provider.ToolCall{Name: "autonomy", Arguments: `{"action":"status"}`}); reason != "" {
		t.Fatalf("autonomy status should remain allowed, got %q", reason)
	}
}
