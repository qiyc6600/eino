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
	RunID       string     `json:"run_id"`
	Status      RunStatus  `json:"status"`
	Answer      string     `json:"answer,omitempty"`
	Interrupt   *Interrupt `json:"interrupt,omitempty"`
	Events      []Event    `json:"events,omitempty"`
	RoutedAgent string     `json:"routed_agent,omitempty"`
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
	threads       map[string][]*schema.Message // threadID -> conversation history (Eino schema.Message)
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
		threads:       make(map[string][]*schema.Message),
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

	// Load thread history
	threadMessages := r.getThreadMessages(threadID)

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

	// Apply context compression before sending to the agent.
	// Converts Eino schema.Message -> contextmgr.Message -> compress -> convert back.
	// This ensures long conversations don't exceed the token window.
	// If compression happened, replace the thread history with the compressed version
	// so that we don't re-compress the same old messages on every turn.
	compressedMessages := r.compressMessages(ctx, messages, recorder)
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
			Events:      recorder.Events(),
			RoutedAgent: result.RoutedAgent,
		}

		r.mu.Lock()
		r.runs[runID] = &runResult
		r.threads[threadID] = threadMessagesWithToolCall
		r.mu.Unlock()

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
		RunID:       runID,
		Status:      StatusCompleted,
		Answer:      result.Answer,
		Events:      recorder.Events(),
		RoutedAgent: result.RoutedAgent,
	}

	r.mu.Lock()
	r.runs[runID] = &runResult
	r.threads[threadID] = savedThreadMessages
	r.mu.Unlock()

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
	threadMessages := r.getThreadMessages(threadID)

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

	// Apply context compression before streaming
	messages = r.compressMessages(ctx, messages, nil)

	// Save user message
	threadMessages = append(threadMessages, schema.UserMessage(userMessage))
	r.mu.Lock()
	r.threads[threadID] = threadMessages
	r.mu.Unlock()

	return r.supervisor.Stream(ctx, messages)
}

// SaveAssistantMessage persists an assistant message to thread history.
func (r *Runner) SaveAssistantMessage(threadID, content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	threadMessages := r.threads[threadID]
	threadMessages = append(threadMessages, schema.AssistantMessage(content, nil))
	r.threads[threadID] = threadMessages
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

	ctx := context.Background()
	ctx = injectAuthContext(ctx, authCtx, req.ThreadID, req.RunID)

	_, err := r.hitlSvc.Approve(ctx, interruptID, decision)
	if err != nil {
		return ChatRunResult{Status: StatusError, Answer: fmt.Sprintf("审批处理失败：%v", err)}
	}

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
					threadMessages := r.getThreadMessages(req.ThreadID)
					threadMessages = append(threadMessages, schema.AssistantMessage(steppedState.Answer, nil))
					r.mu.Lock()
					r.threads[req.ThreadID] = threadMessages
					r.mu.Unlock()

					return ChatRunResult{
						RunID:  req.RunID,
						Status: StatusCompleted,
						Answer: steppedState.Answer,
					}
				}

				// Still interrupted (another interrupt hit during resume)
				return ChatRunResult{
					RunID:  req.RunID,
					Status: StatusInterrupted,
					Answer: steppedState.Answer,
				}
			}
		}

		// Fallback for node-level interrupt without checkpoint:
		// Treat like a completed run with the approval result
		if decision.Approved {
			return ChatRunResult{
				RunID:  req.RunID,
				Status: StatusCompleted,
				Answer: "计划已批准，继续执行。",
			}
		}
		return ChatRunResult{
			RunID:  req.RunID,
			Status: StatusCompleted,
			Answer: "计划已被拒绝：" + decision.Reason,
		}
	}

	// Tool-level interrupt handling: try checkpoint-based resume first
	// (preserves full conversation history + intermediate results),
	// fall back to thread-based reconstruction if checkpoint unavailable.
	if r.steppedRunner != nil && r.memorySvc != nil {
		steppedState, loadErr := LoadFromCheckpoint(ctx, authCtx.UserID, req.ThreadID, req.RunID, r.memorySvc.GetCheckpointStore())
		if loadErr == nil && steppedState != nil {
			// Resume from full state: apply approval and continue ReAct loop
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
				threadMessages := r.getThreadMessages(req.ThreadID)
				threadMessages = append(threadMessages, schema.AssistantMessage(steppedState.Answer, nil))
				r.mu.Lock()
				r.threads[req.ThreadID] = threadMessages
				r.mu.Unlock()
				return ChatRunResult{RunID: req.RunID, Status: StatusCompleted, Answer: steppedState.Answer}
			}
			return ChatRunResult{RunID: req.RunID, Status: StatusInterrupted, Answer: steppedState.Answer}
		}
	}

	// Fallback: thread-based reconstruction (original logic)
	if decision.Approved {
		// Execute the approved tool (re-run with original args, idempotent)
		tool, found := r.registry.Get(req.ToolName)
		if !found {
			return ChatRunResult{Status: StatusError, Answer: fmt.Sprintf("工具 %s 不存在", req.ToolName)}
		}

		toolCtx := map[string]any{"user_id": authCtx.UserID, "roles": authCtx.Roles}
		result := tool.Fn(toolCtx, req.Arguments)
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
	threadMessages := r.getThreadMessages(req.ThreadID)

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
	messages = r.compressMessages(ctx, messages, nil)

	// Run the agent again with the full conversation (including tool result)
	var result SupervisorRunResult
	if r.steppedRunner != nil {
		result = r.steppedRunner.RunAll(ctx, messages, nil)
	} else {
		result = r.supervisor.Run(ctx, messages, nil)
	}

	// Step 4: Save the final assistant message to thread history
	threadMessages = append(threadMessages, schema.AssistantMessage(result.Answer, nil))
	r.mu.Lock()
	r.threads[req.ThreadID] = threadMessages
	r.mu.Unlock()

	return ChatRunResult{
		RunID:  req.RunID,
		Status: StatusCompleted,
		Answer: result.Answer,
	}
}

