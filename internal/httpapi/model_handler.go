package httpapi

import (
	"net/http"
)

// ModelProfileInfo describes a model profile for the API response.
type ModelProfileInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	NeedsAPIKey bool   `json:"needs_api_key"`
	HasAPIKey   bool   `json:"has_api_key"`
}

// ModelSwitcher is the interface that the App must satisfy for model switching.
// This avoids a circular import between httpapi and app packages.
type ModelSwitcher interface {
	CurrentModel() string
	SwitchModel(profileID, apiKey string) error
	ListModelProfiles() []ModelProfileInfo
}

// ModelHandler handles model switching API endpoints.
type ModelHandler struct {
	switcher ModelSwitcher
}

// NewModelHandler creates a new ModelHandler.
func NewModelHandler(switcher ModelSwitcher) *ModelHandler {
	return &ModelHandler{switcher: switcher}
}

// ListModels handles GET /api/models
// Returns all preset model profiles and the current active model ID.
func (h *ModelHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	profiles := h.switcher.ListModelProfiles()

	writeJSON(w, http.StatusOK, map[string]any{
		"models":        profiles,
		"current_model": h.switcher.CurrentModel(),
	})
}

// SwitchModelRequest is the request body for POST /api/models/switch
type SwitchModelRequest struct {
	ProfileID string `json:"profile_id"`
	APIKey    string `json:"api_key,omitempty"`
}

// SwitchModel handles POST /api/models/switch
// Switches the active model to the specified profile.
func (h *ModelHandler) SwitchModel(w http.ResponseWriter, r *http.Request) {
	var req SwitchModelRequest
	if !decodeBody(w, r, &req) {
		return
	}

	if req.ProfileID == "" {
		writeError(w, http.StatusBadRequest, "profile_id is required")
		return
	}

	if err := h.switcher.SwitchModel(req.ProfileID, req.APIKey); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"current_model": h.switcher.CurrentModel(),
	})
}
