package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DefaultSessionTTL is the sliding session lifetime when not configured.
const DefaultSessionTTL = 30 * time.Minute

// Service provides authentication operations.
type Service struct {
	store SessionStore
	rbac  *RBACManager
	// usersMu guards the users map: login, user management, and role
	// updates arrive on concurrent HTTP goroutines.
	usersMu    sync.RWMutex
	users      map[string]*User // username -> User
	sessionTTL time.Duration    // sliding session lifetime
}

// NewService creates a new auth service with default seed users.
// An optional sessionTTL overrides DefaultSessionTTL.
func NewService(store SessionStore, rbac *RBACManager, sessionTTL ...time.Duration) *Service {
	ttl := DefaultSessionTTL
	if len(sessionTTL) > 0 && sessionTTL[0] > 0 {
		ttl = sessionTTL[0]
	}
	s := &Service{
		store:      store,
		rbac:       rbac,
		users:      make(map[string]*User),
		sessionTTL: ttl,
	}

	// Seed users
	s.seedUsers()
	return s
}

func (s *Service) seedUsers() {
	s.users["admin"] = &User{
		ID:           "u_admin",
		Username:     "admin",
		PasswordHash: hashPassword("admin123"),
		Roles:        []string{"admin"},
	}
	s.users["visitor"] = &User{
		ID:           "u_visitor",
		Username:     "visitor",
		PasswordHash: hashPassword("visitor123"),
		Roles:        []string{"visitor"},
	}
}

// Login validates credentials and creates a session.
func (s *Service) Login(ctx context.Context, username, password string) (*LoginResponse, error) {
	s.usersMu.RLock()
	user, ok := s.users[username]
	s.usersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("invalid credentials")
	}

	if user.PasswordHash != hashPassword(password) {
		return nil, fmt.Errorf("invalid credentials")
	}

	sessionID := "s_" + uuid.New().String()
	session := Session{
		ID:        sessionID,
		UserID:    user.ID,
		Username:  user.Username,
		Roles:     user.Roles,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(s.sessionTTL),
	}

	if err := s.store.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return &LoginResponse{
		SessionID: sessionID,
		ExpiresAt: session.ExpiresAt,
		User: UserPublic{
			ID:       user.ID,
			Username: user.Username,
			Roles:    user.Roles,
		},
	}, nil
}

// ValidateSession checks if a session ID is valid and returns the session.
// Expired sessions are rejected and removed. A valid session is renewed
// (sliding TTL): the expiration deadline is extended by the session TTL,
// so active users are never logged out while idle users eventually expire.
func (s *Service) ValidateSession(ctx context.Context, sessionID string) (*Session, error) {
	session, ok, err := s.store.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session lookup error: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("unauthorized: invalid session")
	}

	// Expiry check. Zero ExpiresAt means a session created before TTL was
	// introduced — treat it as non-expiring for backward compatibility.
	if !session.ExpiresAt.IsZero() && time.Now().After(session.ExpiresAt) {
		_ = s.store.Delete(ctx, sessionID)
		return nil, fmt.Errorf("unauthorized: session expired")
	}

	// Sliding renewal: every successful validation extends the deadline.
	if !session.ExpiresAt.IsZero() {
		session.ExpiresAt = time.Now().Add(s.sessionTTL)
		if err := s.store.Update(ctx, session); err != nil {
			return nil, fmt.Errorf("session renewal error: %w", err)
		}
	}

	return &session, nil
}

// Logout deletes a session.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.store.Delete(ctx, sessionID)
}

// GetUser retrieves a user by username.
func (s *Service) GetUser(ctx context.Context, username string) (*User, bool) {
	s.usersMu.RLock()
	defer s.usersMu.RUnlock()
	u, ok := s.users[username]
	return u, ok
}

// GetUserByID retrieves a user by ID.
func (s *Service) GetUserByID(ctx context.Context, userID string) (*User, bool) {
	s.usersMu.RLock()
	defer s.usersMu.RUnlock()
	for _, u := range s.users {
		if u.ID == userID {
			return u, true
		}
	}
	return nil, false
}

// ListUsers returns all users (without password hashes).
func (s *Service) ListUsers(ctx context.Context) []UserPublic {
	s.usersMu.RLock()
	result := make([]UserPublic, 0, len(s.users))
	for _, u := range s.users {
		result = append(result, UserPublic{
			ID:       u.ID,
			Username: u.Username,
			Roles:    u.Roles,
		})
	}
	s.usersMu.RUnlock()
	return result
}

// CreateUser adds a new user.
func (s *Service) CreateUser(ctx context.Context, username, password string, roles []string) (*UserPublic, error) {
	s.usersMu.Lock()
	defer s.usersMu.Unlock()

	if _, exists := s.users[username]; exists {
		return nil, fmt.Errorf("user already exists: %s", username)
	}
	user := &User{
		ID:           "u_" + uuid.New().String()[:8],
		Username:     username,
		PasswordHash: hashPassword(password),
		Roles:        roles,
	}
	s.users[username] = user
	return &UserPublic{ID: user.ID, Username: user.Username, Roles: user.Roles}, nil
}

// UpdateUserRoles updates the roles of a user.
func (s *Service) UpdateUserRoles(ctx context.Context, userID string, roles []string) error {
	s.usersMu.Lock()
	defer s.usersMu.Unlock()

	for _, u := range s.users {
		if u.ID == userID {
			u.Roles = roles
			return nil
		}
	}
	return fmt.Errorf("user not found: %s", userID)
}

// RBAC returns the RBAC manager.
func (s *Service) RBAC() *RBACManager {
	return s.rbac
}

// Store returns the session store.
func (s *Service) Store() SessionStore {
	return s.store
}

// SessionTTL returns the configured sliding session lifetime.
func (s *Service) SessionTTL() time.Duration {
	return s.sessionTTL
}

// SessionExpiry returns the current expiration deadline of a session.
func (s *Service) SessionExpiry(ctx context.Context, sessionID string) (time.Time, bool) {
	session, ok, err := s.store.Get(ctx, sessionID)
	if err != nil || !ok {
		return time.Time{}, false
	}
	return session.ExpiresAt, true
}

func hashPassword(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
}
