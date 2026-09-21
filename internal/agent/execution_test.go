package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/tools"
)

type scriptModel struct {
	*MockChatModel
	generateFn func(context.Context, []*schema.Message) (*schema.Message, error)
}

func (m *scriptModel) Generate(ctx context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return m.generateFn(ctx, msgs)
}
func (m *scriptModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func scripted(fn func(context.Context, []*schema.Message) (*schema.Message, error)) *scriptModel {
	return &scriptModel{MockChatModel: NewMockChatModel(), generateFn: fn}
}
func calls(names ...string) *schema.Message {
	var tc []schema.ToolCall
	for i, name := range names {
		tc = append(tc, schema.ToolCall{ID: fmt.Sprintf("call-%d", i), Function: schema.FunctionCall{Name: name, Arguments: `{"message":"task"}`}})
	}
	return schema.AssistantMessage("", tc)
}
func sequence(names ...string) *scriptModel {
	return scripted(func(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
		if msgs[len(msgs)-1].Role == schema.User {
			return calls(names...), nil
		}
		// Validate that every call has exactly one result before another model request.
		expected := map[string]int{}
		for _, msg := range msgs {
			for _, tc := range msg.ToolCalls {
				expected[tc.ID]++
			}
			if msg.Role == schema.Tool {
				expected[msg.ToolCallID]--
			}
		}
		for id, n := range expected {
			if n != 0 {
				return nil, fmt.Errorf("unpaired call %s (%d)", id, n)
			}
		}
		return schema.AssistantMessage("done", nil), nil
	})
}
func testIdentity() *auth.AuthContext {
	return &auth.AuthContext{UserID: "u_admin", Roles: []string{"admin"}}
}
func testRuntime(t *testing.T, m model.ToolCallingChatModel, registry *tools.ToolRegistry, entries []*DispatchEntry, dir string) *Runner {
	t.Helper()
	var cp memory.CheckpointStore = memory.NewInMemoryCheckpointStore()
	if dir != "" {
		var err error
		cp, err = memory.NewFileCheckpointStore(filepath.Join(dir, "cp.json"))
		if err != nil {
			t.Fatal(err)
		}
	}
	manager := hitl.NewInterruptManager(cp)
	if dir != "" {
		if err := manager.UseFile(filepath.Join(dir, "approvals.json")); err != nil {
			t.Fatal(err)
		}
	}
	svc := hitl.NewService(manager, cp, nil)
	mem := memory.NewService(memory.NewInMemoryMemoryStore(), cp, memory.NewInMemoryVectorStore(), nil)
	stepped := NewSteppedRunner(m, registry, svc, 20, entries, nil)
	stepped.retryDelay = time.Millisecond
	runner := NewRunner(nil, stepped, svc, registry, nil, mem, nil, 4096)
	if dir != "" {
		if err := runner.UseFileThreads(filepath.Join(dir, "threads.json")); err != nil {
			t.Fatal(err)
		}
	}
	return runner
}
func countedTool(t *testing.T, registry *tools.ToolRegistry, name string, high bool, counter *atomic.Int32) *DispatchEntry {
	t.Helper()
	if err := registry.Register(tools.RegisteredTool{Meta: tools.ToolMeta{Name: name, RequiresApproval: high}, Fn: func(identity *auth.ToolIdentity, _ string) tools.ToolResult {
		if identity == nil || identity.Context == nil {
			return tools.SystemErrorResult(name, "missing execution context", "")
		}
		counter.Add(1)
		return tools.SuccessResult(name, "ok")
	}}); err != nil {
		t.Fatal(err)
	}
	return &DispatchEntry{Info: &schema.ToolInfo{Name: name}, ToolName: name, RequiresApproval: high}
}
func mustInterrupt(t *testing.T, result ChatRunResult) string {
	t.Helper()
	if result.Status != StatusInterrupted || result.Interrupt == nil {
		t.Fatalf("expected actionable interrupt: %+v", result)
	}
	return result.Interrupt.InterruptID
}

func TestExecution_RateLimitBoundedAndErrorsReported(t *testing.T) {
	var n atomic.Int32
	m := scripted(func(context.Context, []*schema.Message) (*schema.Message, error) {
		n.Add(1)
		return nil, errors.New("429 rate limit")
	})
	r := testRuntime(t, m, tools.NewToolRegistry(), nil, "")
	result := r.Chat(testIdentity(), "thread", "hello")
	if result.Status != StatusError || n.Load() != 4 {
		t.Fatalf("status=%s attempts=%d", result.Status, n.Load())
	}
}
func TestExecution_CancellationStopsBackoffAndModel(t *testing.T) {
	for _, kind := range []string{"backoff", "model"} {
		t.Run(kind, func(t *testing.T) {
			entered := make(chan struct{})
			var n atomic.Int32
			m := scripted(func(ctx context.Context, _ []*schema.Message) (*schema.Message, error) {
				if n.Add(1) == 1 {
					close(entered)
				}
				if kind == "model" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return nil, errors.New("429")
			})
			r := testRuntime(t, m, tools.NewToolRegistry(), nil, "")
			r.steppedRunner.retryDelay = time.Hour
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan ChatRunResult, 1)
			go func() { done <- r.ChatContext(ctx, testIdentity(), "thread", "hello") }()
			<-entered
			cancel()
			select {
			case result := <-done:
				if result.Status != "cancelled" {
					t.Fatalf("%+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not stop execution")
			}
			if n.Load() != 1 {
				t.Fatalf("unexpected retry: %d", n.Load())
			}
		})
	}
}
func TestExecution_PlanThenNestedSequentialApprovalsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewToolRegistry()
	var first, second, tail atomic.Int32
	childEntries := []*DispatchEntry{countedTool(t, registry, "first", true, &first), countedTool(t, registry, "second", true, &second)}
	tailEntry := countedTool(t, registry, "tail", false, &tail)
	parentModel := sequence("child", "tail")
	childModel := sequence("first", "second")
	create := func() *Runner {
		r := testRuntime(t, parentModel, registry, nil, dir)
		child := NewSteppedRunner(childModel, registry, r.hitlSvc, 20, childEntries, nil)
		wrapper := &agentToolWrapper{agent: &ReactAgent{config: ReactAgentConfig{Name: "child", Instruction: "child", ToolNames: []string{"first", "second"}}}, steppedRunner: child}
		r.steppedRunner = NewSteppedRunner(parentModel, registry, r.hitlSvc, 20, []*DispatchEntry{{Info: &schema.ToolInfo{Name: "child"}, IsSubAgent: true, AgentWrapper: wrapper}, tailEntry}, nil)
		return r
	}
	r := create()
	plan := r.Chat(testIdentity(), "t", "task", WithConfirmBeforeExecute())
	planID := mustInterrupt(t, plan)
	r = create()
	one := r.Resume(testIdentity(), planID, hitl.ApprovalDecision{Approved: true})
	firstID := mustInterrupt(t, one)
	if one.RunID != plan.RunID || first.Load() != 0 || tail.Load() != 0 {
		t.Fatalf("early execution: %+v", one)
	}
	r = create()
	two := r.Resume(testIdentity(), firstID, hitl.ApprovalDecision{Approved: true})
	secondID := mustInterrupt(t, two)
	if first.Load() != 1 || second.Load() != 0 || tail.Load() != 0 {
		t.Fatal("wrong execution order")
	}
	r = create()
	final := r.Resume(testIdentity(), secondID, hitl.ApprovalDecision{Approved: true})
	if final.Status != StatusCompleted || first.Load() != 1 || second.Load() != 1 || tail.Load() != 1 {
		t.Fatalf("bad final result %+v, counts=%d,%d,%d", final, first.Load(), second.Load(), tail.Load())
	}
	r = create()
	retry := r.Resume(testIdentity(), secondID, hitl.ApprovalDecision{Approved: true})
	if retry.Status != StatusCompleted || retry.Answer != final.Answer || second.Load() != 1 {
		t.Fatal("durable receipt did not prevent replay")
	}
	if len(r.hitlSvc.ListPending(context.Background(), "u_admin")) != 0 {
		t.Fatal("left pending approvals")
	}
	opposite := r.Resume(testIdentity(), secondID, hitl.ApprovalDecision{Approved: false})
	if opposite.Status != StatusError {
		t.Fatal("conflicting decision accepted")
	}
}
func TestExecution_ConcurrentApprovalExecutesOnce(t *testing.T) {
	registry := tools.NewToolRegistry()
	var count atomic.Int32
	entry := countedTool(t, registry, "send", true, &count)
	r := testRuntime(t, sequence("send"), registry, []*DispatchEntry{entry}, "")
	id := mustInterrupt(t, r.Chat(testIdentity(), "t", "task"))
	var wg sync.WaitGroup
	results := make(chan ChatRunResult, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- r.Resume(testIdentity(), id, hitl.ApprovalDecision{Approved: true})
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result.Status != StatusCompleted {
			t.Fatalf("%+v", result)
		}
	}
	if count.Load() != 1 {
		t.Fatalf("executed %d times", count.Load())
	}
}
func TestExecution_DirectBatchDoesNotReplayEarlierTools(t *testing.T) {
	registry := tools.NewToolRegistry()
	var low, high atomic.Int32
	entries := []*DispatchEntry{countedTool(t, registry, "low", false, &low), countedTool(t, registry, "high", true, &high)}
	r := testRuntime(t, sequence("low", "high", "high"), registry, entries, "")
	id := mustInterrupt(t, r.Chat(testIdentity(), "t", "task"))
	id2 := mustInterrupt(t, r.Resume(testIdentity(), id, hitl.ApprovalDecision{Approved: true}))
	if low.Load() != 1 || high.Load() != 1 {
		t.Fatal("earlier tool replayed or same-name calls executed together")
	}
	final := r.Resume(testIdentity(), id2, hitl.ApprovalDecision{Approved: false, Reason: "skip"})
	if final.Status != StatusCompleted || low.Load() != 1 || high.Load() != 1 {
		t.Fatalf("%+v", final)
	}
}
func TestExecution_PlanRejectionKeepsToolPairs(t *testing.T) {
	registry := tools.NewToolRegistry()
	var count atomic.Int32
	entry := countedTool(t, registry, "low", false, &count)
	r := testRuntime(t, sequence("low", "low"), registry, []*DispatchEntry{entry}, "")
	id := mustInterrupt(t, r.Chat(testIdentity(), "t", "task", WithConfirmBeforeExecute()))
	result := r.Resume(testIdentity(), id, hitl.ApprovalDecision{Approved: false, Reason: "skip"})
	if result.Status != StatusCompleted || count.Load() != 0 {
		t.Fatalf("%+v", result)
	}
}
func TestExecution_ConcurrentConversationKeepsEveryTurn(t *testing.T) {
	m := scripted(func(context.Context, []*schema.Message) (*schema.Message, error) {
		return schema.AssistantMessage("hello", nil), nil
	})
	r := testRuntime(t, m, tools.NewToolRegistry(), nil, "")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := r.Chat(testIdentity(), "t", "hello")
			if result.Status != StatusCompleted {
				t.Errorf("%+v", result)
			}
		}()
	}
	wg.Wait()
	if n := len(r.GetThreadMessages("u_admin", "t")); n != 32 {
		t.Fatalf("lost turns: %d messages", n)
	}
	if len(r.threadLocks) != 0 {
		t.Fatal("idle lock leaked")
	}
}
func TestExecution_ApprovalReceiptWriteFailurePreventsTool(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewToolRegistry()
	var count atomic.Int32
	entry := countedTool(t, registry, "send", true, &count)
	r := testRuntime(t, sequence("send"), registry, []*DispatchEntry{entry}, dir)
	id := mustInterrupt(t, r.Chat(testIdentity(), "t", "task"))
	// A directory where the temporary file should be forces a deterministic write failure.
	if err := os.Mkdir(filepath.Join(dir, "approvals.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	result := r.Resume(testIdentity(), id, hitl.ApprovalDecision{Approved: true})
	if result.Status != StatusError || count.Load() != 0 {
		t.Fatalf("%+v executed=%d", result, count.Load())
	}
}
func TestExecution_UncertainOutcomeBlocksReplayAfterRestart(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewToolRegistry()
	var count atomic.Int32
	entry := countedTool(t, registry, "send", true, &count)
	r := testRuntime(t, sequence("send"), registry, []*DispatchEntry{entry}, dir)
	id := mustInterrupt(t, r.Chat(testIdentity(), "t", "task"))
	req, _ := r.hitlSvc.GetApproval(id)
	req.Phase = "running"
	req.Decision = &hitl.ApprovalDecision{Approved: true}
	if err := r.hitlSvc.SaveApproval(req); err != nil {
		t.Fatal(err)
	}
	r = testRuntime(t, sequence("send"), registry, []*DispatchEntry{entry}, dir)
	result := r.Resume(testIdentity(), id, hitl.ApprovalDecision{Approved: true})
	if result.Status != StatusError || !strings.Contains(result.Answer, "核对") || count.Load() != 0 {
		t.Fatalf("%+v", result)
	}
}
func TestExecution_ThreadWriteFailureIsNotSuccess(t *testing.T) {
	dir := t.TempDir()
	r := testRuntime(t, sequence(), tools.NewToolRegistry(), nil, dir)
	if err := os.Mkdir(filepath.Join(dir, "threads.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	result := r.Chat(testIdentity(), "t", "hello")
	if result.Status != StatusError || len(r.GetThreadMessages("u_admin", "t")) != 0 {
		t.Fatalf("%+v", result)
	}
}

func TestExecution_ResultWriteFailureBlocksReplay(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewToolRegistry()
	var count atomic.Int32
	if err := registry.Register(tools.RegisteredTool{Meta: tools.ToolMeta{Name: "send", RequiresApproval: true}, Fn: func(_ *auth.ToolIdentity, _ string) tools.ToolResult {
		count.Add(1)
		if err := os.Mkdir(filepath.Join(dir, "approvals.json.tmp"), 0700); err != nil {
			t.Error(err)
		}
		return tools.SuccessResult("send", "sent")
	}}); err != nil {
		t.Fatal(err)
	}
	entries := []*DispatchEntry{{Info: &schema.ToolInfo{Name: "send"}, ToolName: "send", RequiresApproval: true}}
	r := testRuntime(t, sequence("send"), registry, entries, dir)
	id := mustInterrupt(t, r.Chat(testIdentity(), "t", "task"))
	result := r.Resume(testIdentity(), id, hitl.ApprovalDecision{Approved: true})
	if result.Status != StatusError || count.Load() != 1 {
		t.Fatalf("%+v executed=%d", result, count.Load())
	}
	r = testRuntime(t, sequence("send"), registry, entries, dir)
	result = r.Resume(testIdentity(), id, hitl.ApprovalDecision{Approved: true})
	if result.Status != StatusError || count.Load() != 1 || !strings.Contains(result.Answer, "核对") {
		t.Fatalf("%+v executed=%d", result, count.Load())
	}
}
func TestExecution_ResumeRateLimitDoesNotReplayTool(t *testing.T) {
	registry := tools.NewToolRegistry()
	var count, attempts atomic.Int32
	entry := countedTool(t, registry, "send", true, &count)
	m := scripted(func(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
		if msgs[len(msgs)-1].Role == schema.User {
			return calls("send"), nil
		}
		attempts.Add(1)
		return nil, errors.New("429 rate limit")
	})
	r := testRuntime(t, m, registry, []*DispatchEntry{entry}, "")
	id := mustInterrupt(t, r.Chat(testIdentity(), "t", "task"))
	result := r.Resume(testIdentity(), id, hitl.ApprovalDecision{Approved: true})
	retry := r.Resume(testIdentity(), id, hitl.ApprovalDecision{Approved: true})
	if result.Status != StatusError || retry.Status != StatusError || count.Load() != 1 || attempts.Load() != 4 {
		t.Fatalf("%+v tool=%d attempts=%d", result, count.Load(), attempts.Load())
	}
}
func TestExecution_MaxStepsAndSystemToolFailureAreErrors(t *testing.T) {
	registry := tools.NewToolRegistry()
	var count atomic.Int32
	entry := countedTool(t, registry, "low", false, &count)
	r := testRuntime(t, sequence("low"), registry, []*DispatchEntry{entry}, "")
	r.steppedRunner.maxSteps = 1
	if result := r.Chat(testIdentity(), "t", "task"); result.Status != StatusError {
		t.Fatalf("%+v", result)
	}
	registry = tools.NewToolRegistry()
	registry.Register(tools.RegisteredTool{Meta: tools.ToolMeta{Name: "failed"}, Fn: func(*auth.ToolIdentity, string) tools.ToolResult {
		return tools.SystemErrorResult("failed", "disk unavailable", "")
	}})
	r = testRuntime(t, sequence("failed"), registry, []*DispatchEntry{{Info: &schema.ToolInfo{Name: "failed"}, ToolName: "failed"}}, "")
	if result := r.Chat(testIdentity(), "t", "task"); result.Status != StatusError {
		t.Fatalf("%+v", result)
	}
}

func TestExecution_CheckpointWriteFailureDoesNotPublishApproval(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewToolRegistry()
	var count atomic.Int32
	entry := countedTool(t, registry, "send", true, &count)
	r := testRuntime(t, sequence("send"), registry, []*DispatchEntry{entry}, dir)
	if err := os.Mkdir(filepath.Join(dir, "cp.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	result := r.Chat(testIdentity(), "t", "task")
	if result.Status != StatusError || count.Load() != 0 || len(r.hitlSvc.ListPending(context.Background(), "u_admin")) != 0 {
		t.Fatalf("partial approval published: %+v", result)
	}
	checkpoints, err := r.memorySvc.GetCheckpointStore().ListByThread(context.Background(), "u_admin", "t")
	if err != nil || len(checkpoints) != 0 {
		t.Fatalf("failed write visible in memory: %v %v", checkpoints, err)
	}
}
func TestExecution_ToolReceivesRequestCancellation(t *testing.T) {
	entered := make(chan struct{})
	registry := tools.NewToolRegistry()
	registry.Register(tools.RegisteredTool{Meta: tools.ToolMeta{Name: "wait"}, Fn: func(identity *auth.ToolIdentity, _ string) tools.ToolResult {
		close(entered)
		<-identity.Context.Done()
		return tools.SystemErrorResult("wait", identity.Context.Err().Error(), "")
	}})
	r := testRuntime(t, sequence("wait"), registry, []*DispatchEntry{{Info: &schema.ToolInfo{Name: "wait"}, ToolName: "wait"}}, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan ChatRunResult, 1)
	go func() { done <- r.ChatContext(ctx, testIdentity(), "t", "task") }()
	<-entered
	cancel()
	select {
	case result := <-done:
		if result.Status != StatusCancelled {
			t.Fatalf("%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("tool did not stop")
	}
}
func TestExecution_QueuedCancellationDoesNotDiscardMessages(t *testing.T) {
	r := testRuntime(t, sequence(), tools.NewToolRegistry(), nil, "")
	unlock, err := r.lockThread(context.Background(), "u_admin", "t")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result := r.ChatContext(ctx, testIdentity(), "t", "hello")
	unlock()
	if result.Status != StatusError || len(r.GetThreadMessages("u_admin", "t")) != 0 || len(r.threadLocks) != 0 {
		t.Fatalf("%+v", result)
	}
}
