package telegram

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/session"
)

func TestCleanTelegramSessionTitleRemovesInternalRulesAndTruncatesRunes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "delivery rule",
			input: "关于我的记忆\n\n[Telegram delivery rule]\nIf you want Telegram to send a file, save it first.",
			want:  "关于我的记忆",
		},
		{
			name:  "core memory and media",
			input: "可见标题\nMEDIA:/tmp/result.txt\n[Core Memory]\nprivate context",
			want:  "可见标题",
		},
		{
			name:  "unicode limit",
			input: strings.Repeat("会", telegramSessionTitleLimit+1),
			want:  strings.Repeat("会", telegramSessionTitleLimit) + "...",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cleanTelegramSessionTitle(test.input); got != test.want {
				t.Fatalf("cleanTelegramSessionTitle() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTelegramFormatterPaginatesAndCategorizesSessions(t *testing.T) {
	now := time.Now()
	infos := make([]session.SessionInfo, 0, 11)
	for index := 1; index <= 11; index++ {
		info := session.SessionInfo{
			ID:           fmt.Sprintf("session-%02d", index),
			Title:        fmt.Sprintf("历史会话 %02d", index),
			MessageCount: index,
			CreatedAt:    now.Add(-48 * time.Hour),
			UpdatedAt:    now.Add(-2 * time.Hour),
		}
		switch index {
		case 1:
			info.Title = "活跃 <会话>\n[Telegram delivery rule]\ninternal"
			info.UpdatedAt = now
		case 2:
			info.Title = "delegate-task: code review"
			info.UpdatedAt = now
		}
		infos = append(infos, info)
	}

	firstPage := (TelegramFormatter{}).FormatSessionsList(infos, "session-01", 1)
	for _, expected := range []string{
		"<b>会话列表</b>（第 1/2 页，共 11 个会话）",
		"🟢 <b>活跃会话</b>（1 个）",
		"⏸️ <b>后台任务</b>（1 个）",
		"活跃 &lt;会话&gt;",
		"<code>/sessions 2</code>",
		"<code>/session &lt;序号或 ID&gt;</code>",
	} {
		if !strings.Contains(firstPage, expected) {
			t.Fatalf("expected %q in first page:\n%s", expected, firstPage)
		}
	}
	if strings.Contains(firstPage, "Telegram delivery rule") || strings.Contains(firstPage, "历史会话 11") {
		t.Fatalf("first page leaked internal content or a later page: %s", firstPage)
	}

	lastPage := (TelegramFormatter{}).FormatSessionsList(infos, "", 99)
	if !strings.Contains(lastPage, "第 2/2 页") || !strings.Contains(lastPage, "[11] 历史会话 11") {
		t.Fatalf("expected clamped final page, got:\n%s", lastPage)
	}
}

func TestTelegramFormatterSessionDetailHidesInternalContent(t *testing.T) {
	manager, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("new session manager: %v", err)
	}
	sess := manager.NewWithTitle("部署方案\n[User request]\ninternal request")
	sess.AddMessage("system", "must never be shown")
	sess.AddMessage("user", "可见问题\n\n[Telegram delivery rule]\ninternal rule")
	sess.AddMessage("assistant", "回复 <内容>")
	info := manager.ListInfo()[0]

	detail := (TelegramFormatter{}).FormatSessionDetail(info, sess, sess.ID)
	for _, expected := range []string{"<b>会话详情</b>", "部署方案", "[用户] 可见问题", "[助手] 回复 &lt;内容&gt;", "🟢 活跃"} {
		if !strings.Contains(detail, expected) {
			t.Fatalf("expected %q in detail:\n%s", expected, detail)
		}
	}
	for _, leaked := range []string{"User request", "Telegram delivery rule", "must never be shown"} {
		if strings.Contains(detail, leaked) {
			t.Fatalf("detail leaked %q:\n%s", leaked, detail)
		}
	}
}
