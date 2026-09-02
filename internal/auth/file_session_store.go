package auth

import (
	"context"
	"fmt"
	"sync"

	"github.com/example/agent-eino-demo/internal/storage"
)

// FileSessionStore decorates InMemorySessionStore with JSON file persistence.
// It implements the same SessionStore interface, so it can replace the
// in-memory backend without any business-code change: every mutation is
// flushed to disk, and state is reloaded from the file at startup.
type FileSessionStore struct {
	*InMemorySessionStore

	persistMu sync.Mutex // serializes file writes
	path      string
}

// NewFileSessionStore creates a FileSessionStore backed by the given JSON
// file. Existing state (if any) is loaded; a missing file starts empty.
func NewFileSessionStore(path string) (*FileSessionStore, error) {
	store := &FileSessionStore{
		InMemorySessionStore: NewInMemorySessionStore(),
		path:                 path,
	}

	var sessions []Session
	if err := storage.LoadJSONFile(path, &sessions); err != nil {
		return nil, fmt.Errorf("failed to load session store from %s: %w", path, err)
	}
	store.InMemorySessionStore.Restore(sessions)
	return store, nil
}

func (f *FileSessionStore) Create(ctx context.Context, session Session) error {
	if err := f.InMemorySessionStore.Create(ctx, session); err != nil {
		return err
	}
	return f.persist()
}

func (f *FileSessionStore) Update(ctx context.Context, session Session) error {
	if err := f.InMemorySessionStore.Update(ctx, session); err != nil {
		return err
	}
	return f.persist()
}

func (f *FileSessionStore) Delete(ctx context.Context, sessionID string) error {
	if err := f.InMemorySessionStore.Delete(ctx, sessionID); err != nil {
		return err
	}
	return f.persist()
}

// persist atomically writes the full session snapshot to disk.
func (f *FileSessionStore) persist() error {
	f.persistMu.Lock()
	defer f.persistMu.Unlock()
	return storage.WriteJSONFileAtomic(f.path, f.InMemorySessionStore.Snapshot())
}

// Ensure FileSessionStore implements SessionStore.
var _ SessionStore = (*FileSessionStore)(nil)
