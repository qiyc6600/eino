package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/contextmgr"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/tools"
	"github.com/google/uuid"
)

// defaultMaxToolResultChars caps a single tool result before it enters the
// conversation, so one verbose response cannot crowd out the rest of the window.
const defaultMaxToolResultChars = 8000

// DispatchEntry represents a single entry in the SteppedRunner's dispatch table.
// It abstracts over "real tool" and "sub-agent tool" uniformly.
type DispatchEntry struct {
	Info             *schema.ToolInfo  // tool definition for LLM binding
	IsSubAgent       bool              // true = delegate to agentToolWrapper, false = real tool
	RequiresApproval bool              // for real tools: HITL gate needed?
	AgentWrapper     *agentToolWrapper // for sub-agents: the wrapper to call (set only when IsSubAgent)
	ToolName         string            // for real tools: the name in ToolRegistry
}

// SubAgentInterruptError is returned by agentToolWrapper.InvokableRun
// when a sub-agent requires HITL approval for a dangerous tool.
// This allows the SteppedRunner to propagate the interrupt rather than
// treating it as a generic error.
type SubAgentInterruptError struct {
	AgentName string
	ToolName  string
	Arguments string
}

func (e *SubAgentInterruptError) Error() string {
	return fmt.Sprintf("interrupt: sub-agent %s requires approval for %s", e.AgentName, e.ToolName)
}

// isSubAgentInterrupt checks if an error is a SubAgentInterruptError.
func isSubAgentInterrupt(err error) bool {
	var sae *SubAgentInterruptError
	return errors.As(err, &sae)
}

// SteppedRunState holds the execution state of a stepped ReAct loop.
// This state can be serialized to a Checkpoint for interrupt/resume.
type SteppedRunState struct {
	Children map[string]*SteppedRunState `json:"children,omitempty"`
	Step     int                         `json:"step"` // current iteration (0-based)
	// Messages is the authoritative, complete conversation history. It is what
	// gets persisted to the thread store, so compaction never destroys the
	// original exchange.
	Messages []SchemaMessage `json:"messages"`
	// ModelContext is the compacted view of Messages that was sent to the model
	// for this run (summary plus recent turns). It is nil when the run was not
	// compacted, in which case Messages is sent as-is. It is never persisted to
	// the thread store — only carried through checkpoints so a resume continues
	// against the same context.
	ModelContext     []SchemaMessage `json:"model_context,omitempty"`
	PendingToolCalls []ToolCallInfo  `json:"pending_tool_calls"` // tool calls awaiting execution
	Done             bool            `json:"done"`               // whether the loop has completed
	Answer           string          `json:"answer"`             // final answer when done
	RunID            string          `json:"run_id"`             // run identifier
	ThreadID         string          `json:"thread_id"`          // thread identifier
	// Usage accumulates provider-reported token counts across the run's model
	// calls, when the provider reports them.
	Usage *UsageInfo `json:"usage,omitempty"`
	// StoredCount is how many history messages (excluding the system prompt) the
	// thread store held when this run started. Messages past it are this run's own
	// additions, which lets the store append instead of rewriting the whole
	// history. A checkpoint written before this field existed decodes as 0, and
	// the store's length guard then rejects the append in favour of a replace.
	StoredCount int `json:"stored_count,omitempty"`
	// HistoryRepaired is set when orphaned tool calls in the loaded history had to
	// be repaired. That changes messages the store already holds, so the run can
	// no longer be persisted by appending.
	HistoryRepaired bool `json:"history_repaired,omitempty"`
}

// extendsStoredHistory reports whether everything this run produced can be
// written as a suffix of the stored history.
func (s *SteppedRunState) extendsStoredHistory(total int) bool {
	return !s.HistoryRepaired && s.StoredCount <= total
}

// appendMessage records a message in the complete history and, when this run
// was compacted, in the model-facing context as well — keeping the two in step.
func (s *SteppedRunState) appendMessage(msg SchemaMessage) {
	s.Messages = append(s.Messages, msg)
	if s.ModelContext != nil {
		s.ModelContext = append(s.ModelContext, msg)
	}
}

