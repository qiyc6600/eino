package auth

import (
	"context"
	"fmt"
	"sync"
)

// InMemorySessionStore is the default in-memory implementation of SessionStore.
type InMemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session   // sessionID -> Session
	byUser   map[string][]Session // userID -> sessions
}

// NewInMemorySessionStore creates a new InMemorySessionStore.
func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		sessions: make(map[string]Session),
		byUser:   make(map[string][]Session),
	}
}

func (s *InMemorySessionStore) Create(ctx context.Context, session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[session.ID] = session
	s.byUser[session.UserID] = append(s.byUser[session.UserID], session)
	return nil
}

func (s *InMemorySessionStore) Get(ctx context.Context, sessionID string) (Session, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionID]
	return session, ok, nil
}

// Update replaces a stored session in place (sliding TTL renewal).
// Returns an error when the session does not exist.
func (s *InMemorySessionStore) Update(ctx context.Context, session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[session.ID]; !ok {
		return fmt.Errorf("session not found: %s", session.ID)
	}
	s.sessions[session.ID] = session
	sessions := s.byUser[session.UserID]
	for i := range sessions {
		if sessions[i].ID == session.ID {
			sessions[i] = session
			break
		}
	}
	return nil
}

func (s *InMemorySessionStore) Delete(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	delete(s.sessions, sessionID)

	sessions := s.byUser[session.UserID]
	for i, ss := range sessions {
		if ss.ID == sessionID {
			s.byUser[session.UserID] = append(sessions[:i], sessions[i+1:]...)
			break
		}
	}
	return nil
}

func (s *InMemorySessionStore) DeleteByUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range s.byUser[userID] {
		delete(s.sessions, session.ID)
	}
	delete(s.byUser, userID)
	return nil
}

func (s *InMemorySessionStore) ListByUser(ctx context.Context, userID string) ([]Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := s.byUser[userID]
	result := make([]Session, len(sessions))
	copy(result, sessions)
	return result, nil
}

// Snapshot returns all stored sessions (for persistence decorators).
func (s *InMemorySessionStore) Snapshot() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, session)
	}
	return result
}

// Restore replaces all stored sessions (used by persistence decorators
// to reload state from disk at startup).
func (s *InMemorySessionStore) Restore(sessions []Session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions = make(map[string]Session, len(sessions))
	s.byUser = make(map[string][]Session)
	for _, session := range sessions {
		s.sessions[session.ID] = session
		s.byUser[session.UserID] = append(s.byUser[session.UserID], session)
	}
}
