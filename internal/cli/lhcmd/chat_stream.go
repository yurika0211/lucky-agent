package lhcmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/session"
)

type chatStreamResult struct {
	Response string
}

func runChatStreamInput(ctx context.Context, a *agent.Agent, sess *session.Session, input agent.UserTurnInput, loopCfg agent.LoopConfig) (*chatStreamResult, error) {
	if a == nil {
		return nil, fmt.Errorf("agent is nil")
	}
	if sess == nil {
		return nil, fmt.Errorf("session is nil")
	}

	events, err := a.ChatWithSessionStreamInputWithLoopConfig(ctx, sess.ID, input, loopCfg)
	if err != nil {
		return nil, err
	}
	prevResolver := hitlResolver
	hitlResolver = func(requestID, decision, inputText string) error {
		_, err := a.RespondApprovalWithInput(ctx, "runtime", requestID, decision, inputText)
		return err
	}
	defer func() { hitlResolver = prevResolver }()

	mq := startMarquee("Lucky> ", "thinking")
	defer mq.Stop()

	var finalResponse string

	for event := range events {
		switch event.Type {
		case agent.ChatEventThinking:
			mq.Update(formatChatStatus(event.Content, "thinking"))
		case agent.ChatEventToolCall:
			mq.Update(formatToolStatus("tool", event.Name))
		case agent.ChatEventApprovalRequired:
			mq.Stop()
			if err := promptHITL(event); err != nil {
				return nil, err
			}
			mq = startMarquee("Lucky> ", "waiting")
		case agent.ChatEventToolResult:
			mq.Update(formatToolStatus("done", event.Name))
		case agent.ChatEventDone:
			finalResponse = event.Content
		case agent.ChatEventError:
			return nil, event.Err
		}
	}

	return &chatStreamResult{Response: finalResponse}, nil
}

func promptHITL(event agent.ChatEvent) error {
	kind := "approval"
	requestID := ""
	prompt := strings.TrimSpace(event.Content)
	toolName := strings.TrimSpace(event.Name)
	if event.Approval != nil {
		if event.Approval.Kind != "" {
			kind = event.Approval.Kind
		}
		requestID = strings.TrimSpace(event.Approval.RequestID)
		if text := strings.TrimSpace(event.Approval.Prompt); text != "" {
			prompt = text
		} else if text := strings.TrimSpace(event.Approval.Reason); text != "" && prompt == "" {
			prompt = text
		}
		if toolName == "" {
			toolName = event.Approval.Tool
		}
	}
	fmt.Println()
	if kind == "input" {
		if prompt == "" {
			prompt = "请补充信息"
		}
		fmt.Printf("📝 %s\n> ", prompt)
	} else {
		fmt.Printf("🔐 需要确认工具调用")
		if toolName != "" {
			fmt.Printf(" [%s]", toolName)
		}
		fmt.Println()
		if prompt != "" {
			fmt.Println(prompt)
		}
		fmt.Print("输入 y 允许 / n 拒绝 > ")
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return fmt.Errorf("read approval input: %w", err)
	}
	line = strings.TrimSpace(line)
	decision := "deny"
	input := ""
	if kind == "input" {
		decision = "submit"
		input = line
		if line == "" {
			decision = "cancel"
		}
	} else {
		switch strings.ToLower(line) {
		case "y", "yes", "allow", "允许", "同意":
			decision = "allow"
		case "n", "no", "deny", "拒绝", "取消":
			decision = "deny"
		default:
			decision = "deny"
		}
	}
	if requestID == "" {
		return fmt.Errorf("approval request is missing an id")
	}
	if hitlResolver == nil {
		return fmt.Errorf("approval resolver is not configured")
	}
	return hitlResolver(requestID, decision, input)
}

var hitlResolver func(requestID, decision, input string) error

func formatChatStatus(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	raw = strings.TrimSuffix(raw, "...")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "Thinking")
	raw = strings.TrimPrefix(raw, "thinking")
	raw = strings.Trim(raw, ".() ")
	if raw == "" {
		return fallback
	}
	return fallback + " " + raw
}

func formatToolStatus(prefix, toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return prefix
	}
	return prefix + " " + toolName
}
