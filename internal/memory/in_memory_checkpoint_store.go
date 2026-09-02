package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/example/agent-eino-demo/internal/auth"
)

// InMemoryCheckpointStore is the default in-memory implementation of CheckpointStore.
// Every method rejects calls whose userID (from the checkpoint key) does not
// match the authenticated identity in ctx (auth.CheckUserScope).
type InMemoryCheckpointStore struct {
	mu          sync.RWMutex
	checkpoints map[string]Checkpoint // key: userID:threadID:runID:step
}

// NewInMemoryCheckpointStore creates a new InMemoryCheckpointStore.
func NewInMemoryCheckpointStore() *InMemoryCheckpointStore {
	return &InMemoryCheckpointStore{
		checkpoints: make(map[string]Checkpoint),
	}
}

func checkpointKey(k CheckpointKey) string {
	return fmt.Sprintf("%s:%s:%s:%d", k.UserID, k.ThreadID, k.RunID, k.Step)
}

// Save stores a checkpoint. If a checkpoint with the same key already exists,
// it is updated (the Step field prevents accidental overwrites of different steps).
func (s *InMemoryCheckpointStore) Save(ctx context.Context, checkpoint Checkpoint) error {
	if err := auth.CheckUserScope(ctx, checkpoint.UserID); err != nil {
		return err
	}

	key := checkpointKey(CheckpointKey{
		UserID:   checkpoint.UserID,
		ThreadID: checkpoint.ThreadID,
		RunID:    checkpoint.RunID,
		Step:     checkpoint.Step,
	})

	s.mu.Lock()
	defer s.mu.Unlock()

	// If a checkpoint with this exact key already exists, update it
	if existing, ok := s.checkpoints[key]; ok {
		checkpoint.CreatedAt = existing.CreatedAt // preserve original creation time
	}

	s.checkpoints[key] = checkpoint
	return nil
}

// Load retrieves a checkpoint by key.
func (s *InMemoryCheckpointStore) Load(ctx context.Context, key CheckpointKey) (Checkpoint, bool, error) {
	if err := auth.CheckUserScope(ctx, key.UserID); err != nil {
		return Checkpoint{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	cp, ok := s.checkpoints[checkpointKey(key)]
	return cp, ok, nil
}

// Delete removes a checkpoint by key.
func (s *InMemoryCheckpointStore) Delete(ctx context.Context, key CheckpointKey) error {
	if err := auth.CheckUserScope(ctx, key.UserID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.checkpoints, checkpointKey(key))
	return nil
}

// ListByThread returns all checkpoints for a user+thread, sorted by CreatedAt descending (newest first).
func (s *InMemoryCheckpointStore) ListByThread(ctx context.Context, userID, threadID string) ([]Checkpoint, error) {
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Checkpoint
	for _, cp := range s.checkpoints {
		if cp.UserID == userID && cp.ThreadID == threadID {
			result = append(result, cp)
		}
	}

	// Sort by CreatedAt descending (newest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	return result, nil
}

// Snapshot returns all stored checkpoints (for persistence decorators).
func (s *InMemoryCheckpointStore) Snapshot() []Checkpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Checkpoint, 0, len(s.checkpoints))
	for _, cp := range s.checkpoints {
		result = append(result, cp)
	}
	return result
}

// Restore replaces all stored checkpoints (used by persistence decorators
// to reload state from disk at startup).
func (s *InMemoryCheckpointStore) Restore(checkpoints []Checkpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.checkpoints = make(map[string]Checkpoint, len(checkpoints))
	for _, cp := range checkpoints {
		key := checkpointKey(CheckpointKey{
			UserID:   cp.UserID,
			ThreadID: cp.ThreadID,
			RunID:    cp.RunID,
			Step:     cp.Step,
		})
		s.checkpoints[key] = cp
	}
}
