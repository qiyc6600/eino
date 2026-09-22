package hitl

import "context"

// ApprovalStore is the durable coordination surface for HITL decisions.
// Claim must be atomic across processes: exactly one caller may move a pending
// approval to running. Complete must only accept the matching claim token.
type ApprovalStore interface {
	Save(ctx context.Context, req *ApprovalRequest) error
	Get(ctx context.Context, interruptID string) (*ApprovalRequest, bool, error)
	ListPending(ctx context.Context, userID string) ([]*ApprovalRequest, error)
	// ListDecided returns the most recently decided approvals, newest first. An
	// approval leaves the pending list the moment it is decided, so without this
	// the only record of a decision is the chat card that produced it.
	ListDecided(ctx context.Context, userID string, limit int) ([]*ApprovalRequest, error)
	Claim(ctx context.Context, interruptID, userID string, decision ApprovalDecision, claimToken string) (*ApprovalRequest, bool, error)
	Complete(ctx context.Context, req *ApprovalRequest) error
}
