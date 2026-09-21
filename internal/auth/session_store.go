package auth

import "context"

// SessionStore is the required interface for session persistence.
// At least one implementation (InMemorySessionStore) must be provided.
type SessionStore interface {
	Create(ctx context.Context, session Session) error
	Get(ctx context.Context, sessionID string) (Session, bool, error)
	// Update replaces a stored session (used for sliding TTL renewal).
	Update(ctx context.Context, session Session) error
	Delete(ctx context.Context, sessionID string) error
	DeleteByUser(ctx context.Context, userID string) error
	ListByUser(ctx context.Context, userID string) ([]Session, error)
}
