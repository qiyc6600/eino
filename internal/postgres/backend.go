// Package postgres provides PostgreSQL implementations of every durable store.
package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/tools"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Backend struct {
	DB          *sql.DB
	lockDB      *sql.DB
	Users       *UserStore
	Sessions    *SessionStore
	Threads     *ThreadStore
	Runs        *RunStore
	Checkpoints *CheckpointStore
	Memories    *MemoryStore
	Approvals   *ApprovalStore
	Orders      *OrderStore
	Emails      *EmailStore
	LoginLimits *LoginLimitStore
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func Open(ctx context.Context, dsn string, maxOpen, maxIdle int, maxLifetime time.Duration) (*Backend, error) {
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required for postgres storage")
	}
	db, err := openPool(dsn, maxOpen, maxIdle, maxLifetime)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	// Advisory locks pin one physical connection for a complete agent run.
	// Keep them on a separate pool so saturated locks cannot starve the SQL
	// operations performed while those locks are held.
	lockDB, err := openPool(dsn, maxOpen, maxIdle, maxLifetime)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := lockDB.PingContext(ctx); err != nil {
		lockDB.Close()
		db.Close()
		return nil, fmt.Errorf("connect postgres lock pool: %w", err)
	}
	return newBackend(db, lockDB), nil
}

func openPool(dsn string, maxOpen, maxIdle int, maxLifetime time.Duration) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if maxOpen > 0 {
		db.SetMaxOpenConns(maxOpen)
	}
	if maxIdle >= 0 {
		db.SetMaxIdleConns(maxIdle)
	}
	if maxLifetime > 0 {
		db.SetConnMaxLifetime(maxLifetime)
	}
	return db, nil
}

func newBackend(db, lockDB *sql.DB) *Backend {
	return &Backend{
		DB: db, lockDB: lockDB, Users: &UserStore{db: db}, Sessions: &SessionStore{db: db}, Threads: &ThreadStore{db: db, lockDB: lockDB}, Runs: &RunStore{db: db},
		Checkpoints: &CheckpointStore{db: db}, Memories: &MemoryStore{db: db},
		Approvals: &ApprovalStore{db: db}, Orders: &OrderStore{db: db}, Emails: &EmailStore{db: db}, LoginLimits: &LoginLimitStore{db: db},
	}
}

// NewBackend wires stores around an existing DB, primarily for tests and
// embedding. In this mode advisory locks share the supplied pool.
func NewBackend(db *sql.DB) *Backend { return newBackend(db, db) }

func (b *Backend) Close() error {
	if b.lockDB != nil && b.lockDB != b.DB {
		return errors.Join(b.lockDB.Close(), b.DB.Close())
	}
	return b.DB.Close()
}

// SessionStore -------------------------------------------------------------

type SessionStore struct{ db *sql.DB }

func (s *SessionStore) Create(ctx context.Context, session auth.Session) error {
	roles, err := json.Marshal(session.Roles)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_sessions(id,user_id,username,roles,created_at,expires_at) VALUES($1,$2,$3,$4::jsonb,$5,$6)`,
		session.ID, session.UserID, session.Username, roles, session.CreatedAt, session.ExpiresAt)
	return err
}

func scanSession(row interface{ Scan(...any) error }) (auth.Session, bool, error) {
	var session auth.Session
	var roles []byte
	if err := row.Scan(&session.ID, &session.UserID, &session.Username, &roles, &session.CreatedAt, &session.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.Session{}, false, nil
		}
		return auth.Session{}, false, err
	}
	if err := json.Unmarshal(roles, &session.Roles); err != nil {
		return auth.Session{}, false, err
	}
	return session, true, nil
}

func (s *SessionStore) Get(ctx context.Context, id string) (auth.Session, bool, error) {
	return scanSession(s.db.QueryRowContext(ctx, `SELECT id,user_id,username,roles,created_at,expires_at FROM agent_sessions WHERE id=$1`, id))
}

func (s *SessionStore) Update(ctx context.Context, session auth.Session) error {
	roles, err := json.Marshal(session.Roles)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET user_id=$2,username=$3,roles=$4::jsonb,created_at=$5,expires_at=$6 WHERE id=$1`,
		session.ID, session.UserID, session.Username, roles, session.CreatedAt, session.ExpiresAt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("session not found: %s", session.ID)
	}
	return err
}

