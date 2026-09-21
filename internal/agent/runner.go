package agent

import (
	"context"
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
)

// RunStatus represents the status of an agent run.
type RunStatus string

const (
	StatusCompleted   RunStatus = "completed"
	StatusInterrupted RunStatus = "interrupted"
	StatusError       RunStatus = "error"
	StatusCancelled   RunStatus = "cancelled"
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
	Current    int  `json:"current"`              // tokens in the context sent to the LLM
	Threshold  int  `json:"threshold"`            // compression trigger threshold
	Max        int  `json:"max"`                  // context window limit
	Compressed bool `json:"compressed,omitempty"` // true if compression was applied
}

// InterruptPlanStep is one planned action shown on node-level approval cards.
type InterruptPlanStep struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// Interrupt describes an interruption in an agent run.
type Interrupt struct {
	InterruptID string `json:"interrupt_id"`
	ToolName    string `json:"tool_name,omitempty"`
	NodeName    string `json:"node_name,omitempty"`
	Arguments   string `json:"arguments,omitempty"`
	Message     string `json:"message"`
	Type        string `json:"type"`
	// Plan carries the structured planned steps for node-level (plan review)
	// interrupts, so the UI can render them without parsing message text.
	Plan []InterruptPlanStep `json:"plan,omitempty"`
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
	runtimeMu          sync.RWMutex
	locksMu            sync.Mutex
	threadLocks        map[threadKey]*runLock
	mu                 sync.RWMutex
	supervisor         *SupervisorAgent
	steppedRunner      *SteppedRunner
	hitlSvc            *hitl.Service
	registry           *tools.ToolRegistry
	rbac               *auth.RBACManager
	memorySvc          *memory.Service
	summarizer         *contextmgr.Summarizer
	counter            contextmgr.TokenCounter
	runs               map[string]*ChatRunResult
	runOwners          map[string]string // runID -> owner userID (isolation for run events)
	runStore           RunStore
	runRetention       time.Duration
	interruptPublisher InterruptPublisher
	resumePublisher    ResumePublisher
	threads            ThreadStore // per-user conversation threads
	maxTokens          int
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
		runRetention:  7 * 24 * time.Hour,
		threadLocks:   make(map[threadKey]*runLock),
		threads:       newThreadStore(),
		maxTokens:     maxTokens,
	}
}

// SetSupervisor replaces the supervisor agent (used for model switching).
func (r *Runner) SetSupervisor(s *SupervisorAgent) {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.supervisor = s
}

// SetSteppedRunner replaces the stepped runner (used for model switching).
func (r *Runner) SetSteppedRunner(sr *SteppedRunner) {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steppedRunner = sr
}

// UseFileThreads swaps in a file-persisted thread store so conversation
// histories survive process restarts. Call during startup, before serving —
// the swap itself is not synchronized against live traffic.
func (r *Runner) UseFileThreads(path string) error {
	ts, err := NewFileThreadStore(path)
	if err != nil {
		return err
	}
	r.threads = ts
	return nil
}

// UseThreadStore installs a database-backed store during startup.
func (r *Runner) UseThreadStore(store ThreadStore) {
	r.threads = store
}

// ExtractAndSavePreferences extracts user preferences from a message and saves to long-term memory.
// Called after streaming completes to ensure preferences are captured in all modes.
func (r *Runner) ExtractAndSavePreferences(authCtx *auth.AuthContext, userMessage string) {
	ctx := context.Background()
	_ = r.memorySvc.ExtractAndSave(ctx, authCtx.UserID, authCtx.ThreadID, userMessage)
}

// ChatOption customizes a single chat run.
type ChatOption func(*chatOptions)

type chatOptions struct {
	confirmBeforeExecute bool
	sink                 ProgressSink
}

// WithConfirmBeforeExecute enables the node-level plan review interrupt for
// this run: execution pauses after the LLM decides on tool calls but before
// executing them, presenting the plan for human approval. The flag is
// explicit per request (frontend toggle / API field) — no keyword guessing.
func WithConfirmBeforeExecute() ChatOption {
	return func(o *chatOptions) { o.confirmBeforeExecute = true }
}

// WithProgressSink attaches a sink that receives events and content fragments
// while the run is still executing, so a caller can stream progress to a client
// instead of waiting for the final result.
func WithProgressSink(sink ProgressSink) ChatOption {
	return func(o *chatOptions) { o.sink = sink }
}

// Chat executes a chat request through the Eino agent pipeline.
func (r *Runner) Chat(authCtx *auth.AuthContext, threadID, userMessage string, opts ...ChatOption) ChatRunResult {
	return r.ChatContext(context.Background(), authCtx, threadID, userMessage, opts...)
}

// ChatStream is a compatibility adapter over the same checked execution path.
// For actionable approval payloads, use ChatContext (as the HTTP SSE handler does).
func (r *Runner) ChatStream(ac *auth.AuthContext, threadID, message string) (*schema.StreamReader[*schema.Message], error) {
	result := r.Chat(ac, threadID, message)
	if result.Status != StatusCompleted {
		return nil, fmt.Errorf("run %s: %s: %s", result.RunID, result.Status, result.Answer)
	}
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(result.Answer, nil)}), nil
}

