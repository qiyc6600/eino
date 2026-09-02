package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/contextmgr"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/tools"
	"github.com/google/uuid"
)

// RunStatus represents the status of an agent run.
type RunStatus string

const (
	StatusCompleted   RunStatus = "completed"
	StatusInterrupted RunStatus = "interrupted"
	StatusError       RunStatus = "error"
)

// ChatRunResult is the full result of an agent run.
type ChatRunResult struct {
	RunID         string     `json:"run_id"`
	Status        RunStatus  `json:"status"`
	Answer        string     `json:"answer,omitempty"`
	Interrupt     *Interrupt `json:"interrupt,omitempty"`
	Events        []Event    `json:"events,omitempty"`
	RoutedAgent   string     `json:"routed_agent,omitempty"`
	ContextTokens *TokenInfo `json:"context_tokens,omitempty"`
}

// TokenInfo shows the context window usage for the current turn.
type TokenInfo struct {
	Current   int `json:"current"`   // tokens in the context sent to the LLM
	Threshold int `json:"threshold"` // compression trigger threshold
	Max       int `json:"max"`       // context window limit
	Compressed bool `json:"compressed,omitempty"` // true if compression was applied
}

// Interrupt describes an interruption in an agent run.
type Interrupt struct {
	InterruptID string `json:"interrupt_id"`
	ToolName    string `json:"tool_name,omitempty"`
	NodeName    string `json:"node_name,omitempty"`
	Arguments   string `json:"arguments,omitempty"`
	Message     string `json:"message"`
	Type        string `json:"type"`
}

// sanitizeMessages ensures every assistant message with tool_calls has
// corresponding tool result messages following it. If any tool_call is
// missing its result (e.g., because the run was interrupted before the tool
// executed), a placeholder tool result is inserted. This prevents 400 errors
// from the LLM API ("An assistant message with 'tool_calls' must be followed
// by tool messages") when a thread contains orphaned tool_calls from
// interrupted runs.
func sanitizeMessages(messages []*schema.Message) []*schema.Message {
	var result []*schema.Message

	for i, msg := range messages {
		result = append(result, msg)

		// Only check assistant messages with tool_calls
		if msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
			continue
		}

		// Collect tool_call IDs that need results
		neededIDs := make(map[string]bool)
		for _, tc := range msg.ToolCalls {
			neededIDs[tc.ID] = true
		}

		// Check subsequent messages for tool results
		for j := i + 1; j < len(messages); j++ {
			next := messages[j]
			if next.Role == schema.Tool {
				delete(neededIDs, next.ToolCallID)
			}
			// Stop scanning at the next assistant/user message boundary
			// (tool results must immediately follow their tool_call)
			if next.Role == schema.Assistant || next.Role == schema.User {
				break
			}
		}

		// Insert placeholder tool results for any missing IDs
		if len(neededIDs) > 0 {
			for id := range neededIDs {
				placeholder := schema.ToolMessage(
					"⏸️ 该操作已被中断，等待人工审批。如需继续，请在审批中心处理。",
					id,
				)
				result = append(result, placeholder)
			}
		}
	}

	return result
}

// Runner is the main agent execution entry point.
type Runner struct {
	mu            sync.RWMutex
	supervisor    *SupervisorAgent
	steppedRunner *SteppedRunner
	hitlSvc       *hitl.Service
	registry      *tools.ToolRegistry
	rbac          *auth.RBACManager
	memorySvc     *memory.Service
	summarizer    *contextmgr.Summarizer
	counter       contextmgr.TokenCounter
	runs          map[string]*ChatRunResult
	runOwners     map[string]string        // runID -> owner userID (isolation for run events)
	resumeResults map[string]ChatRunResult // interruptID -> Resume result (for idempotent retry)
	threads       *threadStore             // per-user conversation threads
	maxTokens     int
}

// NewRunner creates a new Runner.
func NewRunner(
	supervisor *SupervisorAgent,
	steppedRunner *SteppedRunner,
	hitlSvc *hitl.Service,
	registry *tools.ToolRegistry,
	rbac *auth.RBACManager,
	memorySvc *memory.Service,
	summarizer *contextmgr.Summarizer,
	maxTokens int,
) *Runner {
	return &Runner{
		supervisor:    supervisor,
		steppedRunner: steppedRunner,
		hitlSvc:       hitlSvc,
		registry:      registry,
		rbac:          rbac,
		memorySvc:     memorySvc,
		summarizer:    summarizer,
		counter:       contextmgr.NewSimpleTokenCounter(),
		runs:          make(map[string]*ChatRunResult),
		runOwners:     make(map[string]string),
		resumeResults: make(map[string]ChatRunResult),
		threads:       newThreadStore(),
		maxTokens:     maxTokens,
	}
}

// SetSupervisor replaces the supervisor agent (used for model switching).
func (r *Runner) SetSupervisor(s *SupervisorAgent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.supervisor = s
}

// SetSteppedRunner replaces the stepped runner (used for model switching).
func (r *Runner) SetSteppedRunner(sr *SteppedRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steppedRunner = sr
}

// ExtractAndSavePreferences extracts user preferences from a message and saves to long-term memory.
// Called after streaming completes to ensure preferences are captured in all modes.
func (r *Runner) ExtractAndSavePreferences(authCtx *auth.AuthContext, userMessage string) {
	ctx := context.Background()
	_ = r.memorySvc.ExtractAndSave(ctx, authCtx.UserID, userMessage)
}

