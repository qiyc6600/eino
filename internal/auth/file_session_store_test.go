package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestFileSessionStore_PersistenceRoundTrip verifies that sessions survive
// a store restart (process restart simulation) when using the file backend.
func TestFileSessionStore_PersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	ctx := context.Background()

	// First store: create a session.
	store1, err := NewFileSessionStore(path)
	if err != nil {
		t.Fatalf("create file session store: %v", err)
	}
	session := Session{
		ID:        "s_persist",
		UserID:    "u_admin",
		Username:  "admin",
		Roles:     []string{"admin"},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(30 * time.Minute),
	}
	if err := store1.Create(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Second store over the same file: session must be readable.
	store2, err := NewFileSessionStore(path)
	if err != nil {
		t.Fatalf("reopen file session store: %v", err)
	}
	got, ok, err := store2.Get(ctx, "s_persist")
	if err != nil || !ok {
		t.Fatalf("session should survive restart, ok=%v err=%v", ok, err)
	}
	if got.UserID != "u_admin" || len(got.Roles) != 1 || got.Roles[0] != "admin" {
		t.Errorf("session data mismatch after restart: %+v", got)
	}

	// Update (sliding TTL renewal) must persist too.
	renewed := got
	renewed.ExpiresAt = time.Now().Add(60 * time.Minute)
	if err := store2.Update(ctx, renewed); err != nil {
		t.Fatalf("update session: %v", err)
	}
	store3, err := NewFileSessionStore(path)
	if err != nil {
		t.Fatalf("reopen after update: %v", err)
	}
	got3, ok, _ := store3.Get(ctx, "s_persist")
	if !ok {
		t.Fatal("session missing after update + restart")
	}
	if got3.ExpiresAt.Before(time.Now().Add(59 * time.Minute)) {
		t.Errorf("renewed expiry did not persist: %v", got3.ExpiresAt)
	}

	// Delete must persist as well.
	if err := store3.Delete(ctx, "s_persist"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	store4, err := NewFileSessionStore(path)
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	if _, ok, _ := store4.Get(ctx, "s_persist"); ok {
		t.Error("deleted session should stay deleted after restart")
	}
}

// TestFileSessionStore_ValidationAndTTL verifies that the file backend keeps
// ValidateSession expiry semantics (sessions created via Service still expire).
func TestFileSessionStore_ValidationAndTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions_ttl.json")

	store, err := NewFileSessionStore(path)
	if err != nil {
		t.Fatalf("create file session store: %v", err)
	}
	svc := newTestService(t, store, NewRBACManager(), 40*time.Millisecond)

	resp, err := svc.Login(context.Background(), "admin", "admin123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := svc.ValidateSession(context.Background(), resp.SessionID); err == nil {
		t.Error("expired session must be rejected even with the file backend")
	}
}

func TestFileSessionStoreDeleteByUserPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions_revoke.json")
	store, err := NewFileSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range []Session{
		{ID: "s_1", UserID: "u_admin"},
		{ID: "s_2", UserID: "u_admin"},
		{ID: "s_3", UserID: "u_visitor"},
	} {
		if err := store.Create(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteByUser(ctx, "u_admin"); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if sessions, _ := reopened.ListByUser(ctx, "u_admin"); len(sessions) != 0 {
		t.Fatalf("revoked sessions returned after restart: %+v", sessions)
	}
	if _, ok, _ := reopened.Get(ctx, "s_3"); !ok {
		t.Fatal("another user's session was not preserved")
	}
}
