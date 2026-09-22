package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func toolMsg(name, content string) *schema.Message {
	return &schema.Message{Role: schema.Tool, Name: name, Content: content}
}

func userMsg(content string) *schema.Message {
	return &schema.Message{Role: schema.User, Content: content}
}

// TestSummarizeToolResults_NeverLeaksInternalNames is the guard for the reported
// problem: answers used to read "根据工具执行结果——math_agent：根据工具执行结果——
// calculator：unsupported expression". Every built-in name is checked, plus the
// sub-agents, because the name is a routing key and has no business in prose.
func TestSummarizeToolResults_NeverLeaksInternalNames(t *testing.T) {
	internalNames := []string{
		"calculator", "weather", "grep", "query_order", "delete_order", "send_email",
		"math_agent", "search_agent", "general_agent",
		"plan_review",
	}

	conversations := [][]*schema.Message{
		{userMsg("1+4"), toolMsg("calculator", "1 + 4 = 5")},
		{userMsg("北京天气"), toolMsg("weather", "🌍 Beijing，China\n🌡️ 温度：26°C")},
		{userMsg("我的订单"), toolMsg("query_order", "订单 A-1001：已发货")},
		{
			userMsg("查天气和订单"),
			toolMsg("weather", "🌍 Beijing，China"),
			toolMsg("query_order", "订单 A-1001：已发货"),
		},
		// A sub-agent's answer arrives as the content of the outer tool call.
		{userMsg("1+4"), toolMsg("math_agent", "1 + 4 = 5")},
	}

	for _, messages := range conversations {
		got := summarizeToolResults(messages)
		for _, name := range internalNames {
			if strings.Contains(got, name) {
				t.Errorf("answer exposes the internal name %q:\n%s", name, got)
			}
		}
		if strings.Contains(got, "根据工具执行结果") {
			t.Errorf("answer still carries the old wrapper:\n%s", got)
		}
	}
}

// The nesting the user saw came from the wrapper being applied once per agent
// layer. A sub-agent's answer is a tool result at the outer layer, so it must
// pass through unchanged rather than being wrapped again.
func TestSummarizeToolResults_CollapsesNesting(t *testing.T) {
	// The exact shape produced before the fix: an inner layer's summary handed to
	// the outer layer as a sub-agent's answer.
	nested := toolMsg("math_agent", "根据工具执行结果——calculator：1 + 4 = 5")
	got := summarizeToolResults([]*schema.Message{userMsg("1+4"), nested})

	if got != "1 + 4 = 5" {
		t.Errorf("nested summary not collapsed, got:\n%s", got)
	}
}

// A single result is the tool's own user-facing text. Passing it through is what
// makes the nesting collapse, and it reads like an answer rather than a log line.
func TestSummarizeToolResults_SingleResultIsReturnedAsIs(t *testing.T) {
	got := summarizeToolResults([]*schema.Message{userMsg("1+4"), toolMsg("calculator", "1 + 4 = 5")})
	if got != "1 + 4 = 5" {
		t.Errorf("got %q, want the tool's own text unchanged", got)
	}
}

// Several results are labelled so the reader can tell them apart — with the
// Chinese labels, not the internal names.
func TestSummarizeToolResults_MultipleResultsUseDisplayLabels(t *testing.T) {
	got := summarizeToolResults([]*schema.Message{
		userMsg("查天气和订单"),
		toolMsg("weather", "🌍 Beijing，China"),
		toolMsg("query_order", "订单 A-1001：已发货"),
	})

	if !strings.Contains(got, "天气查询：") {
		t.Errorf("weather result is not labelled with its display name:\n%s", got)
	}
	if !strings.Contains(got, "订单查询：") {
		t.Errorf("order result is not labelled with its display name:\n%s", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("expected one line per result, got:\n%s", got)
	}
}

// Tool errors are English and log-shaped. Quoting them verbatim put fragments
// like "unsupported expression:" into Chinese answers.
func TestSummarizeToolResults_TranslatesKnownToolFailures(t *testing.T) {
	cases := []struct{ content, want string }{
		{"unsupported expression: ", "抱歉，这个表达式我算不出来。"},
		{"expression is empty", "抱歉，没有可计算的表达式。"},
		{"division by zero", "抱歉，除数不能为零。"},
		{"order A-1001 not found for current user", "抱歉，没有找到对应的订单。"},
		{"weather lookup failed for Beijing: dial tcp: connection refused", "抱歉，天气服务暂时不可用。"},
	}
	for _, tc := range cases {
		got := summarizeToolResults([]*schema.Message{userMsg("..."), toolMsg("calculator", tc.content)})
		if got != tc.want {
			t.Errorf("for %q got %q, want %q", tc.content, got, tc.want)
		}
		if strings.Contains(got, "expression") || strings.Contains(got, "not found") {
			t.Errorf("English fragment survived translation: %q", got)
		}
	}
}

// An unrecognised result must pass through untouched: the mock translates the
// shapes it knows and never guesses at the rest.
func TestSummarizeToolResults_UnknownTextPassesThrough(t *testing.T) {
	const content = "订单 A-1001：已发货，预计明天送达"
	got := summarizeToolResults([]*schema.Message{userMsg("..."), toolMsg("query_order", content)})
	if got != content {
		t.Errorf("unknown content was rewritten: got %q, want %q", got, content)
	}
}

// Only this turn's results. The message list carries the whole thread, so a
// weather lookup from an earlier turn used to reappear in the answer to a later
// arithmetic question.
func TestSummarizeToolResults_IgnoresEarlierTurns(t *testing.T) {
	messages := []*schema.Message{
		userMsg("北京天气"),
		toolMsg("weather", "🌍 Beijing，China"),
		{Role: schema.Assistant, Content: "北京 26°C"},
		userMsg("1+4"),
		toolMsg("calculator", "1 + 4 = 5"),
	}
	got := summarizeToolResults(messages)
	if strings.Contains(got, "Beijing") {
		t.Errorf("an earlier turn's result leaked into this answer:\n%s", got)
	}
	if got != "1 + 4 = 5" {
		t.Errorf("got %q", got)
	}
}

func TestSummarizeToolResults_NoResultsAtAll(t *testing.T) {
	if got := summarizeToolResults([]*schema.Message{userMsg("你好")}); got == "" {
		t.Error("empty narration")
	}
}
