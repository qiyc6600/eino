package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/example/agent-eino-demo/internal/storage"
)

// FileMemoryStore decorates InMemoryMemoryStore with JSON file persistence.
// It implements the same MemoryStore interface, so it can replace the
// in-memory backend without any business-code change. Because the in-memory
// implementation is embedded, the store-level cross-user scope check
// (auth.CheckUserScope) is inherited unchanged.
type FileMemoryStore struct {
	*InMemoryMemoryStore

	persistMu sync.Mutex // serializes file writes
	path      string
}

// NewFileMemoryStore creates a FileMemoryStore backed by the given JSON file.
// Existing state (if any) is loaded; a missing file starts empty.
func NewFileMemoryStore(path string) (*FileMemoryStore, error) {
	store := &FileMemoryStore{
		InMemoryMemoryStore: NewInMemoryMemoryStore(),
		path:                path,
	}

	var entries []MemoryEntry
	if err := storage.LoadJSONFile(path, &entries); err != nil {
		return nil, fmt.Errorf("failed to load memory store from %s: %w", path, err)
	}
	store.InMemoryMemoryStore.Restore(entries)
	return store, nil
}

func (f *FileMemoryStore) Put(ctx context.Context, entry MemoryEntry) error {
	if err := f.InMemoryMemoryStore.Put(ctx, entry); err != nil {
		return err
	}
	return f.persist()
}

func (f *FileMemoryStore) Delete(ctx context.Context, userID, key string) error {
	if err := f.InMemoryMemoryStore.Delete(ctx, userID, key); err != nil {
		return err
	}
	return f.persist()
}

// persist atomically writes the full entry snapshot to disk.
func (f *FileMemoryStore) persist() error {
	f.persistMu.Lock()
	defer f.persistMu.Unlock()
	return storage.WriteJSONFileAtomic(f.path, f.InMemoryMemoryStore.Snapshot())
}

// Ensure FileMemoryStore implements MemoryStore.
var _ MemoryStore = (*FileMemoryStore)(nil)