// Chat executes a chat request through the Eino agent pipeline.
func (r *Runner) Chat(authCtx *auth.AuthContext, threadID, userMessage string) ChatRunResult {
	runID := "r_" + uuid.New().String()[:8]
	recorder := NewEventRecorder(runID)

	// Load thread history (scoped to the calling user)
	threadMessages := r.threads.Copy(authCtx.UserID, threadID)

	// Build context for tool execution (injected via Go context)
	ctx := context.Background()
	ctx = injectAuthContext(ctx, authCtx, threadID, runID)

	// Inject long-term memory into system prompt
	memCtx := r.memorySvc.BuildMemoryContext(ctx, authCtx.UserID)

	// Build dynamic system prompt based on dispatch mode
	// When sub-agents are registered, use the supervisor routing prompt;
	// otherwise fall back to the direct tool prompt.
	var systemContent string
	if r.steppedRunner != nil && r.steppedRunner.HasSubAgents() {
		systemContent = r.buildSupervisorPrompt(authCtx.Roles)
	} else {
		systemContent = r.buildSystemPrompt(authCtx.Roles)
	}
	if memCtx != "" {
		systemContent += "\n\n" + memCtx
	}

	// Inject vector-retrieved semantic memories (query-specific)
	vectorCtx := r.memorySvc.QueryVectorMemory(ctx, authCtx.UserID, userMessage, 3)
	if vectorCtx != "" {
		systemContent += "\n\n" + vectorCtx
	}

	messages := []*schema.Message{schema.SystemMessage(systemContent)}
	messages = append(messages, threadMessages...)
	messages = append(messages, schema.UserMessage(userMessage))

	// Sanitize the message history: insert placeholder tool results for any
	// assistant tool_call that lacks a corresponding tool result. This happens
	// when a previous run in this thread was interrupted (e.g., a delete_order
	// request awaiting approval) and the user sends a new message before
	// resolving it. Without this, the LLM API rejects the orphaned tool_call
	// with a 400 error and the approval card never appears.
	messages = sanitizeMessages(messages)

	// Apply context compression before sending to the agent.
	// Converts Eino schema.Message -> contextmgr.Message -> compress -> convert back.
	// This ensures long conversations don't exceed the token window.
	// If compression happened, replace the thread history with the compressed version
	// so that we don't re-compress the same old messages on every turn.
	compressedMessages, tokenInfo := r.compressMessages(ctx, messages, recorder)
	wasCompressed := len(compressedMessages) < len(messages)
	messages = compressedMessages

	// Run the agent with interrupt support
	// Use SteppedRunner (manual ReAct loop) if available — this enables
	// tool-level interrupts before dangerous tools are executed.
	// Fall back to supervisor (black-box) for backward compat.
	var result SupervisorRunResult
	var interruptReq *InterruptRequest
	var steppedState *SteppedRunState

	if r.steppedRunner != nil {
		// Check if user's message signals a "review before execute" intent.
		// If so, enable node-level interrupts for this run so the LLM's plan
		// is presented for human approval before any tools are executed.
		if wantsNodeInterrupt(userMessage) {
			r.steppedRunner.SetNodeInterruptConfig(&NodeInterruptConfig{
				Enabled:  true,
				NodeName: "plan_review",
				Message:  "Agent 已生成执行计划，需要人工审批后方可继续",
			})
		} else {
			// Disable node-level interrupt for normal runs
			r.steppedRunner.SetNodeInterruptConfig(nil)
		}

		// Stepped execution: run step-by-step with interrupt gates
		steppedState = &SteppedRunState{
			Step:     0,
			Messages: toSchemaMessages(messages),
			Done:     false,
			RunID:    runID,
			ThreadID: threadID,
		}

		maxRetries := 3
		for attempt := 0; attempt <= maxRetries; attempt++ {
			for !steppedState.Done {
				var stepErr error
				steppedState, interruptReq, stepErr = r.steppedRunner.RunStep(ctx, steppedState, recorder)
				if stepErr != nil {
					if isRateLimitError(stepErr.Error()) && attempt < maxRetries {
						waitSec := (attempt + 1) * 5
						recorder.Record(EventAgentEnd, fmt.Sprintf("Rate limited, retrying in %ds (attempt %d/%d)", waitSec, attempt+1, maxRetries), nil)
						time.Sleep(time.Duration(waitSec) * time.Second)
						continue
					}
					result = SupervisorRunResult{Answer: fmt.Sprintf("Error at step %d: %v", steppedState.Step, stepErr)}
					break
				}
				if interruptReq != nil {
					// Dangerous tool detected — pause and request approval
					break
				}
			}
			if result.Answer != "" && isRateLimitError(result.Answer) {
				continue
			}
			break
		}

		if steppedState.Done {
			result = SupervisorRunResult{
				Answer:      steppedState.Answer,
				RoutedAgent: "assistant",
			}
		}
	} else {
		// Fallback: black-box supervisor (no interrupt support)
		maxRetries := 3
		for attempt := 0; attempt <= maxRetries; attempt++ {
			result = r.supervisor.Run(ctx, messages, recorder)
			if result.Answer != "" && isRateLimitError(result.Answer) && attempt < maxRetries {
				waitSec := (attempt + 1) * 5
				recorder.Record(EventAgentEnd, fmt.Sprintf("Rate limited, retrying in %ds (attempt %d/%d)", waitSec, attempt+1, maxRetries), nil)
				time.Sleep(time.Duration(waitSec) * time.Second)
				continue
			}
			break
		}
	}

	// Save user message to thread history
	threadMessages = append(threadMessages, schema.UserMessage(userMessage))

	if interruptReq != nil {
		// Interrupt detected (tool-level or node-level) — create approval request and save state
		var req *hitl.ApprovalRequest
		var err error

		if interruptReq.Type == hitl.InterruptTypeNode {
			// Node-level interrupt: plan review node
			req, err = r.hitlSvc.RequestNodeInterrupt(ctx, authCtx, runID,
				interruptReq.NodeName, interruptReq.Message,
				map[string]any{
					"pending_tool_calls": interruptReq.Arguments,
				})
		} else {
			// Tool-level interrupt: dangerous tool
			req, err = r.hitlSvc.RequestToolInterrupt(ctx, authCtx, runID, interruptReq.ToolName, interruptReq.Arguments,
				string(tools.RiskLevelHigh),
				interruptReq.Message)
		}
		if err != nil {
			return ChatRunResult{
				RunID:  runID,
				Status: StatusError,
				Answer: fmt.Sprintf("创建审批失败：%v", err),
				Events: recorder.Events(),
			}
		}

		// Also store the ToolCallID in the approval request so Resume can match the tool result
		// with the assistant's tool_call message
		req.ToolCallID = interruptReq.InterruptID // use interrupt ID as tool call ID if not available

		// Build the interrupt info for the response
		interruptType := "tool"
		interruptToolName := interruptReq.ToolName
		interruptNodeName := ""
		interruptMsg := req.Message
		if interruptReq.Type == hitl.InterruptTypeNode {
			interruptType = "node"
			interruptToolName = ""
			interruptNodeName = interruptReq.NodeName
		}

		// Save the assistant message with tool_calls to the thread so Resume can reconstruct
		// the full conversation. Without this, the tool result would have no context.
		// The SteppedRunner's RunStep already added the assistant tool_call to steppedState.Messages,
		// but we need it in the thread format (schema.Message) too.
		threadMessagesWithToolCall := append(threadMessages, &schema.Message{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{
				{
					ID: interruptReq.InterruptID,
					Function: schema.FunctionCall{
						Name:      interruptReq.ToolName,
						Arguments: interruptReq.Arguments,
					},
				},
			},
		})

		runResult := ChatRunResult{
			RunID:  runID,
			Status: StatusInterrupted,
			Interrupt: &Interrupt{
				InterruptID: req.InterruptID,
				ToolName:    interruptToolName,
				NodeName:    interruptNodeName,
				Arguments:   interruptReq.Arguments,
				Message:     interruptMsg,
				Type:        interruptType,
			},
			Events:        recorder.Events(),
			RoutedAgent:   result.RoutedAgent,
			ContextTokens: tokenInfo,
		}

		r.mu.Lock()
		r.runs[runID] = &runResult
		r.runOwners[runID] = authCtx.UserID
		r.mu.Unlock()
		r.threads.Replace(authCtx.UserID, threadID, threadMessagesWithToolCall)

		// Save full SteppedRunState to checkpoint for crash recovery.
		// This enables resuming an interrupted run even after a process restart,
		// not just within the same process via in-memory steppedState.
		if steppedState != nil {
			_ = steppedState.SaveToCheckpoint(ctx, authCtx.UserID, threadID, r.memorySvc.GetCheckpointStore())
		}

		return runResult
	}

	// Completed successfully
	// Determine what to save back to thread history:
	// If compression happened, replace old messages with the compressed version
	// so that we don't re-compress the same old messages on every turn.
	var savedThreadMessages []*schema.Message
	if wasCompressed && len(compressedMessages) > 2 {
		// compressedMessages = [system, ...summary+recent..., userMsg]
		// Save everything except the first (system) and last (current user message)
		savedThreadMessages = compressedMessages[1 : len(compressedMessages)-1]
		savedThreadMessages = append(savedThreadMessages, schema.AssistantMessage(result.Answer, nil))
	} else {
		savedThreadMessages = append(threadMessages, schema.AssistantMessage(result.Answer, nil))
	}

	// Extract and save user preferences
	_ = r.memorySvc.ExtractAndSave(ctx, authCtx.UserID, userMessage)

	runResult := ChatRunResult{
		RunID:         runID,
		Status:        StatusCompleted,
		Answer:        result.Answer,
		Events:        recorder.Events(),
		RoutedAgent:   result.RoutedAgent,
		ContextTokens: tokenInfo,
	}

	r.mu.Lock()
	r.runs[runID] = &runResult
	r.runOwners[runID] = authCtx.UserID
	r.mu.Unlock()
	r.threads.Replace(authCtx.UserID, threadID, savedThreadMessages)

	// Save conversation snapshot for crash recovery
	go func() {
		snapshotCtx := context.Background()
		if data, err := json.Marshal(savedThreadMessages); err == nil {
			_ = r.memorySvc.SaveSnapshot(snapshotCtx, authCtx.UserID, threadID, runID, data)
		}
	}()

	return runResult
}

