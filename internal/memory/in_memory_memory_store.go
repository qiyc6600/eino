package memory

import (
	"context"
	"sync"
)

// InMemoryMemoryStore is the default in-memory implementation of MemoryStore.
type InMemoryMemoryStore struct {
	mu      sync.RWMutex
	entries map[string]map[string]MemoryEntry // userID -> key -> entry
}

// NewInMemoryMemoryStore creates a new InMemoryMemoryStore.
func NewInMemoryMemoryStore() *InMemoryMemoryStore {
	return &InMemoryMemoryStore{
		entries: make(map[string]map[string]MemoryEntry),
	}
}

func (s *InMemoryMemoryStore) Put(ctx context.Context, entry MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entries[entry.UserID]; !ok {
		s.entries[entry.UserID] = make(map[string]MemoryEntry)
	}
	s.entries[entry.UserID][entry.Key] = entry
	return nil
}

func (s *InMemoryMemoryStore) Get(ctx context.Context, userID, key string) (MemoryEntry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userEntries, ok := s.entries[userID]
	if !ok {
		return MemoryEntry{}, false, nil
	}
	entry, ok := userEntries[key]
	return entry, ok, nil
}

func (s *InMemoryMemoryStore) List(ctx context.Context, userID string) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userEntries, ok := s.entries[userID]
	if !ok {
		return []MemoryEntry{}, nil
	}

	result := make([]MemoryEntry, 0, len(userEntries))
	for _, entry := range userEntries {
		result = append(result, entry)
	}
	return result, nil
}

func (s *InMemoryMemoryStore) Delete(ctx context.Context, userID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if userEntries, ok := s.entries[userID]; ok {
		delete(userEntries, key)
	}
	return nil
}
