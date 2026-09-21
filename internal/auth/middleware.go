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

// SessionCookieConfig configures the browser session cookie.
type SessionCookieConfig struct {
	// TTL mirrors the session lifetime so the cookie expires with the session.
	TTL time.Duration
	// Secure must be false when serving plain HTTP on a non-localhost address,
	// where browsers drop Secure cookies and every request looks unauthenticated.
	Secure bool
}

// AuthMiddleware validates the session on every request and injects AuthContext.
//
// On success it re-issues the session cookie. The session's expiry slides in the
// store, but a cookie's MaxAge is absolute in the browser: issued once at login,
// it makes the browser drop the credential at a fixed moment regardless of
// activity — which silently overrides the sliding lifetime and logs out a user
// who has been active the whole time. Re-issuing keeps the two in step.
func AuthMiddleware(svc *Service, cookie SessionCookieConfig) func(http.Handler) http.Handler {
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

			// Header first: once the handler writes its body the cookie is too late.
			// Only for cookie-borne sessions — a client using the Authorization
			// header has no cookie to slide.
			if cookie.TTL > 0 && r.Header.Get("Authorization") == "" {
				SetSessionCookie(w, session.ID, cookie.TTL, cookie.Secure)
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
