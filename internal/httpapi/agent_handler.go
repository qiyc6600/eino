package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
)

// AgentHandler handles agent chat API endpoints.
type AgentHandler struct {
	runner    *agent.Runner
	memorySvc *memory.Service
}

// NewAgentHandler creates a new AgentHandler.
func NewAgentHandler(runner *agent.Runner, memorySvc *memory.Service) *AgentHandler {
	return &AgentHandler{runner: runner, memorySvc: memorySvc}
}

// ChatRequest is the request body for POST /api/agent/chat
type ChatRequest struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
	Stream   bool   `json:"stream"` // if true, use SSE streaming
	// ConfirmBeforeExecute enables the node-level plan review interrupt for
	// this request: the run pauses after the LLM decides on tool calls and
	// waits for human approval before executing them.
	ConfirmBeforeExecute bool `json:"confirmBeforeExecute"`
}

// Chat handles POST /api/agent/chat
// Supports both regular JSON response and SSE streaming based on "stream" field.
func (h *AgentHandler) Chat(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req ChatRequest
	if !decodeBody(w, r, &req) {
		return
	}

	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	if req.ThreadID == "" {
		req.ThreadID = "t_default"
	}
	// Copy-on-write: never mutate the shared AuthContext in place —
	// the pointer is captured by goroutines (preference extraction, SSE).
	ac = ac.WithThread(req.ThreadID)

	if req.Stream {
		h.chatStream(w, r, ac, &req)
		return
	}

	// Non-streaming: original behavior
	result := h.runner.ChatContext(r.Context(), ac, req.ThreadID, req.Message, chatOptionsFromRequest(&req)...)
	writeJSON(w, http.StatusOK, result)
}

// chatOptionsFromRequest maps the API request flags to runner chat options.
func chatOptionsFromRequest(req *ChatRequest) []agent.ChatOption {
	var opts []agent.ChatOption
	if req.ConfirmBeforeExecute {
		opts = append(opts, agent.WithConfirmBeforeExecute())
	}
	return opts
}

// chatStream handles SSE streaming for chat responses.
// It always uses the non-streaming Chat() to ensure complete results with events,
// interrupts, and run metadata, then sends the answer as SSE events.
// The SteppedRunner-based Chat() is the only execution path that correctly
// records events, handles HITL interrupts, and provides runId for follow-up.
func (h *AgentHandler) chatStream(w http.ResponseWriter, r *http.Request, ac *auth.AuthContext, req *ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Progress is written to the response while the run executes. The sink runs
	// on this same goroutine (ChatContext is synchronous), so no locking is
	// needed around the ResponseWriter.
	sink := &progressSink{w: w, flusher: flusher, clientGone: r.Context().Done()}

	// Always use Chat() — it goes through SteppedRunner which records events,
	// handles interrupts, and provides complete results.
	opts := append(chatOptionsFromRequest(req), agent.WithProgressSink(sink))
	result := h.runner.ChatContext(r.Context(), ac, req.ThreadID, req.Message, opts...)

	// Fetch current memory
	memEntries, _ := h.memorySvc.ListPreferences(r.Context(), ac.UserID)

	if result.Status == "interrupted" {
		// Send interrupt event
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", jsonEncode(map[string]any{
			"status":        "interrupted",
			"answer":        result.Answer,
			"threadId":      req.ThreadID,
			"interrupt":     result.Interrupt,
			"memory":        memEntries,
			"runId":         result.RunID,
			"events":        result.Events,
			"contextTokens": result.ContextTokens,
			"actualTokens":  result.ActualTokens,
		}))
		flusher.Flush()
		return
	}

	// The answer was already streamed fragment by fragment when a sink was
	// active; resending it whole would make the client append it twice. Failed
	// and cancelled runs carry an error message in Answer, which belongs in the
	// done frame only — sending it as content would render it as an answer.
	if result.Answer != "" && sink.deltas == 0 && result.Status == agent.StatusCompleted {
		fmt.Fprintf(w, "event: chunk\ndata: %s\n\n", jsonEncode(map[string]string{
			"content": result.Answer,
		}))
		flusher.Flush()
	}

	fmt.Fprintf(w, "event: done\ndata: %s\n\n", jsonEncode(map[string]any{
		"status":        result.Status,
		"answer":        result.Answer,
		"threadId":      req.ThreadID,
		"memory":        memEntries,
		"runId":         result.RunID,
		"events":        result.Events,
		"contextTokens": result.ContextTokens,
		"actualTokens":  result.ActualTokens,
		"streamed":      sink.deltas > 0,
	}))
	flusher.Flush()
}

