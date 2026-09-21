package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/google/uuid"
)

const runTimeout = 2 * time.Minute

// References include waiters, so idle locks can be reclaimed without splitting a queue.
type runLock struct {
	gate chan struct{}
	refs int
}

func (r *Runner) lockThread(ctx context.Context, user, thread string) (func(), error) {
	key := threadKey{userID: user, threadID: thread}
	r.locksMu.Lock()
	lock := r.threadLocks[key]
	if lock == nil {
		lock = &runLock{gate: make(chan struct{}, 1)}
		r.threadLocks[key] = lock
	}
	lock.refs++
	r.locksMu.Unlock()
	releaseRef := func() {
		r.locksMu.Lock()
		defer r.locksMu.Unlock()
		lock.refs--
		if lock.refs == 0 {
			delete(r.threadLocks, key)
		}
	}
	select {
	case lock.gate <- struct{}{}:
		localUnlock := func() { <-lock.gate; releaseRef() }
		if distributed, ok := r.threads.(DistributedThreadLocker); ok {
			distributedUnlock, err := distributed.LockThread(ctx, user, thread)
			if err != nil {
				localUnlock()
				return nil, err
			}
			return func() { distributedUnlock(); localUnlock() }, nil
		}
		return localUnlock, nil
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	}
}

func runFailure(id string, err error) ChatRunResult {
	status := StatusError
	if errors.Is(err, context.Canceled) {
		status = StatusCancelled
	}
	return ChatRunResult{RunID: id, Status: status, Answer: err.Error()}
}

// ChatContext serializes a conversation's read/execute/write transaction and carries
// cancellation through every model step, retry wait and context-aware tool.
func (r *Runner) ChatContext(parent context.Context, ac *auth.AuthContext, thread, message string, opts ...ChatOption) ChatRunResult {
	ctx, cancel := context.WithTimeout(parent, runTimeout)
	defer cancel()
	unlock, err := r.lockThread(ctx, ac.UserID, thread)
	if err != nil {
		return runFailure("", err)
	}
	defer unlock()
	r.runtimeMu.RLock()
	defer r.runtimeMu.RUnlock()
	id := "r_" + uuid.New().String()
	ctx = injectAuthContext(ctx, ac, thread, id)
	pendingApprovals, err := r.hitlSvc.ListPendingE(ctx, ac.UserID)
	if err != nil {
		return runFailure(id, fmt.Errorf("读取待审批任务失败：%w", err))
	}
	for _, pending := range pendingApprovals {
		if pending.ThreadID == thread {
			return runFailure(id, fmt.Errorf("此会话有待处理审批，请先完成审批"))
		}
	}
	if r.steppedRunner == nil {
		return runFailure(id, fmt.Errorf("resumable runner is unavailable"))
	}
	options := &chatOptions{}
	for _, option := range opts {
		option(options)
	}
	prompt := r.buildSystemPrompt(ac.Roles)
	if r.steppedRunner.HasSubAgents() {
		prompt = r.buildSupervisorPrompt(ac.Roles)
	}
	if mem := r.memorySvc.RetrieveRelevant(ctx, ac.UserID, message, 0, true); mem != "" {
		prompt += "\n\n" + mem
	}
	threadMessages, err := r.copyThread(ctx, ac.UserID, thread)
	if err != nil {
		return runFailure(id, fmt.Errorf("读取对话失败：%w", err))
	}
	messages := append([]*schema.Message{schema.SystemMessage(prompt)}, threadMessages...)
	messages = append(messages, schema.UserMessage(message))
	recorder := NewEventRecorder(id)
	if options.sink != nil {
		recorder.SetSink(options.sink)
	}
	// Compact only what is sent to the model. The complete exchange is kept in
	// Messages so persisting this run never replaces the conversation with its
	// summary — and so the next turn compacts the original text, not a summary
	// of a summary.
	full := sanitizeMessages(messages)
	compacted, tokens := r.compressMessages(ctx, full, recorder)
	state := &SteppedRunState{RunID: id, ThreadID: thread, Messages: toSchemaMessages(full)}
	if tokens != nil && tokens.Compressed {
		state.ModelContext = toSchemaMessages(compacted)
	}
	result, _ := r.advance(ctx, ac, state, recorder, options.confirmBeforeExecute, false)
	result.ContextTokens = tokens
	if result.Status == StatusCompleted {
		_ = r.memorySvc.ExtractAndSave(ctx, ac.UserID, thread, message)
	}
	if err := r.rememberResult(ctx, ac.UserID, result); err != nil {
		return runFailure(id, fmt.Errorf("保存运行事件失败：%w", err))
	}
	return result
}

