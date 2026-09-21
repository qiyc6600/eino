package agent

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// threadRecord is a conversation thread bound to its owning user.
type threadRecord struct {
	messages []*schema.Message
}

// threadKey namespaces a thread by owner and client-side thread ID.
// The same threadID under different users maps to different threads, so
// the shared "t_default" default never collides across users.
type threadKey struct {
	userID   string
	threadID string
}

// ThreadStore is the storage surface the Runner needs for conversation
// threads. The in-memory threadStore is the default; FileThreadStore is the
// persisted decorator.
type ThreadStore interface {
	Copy(userID, threadID string) []*schema.Message
	Replace(userID, threadID string, msgs []*schema.Message) error
	Append(userID, threadID string, msgs ...*schema.Message) error
	Create(userID, threadID string) error
	Delete(userID, threadID string) (bool, error)
	List(userID string) []string
}

// ContextThreadStore preserves database errors and request cancellation.
type ContextThreadStore interface {
	CopyContext(ctx context.Context, userID, threadID string) ([]*schema.Message, error)
	ListContext(ctx context.Context, userID string) ([]string, error)
}

// ContextThreadMutator lets database stores carry cancellation through writes.
type ContextThreadMutator interface {
	ReplaceContext(ctx context.Context, userID, threadID string, msgs []*schema.Message) error
	AppendContext(ctx context.Context, userID, threadID string, msgs ...*schema.Message) error
	CreateContext(ctx context.Context, userID, threadID string) error
	DeleteContext(ctx context.Context, userID, threadID string) (bool, error)
}

// DistributedThreadLocker serializes one conversation across service
// instances. PostgreSQL implements this with a session advisory lock.
type DistributedThreadLocker interface {
	LockThread(ctx context.Context, userID, threadID string) (func(), error)
}

// threadStore stores conversation threads, strictly namespaced by userID.
// Every method only ever touches the calling user's namespace, which makes
// cross-user reads and deletes impossible by construction — isolation is
// enforced by the store, not by caller discipline.
type threadStore struct {
	mu      sync.RWMutex
	threads map[threadKey]*threadRecord
}

func newThreadStore() *threadStore {
	return &threadStore{threads: make(map[threadKey]*threadRecord)}
}

// Ensure the in-memory implementation satisfies the interface.
var _ ThreadStore = (*threadStore)(nil)

func (s *threadStore) record(userID, threadID string) *threadRecord {
	key := threadKey{userID: userID, threadID: threadID}
	rec, ok := s.threads[key]
	if !ok {
		rec = &threadRecord{}
		s.threads[key] = rec
	}
	return rec
}

// Copy returns a snapshot of the user's thread messages for local mutation.
// Returns nil when the thread does not exist yet.
func (s *threadStore) Copy(userID, threadID string) []*schema.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.threads[threadKey{userID: userID, threadID: threadID}]
	if !ok {
		return nil
	}
	result := make([]*schema.Message, len(rec.messages))
	copy(result, rec.messages)
	return result
}

// Replace atomically get-or-creates the user's thread and stores msgs.
func (s *threadStore) Replace(userID, threadID string, msgs []*schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(userID, threadID).messages = msgs
	return nil
}

// Append atomically get-or-creates the user's thread and appends msgs.
func (s *threadStore) Append(userID, threadID string, msgs ...*schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.record(userID, threadID)
	rec.messages = append(rec.messages, msgs...)
	return nil
}

// Create idempotently creates an empty thread for the user.
func (s *threadStore) Create(userID, threadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(userID, threadID)
	return nil
}

// Delete removes the user's thread. Returns false when the user has no
// such thread (including when it exists only under a different user).
func (s *threadStore) Delete(userID, threadID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := threadKey{userID: userID, threadID: threadID}
	if _, ok := s.threads[key]; !ok {
		return false, nil
	}
	delete(s.threads, key)
	return true, nil
}

// List returns the thread IDs owned by the user.
func (s *threadStore) List(userID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.threads))
	for key := range s.threads {
		if key.userID == userID {
			ids = append(ids, key.threadID)
		}
	}
	return ids
}

// ThreadSnapshot is one persisted thread: owner + full message history.
type ThreadSnapshot struct {
	UserID   string            `json:"user_id"`
	ThreadID string            `json:"thread_id"`
	Messages []*schema.Message `json:"messages"`
}

// Snapshot returns all threads with their messages (for persistence decorators).
func (s *threadStore) Snapshot() []ThreadSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ThreadSnapshot, 0, len(s.threads))
	for key, rec := range s.threads {
		msgs := make([]*schema.Message, len(rec.messages))
		copy(msgs, rec.messages)
		result = append(result, ThreadSnapshot{
			UserID:   key.userID,
			ThreadID: key.threadID,
			Messages: msgs,
		})
	}
	return result
}

// Restore replaces all stored threads (used by persistence decorators to
// reload state from disk at startup).
func (s *threadStore) Restore(snapshots []ThreadSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.threads = make(map[threadKey]*threadRecord, len(snapshots))
	for _, snap := range snapshots {
		if snap.UserID == "" || snap.ThreadID == "" {
			continue
		}
		key := threadKey{userID: snap.UserID, threadID: snap.ThreadID}
		s.threads[key] = &threadRecord{messages: snap.Messages}
	}
}