// ChatStream executes a chat request with streaming output.
// Returns a schema.StreamReader[*schema.Message] for the caller to consume chunks.
// The caller is responsible for saving messages to thread history after streaming completes.
func (r *Runner) ChatStream(authCtx *auth.AuthContext, threadID, userMessage string) (*schema.StreamReader[*schema.Message], error) {
	threadMessages := r.threads.Copy(authCtx.UserID, threadID)

	ctx := context.Background()
	ctx = injectAuthContext(ctx, authCtx, threadID, "r_stream")

	memCtx := r.memorySvc.BuildMemoryContext(ctx, authCtx.UserID)

	var systemContent string
	if r.steppedRunner != nil && r.steppedRunner.HasSubAgents() {
		systemContent = r.buildSupervisorPrompt(authCtx.Roles)
	} else {
		systemContent = r.buildSystemPrompt(authCtx.Roles)
	}
	if memCtx != "" {
		systemContent += "\n\n" + memCtx
	}

	messages := []*schema.Message{schema.SystemMessage(systemContent)}
	messages = append(messages, threadMessages...)
	messages = append(messages, schema.UserMessage(userMessage))

	// Sanitize orphaned tool_calls from interrupted runs (see Chat()).
	messages = sanitizeMessages(messages)

	// Apply context compression before streaming
	messages, _ = r.compressMessages(ctx, messages, nil)

	// Save user message
	threadMessages = append(threadMessages, schema.UserMessage(userMessage))
	r.threads.Replace(authCtx.UserID, threadID, threadMessages)

	return r.supervisor.Stream(ctx, messages)
}

