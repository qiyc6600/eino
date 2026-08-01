package memory

import "context"

// MemoryStore is the interface for long-term memory persistence.
// Must be separate from CheckpointStore (short-term memory).
// Data model and lifecycle are fundamentally different:
// - Short-term memory = state snapshot (CheckpointStore)
// - Long-term memory = fact/preference entries (MemoryStore)
type MemoryStore interface {
	Put(ctx context.Context, entry MemoryEntry) error
	Get(ctx context.Context, userID, key string) (MemoryEntry, bool, error)
	List(ctx context.Context, userID string) ([]MemoryEntry, error)
	Delete(ctx context.Context, userID, key string) error
}

// MemoryEntry represents a single long-term memory fact/preference.
type MemoryEntry struct {
	UserID    string `json:"user_id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Source    string `json:"source"` // e.g., "user_stated", "extracted"
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
