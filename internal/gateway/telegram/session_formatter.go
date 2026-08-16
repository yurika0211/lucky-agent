package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/session"
)

const telegramSessionTitleLimit = 30

type telegramSessionListItem struct {
	info  session.SessionInfo
	index int
}

// FormatSessionsList renders a page of the session list as Telegram HTML.
func (TelegramFormatter) FormatSessionsList(infos []session.SessionInfo, currentID string, page int) string {
	pageInfos, currentPage, pageCount := telegramListPage(infos, page)
	pageStart := (currentPage - 1) * telegramCommandListPageSize
	items := make([]telegramSessionListItem, 0, len(pageInfos))
	for index, info := range pageInfos {
		items = append(items, telegramSessionListItem{info: info, index: pageStart + index + 1})
	}

	active := make([]telegramSessionListItem, 0, len(items))
	tasks := make([]telegramSessionListItem, 0, len(items))
	others := make([]telegramSessionListItem, 0, len(items))
	for _, item := range items {
		switch {
		case isTelegramTaskSession(item.info):
			tasks = append(tasks, item)
		case isTelegramActiveSession(item.info, currentID):
			active = append(active, item)
		default:
			others = append(others, item)
		}
	}

	var body strings.Builder
	body.WriteString(fmt.Sprintf("📚 <b>会话列表</b>（第 %d/%d 页，共 %d 个会话）\n\n", currentPage, pageCount, len(infos)))
	writeTelegramSessionGroup(&body, "🟢 <b>活跃会话</b>", active, currentID)
	writeTelegramSessionGroup(&body, "⏸️ <b>后台任务</b>", tasks, currentID)
	writeTelegramSessionGroup(&body, "📝 <b>其他会话</b>", others, currentID)

	body.WriteString("<b>查看更多</b>\n")
	if currentPage > 1 {
		body.WriteString(fmt.Sprintf("• <code>/sessions %d</code> — 上一页\n", currentPage-1))
	}
	if currentPage < pageCount {
		body.WriteString(fmt.Sprintf("• <code>/sessions %d</code> — 下一页\n", currentPage+1))
	}
	body.WriteString("• <code>/sessions all</code> — 显示全部\n")
	body.WriteString("• <code>/session &lt;序号或 ID&gt;</code> — 查看详情\n")
	body.WriteString("• <code>/resume &lt;序号或 ID&gt;</code> — 切换会话")
	return strings.TrimSpace(body.String())
}

func writeTelegramSessionGroup(body *strings.Builder, title string, items []telegramSessionListItem, currentID string) {
	if len(items) == 0 {
		return
	}
	body.WriteString(fmt.Sprintf("%s（%d 个）\n", title, len(items)))
	for index, item := range items {
		last := index == len(items)-1
		prefix, indent := "├─", "│  "
		if last {
			prefix, indent = "└─", "   "
		}
		current := ""
		if item.info.ID == currentID {
			current = " <b>（当前）</b>"
		}
		body.WriteString(fmt.Sprintf("%s [%d] %s%s\n", prefix, item.index, escapeTelegramCommandHTML(telegramSessionDisplayTitle(item.info.Title)), current))
		body.WriteString(fmt.Sprintf("%s└─ %d 条消息 | 最近更新 %s\n", indent, item.info.MessageCount, telegramSessionShortTime(item.info.UpdatedAt)))
	}
	body.WriteString("\n")
}

