package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/tool"
)

func TestTelegramFormatterPaginatesAndEscapesToolMetadata(t *testing.T) {
	tools := make([]*tool.Tool, 0, 11)
	for index := 1; index <= 11; index++ {
		description := "Read a local file"
		if index == 1 {
			description = `Read <unsafe> & "quoted" content`
		}
		tools = append(tools, &tool.Tool{
			Name:        "tool-" + fmt.Sprintf("%02d", index),
			Description: description,
			Category:    tool.CatBuiltin,
			Enabled:     true,
		})
	}

	formatted := (TelegramFormatter{}).FormatToolsList(tools, 1)
	if !strings.Contains(formatted, "<b>Available tools</b> (page 1/2)") {
		t.Fatalf("expected first page header, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Read &lt;unsafe&gt; &amp; &#34;quoted&#34; content") {
		t.Fatalf("expected escaped metadata, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "tool-11") {
		t.Fatalf("expected first page to contain ten tools, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "<code>/tools 2</code>") || !strings.Contains(formatted, "<code>/tool &lt;name&gt;</code>") {
		t.Fatalf("expected pagination and detail navigation, got:\n%s", formatted)
	}

	secondPage := (TelegramFormatter{}).FormatToolsList(tools, 2)
	if !strings.Contains(secondPage, "tool-11") || !strings.Contains(secondPage, "(page 2/2)") {
		t.Fatalf("expected second page to contain final tool, got:\n%s", secondPage)
	}
}

func TestTelegramFormatterShowsToolAndSkillDetails(t *testing.T) {
	formattedTool := (TelegramFormatter{}).FormatToolDetail(&tool.Tool{
		Name:        "file<read>",
		Description: "Read <files> & metadata",
		Category:    tool.CatBuiltin,
		Permission:  tool.PermApprove,
		Enabled:     true,
		Source:      "core&tools",
		Parameters: map[string]tool.Param{
			"zeta":  {Type: "string", Description: "Last > first", Required: false},
			"alpha": {Type: "integer", Description: "First & required", Required: true, Default: 1},
		},
	})
	if !strings.Contains(formattedTool, "<b>Parameters</b>") || !strings.Contains(formattedTool, "file&lt;read&gt;") {
		t.Fatalf("expected formatted tool details, got:\n%s", formattedTool)
	}
	if strings.Index(formattedTool, "<code>alpha</code>") > strings.Index(formattedTool, "<code>zeta</code>") {
		t.Fatalf("expected parameters in deterministic order, got:\n%s", formattedTool)
	}
	if !strings.Contains(formattedTool, "Read &lt;files&gt; &amp; metadata") || !strings.Contains(formattedTool, "core&amp;tools") {
		t.Fatalf("expected escaped tool details, got:\n%s", formattedTool)
	}
	for _, expected := range []string{"<b>Example</b>", `<code>file&lt;read&gt;(alpha=1)</code>`, "<b>Notes</b>"} {
		if !strings.Contains(formattedTool, expected) {
			t.Fatalf("expected %q in tool detail:\n%s", expected, formattedTool)
		}
	}

	formattedSkill := (TelegramFormatter{}).FormatSkillDetail(&tool.SkillInfo{
		Name:        "web<research>",
		Description: "Research <sources> & summarize",
		Aliases:     []string{"research&web"},
		Available:   true,
		Tools: []tool.SkillToolDef{{
			Name:          "search",
			Description:   "Find <results>",
			ExposeToModel: true,
			Parameters: map[string]tool.Param{
				"query": {Type: "string", Required: true},
			},
		}},
	})
	for _, expected := range []string{"web&lt;research&gt;", "research&amp;web", "Find &lt;results&gt;", "<code>query</code> (required, string)"} {
		if !strings.Contains(formattedSkill, expected) {
			t.Fatalf("expected %q in skill detail:\n%s", expected, formattedSkill)
		}
	}
}

func TestTelegramListPageParser(t *testing.T) {
	tests := []struct {
		args    string
		page    int
		showAll bool
		valid   bool
	}{
		{args: "", page: 1, valid: true},
		{args: "2", page: 2, valid: true},
		{args: "ALL", page: 1, showAll: true, valid: true},
		{args: "0"},
		{args: "one"},
		{args: "1 extra"},
	}
	for _, test := range tests {
		t.Run(test.args, func(t *testing.T) {
			page, showAll, err := parseTelegramListPage(test.args)
			if test.valid {
				if err != nil || page != test.page || showAll != test.showAll {
					t.Fatalf("parseTelegramListPage(%q) = (%d, %t, %v), want (%d, %t, nil)", test.args, page, showAll, err, test.page, test.showAll)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected %q to be rejected", test.args)
			}
		})
	}
}

func TestTelegramToolAndSkillCommandsUseHTMLAndSupportAllPages(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()
	adapter, sent := newAdapterWithCapturedMessages(t)
	adapter.cfg.RateLimit = 10000
	handler.adapter = adapter

	registry := handler.agent.(*mockAgentProvider).toolsVal
	for index := 1; index <= 11; index++ {
		registry.Register(&tool.Tool{
			Name:        "tool-" + fmt.Sprintf("%02d", index),
			Description: "Tool description",
			Category:    tool.CatBuiltin,
			Parameters: map[string]tool.Param{
				"path": {Type: "string", Description: "Path to inspect", Required: true},
			},
		})
	}
	handler.agent.(*mockAgentProvider).skillsVal = []*tool.SkillInfo{
		{
			Name:        "research",
			Aliases:     []string{"web-research"},
			Description: "Research sources",
			Available:   true,
		},
	}

	message := func(command, args string) *gateway.Message {
		return &gateway.Message{
			Chat:      gateway.Chat{ID: "12345", Type: gateway.ChatPrivate},
			Text:      "/" + command,
			IsCommand: true,
			Command:   command,
			Args:      args,
		}
	}
	if err := handler.handleCommand(context.Background(), message("tools", "all")); err != nil {
		t.Fatalf("/tools all: %v", err)
	}
	if len(*sent) != 2 {
		t.Fatalf("expected two tool pages, got %#v", *sent)
	}
	for _, response := range *sent {
		if response.ParseMode != "HTML" {
			t.Fatalf("expected HTML parse mode, got %#v", response)
		}
	}
	if !strings.Contains((*sent)[1].Text, "tool-11") {
		t.Fatalf("expected final tool on the second page, got %#v", (*sent)[1])
	}

	if err := handler.handleCommand(context.Background(), message("tool", "TOOL-01")); err != nil {
		t.Fatalf("/tool: %v", err)
	}
	if detail := (*sent)[len(*sent)-1].Text; !strings.Contains(detail, "<b>Parameters</b>") || !strings.Contains(detail, "<code>path</code>") {
		t.Fatalf("expected tool detail response, got:\n%s", detail)
	}

	if err := handler.handleCommand(context.Background(), message("skill", "web-research")); err != nil {
		t.Fatalf("/skill: %v", err)
	}
	if detail := (*sent)[len(*sent)-1].Text; !strings.Contains(detail, "<b>research</b>") {
		t.Fatalf("expected skill detail response, got:\n%s", detail)
	}

	*sent = nil
	adapter.rememberTelegramThread("-100123", "777", "42")
	topicMessage := message("tools", "")
	topicMessage.ID = "777"
	topicMessage.Chat = gateway.Chat{ID: "-100123", Type: gateway.ChatSuperGroup}
	if err := handler.handleCommand(context.Background(), topicMessage); err != nil {
		t.Fatalf("topic /tools: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected one topic response, got %#v", *sent)
	}
	if response := (*sent)[0]; response.ParseMode != "HTML" || response.ReplyTo != "777" || response.ThreadID != "42" {
		t.Fatalf("expected reply-aware HTML topic response, got %#v", response)
	}
}