func (s *SessionStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_sessions WHERE id=$1`, id)
	return err
}

func (s *SessionStore) DeleteByUser(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_sessions WHERE user_id=$1`, userID)
	return err
}

func (s *SessionStore) ListByUser(ctx context.Context, userID string) ([]auth.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,username,roles,created_at,expires_at FROM agent_sessions WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []auth.Session
	for rows.Next() {
		session, _, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, rows.Err()
}

// ThreadStore --------------------------------------------------------------

type ThreadStore struct {
	db     *sql.DB
	lockDB *sql.DB
}

func (s *ThreadStore) Copy(userID, threadID string) []*schema.Message {
	messages, _ := s.CopyContext(context.Background(), userID, threadID)
	return messages
}

func (s *ThreadStore) CopyContext(ctx context.Context, userID, threadID string) ([]*schema.Message, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT messages FROM agent_threads WHERE user_id=$1 AND thread_id=$2`, userID, threadID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var messages []*schema.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *ThreadStore) Replace(userID, threadID string, messages []*schema.Message) error {
	return s.ReplaceContext(context.Background(), userID, threadID, messages)
}

func (s *ThreadStore) ReplaceContext(ctx context.Context, userID, threadID string, messages []*schema.Message) error {
	return replaceThread(ctx, s.db, userID, threadID, messages)
}

func replaceThread(ctx context.Context, db sqlExecer, userID, threadID string, messages []*schema.Message) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO agent_threads(user_id,thread_id,messages,updated_at) VALUES($1,$2,$3::jsonb,NOW())
		ON CONFLICT(user_id,thread_id) DO UPDATE SET messages=EXCLUDED.messages,updated_at=NOW()`, userID, threadID, data)
	return err
}

func (s *ThreadStore) Append(userID, threadID string, messages ...*schema.Message) error {
	return s.AppendContext(context.Background(), userID, threadID, messages...)
}

func (s *ThreadStore) AppendContext(ctx context.Context, userID, threadID string, messages ...*schema.Message) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_threads(user_id,thread_id,messages,updated_at) VALUES($1,$2,$3::jsonb,NOW())
		ON CONFLICT(user_id,thread_id) DO UPDATE SET messages=agent_threads.messages || EXCLUDED.messages,updated_at=NOW()`, userID, threadID, data)
	return err
}

// AppendHistoryContext appends only when the stored history still has the
// expected length. The guard is evaluated inside the UPDATE, so the existing
// blob is never read: the write cost is proportional to the new messages rather
// than to the whole conversation.
func (s *ThreadStore) AppendHistoryContext(ctx context.Context, userID, threadID string, expectedLen int, messages []*schema.Message) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_threads
		SET messages = messages || $3::jsonb, updated_at = NOW()
		WHERE user_id=$1 AND thread_id=$2 AND jsonb_array_length(messages) = $4`,
		userID, threadID, data, expectedLen)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	if expectedLen != 0 {
		// The stored history is not the prefix we assumed.
		return agent.ErrThreadAppendMismatch
	}
	// First write for this thread. Insert only if it truly does not exist; a row
	// that appeared in between (or already existed) must not be appended to
	// blindly, so report a mismatch and let the caller replace instead.
	inserted, err := s.db.ExecContext(ctx, `INSERT INTO agent_threads(user_id,thread_id,messages,updated_at)
		VALUES($1,$2,$3::jsonb,NOW()) ON CONFLICT DO NOTHING`, userID, threadID, data)
	if err != nil {
		return err
	}
	n, err := inserted.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return agent.ErrThreadAppendMismatch
}

// PruneThreadsBefore deletes the user's threads last touched before the cutoff.
// Retention is computed from the existing updated_at column, so no schema change
// is needed; the (user_id, updated_at) index keeps the sweep cheap.
func (s *ThreadStore) PruneThreadsBefore(ctx context.Context, userID string, cutoff time.Time) (int, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM agent_threads WHERE user_id=$1 AND updated_at < $2`, userID, cutoff)
	if err != nil {
		return 0, err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(removed), nil
}

func (s *ThreadStore) Create(userID, threadID string) error {
	return s.CreateContext(context.Background(), userID, threadID)
}
func (s *ThreadStore) CreateContext(ctx context.Context, userID, threadID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_threads(user_id,thread_id,messages) VALUES($1,$2,'[]'::jsonb) ON CONFLICT DO NOTHING`, userID, threadID)
	return err
}

