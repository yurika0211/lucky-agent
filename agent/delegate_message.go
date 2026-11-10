package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	delegateruntime "github.com/yurika0211/luckyagent-orchestrator/delegate"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

const parentMessageQueueLimit = 4

// parentMessage is one unprocessed note from the parent session. The child
// loop appends it as a user message after the current tool round.
type parentMessage struct {
	taskID  string
	session string
	body    string
}

// parentMessageQueue holds at most four unprocessed messages for one child.
// A fifth message is an error. Nothing is dropped.
type parentMessageQueue struct {
	mu       sync.Mutex
	byTask   map[string][]parentMessage
	draining map[string]bool
}

func (q *parentMessageQueue) enqueue(taskID, sessionID, body string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.byTask == nil {
		q.byTask = map[string][]parentMessage{}
	}
	queued := q.byTask[taskID]
	if len(queued) >= parentMessageQueueLimit {
		return fmt.Errorf("parent message queue is full for %s (4 unprocessed messages)", taskID)
	}
	q.byTask[taskID] = append(queued, parentMessage{taskID: taskID, session: sessionID, body: body})
	return nil
}

func (q *parentMessageQueue) count(taskID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.byTask[taskID])
}

func (q *parentMessageQueue) take(taskID string) []parentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	queued := q.byTask[taskID]
	if len(queued) == 0 {
		return nil
	}
	delete(q.byTask, taskID)
	out := append([]parentMessage(nil), queued...)
	return out
}

func (q *parentMessageQueue) restore(taskID string, taken []parentMessage) {
	if len(taken) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.byTask == nil {
		q.byTask = map[string][]parentMessage{}
	}
	q.byTask[taskID] = append(append([]parentMessage(nil), taken...), q.byTask[taskID]...)
}

func (q *parentMessageQueue) beginDrain(taskID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.draining == nil {
		q.draining = map[string]bool{}
	}
	if q.draining[taskID] {
		return false
	}
	q.draining[taskID] = true
	return true
}

func (q *parentMessageQueue) endDrain(taskID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.draining, taskID)
}

// parentMessages returns the process-wide queue for this agent. The box is
// published once. Concurrent action=message and the child drain must not
// race on the pointer or replace a queue that already holds notes.
func (a *Agent) parentMessages() *parentMessageQueue {
	if a == nil {
		return nil
	}
	a.parentMessagesOnce.Do(func() {
		a.parentMessagesBox = &parentMessageQueue{}
	})
	return a.parentMessagesBox
}

// installSubagentMessage wires action=message onto the wrapped subagent tool.
// A live child queues the text. A finished child uses the existing resume_from
// path. A child loop cannot send, even when recursive delegate is open.
func (a *Agent) installSubagentMessage() {
	if a == nil || a.tools == nil || a.delegate == nil {
		return
	}
	current, ok := a.tools.Get("subagent")
	if !ok || current == nil || current.ContextDetailedHandler == nil {
		return
	}
	inner := current.ContextDetailedHandler
	wrapped := *current
	copied := make(map[string]tool.Param, len(wrapped.Parameters)+2)
	for key, value := range wrapped.Parameters {
		copied[key] = value
	}
	if action, ok := copied["action"]; ok {
		action.Description = strings.TrimSpace(action.Description + " message queues text onto a pending or running child, or resumes a finished one.")
		copied["action"] = action
	}
	copied["message"] = tool.Param{
		Type:        "string",
		Description: "Text for action=message. Queued after the child's current tool round. A finished task resumes with this text as the new work.",
		Required:    false,
	}
	if taskID, ok := copied["task_id"]; ok && !strings.Contains(taskID.Description, "action=message") {
		taskID.Description = strings.TrimSpace(taskID.Description + " Also the child that receives action=message.")
		copied["task_id"] = taskID
	}
	wrapped.Parameters = copied
	wrapped.ContextDetailedHandler = func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
		if !strings.EqualFold(delegateStringArg(args, "action"), "message") {
			return inner(exec, args)
		}
		out, err := a.handleSubagentMessage(exec, args)
		return tool.ToolCallResult{Output: out}, err
	}
	wrapped.Handler = func(args map[string]any) (string, error) {
		result, err := wrapped.ContextDetailedHandler(tool.ExecutionContext{}, args)
		return result.Output, err
	}
	a.tools.Register(&wrapped)
}

