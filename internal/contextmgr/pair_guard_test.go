package contextmgr

import "testing"

func TestGuardToolPairs_NoPairs(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	result := GuardToolPairs(msgs)
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}

func TestGuardToolPairs_CompletePair(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "tc_1", Name: "calc"}}},
		{Role: "tool", Content: "42", ToolID: "tc_1", Name: "calc"},
	}
	result := GuardToolPairs(msgs)
	if len(result) != 2 {
		t.Errorf("complete pairs should be kept, got %d messages", len(result))
	}
}

func TestGuardToolPairs_OrphanedToolCall(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "tc_1", Name: "calc"}}},
		{Role: "assistant", Content: "done"},
	}
	// tc_1 has no matching tool result — should be removed
	result := GuardToolPairs(msgs)
	for _, m := range result {
		for _, tc := range m.ToolCalls {
			if tc.ID == "tc_1" {
				t.Error("orphaned tool_call tc_1 should have been removed")
			}
		}
	}
}

func TestGuardToolPairs_OrphanedToolResult(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "tool", Content: "42", ToolID: "tc_1", Name: "calc"},
		{Role: "assistant", Content: "done"},
	}
	// tc_1 result has no matching tool call — should be removed
	result := GuardToolPairs(msgs)
	for _, m := range result {
		if m.ToolID == "tc_1" {
			t.Error("orphaned tool_result tc_1 should have been removed")
		}
	}
}

func TestGuardToolPairs_SystemMessagesPreserved(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys", IsSystem: true},
		{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "tc_1", Name: "calc"}}},
	}
	result := GuardToolPairs(msgs)
	hasSystem := false
	for _, m := range result {
		if m.IsSystem {
			hasSystem = true
		}
	}
	if !hasSystem {
		t.Error("system messages should always be preserved")
	}
}

func TestGuardToolPairs_MultiplePairs(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{
			{ID: "tc_1", Name: "calc"},
			{ID: "tc_2", Name: "weather"},
		}},
		{Role: "tool", Content: "42", ToolID: "tc_1", Name: "calc"},
		{Role: "tool", Content: "sunny", ToolID: "tc_2", Name: "weather"},
	}
	result := GuardToolPairs(msgs)
	if len(result) != 3 {
		t.Errorf("expected 3 messages (1 assistant + 2 tool results), got %d", len(result))
	}
}

func TestGuardToolPairs_PartialOrphan(t *testing.T) {
	// One complete pair and one orphaned call
	msgs := []Message{
		{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{
			{ID: "tc_1", Name: "calc"},
			{ID: "tc_2", Name: "weather"},
		}},
		{Role: "tool", Content: "42", ToolID: "tc_1", Name: "calc"},
		// tc_2 result is missing
	}
	result := GuardToolPairs(msgs)
	// The assistant message with tc_1 and tc_2 should be removed since tc_2 is orphaned
	// (the whole message is either kept or removed)
	for _, m := range result {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			// If kept, all its tool calls should have matching results
			for _, tc := range m.ToolCalls {
				found := false
				for _, m2 := range result {
					if m2.ToolID == tc.ID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("assistant message with orphaned tool_call %s should have been removed", tc.ID)
				}
			}
		}
	}
}