func (s *ThreadStore) Delete(userID, threadID string) (bool, error) {
	return s.DeleteContext(context.Background(), userID, threadID)
}

func (s *ThreadStore) DeleteContext(ctx context.Context, userID, threadID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM agent_threads WHERE user_id=$1 AND thread_id=$2`, userID, threadID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *ThreadStore) List(userID string) []string {
	threads, _ := s.ListContext(context.Background(), userID)
	return threads
}

func (s *ThreadStore) ListContext(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT thread_id FROM agent_threads WHERE user_id=$1 ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (s *ThreadStore) LockThread(ctx context.Context, userID, threadID string) (func(), error) {
	conn, err := s.lockDB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	// PostgreSQL text cannot contain NUL bytes. A JSON tuple keeps the two
	// components unambiguous without relying on a forbidden separator.
	keyData, _ := json.Marshal([]string{userID, threadID})
	key := string(keyData)
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
		conn.Close()
		return nil, err
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(releaseCtx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key)
		_ = conn.Close()
	}, nil
}

// CheckpointStore ----------------------------------------------------------

type CheckpointStore struct{ db *sql.DB }

func (s *CheckpointStore) Save(ctx context.Context, checkpoint memory.Checkpoint) error {
	return saveCheckpoint(ctx, s.db, checkpoint)
}

func saveCheckpoint(ctx context.Context, db sqlExecer, checkpoint memory.Checkpoint) error {
	if err := auth.CheckUserScope(ctx, checkpoint.UserID); err != nil {
		return err
	}
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO agent_checkpoints(user_id,thread_id,run_id,step,data,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5::jsonb,$6,$7) ON CONFLICT(user_id,thread_id,run_id,step)
		DO UPDATE SET data=EXCLUDED.data,updated_at=EXCLUDED.updated_at`, checkpoint.UserID, checkpoint.ThreadID,
		checkpoint.RunID, checkpoint.Step, data, checkpoint.CreatedAt, checkpoint.UpdatedAt)
	return err
}

func scanCheckpoint(row interface{ Scan(...any) error }) (memory.Checkpoint, bool, error) {
	var data []byte
	if err := row.Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return memory.Checkpoint{}, false, nil
		}
		return memory.Checkpoint{}, false, err
	}
	var checkpoint memory.Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return memory.Checkpoint{}, false, err
	}
	return checkpoint, true, nil
}

func (s *CheckpointStore) Load(ctx context.Context, key memory.CheckpointKey) (memory.Checkpoint, bool, error) {
	if err := auth.CheckUserScope(ctx, key.UserID); err != nil {
		return memory.Checkpoint{}, false, err
	}
	return scanCheckpoint(s.db.QueryRowContext(ctx, `SELECT data FROM agent_checkpoints WHERE user_id=$1 AND thread_id=$2 AND run_id=$3 AND step=$4`,
		key.UserID, key.ThreadID, key.RunID, key.Step))
}

func (s *CheckpointStore) Delete(ctx context.Context, key memory.CheckpointKey) error {
	if err := auth.CheckUserScope(ctx, key.UserID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_checkpoints WHERE user_id=$1 AND thread_id=$2 AND run_id=$3 AND step=$4`,
		key.UserID, key.ThreadID, key.RunID, key.Step)
	return err
}

func (s *CheckpointStore) ListByThread(ctx context.Context, userID, threadID string) ([]memory.Checkpoint, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM agent_checkpoints WHERE user_id=$1 AND thread_id=$2 ORDER BY created_at DESC`, userID, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []memory.Checkpoint
	for rows.Next() {
		checkpoint, _, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, checkpoint)
	}
	return result, rows.Err()
}

// MemoryStore --------------------------------------------------------------

type MemoryStore struct{ db *sql.DB }