// progressSink forwards run progress to an SSE response. Every callback runs on
// the goroutine executing the run, so writes are naturally serialized.
type progressSink struct {
	w       http.ResponseWriter
	flusher http.Flusher
	// clientGone is closed when the caller's connection drops. Writes after that
	// are pointless — and on the resume path the run deliberately keeps going, so
	// the sink must not keep writing to a dead connection.
	clientGone <-chan struct{}
	deltas     int // content fragments already sent, to avoid resending the answer
}

// disconnected reports whether the client has already gone away.
func (s *progressSink) disconnected() bool {
	if s.clientGone == nil {
		return false
	}
	select {
	case <-s.clientGone:
		return true
	default:
		return false
	}
}

func (s *progressSink) OnEvent(event agent.Event) {
	if s.disconnected() {
		return
	}
	name, payload, ok := sseFrameForEvent(event)
	if !ok {
		return
	}
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, jsonEncode(payload))
	s.flusher.Flush()
}

func (s *progressSink) OnDelta(content string) {
	if s.disconnected() {
		return
	}
	s.deltas++
	fmt.Fprintf(s.w, "event: chunk\ndata: %s\n\n", jsonEncode(map[string]string{"content": content}))
	s.flusher.Flush()
}

// sseFrameForEvent maps an execution event to the frame the UI renders in the
// chat area. Model-call and compression events stay in the events panel (sent
// with the final payload) so the chat does not fill with noise.
func sseFrameForEvent(event agent.Event) (string, map[string]any, bool) {
	// Prefer the structured metadata, falling back to the event's own fields.
	target := event.ToolName
	if name, ok := event.Metadata["tool"].(string); ok && name != "" {
		target = name
	} else if name, ok := event.Metadata["agent"].(string); ok && name != "" {
		target = name
	} else if target == "" {
		target = event.AgentName
	}

	switch event.Type {
	case agent.EventSupervisorRoute:
		return "tool_call", map[string]any{"phase": "route", "tool": target, "detail": event.Detail}, true
	case agent.EventToolCallStart:
		return "tool_call", map[string]any{"phase": "start", "tool": target, "detail": event.Detail}, true
	case agent.EventToolCallEnd:
		return "tool_call", map[string]any{"phase": "end", "tool": target, "detail": event.Detail, "result": event.Metadata["result"]}, true
	case agent.EventACLDenied:
		return "tool_call", map[string]any{"phase": "denied", "tool": target, "detail": event.Detail}, true
	case agent.EventHITLInterrupt:
		return "tool_call", map[string]any{"phase": "approval", "tool": target, "detail": event.Detail}, true
	}
	return "", nil, false
}

