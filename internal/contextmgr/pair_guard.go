package contextmgr

// GuardToolPairs ensures that tool_call and tool_result messages are never split apart.
// If an assistant message with tool_calls is kept but the corresponding tool result is
// trimmed (or vice versa), the entire group is either kept or removed together.
func GuardToolPairs(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}

	// Build a set of tool_call IDs present in the messages
	toolCallIDs := make(map[string]bool)
	toolResultIDs := make(map[string]bool)

	for _, m := range messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				toolCallIDs[tc.ID] = true
			}
		}
		if m.Role == "tool" && m.ToolID != "" {
			toolResultIDs[m.ToolID] = true
		}
	}

	// Check for unmatched pairs
	hasUnmatched := false
	for id := range toolCallIDs {
		if !toolResultIDs[id] {
			hasUnmatched = true
			break
		}
	}
	if !hasUnmatched {
		for id := range toolResultIDs {
			if !toolCallIDs[id] {
				hasUnmatched = true
				break
			}
		}
	}

	if !hasUnmatched {
		return messages
	}

	// Remove messages that belong to incomplete tool call/result pairs
	// First, find the complete groups
	groups := buildToolGroups(messages)

	var result []Message
	for _, m := range messages {
		if m.Role == "system" || m.IsSystem {
			result = append(result, m)
			continue
		}

		// Check if this message belongs to a complete group
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			if isGroupComplete(m.ToolCalls, toolResultIDs) {
				result = append(result, m)
			}
			continue
		}

		if m.Role == "tool" && m.ToolID != "" {
			if toolCallIDs[m.ToolID] {
				result = append(result, m)
			}
			continue
		}

		// Non-tool messages are always kept
		result = append(result, m)
	}

	_ = groups // suppress unused warning
	return result
}

// toolGroup represents a group of related messages (assistant tool_call + tool results).
type toolGroup struct {
	assistantIdx int
	toolIDs      []string
}

func buildToolGroups(messages []Message) []toolGroup {
	var groups []toolGroup
	for i, m := range messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			g := toolGroup{assistantIdx: i}
			for _, tc := range m.ToolCalls {
				g.toolIDs = append(g.toolIDs, tc.ID)
			}
			groups = append(groups, g)
		}
	}
	return groups
}

func isGroupComplete(toolCalls []ToolCallRef, resultIDs map[string]bool) bool {
	for _, tc := range toolCalls {
		if !resultIDs[tc.ID] {
			return false
		}
	}
	return true
}
