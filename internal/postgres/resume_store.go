package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/auth"
)

// PublishResume atomically records a claimed approval's receipt, run result,
// and either its final thread or the next pending interrupt.
func (b *Backend) PublishResume(ctx context.Context, publication agent.ResumePublication) error {
	req := publication.Approval
	if req == nil || req.InterruptID == "" || req.UserID == "" || req.ThreadID == "" || req.RunID == "" ||
		req.Phase != "finished" || req.ClaimToken == "" || req.Decision == nil || len(req.Result) == 0 ||
		publication.Result.RunID != req.RunID || publication.Retention <= 0 {
		return fmt.Errorf("inconsistent approval completion")
	}
	if err := auth.CheckUserScope(ctx, req.UserID); err != nil {
		return err
	}
	resultData, err := json.Marshal(publication.Result)
	if err != nil {
		return err
	}
	if !bytes.Equal(req.Result, resultData) {
		return fmt.Errorf("approval receipt does not match run result")
	}
	switch publication.Result.Status {
	case agent.StatusCompleted:
		if !publication.ReplaceThread || publication.Next != nil {
			return fmt.Errorf("completed run requires a final thread")
		}
	case agent.StatusInterrupted:
		if publication.ReplaceThread || publication.Next == nil || publication.Result.Interrupt == nil ||
			publication.Next.Approval == nil || publication.Next.Approval.InterruptID != publication.Result.Interrupt.InterruptID ||
			publication.Next.Approval.InterruptID == req.InterruptID || !bytes.Equal(req.State, publication.Next.Checkpoint.State) {
			return fmt.Errorf("interrupted run requires the next approval")
		}
		if err := validateInterruptPublication(ctx, publication.Next); err != nil {
			return err
		}
		if publication.Next.Checkpoint.UserID != req.UserID || publication.Next.Checkpoint.ThreadID != req.ThreadID || publication.Next.Checkpoint.RunID != req.RunID {
			return fmt.Errorf("next interrupt belongs to a different run")
		}
	case agent.StatusError, agent.StatusCancelled:
		if publication.ReplaceThread || publication.Next != nil {
			return fmt.Errorf("failed run cannot publish a thread or approval")
		}
	default:
		return fmt.Errorf("unknown run status %q", publication.Result.Status)
	}
	tx, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if publication.Next != nil {
		next := publication.Next
		if err := saveCheckpoint(ctx, tx, next.Checkpoint); err != nil {
			return fmt.Errorf("save next checkpoint: %w", err)
		}
		if err := replaceThread(ctx, tx, req.UserID, req.ThreadID, next.Messages); err != nil {
			return fmt.Errorf("save next thread: %w", err)
		}
		if err := writeApproval(ctx, tx, next.Approval, true); err != nil {
			return fmt.Errorf("save next approval: %w", err)
		}
	} else if publication.ReplaceThread {
		if err := replaceThread(ctx, tx, req.UserID, req.ThreadID, publication.Messages); err != nil {
			return fmt.Errorf("save final thread: %w", err)
		}
	}
	if err := completeApproval(ctx, tx, req); err != nil {
		return fmt.Errorf("save approval receipt: %w", err)
	}
	if err := writeRun(ctx, tx, req.UserID, publication.Result, publication.Retention); err != nil {
		return fmt.Errorf("save run result: %w", err)
	}
	return tx.Commit()
}

var _ agent.ResumePublisher = (*Backend)(nil)
