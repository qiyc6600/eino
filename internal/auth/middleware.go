package auth

import (
	"context"
	"net/http"
	"strings"
)

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

// extractSessionID reads the session ID from the Authorization header.
// Query parameters are deliberately NOT accepted: credentials in URLs leak
// into browser history, access logs and Referer headers.
func extractSessionID(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	return ""
}

// ContextWithAuth is a helper for tests to inject AuthContext into context.
func ContextWithAuth(ctx context.Context, ac *AuthContext) context.Context {
	return WithAuthContext(ctx, ac)
}
