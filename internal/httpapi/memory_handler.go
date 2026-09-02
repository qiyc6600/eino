package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/memory"
)

// MemoryHandler handles memory API endpoints.
type MemoryHandler struct {
	memSvc *memory.Service
}

// NewMemoryHandler creates a new MemoryHandler.
func NewMemoryHandler(memSvc *memory.Service) *MemoryHandler {
	return &MemoryHandler{memSvc: memSvc}
}

// ListMemory handles GET /api/memory
func (h *MemoryHandler) ListMemory(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	entries, err := h.memSvc.ListPreferences(r.Context(), ac.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, entries)
}

// PutMemoryRequest is the request body for POST /api/memory
type PutMemoryRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// PutMemory handles POST /api/memory
func (h *MemoryHandler) PutMemory(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req PutMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.memSvc.PutPreference(r.Context(), ac.UserID, req.Key, req.Value); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteMemory handles DELETE /api/memory/{key}
func (h *MemoryHandler) DeleteMemory(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	key := extractPathSuffix(r.URL.Path, "/api/memory/")

	if err := h.memSvc.DeletePreference(r.Context(), ac.UserID, key); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ConsolidateMemory handles POST /api/memory/consolidate
// Runs one memory-lifecycle pass: archives stale low-value entries and
// consolidates the remaining ones into a user profile (LLM when available,
// rule-based fallback otherwise).
func (h *MemoryHandler) ConsolidateMemory(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	result, err := h.memSvc.Consolidate(r.Context(), ac.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}
