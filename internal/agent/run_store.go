package agent

import (
	"context"
	"time"
)

// RunStore shares the latest run result, including its events, across instances.
// Implementations must scope reads and writes by user ID.
type RunStore interface {
	Save(ctx context.Context, userID string, result ChatRunResult, retention time.Duration) error
	Load(ctx context.Context, userID, runID string) (*ChatRunResult, bool, error)
}
