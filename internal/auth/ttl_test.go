package auth

import (
	"context"
	"testing"
	"time"
)

// TestSessionTTL_Expiry verifies that a session becomes invalid after its
// TTL elapses without activity, and that the expired session is removed.
func TestSessionTTL_Expiry(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := newTestService(t, store, rbac, 60*time.Millisecond)

	resp, err := svc.Login(context.Background(), "admin", "admin123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	// Valid immediately after login.
	if _, err := svc.ValidateSession(context.Background(), resp.SessionID); err != nil {
		t.Fatalf("session should be valid right after login: %v", err)
	}

	// Wait past the TTL (no activity in between → no renewal).
	time.Sleep(120 * time.Millisecond)

	if _, err := svc.ValidateSession(context.Background(), resp.SessionID); err == nil {
		t.Fatal("expired session must be rejected")
	}

	// The expired session is removed from the store.
	if _, ok, _ := store.Get(context.Background(), resp.SessionID); ok {
		t.Error("expired session should be deleted from the store")
	}
}

// TestSessionTTL_SlidingRenewal verifies that each successful validation
// extends the expiration deadline: a continuously active session outlives
// its TTL many times over.
func TestSessionTTL_SlidingRenewal(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	ttl := 150 * time.Millisecond
	svc := newTestService(t, store, rbac, ttl)

	resp, err := svc.Login(context.Background(), "admin", "admin123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	// Sleep 100ms (< TTL), then validate — repeated well beyond the original
	// expiry. Without sliding renewal the session would die after ~150ms.
	for i := 0; i < 6; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := svc.ValidateSession(context.Background(), resp.SessionID); err != nil {
			t.Fatalf("active session should be renewed on each validation (iteration %d): %v", i, err)
		}
	}

	// Total elapsed ~600ms > 4x TTL — only possible with sliding renewal.
	// Stop being active and confirm it eventually expires.
	time.Sleep(250 * time.Millisecond)
	if _, err := svc.ValidateSession(context.Background(), resp.SessionID); err == nil {
		t.Error("idle session should expire after TTL without activity")
	}
}

// TestSessionTTL_LoginResponseCarriesExpiry verifies the login response
// exposes the expiration deadline to the client.
func TestSessionTTL_LoginResponseCarriesExpiry(t *testing.T) {
	ttl := 5 * time.Minute
	svc := newTestService(t, NewInMemorySessionStore(), NewRBACManager(), ttl)

	resp, err := svc.Login(context.Background(), "admin", "admin123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if resp.ExpiresAt.IsZero() {
		t.Fatal("expected non-zero ExpiresAt in login response")
	}
	delta := time.Until(resp.ExpiresAt)
	if delta > ttl || delta < ttl-5*time.Second {
		t.Errorf("ExpiresAt should be ~TTL from now, got %v", delta)
	}
}

// TestSessionStore_Update verifies the Update operation backing renewal.
func TestSessionStore_Update(t *testing.T) {
	store := NewInMemorySessionStore()
	ctx := context.Background()

	session := Session{
		ID:        "s_test",
		UserID:    "u_admin",
		Username:  "admin",
		Roles:     []string{"admin"},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := store.Create(ctx, session); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Update the deadline.
	session.ExpiresAt = time.Now().Add(2 * time.Minute)
	if err := store.Update(ctx, session); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	got, ok, _ := store.Get(ctx, "s_test")
	if !ok {
		t.Fatal("session should still exist after update")
	}
	if !got.ExpiresAt.Equal(session.ExpiresAt) {
		t.Errorf("update should persist the new ExpiresAt, got %v want %v", got.ExpiresAt, session.ExpiresAt)
	}

	// Updating a nonexistent session is an error.
	ghost := Session{ID: "s_ghost", UserID: "u_admin"}
	if err := store.Update(ctx, ghost); err == nil {
		t.Error("update of nonexistent session should fail")
	}
}

// TestSessionTTL_ZeroExpiresAtBackwardCompat verifies that sessions stored
// without an expiry (pre-TTL data) are not spuriously rejected.
func TestSessionTTL_ZeroExpiresAtBackwardCompat(t *testing.T) {
	store := NewInMemorySessionStore()
	rbac := NewRBACManager()
	svc := newTestService(t, store, rbac, 1*time.Millisecond)

	// Seed a session directly with zero ExpiresAt.
	session := Session{ID: "s_legacy", UserID: "u_admin", Username: "admin", Roles: []string{"admin"}}
	if err := store.Create(context.Background(), session); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	if _, err := svc.ValidateSession(context.Background(), "s_legacy"); err != nil {
		t.Errorf("zero-expires session should be treated as non-expiring: %v", err)
	}
}
