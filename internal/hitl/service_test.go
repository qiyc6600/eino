package hitl

import (
	"context"
	"testing"

	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/memory"
)

// newTestService builds a service with the in-memory manager, which is the
// deployment where the manager's own lock provides the atomic claim.
func newTestService(t *testing.T) (*Service, *InterruptManager) {
	t.Helper()
	checkpointStore := memory.NewInMemoryCheckpointStore()
	manager := NewInterruptManager(checkpointStore)
	return NewService(manager, checkpointStore, auth.NewRBACManager()), manager
}

// raiseToolInterrupt creates a pending tool approval through the production API
// and returns its id. The tests below used to call ExecuteWithApproval, which
// was a second, unused way to raise the same interrupt.
func raiseToolInterrupt(t *testing.T, svc *Service, authCtx *auth.AuthContext, runID string) string {
	t.Helper()
	req, err := svc.RequestToolInterrupt(context.Background(), authCtx, runID,
		"delete_order", `{"order_id":"A-1001"}`, "high", "该操作需要人工审批。")
	if err != nil {
		t.Fatalf("raise interrupt: %v", err)
	}
	if req.Status != StatusPending {
		t.Fatalf("a new interrupt should be pending, got %s", req.Status)
	}
	return req.InterruptID
}

func TestHITL_ToolInterrupt(t *testing.T) {
	svc, _ := newTestService(t)
	authCtx := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}

	id := raiseToolInterrupt(t, svc, authCtx, "r_001")
	if id == "" {
		t.Fatal("expected an interrupt id")
	}

	pending := svc.ListPending(context.Background(), "u_admin")
	if len(pending) != 1 || pending[0].InterruptID != id {
		t.Fatalf("expected the interrupt to be listed as pending, got %v", pending)
	}
	// Another user must not see it.
	if other := svc.ListPending(context.Background(), "u_other"); len(other) != 0 {
		t.Fatalf("another user's pending list must be empty, got %v", other)
	}
	// And an empty user must not mean "every user": that unfiltered read used to
	// exist as GetPending, and it would have handed every pending approval to any
	// caller that passed "".
	if all := svc.ListPending(context.Background(), ""); len(all) != 0 {
		t.Fatalf("an empty userID must not return other users' approvals, got %v", all)
	}
}

// TestHITL_ClaimIsSingleWinner is the core safety invariant: only one caller may
// claim an approval, whatever the decision, because claiming is what authorises
// the gated tool to run.
func TestHITL_ClaimIsSingleWinner(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	authCtx := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}
	id := raiseToolInterrupt(t, svc, authCtx, "r_002")

	first, won, err := svc.ClaimApproval(ctx, id, "u_admin", ApprovalDecision{Approved: true, Reason: "first"}, "tok-1")
	if err != nil || !won {
		t.Fatalf("the first claim must win: won=%v err=%v", won, err)
	}
	if first.Status != StatusApproved || first.Phase != "running" || first.ClaimToken != "tok-1" {
		t.Fatalf("the claim should mark it approved and running: %+v", first)
	}

	// A second claim, even with the same decision, must not win.
	second, won, err := svc.ClaimApproval(ctx, id, "u_admin", ApprovalDecision{Approved: true, Reason: "again"}, "tok-2")
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Fatal("a second claim must not win — two winners means the tool runs twice")
	}
	if second == nil || second.ClaimToken != "tok-1" {
		t.Fatalf("the losing claim should report the existing claim, got %+v", second)
	}

	// A claim with the opposite decision must not win either: flipping a decision
	// after it was made would be an approval bypass.
	if _, won, err := svc.ClaimApproval(ctx, id, "u_admin", ApprovalDecision{Approved: false}, "tok-3"); err != nil || won {
		t.Fatalf("a claim with the opposite decision must not win: won=%v err=%v", won, err)
	}
}

// TestHITL_ClaimRequiresOwnership: a claim is scoped to the user who owns the
// approval, so one user cannot approve another's gated operation.
func TestHITL_ClaimRequiresOwnership(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	owner := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}
	id := raiseToolInterrupt(t, svc, owner, "r_003")

	claimed, won, err := svc.ClaimApproval(ctx, id, "u_intruder", ApprovalDecision{Approved: true}, "tok-x")
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Fatal("another user claimed an approval they do not own")
	}
	// The losing caller must not be handed the request either: that would confirm
	// the interrupt exists to someone who cannot see it.
	if claimed != nil {
		t.Fatalf("a non-owner must not receive the request, got %+v", claimed)
	}
	// And it must still be pending for its owner.
	if pending := svc.ListPending(ctx, "u_admin"); len(pending) != 1 {
		t.Fatalf("the approval should still be pending for its owner, got %v", pending)
	}
}

// TestHITL_CompleteRequiresTheLiveClaim: only the claim that won may finish the
// approval, so a stale worker cannot mark someone else's execution done.
func TestHITL_CompleteRequiresTheLiveClaim(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	owner := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}
	id := raiseToolInterrupt(t, svc, owner, "r_004")

	// Completing without a claim must be refused.
	unclaimed, _, err := svc.ClaimApproval(ctx, id, "u_admin", ApprovalDecision{Approved: true}, "tok-live")
	if err != nil {
		t.Fatal(err)
	}
	bogus := *unclaimed
	bogus.ClaimToken = "tok-stale"
	if err := svc.CompleteApproval(ctx, &bogus); err == nil {
		t.Fatal("completing with a stale claim token must be refused")
	}

	// The live claim may complete.
	unclaimed.Result = []byte(`{"ok":true}`)
	unclaimed.Phase = "finished"
	if err := svc.CompleteApproval(ctx, unclaimed); err != nil {
		t.Fatalf("the live claim should be able to complete: %v", err)
	}

	// And a completed approval is no longer pending.
	if pending := svc.ListPending(ctx, "u_admin"); len(pending) != 0 {
		t.Fatalf("a completed approval must not stay pending, got %v", pending)
	}
}

func TestHITL_Reject(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	authCtx := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}
	id := raiseToolInterrupt(t, svc, authCtx, "r_005")

	req, won, err := svc.ClaimApproval(ctx, id, "u_admin", ApprovalDecision{Approved: false, Reason: "test reject"}, "tok-r")
	if err != nil || !won {
		t.Fatalf("claim failed: won=%v err=%v", won, err)
	}
	if req.Status != StatusRejected {
		t.Errorf("expected rejected status, got: %s", req.Status)
	}
}

func TestHITL_RejectedToolResult(t *testing.T) {
	svc, _ := newTestService(t)

	result := svc.ExecuteRejectedTool("delete_order", "interrupt_123", "user declined")
	if result.Metadata["status"] != "disapproved" {
		t.Errorf("expected status=disapproved, got %v", result.Metadata["status"])
	}
	if result.Metadata["interrupt_id"] != "interrupt_123" {
		t.Errorf("expected interrupt_id in metadata")
	}
}

func TestHITL_ListPending(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	authCtx := &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}, ThreadID: "t_001"}

	if pending := svc.ListPending(ctx, "u_admin"); len(pending) != 0 {
		t.Errorf("expected 0 pending, got %d", len(pending))
	}

	raiseToolInterrupt(t, svc, authCtx, "r_006")

	if pending := svc.ListPending(ctx, "u_admin"); len(pending) != 1 {
		t.Errorf("expected 1 pending, got %d", len(pending))
	}
}