// SaveAssistantMessage persists an assistant message to thread history.
func (r *Runner) SaveAssistantMessage(userID, threadID, content string) error {
	return r.SaveAssistantMessageContext(context.Background(), userID, threadID, content)
}

func (r *Runner) SaveAssistantMessageContext(ctx context.Context, userID, threadID, content string) error {
	unlock, err := r.lockThread(ctx, userID, threadID)
	if err != nil {
		return err
	}
	defer unlock()
	if store, ok := r.threads.(ContextThreadMutator); ok {
		return store.AppendContext(ctx, userID, threadID, schema.AssistantMessage(content, nil))
	}
	return r.threads.Append(userID, threadID, schema.AssistantMessage(content, nil))
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
	return r.ResumeContext(context.Background(), authCtx, interruptID, decision)
}

// GetRunContext returns only the requesting user's run and distinguishes a
// missing run from a storage failure.
func (r *Runner) GetRunContext(ctx context.Context, runID, userID string) (*ChatRunResult, bool, error) {
	if r.runStore != nil {
		return r.runStore.Load(ctx, userID, runID)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.runOwners[runID] != userID {
		return nil, false, nil
	}
	result, ok := r.runs[runID]
	return result, ok, nil
}

// UseRunStore enables shared persistence for run results and events.
func (r *Runner) UseRunStore(store RunStore, retention time.Duration) {
	r.runStore = store
	if retention > 0 {
		r.runRetention = retention
	}
}

// UseInterruptPublisher enables atomic publication of resumable interrupts.
func (r *Runner) UseInterruptPublisher(publisher InterruptPublisher) {
	r.interruptPublisher = publisher
}

// UseResumePublisher enables atomic publication of approval outcomes.
func (r *Runner) UseResumePublisher(publisher ResumePublisher) {
	r.resumePublisher = publisher
}

// GetThreadMessages returns messages for the user's thread.
func (r *Runner) GetThreadMessages(userID, threadID string) []*schema.Message {
	return r.threads.Copy(userID, threadID)
}

func (r *Runner) GetThreadMessagesContext(ctx context.Context, userID, threadID string) ([]*schema.Message, error) {
	return r.copyThread(ctx, userID, threadID)
}

// CreateThread idempotently creates a thread in the user's namespace.
func (r *Runner) CreateThread(userID, threadID string) error {
	return r.CreateThreadContext(context.Background(), userID, threadID)
}

func (r *Runner) CreateThreadContext(ctx context.Context, userID, threadID string) error {
	if store, ok := r.threads.(ContextThreadMutator); ok {
		return store.CreateContext(ctx, userID, threadID)
	}
	return r.threads.Create(userID, threadID)
}

// DeleteThread removes the user's thread. Returns false when the user has
// no such thread (a thread owned by another user is never deleted).
func (r *Runner) DeleteThread(userID, threadID string) (bool, error) {
	return r.DeleteThreadContext(context.Background(), userID, threadID)
}

func (r *Runner) DeleteThreadContext(ctx context.Context, userID, threadID string) (bool, error) {
	unlock, err := r.lockThread(ctx, userID, threadID)
	if err != nil {
		return false, err
	}
	defer unlock()
	if store, ok := r.threads.(ContextThreadMutator); ok {
		return store.DeleteContext(ctx, userID, threadID)
	}
	return r.threads.Delete(userID, threadID)
}

// ListThreads returns the thread IDs owned by the user.
func (r *Runner) ListThreads(userID string) []string {
	return r.threads.List(userID)
}

func (r *Runner) ListThreadsContext(ctx context.Context, userID string) ([]string, error) {
	return r.listThreads(ctx, userID)
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

// ThreadTokenInfo estimates a thread's context usage without running the
// agent: system prompt + memory context (read-only — no access reinforcement)
// + the thread's own history. Used by the UI token bar so each conversation
// shows its own usage immediately on thread switch.
func (r *Runner) ThreadTokenInfo(userID string, roles []string, threadID string) *TokenInfo {
	r.runtimeMu.RLock()
	defer r.runtimeMu.RUnlock()
	if r.summarizer == nil {
		return nil
	}
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		UserID:   userID,
		Roles:    roles,
		ThreadID: threadID,
	})

	var systemContent string
	if r.steppedRunner != nil && r.steppedRunner.HasSubAgents() {
		systemContent = r.buildSupervisorPrompt(roles)
	} else {
		systemContent = r.buildSystemPrompt(roles)
	}
	if memCtx := r.memorySvc.RetrieveRelevant(ctx, userID, "", 0, false); memCtx != "" {
		systemContent += "\n\n" + memCtx
	}

	messages := []*schema.Message{schema.SystemMessage(systemContent)}
	threadMessages, err := r.copyThread(ctx, userID, threadID)
	if err != nil {
		return nil
	}
	messages = append(messages, threadMessages...)

	ctxMsgs := einoToContextMessages(messages)
	tokens := r.summarizer.CountTokens(ctxMsgs)
	threshold := int(float64(r.maxTokens) * r.summarizer.ThresholdRatio())
	return &TokenInfo{Current: tokens, Threshold: threshold, Max: r.maxTokens}
}

// isRateLimitError checks if the error message indicates a 429 rate limit.
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
					ID:        tc.ID,
					Name:      tc.Function.Name,
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
