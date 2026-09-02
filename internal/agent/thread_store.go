package agent

import (
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
func (s *threadStore) Replace(userID, threadID string, msgs []*schema.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(userID, threadID).messages = msgs
}

// Append atomically get-or-creates the user's thread and appends msgs.
func (s *threadStore) Append(userID, threadID string, msgs ...*schema.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.record(userID, threadID)
	rec.messages = append(rec.messages, msgs...)
}

// Create idempotently creates an empty thread for the user.
func (s *threadStore) Create(userID, threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(userID, threadID)
}

// Delete removes the user's thread. Returns false when the user has no
// such thread (including when it exists only under a different user).
func (s *threadStore) Delete(userID, threadID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := threadKey{userID: userID, threadID: threadID}
	if _, ok := s.threads[key]; !ok {
		return false
	}
	delete(s.threads, key)
	return true
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
