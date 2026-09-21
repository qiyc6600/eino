package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/contextmgr"
)

// recordingModel captures the message list of every model call, so a test can
// assert what the model actually received.
func recordingModel() (*scriptModel, func() [][]*schema.Message) {
	var mu sync.Mutex
	var calls [][]*schema.Message
	m := scripted(func(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
		mu.Lock()
		snapshot := append([]*schema.Message(nil), msgs...)
		calls = append(calls, snapshot)
		mu.Unlock()
		return schema.AssistantMessage("好的", nil), nil
	})
	return m, func() [][]*schema.Message {
		mu.Lock()
		defer mu.Unlock()
		return append([][]*schema.Message(nil), calls...)
	}
}

// TestExecution_MessageWindowCapsModelContext covers the count-based sliding
// window: it bounds how many messages reach the model while the stored history
// stays complete.
func TestExecution_MessageWindowCapsModelContext(t *testing.T) {
	m, calls := recordingModel()
	r := testRuntime(t, m, nil, nil, "")
	r.SetMessageWindow(5)

	for i := 0; i < 5; i++ {
		if result := r.Chat(testIdentity(), "thread", "继续聊"); result.Status != StatusCompleted {
			t.Fatalf("turn %d failed: %+v", i+1, result)
		}
	}

	seen := calls()
	// 5 non-system messages plus the always-kept system prompt.
	const maxExpected = 6
	for i, msgs := range seen {
		if len(msgs) > maxExpected {
			t.Fatalf("model call %d received %d messages, window allows %d", i, len(msgs), maxExpected)
		}
		if len(msgs) > 0 && msgs[0].Role != schema.System {
			t.Fatalf("model call %d lost its system prompt: %v", i, msgs[0].Role)
		}
	}
	// The window must actually have engaged on the final turn, otherwise this
	// test would pass without exercising it.
	last := seen[len(seen)-1]
	if len(last) != maxExpected {
		t.Fatalf("expected the window to cap the last call at %d messages, got %d", maxExpected, len(last))
	}
	// The stored history is unaffected: the window governs the model context only.
	if got := len(r.GetThreadMessages("u_admin", "thread")); got != 10 {
		t.Fatalf("the window must not shorten the stored history, got %d messages", got)
	}
}

// TestExecution_MessageWindowKeepsToolPairs asserts the window never leaves a
// tool result without the call that produced it.
func TestExecution_MessageWindowKeepsToolPairs(t *testing.T) {
	m, calls := recordingModel()
	r := testRuntime(t, m, nil, nil, "")
	r.SetMessageWindow(4)

	// A history whose cut point falls inside a tool interaction.
	seed := []*schema.Message{
		schema.UserMessage("u1"),
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "c1", Function: schema.FunctionCall{Name: "calculator", Arguments: "{}"}},
		}),
		&schema.Message{Role: schema.Tool, Content: "1+1=2", ToolCallID: "c1", Name: "calculator"},
		schema.AssistantMessage("a1", nil),
		schema.UserMessage("u2"),
		schema.AssistantMessage("a2", nil),
	}
	r.threads.Replace("u_admin", "thread", seed)

	if result := r.Chat(testIdentity(), "thread", "再来"); result.Status != StatusCompleted {
		t.Fatalf("run failed: %+v", result)
	}

	for _, msgs := range calls() {
		for _, msg := range msgs {
			if msg.Role != schema.Tool {
				continue
			}
			found := false
			for _, candidate := range msgs {
				for _, tc := range candidate.ToolCalls {
					if tc.ID == msg.ToolCallID {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("the window left a tool result without its call: %+v", msgs)
			}
		}
	}
}

// TestExecution_NoMessageWindowKeepsFullContext is the default-behavior guard:
// without the window configured the model still sees the whole history.
func TestExecution_NoMessageWindowKeepsFullContext(t *testing.T) {
	m, calls := recordingModel()
	r := testRuntime(t, m, nil, nil, "")

	for i := 0; i < 4; i++ {
		if result := r.Chat(testIdentity(), "thread", "继续聊"); result.Status != StatusCompleted {
			t.Fatalf("turn %d failed: %+v", i+1, result)
		}
	}

	seen := calls()
	last := seen[len(seen)-1]
	// Turn 4: three previous turns of history (6) plus the system prompt and
	// the new user message.
	if len(last) != 8 {
		t.Fatalf("default configuration must not trim the context, got %d messages", len(last))
	}
}

// TestTrimByCountWindowDocumentsSummarizationTradeoff records the documented
// interaction: a window small enough to keep the conversation under the token
// threshold means summarization never triggers.
func TestTrimByCountWindowDocumentsSummarizationTradeoff(t *testing.T) {
	// This asserts the window is applied before the token logic: with a tiny
	// window the summarizer only ever sees the windowed list.
	m, calls := recordingModel()
	r := testRuntime(t, m, nil, nil, "")
	r.summarizer = contextmgr.NewSummarizer(contextmgr.NewSimpleTokenCounter(), 0.5, 20, m)
	r.SetMessageWindow(3)

	for i := 0; i < 4; i++ {
		if result := r.Chat(testIdentity(), "thread", "继续"); result.Status != StatusCompleted {
			t.Fatalf("turn %d failed: %+v", i+1, result)
		}
	}
	for _, msgs := range calls() {
		if len(msgs) > 4 { // 3 windowed + system
			t.Fatalf("window not applied before the token logic: %d messages", len(msgs))
		}
	}
	// With the context held this small, no compression event can be produced.
	if strings.Contains(historyText(r.GetThreadMessages("u_admin", "thread")), "历史摘要") {
		t.Fatal("a windowed context should not reach the summarization threshold")
	}
}
