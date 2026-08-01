package contextmgr

import "testing"

func TestSummarizer_ShouldSummarize(t *testing.T) {
	counter := NewSimpleTokenCounter()
	summarizer := NewSummarizer(counter, 0.8, 800, nil) // nil chatModel = rule-based

	// Create messages that exceed 80% of 1000 tokens
	var msgs []Message
	for i := 0; i < 50; i++ {
		msgs = append(msgs, Message{Role: "user", Content: "这是一段测试消息用于验证摘要功能是否正常工作"})
		msgs = append(msgs, Message{Role: "assistant", Content: "收到，这是助手的回复内容"})
	}

	if !summarizer.ShouldSummarize(msgs, 1000) {
		t.Error("expected shouldSummarize=true for large message set")
	}
}

func TestSummarizer_ShouldNotSummarize(t *testing.T) {
	counter := NewSimpleTokenCounter()
	summarizer := NewSummarizer(counter, 0.8, 800, nil)

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	if summarizer.ShouldSummarize(msgs, 1000) {
		t.Error("expected shouldSummarize=false for small message set")
	}
}

func TestSummarizer_CompressWithRules(t *testing.T) {
	counter := NewSimpleTokenCounter()
	summarizer := NewSummarizer(counter, 0.8, 200, nil) // small target for quick compression

	var msgs []Message
	msgs = append(msgs, Message{Role: "system", Content: "system prompt", IsSystem: true})
	for i := 0; i < 30; i++ {
		msgs = append(msgs, Message{Role: "user", Content: "测试消息内容"})
		msgs = append(msgs, Message{Role: "assistant", Content: "助手回复内容"})
	}

	result := summarizer.Compress(nil, msgs, 500)

	// System message should be preserved
	hasSystem := false
	hasSummary := false
	for _, m := range result {
		if m.IsSystem {
			hasSystem = true
		}
		if m.IsSummary {
			hasSummary = true
		}
	}
	if !hasSystem {
		t.Error("system message should be preserved after compression")
	}
	if !hasSummary {
		t.Error("summary message should be created after compression")
	}

	// Compressed messages should be fewer than original
	if len(result) >= len(msgs) {
		t.Errorf("expected fewer messages after compression, got %d >= %d", len(result), len(msgs))
	}
}

func TestSummarizer_CompressNotNeeded(t *testing.T) {
	counter := NewSimpleTokenCounter()
	summarizer := NewSummarizer(counter, 0.8, 800, nil)

	msgs := []Message{
		{Role: "system", Content: "sys", IsSystem: true},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}

	result := summarizer.Compress(nil, msgs, 10000)
	if len(result) != 3 {
		t.Errorf("expected 3 messages when no compression needed, got %d", len(result))
	}
}

func TestSummarizer_RuleBasedFallback(t *testing.T) {
	counter := NewSimpleTokenCounter()
	summarizer := NewSummarizer(counter, 0.8, 800, nil) // nil = rule-based

	msgs := []Message{
		{Role: "user", Content: "计算 2+3"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCallRef{{ID: "tc_1", Name: "calculator", Arguments: `{"expression":"2+3"}`}}},
		{Role: "tool", Content: "5", ToolID: "tc_1", Name: "calculator"},
		{Role: "assistant", Content: "2+3=5"},
	}

	summary := summarizer.SummarizeOldMessages(nil, msgs)
	if summary == "" {
		t.Error("expected non-empty summary from rule-based fallback")
	}
	if len(summary) < 4 {
		t.Errorf("summary seems too short: %s", summary)
	}
}

func TestSummarizer_SummaryMessageFormat(t *testing.T) {
	counter := NewSimpleTokenCounter()
	summarizer := NewSummarizer(counter, 0.8, 200, nil)

	var msgs []Message
	msgs = append(msgs, Message{Role: "system", Content: "system prompt", IsSystem: true})
	for i := 0; i < 20; i++ {
		msgs = append(msgs, Message{Role: "user", Content: "测试消息"})
		msgs = append(msgs, Message{Role: "assistant", Content: "回复内容"})
	}

	result := summarizer.Compress(nil, msgs, 300)

	// Find summary message and verify its format
	for _, m := range result {
		if m.IsSummary {
			if m.Role != "assistant" {
				t.Errorf("summary message should have role=assistant, got %s", m.Role)
			}
			if m.Metadata == nil || m.Metadata["summary"] != true {
				t.Error("summary message should have metadata.summary=true")
			}
			return
		}
	}
	t.Error("no summary message found in compressed result")
}
