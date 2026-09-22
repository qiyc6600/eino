package hitl

import (
	"context"
	"sync"
	"time"

	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/tools"
)

// Service provides HITL approval and resume operations.
type Service struct {
	executionMu sync.Mutex
	manager     *InterruptManager
	checkpoint  memory.CheckpointStore
	rbac        *auth.RBACManager
	idempotency map[string]*tools.ToolResult // idempotencyKey -> result
}

// NewService creates a new HITL service.
func NewService(manager *InterruptManager, checkpoint memory.CheckpointStore, rbac *auth.RBACManager) *Service {
	return &Service{
		manager:     manager,
		checkpoint:  checkpoint,
		rbac:        rbac,
		idempotency: make(map[string]*tools.ToolResult),
	}
}

// RequestToolInterrupt creates a tool-level interrupt before a dangerous tool is executed.
func (s *Service) RequestToolInterrupt(ctx context.Context, authCtx *auth.AuthContext, runID, toolName, arguments, riskLevel, message string) (*ApprovalRequest, error) {
	payload := map[string]any{
		"type":       InterruptTypeTool,
		"tool_name":  toolName,
		"arguments":  arguments,
		"risk_level": riskLevel,
		"message":    message,
		"user_id":    authCtx.UserID,
		"thread_id":  authCtx.ThreadID,
		"run_id":     runID,
	}
	return s.manager.RequestInterrupt(ctx, payload)
}

// ListPending returns pending approvals for a user.
func (s *Service) ListPending(ctx context.Context, userID string) []*ApprovalRequest {
	return s.manager.GetPendingForUser(ctx, userID)
}

// ListPendingE is the error-preserving form used by database-backed callers.
func (s *Service) ListPendingE(ctx context.Context, userID string) ([]*ApprovalRequest, error) {
	return s.manager.GetPendingForUserE(ctx, userID)
}

// ListDecided returns the caller's recently decided approvals.
func (s *Service) ListDecided(ctx context.Context, userID string, limit int) ([]*ApprovalRequest, error) {
	return s.manager.ListDecided(ctx, userID, limit)
}

// GetApproval returns a specific approval request.
func (s *Service) GetApproval(interruptID string) (*ApprovalRequest, bool) {
	return s.manager.GetRequest(interruptID)
}

func (s *Service) GetApprovalContext(ctx context.Context, interruptID string) (*ApprovalRequest, bool, error) {
	return s.manager.GetRequestContext(ctx, interruptID)
}

// SaveCheckpoint is a convenience method for saving checkpoints.
func (s *Service) SaveCheckpoint(ctx context.Context, userID, threadID, runID string, state []byte) error {
	return s.checkpoint.Save(ctx, memory.Checkpoint{
		UserID:      userID,
		ThreadID:    threadID,
		RunID:       runID,
		State:       state,
		Interrupted: false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	})
}

// LoadCheckpoint loads a checkpoint by key.
func (s *Service) LoadCheckpoint(ctx context.Context, userID, threadID, runID string) (memory.Checkpoint, bool, error) {
	return s.checkpoint.Load(ctx, memory.CheckpointKey{UserID: userID, ThreadID: threadID, RunID: runID})
}

func (s *Service) SaveApprovalContext(ctx context.Context, req *ApprovalRequest) error {
	return s.manager.SaveContext(ctx, req)
}

func (s *Service) ClaimApproval(ctx context.Context, interruptID, userID string, decision ApprovalDecision, claimToken string) (*ApprovalRequest, bool, error) {
	return s.manager.Claim(ctx, interruptID, userID, decision, claimToken)
}

func (s *Service) CompleteApproval(ctx context.Context, req *ApprovalRequest) error {
	return s.manager.Complete(ctx, req)
}
