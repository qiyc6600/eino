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

// approvalView is an ApprovalRequest plus the display label for whatever it
// interrupts.
//
// The embedded struct's fields marshal inline (it carries no JSON tags, so the
// wire format is unchanged), and Label is added alongside them. Resolving the
// label here rather than in the page means one table of names: the page cannot
// label the same tool differently from the chat progress line, and it does not
// need to know that a node interrupt is called "plan_review" internally.
type approvalView struct {
	*hitl.ApprovalRequest
	Label string
}

func toApprovalViews(reqs []*hitl.ApprovalRequest) []approvalView {
	views := make([]approvalView, 0, len(reqs))
	for _, req := range reqs {
		// State and Result are large and irrelevant to a list row.
		req.State = nil
		req.Result = nil
		views = append(views, approvalView{
			ApprovalRequest: req,
			Label:           agent.DisplayLabelFor(agent.InterruptTargetName(req.ToolName, req.NodeName)),
		})
	}
	return views
}

// ListApprovals handles GET /api/approvals
func (h *ApprovalHandler) ListApprovals(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	pending := h.hitlSvc.ListPending(r.Context(), ac.UserID)
	writeJSON(w, http.StatusOK, toApprovalViews(pending))
}

// ListHistory handles GET /api/approvals/history.
//
// The pending list empties as approvals are handled, which left the panel with
// nothing to show the moment a decision was made. This is the other half: what was
// decided, newest first, so the panel stays a record rather than a to-do list that
// forgets.
func (h *ApprovalHandler) ListHistory(w http.ResponseWriter, req *http.Request) {
	ac := auth.FromContext(req.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	decided, err := h.hitlSvc.ListDecided(req.Context(), ac.UserID, approvalHistoryLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toApprovalViews(decided))
}

// approvalHistoryLimit bounds the history: it is a record of recent activity, not
// an audit log, and an unbounded list would grow with every approval ever made.
const approvalHistoryLimit = 20

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
	writeJSON(w, http.StatusOK, toApprovalViews([]*hitl.ApprovalRequest{req})[0])
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
