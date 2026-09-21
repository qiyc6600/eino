package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/example/agent-eino-demo/internal/memory"
)

// TestMockModel_DoesNotClaimMemoryExtraction pins the deliberate mock-mode
// behaviour: the mock answers a memory-extraction prompt as ordinary chat, so
// memory.Service cannot parse it and extraction falls back to its rule matcher.
//
// This is worth a test because the obvious "fix" is the wrong one. The mock used
// to carry a hand-written extraction matcher behind a marker string that never
// matched the service's actual prompt, which made it dead code — but had the
// marker been aligned, that matcher would have started writing wrong memories:
// a bare "java" match would turn JavaScript into Java, and a bare "go" match
// would turn Django into Go. A mock that guesses at extraction silently
// disagrees with the real model's judgement, so it must not guess at all.
func TestMockModel_DoesNotClaimMemoryExtraction(t *testing.T) {
	// Exactly the prompt memory.Service.extractWithLLM sends.
	prompt := []*schema.Message{
		schema.SystemMessage("你是一个用户记忆提取助手，只返回 JSON 数组，不返回其他内容。"),
		schema.UserMessage("你是一个用户记忆提取助手。从用户消息中提取值得长期记住的信息，以 JSON 数组格式返回。\n\n用户消息：我喜欢用JavaScript"),
	}

	resp, err := NewMockChatModel().Generate(context.Background(), prompt)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(resp.Content, "[") {
		t.Fatalf("the mock must not answer an extraction prompt with a JSON array — "+
			"memory extraction would then take the LLM path with a guessed value. got %q", resp.Content)
	}
}

// TestMemoryExtraction_MockModeUsesRules asserts the observable consequence:
// with the mock as the chat model, preferences are still written, and they are
// attributed to the rule matcher rather than to the model.
func TestMemoryExtraction_MockModeUsesRules(t *testing.T) {
	store := memory.NewInMemoryMemoryStore()
	svc := memory.NewService(store, nil, nil, NewMockChatModel())

	ctx := context.Background()
	if err := svc.ExtractAndSave(ctx, "u_admin", "t1", "我喜欢用JavaScript，请简洁回答"); err != nil {
		t.Fatalf("extract: %v", err)
	}

	entries, err := svc.ListPreferences(ctx, "u_admin")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("mock mode must still extract preferences, via the rule matcher")
	}

	byKey := make(map[string]memory.MemoryEntry, len(entries))
	for _, e := range entries {
		byKey[e.Key] = e
	}

	lang, ok := byKey["preferred_language"]
	if !ok {
		t.Fatalf("preferred_language not extracted; got %v", byKey)
	}
	// The rule matcher checks "javascript" before the bare "java" case, so it
	// resolves this correctly. A naive matcher would answer Java here.
	if lang.Value != "JavaScript" {
		t.Fatalf("preferred_language = %q, want JavaScript", lang.Value)
	}
	if lang.Source != "user_stated" {
		t.Fatalf("source = %q, want user_stated (rule path). "+
			"llm_extracted means the mock took the model path and guessed", lang.Source)
	}

	if style, ok := byKey["answer_style"]; !ok || style.Value != "concise" {
		t.Fatalf("answer_style not extracted as concise; got %+v", style)
	}
}
