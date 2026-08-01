package hitl

import (
	"context"
	"testing"

	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/tools"
)

func TestHITL_ToolInterrupt(t *testing.T) {
	checkpointStore := memory.NewInMemoryCheckpointStore()
	manager := NewInterruptManager(checkpointStore)
	rbac := auth.NewRBACManager()
	svc := NewService(manager, checkpointStore, rbac)

	authCtx := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}
	tool := tools.NewDeleteOrderTool(tools.NewOrderStore())

	// Create interrupt
	result := svc.ExecuteWithApproval(context.Background(), authCtx, "r_001", tool, `{"order_id":"A-1001"}`)
	if result.Metadata["status"] != "interrupted" {
		t.Errorf("expected interrupted status, got: %v", result.Metadata["status"])
	}
	if result.Metadata["interrupt_id"] == nil || result.Metadata["interrupt_id"] == "" {
		t.Error("expected interrupt_id in metadata")
	}
}

func TestHITL_ApproveAndExecute(t *testing.T) {
	checkpointStore := memory.NewInMemoryCheckpointStore()
	manager := NewInterruptManager(checkpointStore)
	rbac := auth.NewRBACManager()
	svc := NewService(manager, checkpointStore, rbac)

	authCtx := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}
	tool := tools.NewDeleteOrderTool(tools.NewOrderStore())

	// Create interrupt
	result := svc.ExecuteWithApproval(context.Background(), authCtx, "r_002", tool, `{"order_id":"A-1001"}`)
	interruptID, _ := result.Metadata["interrupt_id"].(string)
	if interruptID == "" {
		t.Fatal("expected interrupt_id")
	}

	// Approve
	req, err := svc.Approve(context.Background(), interruptID, ApprovalDecision{Approved: true, Reason: "test approve"})
	if err != nil {
		t.Fatalf("approve failed: %v", err)
	}
	if req.Status != StatusApproved {
		t.Errorf("expected approved status, got: %s", req.Status)
	}
}

func TestHITL_Reject(t *testing.T) {
	checkpointStore := memory.NewInMemoryCheckpointStore()
	manager := NewInterruptManager(checkpointStore)
	rbac := auth.NewRBACManager()
	svc := NewService(manager, checkpointStore, rbac)

	authCtx := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}
	tool := tools.NewDeleteOrderTool(tools.NewOrderStore())

	result := svc.ExecuteWithApproval(context.Background(), authCtx, "r_003", tool, `{"order_id":"A-1001"}`)
	interruptID, _ := result.Metadata["interrupt_id"].(string)

	req, err := svc.Approve(context.Background(), interruptID, ApprovalDecision{Approved: false, Reason: "test reject"})
	if err != nil {
		t.Fatalf("reject failed: %v", err)
	}
	if req.Status != StatusRejected {
		t.Errorf("expected rejected status, got: %s", req.Status)
	}
}

func TestHITL_Idempotency(t *testing.T) {
	checkpointStore := memory.NewInMemoryCheckpointStore()
	manager := NewInterruptManager(checkpointStore)
	rbac := auth.NewRBACManager()
	svc := NewService(manager, checkpointStore, rbac)

	authCtx := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}}
	tool := tools.NewDeleteOrderTool(tools.NewOrderStore())

	// Execute twice with same runID + toolCallID — should return identical result
	result1 := svc.ExecuteApprovedTool(context.Background(), authCtx, "r_004", "tc_001", tool, `{"order_id":"A-1001"}`)
	result2 := svc.ExecuteApprovedTool(context.Background(), authCtx, "r_004", "tc_001", tool, `{"order_id":"A-1001"}`)

	if result1.Content != result2.Content {
		t.Errorf("idempotency violated: result1=%s result2=%s", result1.Content, result2.Content)
	}
}

func TestHITL_RejectedToolResult(t *testing.T) {
	checkpointStore := memory.NewInMemoryCheckpointStore()
	manager := NewInterruptManager(checkpointStore)
	rbac := auth.NewRBACManager()
	svc := NewService(manager, checkpointStore, rbac)

	result := svc.ExecuteRejectedTool("delete_order", "interrupt_123", "user declined")
	if result.Metadata["status"] != "disapproved" {
		t.Errorf("expected status=disapproved, got %v", result.Metadata["status"])
	}
	if result.Metadata["interrupt_id"] != "interrupt_123" {
		t.Errorf("expected interrupt_id in metadata")
	}
}

func TestHITL_ListPending(t *testing.T) {
	checkpointStore := memory.NewInMemoryCheckpointStore()
	manager := NewInterruptManager(checkpointStore)
	rbac := auth.NewRBACManager()
	svc := NewService(manager, checkpointStore, rbac)

	authCtx := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}
	tool := tools.NewDeleteOrderTool(tools.NewOrderStore())

	// No pending approvals initially
	pending := svc.ListPending(context.Background(), "u_admin")
	if len(pending) != 0 {
		t.Errorf("expected 0 pending, got %d", len(pending))
	}

	// Create interrupt
	svc.ExecuteWithApproval(context.Background(), authCtx, "r_005", tool, `{"order_id":"A-1001"}`)

	// Should now have 1 pending
	pending = svc.ListPending(context.Background(), "u_admin")
	if len(pending) != 1 {
		t.Errorf("expected 1 pending, got %d", len(pending))
	}
}
