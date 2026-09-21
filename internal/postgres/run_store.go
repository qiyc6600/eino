package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/auth"
)

// RunStore keeps the latest result of each run until its configured expiry.
type RunStore struct{ db *sql.DB }

func (s *RunStore) Save(ctx context.Context, userID string, result agent.ChatRunResult, retention time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeRun(ctx, tx, userID, result, retention); err != nil {
		return err
	}
	return tx.Commit()
}

func writeRun(ctx context.Context, db sqlExecer, userID string, result agent.ChatRunResult, retention time.Duration) error {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return err
	}
	if userID == "" || result.RunID == "" || retention <= 0 {
		return fmt.Errorf("invalid run storage parameters")
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_runs(user_id,run_id,result,updated_at,expires_at)
		VALUES($1,$2,$3::jsonb,NOW(),NOW()+($4 * INTERVAL '1 second'))
		ON CONFLICT(user_id,run_id) DO UPDATE SET result=EXCLUDED.result,updated_at=NOW(),expires_at=EXCLUDED.expires_at`,
		userID, result.RunID, data, retention.Seconds()); err != nil {
		return err
	}
	// Expired rows are removed on subsequent writes, with the expiry index
	// keeping this cheap even when historical runs have accumulated.
	if _, err := db.ExecContext(ctx, `DELETE FROM agent_runs WHERE expires_at <= NOW()`); err != nil {
		return err
	}
	return nil
}

func (s *RunStore) Load(ctx context.Context, userID, runID string) (*agent.ChatRunResult, bool, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, false, err
	}
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT result FROM agent_runs WHERE user_id=$1 AND run_id=$2 AND expires_at > NOW()`, userID, runID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var result agent.ChatRunResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false, err
	}
	return &result, true, nil
}

var _ agent.RunStore = (*RunStore)(nil)
