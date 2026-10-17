package agent

import (
	"context"
	"strings"

	"github.com/yurika0211/luckyagent-tools/hitl"
	"github.com/yurika0211/luckyagent/internal/tool"
)

const (
	hitlProviderRuntime = "runtime"
	hitlDefaultTimeout  = hitl.DefaultTimeout
	hitlKindApproval    = hitl.KindApproval
	hitlKindInput       = hitl.KindInput
)

type HITLResolution = hitl.Resolution
type hitlRequest = hitl.Request

// HITLGate preserves the host API while the reusable broker owns pending waits.
// The host owns chat-event presentation and final permission decisions.
type HITLGate struct{ broker *hitl.Gate }

func newHITLGate() *HITLGate { return &HITLGate{broker: hitl.New(hitl.Options{})} }

func (g *HITLGate) core() *hitl.Gate {
	if g == nil {
		return nil
	}
	return g.broker
}

func (g *HITLGate) list() []tool.PendingApproval {
	if g == nil {
		return nil
	}
	items := g.core().List()
	out := make([]tool.PendingApproval, 0, len(items))
	for _, p := range items {
		out = append(out, pendingToAPI(p))
	}
	return out
}

func (g *HITLGate) RequestAndWait(ctx context.Context, req hitlRequest, emit func(ChatEvent)) (HITLResolution, error) {
	var notify func(hitl.Pending)
	if emit != nil {
		notify = func(p hitl.Pending) {
			content := p.Summary
			if content == "" {
				content = "Approval required"
			}
			emit(ChatEvent{Type: ChatEventApprovalRequired, Name: p.Tool, Content: content,
				Approval: &ApprovalEvent{RequestID: p.ID, Tool: p.Tool, Action: firstNonEmpty(p.Action, p.Kind),
					Reason: firstNonEmpty(p.Reason, p.Prompt, p.Summary), Kind: p.Kind, Prompt: p.Prompt, SessionID: p.SessionID},
			})
		}
	}
	return g.core().RequestAndWait(ctx, req, notify)
}

func (g *HITLGate) ResolveText(sessionID, text string) (tool.PendingApproval, bool, error) {
	p, handled, err := g.core().ResolveText(sessionID, text)
	if !handled || err != nil {
		return tool.PendingApproval{}, handled, err
	}
	return pendingToAPI(p), true, nil
}

func (g *HITLGate) Resolve(id, decision, input string) (tool.PendingApproval, error) {
	p, err := g.core().Resolve(id, decision, input)
	if err != nil {
		return tool.PendingApproval{}, err
	}
	return pendingToAPI(p), nil
}

func pendingToAPI(p hitl.Pending) tool.PendingApproval {
	opts := []tool.ApprovalOption{
		{ID: "allow", Label: "允许", Kind: "allow_once"},
		{ID: "deny", Label: "拒绝", Kind: "deny"},
		{ID: "cancel", Label: "取消", Kind: "cancel"},
	}
	switch p.Kind {
	case hitlKindInput:
		opts = []tool.ApprovalOption{
			{ID: "submit", Label: "提交", Kind: "submit"},
			{ID: "cancel", Label: "取消", Kind: "cancel"},
		}
	case hitlKindCredential:
		opts = []tool.ApprovalOption{
			{ID: "submit", Label: "保存凭据", Kind: "submit"},
			{ID: "cancel", Label: "取消", Kind: "cancel"},
		}
	}
	summary := strings.TrimSpace(p.Summary)
	if summary == "" {
		switch p.Kind {
		case hitlKindInput:
			summary = "需要你补充信息"
		case hitlKindCredential:
			summary = "需要你在安全表单填写凭据"
		default:
			summary = "需要批准工具调用: " + p.Tool
		}
	}
	params := map[string]any{
		"kind":   p.Kind,
		"tool":   p.Tool,
		"action": p.Action,
		"prompt": p.Prompt,
		"args":   p.Args,
	}
	if p.Kind == hitlKindCredential {
		params["secure"] = true
	}
	return tool.PendingApproval{
		Provider:  hitlProviderRuntime,
		ID:        p.ID,
		Method:    p.Kind,
		SessionID: p.SessionID,
		Reason:    p.Reason,
		Summary:   summary,
		Params:    params,
		Options:   opts,
		CreatedAt: p.CreatedAt,
	}
}

type hitlEmitKey struct{}

func withHITLEmitter(ctx context.Context, emit func(ChatEvent)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, hitlEmitKey{}, emit)
}

func hitlEmitterFrom(ctx context.Context) func(ChatEvent) {
	if ctx == nil {
		return nil
	}
	emit, _ := ctx.Value(hitlEmitKey{}).(func(ChatEvent))
	return emit
}

var interpretHITLReply = hitl.InterpretReply
var isAllowDecision = hitl.IsAllowDecision
var isDenyDecision = hitl.IsDenyDecision

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
