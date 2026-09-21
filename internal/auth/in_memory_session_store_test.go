package auth

import (
	"context"
	"testing"
	"time"
)

func TestInMemorySessionStore_CreateAndGet(t *testing.T) {
	store := NewInMemorySessionStore()

	session := Session{
		ID:        "s_001",
		UserID:    "u_admin",
		Username:  "admin",
		Roles:     []string{"admin"},
		CreatedAt: time.Now(),
	}

	err := store.Create(context.Background(), session)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	got, ok, err := store.Get(context.Background(), "s_001")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !ok {
		t.Fatal("expected to find session")
	}
	if got.UserID != "u_admin" {
		t.Errorf("expected userID=u_admin, got %s", got.UserID)
	}
}

func TestInMemorySessionStore_GetNonExistent(t *testing.T) {
	store := NewInMemorySessionStore()
	_, ok, _ := store.Get(context.Background(), "nonexistent")
	if ok {
		t.Error("expected not found for nonexistent session")
	}
}

func TestInMemorySessionStore_Delete(t *testing.T) {
	store := NewInMemorySessionStore()
	store.Create(context.Background(), Session{ID: "s_002", UserID: "u_admin", CreatedAt: time.Now()})

	store.Delete(context.Background(), "s_002")
	_, ok, _ := store.Get(context.Background(), "s_002")
	if ok {
		t.Error("expected session to be deleted")
	}
}

func TestInMemorySessionStore_DeleteNonExistent(t *testing.T) {
	store := NewInMemorySessionStore()
	// Deleting a nonexistent session should not error
	err := store.Delete(context.Background(), "nonexistent")
	if err != nil {
		t.Errorf("expected no error for deleting nonexistent session, got: %v", err)
	}
}

func TestInMemorySessionStore_ListByUser(t *testing.T) {
	store := NewInMemorySessionStore()
	store.Create(context.Background(), Session{ID: "s_1", UserID: "u_admin", CreatedAt: time.Now()})
	store.Create(context.Background(), Session{ID: "s_2", UserID: "u_admin", CreatedAt: time.Now()})
	store.Create(context.Background(), Session{ID: "s_3", UserID: "u_visitor", CreatedAt: time.Now()})

	sessions, err := store.ListByUser(context.Background(), "u_admin")
	if err != nil {
		t.Fatalf("list by user failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions for u_admin, got %d", len(sessions))
	}
}

func TestInMemorySessionStore_ListByUser_Empty(t *testing.T) {
	store := NewInMemorySessionStore()
	sessions, err := store.ListByUser(context.Background(), "u_nobody")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions for unknown user, got %d", len(sessions))
	}
}

func TestInMemorySessionStore_DeleteByUser(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	_ = store.Create(ctx, Session{ID: "s_1", UserID: "u_admin"})
	_ = store.Create(ctx, Session{ID: "s_2", UserID: "u_admin"})
	_ = store.Create(ctx, Session{ID: "s_3", UserID: "u_visitor"})
	if err := store.DeleteByUser(ctx, "u_admin"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s_1", "s_2"} {
		if _, ok, _ := store.Get(ctx, id); ok {
			t.Fatalf("session %s was not deleted", id)
		}
	}
	if _, ok, _ := store.Get(ctx, "s_3"); !ok {
		t.Fatal("another user's session was deleted")
	}
}
