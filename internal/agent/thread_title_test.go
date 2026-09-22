package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/tools"
)

func TestDeriveThreadTitle(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    string
	}{
		{"short message is used as-is", "计算 1+4", "计算 1+4"},
		{"chinese question", "查询北京天气", "查询北京天气"},
		{"leading and trailing space", "   查询北京天气  ", "查询北京天气"},
		{"newlines collapse", "第一行\n第二行", "第一行 第二行"},
		{"tabs and runs of space", "计算\t\t1 + 4", "计算 1 + 4"},
		{"first sentence wins", "帮我看看这个报错。日志在下面，很长很长", "帮我看看这个报错"},
		{"english question mark", "why is this failing? the log is below", "why is this failing"},
		{"a short first sentence is not a title", "好的。帮我查一下订单状态", "好的。帮我查一下订单状态"},
		{"empty", "", ""},
		{"whitespace only", "   \n  ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveThreadTitle(tc.message); got != tc.want {
				t.Errorf("DeriveThreadTitle(%q) = %q, want %q", tc.message, got, tc.want)
			}
		})
	}
}

// A long message is cut to a label, not kept whole: the sidebar has room for a
// label, and an unbounded title would make the list unreadable.
func TestDeriveThreadTitle_Truncates(t *testing.T) {
	got := DeriveThreadTitle(strings.Repeat("很长的消息", 20))

	if w := tools.DisplayWidth(got); w > maxThreadTitleWidth {
		t.Errorf("title is %d columns wide, want at most %d: %q", w, maxThreadTitleWidth, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated title should say so: %q", got)
	}
}

// Width, not character count: a Han character takes two columns, so a Chinese
// title gets about half as many characters as a Latin one for the same room.
// Counting runes gave Chinese titles twice the visual length of English ones.
func TestDeriveThreadTitle_CountsDisplayColumns(t *testing.T) {
	chinese := DeriveThreadTitle(strings.Repeat("中", 40))
	english := DeriveThreadTitle(strings.Repeat("ab", 40))

	cnRunes := len([]rune(chinese))
	enRunes := len([]rune(english))
	if cnRunes >= enRunes {
		t.Errorf("Chinese title kept %d characters vs %d for English; the cap is not measuring width",
			cnRunes, enRunes)
	}
	if w := tools.DisplayWidth(english); w > maxThreadTitleWidth {
		t.Errorf("English title is %d columns wide, want at most %d", w, maxThreadTitleWidth)
	}
}

// The cut counts characters, not bytes: cutting a Chinese title by bytes would
// split a character and produce mojibake.
func TestDeriveThreadTitle_CutsOnCharacterBoundaries(t *testing.T) {
	got := DeriveThreadTitle(strings.Repeat("中", 40))
	for _, r := range got {
		if r == '\uFFFD' {
			t.Fatalf("truncation produced an invalid rune: %q", got)
		}
	}
	if strings.Contains(got, "\ufffd") {
		t.Fatalf("truncation split a character: %q", got)
	}
}

// The title comes from the first thing the user said, which is what the
// conversation is about — not from whatever message happened to arrive later.
func TestTitleFromFirstUserMessage(t *testing.T) {
	messages := []*schema.Message{
		{Role: schema.System, Content: "你是一个智能助手"},
		{Role: schema.User, Content: "查询北京天气"},
		{Role: schema.Assistant, Content: "北京 26°C"},
		{Role: schema.User, Content: "那上海呢"},
	}
	if got := TitleFromFirstUserMessage(messages); got != "查询北京天气" {
		t.Errorf("got %q, want the first user message", got)
	}
}

// A thread whose first message produced no title (an empty message) falls
// through to the next one rather than leaving the thread nameless.
func TestTitleFromFirstUserMessage_SkipsEmpty(t *testing.T) {
	messages := []*schema.Message{
		{Role: schema.User, Content: "   "},
		{Role: schema.User, Content: "查我的订单"},
	}
	if got := TitleFromFirstUserMessage(messages); got != "查我的订单" {
		t.Errorf("got %q", got)
	}
}

func TestTitleFromFirstUserMessage_NoUserMessages(t *testing.T) {
	if got := TitleFromFirstUserMessage([]*schema.Message{{Role: schema.Assistant, Content: "你好"}}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
