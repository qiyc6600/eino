// Package hitl implements Human-in-the-Loop (HITL) interrupt and approval mechanisms.
//
// # 恢复语义说明
//
// 本包实现两种中断类型，其恢复语义不同：
//
// 工具级中断恢复：恢复时重新执行工具（非从中断行继续）。
//
//	原因：Eino 的 ReAct 循环中，工具调用完成后才将结果写入对话历史。
//	中断发生在工具调用前，此时工具尚未执行，无"中断行"可继续。
//	恢复时，Runner.Resume() 重新调用工具函数并传入原始参数。
//	幂等性通过 runID+toolCallID 生成的 idempotencyKey 保证。
//
// 节点级中断恢复：恢复时从中断点继续执行后续节点。
//
//	中断前：节点执行到中断点，状态保存在 Checkpoint 中。
//	批准后：从中断点继续执行后续节点。
//	拒绝后：进入拒绝分支或生成替代方案。
//
// 业务方需确保危险工具的幂等性，以应对工具级中断的"重新执行"语义。
package hitl

import (
	"time"
)

// InterruptType distinguishes between node-level and tool-level interrupts.
type InterruptType string

const (
	InterruptTypeNode InterruptType = "node"
	InterruptTypeTool InterruptType = "tool"
)

// ApprovalStatus represents the status of an approval request.
type ApprovalStatus string

const (
	StatusPending   ApprovalStatus = "pending"
	StatusApproved  ApprovalStatus = "approved"
	StatusRejected  ApprovalStatus = "rejected"
)

// ApprovalRequest represents an interrupt that requires human approval.
type ApprovalRequest struct {
	InterruptID string
	Type        InterruptType
	RunID       string
	UserID      string
	ThreadID    string
	// NodeName and ToolName are mutually exclusive:
	// node interrupt => ToolName is empty; tool interrupt => NodeName is empty.
	NodeName    string
	ToolName    string
	ToolCallID  string // the Eino tool_call ID for feeding result back to ReAct loop
	Arguments   string
	Payload     map[string]any
	RiskLevel   string
	Message     string
	Status      ApprovalStatus
	CreatedAt   time.Time
	DecidedAt   *time.Time
	Decision    *ApprovalDecision
}

// ApprovalDecision represents the human's decision on an approval request.
type ApprovalDecision struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}
