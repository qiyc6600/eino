package agent

import "github.com/example/agent-eino-demo/internal/tools"

// SubAgentSpec is the single definition of a built-in sub-agent.
//
// It used to be spread across two tables that had already drifted apart: the
// builder in supervisor.go carried the instructions and one set of descriptions,
// while runner.go carried a second set of descriptions plus its own copy of the
// tool list, purely to render the supervisor's system prompt. Adding a sub-agent
// meant editing both, and the two descriptions disagreed in wording. Everything
// about a built-in sub-agent now lives here.
type SubAgentSpec struct {
	// Name is the internal identifier: the supervisor's routing key, the ACL
	// subject, and the field carried in run events. Never rename it for display.
	Name string
	// Label is the display-only name shown in the interface.
	Label string
	// Description is what the supervisor is told, to route on. It is prompt
	// text, not security policy: which tools a sub-agent may actually call is
	// decided by ToolNames plus the ACL, never by this string.
	Description string
	// Instruction is the sub-agent's own system prompt.
	Instruction string
	// ToolNames are the tools this sub-agent owns. Also used to decide whether a
	// role can reach the sub-agent at all.
	ToolNames []string
}

// BuiltinSubAgents returns the three standard sub-agents.
func BuiltinSubAgents() []SubAgentSpec {
	return []SubAgentSpec{
		{
			Name:        "math_agent",
			Label:       "数学计算",
			ToolNames:   []string{"calculator"},
			Instruction: "你是一个数学助手，擅长数学计算。请使用 calculator 工具来帮助用户完成计算。",
			Description: "数学计算助手。当用户需要计算、算术运算、数学问题时调用。可用工具：calculator。",
		},
		{
			Name:        "search_agent",
			Label:       "信息搜索",
			ToolNames:   []string{"weather", "grep"},
			Instruction: "你是一个搜索助手，擅长查询天气和搜索日志信息。请使用 weather 和 grep 工具来帮助用户。",
			Description: "信息搜索助手。当用户需要查询天气、搜索日志、查找信息时调用。可用工具：weather（天气查询）、grep（日志搜索）。",
		},
		{
			Name:      "general_agent",
			Label:     "业务办理",
			ToolNames: []string{"query_order", "delete_order", "send_email"},
			Instruction: "你是一个通用业务助手，负责处理订单查询、删除订单、发送邮件。可用工具：query_order（查询订单）、delete_order（删除订单）、send_email（发送邮件）。\n\n" +
				"核心规则（必须严格遵守）：\n" +
				"- 必须直接调用工具完成任务，禁止用文字要求用户确认。系统内置审批机制：delete_order、send_email 等高危操作在工具执行前会自动触发人工审批，无需你自行询问用户。\n" +
				"- 当用户要求删除订单时，从用户消息中提取订单号（如 A-1001、B-2003），立即调用 delete_order 工具，参数为 {\"order_id\": \"<订单号>\"}。不要回复\"是否确认删除\"之类的话。\n" +
				"- 当用户要求发送邮件时，提取收件人邮箱和内容，立即调用 send_email 工具。\n" +
				"- 当用户要求查询订单时，调用 query_order 工具。\n" +
				"- 调用工具后，根据工具返回结果用自然语言回复用户。",
			Description: "通用业务助手。当用户需要查询订单、删除订单、发送邮件时调用。可用工具：query_order、delete_order（需审批）、send_email（需审批）。",
		},
	}
}

// nodeLabels names the non-tool interrupt points. "plan_review" is the node the
// stepped runner pauses at before executing a plan; without a label the approval
// card showed that identifier to the user.
var nodeLabels = map[string]string{
	"plan_review": "执行计划审批",
}

// SubAgentLabel returns the display label for a sub-agent, or "" when the name
// is not a built-in sub-agent.
func SubAgentLabel(name string) string {
	for _, spec := range BuiltinSubAgents() {
		if spec.Name == name {
			return spec.Label
		}
	}
	return ""
}

// isInternalExecutionName reports whether a name is one this package or the tool
// registry defines, as opposed to a value that merely looks like a name.
func isInternalExecutionName(name string) bool {
	return SubAgentLabel(name) != "" || tools.HasDisplayLabel(name)
}

// InterruptTargetName picks whichever name an interrupt carries: a tool
// interrupt names a tool, a node interrupt names a node. Only one of the two is
// ever set, so this is the name to resolve a label from.
func InterruptTargetName(toolName, nodeName string) string {
	if toolName != "" {
		return toolName
	}
	return nodeName
}

// DisplayLabelFor resolves any internal execution name — a sub-agent, a tool, or
// a node the run paused at — to what the interface should show. The fallback is
// the name itself, which is the honest answer for external MCP tools: their
// names come from a third party and we have no label for them.
//
// Routing, permissions and stored data all keep using the internal name; this is
// display only.
func DisplayLabelFor(name string) string {
	if name == "" {
		return ""
	}
	if label := SubAgentLabel(name); label != "" {
		return label
	}
	if label, ok := nodeLabels[name]; ok {
		return label
	}
	return tools.DisplayLabel(name)
}
