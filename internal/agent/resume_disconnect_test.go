package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/tools"
)

// TestExecution_ResumeCompletesAfterCallerDisconnects pins the semantics of the
// post-claim context: once the approval is claimed the decision is durable and
// the gated tool may already have run, so the caller's connection must not be
// able to cancel the work.
//
// The failure this guards against is not merely a lost response — the receipt
// would record `cancelled`, and because the claim is already consumed the
// approval could never be retried, leaving a side effect that happened recorded
// as cancelled.
func TestExecution_ResumeCompletesAfterCallerDisconnects(t *testing.T) {
	registry := tools.NewToolRegistry()
	var count atomic.Int32
	entry := countedTool(t, registry, "gated", true, &count)

	// The model blocks after the tool result, which is the window between the
	// side effect and the receipt write.
	var once sync.Once
	toolResultSeen := make(chan struct{})
	release := make(chan struct{})
	m := scripted(func(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
		if msgs[len(msgs)-1].Role == schema.Tool {
			once.Do(func() { close(toolResultSeen) })
			<-release
			return schema.AssistantMessage("已完成", nil), nil
		}
		return calls("gated"), nil
	})
	r := testRuntime(t, m, registry, []*DispatchEntry{entry}, "")
	id := mustInterrupt(t, r.Chat(testIdentity(), "thread", "执行高危操作"))

	// The caller's context is cancellable, like an HTTP request context.
	callerCtx, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	done := make(chan ChatRunResult, 1)
	go func() {
		done <- r.ResumeContext(callerCtx, testIdentity(), id, hitl.ApprovalDecision{Approved: true})
	}()

	// Wait until the tool has executed and the run is mid-flight, then walk away.
	<-toolResultSeen
	cancelCaller()
	close(release)

	result := <-done
	if result.Status != StatusCompleted {
		t.Fatalf("a disconnect must not cancel an authorised resume, got status=%s answer=%q", result.Status, result.Answer)
	}
	if count.Load() != 1 {
		t.Fatalf("the gated tool ran %d times, want exactly 1", count.Load())
	}

	// The stored receipt must record the completed run, so a retry replays it
	// instead of reporting an uncertain outcome.
	stored, found, err := r.GetRunContext(context.Background(), result.RunID, "u_admin")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("no run result was stored")
	}
	if stored.Status != StatusCompleted {
		t.Fatalf("stored receipt says %s, want %s", stored.Status, StatusCompleted)
	}
}
