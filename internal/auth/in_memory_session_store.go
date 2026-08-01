package auth

import (
	"context"
	"sync"
)

// InMemorySessionStore is the default in-memory implementation of SessionStore.
type InMemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session     // sessionID -> Session
	byUser   map[string][]Session   // userID -> sessions
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

func (s *InMemorySessionStore) ListByUser(ctx context.Context, userID string) ([]Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := s.byUser[userID]
	result := make([]Session, len(sessions))
	copy(result, sessions)
	return result, nil
}
