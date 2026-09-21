package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/example/agent-eino-demo/internal/auth"
)

type LoginLimitStore struct{ db *sql.DB }

func (s *LoginLimitStore) LockedUntil(ctx context.Context, keys []string, now time.Time) (time.Time, error) {
	var latest time.Time
	for _, key := range keys {
		var locked sql.NullTime
		if err := s.db.QueryRowContext(ctx, `SELECT locked_until FROM agent_login_rate_limits WHERE limit_key=$1`, key).Scan(&locked); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return time.Time{}, err
		}
		if locked.Valid && locked.Time.After(now) && locked.Time.After(latest) {
			latest = locked.Time
		}
	}
	return latest, nil
}

func (s *LoginLimitStore) RecordFailure(ctx context.Context, keys []string, now time.Time, policy auth.LoginRatePolicy) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Keys are sorted by auth.loginLimitKeys. Transaction locks serialize
	// concurrent failures on every instance and prevent lost increments.
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "agent-login-limit:"+key); err != nil {
			return err
		}
	}
	for _, key := range keys {
		var state auth.LoginLimitState
		var locked sql.NullTime
		err := tx.QueryRowContext(ctx,
			`SELECT failure_count,window_started_at,locked_until FROM agent_login_rate_limits WHERE limit_key=$1`, key,
		).Scan(&state.FailureCount, &state.WindowStartedAt, &locked)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if locked.Valid {
			state.LockedUntil = locked.Time
		}
		state = auth.AdvanceLoginLimitState(state, now, policy)
		var lockValue any
		if !state.LockedUntil.IsZero() {
			lockValue = state.LockedUntil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO agent_login_rate_limits(limit_key,failure_count,window_started_at,locked_until)
			VALUES($1,$2,$3,$4) ON CONFLICT(limit_key) DO UPDATE SET
			failure_count=EXCLUDED.failure_count,window_started_at=EXCLUDED.window_started_at,locked_until=EXCLUDED.locked_until`,
			key, state.FailureCount, state.WindowStartedAt, lockValue,
		); err != nil {
			return fmt.Errorf("write login limit: %w", err)
		}
	}
	return tx.Commit()
}

func (s *LoginLimitStore) Reset(ctx context.Context, keys []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "agent-login-limit:"+key); err != nil {
			return err
		}
	}
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_login_rate_limits WHERE limit_key=$1`, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var _ auth.LoginLimitStore = (*LoginLimitStore)(nil)
