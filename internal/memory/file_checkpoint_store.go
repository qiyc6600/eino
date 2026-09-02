package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/example/agent-eino-demo/internal/storage"
)

// FileCheckpointStore decorates InMemoryCheckpointStore with JSON file
// persistence. It implements the same CheckpointStore interface, so it can
// replace the in-memory backend without any business-code change. Interrupted
// run states survive process restarts, enabling cross-process resume.
type FileCheckpointStore struct {
	*InMemoryCheckpointStore

	persistMu sync.Mutex // serializes file writes
	path      string
}

// NewFileCheckpointStore creates a FileCheckpointStore backed by the given
// JSON file. Existing state (if any) is loaded; a missing file starts empty.
func NewFileCheckpointStore(path string) (*FileCheckpointStore, error) {
	store := &FileCheckpointStore{
		InMemoryCheckpointStore: NewInMemoryCheckpointStore(),
		path:                    path,
	}

	var checkpoints []Checkpoint
	if err := storage.LoadJSONFile(path, &checkpoints); err != nil {
		return nil, fmt.Errorf("failed to load checkpoint store from %s: %w", path, err)
	}
	store.InMemoryCheckpointStore.Restore(checkpoints)
	return store, nil
}

func (f *FileCheckpointStore) Save(ctx context.Context, checkpoint Checkpoint) error {
	if err := f.InMemoryCheckpointStore.Save(ctx, checkpoint); err != nil {
		return err
	}
	return f.persist()
}

func (f *FileCheckpointStore) Delete(ctx context.Context, key CheckpointKey) error {
	if err := f.InMemoryCheckpointStore.Delete(ctx, key); err != nil {
		return err
	}
	return f.persist()
}

// persist atomically writes the full checkpoint snapshot to disk.
func (f *FileCheckpointStore) persist() error {
	f.persistMu.Lock()
	defer f.persistMu.Unlock()
	return storage.WriteJSONFileAtomic(f.path, f.InMemoryCheckpointStore.Snapshot())
}

// Ensure FileCheckpointStore implements CheckpointStore.
var _ CheckpointStore = (*FileCheckpointStore)(nil)
