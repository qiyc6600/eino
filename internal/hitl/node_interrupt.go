package hitl

import (
	"context"
)

// NodeInterruptGate provides a gate that can interrupt agent execution at a
// non-tool node (e.g., after plan generation, before batch execution).
// This is placed in the supervisor's outer Graph/Workflow, NOT inside the
// ReAct agent's internal Reason -> Act -> Observe loop.
//
// 恢复语义（节点级中断）：恢复时从中断点继续执行后续节点，而非重新执行中断节点。
// 与工具级中断的"重新执行"语义不同，因为节点中断发生在逻辑节点之间，
// 批准后直接进入下一个节点即可。
type NodeInterruptGate struct {
	NodeName string
	Message  string
	manager  *InterruptManager
}

// NewNodeInterruptGate creates a gate for a named workflow node.
func NewNodeInterruptGate(nodeName, message string, manager *InterruptManager) *NodeInterruptGate {
	return &NodeInterruptGate{
		NodeName: nodeName,
		Message:  message,
		manager:  manager,
	}
}

// Check executes the gate logic: creates an interrupt if approval is needed.
// Returns the ApprovalRequest so the caller can wait for the decision.
func (g *NodeInterruptGate) Check(ctx context.Context, payload map[string]any) (*ApprovalRequest, error) {
	payload["type"] = InterruptTypeNode
	payload["node_name"] = g.NodeName
	payload["message"] = g.Message
	return g.manager.RequestInterrupt(ctx, payload)
}
