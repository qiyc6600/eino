package tools

// Display labels for the built-in tools.
//
// The internal names are load-bearing: they are the routing keys the supervisor
// picks from, the ACL subjects roles are granted, the idempotency-key component
// and the field carried in run events and approval records. None of that may be
// renamed for cosmetic reasons. The label is the separate, display-only name —
// what the interface shows a person.
//
// One table, one lookup, so a tool cannot end up labelled differently in the
// chat progress line, the approval card and the tool panel. Tools with no entry
// here (external MCP tools, whose names come from a third party) fall back to
// their own name: inventing a Chinese label for someone else's tool would be a
// guess, and the name is what the operator configured.
var displayLabels = map[string]string{
	"calculator":   "数学计算",
	"weather":      "天气查询",
	"grep":         "日志搜索",
	"query_order":  "订单查询",
	"delete_order": "删除订单",
	"send_email":   "邮件发送",
}

// DisplayLabel returns the human-readable label for a tool, or the internal name
// when the tool declares none.
func DisplayLabel(toolName string) string {
	if label, ok := displayLabels[toolName]; ok {
		return label
	}
	return toolName
}

// HasDisplayLabel reports whether a tool name has a declared label, i.e. whether
// it is a built-in whose label is maintained here.
func HasDisplayLabel(toolName string) bool {
	_, ok := displayLabels[toolName]
	return ok
}
