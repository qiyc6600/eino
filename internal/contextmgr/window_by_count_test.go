package contextmgr

import "testing"

func TestTrimByCount_NoTrimNeeded(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys", IsSystem: true},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	result := TrimByCount(msgs, 10)
	if len(result) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result))
	}
}

func TestTrimByCount_SystemAlwaysKept(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys", IsSystem: true},
		{Role: "user", Content: "m1"},
		{Role: "assistant", Content: "m2"},
		{Role: "user", Content: "m3"},
		{Role: "assistant", Content: "m4"},
		{Role: "user", Content: "m5"},
	}
	result := TrimByCount(msgs, 3) // keep 3 non-system + 1 system
	hasSystem := false
	for _, m := range result {
		if m.IsSystem {
			hasSystem = true
		}
	}
	if !hasSystem {
		t.Error("system message should always be kept")
	}
}

func TestTrimByCount_KeepsRecent(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys", IsSystem: true},
		{Role: "user", Content: "old1"},
		{Role: "assistant", Content: "old2"},
		{Role: "user", Content: "recent1"},
		{Role: "assistant", Content: "recent2"},
	}
	result := TrimByCount(msgs, 2)
	// Last non-system message should be "recent2"
	lastNonSystem := ""
	for _, m := range result {
		if !m.IsSystem {
			lastNonSystem = m.Content
		}
	}
	if lastNonSystem != "recent2" {
		t.Errorf("expected last message to be recent2, got %s", lastNonSystem)
	}
}

func TestTrimByCount_ToolPairProtection(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys", IsSystem: true},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "tc_1", Name: "calc", Arguments: "{}"}}},
		{Role: "tool", Content: "42", ToolID: "tc_1", Name: "calc"},
		{Role: "assistant", Content: "The answer is 42"},
		{Role: "user", Content: "next question"},
	}
	// Trim to 2 non-system messages — should not split tool_call/tool_result pair
	result := TrimByCount(msgs, 2)
	// Verify no orphaned tool_call or tool_result
	toolCallIDs := map[string]bool{}
	toolResultIDs := map[string]bool{}
	for _, m := range result {
		for _, tc := range m.ToolCalls {
			toolCallIDs[tc.ID] = true
		}
		if m.ToolID != "" {
			toolResultIDs[m.ToolID] = true
		}
	}
	for id := range toolCallIDs {
		if !toolResultIDs[id] {
			t.Errorf("orphaned tool_call %s found after trimming", id)
		}
	}
	for id := range toolResultIDs {
		if !toolCallIDs[id] {
			t.Errorf("orphaned tool_result %s found after trimming", id)
		}
	}
}