// SaveAssistantMessage persists an assistant message to thread history.
func (r *Runner) SaveAssistantMessage(userID, threadID, content string) {
	r.threads.Append(userID, threadID, schema.AssistantMessage(content, nil))
}

// Resume resumes an interrupted run after an approval decision.
//
// 恢复语义（工具级中断 + 节点级中断 统一）：
// - approve: 执行工具，结果喂回 ReAct 循环，LLM 继续规划（从中断点继续）
// - reject: 拒绝消息喂回 ReAct 循环，LLM 重新规划（从中断点继续）
//
// 与旧实现的关键区别：reject 不再直接终止，而是让 LLM 收到拒绝反馈后继续规划。
// 例如用户拒绝删除订单后，LLM 会说"好的，订单保留"而不是直接中断。
func (r *Runner) Resume(authCtx *auth.AuthContext, interruptID string, decision hitl.ApprovalDecision) ChatRunResult {
	req, ok := r.hitlSvc.GetApproval(interruptID)
	if !ok {
		return ChatRunResult{Status: StatusError, Answer: "审批请求不存在"}
	}

	// Ownership check: only the user who triggered the interrupt may approve
	// or reject it. Non-owners get the same "not found" answer so interrupt
	// IDs of other users are not even confirmed to exist.
	if req.UserID != authCtx.UserID {
		return ChatRunResult{Status: StatusError, Answer: "审批请求不存在"}
	}

	// Idempotency: if this interrupt was already resumed (e.g. the user
	// double-clicked approve), return the cached result from the first call
	// instead of re-executing the gated tool and re-running the ReAct loop.
	r.mu.RLock()
	cached, resumeExists := r.resumeResults[interruptID]
	r.mu.RUnlock()
	if resumeExists {
		return cached
	}

	ctx := context.Background()
	ctx = injectAuthContext(ctx, authCtx, req.ThreadID, req.RunID)

	_, err := r.hitlSvc.Approve(ctx, interruptID, decision)
	if err != nil {
		return ChatRunResult{Status: StatusError, Answer: fmt.Sprintf("审批处理失败：%v", err)}
	}

	// Patch thread history: the interrupt stored an assistant message with tool_calls
	// but no corresponding tool result. We must append the tool result now, otherwise
	// subsequent LLM calls will 400 with "insufficient tool messages following tool_calls".
	r.patchInterruptToolResult(authCtx, req, decision)

	// Step 1: Determine the tool result to feed back into the ReAct loop
	var toolResultContent string
	var toolName string

	// Check if this is a node-level interrupt
	isNodeInterrupt := req.NodeName != ""

	if isNodeInterrupt {
		// Node-level interrupt resume: load full state from checkpoint and continue
		if r.steppedRunner != nil && r.memorySvc != nil {
			steppedState, loadErr := LoadFromCheckpoint(ctx, authCtx.UserID, req.ThreadID, req.RunID, r.memorySvc.GetCheckpointStore())
			if loadErr == nil && steppedState != nil {
				// Apply approval/rejection to the stepped state
				_, handleErr := r.steppedRunner.HandleApproval(ctx, steppedState, &InterruptRequest{
					InterruptID: req.InterruptID,
					Type:        hitl.InterruptTypeNode,
					NodeName:    req.NodeName,
					RunID:       req.RunID,
					ThreadID:    req.ThreadID,
				}, decision.Approved, decision.Reason)
				if handleErr != nil {
					return ChatRunResult{Status: StatusError, Answer: fmt.Sprintf("节点恢复处理失败：%v", handleErr)}
				}

				// Continue the ReAct loop from the saved state
				recorder := NewEventRecorder(req.RunID)
				for !steppedState.Done {
					var stepErr error
					var interruptReq2 *InterruptRequest
					steppedState, interruptReq2, stepErr = r.steppedRunner.RunStep(ctx, steppedState, recorder)
					if stepErr != nil {
						if isRateLimitError(stepErr.Error()) {
							continue
						}
						break
					}
					if interruptReq2 != nil {
						// Another interrupt during resume — save and return
						// (Could be another tool-level interrupt)
						break
					}
				}

				if steppedState.Done {
					// Save final state
					threadMessages := r.threads.Copy(authCtx.UserID, req.ThreadID)
					threadMessages = append(threadMessages, schema.AssistantMessage(steppedState.Answer, nil))
					r.threads.Replace(authCtx.UserID, req.ThreadID, threadMessages)

					result := ChatRunResult{
						RunID:  req.RunID,
						Status: StatusCompleted,
						Answer: steppedState.Answer,
					}
					r.mu.Lock()
					r.resumeResults[interruptID] = result
					r.mu.Unlock()
					return result
				}

				// Still interrupted (another interrupt hit during resume)
				result := ChatRunResult{
					RunID:  req.RunID,
					Status: StatusInterrupted,
					Answer: steppedState.Answer,
				}
				r.mu.Lock()
				r.resumeResults[interruptID] = result
				r.mu.Unlock()
				return result
			}
		}

		// Fallback for node-level interrupt without checkpoint:
		// Treat like a completed run with the approval result
		if decision.Approved {
			result := ChatRunResult{
				RunID:  req.RunID,
				Status: StatusCompleted,
				Answer: "计划已批准，继续执行。",
			}
			r.mu.Lock()
			r.resumeResults[interruptID] = result
			r.mu.Unlock()
			return result
		}
		result := ChatRunResult{
			RunID:  req.RunID,
			Status: StatusCompleted,
			Answer: "计划已被拒绝：" + decision.Reason,
		}
		r.mu.Lock()
		r.resumeResults[interruptID] = result
		r.mu.Unlock()
		return result
	}

	// Tool-level interrupt handling: try checkpoint-based resume first
	// (preserves full conversation history + intermediate results),
	// fall back to thread-based reconstruction if checkpoint unavailable.
	if r.steppedRunner != nil && r.memorySvc != nil {
		steppedState, loadErr := LoadFromCheckpoint(ctx, authCtx.UserID, req.ThreadID, req.RunID, r.memorySvc.GetCheckpointStore())
		if loadErr == nil && steppedState != nil {
		}
		if loadErr == nil && steppedState != nil {
			// Determine whether the gated tool is a direct pending call or
			// nested inside a sub-agent. When the supervisor routes to a
			// sub-agent (e.g. general_agent) and the sub-agent's internal
			// ReAct hits a dangerous tool (e.g. delete_order), the interrupt's
			// ToolName (delete_order) is NOT in the outer PendingToolCalls
			// (which holds general_agent). The two cases need different handling.
			isNestedTool := true
			for _, tc := range steppedState.PendingToolCalls {
				if tc.Name == req.ToolName {
					isNestedTool = false
					break
				}
			}

			if isNestedTool {
				// Nested sub-agent tool interrupt: resolve the decision at the
				// outer level by executing the gated tool (if approved) and
				// feeding its result as the sub-agent call's tool observation.
				if err := r.resolveNestedToolInterrupt(ctx, authCtx, steppedState, req, decision); err != nil {
					return ChatRunResult{Status: StatusError, Answer: err.Error()}
				}
			} else {
				// Direct tool interrupt: use HandleApproval to execute/skip
				_, handleErr := r.steppedRunner.HandleApproval(ctx, steppedState, &InterruptRequest{
					InterruptID: req.InterruptID,
					Type:        hitl.InterruptTypeTool,
					ToolName:    req.ToolName,
					Arguments:   req.Arguments,
					RunID:       req.RunID,
					ThreadID:    req.ThreadID,
				}, decision.Approved, decision.Reason)
				if handleErr != nil {
					return ChatRunResult{Status: StatusError, Answer: fmt.Sprintf("工具恢复处理失败：%v", handleErr)}
				}
			}

			recorder := NewEventRecorder(req.RunID)
			for !steppedState.Done {
				var stepErr error
				var interruptReq2 *InterruptRequest
				steppedState, interruptReq2, stepErr = r.steppedRunner.RunStep(ctx, steppedState, recorder)
				if stepErr != nil {
					if isRateLimitError(stepErr.Error()) {
						continue
					}
					break
				}
				if interruptReq2 != nil {
					break
				}
			}

			if steppedState.Done {
				threadMessages := r.threads.Copy(authCtx.UserID, req.ThreadID)
				threadMessages = append(threadMessages, schema.AssistantMessage(steppedState.Answer, nil))
				r.threads.Replace(authCtx.UserID, req.ThreadID, threadMessages)
				result := ChatRunResult{RunID: req.RunID, Status: StatusCompleted, Answer: steppedState.Answer}
				r.mu.Lock()
				r.resumeResults[interruptID] = result
				r.mu.Unlock()
				return result
			}
			result := ChatRunResult{RunID: req.RunID, Status: StatusInterrupted, Answer: steppedState.Answer}
			r.mu.Lock()
			r.resumeResults[interruptID] = result
			r.mu.Unlock()
			return result
		}
	}

	// Fallback: thread-based reconstruction (original logic)
	if decision.Approved {
		// Execute the approved tool (re-run with original args, idempotent)
		tool, found := r.registry.Get(req.ToolName)
		if !found {
			return ChatRunResult{Status: StatusError, Answer: fmt.Sprintf("工具 %s 不存在", req.ToolName)}
		}

		identity := auth.ToolIdentityFromContext(ctx)
		result := tool.Fn(identity, req.Arguments)
		toolName = req.ToolName

		if result.Error != "" {
			toolResultContent = result.Error
		} else {
			toolResultContent = result.Content
		}
	} else {
		// Rejected: feed rejection back as tool result so LLM can re-plan
		toolName = req.ToolName
		toolResultContent = "❌ 用户拒绝执行该操作"
		if decision.Reason != "" {
			toolResultContent += "：" + decision.Reason
		}
	}

	// Step 2: Load thread messages and reconstruct the conversation
	// Chat() already saved the assistant message with tool_calls to the thread
	// when it detected the interrupt. So we just need to append the tool result.
	threadMessages := r.threads.Copy(authCtx.UserID, req.ThreadID)

	// Generate ToolCallID if not already set (for backward compat)
	toolCallID := req.ToolCallID
	if toolCallID == "" {
		toolCallID = "tc_" + uuid.New().String()[:8]
	}

	// Check if the last message is already the assistant's tool_call message
	// (saved by Chat() when it detected the interrupt). If not, add it.
	needsAssistantMsg := true
	if len(threadMessages) > 0 {
		lastMsg := threadMessages[len(threadMessages)-1]
		if lastMsg.Role == schema.Assistant && len(lastMsg.ToolCalls) > 0 {
			for _, tc := range lastMsg.ToolCalls {
				if tc.Function.Name == toolName {
					needsAssistantMsg = false
					// Use the existing ToolCallID from the saved assistant message
					toolCallID = tc.ID
					break
				}
			}
		}
	}

	if needsAssistantMsg {
		// Insert the assistant message with tool_calls (the LLM's decision to call this tool)
		assistantMsg := &schema.Message{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{
				{
					ID: toolCallID,
					Function: schema.FunctionCall{
						Name:      toolName,
						Arguments: req.Arguments,
					},
				},
			},
		}
		threadMessages = append(threadMessages, assistantMsg)
	}

	// Append the tool result message
	threadMessages = append(threadMessages, &schema.Message{
		Role:       schema.Tool,
		Content:    toolResultContent,
		Name:       toolName,
		ToolCallID: toolCallID,
	})

	// Step 3: Re-enter the ReAct loop with the updated conversation
	// This is "resume from interrupt point" — the LLM sees the tool result
	// and continues planning from where it left off
	memCtx := r.memorySvc.BuildMemoryContext(ctx, authCtx.UserID)
	vectorCtx := r.memorySvc.QueryVectorMemory(ctx, authCtx.UserID, "", 3)

	var systemContent string
	if r.steppedRunner != nil && r.steppedRunner.HasSubAgents() {
		systemContent = r.buildSupervisorPrompt(authCtx.Roles)
	} else {
		systemContent = r.buildSystemPrompt(authCtx.Roles)
	}
	if memCtx != "" {
		systemContent += "\n\n" + memCtx
	}
	if vectorCtx != "" {
		systemContent += "\n\n" + vectorCtx
	}

	messages := append([]*schema.Message{schema.SystemMessage(systemContent)}, threadMessages...)
	messages, _ = r.compressMessages(ctx, messages, nil)

	// Run the agent again with the full conversation (including tool result)
	var result SupervisorRunResult
	if r.steppedRunner != nil {
		result = r.steppedRunner.RunAll(ctx, messages, nil)
	} else {
		result = r.supervisor.Run(ctx, messages, nil)
	}

	// Step 4: Save the final assistant message to thread history
	threadMessages = append(threadMessages, schema.AssistantMessage(result.Answer, nil))
	r.threads.Replace(authCtx.UserID, req.ThreadID, threadMessages)

	finalResult := ChatRunResult{
		RunID:  req.RunID,
		Status: StatusCompleted,
		Answer: result.Answer,
	}
	r.mu.Lock()
	r.resumeResults[interruptID] = finalResult
	r.mu.Unlock()

	return finalResult
}

