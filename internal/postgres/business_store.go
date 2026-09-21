package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/tools"
	"github.com/google/uuid"
)

type OrderStore struct{ db *sql.DB }

// Seed inserts demo orders once. Deleted rows use a tombstone, so restarting
// the application never resurrects an order that was already deleted.
func (s *OrderStore) Seed(ctx context.Context, orders []tools.Order) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, order := range orders {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_orders(user_id,order_id,status,amount,description)
			VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,order_id) DO NOTHING`,
			order.UserID, order.ID, order.Status, order.Amount, order.Desc); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *OrderStore) QueryByUserContext(ctx context.Context, userID string) ([]tools.Order, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT order_id,user_id,status,amount,description FROM agent_orders
		WHERE user_id=$1 AND deleted_at IS NULL ORDER BY order_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []tools.Order
	for rows.Next() {
		var order tools.Order
		if err := rows.Scan(&order.ID, &order.UserID, &order.Status, &order.Amount, &order.Desc); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

type deleteEffect struct {
	Deleted bool `json:"deleted"`
}

func (s *OrderStore) DeleteContext(ctx context.Context, userID, orderID, idempotencyKey string) (bool, bool, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return false, false, err
	}
	if idempotencyKey == "" {
		result, err := s.db.ExecContext(ctx, `UPDATE agent_orders SET deleted_at=NOW(),updated_at=NOW()
			WHERE user_id=$1 AND order_id=$2 AND deleted_at IS NULL`, userID, orderID)
		if err != nil {
			return false, false, err
		}
		n, err := result.RowsAffected()
		return n == 1, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, idempotencyKey); err != nil {
		return false, false, err
	}
	var saved []byte
	err = tx.QueryRowContext(ctx, `SELECT result FROM agent_tool_effects WHERE idempotency_key=$1 AND user_id=$2 AND tool_name='delete_order'`,
		idempotencyKey, userID).Scan(&saved)
	if err == nil {
		var effect deleteEffect
		if err := json.Unmarshal(saved, &effect); err != nil {
			return false, false, err
		}
		if err := tx.Commit(); err != nil {
			return false, false, err
		}
		return effect.Deleted, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE agent_orders SET deleted_at=NOW(),updated_at=NOW()
		WHERE user_id=$1 AND order_id=$2 AND deleted_at IS NULL`, userID, orderID)
	if err != nil {
		return false, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, false, err
	}
	deleted := n == 1
	data, _ := json.Marshal(deleteEffect{Deleted: deleted})
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_tool_effects(idempotency_key,user_id,tool_name,result)
		VALUES($1,$2,'delete_order',$3::jsonb)`, idempotencyKey, userID, data); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return deleted, false, nil
}

type EmailStore struct{ db *sql.DB }

func (s *EmailStore) RecordContext(ctx context.Context, rec tools.SentEmailRecord, idempotencyKey string) (bool, error) {
	if err := auth.CheckUserScope(ctx, rec.UserID); err != nil {
		return false, err
	}
	if idempotencyKey == "" {
		idempotencyKey = "email:" + uuid.NewString()
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO agent_email_records(idempotency_key,user_id,recipient,subject,body,run_id,sent_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(idempotency_key) DO NOTHING`,
		idempotencyKey, rec.UserID, rec.To, rec.Subject, rec.Body, rec.RunID, rec.SentAt)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *EmailStore) ListByUserContext(ctx context.Context, userID string) ([]tools.SentEmailRecord, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT recipient,subject,body,user_id,sent_at,run_id FROM agent_email_records
		WHERE user_id=$1 ORDER BY sent_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []tools.SentEmailRecord
	for rows.Next() {
		var rec tools.SentEmailRecord
		if err := rows.Scan(&rec.To, &rec.Subject, &rec.Body, &rec.UserID, &rec.SentAt, &rec.RunID); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}
