package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/tools"
	"github.com/jackc/pgx/v5/pgconn"
)

func mockBackend(t *testing.T) (*Backend, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewBackend(db), mock
}

func TestPublishInterruptRollsBackWhenApprovalWriteFails(t *testing.T) {
	backend, mock := mockBackend(t)
	state := []byte(`{"run_id":"r-1"}`)
	publication := agent.InterruptPublication{
		Checkpoint: memory.Checkpoint{UserID: "u-1", ThreadID: "t-1", RunID: "r-1", State: state, Interrupted: true, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Messages:   []*schema.Message{schema.UserMessage("hello")},
		Approval:   &hitl.ApprovalRequest{InterruptID: "i-1", UserID: "u-1", ThreadID: "t-1", RunID: "r-1", State: state, Status: hitl.StatusPending, CreatedAt: time.Now()},
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO agent_checkpoints").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO agent_threads").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO agent_approvals").WillReturnError(errors.New("approval write failed"))
	mock.ExpectRollback()
	err := backend.PublishInterrupt(context.Background(), publication)
	if err == nil || !strings.Contains(err.Error(), "approval write failed") {
		t.Fatalf("expected approval failure, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishResumeRollsBackAtEveryWrite(t *testing.T) {
	for _, next := range []bool{false, true} {
		steps := []string{"INSERT INTO agent_threads", "UPDATE agent_approvals SET data", "INSERT INTO agent_runs", "DELETE FROM agent_runs"}
		mode := "complete"
		if next {
			mode = "next interrupt"
			steps = []string{"INSERT INTO agent_checkpoints", "INSERT INTO agent_threads", "INSERT INTO agent_approvals", "UPDATE agent_approvals SET data", "INSERT INTO agent_runs", "DELETE FROM agent_runs"}
		}
		for failAt := range steps {
			t.Run(fmt.Sprintf("%s/%d", mode, failAt), func(t *testing.T) {
				backend, mock := mockBackend(t)
				result := agent.ChatRunResult{RunID: "r-1", Status: agent.StatusCompleted}
				if next {
					result.Status = agent.StatusInterrupted
					result.Interrupt = &agent.Interrupt{InterruptID: "i-next"}
				}
				resultData, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				state := []byte(`{"run_id":"r-1"}`)
				publication := agent.ResumePublication{
					Approval: &hitl.ApprovalRequest{
						InterruptID: "i-old", UserID: "u-1", ThreadID: "t-1", RunID: "r-1", State: state,
						Phase: "finished", ClaimToken: "claim", Status: hitl.StatusApproved,
						Decision: &hitl.ApprovalDecision{Approved: true}, Result: resultData,
					},
					Result: result, Retention: time.Hour,
				}
				if next {
					publication.Next = &agent.InterruptPublication{
						Checkpoint: memory.Checkpoint{UserID: "u-1", ThreadID: "t-1", RunID: "r-1", State: state, Interrupted: true},
						Messages:   []*schema.Message{schema.UserMessage("next")},
						Approval:   &hitl.ApprovalRequest{InterruptID: "i-next", UserID: "u-1", ThreadID: "t-1", RunID: "r-1", State: state, Status: hitl.StatusPending},
					}
				} else {
					publication.ReplaceThread = true
					publication.Messages = []*schema.Message{schema.UserMessage("done")}
				}
				mock.ExpectBegin()
				for i, statement := range steps[:failAt+1] {
					expectation := mock.ExpectExec(statement)
					if i == failAt {
						expectation.WillReturnError(errors.New("injected failure"))
					} else {
						expectation.WillReturnResult(sqlmock.NewResult(0, 1))
					}
				}
				mock.ExpectRollback()
				if err := backend.PublishResume(context.Background(), publication); err == nil || !strings.Contains(err.Error(), "injected failure") {
					t.Fatalf("expected injected failure, got %v", err)
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func approvalRow(req *hitl.ApprovalRequest, status, phase, token string, approved any, reason string, decided any) *sqlmock.Rows {
	data, _ := json.Marshal(req)
	return sqlmock.NewRows([]string{"data", "status", "phase", "approved", "reason", "claim_token", "decided_at"}).
		AddRow(data, status, phase, approved, reason, token, decided)
}

func TestApprovalStoreAtomicClaimAndReplay(t *testing.T) {
	backend, mock := mockBackend(t)
	req := &hitl.ApprovalRequest{
		InterruptID: "i-1", UserID: "u-1", ThreadID: "t-1", RunID: "r-1",
		Status: hitl.StatusPending, CreatedAt: time.Now(), State: []byte(`{"step":1}`),
	}
	decision := hitl.ApprovalDecision{Approved: true, Reason: "reviewed"}
	decided := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta("UPDATE agent_approvals SET status=$3,phase='running',approved=$4,reason=$5,claim_token=$6,decided_at=NOW(),updated_at=NOW()")+".*").
		WithArgs("i-1", "u-1", hitl.StatusApproved, true, "reviewed", "claim-a").
		WillReturnRows(approvalRow(req, string(hitl.StatusApproved), "running", "claim-a", true, "reviewed", decided))

	claimed, won, err := backend.Approvals.Claim(context.Background(), "i-1", "u-1", decision, "claim-a")
	if err != nil || !won {
		t.Fatalf("claim failed: won=%v err=%v", won, err)
	}
	if claimed.Phase != "running" || claimed.ClaimToken != "claim-a" || claimed.Decision == nil || !claimed.Decision.Approved {
		t.Fatalf("claim overlay mismatch: %+v", claimed)
	}

	// A second instance loses the compare-and-set and reads the existing claim.
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE agent_approvals SET status=$3,phase='running',approved=$4,reason=$5,claim_token=$6,decided_at=NOW(),updated_at=NOW()")+".*").
		WithArgs("i-1", "u-1", hitl.StatusApproved, true, "reviewed", "claim-b").
		WillReturnRows(sqlmock.NewRows([]string{"data", "status", "phase", "approved", "reason", "claim_token", "decided_at"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT data,status,phase,approved,reason,claim_token,decided_at FROM agent_approvals WHERE interrupt_id=$1")).
		WithArgs("i-1").WillReturnRows(approvalRow(req, string(hitl.StatusApproved), "running", "claim-a", true, "reviewed", decided))

	existing, won, err := backend.Approvals.Claim(context.Background(), "i-1", "u-1", decision, "claim-b")
	if err != nil || won || existing.ClaimToken != "claim-a" {
		t.Fatalf("second claim should lose: won=%v req=%+v err=%v", won, existing, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalStoreCompleteRequiresClaimOwnership(t *testing.T) {
	backend, mock := mockBackend(t)
	req := &hitl.ApprovalRequest{
		InterruptID: "i-1", UserID: "u-1", Status: hitl.StatusApproved,
		Phase: "finished", ClaimToken: "claim-a", Result: []byte(`{"status":"completed"}`),
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE agent_approvals SET data=$3::jsonb,status=$4,phase='finished',updated_at=NOW()")+".*").
		WithArgs("i-1", "claim-a", sqlmock.AnyArg(), hitl.StatusApproved).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := backend.Approvals.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE agent_approvals SET data=$3::jsonb,status=$4,phase='finished',updated_at=NOW()")+".*").
		WithArgs("i-1", "claim-a", sqlmock.AnyArg(), hitl.StatusApproved).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := backend.Approvals.Complete(context.Background(), req); err == nil {
		t.Fatal("stale claim unexpectedly completed approval")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThreadStoreAppendAndAdvisoryLock(t *testing.T) {
	backend, mock := mockBackend(t)
	mock.ExpectExec("INSERT INTO agent_threads.*agent_threads.messages \\|\\| EXCLUDED.messages").
		WithArgs("u-1", "t-1", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	if err := backend.Threads.Append("u-1", "t-1", schema.UserMessage("hello")); err != nil {
		t.Fatal(err)
	}

	key := `["u-1","t-1"]`
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_lock(hashtextextended($1,0))")).
		WithArgs(key).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock(hashtextextended($1,0))")).
		WithArgs(key).WillReturnResult(sqlmock.NewResult(0, 1))
	unlock, err := backend.Threads.LockThread(context.Background(), "u-1", "t-1")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestThreadStoreAppendHistoryGuard covers the length-guarded append: the guard
// is evaluated inside the UPDATE, so a matching history is appended in place
// while a stale one is reported for the caller to replace.
func TestThreadStoreAppendHistoryGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("matching prefix appends in place", func(t *testing.T) {
		backend, mock := mockBackend(t)
		mock.ExpectExec("UPDATE agent_threads.*messages \\|\\| \\$3::jsonb").
			WithArgs("u-1", "t-1", sqlmock.AnyArg(), 4).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := backend.Threads.AppendHistoryContext(ctx, "u-1", "t-1", 4, []*schema.Message{schema.UserMessage("next")}); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("stale prefix reports a mismatch instead of appending", func(t *testing.T) {
		backend, mock := mockBackend(t)
		mock.ExpectExec("UPDATE agent_threads.*messages \\|\\| \\$3::jsonb").
			WithArgs("u-1", "t-1", sqlmock.AnyArg(), 2).
			WillReturnResult(sqlmock.NewResult(0, 0))
		err := backend.Threads.AppendHistoryContext(ctx, "u-1", "t-1", 2, []*schema.Message{schema.UserMessage("next")})
		if !errors.Is(err, agent.ErrThreadAppendMismatch) {
			t.Fatalf("expected ErrThreadAppendMismatch, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("first turn inserts only when the thread is absent", func(t *testing.T) {
		backend, mock := mockBackend(t)
		mock.ExpectExec("UPDATE agent_threads.*messages \\|\\| \\$3::jsonb").
			WithArgs("u-1", "t-1", sqlmock.AnyArg(), 0).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO agent_threads.*ON CONFLICT DO NOTHING").
			WithArgs("u-1", "t-1", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := backend.Threads.AppendHistoryContext(ctx, "u-1", "t-1", 0, []*schema.Message{schema.UserMessage("first")}); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a thread that appeared in between is not appended to blindly", func(t *testing.T) {
		backend, mock := mockBackend(t)
		mock.ExpectExec("UPDATE agent_threads.*messages \\|\\| \\$3::jsonb").
			WithArgs("u-1", "t-1", sqlmock.AnyArg(), 0).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO agent_threads.*ON CONFLICT DO NOTHING").
			WithArgs("u-1", "t-1", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))
		err := backend.Threads.AppendHistoryContext(ctx, "u-1", "t-1", 0, []*schema.Message{schema.UserMessage("first")})
		if !errors.Is(err, agent.ErrThreadAppendMismatch) {
			t.Fatalf("expected ErrThreadAppendMismatch, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cross-user writes are refused by the store", func(t *testing.T) {
		backend, mock := mockBackend(t)
		// The identity in context belongs to another user, so the store must
		// refuse before touching the database.
		ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{UserID: "u-other"})
		err := backend.Threads.AppendHistoryContext(ctx, "u-1", "t-1", 0, []*schema.Message{schema.UserMessage("x")})
		if err == nil {
			t.Fatal("expected a scope violation")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestThreadStoreUsesDedicatedLockPool(t *testing.T) {
	db, dbMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lockDB, lockMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer lockDB.Close()
	backend := newBackend(db, lockDB)
	key := `["u-1","t-1"]`
	lockMock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_lock(hashtextextended($1,0))")).
		WithArgs(key).WillReturnResult(sqlmock.NewResult(0, 1))
	lockMock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock(hashtextextended($1,0))")).
		WithArgs(key).WillReturnResult(sqlmock.NewResult(0, 1))

	unlock, err := backend.Threads.LockThread(context.Background(), "u-1", "t-1")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := lockMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := dbMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("operational pool received lock query: %v", err)
	}
}

func TestUserStoreCreateReadAndUpdate(t *testing.T) {
	backend, mock := mockBackend(t)
	user := auth.User{ID: "u-1", Username: "alice", PasswordHash: "hash", Roles: []string{"visitor"}}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO agent_users(id,username,password_hash,roles) VALUES($1,$2,$3,$4::jsonb)")).
		WithArgs(user.ID, user.Username, user.PasswordHash, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := backend.Users.Create(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	roles, _ := json.Marshal(user.Roles)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,username,password_hash,roles FROM agent_users WHERE username=$1")).
		WithArgs("alice").WillReturnRows(sqlmock.NewRows([]string{"id", "username", "password_hash", "roles"}).
		AddRow(user.ID, user.Username, user.PasswordHash, roles))
	loaded, ok, err := backend.Users.GetByUsername(context.Background(), "alice")
	if err != nil || !ok || loaded.ID != user.ID || len(loaded.Roles) != 1 {
		t.Fatalf("user round trip: ok=%v user=%+v err=%v", ok, loaded, err)
	}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE agent_users SET roles=$2::jsonb,updated_at=NOW() WHERE id=$1")).
		WithArgs(user.ID, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := backend.Users.UpdateRoles(context.Background(), user.ID, []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE agent_users SET password_hash=$2,updated_at=NOW() WHERE id=$1")).
		WithArgs(user.ID, "new-hash").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := backend.Users.UpdatePasswordHash(context.Background(), user.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresSessionStoreDeleteByUser(t *testing.T) {
	backend, mock := mockBackend(t)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM agent_sessions WHERE user_id=$1")).
		WithArgs("u-1").WillReturnResult(sqlmock.NewResult(0, 2))
	if err := backend.Sessions.DeleteByUser(context.Background(), "u-1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserStoreMapsConflictsAndMissingUsers(t *testing.T) {
	backend, mock := mockBackend(t)
	user := auth.User{ID: "u-1", Username: "alice", PasswordHash: "hash", Roles: []string{"visitor"}}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO agent_users(id,username,password_hash,roles) VALUES($1,$2,$3,$4::jsonb)")).
		WithArgs(user.ID, user.Username, user.PasswordHash, sqlmock.AnyArg()).
		WillReturnError(&pgconn.PgError{Code: "23505"})
	if err := backend.Users.Create(context.Background(), user); !errors.Is(err, auth.ErrUserExists) {
		t.Fatalf("expected ErrUserExists, got %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE agent_users SET roles=$2::jsonb,updated_at=NOW() WHERE id=$1")).
		WithArgs("missing", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
	if err := backend.Users.UpdateRoles(context.Background(), "missing", []string{"admin"}); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE agent_users SET password_hash=$2,updated_at=NOW() WHERE id=$1")).
		WithArgs("missing", "new-hash").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := backend.Users.UpdatePasswordHash(context.Background(), "missing", "new-hash"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOrderStoreDeleteIsIdempotent(t *testing.T) {
	backend, mock := mockBackend(t)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{UserID: "u-1"})

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO agent_orders").
		WithArgs("u-1", "o-1", "pending", "10", "test").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := backend.Orders.Seed(ctx, []tools.Order{{ID: "o-1", UserID: "u-1", Status: "pending", Amount: "10", Desc: "test"}}); err != nil {
		t.Fatal(err)
	}

	key := "u-1:r-1:delete_order:tc-1"
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended($1,0))")).
		WithArgs(key).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT result FROM agent_tool_effects").
		WithArgs(key, "u-1").WillReturnError(errors.New("query failure"))
	// The store must distinguish no row from operational query failures.
	if _, _, err := backend.Orders.DeleteContext(ctx, "u-1", "o-1", key); err == nil {
		t.Fatal("expected query failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOrderStoreDeleteReplay(t *testing.T) {
	backend, mock := mockBackend(t)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{UserID: "u-1"})
	key := "u-1:r-1:delete_order:tc-1"

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended($1,0))")).
		WithArgs(key).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT result FROM agent_tool_effects").
		WithArgs(key, "u-1").WillReturnRows(sqlmock.NewRows([]string{"result"}))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE agent_orders SET deleted_at=NOW(),updated_at=NOW()")).
		WithArgs("u-1", "o-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO agent_tool_effects").
		WithArgs(key, "u-1", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	deleted, replayed, err := backend.Orders.DeleteContext(ctx, "u-1", "o-1", key)
	if err != nil || !deleted || replayed {
		t.Fatalf("first delete: deleted=%v replayed=%v err=%v", deleted, replayed, err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended($1,0))")).
		WithArgs(key).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT result FROM agent_tool_effects").
		WithArgs(key, "u-1").WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow([]byte(`{"deleted":true}`)))
	mock.ExpectCommit()
	deleted, replayed, err = backend.Orders.DeleteContext(ctx, "u-1", "o-1", key)
	if err != nil || !deleted || !replayed {
		t.Fatalf("replayed delete: deleted=%v replayed=%v err=%v", deleted, replayed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEmailStoreRecordIsIdempotent(t *testing.T) {
	backend, mock := mockBackend(t)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{UserID: "u-1"})
	record := tools.SentEmailRecord{UserID: "u-1", To: "a@example.com", Subject: "hello", Body: "body", RunID: "r-1", SentAt: time.Now()}
	key := "u-1:r-1:send_email:tc-1"
	for _, rows := range []int64{1, 0} {
		mock.ExpectExec("INSERT INTO agent_email_records").
			WithArgs(key, record.UserID, record.To, record.Subject, record.Body, record.RunID, record.SentAt).
			WillReturnResult(sqlmock.NewResult(1, rows))
		created, err := backend.Emails.RecordContext(ctx, record, key)
		if err != nil || created != (rows == 1) {
			t.Fatalf("record rows=%d created=%v err=%v", rows, created, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedInitialMigrationContainsAllDurableTables(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 3 || migrations[0].Version != 1 || migrations[0].Name != "initial" || migrations[1].Version != 2 || migrations[2].Version != 3 || migrations[2].Name != "run_events" {
		t.Fatalf("unexpected migrations: %+v", migrations)
	}
	for _, table := range []string{
		"agent_users", "agent_sessions", "agent_threads", "agent_checkpoints", "agent_memories",
		"agent_approvals", "agent_orders", "agent_email_records", "agent_tool_effects",
	} {
		if !regexp.MustCompile(`(?m)CREATE TABLE IF NOT EXISTS ` + table + `\b`).MatchString(migrations[0].SQL) {
			t.Errorf("initial migration does not create %s", table)
		}
	}
}

func expectMigrationLock(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_lock(hashtextextended($1,0))")).
		WithArgs(migrationLockName).WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectMigrationUnlock(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock(hashtextextended($1,0))")).
		WithArgs(migrationLockName).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestMigrateAppliesAndRecordsPendingVersion(t *testing.T) {
	backend, mock := mockBackend(t)
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations[0]

	expectMigrationLock(mock)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS agent_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT version,name,checksum FROM agent_schema_migrations ORDER BY version").
		WillReturnRows(sqlmock.NewRows([]string{"version", "name", "checksum"}))
	mock.ExpectBegin()
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS agent_users").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO agent_schema_migrations(version,name,checksum) VALUES($1,$2,$3)")).
		WithArgs(initial.Version, initial.Name, initial.Checksum).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("CREATE TABLE agent_login_rate_limits").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO agent_schema_migrations(version,name,checksum) VALUES($1,$2,$3)")).
		WithArgs(migrations[1].Version, migrations[1].Name, migrations[1].Checksum).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("CREATE TABLE agent_runs").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO agent_schema_migrations(version,name,checksum) VALUES($1,$2,$3)")).
		WithArgs(migrations[2].Version, migrations[2].Name, migrations[2].Checksum).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectMigrationUnlock(mock)

	if err := migrate(context.Background(), backend.DB); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateSkipsVerifiedVersion(t *testing.T) {
	backend, mock := mockBackend(t)
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations[0]

	expectMigrationLock(mock)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS agent_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT version,name,checksum FROM agent_schema_migrations ORDER BY version").
		WillReturnRows(sqlmock.NewRows([]string{"version", "name", "checksum"}).
			AddRow(initial.Version, initial.Name, initial.Checksum).
			AddRow(migrations[1].Version, migrations[1].Name, migrations[1].Checksum).
			AddRow(migrations[2].Version, migrations[2].Name, migrations[2].Checksum))
	expectMigrationUnlock(mock)

	if err := migrate(context.Background(), backend.DB); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateRejectsChangedOrFutureVersion(t *testing.T) {
	tests := []struct {
		name     string
		version  int64
		migName  string
		checksum string
		want     string
	}{
		{name: "changed", version: 1, migName: "initial", checksum: "changed", want: "integrity check failed"},
		{name: "future", version: 999, migName: "future", checksum: "checksum", want: "newer than this application"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, mock := mockBackend(t)
			expectMigrationLock(mock)
			mock.ExpectExec("CREATE TABLE IF NOT EXISTS agent_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery("SELECT version,name,checksum FROM agent_schema_migrations ORDER BY version").
				WillReturnRows(sqlmock.NewRows([]string{"version", "name", "checksum"}).
					AddRow(tt.version, tt.migName, tt.checksum))
			expectMigrationUnlock(mock)

			err := migrate(context.Background(), backend.DB)
			if err == nil || !regexp.MustCompile(tt.want).MatchString(err.Error()) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrateRollsBackFailedVersion(t *testing.T) {
	backend, mock := mockBackend(t)
	expectMigrationLock(mock)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS agent_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT version,name,checksum FROM agent_schema_migrations ORDER BY version").
		WillReturnRows(sqlmock.NewRows([]string{"version", "name", "checksum"}))
	mock.ExpectBegin()
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS agent_users").WillReturnError(errors.New("ddl failed"))
	mock.ExpectRollback()
	expectMigrationUnlock(mock)

	err := migrate(context.Background(), backend.DB)
	if err == nil || !regexp.MustCompile("apply migration 1").MatchString(err.Error()) {
		t.Fatalf("expected migration error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBackendReadyValidatesCompleteSchema(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations[0]
	tests := []struct {
		name    string
		rows    *sqlmock.Rows
		wantErr string
	}{
		{
			name: "ready",
			rows: sqlmock.NewRows([]string{"version", "name", "checksum"}).
				AddRow(initial.Version, initial.Name, initial.Checksum).
				AddRow(migrations[1].Version, migrations[1].Name, migrations[1].Checksum).
				AddRow(migrations[2].Version, migrations[2].Name, migrations[2].Checksum),
		},
		{
			name:    "missing migration",
			rows:    sqlmock.NewRows([]string{"version", "name", "checksum"}),
			wantErr: "migration 1 is not applied",
		},
		{
			name: "second migration missing",
			rows: sqlmock.NewRows([]string{"version", "name", "checksum"}).
				AddRow(initial.Version, initial.Name, initial.Checksum),
			wantErr: "migration 2 is not applied",
		},
		{
			name: "third migration missing",
			rows: sqlmock.NewRows([]string{"version", "name", "checksum"}).
				AddRow(initial.Version, initial.Name, initial.Checksum).
				AddRow(migrations[1].Version, migrations[1].Name, migrations[1].Checksum),
			wantErr: "migration 3 is not applied",
		},
		{
			name: "changed migration",
			rows: sqlmock.NewRows([]string{"version", "name", "checksum"}).
				AddRow(initial.Version, initial.Name, "wrong"),
			wantErr: "integrity check failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, mock := mockBackend(t)
			mock.ExpectQuery("SELECT version,name,checksum FROM agent_schema_migrations ORDER BY version").WillReturnRows(tt.rows)
			err := backend.Ready(context.Background())
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBackendReadyReportsDatabaseFailure(t *testing.T) {
	backend, mock := mockBackend(t)
	mock.ExpectQuery("SELECT version,name,checksum FROM agent_schema_migrations ORDER BY version").
		WillReturnError(errors.New("database unavailable"))
	err := backend.Ready(context.Background())
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("expected database error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