// resolveNestedToolInterrupt handles a tool-level interrupt that originated
// inside a sub-agent. The outer SteppedRunner's PendingToolCalls holds the
// sub-agent call (e.g. general_agent), not the gated tool itself (e.g.
// delete_order). The sub-agent's internal state is not preserved across the
// interrupt, so we resolve the decision at the outer level: execute the gated
// tool (if approved) — or record the rejection — and feed the result as the
// sub-agent call's tool observation, so the outer ReAct loop continues with a
// concrete outcome instead of re-running the sub-agent and re-triggering the
// interrupt in a loop.
func (r *Runner) resolveNestedToolInterrupt(ctx context.Context, authCtx *auth.AuthContext, state *SteppedRunState, req *hitl.ApprovalRequest, decision hitl.ApprovalDecision) error {
	if len(state.PendingToolCalls) == 0 {
		return fmt.Errorf("no pending tool calls to resume")
	}
	// The sub-agent call is the first (typically only) pending call.
	subAgentTC := state.PendingToolCalls[0]

	var content string
	if decision.Approved {
		tool, found := r.registry.Get(req.ToolName)
		if !found {
			return fmt.Errorf("工具 %s 不存在", req.ToolName)
		}
		identity := auth.ToolIdentityFromContext(ctx)
		result := tool.Fn(identity, req.Arguments)
		if result.Error != "" {
			content = result.Error
		} else {
			content = result.Content
		}
	} else {
		content = "❌ 用户拒绝执行该操作"
		if decision.Reason != "" {
			content += "：" + decision.Reason
		}
	}

	state.Messages = append(state.Messages, SchemaMessage{
		Role:       "tool",
		Content:    content,
		ToolCallID: subAgentTC.ID,
		Name:       subAgentTC.Name,
	})
	state.PendingToolCalls = nil
	return nil
}

