package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLoginLimiterUsernameAndSource(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryLoginLimitStore()
	limiter, err := NewLoginLimiter(store, LoginRatePolicy{MaxFailures: 3, Window: time.Minute, Lockout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if err := limiter.Check(ctx, "Alice", "192.0.2.1"); err != nil {
			t.Fatal(err)
		}
		if err := limiter.RecordFailure(ctx, "Alice", "192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	for _, attempt := range []struct{ username, source string }{
		{"alice", "192.0.2.2"}, // username limit across IPs
		{"bob", "192.0.2.1"},   // source limit across usernames
	} {
		var limited *LoginRateLimitError
		if err := limiter.Check(ctx, attempt.username, attempt.source); !errors.As(err, &limited) || limited.RetryAfter != 2*time.Minute {
			t.Fatalf("%+v: expected lockout, got %v", attempt, err)
		}
	}
	if err := limiter.Check(ctx, "bob", "192.0.2.2"); err != nil {
		t.Fatalf("unrelated login was limited: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := limiter.Check(ctx, "alice", "192.0.2.1"); err != nil {
		t.Fatalf("lockout did not expire: %v", err)
	}
	if err := limiter.RecordFailure(ctx, "alice", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Check(ctx, "alice", "192.0.2.1"); err != nil {
		t.Fatalf("expired lockout should start a fresh window: %v", err)
	}
}

func TestLoginServiceResetsFailuresAfterSuccess(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, NewInMemorySessionStore(), NewRBACManager())
	limiter, err := NewLoginLimiter(NewInMemoryLoginLimitStore(), LoginRatePolicy{MaxFailures: 2, Window: time.Hour, Lockout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	service.SetLoginLimiter(limiter)
	if _, err := service.LoginWithSource(ctx, "admin", "wrong", "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
	if _, err := service.LoginWithSource(ctx, "admin", "admin123", "192.0.2.1"); err != nil {
		t.Fatalf("correct password rejected before threshold: %v", err)
	}
	if _, err := service.LoginWithSource(ctx, "admin", "wrong", "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("failure count was not reset: %v", err)
	}
	if _, err := service.LoginWithSource(ctx, "admin", "wrong", "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second invalid attempt: %v", err)
	}
	if _, err := service.LoginWithSource(ctx, "admin", "admin123", "192.0.2.1"); !errors.Is(err, ErrLoginRateLimited) {
		t.Fatalf("correct password must not bypass lockout: %v", err)
	}
}

func TestLoginLimiterUnknownUsernameCountsAgainstSource(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, NewInMemorySessionStore(), NewRBACManager())
	limiter, err := NewLoginLimiter(NewInMemoryLoginLimitStore(), LoginRatePolicy{MaxFailures: 2, Window: time.Minute, Lockout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	service.SetLoginLimiter(limiter)
	for _, username := range []string{"absent-1", "absent-2"} {
		if _, err := service.LoginWithSource(ctx, username, "guess", "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("unknown user error: %v", err)
		}
	}
	if _, err := service.LoginWithSource(ctx, "admin", "admin123", "192.0.2.1"); !errors.Is(err, ErrLoginRateLimited) {
		t.Fatalf("unknown usernames did not exhaust source budget: %v", err)
	}
}
