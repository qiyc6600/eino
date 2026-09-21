package httpapi

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/agent-eino-demo/internal/auth"
)

// AuthHandler handles authentication API endpoints.
type AuthHandler struct {
	authSvc *auth.Service
	cookie  SessionCookieConfig
}

// SessionCookieConfig configures the browser session cookie.
type SessionCookieConfig struct {
	// TTL mirrors the session lifetime so the cookie expires with the session.
	TTL time.Duration
	// Secure must be false when serving plain HTTP on a non-localhost address,
	// where browsers drop Secure cookies and every request looks unauthenticated.
	Secure bool
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(authSvc *auth.Service, cookie SessionCookieConfig) *AuthHandler {
	return &AuthHandler{authSvc: authSvc, cookie: cookie}
}

// Login handles POST /api/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req auth.LoginRequest
	if !decodeBody(w, r, &req) {
		return
	}

	source, _, _ := net.SplitHostPort(r.RemoteAddr)
	resp, err := h.authSvc.LoginWithSource(r.Context(), req.Username, req.Password, source)
	if err != nil {
		var limited *auth.LoginRateLimitError
		if errors.As(err, &limited) {
			seconds := int64((limited.RetryAfter + time.Second - 1) / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			writeError(w, http.StatusTooManyRequests, auth.ErrLoginRateLimited.Error())
		} else if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, auth.ErrInvalidCredentials.Error())
		} else {
			writeError(w, http.StatusServiceUnavailable, "login temporarily unavailable")
		}
		return
	}

	// The cookie is what keeps a browser signed in across reloads; sessionId
	// stays in the body so non-browser clients can keep using the header.
	auth.SetSessionCookie(w, resp.SessionID, h.cookie.TTL, h.cookie.Secure)
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
	auth.ClearSessionCookie(w, h.cookie.Secure)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ListUsers handles GET /api/users
func (h *AuthHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.authSvc.ListUsersE(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// CreateUser handles POST /api/users
func (h *AuthHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		Roles    []string `json:"roles"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	user, err := h.authSvc.CreateUser(r.Context(), req.Username, req.Password, req.Roles)
	if err != nil {
		if errors.Is(err, auth.ErrUserExists) {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to create user")
		}
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

// ListRoles handles GET /api/roles
func (h *AuthHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles := h.authSvc.RBAC().ListRoles()
	type rolePublic struct {
		Name        string            `json:"name"`
		Permissions []auth.Permission `json:"permissions"`
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
	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.authSvc.UpdateUserRoles(r.Context(), userID, req.Roles); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to update user roles")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