// GetRun returns a stored run result, but only when it belongs to userID.
// Runs of other users are reported as not found so run IDs (short, guessable)
// leak neither events nor existence across users.
func (r *Runner) GetRun(runID, userID string) (*ChatRunResult, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.runOwners[runID] != userID {
		return nil, false
	}
	result, ok := r.runs[runID]
	return result, ok
}

// GetThreadMessages returns messages for the user's thread.
func (r *Runner) GetThreadMessages(userID, threadID string) []*schema.Message {
	return r.threads.Copy(userID, threadID)
}

// CreateThread idempotently creates a thread in the user's namespace.
func (r *Runner) CreateThread(userID, threadID string) {
	r.threads.Create(userID, threadID)
}

// DeleteThread removes the user's thread. Returns false when the user has
// no such thread (a thread owned by another user is never deleted).
func (r *Runner) DeleteThread(userID, threadID string) bool {
	return r.threads.Delete(userID, threadID)
}

// ListThreads returns the thread IDs owned by the user.
func (r *Runner) ListThreads(userID string) []string {
	return r.threads.List(userID)
}

// ThreadMessagesJSON returns thread messages as JSON-serializable maps.
func (r *Runner) ThreadMessagesJSON(userID, threadID string) []map[string]any {
	msgs := r.GetThreadMessages(userID, threadID)
	result := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, map[string]any{
			"role":    string(m.Role),
			"content": m.Content,
		})
	}
	return result
}

