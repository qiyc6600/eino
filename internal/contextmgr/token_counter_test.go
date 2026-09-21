package contextmgr

import "testing"

func TestSimpleTokenCounter_English(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msg := Message{Role: "user", Content: "Hello world, this is a test."}
	count := counter.CountMessage(msg)
	if count <= 0 {
		t.Error("expected positive token count for English text")
	}
}

func TestSimpleTokenCounter_Chinese(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msg := Message{Role: "user", Content: "你好世界，这是一个测试。"}
	count := counter.CountMessage(msg)
	if count <= 0 {
		t.Error("expected positive token count for Chinese text")
	}
}

func TestSimpleTokenCounter_RoleOverhead(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msg := Message{Role: "user", Content: "test"}
	count := counter.CountMessage(msg)
	// Should include at least role overhead (4) + content tokens
	if count < 4 {
		t.Errorf("expected at least role overhead (4), got %d", count)
	}
}

func TestSimpleTokenCounter_MultipleMessages(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	total := counter.CountMessages(msgs)
	individual := counter.CountMessage(msgs[0]) + counter.CountMessage(msgs[1])
	if total != individual {
		t.Errorf("total (%d) should equal sum of individuals (%d)", total, individual)
	}
}

func TestSimpleTokenCounter_EmptyContent(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msg := Message{Role: "user", Content: ""}
	count := counter.CountMessage(msg)
	// Should still have role overhead
	if count < 4 {
		t.Errorf("expected at least role overhead for empty content, got %d", count)
	}
}

func TestSimpleTokenCounter_MixedContent(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msg := Message{Role: "user", Content: "Hello 你好 world 世界"}
	count := counter.CountMessage(msg)
	if count <= 0 {
		t.Error("expected positive token count for mixed content")
	}
}

// Tool call arguments are part of every request that carries a tool call, and
// are usually the largest part of it.
func TestSimpleTokenCounter_CountsToolCallArguments(t *testing.T) {
	counter := NewSimpleTokenCounter()
	plain := Message{Role: "assistant", Content: "ok"}
	withCall := Message{
		Role:    "assistant",
		Content: "ok",
		ToolCalls: []ToolCallRef{{
			ID:        "call_1",
			Name:      "query_order",
			Arguments: `{"order_id":"A-1001","include_history":true,"note":"请一并返回最近三个月的物流轨迹"}`,
		}},
	}
	if counter.CountMessage(withCall) <= counter.CountMessage(plain) {
		t.Fatalf("tool call arguments must add to the count: with=%d plain=%d",
			counter.CountMessage(withCall), counter.CountMessage(plain))
	}
}

// A tool result message carries the tool name alongside its payload.
func TestSimpleTokenCounter_CountsToolName(t *testing.T) {
	counter := NewSimpleTokenCounter()
	unnamed := Message{Role: "tool", Content: "done"}
	named := Message{Role: "tool", Content: "done", Name: "query_order"}
	if counter.CountMessage(named) <= counter.CountMessage(unnamed) {
		t.Fatalf("tool name must add to the count: named=%d unnamed=%d",
			counter.CountMessage(named), counter.CountMessage(unnamed))
	}
}

func TestCountText(t *testing.T) {
	count := CountText("Hello world")
	if count <= 0 {
		t.Error("expected positive token count")
	}
}

func TestCountString(t *testing.T) {
	count := CountString("Hello world")
	if count <= 0 {
		t.Error("expected positive token count")
	}
}

func TestIsChinese(t *testing.T) {
	if !IsChinese("你好") {
		t.Error("expected IsChinese=true for Chinese text")
	}
	if IsChinese("hello") {
		t.Error("expected IsChinese=false for English text")
	}
}
