package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestExtractMathExpr covers the expression parser, including the case that made
// the mock answer a different question than the one asked.
//
// It used to return a hardcoded "1 + 1" whenever it found nothing, and its parser
// required whitespace around the operator. So "计算1+4" produced "1 + 1 = 2": a
// plausible-looking result for an expression the user never typed. Returning ""
// instead makes the calculator report "unsupported expression", which is visible.
func TestExtractMathExpr(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// The reported failure: no spaces around the operator.
		{"计算1+4", "1 + 4"},
		{"计算 1+4", "1 + 4"},
		{"1+4等于多少", "1 + 4"},
		// The forms that already worked.
		{"计算 1 + 4", "1 + 4"},
		{"计算 23 乘以 19", "23 * 19"},
		{"计算 23 × 19", "23 * 19"},
		{"100/4", "100 / 4"},
		{"99-33 等于多少", "99 - 33"},
		{"计算 1加4", "1 + 4"},
		{"计算 10 除以 4", "10 / 4"},
		{"计算 100 / 4 + 3", "100 / 4 + 3"},
		{"帮我算算 3.5*2", "3.5 * 2"},
		// No expression: the answer must be empty, never invented.
		{"今天天气怎么样", ""},
		{"你好", ""},
		{"计算", ""},
		{"计算 删除订单后的剩余数量", ""},
		{"1 + + 2", ""},
		// An operator left over from a word must not become an expression.
		{"除了计算 3*2", "3 * 2"},
	}
	for _, c := range cases {
		if got := extractMathExpr(c.in); got != c.want {
			t.Errorf("extractMathExpr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestExtractMathExpr_NeverInventsAnExpression pins the property rather than the
// cases: an unparseable message must not yield a usable expression, because the
// calculator would then answer it as if it were what the user asked.
func TestExtractMathExpr_NeverInventsAnExpression(t *testing.T) {
	for _, msg := range []string{"计算", "帮我算一下", "算算看", "计算这个问题", "计算 未知的东西"} {
		if got := extractMathExpr(msg); got != "" {
			t.Errorf("extractMathExpr(%q) invented %q; it must return empty instead", msg, got)
		}
	}
}

// TestSummarizeToolResults_OnlyTheCurrentTurn is the guard for the second half of
// the same report: the answer repeated every tool result in the thread, so an
// earlier weather lookup reappeared in the summary of a later arithmetic question.
func TestSummarizeToolResults_OnlyTheCurrentTurn(t *testing.T) {
	messages := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("北京天气怎么样"),
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{{ID: "c1", Function: schema.FunctionCall{Name: "weather"}}}},
		{Role: schema.Tool, Name: "weather", Content: "北京 23°C", ToolCallID: "c1"},
		{Role: schema.Assistant, Content: "北京 23 度"},
		schema.UserMessage("计算1+4"),
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{{ID: "c2", Function: schema.FunctionCall{Name: "calculator"}}}},
		{Role: schema.Tool, Name: "calculator", Content: "1 + 4 = 5", ToolCallID: "c2"},
	}

	got := summarizeToolResults(messages)
	if !strings.Contains(got, "1 + 4 = 5") {
		t.Fatalf("the current turn's result is missing: %q", got)
	}
	if strings.Contains(got, "北京") {
		t.Fatalf("an earlier turn's tool result leaked into this answer: %q", got)
	}
	// The same rule must not lose everything when a run has no user message in
	// view (a sub-agent context), so an empty result is not acceptable either.
	if got == "" || strings.Contains(got, "工具执行完成") {
		t.Fatalf("the summary found no tool result at all: %q", got)
	}
}
