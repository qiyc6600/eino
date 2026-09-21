package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrLoginRateLimited = errors.New("too many login attempts")

type LoginRatePolicy struct {
	MaxFailures int
	Window      time.Duration
	Lockout     time.Duration
}

type LoginLimitState struct {
	FailureCount    int
	WindowStartedAt time.Time
	LockedUntil     time.Time
}

type LoginLimitStore interface {
	LockedUntil(ctx context.Context, keys []string, now time.Time) (time.Time, error)
	RecordFailure(ctx context.Context, keys []string, now time.Time, policy LoginRatePolicy) error
	Reset(ctx context.Context, keys []string) error
}

type LoginLimiter struct {
	store  LoginLimitStore
	policy LoginRatePolicy
	now    func() time.Time
}

type LoginRateLimitError struct {
	RetryAfter time.Duration
}

func (e *LoginRateLimitError) Error() string { return ErrLoginRateLimited.Error() }
func (e *LoginRateLimitError) Unwrap() error { return ErrLoginRateLimited }

func NewLoginLimiter(store LoginLimitStore, policy LoginRatePolicy) (*LoginLimiter, error) {
	if store == nil {
		return nil, errors.New("login limit store is required")
	}
	if policy.MaxFailures <= 0 || policy.Window <= 0 || policy.Lockout <= 0 {
		return nil, errors.New("login rate limit values must be positive")
	}
	return &LoginLimiter{store: store, policy: policy, now: time.Now}, nil
}

func (l *LoginLimiter) Check(ctx context.Context, username, source string) error {
	now := l.now()
	lockedUntil, err := l.store.LockedUntil(ctx, loginLimitKeys(username, source), now)
	if err != nil {
		return fmt.Errorf("check login rate limit: %w", err)
	}
	if lockedUntil.After(now) {
		return &LoginRateLimitError{RetryAfter: lockedUntil.Sub(now)}
	}
	return nil
}

func (l *LoginLimiter) RecordFailure(ctx context.Context, username, source string) error {
	if err := l.store.RecordFailure(ctx, loginLimitKeys(username, source), l.now(), l.policy); err != nil {
		return fmt.Errorf("record login failure: %w", err)
	}
	return nil
}

func (l *LoginLimiter) Reset(ctx context.Context, username, source string) error {
	if err := l.store.Reset(ctx, loginLimitKeys(username, source)); err != nil {
		return fmt.Errorf("reset login rate limit: %w", err)
	}
	return nil
}

func loginLimitKeys(username, source string) []string {
	values := []string{"username:" + strings.ToLower(strings.TrimSpace(username))}
	if source != "" {
		values = append(values, "source:"+source)
	}
	keys := make([]string, 0, len(values))
	for _, value := range values {
		digest := sha256.Sum256([]byte(value))
		keys = append(keys, hex.EncodeToString(digest[:]))
	}
	sort.Strings(keys)
	return keys
}

// AdvanceLoginLimitState applies one failed attempt to a fixed-window state.
// Stores use this transition so memory and PostgreSQL enforce identical rules.
func AdvanceLoginLimitState(state LoginLimitState, now time.Time, policy LoginRatePolicy) LoginLimitState {
	if state.LockedUntil.After(now) {
		return state
	}
	if !state.LockedUntil.IsZero() || state.WindowStartedAt.IsZero() || !now.Before(state.WindowStartedAt.Add(policy.Window)) {
		state.FailureCount = 0
		state.WindowStartedAt = now
		state.LockedUntil = time.Time{}
	}
	state.FailureCount++
	if state.FailureCount >= policy.MaxFailures {
		state.LockedUntil = now.Add(policy.Lockout)
	}
	return state
}

type InMemoryLoginLimitStore struct {
	mu     sync.Mutex
	states map[string]LoginLimitState
}

func NewInMemoryLoginLimitStore() *InMemoryLoginLimitStore {
	return &InMemoryLoginLimitStore{states: make(map[string]LoginLimitState)}
}

func (s *InMemoryLoginLimitStore) LockedUntil(_ context.Context, keys []string, now time.Time) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest time.Time
	for _, key := range keys {
		state, ok := s.states[key]
		if !ok {
			continue
		}
		if state.LockedUntil.After(latest) {
			latest = state.LockedUntil
		}
	}
	return latest, nil
}

func (s *InMemoryLoginLimitStore) RecordFailure(_ context.Context, keys []string, now time.Time, policy LoginRatePolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		s.states[key] = AdvanceLoginLimitState(s.states[key], now, policy)
	}
	return nil
}

func (s *InMemoryLoginLimitStore) Reset(_ context.Context, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		delete(s.states, key)
	}
	return nil
}
