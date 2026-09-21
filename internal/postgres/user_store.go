package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/jackc/pgx/v5/pgconn"
)

// UserStore persists accounts shared by all application instances.
type UserStore struct{ db *sql.DB }

func (s *UserStore) Seed(ctx context.Context, users []auth.User) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, user := range users {
		roles, err := json.Marshal(user.Roles)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_users(id,username,password_hash,roles) VALUES($1,$2,$3,$4::jsonb) ON CONFLICT DO NOTHING`,
			user.ID, user.Username, user.PasswordHash, roles); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanUser(row interface{ Scan(...any) error }) (auth.User, bool, error) {
	var user auth.User
	var roles []byte
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &roles); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.User{}, false, nil
		}
		return auth.User{}, false, err
	}
	if err := json.Unmarshal(roles, &user.Roles); err != nil {
		return auth.User{}, false, err
	}
	return user, true, nil
}

const userColumns = `id,username,password_hash,roles`

func (s *UserStore) GetByUsername(ctx context.Context, username string) (auth.User, bool, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM agent_users WHERE username=$1`, username))
}

func (s *UserStore) GetByID(ctx context.Context, userID string) (auth.User, bool, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM agent_users WHERE id=$1`, userID))
}

func (s *UserStore) List(ctx context.Context) ([]auth.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM agent_users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []auth.User
	for rows.Next() {
		user, _, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *UserStore) Create(ctx context.Context, user auth.User) error {
	roles, err := json.Marshal(user.Roles)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_users(id,username,password_hash,roles) VALUES($1,$2,$3,$4::jsonb)`,
		user.ID, user.Username, user.PasswordHash, roles)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: %s", auth.ErrUserExists, user.Username)
	}
	return err
}

func (s *UserStore) UpdateRoles(ctx context.Context, userID string, roles []string) error {
	data, err := json.Marshal(roles)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_users SET roles=$2::jsonb,updated_at=NOW() WHERE id=$1`, userID, data)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("%w: %s", auth.ErrUserNotFound, userID)
	}
	return err
}

func (s *UserStore) UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE agent_users SET password_hash=$2,updated_at=NOW() WHERE id=$1`, userID, passwordHash)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("%w: %s", auth.ErrUserNotFound, userID)
	}
	return err
}

var _ auth.UserStore = (*UserStore)(nil)