// GetRun returns a stored run result.
func (r *Runner) GetRun(runID string) (*ChatRunResult, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result, ok := r.runs[runID]
	return result, ok
}

// GetThreadMessages returns messages for a thread.
func (r *Runner) GetThreadMessages(threadID string) []*schema.Message {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.threads[threadID]
}

func (r *Runner) getThreadMessages(threadID string) []*schema.Message {
	r.mu.RLock()
	defer r.mu.RUnlock()
	msgs := r.threads[threadID]
	result := make([]*schema.Message, len(msgs))
	copy(result, msgs)
	return result
}

// CreateThread creates a new thread.
func (r *Runner) CreateThread(threadID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.threads[threadID]; !ok {
		r.threads[threadID] = []*schema.Message{}
	}
}

// DeleteThread removes a thread and its messages.
func (r *Runner) DeleteThread(threadID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.threads, threadID)
}

// ListThreads returns all thread IDs.
func (r *Runner) ListThreads() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.threads))
	for id := range r.threads {
		ids = append(ids, id)
	}
	return ids
}

// ThreadMessagesJSON returns thread messages as JSON-serializable maps.
func (r *Runner) ThreadMessagesJSON(threadID string) []map[string]any {
	msgs := r.GetThreadMessages(threadID)
	result := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, map[string]any{
			"role":    string(m.Role),
			"content": m.Content,
		})
	}
	return result
}

// injectAuthContext injects auth context values into the Go context for tool execution.
// Uses the exported auth.ContextKey so all packages can read the values.
func injectAuthContext(ctx context.Context, authCtx *auth.AuthContext, threadID, runID string) context.Context {
	// Inject AuthContext itself (for auth checks)
	ctx = auth.WithAuthContext(ctx, authCtx)
	// Inject tool context map (for tool execution — user_id, roles, etc.)
	toolCtxMap := map[string]any{
		"user_id":   authCtx.UserID,
		"roles":     authCtx.Roles,
		"thread_id": threadID,
		"run_id":    runID,
	}
	ctx = auth.WithToolContext(ctx, toolCtxMap)
	return ctx
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
func (r *Runner) compressMessages(ctx context.Context, messages []*schema.Message, recorder *EventRecorder) []*schema.Message {
	if r.summarizer == nil || len(messages) <= 4 {
		return messages // Not enough to compress or no summarizer configured
	}

	// Convert Eino schema.Message -> contextmgr.Message
	ctxMsgs := einoToContextMessages(messages)

	// Check if compression is needed
	if !r.summarizer.ShouldSummarize(ctxMsgs, r.maxTokens) {
		return messages
	}

	// Compress using LLM-based summarization (with rule-based fallback)
	compressed := r.summarizer.Compress(ctx, ctxMsgs, r.maxTokens)

	// Record the compression event
	if recorder != nil {
		recorder.Record(EventSummaryCompress, fmt.Sprintf("Context compressed: %d -> %d messages", len(ctxMsgs), len(compressed)), map[string]any{
			"before": len(ctxMsgs),
			"after":  len(compressed),
		})
	}

	// Convert back: contextmgr.Message -> Eino schema.Message
	return contextToEinoMessages(compressed)
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
