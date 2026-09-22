package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
	pgstore "github.com/example/agent-eino-demo/internal/postgres"
)

// TestPostgresApprovalHistory exercises the SQL behind ListDecided against a real
// database. The in-memory test covers the same behaviour, but the ordering and the
// status filter are SQL here, and the unit tests use sqlmock — which checks the
// statement text but not whether PostgreSQL accepts it or returns the rows in the
// order the panel expects.
func TestPostgresApprovalHistory(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := pgstore.Open(ctx, dsn, 5, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	suffix := uuid.New().String()
	userID := "test-user-" + suffix
	otherUserID := "test-other-" + suffix
	authCtx := auth.WithAuthContext(ctx, &auth.AuthContext{UserID: userID})
	otherCtx := auth.WithAuthContext(ctx, &auth.AuthContext{UserID: otherUserID})

	first := "test-approval-a-" + suffix
	second := "test-approval-b-" + suffix
	stillPending := "test-approval-c-" + suffix
	otherUsers := "test-approval-d-" + suffix
	defer func() {
		_, _ = store.DB.Exec(`DELETE FROM agent_approvals WHERE user_id IN ($1,$2)`, userID, otherUserID)
	}()

	save := func(t *testing.T, ctx context.Context, id, owner string) {
		t.Helper()
		if err := store.Approvals.Save(ctx, &hitl.ApprovalRequest{
			InterruptID: id, UserID: owner, ThreadID: "t", RunID: "r",
			ToolName: "delete_order", Status: hitl.StatusPending, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(t *testing.T, ctx context.Context, id, owner string, approved bool) {
		t.Helper()
		if _, won, err := store.Approvals.Claim(ctx, id, owner,
			hitl.ApprovalDecision{Approved: approved, Reason: "because"}, "tok-"+id); err != nil || !won {
			t.Fatalf("claim %s: won=%v err=%v", id, won, err)
		}
	}

	save(t, authCtx, first, userID)
	claim(t, authCtx, first, userID, true)
	// A moment apart, so the newest-first ordering is unambiguous.
	time.Sleep(20 * time.Millisecond)
	save(t, authCtx, second, userID)
	claim(t, authCtx, second, userID, false)
	save(t, authCtx, stillPending, userID) // never decided
	save(t, otherCtx, otherUsers, otherUserID)
	claim(t, otherCtx, otherUsers, otherUserID, true)

	rows, err := store.Approvals.ListDecided(ctx, userID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected the two decided approvals, got %d: %+v", len(rows), rows)
	}
	if rows[0].InterruptID != second || rows[1].InterruptID != first {
		t.Fatalf("expected newest first (%s then %s), got %s then %s",
			second, first, rows[0].InterruptID, rows[1].InterruptID)
	}
	if rows[0].Status != hitl.StatusRejected || rows[1].Status != hitl.StatusApproved {
		t.Fatalf("statuses not carried: %s=%s %s=%s",
			rows[0].InterruptID, rows[0].Status, rows[1].InterruptID, rows[1].Status)
	}
	// The reason is what makes the history worth reading.
	if rows[1].Decision == nil || rows[1].Decision.Reason != "because" {
		t.Fatalf("the decision reason must survive: %+v", rows[1].Decision)
	}
	if rows[1].DecidedAt == nil {
		t.Fatal("a decided row must carry when it was decided — the ordering depends on it")
	}

	// A pending approval is not history, and another user's decision is not this
	// user's history.
	for _, r := range rows {
		if r.InterruptID == stillPending {
			t.Error("a pending approval appeared in the history")
		}
		if r.InterruptID == otherUsers {
			t.Error("another user's decision appeared in this user's history")
		}
	}

	// The limit is applied by SQL, not after the fact.
	limited, err := store.Approvals.ListDecided(ctx, userID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].InterruptID != second {
		t.Fatalf("limit=1 should return the newest decision, got %+v", limited)
	}
}
