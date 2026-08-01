package contextmgr

import "testing"

func TestTrimByToken_BasicTrim(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msgs := []Message{
		{Role: "system", Content: "system prompt", IsSystem: true},
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you doing today"},
		{Role: "assistant", Content: "I am doing well thank you for asking"},
	}
	// Use a very small token limit to force trimming
	result := TrimByToken(msgs, 20, counter)

	// System message should always be kept
	hasSystem := false
	for _, m := range result {
		if m.IsSystem {
			hasSystem = true
		}
	}
	if !hasSystem {
		t.Error("system message should always be kept")
	}

	// Result should have fewer messages than original
	if len(result) >= len(msgs) {
		t.Errorf("expected fewer messages after token trimming, got %d >= %d", len(result), len(msgs))
	}
}

func TestTrimByToken_AllFit(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msgs := []Message{
		{Role: "system", Content: "sys", IsSystem: true},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	result := TrimByToken(msgs, 10000, counter)
	if len(result) != 3 {
		t.Errorf("expected 3 messages when all fit, got %d", len(result))
	}
}

func TestTrimByToken_OnlySystemFits(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msgs := []Message{
		{Role: "system", Content: "sys", IsSystem: true},
		{Role: "user", Content: "this is a long message that takes many tokens"},
	}
	// Very small limit — only system fits
	result := TrimByToken(msgs, 5, counter)
	if len(result) != 1 {
		t.Errorf("expected 1 message (system only), got %d", len(result))
	}
	if !result[0].IsSystem {
		t.Error("expected only system message")
	}
}

func TestTrimByToken_ToolPairProtection(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msgs := []Message{
		{Role: "system", Content: "sys", IsSystem: true},
		{Role: "user", Content: "calculate"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "tc_1", Name: "calc", Arguments: "{}"}}},
		{Role: "tool", Content: "42", ToolID: "tc_1", Name: "calc"},
		{Role: "assistant", Content: "answer is 42"},
	}
	result := TrimByToken(msgs, 30, counter)

	// Verify no orphaned tool_call/tool_result
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
			t.Errorf("orphaned tool_call %s after token trimming", id)
		}
	}
	for id := range toolResultIDs {
		if !toolCallIDs[id] {
			t.Errorf("orphaned tool_result %s after token trimming", id)
		}
	}
}
