package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// subAgentTools is the tool list the supervisor binds, which is what the mock
// routes against in the real app.
func subAgentTools() []*schema.ToolInfo {
	return []*schema.ToolInfo{
		{Name: "math_agent"}, {Name: "search_agent"}, {Name: "general_agent"}, {Name: "mcp_agent"},
	}
}

// TestMatchToolCall_DeleteRequestDoesNotReachTheCalculator is the regression guard
// for the reported misroute.
//
// "除" — one of the math keywords — is a substring of "删除", so "删除a1008", a
// delete request, matched the math agent and invoked the calculator with no
// expression. Every other keyword in the tables is multi-character and specific;
// only the four single-character operator words were ambiguous, and they are gone
// in favour of the parsed expression.
func TestMatchToolCall_DeleteRequestDoesNotReachTheCalculator(t *testing.T) {
	for _, msg := range []string{
		"删除a1008",
		"删除 A-1008",
		"删除订单A-1008",
		"删掉订单 A-1001",
		"取消订单B-1002",
	} {
		call := matchToolCall(strings.ToLower(msg), msg, subAgentTools())
		if call == nil {
			t.Errorf("%q routed to no agent at all", msg)
			continue
		}
		if call.Function.Name != "general_agent" {
			t.Errorf("%q routed to %q, want general_agent", msg, call.Function.Name)
		}
	}
}

// TestMatchToolCall_MathStillRoutes covers the other side: removing the operator
// words must not cost the math route its coverage.
func TestMatchToolCall_MathStillRoutes(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"计算1+4", "math_agent"},
		{"计算 1 + 4", "math_agent"},
		{"1+4等于多少", "math_agent"},
		{"3乘5", "math_agent"}, // the expression decides, not the word 乘
		{"10除以2是多少", "math_agent"},
		{"23 × 19", "math_agent"},
		// Other routes must keep working.
		{"北京天气怎么样", "search_agent"},
		{"查询我的订单", "general_agent"},
		{"读取笔记 n1", "mcp_agent"},
	}
	for _, c := range cases {
		call := matchToolCall(strings.ToLower(c.msg), c.msg, subAgentTools())
		if call == nil {
			t.Errorf("%q routed to no agent", c.msg)
			continue
		}
		if call.Function.Name != c.want {
			t.Errorf("%q routed to %q, want %q", c.msg, call.Function.Name, c.want)
		}
	}
}

// TestMatchToolCall_UnrelatedTextRoutesNowhere pins that the fix did not simply
// widen every route: text with no tool signal must still reach no tool.
//
// The first group is the discriminating one. Each sentence contains a character
// that used to be a math keyword but no arithmetic — 除 in 排除, 加 in 增加, 减 in
// 减少, 乘 in 乘客 — so with those single-character words in the table each routed
// to the math agent. That is the same defect as "删除a1008", just without a
// competing keyword long enough to outrank it, which is why this group is what
// actually holds the operator words out of the table.
func TestMatchToolCall_UnrelatedTextRoutesNowhere(t *testing.T) {
	ambiguous := []string{
		"排除这个选项",
		"增加一点库存",
		"减少一些错误",
		"乘客信息在哪里",
	}
	for _, msg := range ambiguous {
		call := matchToolCall(strings.ToLower(msg), msg, subAgentTools())
		if call == nil {
			continue
		}
		t.Errorf("%q matched %q; it contains an operator character but no arithmetic, "+
			"so no tool should claim it", msg, call.Function.Name)
	}

	for _, msg := range []string{"你好", "介绍一下你能做什么", "今天过得怎么样"} {
		if call := matchToolCall(strings.ToLower(msg), msg, subAgentTools()); call != nil {
			t.Errorf("%q matched %q; unrelated text must reach no tool", msg, call.Function.Name)
		}
	}
}

// TestExtractOrderID covers the id parser, which had to accept the forms people
// actually type: the previous version required a whitespace-separated field that
// was exactly "A-1001", so an id glued to the verb ("删除a1008") was invisible.
func TestExtractOrderID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"删除a1008", "A-1008"},
		{"删除订单A-1008", "A-1008"},
		{"删除订单a1008", "A-1008"},
		{"删除 A-1008", "A-1008"},
		{"a1008", "A-1008"},
		{"a-1008", "A-1008"},
		{"A1008", "A-1008"},
		{"B-2", "B-2"},
		{"删除订单B1002 后通知我", "B-1002"},
		{"查询订单", ""},
		{"你好", ""},
		// A stray letter with no digits is not an id.
		{"版本 A 发布", ""},
	}
	for _, c := range cases {
		if got := extractOrderID(c.in); got != c.want {
			t.Errorf("extractOrderID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
