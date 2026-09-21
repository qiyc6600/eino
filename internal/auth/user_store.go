package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrUserExists   = errors.New("user already exists")
	ErrUserNotFound = errors.New("user not found")
)

// UserStore persists accounts and their role bindings. Role permission
// definitions remain application configuration in RBACManager.
type UserStore interface {
	Seed(ctx context.Context, users []User) error
	GetByUsername(ctx context.Context, username string) (User, bool, error)
	GetByID(ctx context.Context, userID string) (User, bool, error)
	List(ctx context.Context) ([]User, error)
	Create(ctx context.Context, user User) error
	UpdateRoles(ctx context.Context, userID string, roles []string) error
	UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error
}

type InMemoryUserStore struct {
	mu       sync.RWMutex
	byName   map[string]User
	nameByID map[string]string
}

func NewInMemoryUserStore() *InMemoryUserStore {
	return &InMemoryUserStore{byName: make(map[string]User), nameByID: make(map[string]string)}
}

func cloneUser(user User) User {
	user.Roles = append([]string(nil), user.Roles...)
	return user
}

func (s *InMemoryUserStore) Seed(_ context.Context, users []User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range users {
		if _, exists := s.byName[user.Username]; exists {
			continue
		}
		s.byName[user.Username] = cloneUser(user)
		s.nameByID[user.ID] = user.Username
	}
	return nil
}

func (s *InMemoryUserStore) GetByUsername(_ context.Context, username string) (User, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.byName[username]
	return cloneUser(user), ok, nil
}

func (s *InMemoryUserStore) GetByID(_ context.Context, userID string) (User, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	name, ok := s.nameByID[userID]
	if !ok {
		return User{}, false, nil
	}
	user, ok := s.byName[name]
	return cloneUser(user), ok, nil
}

func (s *InMemoryUserStore) List(_ context.Context) ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]User, 0, len(s.byName))
	for _, user := range s.byName {
		result = append(result, cloneUser(user))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Username < result[j].Username })
	return result, nil
}

func (s *InMemoryUserStore) Create(_ context.Context, user User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byName[user.Username]; exists {
		return fmt.Errorf("%w: %s", ErrUserExists, user.Username)
	}
	if _, exists := s.nameByID[user.ID]; exists {
		return fmt.Errorf("%w: ID %s", ErrUserExists, user.ID)
	}
	s.byName[user.Username] = cloneUser(user)
	s.nameByID[user.ID] = user.Username
	return nil
}

func (s *InMemoryUserStore) UpdateRoles(_ context.Context, userID string, roles []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, ok := s.nameByID[userID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUserNotFound, userID)
	}
	user := s.byName[name]
	user.Roles = append([]string(nil), roles...)
	s.byName[name] = user
	return nil
}

func (s *InMemoryUserStore) UpdatePasswordHash(_ context.Context, userID, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, ok := s.nameByID[userID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUserNotFound, userID)
	}
	user := s.byName[name]
	user.PasswordHash = passwordHash
	s.byName[name] = user
	return nil
}

var _ UserStore = (*InMemoryUserStore)(nil)
