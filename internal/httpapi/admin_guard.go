package httpapi

import (
	"net/http"

	"github.com/example/agent-eino-demo/internal/auth"
)

// requireAdmin gates an endpoint on the admin role.
//
// Until this existed the administrative endpoints were authenticated but not
// authorised: /api/users accepted a POST that creates a user with any roles, so
// any signed-in account — including a visitor — could mint an administrator or
// promote itself. The tool-level ACL was enforced, which made the gap easy to
// miss: the framework was protecting what the model may call, while the routes
// that hand out roles were open.
//
// The check is on the session's roles, which ValidateSession re-reads from the
// store on every request, so a demotion takes effect on the next call rather than
// at the next login.
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac := auth.FromContext(r.Context())
		if ac == nil {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		if !auth.HasRole(ac.Roles, "admin") {
			// 403 rather than 404: the caller is authenticated, and the endpoint's
			// existence is not a secret — only the permission to use it is missing.
			writeError(w, http.StatusForbidden, "administrator role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