// modelMessages returns the context to send to the model: the compacted view
// when this run was compacted, otherwise the complete history.
func (s *SteppedRunState) modelMessages() []SchemaMessage {
	if len(s.ModelContext) > 0 {
		return s.ModelContext
	}
	return s.Messages
}

// SchemaMessage is a serializable version of schema.Message.
type SchemaMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []ToolCallInfo `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

// ToolCallInfo is a serializable version of schema.ToolCall.
type ToolCallInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// InterruptRequest represents a pending interrupt that needs human approval.
type InterruptRequest struct {
	InterruptID string
	ToolCallID  string
	Type        hitl.InterruptType // "tool" or "node"
	ToolName    string             // for tool-level: the tool name
	Arguments   string             // for tool-level: the tool arguments
	NodeName    string             // for node-level: the node name
	Message     string             // human-readable description
	RunID       string
	ThreadID    string
	// Plan carries the structured planned steps for node-level interrupts.
	Plan []InterruptPlanStep
}

// SteppedRunner executes a ReAct loop step-by-step with interrupt support.
type SteppedRunner struct {
	chatModel   model.ToolCallingChatModel // base model (no tools bound)
	toolModel   model.ToolCallingChatModel // model with tools bound via WithTools
	registry    *tools.ToolRegistry
	hitlSvc     *hitl.Service
	rbac        *auth.RBACManager // RBAC checker for tool-level ACL
	retryDelay  time.Duration
	maxSteps    int
	dispatchMap map[string]*DispatchEntry // name -> entry for O(1) dispatch
	toolInfos   []*schema.ToolInfo        // ordered list for LLM binding
	// toolSchemaTokens is what toolInfos add to every request; computed once
	// because the same definitions are resent on each model call.
	toolSchemaTokens int
	// maxToolResultChars caps a single tool result before it enters the history.
	maxToolResultChars int
}

// ToolSchemaTokens reports the request overhead of the bound tool definitions.
func (r *SteppedRunner) ToolSchemaTokens() int {
	return r.toolSchemaTokens
}

// SetMaxToolResultChars caps how much of a single tool result enters the
// conversation. A non-positive value disables the cap.
func (r *SteppedRunner) SetMaxToolResultChars(n int) {
	r.maxToolResultChars = n
}

// capToolResult truncates an oversized tool result so one verbose response
// cannot crowd out the rest of the window.
func (r *SteppedRunner) capToolResult(content string) string {
	if r.maxToolResultChars <= 0 || len(content) <= r.maxToolResultChars {
		return content
	}
	return content[:r.maxToolResultChars] + fmt.Sprintf("\n…（结果已截断，原始长度 %d 字符）", len(content))
}

// NewSteppedRunner creates a new stepped ReAct runner.
func NewSteppedRunner(chatModel model.ToolCallingChatModel,
	registry *tools.ToolRegistry,
	hitlSvc *hitl.Service,
	maxSteps int,
	dispatchEntries []*DispatchEntry,
	rbac *auth.RBACManager,
) *SteppedRunner {
	dispatchMap := make(map[string]*DispatchEntry, len(dispatchEntries))
	toolInfos := make([]*schema.ToolInfo, 0, len(dispatchEntries))

	for _, entry := range dispatchEntries {
		dispatchMap[entry.Info.Name] = entry
		toolInfos = append(toolInfos, entry.Info)
	}

	// Bind tools to the model so the LLM can return structured tool_calls
	toolModel, err := chatModel.WithTools(toolInfos)
	if err != nil {
		// If WithTools fails, fall back to the base model
		// (some models may not support construction-time binding)
		toolModel = chatModel
	}

	return &SteppedRunner{
		chatModel:          chatModel,
		toolModel:          toolModel,
		registry:           registry,
		hitlSvc:            hitlSvc,
		rbac:               rbac,
		maxSteps:           maxSteps,
		retryDelay:         250 * time.Millisecond,
		dispatchMap:        dispatchMap,
		toolInfos:          toolInfos,
		toolSchemaTokens:   estimateToolSchemaTokens(toolInfos),
		maxToolResultChars: defaultMaxToolResultChars,
	}
}

// HasSubAgents returns true if the dispatch table contains sub-agent entries.
func (r *SteppedRunner) HasSubAgents() bool {
	for _, entry := range r.dispatchMap {
		if entry.IsSubAgent {
			return true
		}
	}
	return false
}

// RunAll runs the full ReAct loop to completion (no interrupts).
// Equivalent to the old supervisor.Run() but using stepped execution internally.
func (r *SteppedRunner) RunAll(ctx context.Context, messages []*schema.Message, recorder *EventRecorder) SupervisorRunResult {
	state := &SteppedRunState{
		Step:     0,
		Messages: toSchemaMessages(messages),
		Done:     false,
		RunID:    "r_" + uuid.New().String(),
	}

	var interruptReq *InterruptRequest
	var err error

	for !state.Done {
		state, interruptReq, err = r.RunStep(ctx, state, recorder, false)
		if err != nil {
			return SupervisorRunResult{Answer: fmt.Sprintf("Error at step %d: %v", state.Step, err)}
		}
		if interruptReq != nil {
			return SupervisorRunResult{Answer: "执行需要人工审批，请使用支持中断的执行入口"}
		}
	}

	return SupervisorRunResult{
		Answer:      state.Answer,
		RoutedAgent: "assistant",
	}
}

// RunStep- executes one iteration of the ReAct loop.
// Returns updated state, optional interrupt request, and error.
// If interruptReq is non-nil, the caller should save state and wait for approval.
// RunStep executes one step of the ReAct loop. When confirmBeforeExecute is
// true, the run pauses after the LLM decides on tool calls (before executing
// them) so the plan can be approved or rejected by a human.
func (r *SteppedRunner) RunStep(ctx context.Context, state *SteppedRunState, recorder *EventRecorder, confirmBeforeExecute bool) (*SteppedRunState, *InterruptRequest, error) {
	if err := ctx.Err(); err != nil {
		return state, nil, err
	}
	if state.Done {
		return state, nil, nil
	}
	if len(state.PendingToolCalls) > 0 {
		return r.executePendingTools(ctx, state, recorder)
	}
	if state.Step >= r.maxSteps {
		return state, nil, fmt.Errorf("达到最大迭代次数限制")
	}

	einoMsgs := fromSchemaMessages(state.modelMessages())

	// Step 1: Call LLM (with tools bound)
	resp, err := r.generate(ctx, einoMsgs, recorder)
	if err != nil {
		return state, nil, fmt.Errorf("LLM generate failed at step %d: %w", state.Step, err)
	}

	state.Step++

	// Provider-reported usage is the only authoritative measure of what the
	// request cost; the local counter is a heuristic.
	if usage := messageUsage(resp); usage != nil {
		if state.Usage == nil {
			state.Usage = &UsageInfo{}
		}
		state.Usage.recordUsage(usage)
	}

	// Convert LLM response to our format
	respMsg := SchemaMessage{
		Role:    "assistant",
		Content: resp.Content,
	}
	for _, tc := range resp.ToolCalls {
		respMsg.ToolCalls = append(respMsg.ToolCalls, ToolCallInfo{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	state.appendMessage(respMsg)

	// Step 2: Check if LLM wants to call tools
	if len(resp.ToolCalls) == 0 {
		// No tool calls — LLM is done, return the answer
		state.Done = true
		state.Answer = resp.Content
		return state, nil, nil
	}

	state.PendingToolCalls = append([]ToolCallInfo(nil), respMsg.ToolCalls...)

	// Node-level interrupt point: after LLM decides to call tools,
	// before executing them. This lets humans review the LLM's plan.
	// Implements the "plan_review_node" pattern: LLM generates a plan (tool calls),
	// then the run pauses so a human can approve or reject the entire plan.
	// confirmBeforeExecute is a per-run parameter supplied by the caller
	// (explicit request flag), not shared runner state.
	if confirmBeforeExecute {
		// Build the structured plan for the approval card (rendered as rows
		// by the UI — never dumped into the message text).
		var plan []InterruptPlanStep
		for _, tc := range resp.ToolCalls {
			plan = append(plan, InterruptPlanStep{Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}

		interruptMsg := "Agent 已生成执行计划，等待确认后继续执行"

		// Create node-level interrupt request
		interruptReq := &InterruptRequest{
			InterruptID: "i_" + uuid.New().String(),
			Type:        hitl.InterruptTypeNode,
			NodeName:    "plan_review",
			Arguments:   "", // node-level: no single tool arguments
			Message:     interruptMsg,
			RunID:       state.RunID,
			ThreadID:    state.ThreadID,
			Plan:        plan,
		}

		// Save state to checkpoint so Resume can restore it
		if recorder != nil {
			recorder.Record(EventHITLInterrupt, fmt.Sprintf("Plan review requested at %s", "plan_review"), map[string]any{
				"node":         "plan_review",
				"tool_calls":   len(resp.ToolCalls),
				"interrupt_id": interruptReq.InterruptID,
			})
		}

		// Return with interrupt — the caller (runner.Chat) will save state
		// and present the approval card to the user.
		// On Resume, HandleApproval will continue from here (tools still pending).
		return state, interruptReq, nil
	}

	// Execute tools (with interrupt gates via dispatch table)
	return r.executePendingTools(ctx, state, recorder)
}

// executePendingTools executes all pending tool calls and appends results to messages.
// Uses the dispatch table to route tool calls to either sub-agents or real tools.
// ACL (RBAC) checks are performed before tool execution — if the user's roles
// don't permit the tool, a denial result is returned as the tool observation.
func (r *SteppedRunner) executePendingTools(ctx context.Context, state *SteppedRunState, recorder *EventRecorder) (*SteppedRunState, *InterruptRequest, error) {
	// Extract roles from context for ACL checks
	var roles []string
	if ac := auth.FromContext(ctx); ac != nil {
		roles = ac.Roles
	}

	for len(state.PendingToolCalls) > 0 {
		if err := ctx.Err(); err != nil {
			return state, nil, err
		}
		tc := state.PendingToolCalls[0]
		entry, found := r.dispatchMap[tc.Name]
		if !found {
			// Unknown tool — return error as tool result
			toolMsg := SchemaMessage{
				Role:       "tool",
				Content:    fmt.Sprintf(`{"error":"tool %s not found in dispatch table"}`, tc.Name),
				ToolCallID: tc.ID,
				Name:       tc.Name,
			}
			state.appendMessage(toolMsg)
			state.PendingToolCalls = state.PendingToolCalls[1:]
			continue
		}

		// ACL check: verify the user's roles permit this tool/sub-agent.
		// Runs whenever the framework has an RBAC manager — an identity with
		// no roles (or no identity at all) is denied here, never bypassed.
		if r.rbac != nil {
			toolName := tc.Name
			if entry.IsSubAgent {
				// For sub-agents, check if the user has permission for ANY tool
				// inside that sub-agent. If none, deny the sub-agent call.
				agentConfig := entry.AgentWrapper.SubAgentConfig()
				hasAnyPermission := false
				for _, t := range agentConfig.ToolNames {
					if r.rbac.CanInvokeTool(ctx, roles, t) {
						hasAnyPermission = true
						break
					}
				}
				if !hasAnyPermission {
					reason := fmt.Sprintf("❌ 权限不足：角色 [%s] 无权调用子 Agent %s 的任何工具", strings.Join(roles, ", "), tc.Name)
					toolMsg := SchemaMessage{
						Role:       "tool",
						Content:    reason,
						ToolCallID: tc.ID,
						Name:       tc.Name,
					}
					state.appendMessage(toolMsg)
					if recorder != nil {
						recorder.Record(EventACLDenied, fmt.Sprintf("ACL denied sub-agent %s", tc.Name), map[string]any{
							"agent":  tc.Name,
							"denied": true,
						})
					}
					state.PendingToolCalls = state.PendingToolCalls[1:]
					continue
				}
			} else {
				// For real tools, check directly
				if !r.rbac.CanInvokeTool(ctx, roles, toolName) {
					reason := fmt.Sprintf("❌ 权限不足：角色 [%s] 无权调用工具 %s", strings.Join(roles, ", "), toolName)
					toolMsg := SchemaMessage{
						Role:       "tool",
						Content:    reason,
						ToolCallID: tc.ID,
						Name:       tc.Name,
					}
					state.appendMessage(toolMsg)
					if recorder != nil {
						recorder.Record(EventACLDenied, fmt.Sprintf("ACL denied tool %s", toolName), map[string]any{
							"tool":   toolName,
							"denied": true,
						})
					}
					state.PendingToolCalls = state.PendingToolCalls[1:]
					continue
				}
			}
		}

		if entry.IsSubAgent {
			// Sub-agent dispatch: delegate to agentToolWrapper
			return r.executeSubAgentTool(ctx, state, tc, entry, recorder)
		}

		// Real tool dispatch: check HITL gate
		if entry.RequiresApproval {
			if recorder != nil {
				recorder.Record(EventHITLInterrupt, fmt.Sprintf("Tool %s requires approval", tc.Name), map[string]any{
					"tool":   tc.Name,
					"reason": "high_risk_tool",
				})
			}
			return state, &InterruptRequest{
				InterruptID: "i_" + uuid.New().String(),
				ToolCallID:  tc.ID,
				Type:        hitl.InterruptTypeTool,
				ToolName:    tc.Name,
				Arguments:   tc.Arguments,
				Message:     fmt.Sprintf("高危工具 %s 需要审批", tc.Name),
				RunID:       state.RunID,
				ThreadID:    state.ThreadID,
			}, nil
		}

		// Execute the real tool directly
		if recorder != nil {
			recorder.Record(EventToolCallStart, fmt.Sprintf("Calling tool %s", tc.Name), map[string]any{
				"tool": tc.Name,
				"step": state.Step,
			})
		}
		toolResult, err := r.executeTool(ctx, tc)
		if err != nil {
			return state, nil, err
		}

		state.PendingToolCalls = state.PendingToolCalls[1:]

		// Append tool result as a tool message
		toolMsg := SchemaMessage{
			Role:       "tool",
			Content:    r.capToolResult(toolResult),
			ToolCallID: tc.ID,
			Name:       tc.Name,
		}
		state.appendMessage(toolMsg)

		if recorder != nil {
			recorder.Record(EventToolCallEnd, fmt.Sprintf("Tool %s executed", tc.Name), map[string]any{
				"tool":   tc.Name,
				"step":   state.Step,
				"result": truncate(toolResult, 200),
			})
		}
	}

	// Clear pending tool calls — next step will call LLM again
	state.PendingToolCalls = nil
	return state, nil, nil
}

// executeSubAgentTool delegates a tool call to a sub-agent wrapper.
// If the sub-agent encounters a dangerous tool requiring HITL approval,
// the interrupt is propagated up rather than swallowed as an error.
func (r *SteppedRunner) executeSubAgentTool(ctx context.Context, state *SteppedRunState, tc ToolCallInfo, entry *DispatchEntry, recorder *EventRecorder) (*SteppedRunState, *InterruptRequest, error) {
	if recorder != nil {
		recorder.Record(EventSupervisorRoute, "Routing to sub-agent "+tc.Name, map[string]any{"agent": tc.Name})
	}
	childRunner := entry.AgentWrapper.steppedRunner
	if childRunner == nil {
		return state, nil, fmt.Errorf("sub-agent %s has no resumable runner", tc.Name)
	}
	if state.Children == nil {
		state.Children = make(map[string]*SteppedRunState)
	}
	child := state.Children[tc.ID]
	if child == nil {
		var args struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			return state, nil, err
		}
		child = &SteppedRunState{RunID: state.RunID, ThreadID: state.ThreadID, Messages: toSchemaMessages([]*schema.Message{schema.SystemMessage(entry.AgentWrapper.SubAgentConfig().Instruction), schema.UserMessage(args.Message)})}
		state.Children[tc.ID] = child
	}
	for !child.Done {
		_, interrupt, err := childRunner.RunStep(ctx, child, recorder, false)
		if err != nil || interrupt != nil {
			return state, interrupt, err
		}
	}
	state.appendMessage(SchemaMessage{Role: "tool", Content: r.capToolResult(child.Answer), ToolCallID: tc.ID, Name: tc.Name})
	state.PendingToolCalls = state.PendingToolCalls[1:]
	delete(state.Children, tc.ID)
	return state, nil, nil
}

// executeTool invokes a tool by name with the given arguments.
func (r *SteppedRunner) executeTool(ctx context.Context, tc ToolCallInfo) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	regTool, found := r.registry.Get(tc.Name)
	if !found {
		return "", fmt.Errorf("tool %s not found", tc.Name)
	}

	// Typed identity derived from the authenticated context; nil when the
	// caller chain carries no identity, which tools reject.
	identity := auth.ToolIdentityFromContext(ctx)
	if identity != nil {
		identity.ToolCallID = tc.ID
	}

	result := regTool.Fn(identity, tc.Arguments)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if result.Error != "" {
		if result.Metadata["status"] == "system_error" {
			return "", fmt.Errorf("tool %s: %s", tc.Name, result.Error)
		}
		return result.Error, nil
	}
	return result.Content, nil
}

// HandleApproval processes an approval decision and continues execution.
// For tool-level: if approved, execute the tool; if rejected, feed rejection back to LLM.
// For node-level: if approved, continue from the next node; if rejected, feed rejection back to LLM.
func (r *SteppedRunner) HandleApproval(ctx context.Context, state *SteppedRunState, interrupt *InterruptRequest, approved bool, reason string) (*SteppedRunState, error) {
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if interrupt.Type == hitl.InterruptTypeNode {
		if !approved {
			for _, tc := range state.PendingToolCalls {
				state.appendMessage(SchemaMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Name, Content: "用户拒绝执行计划：" + reason})
			}
			state.PendingToolCalls = nil
		}
		return state, nil
	}
	for _, tc := range state.PendingToolCalls {
		if child := state.Children[tc.ID]; child != nil {
			entry := r.dispatchMap[tc.Name]
			if entry == nil || entry.AgentWrapper == nil || entry.AgentWrapper.steppedRunner == nil {
				return state, fmt.Errorf("missing child runner %s", tc.Name)
			}
			_, err := entry.AgentWrapper.steppedRunner.HandleApproval(ctx, child, interrupt, approved, reason)
			return state, err
		}
	}
	for i, tc := range state.PendingToolCalls {
		if tc.ID != interrupt.ToolCallID || tc.Name != interrupt.ToolName || tc.Arguments != interrupt.Arguments {
			continue
		}
		content := "用户拒绝执行该操作：" + reason
		if approved {
			var err error
			content, err = r.executeTool(ctx, tc)
			if err != nil {
				return state, err
			}
		}
		state.appendMessage(SchemaMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Name, Content: r.capToolResult(content)})
		state.PendingToolCalls = append(state.PendingToolCalls[:i], state.PendingToolCalls[i+1:]...)
		return state, nil
	}
	return state, fmt.Errorf("approved tool call does not match checkpoint")
}

// generate retries only model calls, never tool side effects. Backoff respects
// cancellation. Content is forwarded to the recorder's sink as it arrives, so a
// rate-limited call is retried only while nothing has been emitted: once the
// client has seen part of an answer, replaying it would duplicate that text.
func (r *SteppedRunner) generate(ctx context.Context, messages []*schema.Message, recorder *EventRecorder) (*schema.Message, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if recorder != nil {
			recorder.Record(EventModelCallStart, fmt.Sprintf("Model call (attempt %d)", attempt+1), map[string]any{
				"attempt": attempt + 1,
			})
		}
		response, emitted, err := r.streamOnce(ctx, messages, recorder)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil && response == nil {
			return nil, fmt.Errorf("model returned an empty response")
		}
		if err == nil || !isRateLimitError(err.Error()) || attempt >= 3 || emitted {
			if recorder != nil {
				meta := map[string]any{"attempt": attempt + 1, "failed": err != nil}
				if usage := messageUsage(response); usage != nil {
					meta["prompt_tokens"] = usage.PromptTokens
					meta["completion_tokens"] = usage.CompletionTokens
				}
				recorder.Record(EventModelCallEnd, "Model call finished", meta)
			}
			return response, err
		}
		timer := time.NewTimer(r.retryDelay * time.Duration(1<<attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// streamOnce performs one streaming model call, forwarding content fragments to
// the recorder and merging the chunks into the complete message the ReAct loop
// needs. The bool reports whether any content reached the sink, which decides
// whether a retry is still safe.
func (r *SteppedRunner) streamOnce(ctx context.Context, messages []*schema.Message, recorder *EventRecorder) (*schema.Message, bool, error) {
	stream, err := r.toolModel.Stream(ctx, messages)
	if err != nil {
		return nil, false, err
	}
	defer stream.Close()

	emitted := false
	var chunks []*schema.Message
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, emitted, err
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		if chunk.Content != "" {
			emitted = true
			recorder.Delta(chunk.Content)
		}
	}
	if len(chunks) == 0 {
		return nil, emitted, nil
	}
	// Tool call arguments arrive split across chunks for OpenAI-compatible
	// providers, so the merged message — not the last chunk — is authoritative.
	merged, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, emitted, err
	}
	return merged, emitted, nil
}

// messageUsage extracts provider-reported token usage when the model supplies it.
func messageUsage(msg *schema.Message) *schema.TokenUsage {
	if msg == nil || msg.ResponseMeta == nil {
		return nil
	}
	return msg.ResponseMeta.Usage
}

// Serialize serializes the run state to bytes for checkpoint storage.
func (s *SteppedRunState) Serialize() ([]byte, error) {
	return json.Marshal(s)
}

// DeserializeRunState deserializes a run state from bytes.
func DeserializeRunState(data []byte) (*SteppedRunState, error) {
	var state SteppedRunState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to deserialize run state: %w", err)
	}
	return &state, nil
}

// SaveToCheckpoint saves the run state as a checkpoint snapshot.
func (s *SteppedRunState) SaveToCheckpoint(ctx context.Context, userID, threadID string, store memory.CheckpointStore) error {
	data, err := s.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize state: %w", err)
	}
	now := time.Now()
	return store.Save(ctx, memory.Checkpoint{
		UserID:      userID,
		ThreadID:    threadID,
		RunID:       s.RunID,
		State:       data,
		Interrupted: true,
		Snapshot:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
}

// LoadFromCheckpoint loads a run state from a checkpoint.
func LoadFromCheckpoint(ctx context.Context, userID, threadID, runID string, store memory.CheckpointStore) (*SteppedRunState, error) {
	cp, ok, err := store.Load(ctx, memory.CheckpointKey{UserID: userID, ThreadID: threadID, RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("checkpoint not found for run %s", runID)
	}
	return DeserializeRunState(cp.State)
}

// --- Conversion helpers ---

func toSchemaMessages(msgs []*schema.Message) []SchemaMessage {
	result := make([]SchemaMessage, 0, len(msgs))
	for _, m := range msgs {
		sm := SchemaMessage{
			Role:    string(m.Role),
			Content: m.Content,
			Name:    m.Name,
		}
		if m.ToolCallID != "" {
			sm.ToolCallID = m.ToolCallID
		}
		for _, tc := range m.ToolCalls {
			sm.ToolCalls = append(sm.ToolCalls, ToolCallInfo{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		result = append(result, sm)
	}
	return result
}

func fromSchemaMessages(msgs []SchemaMessage) []*schema.Message {
	result := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "system":
			result = append(result, schema.SystemMessage(m.Content))
		case "user":
			result = append(result, schema.UserMessage(m.Content))
		case "assistant":
			var toolCalls []schema.ToolCall
			for _, tc := range m.ToolCalls {
				toolCalls = append(toolCalls, schema.ToolCall{
					ID: tc.ID,
					Function: schema.FunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
			result = append(result, &schema.Message{
				Role:      schema.Assistant,
				Content:   m.Content,
				ToolCalls: toolCalls,
			})
		case "tool":
			result = append(result, &schema.Message{
				Role:       schema.Tool,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
				Name:       m.Name,
			})
		default:
			result = append(result, schema.AssistantMessage(m.Content, nil))
		}
	}
	return result
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// estimateToolSchemaTokens approximates what the bound tool definitions add to
// every request. Providers bill for them on each call, and they are invisible to
// a counter that only walks messages.
func estimateToolSchemaTokens(infos []*schema.ToolInfo) int {
	if len(infos) == 0 {
		return 0
	}
	counter := contextmgr.NewSimpleTokenCounter()
	total := 0
	for _, info := range infos {
		if info == nil {
			continue
		}
		total += counter.CountMessage(contextmgr.Message{Role: "system", Content: info.Name + " " + info.Desc})
		if info.ParamsOneOf == nil {
			continue
		}
		params, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil || params == nil {
			continue
		}
		raw, err := json.Marshal(params)
		if err != nil {
			continue
		}
		total += counter.CountMessage(contextmgr.Message{Role: "system", Content: string(raw)})
	}
	return total
}