// FormatSessionDetail renders session metadata and up to three visible recent messages.
func (TelegramFormatter) FormatSessionDetail(info session.SessionInfo, sess *session.Session, currentID string) string {
	var body strings.Builder
	body.WriteString("📝 <b>会话详情</b>\n\n")
	body.WriteString(fmt.Sprintf("<b>标题：</b>%s\n", escapeTelegramCommandHTML(telegramSessionDisplayTitle(info.Title))))
	body.WriteString(fmt.Sprintf("<b>ID：</b><code>%s</code>\n", escapeTelegramCommandHTML(info.ID)))
	body.WriteString(fmt.Sprintf("<b>消息数：</b>%d 条\n", info.MessageCount))
	body.WriteString(fmt.Sprintf("<b>创建时间：</b>%s\n", telegramSessionFullTime(info.CreatedAt)))
	body.WriteString(fmt.Sprintf("<b>最近更新：</b>%s\n", telegramSessionFullTime(info.UpdatedAt)))
	body.WriteString(fmt.Sprintf("<b>状态：</b>%s", telegramSessionStatus(info, currentID)))

	if sess != nil {
		if recent := telegramRecentSessionMessages(sess, 3); len(recent) > 0 {
			body.WriteString("\n\n<b>最近消息</b>\n")
			for _, message := range recent {
				body.WriteString(fmt.Sprintf("• [%s] %s\n", telegramSessionRoleLabel(message.role), escapeTelegramCommandHTML(message.content)))
			}
		}
	}

	body.WriteString(fmt.Sprintf("\n💡 使用 <code>/resume %s</code> 切换到此会话", escapeTelegramCommandHTML(info.ID)))
	return strings.TrimSpace(body.String())
}

type telegramSessionMessagePreview struct {
	role    string
	content string
}

func telegramRecentSessionMessages(sess *session.Session, limit int) []telegramSessionMessagePreview {
	if sess == nil || limit <= 0 {
		return nil
	}
	messages := sess.GetMessages()
	previews := make([]telegramSessionMessagePreview, 0, limit)
	for index := len(messages) - 1; index >= 0 && len(previews) < limit; index-- {
		message := messages[index]
		if strings.EqualFold(message.Role, "system") {
			continue
		}
		content := cleanTelegramSessionTitle(message.Content)
		if content == "" {
			continue
		}
		previews = append(previews, telegramSessionMessagePreview{
			role:    message.Role,
			content: truncateTelegramSessionText(content, 80),
		})
	}
	for left, right := 0, len(previews)-1; left < right; left, right = left+1, right-1 {
		previews[left], previews[right] = previews[right], previews[left]
	}
	return previews
}

func cleanTelegramSessionTitle(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"[telegram delivery rule]",
		"[user request]",
		"[replied telegram message]",
		"[working memory",
		"[core memory]",
		"[session history",
		"[retrieved knowledge",
		"if you want telegram to send",
	} {
		if index := strings.Index(lower, marker); index >= 0 {
			value = value[:index]
			lower = lower[:index]
		}
	}

	visibleLines := make([]string, 0, 1)
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "media:") {
			continue
		}
		visibleLines = append(visibleLines, line)
	}
	return truncateTelegramSessionText(strings.Join(visibleLines, " "), telegramSessionTitleLimit)
}

func telegramSessionDisplayTitle(title string) string {
	if title = cleanTelegramSessionTitle(title); title != "" {
		return title
	}
	return "（未命名）"
}

func truncateTelegramSessionText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func isTelegramActiveSession(info session.SessionInfo, currentID string) bool {
	if info.ID == currentID {
		return true
	}
	return !info.UpdatedAt.IsZero() && time.Since(info.UpdatedAt) < time.Hour
}

func isTelegramTaskSession(info session.SessionInfo) bool {
	title := strings.ToLower(info.Title)
	return strings.Contains(title, "delegate-task") ||
		strings.Contains(title, "delegate_task") ||
		strings.Contains(title, "background-task") ||
		strings.Contains(title, "background_task")
}

func telegramSessionStatus(info session.SessionInfo, currentID string) string {
	switch {
	case isTelegramTaskSession(info):
		return "⏸️ 后台任务"
	case isTelegramActiveSession(info, currentID):
		return "🟢 活跃"
	default:
		return "📝 历史会话"
	}
}

func telegramSessionRoleLabel(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return "用户"
	case "assistant":
		return "助手"
	case "tool":
		return "工具"
	default:
		return "消息"
	}
}

func telegramSessionShortTime(value time.Time) string {
	if value.IsZero() {
		return "未知"
	}
	return value.Format("01-02 15:04")
}

func telegramSessionFullTime(value time.Time) string {
	if value.IsZero() {
		return "未知"
	}
	return value.Format("2006-01-02 15:04:05")
}
