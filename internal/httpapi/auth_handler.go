package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/example/agent-eino-demo/internal/auth"
)

// AuthHandler handles authentication API endpoints.
type AuthHandler struct {
	authSvc *auth.Service
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(authSvc *auth.Service) *AuthHandler {
	return &AuthHandler{authSvc: authSvc}
}

// Login handles POST /api/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req auth.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.authSvc.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// Me handles GET /api/auth/me
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Current sliding expiration deadline (empty for pre-TTL sessions).
	expiresAt, _ := h.authSvc.SessionExpiry(r.Context(), ac.SessionID)

	resp := map[string]any{
		"sessionId": ac.SessionID,
		"user": auth.UserPublic{
			ID:       ac.UserID,
			Username: ac.Username,
			Roles:    ac.Roles,
		},
		"tools": h.authSvc.RBAC().GetToolsForRoles(ac.Roles),
	}
	if !expiresAt.IsZero() {
		resp["expiresAt"] = expiresAt
		resp["sessionTTL"] = h.authSvc.SessionTTL().String()
	}
	writeJSON(w, http.StatusOK, resp)
}

// Logout handles POST /api/auth/logout
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	ac := auth.FromContext(r.Context())
	if ac != nil {
		h.authSvc.Logout(r.Context(), ac.SessionID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ListUsers handles GET /api/users
func (h *AuthHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users := h.authSvc.ListUsers(r.Context())
	writeJSON(w, http.StatusOK, users)
}

// CreateUser handles POST /api/users
func (h *AuthHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		Roles    []string `json:"roles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, err := h.authSvc.CreateUser(r.Context(), req.Username, req.Password, req.Roles)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

// ListRoles handles GET /api/roles
func (h *AuthHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles := h.authSvc.RBAC().ListRoles()
	type rolePublic struct {
		Name        string              `json:"name"`
		Permissions []auth.Permission   `json:"permissions"`
	}
	result := make([]rolePublic, 0, len(roles))
	for _, r := range roles {
		result = append(result, rolePublic{Name: r.Name, Permissions: r.Permissions})
	}
	writeJSON(w, http.StatusOK, result)
}

// UpdateUserRoles handles PUT /api/users/{userId}/roles
func (h *AuthHandler) UpdateUserRoles(w http.ResponseWriter, r *http.Request) {
	userID := extractPathSuffix(r.URL.Path, "/api/users/")
	userID = strings.TrimSuffix(userID, "/roles")

	var req struct {
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.authSvc.UpdateUserRoles(r.Context(), userID, req.Roles); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
