package contextx

// A call and all its returned results form one indivisible model exchange.
type messageGroup struct {
	messages []Message
	priority MessagePriority
}

func completeMessageGroups(messages []Message) []messageGroup {
	var groups []messageGroup
	for i := 0; i < len(messages); {
		msg := messages[i]
		if msg.Role == "tool" {
			i++
			continue
		}
		g := messageGroup{messages: []Message{msg}, priority: msg.Priority}
		i++
		if len(msg.ToolCalls) > 0 {
			expected := make(map[string]bool, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				expected[call.ID] = false
			}
			for i < len(messages) && messages[i].Role == "tool" {
				result := messages[i]
				i++
				if returned, ok := expected[result.ToolCallID]; ok && !returned {
					expected[result.ToolCallID] = true
					g.messages = append(g.messages, result)
					if result.Priority < g.priority {
						g.priority = result.Priority
					}
				}
			}
			complete := true
			for id, returned := range expected {
				complete = complete && id != "" && returned
			}
			if !complete {
				continue
			}
		}
		groups = append(groups, g)
	}
	return groups
}

func (cw *ContextWindow) fitCompleteGroups(messages []Message, maxTokens int) []Message {
	groups := completeMessageGroups(messages)
	selected := make([]bool, len(groups))
	remaining := maxTokens - 4
	cost := func(g messageGroup) int {
		total := 0
		for _, msg := range g.messages {
			total += cw.estimator.EstimateMessage(msg)
		}
		return total
	}
	// Critical instructions cannot silently disappear. An overflow is visible
	// through FinalTokens, allowing callers to reject an impossible request.
	for i, g := range groups {
		if g.priority == PriorityCritical {
			selected[i] = true
			remaining -= cost(g)
		}
	}
	for _, priority := range []MessagePriority{PriorityHigh, PriorityNormal, PriorityLow} {
		for i := len(groups) - 1; i >= 0; i-- {
			if g := groups[i]; g.priority == priority && cost(g) <= remaining {
				selected[i] = true
				remaining -= cost(g)
			}
		}
	}
	var out []Message
	for i, g := range groups {
		if selected[i] {
			out = append(out, g.messages...)
		}
	}
	return out
}
