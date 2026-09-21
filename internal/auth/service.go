package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DefaultSessionTTL is the sliding session lifetime when not configured.
const DefaultSessionTTL = 30 * time.Minute

var ErrInvalidCredentials = errors.New("invalid credentials")

// Service provides authentication operations.
type Service struct {
	store      SessionStore
	users      UserStore
	rbac       *RBACManager
	limiter    *LoginLimiter
	sessionTTL time.Duration // sliding session lifetime
}

// NewService creates an auth service with an empty in-memory user store.
// An optional sessionTTL overrides DefaultSessionTTL.
func NewService(store SessionStore, rbac *RBACManager, sessionTTL ...time.Duration) *Service {
	service, err := NewServiceWithUserStore(store, NewInMemoryUserStore(), rbac, sessionTTL...)
	if err != nil {
		panic(err)
	}
	return service
}

// NewServiceWithUserStore creates the auth service with a shared account store.
func NewServiceWithUserStore(store SessionStore, users UserStore, rbac *RBACManager, sessionTTL ...time.Duration) (*Service, error) {
	ttl := DefaultSessionTTL
	if len(sessionTTL) > 0 && sessionTTL[0] > 0 {
		ttl = sessionTTL[0]
	}
	s := &Service{
		store:      store,
		users:      users,
		rbac:       rbac,
		sessionTTL: ttl,
	}
	return s, nil
}

func (s *Service) SetLoginLimiter(limiter *LoginLimiter) {
	s.limiter = limiter
}

// Login validates credentials and creates a session.
func (s *Service) Login(ctx context.Context, username, password string) (*LoginResponse, error) {
	return s.LoginWithSource(ctx, username, password, "")
}

// LoginWithSource enforces independent username and direct peer IP limits.
func (s *Service) LoginWithSource(ctx context.Context, username, password, source string) (*LoginResponse, error) {
	if s.limiter != nil {
		if err := s.limiter.Check(ctx, username, source); err != nil {
			return nil, err
		}
	}
	user, ok, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("user lookup error: %w", err)
	}
	if !ok {
		return nil, s.invalidLogin(ctx, username, source)
	}

	valid, needsUpgrade, verifyErr := verifyPassword(user.PasswordHash, password)
	if verifyErr != nil || !valid {
		return nil, s.invalidLogin(ctx, username, source)
	}
	if needsUpgrade {
		upgraded, err := hashPassword(password)
		if err != nil {
			return nil, fmt.Errorf("upgrade password hash: %w", err)
		}
		if err := s.users.UpdatePasswordHash(ctx, user.ID, upgraded); err != nil {
			return nil, fmt.Errorf("upgrade password hash: %w", err)
		}
		user.PasswordHash = upgraded
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
	if s.limiter != nil {
		if err := s.limiter.Reset(ctx, username, source); err != nil {
			_ = s.store.Delete(ctx, sessionID)
			return nil, err
		}
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

func (s *Service) invalidLogin(ctx context.Context, username, source string) error {
	if s.limiter != nil {
		if err := s.limiter.RecordFailure(ctx, username, source); err != nil {
			return err
		}
	}
	return ErrInvalidCredentials
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
	currentUser, userOK, err := s.users.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, fmt.Errorf("user lookup error: %w", err)
	}
	if !userOK || currentUser.Username != session.Username || !sameRoles(currentUser.Roles, session.Roles) {
		_ = s.store.Delete(ctx, sessionID)
		return nil, fmt.Errorf("unauthorized: session roles changed")
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
	user, ok, err := s.users.GetByUsername(ctx, username)
	if err != nil || !ok {
		return nil, false
	}
	return &user, true
}

// GetUserByID retrieves a user by ID.
func (s *Service) GetUserByID(ctx context.Context, userID string) (*User, bool) {
	user, ok, err := s.users.GetByID(ctx, userID)
	if err != nil || !ok {
		return nil, false
	}
	return &user, true
}

// ListUsers returns all users (without password hashes).
func (s *Service) ListUsers(ctx context.Context) []UserPublic {
	users, _ := s.ListUsersE(ctx)
	return users
}

// ListUsersE returns all users and preserves storage errors for HTTP callers.
func (s *Service) ListUsersE(ctx context.Context) ([]UserPublic, error) {
	users, err := s.users.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]UserPublic, 0, len(users))
	for _, u := range users {
		result = append(result, UserPublic{
			ID:       u.ID,
			Username: u.Username,
			Roles:    u.Roles,
		})
	}
	return result, nil
}

// CreateUser adds a new user.
func (s *Service) CreateUser(ctx context.Context, username, password string, roles []string) (*UserPublic, error) {
	passwordHash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	user := User{
		ID:           "u_" + uuid.New().String()[:8],
		Username:     username,
		PasswordHash: passwordHash,
		Roles:        roles,
	}
	if err := s.users.Create(ctx, user); err != nil {
		return nil, err
	}
	return &UserPublic{ID: user.ID, Username: user.Username, Roles: user.Roles}, nil
}

// UpdateUserRoles updates the roles of a user.
func (s *Service) UpdateUserRoles(ctx context.Context, userID string, roles []string) error {
	if err := s.users.UpdateRoles(ctx, userID, roles); err != nil {
		return err
	}
	if err := s.store.DeleteByUser(ctx, userID); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
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

func sameRoles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, role := range a {
		counts[role]++
	}
	for _, role := range b {
		counts[role]--
		if counts[role] < 0 {
			return false
		}
	}
	return true
}
