package agent

import (
	"context"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
)

// InterruptPublication is the durable state required to resume one interrupt.
type InterruptPublication struct {
	Checkpoint memory.Checkpoint
	Messages   []*schema.Message
	Approval   *hitl.ApprovalRequest
}

// InterruptPublisher commits all three records as one operation.
type InterruptPublisher interface {
	PublishInterrupt(ctx context.Context, publication InterruptPublication) error
}

// ResumePublication is the durable outcome of one claimed approval.
// A completed run replaces the thread; a new interrupt publishes Next.
type ResumePublication struct {
	Approval      *hitl.ApprovalRequest
	Result        ChatRunResult
	Messages      []*schema.Message
	ReplaceThread bool
	Next          *InterruptPublication
	Retention     time.Duration
}

// ResumePublisher commits the receipt and run events with any thread change
// or follow-on interrupt in one operation.
type ResumePublisher interface {
	PublishResume(ctx context.Context, publication ResumePublication) error
}