func (a *Agent) handleSubagentMessage(exec tool.ExecutionContext, args map[string]any) (string, error) {
	if tool.DelegateTaskID(exec.Context) != "" || strings.EqualFold(strings.TrimSpace(exec.Source), "subagent") {
		return "", fmt.Errorf("action=message is only available to the parent session")
	}
	taskID := delegateStringArg(args, "task_id")
	body := delegateStringArg(args, "message")
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	if body == "" {
		return "", fmt.Errorf("message is required")
	}
	sessionID := strings.TrimSpace(exec.SessionID)
	if sessionID == "" {
		return "", fmt.Errorf("action=message requires the parent session")
	}
	// Status without a session tells a missing id from a foreign one. The
	// session check then refuses a task this parent did not create.
	if _, err := a.delegate.HandleStatus(map[string]any{
		"task_id":        taskID,
		"include_result": false,
	}); err != nil {
		return "", fmt.Errorf("task not found: %s", taskID)
	}
	raw, err := a.delegate.HandleStatusForSession(map[string]any{
		"task_id":        taskID,
		"include_result": false,
	}, sessionID)
	if err != nil {
		if strings.Contains(err.Error(), "current session") || strings.Contains(err.Error(), "not available") {
			return "", fmt.Errorf("task %s is not available in the current session", taskID)
		}
		return "", err
	}
	var status struct {
		Status             string `json:"status"`
		ExecutionSessionID string `json:"execution_session_id"`
	}
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return "", err
	}
	switch status.Status {
	case "pending", "running":
		if err := a.parentMessages().enqueue(taskID, sessionID, body); err != nil {
			return "", err
		}
		encoded, err := json.Marshal(map[string]any{
			"task_id":          taskID,
			"status":           status.Status,
			"queued":           true,
			"pending_messages": a.parentMessages().count(taskID),
		})
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	case "completed", "failed", "cancelled":
		return a.delegate.HandleDelegateWithOwner(map[string]any{
			"description": body,
			"resume_from": taskID,
		}, delegateruntime.Owner{SessionID: sessionID, Source: exec.Source})
	default:
		return "", fmt.Errorf("task %s cannot accept a message while %s", taskID, status.Status)
	}
}

// annotateDelegateStatus adds pending_messages to a status payload. Bodies
// stay out of status.
func (a *Agent) annotateDelegateStatus(output string) string {
	if a == nil || strings.TrimSpace(output) == "" {
		return output
	}
	box := a.parentMessages()
	if box == nil {
		return output
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return output
	}
	taskID, _ := payload["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return output
	}
	payload["pending_messages"] = box.count(taskID)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return output
	}
	return string(encoded)
}

// drainParentMessagesAfterToolRound appends queued parent notes as user
// messages. It runs only after the current tool batch has finished.
func (a *Agent) drainParentMessagesAfterToolRound(messages []provider.Message, sess *session.Session, taskID string) []provider.Message {
	if a == nil || strings.TrimSpace(taskID) == "" {
		return messages
	}
	box := a.parentMessages()
	if box == nil {
		return messages
	}
	if !box.beginDrain(taskID) {
		return messages
	}
	defer box.endDrain(taskID)
	taken := box.take(taskID)
	if len(taken) == 0 {
		return messages
	}
	var written []parentMessage
	for _, item := range taken {
		text := "Parent message:\n" + item.body
		msg := provider.Message{Role: "user", Content: text}
		messages = append(messages, msg)
		if sess != nil {
			if err := sess.AddProviderMessageWithError(msg); err != nil {
				box.restore(taskID, append([]parentMessage(nil), taken[len(written):]...))
				return messages
			}
		}
		written = append(written, item)
	}
	return messages
}
