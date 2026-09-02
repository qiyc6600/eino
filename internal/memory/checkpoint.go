package memory

import (
	"context"
	"time"
)

// CheckpointKey uniquely identifies a checkpoint.
type CheckpointKey struct {
	UserID   string
	ThreadID string
	RunID    string
	Step     int // step counter for multiple checkpoints within the same run (0 = single checkpoint)
}

// Checkpoint stores the state of an interrupted run or a conversation snapshot.
// Two use cases:
// 1. HITL interrupt/resume: State holds interrupt metadata, Interrupted=true, Snapshot=false
// 2. Conversation snapshot: State holds serialized thread messages, Interrupted=false, Snapshot=true
//
// The Step field disambiguates multiple checkpoints within the same run.
// For example, if a run hits HITL interrupts twice (step 3 then step 7),
// each interrupt gets its own checkpoint with a different Step value.
// Single-checkpoint saves (snapshots, simple interrupts) use Step=0.
type Checkpoint struct {
	UserID      string    `json:"user_id"`
	ThreadID    string    `json:"thread_id"`
	RunID       string    `json:"run_id"`
	Step        int       `json:"step"`        // disambiguates multiple checkpoints per run
	State       []byte    `json:"state"`       // serialized state (JSON)
	Interrupted bool      `json:"interrupted"` // true if this is an HITL interrupt checkpoint
	Snapshot    bool      `json:"snapshot"`    // true if this is a conversation snapshot
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CheckpointStore persists checkpoints for interrupt/resume and crash recovery.
// Implementations MUST:
//   - Not overwrite existing checkpoints with different Step values (each step is independent)
//   - Return checkpoints from ListByThread sorted by CreatedAt descending (newest first)
type CheckpointStore interface {
	Save(ctx context.Context, checkpoint Checkpoint) error
	Load(ctx context.Context, key CheckpointKey) (Checkpoint, bool, error)
	Delete(ctx context.Context, key CheckpointKey) error
	ListByThread(ctx context.Context, userID, threadID string) ([]Checkpoint, error)
}
