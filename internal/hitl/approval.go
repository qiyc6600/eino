// Package hitl implements Human-in-the-Loop approvals.
// The runner persists the complete nested state, claims execution before side
// effects, and saves the response for repeated decisions. A process crash with
// a running claim is an uncertain outcome and must not trigger automatic replay.
// External tools need business-side idempotency to reconcile such outcomes.
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
	StatusPending  ApprovalStatus = "pending"
	StatusApproved ApprovalStatus = "approved"
	StatusRejected ApprovalStatus = "rejected"
)

// ApprovalRequest represents an interrupt that requires human approval.
type ApprovalRequest struct {
	InterruptID string
	State       []byte `json:"State,omitempty"`  // Complete resumable state, including nested agents.
	Result      []byte `json:"Result,omitempty"` // Durable response for idempotent retries.
	Phase       string // empty, running, or finished; running after a crash needs reconciliation.
	ClaimToken  string // identifies the process that atomically claimed execution.
	Type        InterruptType
	RunID       string
	UserID      string
	ThreadID    string
	// NodeName and ToolName are mutually exclusive:
	// node interrupt => ToolName is empty; tool interrupt => NodeName is empty.
	NodeName   string
	ToolName   string
	ToolCallID string // the Eino tool_call ID for feeding result back to ReAct loop
	Arguments  string
	Payload    map[string]any
	RiskLevel  string
	Message    string
	Status     ApprovalStatus
	CreatedAt  time.Time
	DecidedAt  *time.Time
	Decision   *ApprovalDecision
}

// ApprovalDecision represents the human's decision on an approval request.
type ApprovalDecision struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}