func (r *Runner) copyThread(ctx context.Context, userID, threadID string) ([]*schema.Message, error) {
	if store, ok := r.threads.(ContextThreadStore); ok {
		return store.CopyContext(ctx, userID, threadID)
	}
	return r.threads.Copy(userID, threadID), nil
}

func (r *Runner) listThreads(ctx context.Context, userID string) ([]string, error) {
	if store, ok := r.threads.(ContextThreadStore); ok {
		return store.ListContext(ctx, userID)
	}
	return r.threads.List(userID), nil
}

func (r *Runner) rememberResult(ctx context.Context, user string, result ChatRunResult) error {
	if r.runStore != nil {
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return r.runStore.Save(persistCtx, user, result, r.runRetention)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[result.RunID] = &result
	r.runOwners[result.RunID] = user
	return nil
}

func (r *Runner) completedApprovalResult(ctx context.Context, req *hitl.ApprovalRequest) ChatRunResult {
	var result ChatRunResult
	if err := json.Unmarshal(req.Result, &result); err != nil {
		return runFailure(req.RunID, err)
	}
	_, found, err := r.GetRunContext(ctx, req.RunID, req.UserID)
	if err != nil {
		return runFailure(req.RunID, fmt.Errorf("读取运行事件失败：%w", err))
	}
	// A receipt can be durable even when the following event write failed.
	// Restore it on retry, but never overwrite a newer approval result or
	// resurrect a run after its retention period.
	if !found && (r.runStore == nil || req.DecidedAt != nil && time.Since(*req.DecidedAt) < r.runRetention) {
		if err := r.rememberResult(ctx, req.UserID, result); err != nil {
			return runFailure(req.RunID, fmt.Errorf("保存运行事件失败：%w", err))
		}
	}
	return result
}

// advance is shared by initial execution and every subsequent approval.
// Resumed PostgreSQL runs defer writes so the receipt and resulting state can
// be committed together after execution.
func (r *Runner) advance(ctx context.Context, ac *auth.AuthContext, state *SteppedRunState, recorder *EventRecorder, confirm, deferWrites bool) (ChatRunResult, *InterruptPublication) {
	for !state.Done {
		_, interrupt, err := r.steppedRunner.RunStep(ctx, state, recorder, confirm)
		if err != nil {
			result := runFailure(state.RunID, err)
			result.Events = recorder.Events()
			return result, nil
		}
		if interrupt != nil {
			result, publication, err := prepareInterrupt(ac, state, interrupt)
			if err == nil && !deferWrites {
				err = r.publishInterrupt(ctx, publication)
			}
			if err != nil {
				failure := runFailure(state.RunID, err)
				failure.Events = recorder.Events()
				return failure, nil
			}
			result.Events = recorder.Events()
			return result, publication
		}
	}
	if !deferWrites {
		if err := r.saveHistory(ctx, ac.UserID, state); err != nil {
			return runFailure(state.RunID, fmt.Errorf("保存对话失败：%w", err)), nil
		}
	}
	return ChatRunResult{RunID: state.RunID, Status: StatusCompleted, Answer: state.Answer, Events: recorder.Events(), RoutedAgent: "assistant"}, nil
}

func (r *Runner) saveHistory(ctx context.Context, user string, state *SteppedRunState) error {
	return r.replaceHistory(ctx, user, state.ThreadID, historyMessages(state))
}

func (r *Runner) replaceHistory(ctx context.Context, user, thread string, messages []*schema.Message) error {
	if store, ok := r.threads.(ContextThreadMutator); ok {
		return store.ReplaceContext(ctx, user, thread, messages)
	}
	return r.threads.Replace(user, thread, messages)
}

func historyMessages(state *SteppedRunState) []*schema.Message {
	messages := fromSchemaMessages(state.Messages)
	if len(messages) > 0 && messages[0].Role == schema.System {
		messages = messages[1:]
	}
	return messages
}

func prepareInterrupt(ac *auth.AuthContext, state *SteppedRunState, in *InterruptRequest) (ChatRunResult, *InterruptPublication, error) {
	now := time.Now()
	req := &hitl.ApprovalRequest{
		InterruptID: in.InterruptID, Type: in.Type, RunID: state.RunID, ThreadID: state.ThreadID, UserID: ac.UserID,
		ToolCallID: in.ToolCallID, ToolName: in.ToolName, NodeName: in.NodeName, Arguments: in.Arguments, Message: in.Message,
		Status: hitl.StatusPending, CreatedAt: now, Payload: map[string]any{"plan": in.Plan},
	}
	if in.Type == hitl.InterruptTypeTool {
		req.RiskLevel = "high"
	}
	var err error
	req.State, err = state.Serialize()
	if err != nil {
		return ChatRunResult{}, nil, err
	}
	result := ChatRunResult{RunID: state.RunID, Status: StatusInterrupted, Interrupt: &Interrupt{
		InterruptID: req.InterruptID, Type: string(in.Type), ToolName: in.ToolName, NodeName: in.NodeName, Arguments: in.Arguments, Message: in.Message, Plan: in.Plan,
	}}
	return result, &InterruptPublication{
		Checkpoint: memory.Checkpoint{
			UserID: ac.UserID, ThreadID: state.ThreadID, RunID: state.RunID, State: req.State,
			Interrupted: true, Snapshot: true, CreatedAt: now, UpdatedAt: now,
		},
		Messages: historyMessages(state),
		Approval: req,
	}, nil
}

func (r *Runner) publishInterrupt(ctx context.Context, publication *InterruptPublication) error {
	if r.interruptPublisher != nil {
		return r.interruptPublisher.PublishInterrupt(ctx, *publication)
	}
	if err := r.memorySvc.GetCheckpointStore().Save(ctx, publication.Checkpoint); err != nil {
		return err
	}
	if err := r.replaceHistory(ctx, publication.Checkpoint.UserID, publication.Checkpoint.ThreadID, publication.Messages); err != nil {
		return err
	}
	if err := r.hitlSvc.SaveApprovalContext(ctx, publication.Approval); err != nil {
		return err
	}
	return nil
}

// ResumeContext persists a claim before side effects and a receipt before replying.
// A crash in between has an unknown outcome: refuse automatic replay rather than
// possibly sending an email/deleting an order twice. External transactional tools
// can later reconcile this with their own idempotency keys.
func (r *Runner) ResumeContext(parent context.Context, ac *auth.AuthContext, id string, decision hitl.ApprovalDecision) ChatRunResult {
	ctx, cancel := context.WithTimeout(parent, runTimeout)
	defer cancel()
	req, ok, err := r.hitlSvc.GetApprovalContext(ctx, id)
	if err != nil {
		return runFailure("", fmt.Errorf("读取审批请求失败：%w", err))
	}
	if !ok || req.UserID != ac.UserID {
		return runFailure("", fmt.Errorf("审批请求不存在"))
	}
	unlock, err := r.lockThread(ctx, ac.UserID, req.ThreadID)
	if err != nil {
		return runFailure(req.RunID, err)
	}
	defer unlock()
	r.runtimeMu.RLock()
	defer r.runtimeMu.RUnlock()
	req, ok, err = r.hitlSvc.GetApprovalContext(ctx, id)
	if err != nil {
		return runFailure(req.RunID, fmt.Errorf("重新读取审批请求失败：%w", err))
	}
	if !ok || req.UserID != ac.UserID {
		return runFailure("", fmt.Errorf("审批请求不存在"))
	}
	if req.Decision != nil && req.Decision.Approved != decision.Approved {
		return runFailure(req.RunID, fmt.Errorf("审批已经作出不同决定"))
	}
	if len(req.Result) > 0 {
		return r.completedApprovalResult(ctx, req)
	}
	if req.Phase == "running" {
		return runFailure(req.RunID, fmt.Errorf("上次执行结果未确认，需要核对业务结果后恢复；已阻止重复执行"))
	}
	state, err := DeserializeRunState(req.State)
	if err != nil {
		return runFailure(req.RunID, fmt.Errorf("无法恢复审批状态：%w", err))
	}
	if state.RunID != req.RunID || state.ThreadID != req.ThreadID || r.steppedRunner == nil {
		return runFailure(req.RunID, fmt.Errorf("审批与运行状态不匹配"))
	}
	if err := ctx.Err(); err != nil {
		return runFailure(req.RunID, err)
	}
	ctx = injectAuthContext(ctx, ac, req.ThreadID, req.RunID)
	previous, found, err := r.GetRunContext(ctx, req.RunID, ac.UserID)
	if err != nil {
		return runFailure(req.RunID, fmt.Errorf("读取运行事件失败：%w", err))
	}
	var earlier []Event
	if found {
		earlier = previous.Events
	}
	claimToken := uuid.New().String()
	runID := req.RunID
	req, claimed, err := r.hitlSvc.ClaimApproval(ctx, id, ac.UserID, decision, claimToken)
	if err != nil {
		return runFailure(runID, fmt.Errorf("保存审批执行凭据失败：%w", err))
	}
	if !claimed {
		if req == nil {
			return runFailure("", fmt.Errorf("审批请求不存在"))
		}
		if req.Decision != nil && req.Decision.Approved != decision.Approved {
			return runFailure(req.RunID, fmt.Errorf("审批已经作出不同决定"))
		}
		if len(req.Result) > 0 {
			return r.completedApprovalResult(ctx, req)
		}
		return runFailure(req.RunID, fmt.Errorf("审批已被其他实例领取或上次执行结果未确认，需要核对业务结果后恢复"))
	}
	_, err = r.steppedRunner.HandleApproval(ctx, state, &InterruptRequest{Type: req.Type, ToolCallID: req.ToolCallID, ToolName: req.ToolName, Arguments: req.Arguments, NodeName: req.NodeName}, decision.Approved, decision.Reason)
	var result ChatRunResult
	var next *InterruptPublication
	if err != nil {
		result = runFailure(req.RunID, err)
		result.Events = earlier
	} else {
		result, next = r.advance(ctx, ac, state, ContinueEventRecorder(req.RunID, earlier), false, r.resumePublisher != nil)
	}
	req.State, err = state.Serialize()
	if err != nil {
		return runFailure(req.RunID, err)
	}
	req.Result, err = json.Marshal(result)
	if err != nil {
		return runFailure(req.RunID, err)
	}
	req.Phase = "finished"
	if r.resumePublisher != nil {
		publication := ResumePublication{Approval: req, Result: result, Next: next, Retention: r.runRetention}
		if result.Status == StatusCompleted {
			publication.Messages = historyMessages(state)
			publication.ReplaceThread = true
		}
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := r.resumePublisher.PublishResume(persistCtx, publication); err != nil {
			return runFailure(req.RunID, fmt.Errorf("保存执行结果失败，禁止重复执行，请核对业务结果：%w", err))
		}
		return result
	}
	if err := r.hitlSvc.CompleteApproval(ctx, req); err != nil {
		return runFailure(req.RunID, fmt.Errorf("保存执行结果失败，禁止重复执行，请核对业务结果：%w", err))
	}
	if err := r.rememberResult(ctx, ac.UserID, result); err != nil {
		return runFailure(req.RunID, fmt.Errorf("保存运行事件失败：%w", err))
	}
	return result
}