func (s *MemoryStore) Put(ctx context.Context, entry memory.MemoryEntry) error {
	if err := auth.CheckUserScope(ctx, entry.UserID); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_memories(user_id,memory_key,data,updated_at) VALUES($1,$2,$3::jsonb,NOW())
		ON CONFLICT(user_id,memory_key) DO UPDATE SET data=EXCLUDED.data,updated_at=NOW()`, entry.UserID, entry.Key, data)
	return err
}

func scanMemory(row interface{ Scan(...any) error }) (memory.MemoryEntry, bool, error) {
	var data []byte
	if err := row.Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return memory.MemoryEntry{}, false, nil
		}
		return memory.MemoryEntry{}, false, err
	}
	var entry memory.MemoryEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return memory.MemoryEntry{}, false, err
	}
	return entry, true, nil
}

func (s *MemoryStore) Get(ctx context.Context, userID, key string) (memory.MemoryEntry, bool, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return memory.MemoryEntry{}, false, err
	}
	return scanMemory(s.db.QueryRowContext(ctx, `SELECT data FROM agent_memories WHERE user_id=$1 AND memory_key=$2`, userID, key))
}

func (s *MemoryStore) List(ctx context.Context, userID string) ([]memory.MemoryEntry, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM agent_memories WHERE user_id=$1 ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []memory.MemoryEntry
	for rows.Next() {
		entry, _, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (s *MemoryStore) Delete(ctx context.Context, userID, key string) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_memories WHERE user_id=$1 AND memory_key=$2`, userID, key)
	return err
}

// ApprovalStore ------------------------------------------------------------

type ApprovalStore struct{ db *sql.DB }

func approvalData(req *hitl.ApprovalRequest) ([]byte, error) { return json.Marshal(req) }

func (s *ApprovalStore) Save(ctx context.Context, req *hitl.ApprovalRequest) error {
	return writeApproval(ctx, s.db, req, false)
}

func writeApproval(ctx context.Context, db sqlExecer, req *hitl.ApprovalRequest, newOnly bool) error {
	if err := auth.CheckUserScope(ctx, req.UserID); err != nil {
		return err
	}
	data, err := approvalData(req)
	if err != nil {
		return err
	}
	var approved any
	var reason string
	if req.Decision != nil {
		approved, reason = req.Decision.Approved, req.Decision.Reason
	}
	query := `INSERT INTO agent_approvals(interrupt_id,user_id,thread_id,run_id,status,phase,approved,reason,claim_token,data,created_at,decided_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,NOW())`
	if !newOnly {
		query += ` ON CONFLICT(interrupt_id) DO UPDATE SET
			data=EXCLUDED.data,status=EXCLUDED.status,phase=EXCLUDED.phase,approved=EXCLUDED.approved,reason=EXCLUDED.reason,
			claim_token=EXCLUDED.claim_token,decided_at=EXCLUDED.decided_at,updated_at=NOW()`
	}
	_, err = db.ExecContext(ctx, query, req.InterruptID, req.UserID,
		req.ThreadID, req.RunID, req.Status, req.Phase, approved, reason, req.ClaimToken, data, req.CreatedAt, req.DecidedAt)
	return err
}

// PublishInterrupt commits the checkpoint, conversation and approval together.
func (b *Backend) PublishInterrupt(ctx context.Context, publication agent.InterruptPublication) error {
	if err := validateInterruptPublication(ctx, &publication); err != nil {
		return err
	}
	cp, req := publication.Checkpoint, publication.Approval
	tx, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveCheckpoint(ctx, tx, cp); err != nil {
		return fmt.Errorf("save interrupt checkpoint: %w", err)
	}
	if err := replaceThread(ctx, tx, cp.UserID, cp.ThreadID, publication.Messages); err != nil {
		return fmt.Errorf("save interrupt thread: %w", err)
	}
	if err := writeApproval(ctx, tx, req, true); err != nil {
		return fmt.Errorf("save interrupt approval: %w", err)
	}
	return tx.Commit()
}

func validateInterruptPublication(ctx context.Context, publication *agent.InterruptPublication) error {
	cp, req := publication.Checkpoint, publication.Approval
	if req == nil || req.InterruptID == "" || req.Phase != "" || cp.UserID == "" || cp.ThreadID == "" || cp.RunID == "" ||
		cp.UserID != req.UserID || cp.ThreadID != req.ThreadID || cp.RunID != req.RunID ||
		!bytes.Equal(cp.State, req.State) || len(cp.State) == 0 || !cp.Interrupted || req.Status != hitl.StatusPending {
		return fmt.Errorf("inconsistent interrupt publication")
	}
	return auth.CheckUserScope(ctx, cp.UserID)
}

