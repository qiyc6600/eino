package auth

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// SessionCookieName is the cookie carrying the session ID for browser clients.
// It is HttpOnly so page scripts cannot read the credential, which is what lets
// the SPA survive a reload without keeping the token in JS-reachable storage.
const SessionCookieName = "agent_session"

// AuthMiddleware validates the session on every request and injects AuthContext.
func AuthMiddleware(svc *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sessionID := extractSessionID(r)
			if sessionID == "" {
				http.Error(w, `{"error":"unauthorized: missing session"}`, http.StatusUnauthorized)
				return
			}

			session, err := svc.ValidateSession(r.Context(), sessionID)
			if err != nil {
				http.Error(w, `{"error":"unauthorized: invalid session"}`, http.StatusUnauthorized)
				return
			}

			authCtx := &AuthContext{
				SessionID: session.ID,
				UserID:    session.UserID,
				Username:  session.Username,
				Roles:     session.Roles,
			}

			// Inject AuthContext into request context
			ctx := WithAuthContext(r.Context(), authCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractSessionID reads the session ID from the Authorization header, falling
// back to the session cookie.
//
// The header wins when both are present so non-browser clients keep working
// unchanged. Query parameters are deliberately NOT accepted: credentials in URLs
// leak into browser history, access logs and Referer headers.
func extractSessionID(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		return cookie.Value
	}
	return ""
}

// SetSessionCookie hands the session to a browser as an HttpOnly cookie.
//
// SameSite=Strict is enough CSRF cover here because the SPA is served from the
// same origin and no cross-site navigation needs the cookie. Secure must be off
// when the demo is served over plain HTTP on a non-localhost address, otherwise
// browsers silently drop the cookie and every request looks unauthenticated.
func SetSessionCookie(w http.ResponseWriter, sessionID string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

// ClearSessionCookie expires the session cookie on logout.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// ContextWithAuth is a helper for tests to inject AuthContext into context.
func ContextWithAuth(ctx context.Context, ac *AuthContext) context.Context {
	return WithAuthContext(ctx, ac)
}
