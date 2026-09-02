package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/tools"
	"github.com/google/uuid"
)

// DispatchEntry represents a single entry in the SteppedRunner's dispatch table.
// It abstracts over "real tool" and "sub-agent tool" uniformly.
type DispatchEntry struct {
	Info             *schema.ToolInfo // tool definition for LLM binding
	IsSubAgent       bool            // true = delegate to agentToolWrapper, false = real tool
	RequiresApproval bool            // for real tools: HITL gate needed?
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
	Step             int               `json:"step"`               // current iteration (0-based)
	Messages         []SchemaMessage   `json:"messages"`           // full conversation history
	PendingToolCalls []ToolCallInfo    `json:"pending_tool_calls"` // tool calls awaiting execution
	Done             bool              `json:"done"`               // whether the loop has completed
	Answer           string            `json:"answer"`             // final answer when done
	RunID            string            `json:"run_id"`             // run identifier
	ThreadID         string            `json:"thread_id"`          // thread identifier
}

// SchemaMessage is a serializable version of schema.Message.
type SchemaMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []ToolCallInfo `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
}

// ToolCallInfo is a serializable version of schema.ToolCall.
type ToolCallInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Arguments string `json:"arguments"`
}

// InterruptRequest represents a pending interrupt that needs human approval.
type InterruptRequest struct {
	InterruptID string
	Type        hitl.InterruptType // "tool" or "node"
	ToolName    string             // for tool-level: the tool name
	Arguments   string             // for tool-level: the tool arguments
	NodeName    string             // for node-level: the node name
	Message     string             // human-readable description
	RunID       string
	ThreadID    string
}

// SteppedRunner executes a ReAct loop step-by-step with interrupt support.
type SteppedRunner struct {
	chatModel   model.ToolCallingChatModel // base model (no tools bound)
	toolModel   model.ToolCallingChatModel // model with tools bound via WithTools
	registry    *tools.ToolRegistry
	hitlSvc     *hitl.Service
	rbac        *auth.RBACManager          // RBAC checker for tool-level ACL
	maxSteps    int
	dispatchMap map[string]*DispatchEntry // name -> entry for O(1) dispatch
	toolInfos   []*schema.ToolInfo        // ordered list for LLM binding
}

// NewSteppedRunner creates a new stepped ReAct runner.
func NewSteppedRunner(
	chatModel model.ToolCallingChatModel,
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
		chatModel:   chatModel,
		toolModel:   toolModel,
		registry:    registry,
		hitlSvc:     hitlSvc,
		rbac:        rbac,
		maxSteps:    maxSteps,
		dispatchMap: dispatchMap,
		toolInfos:   toolInfos,
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
		RunID:    "r_" + uuid.New().String()[:8],
	}

	var interruptReq *InterruptRequest
	var err error

	for !state.Done {
		state, interruptReq, err = r.RunStep(ctx, state, recorder, false)
		if err != nil {
			return SupervisorRunResult{Answer: fmt.Sprintf("Error at step %d: %v", state.Step, err)}
		}
		if interruptReq != nil {
			// No interrupt handling in RunAll — auto-approve
			state, err = r.HandleApproval(ctx, state, interruptReq, true, "")
			if err != nil {
				return SupervisorRunResult{Answer: fmt.Sprintf("Error after approval: %v", err)}
			}
		}
	}

	return SupervisorRunResult{
		Answer:       state.Answer,
		RoutedAgent:  "assistant",
	}
}

// RunStep- executes one iteration of the ReAct loop.
// Returns updated state, optional interrupt request, and error.
// If interruptReq is non-nil, the caller should save state and wait for approval.
// RunStep executes one step of the ReAct loop. When confirmBeforeExecute is
// true, the run pauses after the LLM decides on tool calls (before executing
// them) so the plan can be approved or rejected by a human.
func (r *SteppedRunner) RunStep(ctx context.Context, state *SteppedRunState, recorder *EventRecorder, confirmBeforeExecute bool) (*SteppedRunState, *InterruptRequest, error) {
	if state.Done {
		return state, nil, nil
	}

	if state.Step >= r.maxSteps {
		state.Done = true
		state.Answer = "达到最大迭代次数限制"
		return state, nil, nil
	}

	einoMsgs := fromSchemaMessages(state.Messages)

	// If there are pending tool calls (from a previous LLM step), execute them first
	if len(state.PendingToolCalls) > 0 {
		return r.executePendingTools(ctx, state, recorder)
	}

	// Step 1: Call LLM (with tools bound)
	resp, err := r.toolModel.Generate(ctx, einoMsgs)
	if err != nil {
		return state, nil, fmt.Errorf("LLM generate failed at step %d: %w", state.Step, err)
	}

	state.Step++

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
	state.Messages = append(state.Messages, respMsg)

	// Step 2: Check if LLM wants to call tools
	if len(resp.ToolCalls) == 0 {
		// No tool calls — LLM is done, return the answer
		state.Done = true
		state.Answer = resp.Content
		return state, nil, nil
	}

	// Node-level interrupt point: after LLM decides to call tools,
	// before executing them. This lets humans review the LLM's plan.
	// Implements the "plan_review_node" pattern: LLM generates a plan (tool calls),
	// then the run pauses so a human can approve or reject the entire plan.
	// confirmBeforeExecute is a per-run parameter supplied by the caller
	// (explicit request flag), not shared runner state.
	if confirmBeforeExecute {
		// Build a human-readable summary of the planned tool calls
		var planLines []string
		for _, tc := range resp.ToolCalls {
			planLines = append(planLines, fmt.Sprintf("  - %s(%s)", tc.Function.Name, tc.Function.Arguments))
		}
		planSummary := fmt.Sprintf("Agent 计划执行以下操作：\n%s", strings.Join(planLines, "\n"))

		interruptMsg := "Agent 已生成执行计划，需要人工审批后方可继续"
		interruptMsg += "\n\n" + planSummary

		// Create node-level interrupt request
		interruptReq := &InterruptRequest{
			InterruptID: "i_" + uuid.New().String()[:8],
			Type:        hitl.InterruptTypeNode,
			NodeName:    "plan_review",
			Arguments:   "", // node-level: no single tool arguments
			Message:     interruptMsg,
			RunID:       state.RunID,
			ThreadID:    state.ThreadID,
		}

		// Save state to checkpoint so Resume can restore it
		if recorder != nil {
			recorder.Record(EventAgentEnd, fmt.Sprintf("Node interrupt at %s — awaiting approval", "plan_review"), map[string]any{
				"node":          "plan_review",
				"tool_calls":    len(resp.ToolCalls),
				"interrupt_id":  interruptReq.InterruptID,
			})
		}

		// Return with interrupt — the caller (runner.Chat) will save state
		// and present the approval card to the user.
		// On Resume, HandleApproval will continue from here (tools still pending).
		return state, interruptReq, nil
	}

	// Step 3: Store pending tool calls and delegate to executePendingTools
	// which handles both HITL interrupt gates (for real tools) and
	// sub-agent dispatch (for agentToolWrapper entries)
	state.PendingToolCalls = make([]ToolCallInfo, 0, len(resp.ToolCalls))
	for _, tc := range resp.ToolCalls {
		state.PendingToolCalls = append(state.PendingToolCalls, ToolCallInfo{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
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

	for _, tc := range state.PendingToolCalls {
		entry, found := r.dispatchMap[tc.Name]
		if !found {
			// Unknown tool — return error as tool result
			toolMsg := SchemaMessage{
				Role:       "tool",
				Content:    fmt.Sprintf(`{"error":"tool %s not found in dispatch table"}`, tc.Name),
				ToolCallID: tc.ID,
				Name:       tc.Name,
			}
			state.Messages = append(state.Messages, toolMsg)
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
					state.Messages = append(state.Messages, toolMsg)
					if recorder != nil {
						recorder.Record(EventToolCallEnd, fmt.Sprintf("ACL denied sub-agent %s", tc.Name), map[string]any{
							"agent": tc.Name,
							"denied": true,
						})
					}
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
					state.Messages = append(state.Messages, toolMsg)
					if recorder != nil {
						recorder.Record(EventToolCallEnd, fmt.Sprintf("ACL denied tool %s", toolName), map[string]any{
							"tool":   toolName,
							"denied": true,
						})
					}
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
			return state, &InterruptRequest{
				InterruptID: "i_" + uuid.New().String()[:8],
				Type:        hitl.InterruptTypeTool,
				ToolName:    tc.Name,
				Arguments:   tc.Arguments,
				Message:     fmt.Sprintf("高危工具 %s 需要审批", tc.Name),
				RunID:       state.RunID,
				ThreadID:    state.ThreadID,
			}, nil
		}

		// Execute the real tool directly
		toolResult := r.executeTool(ctx, tc)

		// Append tool result as a tool message
		toolMsg := SchemaMessage{
			Role:       "tool",
			Content:    toolResult,
			ToolCallID: tc.ID,
			Name:       tc.Name,
		}
		state.Messages = append(state.Messages, toolMsg)

		if recorder != nil {
			recorder.Record(EventToolCallEnd, fmt.Sprintf("Tool %s executed", tc.Name), map[string]any{
				"tool":  tc.Name,
				"step":  state.Step,
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
func (r *SteppedRunner) executeSubAgentTool(
	ctx context.Context,
	state *SteppedRunState,
	tc ToolCallInfo,
	entry *DispatchEntry,
	recorder *EventRecorder,
) (*SteppedRunState, *InterruptRequest, error) {
	if recorder != nil {
		recorder.Record(EventSupervisorRoute, fmt.Sprintf("Routing to sub-agent %s", tc.Name), map[string]any{
			"agent": tc.Name,
		})
	}

	// Call the sub-agent wrapper
	result, err := entry.AgentWrapper.InvokableRun(ctx, tc.Arguments)
	if err != nil {
		// Check if this is a propagated interrupt from the sub-agent
		if isSubAgentInterrupt(err) {
			var sae *SubAgentInterruptError
			if errors.As(err, &sae) {
				return state, &InterruptRequest{
					InterruptID: "i_" + uuid.New().String()[:8],
					Type:        hitl.InterruptTypeTool,
					ToolName:    sae.ToolName,
					Arguments:   sae.Arguments,
					Message:     fmt.Sprintf("子代理 %s 的高危工具 %s 需要审批", sae.AgentName, sae.ToolName),
					RunID:       state.RunID,
					ThreadID:    state.ThreadID,
				}, nil
			}
		}
		// Genuine error — return as tool result so the LLM can recover
		toolMsg := SchemaMessage{
			Role:       "tool",
			Content:    fmt.Sprintf(`{"error":"sub-agent %s failed: %v"}`, tc.Name, err),
			ToolCallID: tc.ID,
			Name:       tc.Name,
		}
		state.Messages = append(state.Messages, toolMsg)
		// Clear pending and continue
		state.PendingToolCalls = nil
		return state, nil, nil
	}

	// Sub-agent completed successfully — return its answer as the tool result
	toolMsg := SchemaMessage{
		Role:       "tool",
		Content:    result,
		ToolCallID: tc.ID,
		Name:       tc.Name,
	}
	state.Messages = append(state.Messages, toolMsg)

	if recorder != nil {
		recorder.Record(EventToolCallEnd, fmt.Sprintf("Sub-agent %s completed", tc.Name), map[string]any{
			"agent":  tc.Name,
			"step":   state.Step,
			"result": truncate(result, 200),
		})
	}

	// Clear pending — next iteration will call LLM again
	state.PendingToolCalls = nil
	return state, nil, nil
}

// executeTool invokes a tool by name with the given arguments.
func (r *SteppedRunner) executeTool(ctx context.Context, tc ToolCallInfo) string {
	regTool, found := r.registry.Get(tc.Name)
	if !found {
		return fmt.Sprintf(`{"error":"tool %s not found"}`, tc.Name)
	}

	// Typed identity derived from the authenticated context; nil when the
	// caller chain carries no identity, which tools reject.
	identity := auth.ToolIdentityFromContext(ctx)

	result := regTool.Fn(identity, tc.Arguments)
	if result.Error != "" {
		return result.Error
	}
	return result.Content
}

// HandleApproval processes an approval decision and continues execution.
// For tool-level: if approved, execute the tool; if rejected, feed rejection back to LLM.
// For node-level: if approved, continue from the next node; if rejected, feed rejection back to LLM.
func (r *SteppedRunner) HandleApproval(ctx context.Context, state *SteppedRunState, interrupt *InterruptRequest, approved bool, reason string) (*SteppedRunState, error) {
	if interrupt.Type == hitl.InterruptTypeTool {
		if approved {
			// Execute the approved tool
			for _, tc := range state.PendingToolCalls {
				if tc.Name == interrupt.ToolName {
					toolResult := r.executeTool(ctx, tc)

					toolMsg := SchemaMessage{
						Role:       "tool",
						Content:    toolResult,
						ToolCallID: tc.ID,
						Name:       tc.Name,
					}
					state.Messages = append(state.Messages, toolMsg)

					// Remove this tool call from pending
					var remaining []ToolCallInfo
					for _, p := range state.PendingToolCalls {
						if p.ID != tc.ID {
							remaining = append(remaining, p)
						}
					}
					state.PendingToolCalls = remaining
					break
				}
			}
		} else {
			// Rejected: feed rejection message back to LLM as tool result
			rejectionMsg := "用户拒绝执行该操作"
			if reason != "" {
				rejectionMsg += "：" + reason
			}

			// Find the tool call and add rejection as its result
			for _, tc := range state.PendingToolCalls {
				if tc.Name == interrupt.ToolName {
					toolMsg := SchemaMessage{
						Role:       "tool",
						Content:    rejectionMsg,
						ToolCallID: tc.ID,
						Name:       tc.Name,
					}
					state.Messages = append(state.Messages, toolMsg)

					// Remove from pending
					var remaining []ToolCallInfo
					for _, p := range state.PendingToolCalls {
						if p.ID != tc.ID {
							remaining = append(remaining, p)
						}
					}
					state.PendingToolCalls = remaining
					break
				}
			}
		}
	} else if interrupt.Type == hitl.InterruptTypeNode {
		// Node-level interrupt resume:
		// - Approved: continue execution — tools are still in PendingToolCalls,
		//   the next RunStep call to executePendingTools will execute them.
		// - Rejected: clear pending tools and feed a rejection message back
		//   so the LLM can re-plan with the human's feedback.
		if !approved {
			rejectionMsg := "用户拒绝执行计划"
			if reason != "" {
				rejectionMsg += "：" + reason
			}
			// Feed rejection as assistant message so LLM sees the plan was rejected
			assistantContent := "我计划的操作被拒绝了。" + rejectionMsg
			state.Messages = append(state.Messages, SchemaMessage{
				Role:    "assistant",
				Content: assistantContent,
			})
			state.PendingToolCalls = nil
		}
		// If approved, PendingToolCalls remain intact — next RunStep will execute them
	}

	return state, nil
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
