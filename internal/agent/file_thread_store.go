package agent

import (
	"fmt"
	"sync"

	"github.com/cloudwego/eino/schema"

	"github.com/example/agent-eino-demo/internal/storage"
)

// FileThreadStore decorates threadStore with JSON file persistence. It
// exposes the same behavior, so the Runner can swap it in without any code
// change: every mutation is flushed to disk and state is reloaded from the
// file at startup — conversation histories survive process restarts.
type FileThreadStore struct {
	*threadStore

	persistMu sync.Mutex // serializes file writes
	path      string
}

// NewFileThreadStore creates a FileThreadStore backed by the given JSON file.
// Existing state (if any) is loaded; a missing file starts empty.
func NewFileThreadStore(path string) (*FileThreadStore, error) {
	store := &FileThreadStore{
		threadStore: newThreadStore(),
		path:        path,
	}

	var snapshots []ThreadSnapshot
	if err := storage.LoadJSONFile(path, &snapshots); err != nil {
		return nil, fmt.Errorf("failed to load thread store from %s: %w", path, err)
	}
	store.threadStore.Restore(snapshots)
	return store, nil
}

// update stages a mutation, persists it, then publishes it to readers. A failed
// write leaves the in-memory state unchanged as well as returning the error.
func (f *FileThreadStore) update(fn func(*threadStore)) error {
	f.persistMu.Lock()
	defer f.persistMu.Unlock()
	staged := newThreadStore()
	staged.Restore(f.threadStore.Snapshot())
	fn(staged)
	snapshot := staged.Snapshot()
	if err := storage.WriteJSONFileAtomic(f.path, snapshot); err != nil {
		return err
	}
	f.threadStore.Restore(snapshot)
	return nil
}
func (f *FileThreadStore) Replace(userID, threadID string, msgs []*schema.Message) error {
	return f.update(func(s *threadStore) { s.Replace(userID, threadID, msgs) })
}
func (f *FileThreadStore) Append(userID, threadID string, msgs ...*schema.Message) error {
	return f.update(func(s *threadStore) { s.Append(userID, threadID, msgs...) })
}
func (f *FileThreadStore) Create(userID, threadID string) error {
	return f.update(func(s *threadStore) { s.Create(userID, threadID) })
}
func (f *FileThreadStore) Delete(userID, threadID string) (bool, error) {
	var deleted bool
	err := f.update(func(s *threadStore) { deleted, _ = s.Delete(userID, threadID) })
	if err != nil {
		return false, err
	}
	return deleted, nil
}

var _ ThreadStore = (*FileThreadStore)(nil)
