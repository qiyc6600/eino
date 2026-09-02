package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/agent-eino-demo/internal/auth"
)

// TestFileMemoryStore_PersistenceRoundTrip verifies long-term memory entries
// survive a store restart with the file backend.
func TestFileMemoryStore_PersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.json")
	ctx := context.Background()

	store1, err := NewFileMemoryStore(path)
	if err != nil {
		t.Fatalf("create file memory store: %v", err)
	}
	if err := store1.Put(ctx, MemoryEntry{
		UserID: "u_admin", Key: "preference", Value: "likes Python",
		Source: "extracted", CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("put entry: %v", err)
	}

	store2, err := NewFileMemoryStore(path)
	if err != nil {
		t.Fatalf("reopen file memory store: %v", err)
	}
	entry, ok, err := store2.Get(ctx, "u_admin", "preference")
	if err != nil || !ok {
		t.Fatalf("entry should survive restart, ok=%v err=%v", ok, err)
	}
	if entry.Value != "likes Python" {
		t.Errorf("entry value mismatch after restart: %+v", entry)
	}

	// Delete persists too.
	if err := store2.Delete(ctx, "u_admin", "preference"); err != nil {
		t.Fatalf("delete entry: %v", err)
	}
	store3, err := NewFileMemoryStore(path)
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	if _, ok, _ := store3.Get(ctx, "u_admin", "preference"); ok {
		t.Error("deleted entry should stay deleted after restart")
	}
}

// TestFileMemoryStore_ScopeCheckInherited verifies the store-level cross-user
// guard still applies through the file decorator.
func TestFileMemoryStore_ScopeCheckInherited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory_scope.json")

	store, err := NewFileMemoryStore(path)
	if err != nil {
		t.Fatalf("create file memory store: %v", err)
	}

	adminCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{UserID: "u_admin"})
	err = store.Put(adminCtx, MemoryEntry{UserID: "u_visitor", Key: "k", Value: "injected"})
	if err == nil {
		t.Fatal("cross-user write must be rejected through the file backend")
	}

	// The denied write must not have been persisted either.
	reopened, err := NewFileMemoryStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, ok, _ := reopened.Get(context.Background(), "u_visitor", "k"); ok {
		t.Error("denied cross-user entry must not be persisted")
	}
}

// TestFileCheckpointStore_PersistenceRoundTrip verifies interrupted run
// states survive a process restart with the file backend.
func TestFileCheckpointStore_PersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	ctx := context.Background()

	store1, err := NewFileCheckpointStore(path)
	if err != nil {
		t.Fatalf("create file checkpoint store: %v", err)
	}
	cp := Checkpoint{
		UserID:      "u_admin",
		ThreadID:    "t1",
		RunID:       "r1",
		Step:        2,
		State:       []byte(`{"messages":["pending plan"]}`),
		Interrupted: true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := store1.Save(ctx, cp); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	store2, err := NewFileCheckpointStore(path)
	if err != nil {
		t.Fatalf("reopen file checkpoint store: %v", err)
	}
	got, ok, err := store2.Load(ctx, CheckpointKey{UserID: "u_admin", ThreadID: "t1", RunID: "r1", Step: 2})
	if err != nil || !ok {
		t.Fatalf("checkpoint should survive restart, ok=%v err=%v", ok, err)
	}
	if !got.Interrupted || string(got.State) != `{"messages":["pending plan"]}` {
		t.Errorf("checkpoint data mismatch after restart: %+v", got)
	}

	// ListByThread works across restarts.
	list, err := store2.ListByThread(ctx, "u_admin", "t1")
	if err != nil || len(list) != 1 {
		t.Errorf("ListByThread after restart: len=%d err=%v", len(list), err)
	}

	// Delete persists.
	key := CheckpointKey{UserID: "u_admin", ThreadID: "t1", RunID: "r1", Step: 2}
	if err := store2.Delete(ctx, key); err != nil {
		t.Fatalf("delete checkpoint: %v", err)
	}
	store3, err := NewFileCheckpointStore(path)
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	if _, ok, _ := store3.Load(ctx, key); ok {
		t.Error("deleted checkpoint should stay deleted after restart")
	}
}
