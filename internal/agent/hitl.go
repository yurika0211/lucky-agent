package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/tool"
)

const (
	hitlProviderRuntime = "runtime"
	hitlDefaultTimeout  = 10 * time.Minute
	hitlKindApproval    = "approval"
	hitlKindInput       = "input"
)

// HITLResolution is the host's answer to a pending human-in-the-loop request.
type HITLResolution struct {
	Decision string // allow | deny | cancel | (for input: submit)
	Input    string // free-form text when Kind=input
}

type hitlPending struct {
	ID        string
	Kind      string
	SessionID string
	Tool      string
	Action    string
	Reason    string
	Prompt    string
	Args      map[string]any
	Summary   string
	CreatedAt time.Time
	ch        chan HITLResolution
}

// HITLGate holds in-flight approval/input waits for the local agent runtime.
// External coding-agent bridges (Codex/Grok) keep their own pending lists.
type HITLGate struct {
	mu      sync.Mutex
	pending map[string]*hitlPending
}

func newHITLGate() *HITLGate {
	return &HITLGate{pending: make(map[string]*hitlPending)}
}

func (g *HITLGate) list() []tool.PendingApproval {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]tool.PendingApproval, 0, len(g.pending))
	for _, p := range g.pending {
		out = append(out, pendingToAPI(p))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func pendingToAPI(p *hitlPending) tool.PendingApproval {
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

// RequestAndWait registers a pending HITL item, notifies the host via emit,
// and blocks until resolved, canceled, or timed out.
func (g *HITLGate) RequestAndWait(
	ctx context.Context,
	req hitlRequest,
	emit func(ChatEvent),
) (HITLResolution, error) {
	if g == nil {
		return HITLResolution{}, fmt.Errorf("hitl gate is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = newHITLRequestID()
	}
	p := &hitlPending{
		ID:        id,
		Kind:      req.Kind,
		SessionID: req.SessionID,
		Tool:      req.Tool,
		Action:    req.Action,
		Reason:    req.Reason,
		Prompt:    req.Prompt,
		Args:      req.Args,
		Summary:   req.Summary,
		CreatedAt: time.Now().UTC(),
		ch:        make(chan HITLResolution, 1),
	}
	if p.Kind == "" {
		p.Kind = hitlKindApproval
	}

	g.mu.Lock()
	g.pending[id] = p
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		delete(g.pending, id)
		g.mu.Unlock()
	}()

	if emit != nil {
		content := p.Summary
		if content == "" {
			content = "Approval required"
		}
		emit(ChatEvent{
			Type:    ChatEventApprovalRequired,
			Name:    p.Tool,
			Content: content,
			Approval: &ApprovalEvent{
				RequestID: id,
				Tool:      p.Tool,
				Action:    firstNonEmpty(p.Action, p.Kind),
				Reason:    firstNonEmpty(p.Reason, p.Prompt, p.Summary),
				Kind:      p.Kind,
				Prompt:    p.Prompt,
				SessionID: p.SessionID,
			},
		})
	}

	waitCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		waitCtx, cancel = context.WithTimeout(ctx, hitlDefaultTimeout)
		defer cancel()
	}

	select {
	case res := <-p.ch:
		return res, nil
	case <-waitCtx.Done():
		return HITLResolution{}, fmt.Errorf("hitl request %s timed out or canceled: %w", id, waitCtx.Err())
	}
}

// ResolveText tries to match a free-form user reply to a pending request.
// It prefers an explicit request id in the text, then a single pending item
// for the session. Returns false when the text is not an approval reply.
func (g *HITLGate) ResolveText(sessionID, text string) (tool.PendingApproval, bool, error) {
	if g == nil {
		return tool.PendingApproval{}, false, nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return tool.PendingApproval{}, false, nil
	}
	g.mu.Lock()
	var matches []*hitlPending
	for _, p := range g.pending {
		if sessionID == "" || p.SessionID == sessionID {
			matches = append(matches, p)
		}
	}
	g.mu.Unlock()
	if len(matches) == 0 {
		return tool.PendingApproval{}, false, nil
	}
	var target *hitlPending
	for _, p := range matches {
		if strings.Contains(text, p.ID) {
			target = p
			break
		}
	}
	if target == nil && len(matches) == 1 {
		target = matches[0]
	}
	if target == nil {
		return tool.PendingApproval{}, false, fmt.Errorf("multiple pending approvals; include the request id")
	}
	decision, input, ok := interpretHITLReply(target.Kind, text)
	if !ok {
		return tool.PendingApproval{}, false, nil
	}
	resolved, err := g.Resolve(target.ID, decision, input)
	return resolved, true, err
}

func interpretHITLReply(kind, text string) (decision, input string, ok bool) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	// Strip a trailing request id so "允许 hitl-abc" still parses.
	fields := strings.Fields(lower)
	head := lower
	if len(fields) > 0 {
		head = fields[0]
	}
	switch head {
	case "允许", "同意", "批准", "yes", "y", "allow", "approve", "ok":
		return "allow", "", true
	case "拒绝", "取消", "no", "n", "deny", "reject", "cancel":
		return "deny", "", true
	}
	if kind == hitlKindInput {
		return "submit", trimmed, true
	}
	// Credential values must come from the masked form, never from a chat reply.
	return "", "", false
}

// Resolve completes a pending request. decision is case-insensitive.
func (g *HITLGate) Resolve(requestID, decision, input string) (tool.PendingApproval, error) {
	if g == nil {
		return tool.PendingApproval{}, fmt.Errorf("hitl gate is unavailable")
	}
	requestID = strings.TrimSpace(requestID)
	decision = strings.ToLower(strings.TrimSpace(decision))
	if requestID == "" || decision == "" {
		return tool.PendingApproval{}, fmt.Errorf("approval_id and decision are required")
	}

	g.mu.Lock()
	p, ok := g.pending[requestID]
	if !ok {
		g.mu.Unlock()
		return tool.PendingApproval{}, fmt.Errorf("approval %q is not pending", requestID)
	}
	api := pendingToAPI(p)
	ch := p.ch
	g.mu.Unlock()

	res := HITLResolution{Decision: decision, Input: input}
	select {
	case ch <- res:
	default:
		// Already resolved by another caller.
	}
	return api, nil
}

type hitlRequest struct {
	ID        string
	Kind      string
	SessionID string
	Tool      string
	Action    string
	Reason    string
	Prompt    string
	Summary   string
	Args      map[string]any
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
	if emit, ok := ctx.Value(hitlEmitKey{}).(func(ChatEvent)); ok {
		return emit
	}
	return nil
}

func newHITLRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("hitl-%d", time.Now().UnixNano())
	}
	return "hitl-" + hex.EncodeToString(b[:])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func isAllowDecision(decision string) bool {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow", "allow_once", "allow_always", "accept", "approve", "yes", "y", "submit":
		return true
	default:
		return false
	}
}

func isDenyDecision(decision string) bool {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "deny", "reject", "cancel", "cancelled", "no", "n":
		return true
	default:
		return false
	}
}
