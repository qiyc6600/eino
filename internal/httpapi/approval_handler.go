package httpapi

import (
	"fmt"
	"net/http"

	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
)

// ApprovalHandler handles HITL approval API endpoints.
type ApprovalHandler struct {
	hitlSvc *hitl.Service
	runner  *agent.Runner
}

// NewApprovalHandler creates a new ApprovalHandler.
func NewApprovalHandler(hitlSvc *hitl.Service, runner *agent.Runner) *ApprovalHandler {
	return &ApprovalHandler{hitlSvc: hitlSvc, runner: runner}
}

// ListApprovals handles GET /api/approvals
func (h *ApprovalHandler) ListApprovals(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	pending := h.hitlSvc.ListPending(r.Context(), ac.UserID)
	for _, req := range pending {
		req.State = nil
		req.Result = nil
	}
	writeJSON(w, http.StatusOK, pending)
}

// GetApproval handles GET /api/approvals/{interruptId}
// Only the requesting user can see the approval; other users' approvals
// return 404 so interrupt IDs are not confirmed to exist cross-user.
func (h *ApprovalHandler) GetApproval(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	interruptID := extractPathSuffix(r.URL.Path, "/api/approvals/")

	req, ok := h.hitlSvc.GetApproval(interruptID)
	if !ok || req.UserID != ac.UserID {
		writeError(w, http.StatusNotFound, "approval not found")
		return
	}

	req.State = nil
	req.Result = nil
	writeJSON(w, http.StatusOK, req)
}

// DecisionRequest is the request body for POST /api/approvals/{interruptId}/decision
type DecisionRequest struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
	// Stream reports progress over SSE instead of waiting for one JSON response.
	// Off by default: the JSON shape is what existing clients rely on.
	Stream bool `json:"stream,omitempty"`
}

// MakeDecision handles POST /api/approvals/{interruptId}/decision
func (h *ApprovalHandler) MakeDecision(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	interruptID := extractPathSuffix(r.URL.Path, "/api/approvals/")
	interruptID = trimSuffix(interruptID, "/decision")

	var req DecisionRequest
	if !decodeBody(w, r, &req) {
		return
	}

	decision := hitl.ApprovalDecision{
		Approved: req.Approved,
		Reason:   req.Reason,
	}

	if req.Stream {
		h.decideStream(w, r, ac, interruptID, decision)
		return
	}

	// Resume the interrupted run: this applies the approval decision internally,
	// executes the gated tool (if approved), and re-enters the ReAct loop so the
	// LLM continues reasoning from where it paused. The returned answer reflects
	// the tool execution result, not a hardcoded message.
	result := h.runner.ResumeContext(r.Context(), ac, interruptID, decision)
	if result.Status == "error" {
		writeError(w, http.StatusBadRequest, result.Answer)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"runId":     result.RunID,
		"status":    result.Status,
		"answer":    result.Answer,
		"approved":  decision.Approved,
		"interrupt": result.Interrupt,
		"events":    result.Events,
	})
}

// decideStream reports the resume over SSE. A resume runs the gated tool and then
// continues the ReAct loop, so it can take as long as a chat turn and can
// interrupt again — none of which the one-shot JSON response could show.
//
// Errors after the headers are sent are reported through the done frame's status
// rather than an HTTP status code, which is the same trade the chat stream makes.
func (h *ApprovalHandler) decideStream(w http.ResponseWriter, r *http.Request, ac *auth.AuthContext, interruptID string, decision hitl.ApprovalDecision) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sink := &progressSink{w: w, flusher: flusher, clientGone: r.Context().Done()}
	result := h.runner.ResumeContext(r.Context(), ac, interruptID, decision, agent.WithProgressSink(sink))

	fmt.Fprintf(w, "event: done\ndata: %s\n\n", jsonEncode(map[string]any{
		"status":    result.Status,
		"answer":    result.Answer,
		"runId":     result.RunID,
		"approved":  decision.Approved,
		"interrupt": result.Interrupt,
		"events":    result.Events,
		"streamed":  sink.deltas > 0,
	}))
	flusher.Flush()
}
