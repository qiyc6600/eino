package httpapi

import (
	"encoding/json"
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
	writeJSON(w, http.StatusOK, pending)
}

// GetApproval handles GET /api/approvals/{interruptId}
func (h *ApprovalHandler) GetApproval(w http.ResponseWriter, r *http.Request) {
	interruptID := extractPathSuffix(r.URL.Path, "/api/approvals/")

	req, ok := h.hitlSvc.GetApproval(interruptID)
	if !ok {
		writeError(w, http.StatusNotFound, "approval not found")
		return
	}

	writeJSON(w, http.StatusOK, req)
}

// DecisionRequest is the request body for POST /api/approvals/{interruptId}/decision
type DecisionRequest struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	decision := hitl.ApprovalDecision{
		Approved: req.Approved,
		Reason:   req.Reason,
	}

	// Resume the interrupted run: this applies the approval decision internally,
	// executes the gated tool (if approved), and re-enters the ReAct loop so the
	// LLM continues reasoning from where it paused. The returned answer reflects
	// the tool execution result, not a hardcoded message.
	result := h.runner.Resume(ac, interruptID, decision)
	if result.Status == "error" {
		writeError(w, http.StatusBadRequest, result.Answer)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"runId":    result.RunID,
		"status":   result.Status,
		"answer":   result.Answer,
		"approved": decision.Approved,
	})
}
