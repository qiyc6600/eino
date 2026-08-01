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
	ac.ThreadID = req.ThreadID

	if req.Stream {
		h.chatStream(w, r, ac, &req)
		return
	}

	// Non-streaming: original behavior
	result := h.runner.Chat(ac, req.ThreadID, req.Message)
	writeJSON(w, http.StatusOK, result)
}

// chatStream handles SSE streaming for chat responses.
// It streams message chunks as Server-Sent Events, then sends a final [DONE] event.
// When the LLM needs to call tools (ReAct loop), Eino's Stream() only returns the
// first turn. We detect this and fall back to non-streaming Chat() to get the
// complete result, then send it as a single chunk.
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

	stream, err := h.runner.ChatStream(ac, req.ThreadID, req.Message)
	if err != nil {
		// Send error as SSE event
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonEncode(map[string]string{"error": err.Error()}))
		flusher.Flush()
		return
	}
	defer stream.Close()

	var fullContent string
	hasToolCalls := false

	for {
		chunk, err := stream.Recv()
		if err != nil {
			// Stream ended (io.EOF) or error
			break
		}

		if chunk.Content != "" {
			fullContent += chunk.Content
			// Send content chunk as SSE event
			fmt.Fprintf(w, "event: chunk\ndata: %s\n\n", jsonEncode(map[string]string{
				"content": chunk.Content,
			}))
			flusher.Flush()
		}

		// If there are tool calls, the ReAct loop hasn't finished.
		// Eino's Stream() only streams the first turn, so we need to
		// fall back to non-streaming to get the complete result.
		if len(chunk.ToolCalls) > 0 {
			hasToolCalls = true
			for _, tc := range chunk.ToolCalls {
				fmt.Fprintf(w, "event: tool_call\ndata: %s\n\n", jsonEncode(map[string]any{
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				}))
				flusher.Flush()
			}
		}
	}

	// Extract preferences asynchronously (don't block the response)
	go h.runner.ExtractAndSavePreferences(ac, req.Message)

	// Fetch current memory for the done event
	memEntries, _ := h.memorySvc.ListPreferences(r.Context(), ac.UserID)

	// If tool calls were detected, the stream only contains the first LLM turn.
	// Fall back to non-streaming Chat() to get the complete ReAct result.
	if hasToolCalls {
		result := h.runner.Chat(ac, req.ThreadID, req.Message)

		if result.Status == "interrupted" {
			fmt.Fprintf(w, "event: done\ndata: %s\n\n", jsonEncode(map[string]any{
				"status":    "interrupted",
				"answer":    result.Answer,
				"threadId":  req.ThreadID,
				"interrupt": result.Interrupt,
				"memory":    memEntries,
			}))
			flusher.Flush()
			return
		}

		// Send the complete answer as a single chunk
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
		}))
		flusher.Flush()
		return
	}

	// No tool calls — the stream has the complete answer
	// Save the full assistant message to thread history
	h.runner.SaveAssistantMessage(req.ThreadID, fullContent)

	// Send done event with full answer and memory
	fmt.Fprintf(w, "event: done\ndata: %s\n\n", jsonEncode(map[string]any{
		"status":   "completed",
		"answer":   fullContent,
		"threadId": req.ThreadID,
		"memory":   memEntries,
	}))
	flusher.Flush()
}

// GetRunEvents handles GET /api/agent/runs/{runId}/events
func (h *AgentHandler) GetRunEvents(w http.ResponseWriter, r *http.Request) {
	runID := extractPathSuffix(r.URL.Path, "/api/agent/runs/")
	runID = trimSuffix(runID, "/events")

	result, ok := h.runner.GetRun(runID)
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
func (h *AgentHandler) GetThreadMessages(w http.ResponseWriter, r *http.Request) {
	threadID := extractPathSuffix(r.URL.Path, "/api/chat/")
	threadID = trimSuffix(threadID, "/messages")

	if threadID == "" {
		writeError(w, http.StatusBadRequest, "threadId is required")
		return
	}

	messages := h.runner.ThreadMessagesJSON(threadID)
	if messages == nil {
		messages = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, messages)
}

// ListThreads handles GET /api/chat/threads
func (h *AgentHandler) ListThreads(w http.ResponseWriter, r *http.Request) {
	threads := h.runner.ListThreads()
	if threads == nil {
		threads = []string{}
	}
	writeJSON(w, http.StatusOK, threads)
}

// CreateThread handles POST /api/chat/threads
func (h *AgentHandler) CreateThread(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ThreadID string `json:"threadId"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.ThreadID == "" {
		req.ThreadID = "t_" + randomID()
	}
	h.runner.CreateThread(req.ThreadID)
	writeJSON(w, http.StatusCreated, map[string]string{"threadId": req.ThreadID})
}

// DeleteThread handles DELETE /api/chat/{threadId}
func (h *AgentHandler) DeleteThread(w http.ResponseWriter, r *http.Request) {
	threadID := extractPathSuffix(r.URL.Path, "/api/chat/")
	threadID = trimSuffix(threadID, "/delete")

	if threadID == "" {
		writeError(w, http.StatusBadRequest, "threadId is required")
		return
	}

	h.runner.DeleteThread(threadID)
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