// GetRunEvents handles GET /api/agent/runs/{runId}/events
func (h *AgentHandler) GetRunEvents(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	runID := extractPathSuffix(r.URL.Path, "/api/agent/runs/")
	runID = trimSuffix(runID, "/events")

	result, ok, err := h.runner.GetRunContext(r.Context(), runID, ac.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取运行事件失败")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	writeJSON(w, http.StatusOK, result.Events)
}

// GetThreadTokens handles GET /api/chat/{threadId}/tokens
// Returns the thread's own context usage (current/threshold/max) so the UI
// token bar reflects the selected conversation immediately on switch.
func (h *AgentHandler) GetThreadTokens(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	threadID := extractPathSuffix(r.URL.Path, "/api/chat/")
	threadID = trimSuffix(threadID, "/tokens")
	if threadID == "" {
		writeError(w, http.StatusBadRequest, "threadId is required")
		return
	}

	info := h.runner.ThreadTokenInfo(ac.UserID, ac.Roles, threadID)
	if info == nil {
		writeError(w, http.StatusInternalServerError, "token counter unavailable")
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// ResumeRequest is the request body for POST /api/agent/resume
type ResumeRequest struct {
	InterruptID string `json:"interrupt_id"`
	Approved    bool   `json:"approved"`
	Reason      string `json:"reason,omitempty"`
}

// Resume handles POST /api/agent/resume
// Resumes an interrupted run after an approval decision.
func (h *AgentHandler) Resume(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req ResumeRequest
	if !decodeBody(w, r, &req) {
		return
	}

	if req.InterruptID == "" {
		writeError(w, http.StatusBadRequest, "interrupt_id is required")
		return
	}

	decision := hitl.ApprovalDecision{
		Approved: req.Approved,
		Reason:   req.Reason,
	}

	result := h.runner.ResumeContext(r.Context(), ac, req.InterruptID, decision)

	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":    result.RunID,
		"status":    result.Status,
		"answer":    result.Answer,
		"interrupt": result.Interrupt,
		"events":    result.Events,
	})
}

// GetThreadMessages handles GET /api/chat/{threadId}/messages
// Threads are namespaced per user — only the owning user's thread is visible.
func (h *AgentHandler) GetThreadMessages(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	threadID := extractPathSuffix(r.URL.Path, "/api/chat/")
	threadID = trimSuffix(threadID, "/messages")

	if threadID == "" {
		writeError(w, http.StatusBadRequest, "threadId is required")
		return
	}

	threadMessages, err := h.runner.GetThreadMessagesContext(r.Context(), ac.UserID, threadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load thread")
		return
	}
	messages := make([]map[string]any, 0, len(threadMessages))
	for _, message := range threadMessages {
		messages = append(messages, map[string]any{"role": string(message.Role), "content": message.Content})
	}
	if messages == nil {
		messages = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, messages)
}

// ListThreads handles GET /api/chat/threads
func (h *AgentHandler) ListThreads(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	threads, err := h.runner.ListThreadsContext(r.Context(), ac.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list threads")
		return
	}
	if threads == nil {
		threads = []string{}
	}
	writeJSON(w, http.StatusOK, threads)
}

// CreateThread handles POST /api/chat/threads
func (h *AgentHandler) CreateThread(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		ThreadID string `json:"threadId"`
	}
	// This used to ignore the decode error, so a malformed body silently fell
	// through to the generated-id branch and created a thread nobody asked for.
	if !decodeBody(w, r, &req) {
		return
	}
	if req.ThreadID == "" {
		req.ThreadID = "t_" + randomID()
	}
	if err := h.runner.CreateThreadContext(r.Context(), ac.UserID, req.ThreadID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"threadId": req.ThreadID})
}

// DeleteThread handles DELETE /api/chat/{threadId}
// Only the owning user's thread can be deleted; other users' threads
// (or unknown IDs) return 404.
func (h *AgentHandler) DeleteThread(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	threadID := extractPathSuffix(r.URL.Path, "/api/chat/")
	threadID = trimSuffix(threadID, "/delete")

	if threadID == "" {
		writeError(w, http.StatusBadRequest, "threadId is required")
		return
	}

	deleted, err := h.runner.DeleteThreadContext(r.Context(), ac.UserID, threadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "thread not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "threadId": threadID})
}

// Helper: safe JSON encoding for SSE data
func jsonEncode(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Ensure schema is imported
var _ *schema.Message
