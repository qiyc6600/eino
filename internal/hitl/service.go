package hitl

import (
	"context"
	"fmt"
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

// RequestNodeInterrupt creates a node-level interrupt.
func (s *Service) RequestNodeInterrupt(ctx context.Context, authCtx *auth.AuthContext, runID, nodeName, message string, extra map[string]any) (*ApprovalRequest, error) {
	payload := map[string]any{
		"type":      InterruptTypeNode,
		"node_name": nodeName,
		"message":   message,
		"user_id":   authCtx.UserID,
		"thread_id": authCtx.ThreadID,
		"run_id":    runID,
	}
	for k, v := range extra {
		payload[k] = v
	}
	return s.manager.RequestInterrupt(ctx, payload)
}

// Approve processes an approval decision.
func (s *Service) Approve(ctx context.Context, interruptID string, decision ApprovalDecision) (*ApprovalRequest, error) {
	return s.manager.Resume(ctx, interruptID, decision)
}

// ExecuteWithApproval wraps a dangerous tool call with the HITL flow:
// 1. Request interrupt
// 2. Wait for approval (synchronous for in-process demo)
// 3. If approved, execute the tool with idempotency check
// 4. If rejected, return HITL disapproved result
func (s *Service) ExecuteWithApproval(ctx context.Context, authCtx *auth.AuthContext, runID string, tool tools.RegisteredTool, arguments string) tools.ToolResult {
	// Request interrupt
	req, err := s.RequestToolInterrupt(ctx, authCtx, runID, tool.Meta.Name, arguments, string(tool.Meta.RiskLevel),
		fmt.Sprintf("该操作会调用 %s，风险等级：%s，需要人工审批。", tool.Meta.Name, tool.Meta.RiskLevel))
	if err != nil {
		return tools.SystemErrorResult(tool.Meta.Name, "failed to create interrupt: "+err.Error(), "")
	}

	// In the synchronous demo flow, we return the interrupt info.
	// The actual approval will come via the API and the tool will be re-executed on resume.
	// For now, we return a special result indicating the run is interrupted.
	return tools.ToolResult{
		Content: "",
		Error:   "",
		Metadata: map[string]any{
			"tool":         tool.Meta.Name,
			"status":       "interrupted",
			"interrupt_id": req.InterruptID,
			"run_id":       runID,
			"message":      req.Message,
		},
	}
}

// ExecuteApprovedTool executes a tool after approval, with idempotency.
// 恢复语义：工具级中断恢复时重新执行工具，而非从中断行继续。
// 通过 idempotencyKey (runID:toolCallID) 保证同一工具调用的幂等性。
func (s *Service) ExecuteApprovedTool(ctx context.Context, authCtx *auth.AuthContext, runID, toolCallID string, tool tools.RegisteredTool, arguments string) tools.ToolResult {
	s.executionMu.Lock()
	defer s.executionMu.Unlock()

	// Idempotency check
	idempotencyKey := runID + ":" + toolCallID
	if result, ok := s.idempotency[idempotencyKey]; ok {
		return *result
	}

	// Execute the real tool with the framework-minted identity from ctx.
	// The resume caller chain must carry the authenticated AuthContext.
	result := tool.Fn(auth.ToolIdentityFromContext(ctx), arguments)

	// Cache for idempotency
	s.idempotency[idempotencyKey] = &result

	return result
}

// ExecuteRejectedTool returns a disapproval result when a HITL interrupt is rejected.
func (s *Service) ExecuteRejectedTool(toolName, interruptID, reason string) tools.ToolResult {
	return tools.HITLDisapprovedResult(toolName, reason, interruptID)
}

// ListPending returns pending approvals for a user.
func (s *Service) ListPending(ctx context.Context, userID string) []*ApprovalRequest {
	return s.manager.GetPendingForUser(ctx, userID)
}

// ListPendingE is the error-preserving form used by database-backed callers.
func (s *Service) ListPendingE(ctx context.Context, userID string) ([]*ApprovalRequest, error) {
	return s.manager.GetPendingForUserE(ctx, userID)
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

// SaveApproval persists an immutable approval snapshot and execution receipt.
func (s *Service) SaveApproval(req *ApprovalRequest) error { return s.manager.Save(req) }

func (s *Service) SaveApprovalContext(ctx context.Context, req *ApprovalRequest) error {
	return s.manager.SaveContext(ctx, req)
}

func (s *Service) ClaimApproval(ctx context.Context, interruptID, userID string, decision ApprovalDecision, claimToken string) (*ApprovalRequest, bool, error) {
	return s.manager.Claim(ctx, interruptID, userID, decision, claimToken)
}

func (s *Service) CompleteApproval(ctx context.Context, req *ApprovalRequest) error {
	return s.manager.Complete(ctx, req)
}
