package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/example/agent-eino-demo/web"
)

const defaultReadinessTimeout = 2 * time.Second

// ReadinessChecker reports whether dependencies required to serve traffic are
// currently available.
type ReadinessChecker interface {
	Ready(context.Context) error
}

type HealthHandler struct {
	checker ReadinessChecker
	timeout time.Duration
}

func NewHealthHandler(checker ReadinessChecker) *HealthHandler {
	return &HealthHandler{checker: checker, timeout: defaultReadinessTimeout}
}

func (h *HealthHandler) Live(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// The asset version rides along here because /healthz is unauthenticated and
	// already exists: an open tab polls it to notice that the server has been
	// upgraded underneath it, which needs no new route.
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"assets": web.AssetVersion(),
	})
}

func (h *HealthHandler) Ready(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if h.checker != nil {
		ctx, cancel := context.WithTimeout(req.Context(), h.timeout)
		defer cancel()
		if err := h.checker.Ready(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
