package auth

import "context"

// SessionStore is the required interface for session persistence.
// At least one implementation (InMemorySessionStore) must be provided.
type SessionStore interface {
	Create(ctx context.Context, session Session) error
	Get(ctx context.Context, sessionID string) (Session, bool, error)
	Delete(ctx context.Context, sessionID string) error
	ListByUser(ctx context.Context, userID string) ([]Session, error)
}
