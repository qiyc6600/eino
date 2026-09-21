package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestMockModel_ReadsInjectedLanguageExactly covers the mock's read side: it
// answers using the language the framework injected into the system prompt.
//
// The mock used to scan the prompt for language names, checking "Java" before
// "JavaScript", so a user whose stored preference was JavaScript was told the
// mock would use Java — the mock contradicting the memory it was reading. The
// value is now parsed out of the injected line instead of guessed.
func TestMockModel_ReadsInjectedLanguageExactly(t *testing.T) {
	// The exact shape memory.formatEntryLine produces inside the prompt.
	prompt := func(lang string) []*schema.Message {
		return []*schema.Message{
			schema.SystemMessage("你是通用业务助手。\n\n用户记忆（确定性）：\n- preferred_language: " + lang),
			schema.UserMessage("帮我写一个脚本"),
		}
	}

	for _, lang := range []string{"JavaScript", "Java", "Go", "TypeScript", "C++"} {
		resp, err := NewMockChatModel().Generate(context.Background(), prompt(lang))
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if !strings.Contains(resp.Content, lang) {
			t.Fatalf("injected %q but the mock answered %q — it must report the stored preference, not a guess", lang, resp.Content)
		}
	}
}

// TestMockModel_NoInjectedMemoryFallsBack asserts an absent memory line leaves
// the mock's default rather than an empty language.
func TestMockModel_NoInjectedMemoryFallsBack(t *testing.T) {
	resp, err := NewMockChatModel().Generate(context.Background(), []*schema.Message{
		schema.SystemMessage("你是通用业务助手。"),
		schema.UserMessage("帮我写一个脚本"),
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(resp.Content, "Python") {
		t.Fatalf("with no memory the mock should use its default, got %q", resp.Content)
	}
}

// TestDetectLanguagePreference_WholeWords pins the matching rule directly: short
// ambiguous names must not win inside longer ones.
func TestDetectLanguagePreference_WholeWords(t *testing.T) {
	cases := map[string]string{
		"我喜欢用javascript": "JavaScript",
		"我喜欢用JavaScript": "JavaScript",
		"我喜欢用java":       "Java",
		"我喜欢用typescript": "TypeScript",
		"我喜欢用golang":     "Go",
		"我喜欢用go":         "Go",
		"我用django写后端":    "", // "go" inside "django" is not a language mention
		"我在写文档":          "",
	}
	for in, want := range cases {
		if got := detectLanguagePreference(strings.ToLower(in)); got != want {
			t.Errorf("detectLanguagePreference(%q) = %q, want %q", in, got, want)
		}
	}
}
