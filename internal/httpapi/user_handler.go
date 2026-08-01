package httpapi

import (
	"net/http"

	"github.com/example/agent-eino-demo/internal/tools"
)

// UserHandler handles user and tool management API endpoints.
type UserHandler struct {
	registry *tools.ToolRegistry
}

// NewUserHandler creates a new UserHandler.
func NewUserHandler(registry *tools.ToolRegistry) *UserHandler {
	return &UserHandler{registry: registry}
}

// ListTools handles GET /api/tools
func (h *UserHandler) ListTools(w http.ResponseWriter, r *http.Request) {
	toolList := h.registry.List()
	type toolInfo struct {
		Name             string            `json:"name"`
		Description      string            `json:"description"`
		RiskLevel        string            `json:"risk_level"`
		RequiresApproval bool              `json:"requires_approval"`
		ParamSchema      string            `json:"param_schema,omitempty"`
	}

	result := make([]toolInfo, 0, len(toolList))
	for _, t := range toolList {
		result = append(result, toolInfo{
			Name:             t.Meta.Name,
			Description:      t.Meta.Description,
			RiskLevel:        string(t.Meta.RiskLevel),
			RequiresApproval: t.Meta.RequiresApproval,
			ParamSchema:      t.Meta.ParamSchema,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

// GetTool handles GET /api/tools/{toolName}
func (h *UserHandler) GetTool(w http.ResponseWriter, r *http.Request) {
	toolName := extractPathSuffix(r.URL.Path, "/api/tools/")

	t, ok := h.registry.Get(toolName)
	if !ok {
		writeError(w, http.StatusNotFound, "tool not found: "+toolName)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":              t.Meta.Name,
		"description":       t.Meta.Description,
		"risk_level":        string(t.Meta.RiskLevel),
		"requires_approval": t.Meta.RequiresApproval,
		"param_schema":      t.Meta.ParamSchema,
	})
}
