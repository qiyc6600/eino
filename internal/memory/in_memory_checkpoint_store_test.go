package memory

import (
	"context"
	"testing"
)

func TestInMemoryCheckpointStore_SaveAndLoad(t *testing.T) {
	store := NewInMemoryCheckpointStore()

	cp := Checkpoint{
		UserID:   "u_admin",
		ThreadID: "t_001",
		RunID:    "r_001",
		State:    []byte("test-state"),
	}

	err := store.Save(context.Background(), cp)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, ok, err := store.Load(context.Background(), CheckpointKey{UserID: "u_admin", ThreadID: "t_001", RunID: "r_001"})
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if !ok {
		t.Fatal("expected to find checkpoint")
	}
	if string(loaded.State) != "test-state" {
		t.Errorf("expected state=test-state, got %s", string(loaded.State))
	}
}

func TestInMemoryCheckpointStore_LoadNonExistent(t *testing.T) {
	store := NewInMemoryCheckpointStore()
	_, ok, _ := store.Load(context.Background(), CheckpointKey{UserID: "u_admin", ThreadID: "t_999", RunID: "r_999"})
	if ok {
		t.Error("expected not found for nonexistent checkpoint")
	}
}

func TestInMemoryCheckpointStore_Delete(t *testing.T) {
	store := NewInMemoryCheckpointStore()
	store.Save(context.Background(), Checkpoint{UserID: "u_admin", ThreadID: "t_001", RunID: "r_001", State: []byte("x")})

	store.Delete(context.Background(), CheckpointKey{UserID: "u_admin", ThreadID: "t_001", RunID: "r_001"})
	_, ok, _ := store.Load(context.Background(), CheckpointKey{UserID: "u_admin", ThreadID: "t_001", RunID: "r_001"})
	if ok {
		t.Error("expected checkpoint to be deleted")
	}
}

func TestInMemoryCheckpointStore_ListByThread(t *testing.T) {
	store := NewInMemoryCheckpointStore()
	store.Save(context.Background(), Checkpoint{UserID: "u_admin", ThreadID: "t_001", RunID: "r_001", State: []byte("a")})
	store.Save(context.Background(), Checkpoint{UserID: "u_admin", ThreadID: "t_001", RunID: "r_002", State: []byte("b")})
	store.Save(context.Background(), Checkpoint{UserID: "u_admin", ThreadID: "t_002", RunID: "r_003", State: []byte("c")})

	checkpoints, err := store.ListByThread(context.Background(), "u_admin", "t_001")
	if err != nil {
		t.Fatalf("list by thread failed: %v", err)
	}
	if len(checkpoints) != 2 {
		t.Errorf("expected 2 checkpoints for t_001, got %d", len(checkpoints))
	}
}

func TestInMemoryCheckpointStore_UserIsolation(t *testing.T) {
	store := NewInMemoryCheckpointStore()
	store.Save(context.Background(), Checkpoint{UserID: "u_admin", ThreadID: "t_001", RunID: "r_001", State: []byte("admin-state")})
	store.Save(context.Background(), Checkpoint{UserID: "u_visitor", ThreadID: "t_001", RunID: "r_001", State: []byte("visitor-state")})

	loaded, ok, _ := store.Load(context.Background(), CheckpointKey{UserID: "u_admin", ThreadID: "t_001", RunID: "r_001"})
	if !ok || string(loaded.State) != "admin-state" {
		t.Error("user isolation violated: admin got wrong state")
	}

	loaded2, ok2, _ := store.Load(context.Background(), CheckpointKey{UserID: "u_visitor", ThreadID: "t_001", RunID: "r_001"})
	if !ok2 || string(loaded2.State) != "visitor-state" {
		t.Error("user isolation violated: visitor got wrong state")
	}
}

func TestInMemoryCheckpointStore_SnapshotVsInterrupt(t *testing.T) {
	store := NewInMemoryCheckpointStore()

	// Save an interrupt checkpoint
	store.Save(context.Background(), Checkpoint{
		UserID: "u_admin", ThreadID: "t_001", RunID: "r_001",
		State: []byte("interrupt-state"), Interrupted: true, Snapshot: false,
	})

	// Save a conversation snapshot
	store.Save(context.Background(), Checkpoint{
		UserID: "u_admin", ThreadID: "t_001", RunID: "r_snap",
		State: []byte("snapshot-state"), Interrupted: false, Snapshot: true,
	})

	checkpoints, _ := store.ListByThread(context.Background(), "u_admin", "t_001")
	if len(checkpoints) != 2 {
		t.Fatalf("expected 2 checkpoints, got %d", len(checkpoints))
	}

	var intCp, snapCp Checkpoint
	for _, cp := range checkpoints {
		if cp.RunID == "r_001" {
			intCp = cp
		}
		if cp.RunID == "r_snap" {
			snapCp = cp
		}
	}

	if intCp.Interrupted != true || intCp.Snapshot != false {
		t.Error("interrupt checkpoint has wrong flags")
	}
	if snapCp.Interrupted != false || snapCp.Snapshot != true {
		t.Error("snapshot checkpoint has wrong flags")
	}
}
