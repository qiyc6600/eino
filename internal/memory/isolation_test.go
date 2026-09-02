package memory

import (
	"context"
	"testing"

	"github.com/example/agent-eino-demo/internal/auth"
)

// adminCtx returns a context carrying admin's authenticated identity.
func adminCtx() context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		UserID: "u_admin",
		Roles:  []string{"admin"},
	})
}

func TestMemoryStore_CrossUserWriteDenied(t *testing.T) {
	store := NewInMemoryMemoryStore()

	// Admin's context trying to write an entry stamped with visitor's ID.
	err := store.Put(adminCtx(), MemoryEntry{
		UserID: "u_visitor",
		Key:    "preference",
		Value:  "injected",
	})
	if err == nil {
		t.Fatal("cross-user write must be rejected at the store layer")
	}

	// The same write under the matching identity succeeds.
	if err := store.Put(adminCtx(), MemoryEntry{UserID: "u_admin", Key: "preference", Value: "ok"}); err != nil {
		t.Fatalf("same-user write should succeed: %v", err)
	}
}

func TestMemoryStore_CrossUserReadDenied(t *testing.T) {
	store := NewInMemoryMemoryStore()

	// Seed visitor data with no identity in ctx (internal seed path).
	if err := store.Put(context.Background(), MemoryEntry{UserID: "u_visitor", Key: "secret", Value: "data"}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	if _, _, err := store.Get(adminCtx(), "u_visitor", "secret"); err == nil {
		t.Error("cross-user Get must be rejected")
	}
	if _, err := store.List(adminCtx(), "u_visitor"); err == nil {
		t.Error("cross-user List must be rejected")
	}
	if err := store.Delete(adminCtx(), "u_visitor", "secret"); err == nil {
		t.Error("cross-user Delete must be rejected")
	}

	// Background ctx (no identity) still passes — internal jobs keep working.
	if _, ok, _ := store.Get(context.Background(), "u_visitor", "secret"); !ok {
		t.Error("no-identity ctx should not be blocked")
	}
}

func TestCheckpointStore_CrossUserLoadDenied(t *testing.T) {
	store := NewInMemoryCheckpointStore()

	cp := Checkpoint{
		UserID:   "u_visitor",
		ThreadID: "t1",
		RunID:    "r1",
		State:    []byte("visitor conversation state"),
	}
	if err := store.Save(context.Background(), cp); err != nil {
		t.Fatalf("seed save failed: %v", err)
	}

	// Admin context cannot load visitor's checkpoint.
	if _, _, err := store.Load(adminCtx(), CheckpointKey{UserID: "u_visitor", ThreadID: "t1", RunID: "r1"}); err == nil {
		t.Error("cross-user checkpoint Load must be rejected")
	}

	// Admin context cannot overwrite visitor's checkpoint.
	if err := store.Save(adminCtx(), cp); err == nil {
		t.Error("cross-user checkpoint Save must be rejected")
	}

	// Admin context cannot delete visitor's checkpoint.
	if err := store.Delete(adminCtx(), CheckpointKey{UserID: "u_visitor", ThreadID: "t1", RunID: "r1"}); err == nil {
		t.Error("cross-user checkpoint Delete must be rejected")
	}

	// The visitor's state is intact.
	if _, ok, _ := store.Load(context.Background(), CheckpointKey{UserID: "u_visitor", ThreadID: "t1", RunID: "r1"}); !ok {
		t.Error("visitor checkpoint should be intact after denied cross-user access")
	}
}

func TestVectorStore_CrossUserQueryDenied(t *testing.T) {
	store := NewInMemoryVectorStore()

	if err := store.Store(context.Background(), "u_visitor", "visitor private note", nil); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	if _, err := store.Query(adminCtx(), "u_visitor", "private note", 3); err == nil {
		t.Error("cross-user vector Query must be rejected")
	}
	if err := store.Store(adminCtx(), "u_visitor", "injected", nil); err == nil {
		t.Error("cross-user vector Store must be rejected")
	}
	if err := store.DeleteUser(adminCtx(), "u_visitor"); err == nil {
		t.Error("cross-user vector DeleteUser must be rejected")
	}
}