// injectAuthContext injects the authenticated identity into the Go context
// for the whole agent run. The AuthContext copy carries ThreadID/RunID so the
// typed ToolIdentity derived downstream (tools, ACL, HITL) is complete.
func injectAuthContext(ctx context.Context, authCtx *auth.AuthContext, threadID, runID string) context.Context {
	return auth.WithAuthContext(ctx, authCtx.WithThread(threadID).WithRun(runID))
}

// isRateLimitError checks if the error message indicates a 429 rate limit.
// wantsNodeInterrupt checks if the user's message signals a "review before execute" intent.
// When true, the SteppedRunner will pause after the LLM decides tool calls (but before
// executing them) so a human can review and approve/reject the plan.
func wantsNodeInterrupt(msg string) bool {
	lower := strings.ToLower(msg)
	keywords := []string{
		"确认后再执行",
		"确认后执行",
		"先确认再",
		"先审批再",
		"审批后再执行",
		"审核后",
		"确认一下再",
		"请确认",
		"计划审批",
		"生成计划",
		"执行计划",
		"先制定计划",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isRateLimitError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	return strings.Contains(lower, "429") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "tpm") ||
		strings.Contains(lower, "rpm")
}

// compressMessages applies context compression to the message list before sending
// to the agent. Converts between Eino schema.Message and contextmgr.Message formats.
// Returns the (possibly compressed) messages and a TokenInfo snapshot.
func (r *Runner) compressMessages(ctx context.Context, messages []*schema.Message, recorder *EventRecorder) ([]*schema.Message, *TokenInfo) {
	if r.summarizer == nil || len(messages) <= 4 {
		// Still compute token info for display even when skipping compression
		if r.summarizer != nil {
			ctxMsgs := einoToContextMessages(messages)
			tokens := r.summarizer.CountTokens(ctxMsgs)
			threshold := int(float64(r.maxTokens) * r.summarizer.ThresholdRatio())
			return messages, &TokenInfo{Current: tokens, Threshold: threshold, Max: r.maxTokens}
		}
		return messages, nil
	}

	// Convert Eino schema.Message -> contextmgr.Message
	ctxMsgs := einoToContextMessages(messages)
	currentTokens := r.summarizer.CountTokens(ctxMsgs)
	threshold := int(float64(r.maxTokens) * r.summarizer.ThresholdRatio())

	// Check if compression is needed
	if !r.summarizer.ShouldSummarize(ctxMsgs, r.maxTokens) {
		return messages, &TokenInfo{Current: currentTokens, Threshold: threshold, Max: r.maxTokens}
	}

	// Compress using LLM-based summarization (with rule-based fallback)
	compressed := r.summarizer.Compress(ctx, ctxMsgs, r.maxTokens)
	compressedTokens := r.summarizer.CountTokens(compressed)

	// Record the compression event
	if recorder != nil {
		recorder.Record(EventSummaryCompress, fmt.Sprintf("Context compressed: %d -> %d messages, %d -> %d tokens", len(ctxMsgs), len(compressed), currentTokens, compressedTokens), map[string]any{
			"before":        len(ctxMsgs),
			"after":         len(compressed),
			"tokens_before": currentTokens,
			"tokens_after":  compressedTokens,
		})
	}

	// Convert back: contextmgr.Message -> Eino schema.Message
	return contextToEinoMessages(compressed), &TokenInfo{Current: compressedTokens, Threshold: threshold, Max: r.maxTokens, Compressed: true}
}

