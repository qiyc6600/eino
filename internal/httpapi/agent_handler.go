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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
	result := h.runner.Chat(ac, req.ThreadID, req.Message, chatOptionsFromRequest(&req)...)
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

	// Always use Chat() — it goes through SteppedRunner which records events,
	// handles interrupts, and provides complete results.
	result := h.runner.Chat(ac, req.ThreadID, req.Message, chatOptionsFromRequest(req)...)

	// Extract preferences asynchronously
	go h.runner.ExtractAndSavePreferences(ac, req.Message)

	// Fetch current memory
	memEntries, _ := h.memorySvc.ListPreferences(r.Context(), ac.UserID)

	if result.Status == "interrupted" {
		// Send interrupt event
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", jsonEncode(map[string]any{
			"status":    "interrupted",
			"answer":    result.Answer,
			"threadId":  req.ThreadID,
			"interrupt": result.Interrupt,
			"memory":    memEntries,
			"runId":     result.RunID,
			"events":    result.Events,
				"contextTokens": result.ContextTokens,
		}))
		flusher.Flush()
		return
	}

	// Send the complete answer as a single chunk (no streaming typing effect,
	// but ensures events, memory, and interrupt handling are all correct).
	if result.Answer != "" {
		fmt.Fprintf(w, "event: chunk\ndata: %s\n\n", jsonEncode(map[string]string{
			"content": result.Answer,
		}))
		flusher.Flush()
	}

	fmt.Fprintf(w, "event: done\ndata: %s\n\n", jsonEncode(map[string]any{
		"status":   "completed",
		"answer":   result.Answer,
		"threadId": req.ThreadID,
		"memory":   memEntries,
		"runId":    result.RunID,
		"events":   result.Events,
		"contextTokens": result.ContextTokens,
	}))
	flusher.Flush()
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

	result, ok := h.runner.GetRun(runID, ac.UserID)
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	writeJSON(w, http.StatusOK, result.Events)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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

	result := h.runner.Resume(ac, req.InterruptID, decision)

	writeJSON(w, http.StatusOK, map[string]any{
		"run_id": result.RunID,
		"status": result.Status,
		"answer": result.Answer,
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

	messages := h.runner.ThreadMessagesJSON(ac.UserID, threadID)
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

	threads := h.runner.ListThreads(ac.UserID)
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
	json.NewDecoder(r.Body).Decode(&req)
	if req.ThreadID == "" {
		req.ThreadID = "t_" + randomID()
	}
	h.runner.CreateThread(ac.UserID, req.ThreadID)
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

	if !h.runner.DeleteThread(ac.UserID, threadID) {
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
