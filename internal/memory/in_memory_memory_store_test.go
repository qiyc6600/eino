package memory

import (
	"context"
	"testing"
)

func TestInMemoryMemoryStore_PutAndGet(t *testing.T) {
	store := NewInMemoryMemoryStore()

	entry := MemoryEntry{UserID: "u_admin", Key: "preferred_language", Value: "Python"}
	store.Put(context.Background(), entry)

	got, ok, err := store.Get(context.Background(), "u_admin", "preferred_language")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !ok {
		t.Fatal("expected to find entry")
	}
	if got.Value != "Python" {
		t.Errorf("expected Python, got %s", got.Value)
	}
}

func TestInMemoryMemoryStore_UserIsolation(t *testing.T) {
	store := NewInMemoryMemoryStore()
	store.Put(context.Background(), MemoryEntry{UserID: "u_admin", Key: "preferred_language", Value: "Go"})
	store.Put(context.Background(), MemoryEntry{UserID: "u_visitor", Key: "preferred_language", Value: "Python"})

	adminEntry, ok, _ := store.Get(context.Background(), "u_admin", "preferred_language")
	if !ok || adminEntry.Value != "Go" {
		t.Error("user isolation violated: admin should have Go")
	}

	visitorEntry, ok, _ := store.Get(context.Background(), "u_visitor", "preferred_language")
	if !ok || visitorEntry.Value != "Python" {
		t.Error("user isolation violated: visitor should have Python")
	}
}

func TestInMemoryMemoryStore_List(t *testing.T) {
	store := NewInMemoryMemoryStore()
	store.Put(context.Background(), MemoryEntry{UserID: "u_admin", Key: "lang", Value: "Go"})
	store.Put(context.Background(), MemoryEntry{UserID: "u_admin", Key: "style", Value: "concise"})

	entries, err := store.List(context.Background(), "u_admin")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestInMemoryMemoryStore_ListEmpty(t *testing.T) {
	store := NewInMemoryMemoryStore()
	entries, err := store.List(context.Background(), "u_nobody")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for unknown user, got %d", len(entries))
	}
}

func TestInMemoryMemoryStore_Delete(t *testing.T) {
	store := NewInMemoryMemoryStore()
	store.Put(context.Background(), MemoryEntry{UserID: "u_admin", Key: "lang", Value: "Go"})

	store.Delete(context.Background(), "u_admin", "lang")
	_, ok, _ := store.Get(context.Background(), "u_admin", "lang")
	if ok {
		t.Error("expected entry to be deleted")
	}
}

func TestInMemoryMemoryStore_Update(t *testing.T) {
	store := NewInMemoryMemoryStore()
	store.Put(context.Background(), MemoryEntry{UserID: "u_admin", Key: "lang", Value: "Go"})
	// Put again with same key should update
	store.Put(context.Background(), MemoryEntry{UserID: "u_admin", Key: "lang", Value: "Python"})

	got, ok, _ := store.Get(context.Background(), "u_admin", "lang")
	if !ok || got.Value != "Python" {
		t.Errorf("expected updated value Python, got %s", got.Value)
	}
}