// einoToContextMessages converts Eino schema.Message slice to contextmgr.Message slice.
func einoToContextMessages(msgs []*schema.Message) []contextmgr.Message {
	result := make([]contextmgr.Message, 0, len(msgs))
	for _, m := range msgs {
		cm := contextmgr.Message{
			Role:     string(m.Role),
			Content:  m.Content,
			Name:     m.Name,
			IsSystem: m.Role == schema.System,
		}

		// Convert tool calls
		if len(m.ToolCalls) > 0 {
			cm.ToolCalls = make([]contextmgr.ToolCallRef, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				cm.ToolCalls = append(cm.ToolCalls, contextmgr.ToolCallRef{
					ID:       tc.ID,
					Name:     tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		}

		// Tool result message
		if m.Role == schema.Tool {
			cm.ToolID = m.ToolCallID
		}

		result = append(result, cm)
	}
	return result
}

// contextToEinoMessages converts contextmgr.Message slice back to Eino schema.Message slice.
func contextToEinoMessages(msgs []contextmgr.Message) []*schema.Message {
	result := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "system":
			result = append(result, schema.SystemMessage(m.Content))
		case "user":
			result = append(result, schema.UserMessage(m.Content))
		case "assistant":
			if len(m.ToolCalls) > 0 {
				// Assistant message with tool calls
				toolCalls := make([]schema.ToolCall, 0, len(m.ToolCalls))
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
			} else if m.IsSummary {
				// Summary message: inject as assistant message with summary prefix
				result = append(result, schema.AssistantMessage(m.Content, nil))
			} else {
				result = append(result, schema.AssistantMessage(m.Content, nil))
			}
		case "tool":
			result = append(result, &schema.Message{
				Role:       schema.Tool,
				Content:    m.Content,
				Name:       m.Name,
				ToolCallID: m.ToolID,
			})
		default:
			result = append(result, schema.AssistantMessage(m.Content, nil))
		}
	}
	return result
}

// buildSystemPrompt generates a system prompt that only lists tools
// the user is allowed to invoke based on their roles.
func (r *Runner) buildSystemPrompt(roles []string) string {
	allTools := map[string]string{
		"calculator":   "数学计算",
		"weather":      "天气查询（查询任何城市的实时天气）",
		"grep":         "日志搜索",
		"query_order":  "查询订单",
		"delete_order": "删除订单（需要审批）",
		"send_email":   "发送邮件（需要审批）",
	}

	var toolLines []string
	for name, desc := range allTools {
		if r.rbac != nil && r.rbac.CanInvokeTool(nil, roles, name) {
			toolLines = append(toolLines, fmt.Sprintf("  - %s：%s", name, desc))
		}
	}

	prompt := "你是一个智能助手，拥有多种工具来帮助用户完成任务。\n\n" +
		"重要规则：\n" +
		"- 当用户的请求可以通过工具完成时，你必须调用相应的工具，不要只用文字回答。\n" +
		"- 可用工具：\n" +
		strings.Join(toolLines, "\n") + "\n" +
		"- 调用工具时，确保参数完整准确。\n" +
		"- 如果用户请求的操作没有对应的可用工具，请告知用户你没有该权限，不要尝试调用。"

	return prompt
}

// buildSupervisorPrompt generates a system prompt for the supervisor routing mode.
// The supervisor LLM sees sub-agent names (not real tools) and routes user
// requests to the appropriate sub-agent.
func (r *Runner) buildSupervisorPrompt(roles []string) string {
	// Build sub-agent descriptions filtered by RBAC
	type agentInfo struct {
		name string
		desc string
	}

	allAgents := map[string]agentInfo{
		"math_agent":    {name: "math_agent", desc: "数学计算助手。处理计算、算术运算、数学问题。内部工具：calculator。"},
		"search_agent":  {name: "search_agent", desc: "信息搜索助手。处理天气查询、日志搜索、信息查找。内部工具：weather（天气查询）、grep（日志搜索）。"},
		"general_agent": {name: "general_agent", desc: "通用业务助手。处理订单查询、删除订单（需审批）、发送邮件（需审批）。内部工具：query_order、delete_order、send_email。"},
	}

	// Map sub-agents to the tools they contain, so we can filter by RBAC
	agentToolDeps := map[string][]string{
		"math_agent":    {"calculator"},
		"search_agent":  {"weather", "grep"},
		"general_agent": {"query_order", "delete_order", "send_email"},
	}

	var agentLines []string
	for agentName, info := range allAgents {
		// Check if the user has permission for at least one tool in this agent
		deps := agentToolDeps[agentName]
		hasAccess := false
		for _, toolName := range deps {
			if r.rbac != nil && r.rbac.CanInvokeTool(nil, roles, toolName) {
				hasAccess = true
				break
			}
		}
		if hasAccess {
			agentLines = append(agentLines, fmt.Sprintf("  - %s：%s", agentName, info.desc))
		}
	}

	prompt := "你是一个智能调度助手（Supervisor），负责将用户请求路由到专业的子 Agent。\n\n" +
		"重要规则：\n" +
		"- 根据用户请求选择最合适的子 Agent，将用户的完整问题传递给它。\n" +
		"- 不要自己直接计算或查询，交给专业子 Agent 处理。\n" +
		"- 子 Agent 会返回处理结果，你需要汇总后用自然语言回复用户。\n" +
		"- 如果用户的问题不需要任何工具，可以直接回答。\n\n" +
		"可用子 Agent：\n" +
		strings.Join(agentLines, "\n")

	return prompt
}

// patchInterruptToolResult fixes the thread history after an interrupt.
// When a tool-level interrupt occurred, Chat() stored an assistant message with
// tool_calls but no corresponding tool result. This violates the LLM API contract
// (assistant+tool_calls must be followed by a tool message). We append the tool
// result now so subsequent requests won't 400.
func (r *Runner) patchInterruptToolResult(authCtx *auth.AuthContext, req *hitl.ApprovalRequest, decision hitl.ApprovalDecision) {
	threadMsgs := r.threads.Copy(authCtx.UserID, req.ThreadID)

	// Find the last assistant message with tool_calls that has no matching tool result
	var toolCallID string
	var toolCallName string
	for i := len(threadMsgs) - 1; i >= 0; i-- {
		msg := threadMsgs[i]
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			tc := msg.ToolCalls[len(msg.ToolCalls)-1]
			toolCallID = tc.ID
			toolCallName = tc.Function.Name
			break
		}
	}
	if toolCallID == "" {
		return // no orphan tool_call found
	}

	// Check if a tool result already exists for this toolCallID
	for _, msg := range threadMsgs {
		if msg.Role == schema.Tool && msg.ToolCallID == toolCallID {
			return // already patched
		}
	}

	// Build the tool result content.
	// NOTE: we deliberately do NOT execute the real tool here. The actual
	// execution happens later in this Resume() call — either in
	// resolveNestedToolInterrupt (checkpoint path) or the thread-based
	// fallback. Executing here too would run the tool TWICE, which breaks
	// non-idempotent tools like delete_order (the second run finds the order
	// already deleted and reports "not found", masking the real success).
	// The outer thread only needs a syntactically valid tool result so the
	// conversation history doesn't 400 on the next request; the real outcome
	// is captured in the final assistant answer appended by the resume path.
	var content string
	if decision.Approved {
		content = fmt.Sprintf("✅ 操作已批准，正在执行 %s。", req.ToolName)
	} else {
		content = "❌ 用户拒绝执行该操作"
		if decision.Reason != "" {
			content += "：" + decision.Reason
		}
	}

	// Append the tool result message
	threadMsgs = append(threadMsgs, &schema.Message{
		Role:       schema.Tool,
		Content:    content,
		ToolCallID: toolCallID,
		Name:       toolCallName,
	})
	r.threads.Replace(authCtx.UserID, req.ThreadID, threadMsgs)
}