var _ agent.InterruptPublisher = (*Backend)(nil)

func scanApproval(row interface{ Scan(...any) error }) (*hitl.ApprovalRequest, bool, error) {
	var data []byte
	var status, phase, claimToken, reason string
	var approved sql.NullBool
	var decidedAt sql.NullTime
	if err := row.Scan(&data, &status, &phase, &approved, &reason, &claimToken, &decidedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var req hitl.ApprovalRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, false, err
	}
	req.Status, req.Phase, req.ClaimToken = hitl.ApprovalStatus(status), phase, claimToken
	if approved.Valid {
		req.Decision = &hitl.ApprovalDecision{Approved: approved.Bool, Reason: reason}
	}
	if decidedAt.Valid {
		t := decidedAt.Time
		req.DecidedAt = &t
	}
	return &req, true, nil
}

const approvalColumns = `data,status,phase,approved,reason,claim_token,decided_at`

func (s *ApprovalStore) Get(ctx context.Context, id string) (*hitl.ApprovalRequest, bool, error) {
	return scanApproval(s.db.QueryRowContext(ctx, `SELECT `+approvalColumns+` FROM agent_approvals WHERE interrupt_id=$1`, id))
}

func (s *ApprovalStore) ListPending(ctx context.Context, userID string) ([]*hitl.ApprovalRequest, error) {
	query := `SELECT ` + approvalColumns + ` FROM agent_approvals WHERE status='pending'`
	var args []any
	if userID != "" {
		query += ` AND user_id=$1`
		args = append(args, userID)
	}
	query += ` ORDER BY created_at`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*hitl.ApprovalRequest
	for rows.Next() {
		req, _, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, req)
	}
	return result, rows.Err()
}

func (s *ApprovalStore) Claim(ctx context.Context, id, userID string, decision hitl.ApprovalDecision, token string) (*hitl.ApprovalRequest, bool, error) {
	status := hitl.StatusRejected
	if decision.Approved {
		status = hitl.StatusApproved
	}
	row := s.db.QueryRowContext(ctx, `UPDATE agent_approvals SET status=$3,phase='running',approved=$4,reason=$5,claim_token=$6,decided_at=NOW(),updated_at=NOW()
		WHERE interrupt_id=$1 AND user_id=$2 AND status='pending' AND phase='' RETURNING `+approvalColumns,
		id, userID, status, decision.Approved, decision.Reason, token)
	req, ok, err := scanApproval(row)
	if err != nil {
		return nil, false, err
	}
	if ok {
		return req, true, nil
	}
	req, ok, err = s.Get(ctx, id)
	if err != nil || !ok || req.UserID != userID {
		return nil, false, err
	}
	return req, false, nil
}

func (s *ApprovalStore) Complete(ctx context.Context, req *hitl.ApprovalRequest) error {
	return completeApproval(ctx, s.db, req)
}

func completeApproval(ctx context.Context, db sqlExecer, req *hitl.ApprovalRequest) error {
	if err := auth.CheckUserScope(ctx, req.UserID); err != nil {
		return err
	}
	data, err := approvalData(req)
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `UPDATE agent_approvals SET data=$3::jsonb,status=$4,phase='finished',updated_at=NOW()
		WHERE interrupt_id=$1 AND phase='running' AND claim_token=$2`, req.InterruptID, req.ClaimToken, data, req.Status)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return fmt.Errorf("approval claim no longer owned: %s", req.InterruptID)
	}
	return err
}

var (
	_ auth.SessionStore             = (*SessionStore)(nil)
	_ agent.ThreadStore             = (*ThreadStore)(nil)
	_ agent.ContextThreadStore      = (*ThreadStore)(nil)
	_ agent.ContextThreadMutator    = (*ThreadStore)(nil)
	_ agent.DistributedThreadLocker = (*ThreadStore)(nil)
	_ memory.CheckpointStore        = (*CheckpointStore)(nil)
	_ memory.MemoryStore            = (*MemoryStore)(nil)
	_ hitl.ApprovalStore            = (*ApprovalStore)(nil)
	_ tools.OrderRepository         = (*OrderStore)(nil)
	_ tools.EmailRepository         = (*EmailStore)(nil)
)
